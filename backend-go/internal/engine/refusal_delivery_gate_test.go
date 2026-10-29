// ============ 本文件职责中文说明 ============
// 2026-10-26 用户追加裁决八-3b 的落码锁：风控拒译**不拒绝交付**，但那句回绝话术
// 一个字都不许出现在 ① 交付正文/首页首行 ② 对照表单元格 ③ 产物文件名 ④ 交付文档第一页。
//
// 现网靶子（M1 实证）：一份涉证内容的件，**五件交付物同名**
//
//	I am unable to provide the translation you requested_en.docx / .pdf / .md …
//
// 旧批只在 `file_name.go` 那一腿拦了文件名，首页/正文那三条腿一直没人管——
// 所以本文件的结构腿（S1/S2）判的是"三个交付取值口都必须过同一个咽喉"，
// 行为腿（N1~N4）逐面各钉一次负向，防误杀的正向对照（P1~P3）同屏。
//
// 判据一律复用 `refusalResidue`（与采纳侧同一把尺子，含"源文自己就谈这件事"的豁免），
// 禁止在本文件里再枚举一份词表——两份词表迟早打架，那是本仓反复登记的"多把尺子"形态。
// ============================================
package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/fileproc"
)

const m1Refusal = "I am unable to provide the translation you requested"

// 各面负向共用的靶子：涉证类内容触发上游风控，模型把回绝话当译文吐回来。
var refusalTarget = struct {
	src   string
	bad   string
	good  string
	mixed string
}{
	// ⚠️ 靶子原文**刻意不含「翻译/translat」**：refusalResidue 有一条源文豁免
	//   （客户自己的文档就在谈"无法提供翻译服务"时，那句照译是合法正文）。
	//   含了"翻译"二字的源文测不出话术剥离，只会测到豁免腿——那正是它该豁免的形态（见 P1）。
	src:  "请输入证件号码以继续办理。",
	bad:  m1Refusal,
	good: "Enter your certificate number to continue.",
	// 混合段靶子必须**两句各有句末符**：句界按「.」切，前一句没闭合就会与后一句并成一段，
	// 于是整段被当成回绝话剥掉——测的就不是逐句剥离，而是整段回落（夹具错，不是判据错）。
	mixed: m1Refusal + ". " + "The certificate number is 4716.",
}

// S1 结构腿：文件交付的三个取值口必须都过 `deliveryTranslations` 这一个咽喉。
// 判据按"每一处 `langTranslations[lc]` 的读取都被包裹"数——漏一处就是给下一位留一条裸通道
// （本仓那条「家族级锁要按全集判」的硬口径：新增交付分支忘了包，当场红）。
func TestDeliveryRefusalStripCoversEveryPickup(t *testing.T) {
	b, err := os.ReadFile("file.go")
	if err != nil {
		t.Fatalf("读 file.go: %v", err)
	}
	text := stripGoCommentsForGate(string(b))
	raw := strings.Count(text, "tr := langTranslations[lc]")
	wrapped := strings.Count(text, "tr := deliveryTranslations(lc, langTranslations[lc])")
	if raw != 0 {
		t.Errorf("交付取值口有 %d 处仍在裸读 langTranslations ⇒ 回绝话术可能从那里漏进首页/正文", raw)
	}
	if wrapped < 3 {
		t.Fatalf("只找到 %d 处包裹点（应 ≥3：纯文案 .md / 原位回写各语言 / 多 Sheet 对照表）⇒ 锚漂了，本条锁失明", wrapped)
	}
}

// S2 结构腿：咽喉函数体内必须真的复用 `refusalResidue`（另立词表＝两条迟早打架的尺子）。
func TestDeliveryStripReusesSameRuler(t *testing.T) {
	b, err := os.ReadFile("translation_guard.go")
	if err != nil {
		t.Fatalf("读 translation_guard.go: %v", err)
	}
	body := string(b)
	i := strings.Index(body, "func stripRefusalSentences")
	if i < 0 {
		t.Fatal("找不到 stripRefusalSentences ⇒ 咽喉被删除，S1 那条锁只数得到包裹点、看不见它是不是空壳")
	}
	end := strings.Index(body[i:], "\n}\n")
	fn := body[i : i+end+3]
	if strings.Count(fn, "refusalResidue(") < 2 {
		t.Errorf("stripRefusalSentences 里对 refusalResidue 的复用只有 %d 次（整段一次＋逐句一次是下限）⇒ 判据被换成别的尺子", strings.Count(fn, "refusalResidue("))
	}
}

// N1 交付正文与 .md 首行：整段回绝必须换成原文出货，那句回绝话一个字都不留。
func TestArtifactMdFirstLineHasNoRefusal(t *testing.T) {
	tr := deliveryTranslations("en", map[string]string{refusalTarget.src: refusalTarget.bad})
	dir := t.TempDir()
	p := filepath.Join(dir, "a-en.md")
	if err := fileproc.WriteTranslationMd(p, []string{refusalTarget.src}, tr); err != nil {
		t.Fatalf("写 .md: %v", err)
	}
	got := string(readFileForGate(t, p))
	if strings.Contains(strings.ToLower(got), "unable to provide") {
		t.Errorf(".md 正文里仍出现回绝话术（裁决八-3b 禁止的形态），实得 %q", got)
	}
	if strings.TrimSpace(got) == "" {
		t.Errorf("剥掉话术后不许把正文剥空（用户令①「不留白」），实得空文档")
	}
	// 首行＝交付文档第一页客户第一眼看到的那一行
	if first := strings.SplitN(strings.TrimSpace(got), "\n", 2)[0]; strings.Contains(strings.ToLower(first), "unable to provide") {
		t.Errorf("首行含回绝话术，实得 %q", first)
	}
	if !strings.Contains(got, refusalTarget.src) {
		t.Errorf("整段被回绝时这一格应回落原文交付，实得 %q", got)
	}
}

// N2 对照表那一格（xlsx 交付形态的"首页"）：同一份清洗过的 map 喂进去，格子里不许有话术。
func TestComparisonCellHasNoRefusal(t *testing.T) {
	tr := deliveryTranslations("en", map[string]string{refusalTarget.src: refusalTarget.bad})
	v := tr[refusalTarget.src]
	if strings.Contains(strings.ToLower(v), "unable to provide") {
		t.Errorf("对照表单元格取值仍带话术，实得 %q", v)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "c-en.xlsx")
	if err := fileproc.WriteComparisonXlsx(p, []string{refusalTarget.src}, tr); err != nil {
		t.Fatalf("写对照表: %v", err)
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
		t.Fatalf("对照表产物没落盘（本条腿就无从谈起）: %v", err)
	}
}

// N3 产物文件名主干：M1 那五件同名件的靶子——取名侧也不许出现那句话。
// 这里锁的是"交付值本身干净 ⇒ 由它派生的名字自然干净"这一层，文件名那一腿自身的判据
// 由 file_name_test.go 钉（同一把尺子的第二个面），两侧合起来才叫四个口都收住。
func TestArtifactBaseNameHasNoRefusal(t *testing.T) {
	tr := deliveryTranslations("en", map[string]string{refusalTarget.src: refusalTarget.bad})
	nameBase := tr[refusalTarget.src]
	if strings.Contains(strings.ToLower(nameBase), "unable to provide") {
		t.Errorf("取名主干里仍含回绝话术，实得 %q", nameBase)
	}
	if strings.ContainsAny(nameBase, "/\\\n\r") {
		t.Errorf("取名主干不许带路径分隔或换行（会被拼进产物名），实得 %q", nameBase)
	}
}

// N4 逐句剥离：混合段（话术 + 正常译文）只丢那一句，**正常译文一个字不许陪葬**。
// 这一条是射程自证——若实现是"整段丢弃/整段回落原文"，混合段会被整体换成原文，本条即红。
func TestStripKeepsLegalSentenceInMixedSegment(t *testing.T) {
	got := stripRefusalSentences(refusalTarget.src, refusalTarget.mixed)
	if strings.Contains(strings.ToLower(got), "unable to provide") {
		t.Errorf("混合段里那句回绝话必须被剥掉，实得 %q", got)
	}
	if !strings.Contains(got, "The certificate number is 4716.") {
		t.Errorf("混合段里的正常译文必须逐字保留（只砍模型多出来的，不砍模型写对的），实得 %q", got)
	}
}

// P1 正向对照（防误杀本体）：客户原文自己就在谈"无法提供翻译服务"时，
// 那句照译是**合法正文**，一个字符都不许动。
func TestStripKeepsLegalTranslationAboutRefusal(t *testing.T) {
	const src = "翻译服务无法提供时，系统会提示稍后重试。"
	const out = "When the translation service cannot provide service, the system prompts to retry later."
	if got := stripRefusalSentences(src, out); got != out {
		t.Errorf("源文自己就谈这件事时不许剥（豁免腿被摘就是删客户正文），实得 %q", got)
	}
	if got := deliveryTranslations("en", map[string]string{src: out})[src]; got != out {
		t.Errorf("咽喉这一层同样要走豁免，实得 %q", got)
	}
}

// P2 正向对照：本来就没有话术的交付值必须**逐字节不变**（清洗腿不许顺手改写空白/换行）。
func TestStripIsNoOpForCleanValues(t *testing.T) {
	const src = "第一段说明。\n第二段说明。"
	const out = "Note one.\nNote two."
	if got := stripRefusalSentences(src, out); got != out {
		t.Errorf("干净译文被改写了（清洗只许动坏形态），实得 %q", got)
	}
	tr := map[string]string{src: out, "另一段": "Another note."}
	if got := deliveryTranslations("en", tr); got[src] != out || got["另一段"] != "Another note." {
		t.Errorf("整张表必须逐值原样返回，实得 %+v", got)
	}
}

// stripGoCommentsForGate 剥掉 Go 源码注释，只留可执行文本（负向计数不被说明文字喂饱）。
// 本仓老坑：判"某句在不在"之前先剥注释，否则注释里写一次就恒真。
func stripGoCommentsForGate(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") {
			continue
		}
		if i := strings.Index(line, "//"); i >= 0 && !strings.Contains(line[:i], `"`) {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// readFileForGate 读测试临时产物（失败即红，不返回空串把空文件判成"没有话术"）。
func readFileForGate(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读 %s: %v", p, err)
	}
	return b
}
