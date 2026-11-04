// ============ 本文件职责中文说明 ============
// ★ 2026-11-03 立。`api/server.go` 的 `ocr_warm` 与 `fileproc/warm.go` 的 `OcrWarmState`
// 头上都写着「本词**禁止**被任何入口渲染判据消费（锁＝api/health_ocr_warm_gate_test.go W2）」，
// 本文件就是那把锁——**注释点名的锚点必须存在**（AGENTS §一·14）。
//
// 这条锁护的是一个**已经发生过一次的"危险修法"**：把 `ocr_ready` 改成等于 `caps.ocr.loaded`。
// 它看起来只是"让看板更细"，实际是拿 §一·17 那条「能力布尔＝入口可见性的唯一判据」去换一个读数：
//
//	`ocr_ready=false` ⇒ 前端那颗 OCR 入口**整颗不渲染**；
//	而「没预热」是**正常态**（常驻服务闲置卸载、冷启动那段时间都算）⇒
//	改完之后冷启动当天与闲置之后，OCR 入口会在客户界面上凭空消失，**而能力完全可用**。
//	严重度从"看板不够细"升成"功能对客户不可见"。
//
// 所以口径只有一条：**新增一个读数，`ocr_ready` 的判据一字不动**。
// 三条腿分别守三件事：
//
//	W1 出参有 `ocr_warm`，是**字符串**且落在五个状态词里（不是布尔、不是数字、不是路径）。
//	W2 前端**任何**取词点都不读它（能力布尔才是入口的唯一判据）。
//	W3 `ocr_ready` 仍绑 `fileproc.OcrAvailable()`，且 `server.go` 里不出现把两个词接起来的写法。
//
// ⚠️ W2 是**跨语言**的源码锁（读 TypeScript 源），判的是"有没有人开始用它渲染"，
// 不是"这个字段是否存在于接口"——后者由 W1 正向判。两半一起才叫射程闭合：
// 只判 W1 时，前端拿它门禁入口照样全绿；只判 W2 时，字段被整条删掉也全绿。
// ============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// warmStateWords `ocr_warm` 的合法取值（对外排障契约，逐字钉住；同 dispatch/ocr_fix 那一族写法）。
// 五个词各自的动作不同：off＝正常态别去看／cold＝第一单会慢一点／loaded＝不慢／busy＝有活在跑／
// unknown＝探针自己不够格说话（去看 capsvc 段）。合成两个词就会让运维对着一个词做错动作。
var warmStateWords = map[string]string{
	"off":     "没部署常驻服务（正常态，能力照常走子进程）",
	"cold":    "部署了但模型此刻没在内存（第一单会慢，知道就好）",
	"loaded":  "模型已在内存（不慢）",
	"busy":    "模型在内存且有活在跑",
	"unknown": "服务连着但回里没有 OCR 这一档（探针不够格说话，去看 capsvc 段）",
}

// TestHealthOcrWarmIsStatusWord W1：`/api/health.ocr_warm` 必须存在、是字符串、且落在五词表里。
func TestHealthOcrWarmIsStatusWord(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	(&Server{}).handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleHealth 应 200，实际 %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("health 出参非法 JSON: %v", err)
	}
	raw, ok := body["ocr_warm"]
	if !ok {
		t.Fatalf("缺 ocr_warm 字段：冷启动慢这件事就只剩日志里的一句话，看板与排障都读不到。出参: %s", rec.Body.String())
	}
	s, ok := raw.(string)
	if !ok {
		t.Fatalf("ocr_warm 必须是**状态词**（字符串），实得 %T：布尔/数字会让运维按形状猜，而这两个问题（能不能干 / 会不会慢）本来就是两回事。出参: %s", raw, rec.Body.String())
	}
	if _, known := warmStateWords[s]; !known {
		t.Fatalf("ocr_warm=%q 不在五词表内（%s）：多一个词就等于多一种没人写处置动作的状态", s, strings.Join(warmWords(), "/"))
	}
	// 同 #42 口径：只出状态词。路径/主机名/权重文件名一律不许进这一格。
	for _, bad := range []string{"/", "\\", ".onnx", ".py", "127.0.0.1", "localhost"} {
		if strings.Contains(s, bad) {
			t.Errorf("ocr_warm 出现了路径/主机类字面（含 %q）：%q ⇒ 违反「只出状态词」", bad, s)
		}
	}
	// 与能力位**同类不同值**：两者都是字符串/布尔各一格，且 ocr_ready 仍必须是布尔
	// （W3 会把这条判据钉在源码上；这里先判它没被顺手改成字符串）。
	if _, ok := body["ocr_ready"].(bool); !ok {
		t.Errorf("ocr_ready 必须仍是布尔（能力位），实得 %T ⇒ 有人把它改成了与 ocr_warm 同形的读数", body["ocr_ready"])
	}
}

// warmWords 五词表的键（仅用于报错消息，稳定排序）。
func warmWords() []string {
	return []string{"busy", "cold", "loaded", "off", "unknown"}
}

// srcTreeFrontend 返回 frontend-react/src 的绝对路径（相对 backend-go 向上两级）。
// 取不到直接判死：跨语言锁"扫不到目录"与"扫到零命中"必须分开，
// 否则路径一变，W2 会安静地变成一条恒绿锁（AGENTS §三「闸门失明比红灯更坏」）。
func srcTreeFrontend(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "frontend-react", "src")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("找不到前端源码树 %s（这一腿没跑到，不许当通过）: %v", dir, err)
	}
	return dir
}

// walkErrCache 收集遍历中的读错误——读不到就判死，不许当"这个文件没问题"。
func collectFrontendHits(t *testing.T, needle string) (hits []string, files int) {
	t.Helper()
	root := srcTreeFrontend(t)
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !(strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".tsx")) {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		files++
		if strings.Contains(string(b), needle) {
			rel, _ := filepath.Rel(root, p)
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历前端源码失败（这一腿没跑到，不许当通过）: %v", err)
	}
	if files < 100 {
		t.Fatalf("只扫到 %d 个 .ts/.tsx 文件 ⇒ 明显扫空了（前端树远不止这些），W2 会退化成恒绿锁", files)
	}
	return hits, files
}

// TestOcrWarmNotConsumedByEntryGating W2：前端取词点**一律不读 `ocr_warm`**。
//
// 判据是"零命中"，配两枚对照才叫有射程：
//
//	① 正向对照：`ocr_ready` 必须**扫得到**（它就在 voiceCapability.ts 里，是入口门的唯一判据）。
//	   这一条同时证明"扫得到文件、判得出命中"——没有它，整个目录读空也会绿。
//	② 负向对照：把探针针脚换成一个必然存在的字符串，也必须扫得到；
//	   （本用例用 `tts_ready` 当第二枚针脚，它与 ocr 同一文件同一行族。）
func TestOcrWarmNotConsumedByEntryGating(t *testing.T) {
	if hits, files := collectFrontendHits(t, "ocr_warm"); len(hits) > 0 {
		t.Errorf("前端 %d 个取词点里出现了 ocr_warm：%v\n"+
			"→ ocr_warm 答的是「这一单会不会慢」，ocr_ready 答的是「这台能不能干这活」。\n"+
			"   拿前者门禁入口 ⇒ 冷启动当天/闲置卸载之后 OCR 入口整颗消失，而能力完全可用。\n"+
			"   能力布尔才是入口可见性的唯一判据（AGENTS §一·17）。", len(hits), hits)
		_ = files
	}

	// ① 正向对照：入口门那一份必须扫得到 ocr_ready。
	readyHits, _ := collectFrontendHits(t, "ocr_ready")
	if len(readyHits) == 0 {
		t.Fatalf("连 ocr_ready 都扫不到 ⇒ 这条锁在扫空目录（判据失明，比红灯更坏）")
	}
	// ② 第二枚针脚（同族字段，用来证明扫描本身有命中能力）
	if h, _ := collectFrontendHits(t, "tts_ready"); len(h) == 0 {
		t.Fatalf("tts_ready 也扫不到 ⇒ 扫描器只对某个特定词有效（判据本身可疑）")
	}
}

// TestOcrReadyCriterionUntouched W3：`ocr_ready` 的判据仍是 `fileproc.OcrAvailable()`。
//
// 源码级锁定，因为这条判据的"被改坏"在运行期**极难发现**：
// 部署机上只要常驻服务起过一次，`caps.ocr.loaded` 也是 true，于是本地/CI 全绿，
// 只有"闲置卸载之后"那一个窗口里界面少了入口——而那正是这条修法最容易落地的时刻（省内存调优后）。
//
// 两半：
//
//	① 正向：`"ocr_ready": fileproc.OcrAvailable()` 逐字在位；
//	② 负向：`server.go` 里 `ocr_ready` 那一行不许出现 `Warm`/`CapsvcHealthSnapshot`。
func TestOcrReadyCriterionUntouched(t *testing.T) {
	b, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读 server.go 失败（这一腿没跑到，不许当通过）: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, `"ocr_ready": fileproc.OcrAvailable()`) {
		t.Errorf("ocr_ready 的判据必须是 fileproc.OcrAvailable()（Python 可导入 + 权重落盘），"+
			"实读源码里找不到这一行 ⇒ 有人把它换了。换成预热态会让冷启动期间入口消失，而能力完全可用。\n源码: %s", src)
	}
	// 逐行判：只判 ocr_ready 那一行，避免把 ocr_warm 自己那行的合法引用一起误伤。
	lines := strings.Split(src, "\n")
	seen := 0
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "//") {
			continue // 注释里说明"为什么不许改成 loaded"是允许的（本仓要求把理由写在代码旁）
		}
		if !strings.Contains(trimmed, `"ocr_ready"`) {
			continue
		}
		seen++
		for _, bad := range []string{"Warm", "CapsvcHealthSnapshot", "loaded"} {
			if strings.Contains(trimmed, bad) {
				t.Errorf("server.go:%d 把 ocr_ready 接到了预热态（含 %q）：%s\n"+
					"→ 这会把「没预热」报成「不能干」，前端据此整颗摘掉 OCR 入口", i+1, bad, trimmed)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("ocr_ready 在 server.go 里的赋值点应恰 1 处，实得 %d ⇒ 要么被删了要么长出了第二把尺子", seen)
	}
}
