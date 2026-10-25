// ============ file_echo_pipeline_test.go 职责说明 ============
// DEF-01（2026-10-24 云端 UAT）「指令回显」的**管线集成测**——跑真 HandleFile，不是只测判据函数。
//
// 为什么单测判据不够（UAT 文档点名的缺失腿）：
//
//	判据本体（instruction_echo.go 的 fileTranslationUsable）已有单测，装配层也有源码锁
//	（file_writepoints_guard_test.go 数写入点）。但现网那次坏交付的形态是**三件事同时成立**：
//	① 模型把 system 尾注翻成英文段落；② 六个写入点各自放行了它；③ 硬闸按「键存在=已译出」
//	永久跳过该段，于是 1,037 积分的成品里躺着两段英文「规范说明」而真第 2/3 行没了。
//
// 这三件事里任何一件的单测都测不到「整条链最后交付什么」——只有把一段真文件推过整条链、
//
//	再打开产物读字节，才能证明"回显没进客户手里"这条对外承诺成立。
//
// 五条腿：
//
//	E1 正常腿（带尾注）恒回显 ⇒ 产物**一字不含**回显特征串，四行源文**四行都有**译文；
//	E2 极简指令腿真被走到（由假上游自己读出"这一枪的 system 里没有尾注"）——不是数日志；
//	E3 Data.Untranslated 归零 ⇒ 回显段没有被"键存在"骗过硬闸（RC-2 那条不变量的现网形态）；
//	E4 反证：FILE_ECHO_GUARD=0（应急档）时同一份假上游**必须**把回显写进产物——
//	   证明拦住回显的是判据本身，而不是假上游凑巧没吐回显；
//	E5 反证：把极简腿哑掉（回空）⇒ 产物必须回到"缺行＋Untranslated 非零"的残缺形态，
//	   证明救回这四行的正是那条兜底腿，E1 的绿灯不是批量腿自己给的。
//
// ★ 本批由 E3/E1 换出来的两条产品侧修复（都不是测试自伤）：
//
//	① file.go 硬闸批量重译／逐段兜底／F-01 定向补译三个写入点只 addTrans 不 addModelHit，
//	   于是"初翻全被回显拦下、靠补译救回整篇"的单出 model_hits=0 而成品每行都是模型译文
//	   （首跑读数：ModelHits=0 而其余断言全绿）。现三处补了计数。
//	② instruction_echo.go 的「最长连续命中」地板 12 会误杀含「翻译」的正常短句译文
//	   （归一化后一个 translation 词就够 13 字符），真译文被丢两次 ⇒ 原文当未译出交付。
//	   地板抬到 20，两侧读数由 instruction_echo_overreach_test.go 钉住。
//
// 不落 PostgreSQL；上游是 httptest 假服务器（按 system 里有没有币种尾注分派回复）。
// 运行：go test ./internal/engine/ -run TestFileEchoPipeline
// =============================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
)

// echoSrcLines 四行中文源文（现网 uat_sample.txt 同形态：短句＋一条带数字的长标签行）。
var echoSrcLines = []string{
	"系统自动匹配内置汽车数据库",
	"支持多格式文件翻译",
	"当前：全部允许·仓位留空/0 =使用全局单票金额",
	"请在维护周期内校准设备",
}

// echoGoodTr 每行的**合法**译文（极简指令腿给出的形态）。
var echoGoodTr = []string{
	"The system automatically matches the built-in automotive database",
	"Multi-format file translation is supported",
	"Current: all allowed; position left blank/0 = use the global single-ticket amount",
	"Please calibrate the device within the maintenance cycle",
}

// echoParahrase 现网回显形态：模型把 system 里的中文「金额币种保真」尾注当正文翻成英文。
// 含 instructionEchoMarkers 里的 "currency symbol"，且源文（中文）里必然没有 ⇒ 差分判据命中。
const echoPara = "Amount currency fidelity: when the source amount does not state a currency, " +
	"RMB is the default; the translation must keep the RMB standard (¥/CNY/RMB/yuan) and must not " +
	"replace or convert the currency symbol into R, $, € or £, and the amount values stay unchanged."

// echoFullNoteAnchors 判定"这一枪是正常腿"的锚：两条尾注按界面语言取其一。
var echoFullNoteAnchors = []string{"金额币种保真", "Currency rule:"}

// echoBatchIndices 从批量请求的 user 段里现读全部 <sN> 编号（回显必须逐号回吐，
// 少一号那一段就退化成"空译文"而不是"回显"，正向用例会被判成另一件事）。
func echoBatchIndices(user string) []int {
	var out []int
	seen := map[int]bool{}
	for i := 0; i < len(user); i++ {
		if !strings.HasPrefix(user[i:], "<s") {
			continue
		}
		j := i + 2
		k := j
		for k < len(user) && user[k] >= '0' && user[k] <= '9' {
			k++
		}
		if k == j || k >= len(user) || user[k] != '>' {
			continue
		}
		n := 0
		for _, r := range user[j:k] {
			n = n*10 + int(r-'0')
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
		i = k
	}
	return out
}

// echoHarness 假上游 + 引擎装配，并**按请求形态**记账（正常腿/极简腿/批量/单发）。
type echoHarness struct {
	eng *Engine
	mu  sync.Mutex
	// 四类计数：判据只看"极简腿有没有被走到"，其余用于失败时给人看清链路走到哪一步。
	fullBatch    int
	fullSingle   int
	minimalBatch int
	minimalOne   int
	// muteMinimal＝E4/E5 里的 E5 反证开关：极简腿回空，用来验证「这四行是被兜底腿救回来的」。
	// （E4 不动上游，只把判据本身关掉，两条反证各抓一条腿。）
	muteMinimal bool
}

// echoCounts 取当前四类调用计数快照（并发安全）。
func (h *echoHarness) counts() (fullBatch, fullSingle, minimalBatch, minimalOne int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.fullBatch, h.fullSingle, h.minimalBatch, h.minimalOne
}

// echoIsFullLegPrompt system 里带币种/纯度尾注＝正常腿；不带＝极简指令兜底腿。
// 判据取**指令自身**而不是"第几枪"：调用次数会随批量分块、审校腿、并发数变，
// 而"有没有尾注"正是极简腿与正常腿的唯一区别（见 engine.go 的 minimalInstructionFromCtx 分支）。
func echoIsFullLegPrompt(sys string) bool {
	for _, a := range echoFullNoteAnchors {
		if strings.Contains(sys, a) {
			return true
		}
	}
	return false
}

// echoHandler 按 (腿, 批量/单发) 分派回复：正常腿恒回吐指令段，极简腿给逐行正确译文。
func (h *echoHarness) echoHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &req)
	sys, user := "", ""
	for _, m := range req.Messages {
		if m.Role == "system" {
			sys = m.Content
		} else {
			user = m.Content
		}
	}
	full := echoIsFullLegPrompt(sys)
	batch := strings.Contains(user, "<s1>")

	h.mu.Lock()
	switch {
	case full && batch:
		h.fullBatch++
	case full:
		h.fullSingle++
	case batch:
		h.minimalBatch++
	default:
		h.minimalOne++
	}
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	var content string
	switch {
	case full:
		// 正常腿：批量按 <sN> 逐条回吐同一段指令英文，单发按 <t> 回吐。
		if batch {
			var sb strings.Builder
			for _, n := range echoBatchIndices(user) { // 段号从请求里现读，不猜批大小
				fmt.Fprintf(&sb, "<s%d>%s</s%d>\n", n, echoPara, n)
			}
			content = sb.String()
		} else {
			content = "<t>" + echoPara + "</t>"
		}
	default:
		// ★ E5 反证：哑掉极简腿（这一枪回空）——判据照旧拦回显，但拦下之后没有救援，
		//   产物必须回到"缺行"形态。若这里回空产物却依然齐全，说明四行根本不是这条腿给的。
		if h.muteMinimal {
			if batch {
				content = ""
			} else {
				content = "<t></t>"
			}
			break
		}
		// 极简腿：认出这一枪问的是哪一行源文，回那一行的正确译文。
		if batch {
			var sb strings.Builder
			for i, src := range echoSrcLines {
				if strings.Contains(user, src) {
					fmt.Fprintf(&sb, "<s%d>%s</s%d>\n", i+1, echoGoodTr[i], i+1)
				}
			}
			content = sb.String()
		} else {
			content = "<t>" + echoGoodTr[0] + "</t>"
			for i, src := range echoSrcLines {
				if strings.Contains(user, src) {
					content = "<t>" + echoGoodTr[i] + "</t>"
					break
				}
			}
		}
	}
	// 回复含引号/反斜杠，走 strconv.Quote 而不是裸拼（同 file_writepoints_guard_test.go 口径）。
	fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":"stop"}]}`, quoteJSON(content))
}

// quoteJSON 把一段文本编码成 JSON 字符串字面量（假上游回复体里唯一需要转义的位置）。
func quoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// echoEngine 装配：内存假上游 + fast 模式所需的 Engine。
// 必须走 NewEngine 而不是裸 &Engine{LLM, Cfg}：单发腿（极简指令兜底走的那一条）会读
// e.breaker/errRing 等构造期字段，裸结构体里它们是 nil ⇒ goroutine 直接 panic，
// 被 recoverPipeline 吞成"这一段没译出来"（本批首跑真踩：Files=[] 而零报错行）。
// St/Ten/DB/Index 留 nil：术语/文化/阶段模型自动降级，链路最短。
func echoEngine(t *testing.T) *echoHarness {
	t.Helper()
	h := &echoHarness{}
	srv := httptest.NewServer(http.HandlerFunc(h.echoHandler))
	t.Cleanup(srv.Close)
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言：防 UAT 矩阵的 PG env 泄漏进本包（AGENTS §4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	h.eng = NewEngine(cfg, nil, nil, nil)
	return h
}

// echoRun 将一份四行 txt 推过整条文件管线（fast 模式：无 KB 直配、无审校腿，链路最短且唯一）。
// 返回: 结果与**产物字节**（断言全部对着产物内容，不对着内部 map）。
func echoRun(t *testing.T, h *echoHarness) (*FileTranslateResult, string) {
	t.Helper()
	res, artifact := echoRunRaw(t, h)
	if artifact == "" {
		t.Fatalf("没有任何产物文件（Files=%v）⇒ 交付形态这一腿根本没跑起来", res.Files)
	}
	return res, artifact
}

// TestFileEchoPipelineKeepsEchoOutOfArtifact E1/E2/E3：回显不进产物、极简腿真被走到、漏译归零。
func TestFileEchoPipelineKeepsEchoOutOfArtifact(t *testing.T) {
	h := echoEngine(t)
	res, artifact := echoRun(t, h)

	// 反向断言（本用例的本体）：产物里不许出现回显段落或其任何特征串。
	for _, bad := range []string{"currency symbol", "Amount currency fidelity", "RMB is the default", "人民币"} {
		if strings.Contains(artifact, bad) {
			t.Errorf("产物含指令回显特征串 %q ⇒ 客户拿到的是「规范说明」而不是译文\n产物=%q", bad, artifact)
		}
	}
	// 正向断言：四行各有译文（缺一行＝回显顶掉了真译文，正是现网形态）。
	for i, tr := range echoGoodTr {
		if !strings.Contains(artifact, tr) {
			t.Errorf("产物缺第 %d 行译文 %q ⇒ 该行未交付\n产物=%q", i+1, tr, artifact)
		}
	}
	// 源文不许原样留在产物里（原位写回 txt 的成品只应有译文行）。
	for i, src := range echoSrcLines {
		if strings.Contains(artifact, src) {
			t.Errorf("产物第 %d 行仍是中文原文 %q ⇒ 未译出却被当成已交付", i+1, src)
		}
	}
	fb, fs, mb, mo := h.counts()
	if fb+fs == 0 {
		t.Error("正常腿一次都没被调用 ⇒ 这份产物不是从文件主链走出来的，上面的断言无射程")
	}
	if mb+mo == 0 {
		t.Errorf("极简指令兜底腿从未被走到（fullBatch=%d fullSingle=%d minimalBatch=%d minimalOne=%d）"+
			"⇒ 回显被判不可用后没有重译机会，成品只能是残缺交付", fb, fs, mb, mo)
	}
	if got := res.Data.Untranslated["en"]; got != 0 {
		t.Errorf("Data.Untranslated[en]=%d，期望 0 ⇒ 极简腿救回的段没写进译文表（键存在=已译出的不变量破了）", got)
	}
	if got := res.Data.ModelHits; got < len(echoSrcLines) {
		t.Errorf("ModelHits=%d，期望 ≥%d ⇒ 命中计数与实际写入脱钩", got, len(echoSrcLines))
	}
}

// TestFileEchoPipelineGuardOffDeliversEcho E4 反证：关掉回显判据（应急档），同一份假上游必须把回显写进产物。
// 没有这一条，上面那条绿灯可能只是"假上游根本没吐回显"（判据射程为零）——本仓反复踩的那类空转锁。
func TestFileEchoPipelineGuardOffDeliversEcho(t *testing.T) {
	t.Setenv("FILE_ECHO_GUARD", "0")
	h := echoEngine(t)
	res, artifact := echoRun(t, h)

	if !strings.Contains(artifact, "currency symbol") {
		t.Fatalf("判据已关（FILE_ECHO_GUARD=0）却仍没把回显写进产物 ⇒ 假上游压根没模拟现网回显形态，"+
			"正向用例 E1 的绿灯无射程\n产物=%q", artifact)
	}
	// 与 E1 的对照读数＝**回显把真译文顶掉了**（现网形态：成品里躺着两段"规范说明"，
	// 真正的第 2/3 行译文没了）。所以这里要问的是"正常译文有几行不在"，
	// 而不是"中文原文还在不在"——原位写回把该段的内容换成了回显，原文同样不在产物里。
	missingReal := 0
	for _, tr := range echoGoodTr {
		if !strings.Contains(artifact, tr) {
			missingReal++
		}
	}
	if missingReal == 0 {
		t.Errorf("应急档下四条正常译文**全部**仍在产物里 ⇒ 假上游的回显压根没顶掉真译文，"+
			"E1 那条绿灯无从对比\n产物=%q", artifact)
	}
	// 关掉判据后回显被当"已译出"⇒ 硬闸不再重试，Untranslated 归零但交付是坏的：
	// 这一读数正是现网"每一行统计都绿、客户拿到规范说明"的成因，写在这里给下一位改判据的人看。
	if got := res.Data.Untranslated["en"]; got != 0 {
		t.Logf("应急档下 Untranslated[en]=%d（非零说明还有段没写进译文表，与判据无关）", got)
	}
}

// TestFileEchoPipelineMutedMinimalLegDeliversPartial E5 反证：把极简兜底腿哑掉。
// 期望的形态与 E1 严格相反——判据仍然拦住回显（产物里没有那段"规范说明"），
// 但拦住之后没有救援 ⇒ 四行全部回到"以原文交付＋计入 Untranslated"的残缺形态。
// 这一条存在的意义：证明 E1 里"四行都有译文"绿灯**来自兜底腿**，
// 而不是批量正常腿自己给的（若哪天有人把救援腿接线拆掉，E1 只会红在"缺行"上，
// 而看不出是判据过杀还是救援断了；本条把这两种病因分开）。
// echoRunRaw 与 echoRun 同一条链，但**不要求**有产物：反证用例（E5）要看的正是"没有产物"这一形态。
// 产物为空是 file.go 既有的交付语义（`if len(tr) == 0 { continue }`——一种语言一行都没译出时
// 不产出"和原文一模一样"的文件），不是本用例要判对错的对象。
func echoRunRaw(t *testing.T, h *echoHarness) (*FileTranslateResult, string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "uat_sample.txt")
	if err := os.WriteFile(src, []byte(strings.Join(echoSrcLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("写入样例文件失败: %v", err)
	}
	options := map[string]interface{}{
		"target_langs": []interface{}{"en"},
		"mode":         "fast",
	}
	res := h.eng.HandleFile(context.Background(), src, options, nil, nil)
	if res == nil {
		t.Fatal("HandleFile 返回 nil")
	}
	if res.Error != "" {
		t.Fatalf("文件管线整单失败: %s", res.Error)
	}
	if len(res.Files) == 0 {
		return res, ""
	}
	b, err := os.ReadFile(res.Files[0])
	if err != nil {
		t.Fatalf("读产物 %s 失败: %v", res.Files[0], err)
	}
	return res, string(b)
}

func TestFileEchoPipelineMutedMinimalLegDeliversPartial(t *testing.T) {
	h := echoEngine(t)
	h.muteMinimal = true
	res, artifact := echoRunRaw(t, h)

	// ① 判据与救援腿互不相干：哑掉救援腿之后，回显仍然不许进产物。
	if strings.Contains(artifact, "currency symbol") {
		t.Errorf("极简腿已哑，回显却进了产物 ⇒ 拦住回显的不是判据而是救援腿（判据射程被误读）\n产物=%q", artifact)
	}
	// ② 残缺必须如实浮出：四行全部未译出 ⇒ Untranslated=4，且（按既有交付语义）不产出文件。
	if got := res.Data.Untranslated["en"]; got != len(echoSrcLines) {
		t.Errorf("哑掉极简腿后 Untranslated[en]=%d，期望 %d ⇒ 缺失段没有如实统计（客户会拿到残缺成品而工单显示完成）",
			got, len(echoSrcLines))
	}
	if len(res.Files) != 0 {
		t.Errorf("哑掉极简腿后仍然产出了文件（Files=%v）⇒ 与 E1 的对照失去意义：这条腿明明是唯一交付来源",
			res.Files)
	}
	// ③ 与 E1 的同一段读数对照：E1 有产物且四行齐全，本条两者皆无 ⇒ 绿灯确实来自兜底腿。
	if artifact != "" {
		t.Errorf("本用例期望无产物，实得产物 %q", artifact)
	}
}
