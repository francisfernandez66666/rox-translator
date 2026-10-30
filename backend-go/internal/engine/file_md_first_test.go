// ============ file_md_first_test.go · 职责说明 ============
// 两段式交付「md 先行」的顺序锁（★ 2026-10-30 三事合一批 · 组5，B6/B7/B10/B18）。
//
// 为什么必须单独有这条锁：`WithArtifactSink` 未订阅时是空操作，所以「顺序」这件事
// **不会**在任何现有断言里现形——产物集合没变、文件都能打开，只是客户看到的先后关系反了。
// 本文件钉的正是那一个"先后"。
//
// 五类格式各一腿（docx / pdf / txt / 图片 / 音频）：
//   - **txt 走真管线**（假上游 + 内存引擎）：`md → original` 的事件序列、md 文件内容、
//     B18 的「未译出内容」一节（有未译段必点名 / 无未译段整节不出现）；
//   - **docx / pdf / 图片 / 音频 走源码顺序锁**：本机没有 LibreOffice / OCR 权重 / TTS 模型，
//     这四类跑不起真回写（跑了也是"提取就失败"），于是改成按**函数体源码顺序**判
//     「md 事件 → 格式写回调用 → original 事件」这三点的先后——调回旧顺序（先写回再写 md）
//     或把 emit 挪到写回之后，当场红。⚠️ 这说明它不是行为锁；行为覆盖由 UAT T74 段的
//     真件矩阵承担（方案 §7.4），两者缺一不可（AGENTS §三「判据级断言代替不了守卫级」的反面）。
//
// 另外两腿：
//   - B7 写回失败：md 已发 ⇒ 无 original、md 行标 degraded、语言进 degraded_langs
//     （用假上游在被调用的那一刻删掉源件，让 ApplyAlignedText 必然失败）；
//   - B10 同路径去重：`.md` 输入件的 md 与 original 指向**同一路径**，引擎只发这一组事件
//     （服务层据此把 md 行收敛成主产物，UI 只画一个按钮）。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/engine/ -run FileMDFirst
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
	"regexp"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
)

// mdSrcLines 四条中文源文（与 echo 夹具同形态：短句，便于假上游逐行回译文）。
var mdSrcLines = []string{
	"系统自动匹配内置汽车数据库",
	"支持多格式文件翻译",
	"请在维护周期内校准设备",
	"本页内容仅供参考",
}

// mdTr 每行的英文译文（全英文，保证过 fileTranslationUsable）。
var mdTr = []string{
	"The system automatically matches the built-in automotive database",
	"Multi-format file translation is supported",
	"Please calibrate the device within the maintenance cycle",
	"This page is for reference only",
}

// mdSlotRe 从批量请求的 user 段里逐格取 <sN>源文</sN>（N 与源文一起取，回复才对得上号）。
var mdSlotRe = regexp.MustCompile(`<s(\d+)>([^<]*)</s\d+>`)

// mdFirstHarness 假上游 + 引擎 + 事件收集。
type mdFirstHarness struct {
	eng *Engine
	mu  sync.Mutex
	// events 引擎按 emit 顺序推来的产物事件（本文件的核心读数）。
	events []Artifact
	// emptyLine>0 时，那一行（1 起）一律回空——造"未译出"形态（B18 前半 / 硬闸补译）。
	emptyLine int
	// dropSourceOnFirstCall 非空时，第一次收到翻译请求就把这个文件删掉——
	// 用来让**写回**必然失败（B7），而 md 阶段（不读源件）仍能成功。
	dropSourceOnFirstCall string
	dropped               bool
}

// eventsSnapshot 取事件序列快照。
func (h *mdFirstHarness) eventsSnapshot() []Artifact {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Artifact{}, h.events...)
}

// mdFakeHandler 假上游：按 <sN> 逐格回译文（或按 emptyLine 回空），单发 <t> 同理。
func (h *mdFirstHarness) mdFakeHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &req)
	user := ""
	for _, m := range req.Messages {
		if m.Role != "system" {
			user = m.Content
		}
	}
	h.mu.Lock()
	if h.dropSourceOnFirstCall != "" && !h.dropped {
		h.dropped = true
		_ = os.Remove(h.dropSourceOnFirstCall)
	}
	h.mu.Unlock()

	tr := func(src string) string {
		for i, s := range mdSrcLines {
			if s == strings.TrimSpace(src) {
				if h.emptyLine == i+1 {
					return ""
				}
				return mdTr[i]
			}
		}
		return "Translated: " + strings.TrimSpace(src)
	}
	var content string
	if slots := mdSlotRe.FindAllStringSubmatch(user, -1); len(slots) > 0 {
		var sb strings.Builder
		for _, sl := range slots {
			v := tr(sl[2])
			if v != "" {
				fmt.Fprintf(&sb, "<s%s>%s</s%s>\n", sl[1], v, sl[1])
			} else {
				fmt.Fprintf(&sb, "<s%s></s%s>\n", sl[1], sl[1])
			}
		}
		content = sb.String()
	} else if m := regexp.MustCompile(`<t>([\s\S]*?)</t>`).FindStringSubmatch(user); len(m) == 2 {
		v := tr(m[1])
		content = "<t>" + v + "</t>"
	} else {
		content = "<t>" + tr(user) + "</t>"
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":"stop"}]}`, mdQuoteJSON(content))
}

// mdQuoteJSON 把文本编码成 JSON 字符串字面量（回复体里唯一需要转义的位置）。
func mdQuoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// newMDFirstHarness 装配：假上游 + NewEngine（必须走构造器：单发腿会读 breaker 等构造期字段）。
func newMDFirstHarness(t *testing.T, emptyLine int) *mdFirstHarness {
	t.Helper()
	h := &mdFirstHarness{emptyLine: emptyLine}
	srv := httptest.NewServer(http.HandlerFunc(h.mdFakeHandler))
	t.Cleanup(srv.Close)
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言：防 UAT 矩阵的 PG env 泄漏（AGENTS §一·4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	h.eng = NewEngine(cfg, nil, nil, nil)
	return h
}

// mdRun 把一份文件推过整条文件管线（fast 模式：无 KB 直配、无审校腿，链路最短且唯一），
// 并把产物事件收进 harness.events。
func (h *mdFirstHarness) mdRun(t *testing.T, path string, opts map[string]interface{}) *FileTranslateResult {
	t.Helper()
	ctx := WithArtifactSink(context.Background(), func(a Artifact) {
		h.mu.Lock()
		h.events = append(h.events, a)
		h.mu.Unlock()
	})
	options := map[string]interface{}{"target_langs": []interface{}{"en"}, "mode": "fast"}
	for k, v := range opts {
		options[k] = v
	}
	res := h.eng.HandleFile(ctx, path, options, nil, nil)
	if res == nil {
		t.Fatal("HandleFile 返回 nil")
	}
	return res
}

// mdWriteTemp 在临时目录里落一份样例文件，返回路径。
func mdWriteTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("写样例文件失败: %v", err)
	}
	return p
}

// mdKinds 把事件序列压成 "md:/path,m original:/path" 形态，便于失败时一眼看出顺序。
func mdKinds(events []Artifact) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind+":"+filepath.Base(e.Path))
	}
	return out
}

// TestFileMDFirstTxtOrderAndNotes B6（txt 真管线）＋ B18。
func TestFileMDFirstTxtOrderAndNotes(t *testing.T) {
	h := newMDFirstHarness(t, 0)
	src := mdWriteTemp(t, "uat_sample.txt", strings.Join(mdSrcLines, "\n")+"\n")
	res := h.mdRun(t, src, nil)
	if res.Error != "" {
		t.Fatalf("整单失败: %s", res.Error)
	}
	ev := h.eventsSnapshot()
	if len(ev) != 2 {
		t.Fatalf("单语言 txt 应恰好 2 条事件（md + original），实得 %d: %v", len(ev), mdKinds(ev))
	}
	if ev[0].Kind != ArtifactKindMD || ev[1].Kind != ArtifactKindOriginal {
		t.Fatalf("事件顺序必须是 md → original（md 先行），实得 %v", mdKinds(ev))
	}
	if ev[0].Primary {
		t.Error("md 事件不该带 primary（该语言的『下载原格式』目标件是 original）")
	}
	if !ev[1].Primary {
		t.Error("original 事件必须带 primary=1")
	}
	if ev[0].Path == ev[1].Path {
		t.Error("txt 的 md 与回写件必须是两份文件（同路径只应出现在 .md 输入件那一档）")
	}
	// md 里必须是**译文**（不是原文）：说明 md 用的是交付口径的 tr。
	b, err := os.ReadFile(ev[0].Path)
	if err != nil {
		t.Fatalf("md 产物不可读: %v", err)
	}
	if !strings.Contains(string(b), mdTr[0]) {
		t.Fatalf("md 里应有译文（%q），实得前 200 字：%q", mdTr[0], string(b[:min(len(b), 200)]))
	}
	// B18 反向：没有未译段时整节不出现（连标题都不写）。
	if strings.Contains(string(b), "未译出内容") {
		t.Fatalf("无未译段时 md 不该出现「未译出内容」一节：%q", string(b))
	}
	if len(res.Data.DegradedLangs) != 0 {
		t.Errorf("正常交付不该有 degraded_langs：%v", res.Data.DegradedLangs)
	}
}

// TestFileMDFirstUntranslatedNotes B18 前半：有未译段时 md 必须逐段点名。
func TestFileMDFirstUntranslatedNotes(t *testing.T) {
	h := newMDFirstHarness(t, 2) // 第 2 行恒回空 ⇒ 硬闸 3 轮补译后仍缺
	src := mdWriteTemp(t, "uat_sample.txt", strings.Join(mdSrcLines, "\n")+"\n")
	res := h.mdRun(t, src, nil)
	if got := res.Data.Untranslated["en"]; got == 0 {
		t.Fatalf("夹具应造出未译出段（第 2 行），实得 untranslated=%v", res.Data.Untranslated)
	}
	ev := h.eventsSnapshot()
	var mdPath string
	for _, e := range ev {
		if e.Kind == ArtifactKindMD {
			mdPath = e.Path
		}
	}
	if mdPath == "" {
		t.Fatalf("没有 md 事件: %v", mdKinds(ev))
	}
	b, _ := os.ReadFile(mdPath)
	body := string(b)
	if !strings.Contains(body, "未译出内容") {
		t.Fatalf("有未译段时 md 必须带「未译出内容」一节（AGENTS §19 裁决 7）：%q", body)
	}
	if !strings.Contains(body, mdSrcLines[1]) {
		t.Fatalf("那一节必须点名缺的源文（%q）：%q", mdSrcLines[1], body)
	}
}

// TestFileMDFirstSamePathDedup B10（引擎半）：.md 输入件的 md 与 original 同路径、md 在前。
func TestFileMDFirstSamePathDedup(t *testing.T) {
	h := newMDFirstHarness(t, 0)
	src := mdWriteTemp(t, "uat_sample.md", strings.Join(mdSrcLines, "\n\n")+"\n")
	res := h.mdRun(t, src, nil)
	if res.Error != "" {
		t.Fatalf("整单失败: %s", res.Error)
	}
	ev := h.eventsSnapshot()
	if len(ev) != 2 || ev[0].Kind != ArtifactKindMD || ev[1].Kind != ArtifactKindOriginal {
		t.Fatalf(".md 输入件应发 md → original 两条事件，实得 %v", mdKinds(ev))
	}
	if ev[0].Path != ev[1].Path {
		t.Fatalf(".md 输入件的 md 与 original 必须同路径（服务层据此收敛成一行），实得 %q vs %q", ev[0].Path, ev[1].Path)
	}
	if !ev[1].Primary {
		t.Error("同路径的 original 事件必须带 primary=1（服务层据此把那行 md 置主产物）")
	}
	// 旁路不再重复登记同一份文件（TextFiles 里不该出现它）。
	for _, p := range res.TextFiles {
		if p == ev[0].Path {
			t.Fatalf("同路径的 md 不该再进 TextFiles（去重腿）：%v", res.TextFiles)
		}
	}
}

// TestFileMDFirstWritebackFailureDegrades B7：写回失败 ⇒ 只有 md、进 degraded、md 标 degraded。
func TestFileMDFirstWritebackFailureDegrades(t *testing.T) {
	h := newMDFirstHarness(t, 0)
	src := mdWriteTemp(t, "uat_sample.txt", strings.Join(mdSrcLines, "\n")+"\n")
	// 第一次翻译请求时删掉源件：提取已完成、md 阶段只写译文、写回要重读源件 ⇒ 必然失败。
	h.dropSourceOnFirstCall = src
	res := h.mdRun(t, src, nil)
	if res.Error != "" {
		t.Fatalf("写回失败应降级交付而不是整单失败：%s", res.Error)
	}
	ev := h.eventsSnapshot()
	var hasOriginal, mdDegraded bool
	for _, e := range ev {
		if e.Kind == ArtifactKindOriginal {
			hasOriginal = true
		}
		if e.Kind == ArtifactKindMD && e.Degraded {
			mdDegraded = true
		}
	}
	if hasOriginal {
		t.Fatalf("写回失败时不该发 original（客户端会点到一个不存在的按钮）：%v", mdKinds(ev))
	}
	if !mdDegraded {
		t.Fatalf("写回失败时 md 行必须标 degraded（UI 右钮禁用并给原因）：%v", mdKinds(ev))
	}
	found := false
	for _, lc := range res.Data.DegradedLangs {
		if lc == "en" {
			found = true
		}
	}
	if !found {
		t.Fatalf("该语言必须进 degraded_langs：%v", res.Data.DegradedLangs)
	}
	if len(res.Files) == 0 {
		t.Fatal("降级交付仍应有 md 产物（filesOut 不能空）")
	}
}

// mdSrcIndex 返回 needle 在源码里的字节位置（找不到直接判红：锚点搬家＝这条锁失明）。
func mdSrcIndex(t *testing.T, src, needle string) int {
	t.Helper()
	i := strings.Index(src, needle)
	if i < 0 {
		t.Fatalf("锚点缺失：%q（实现在别的形态上重写了？这条锁已失明，需同步调整）", needle)
	}
	return i
}

// TestFileMDFirstSourceOrderHeavyFormats docx/pdf/图片/音频的顺序锁。
//
// 本机跑不起这四类的真回写（无 LibreOffice / OCR 权重 / TTS 模型），所以按源码顺序判：
// 「md 事件」必须出现在该格式的**写回调用之前**，「original 事件」必须出现在写回之后。
// 调回旧顺序（先写回再写 md）或把 emit 挪到写回之后，这里当场红。
//
// ⚠️ 判据只在**逐语言写回段**（图片/音频/办公/文本共用的那个大 else 分支）里取位置：
// 同一个锚点字符串在别处也有（纯文案分支的 ApplyAlignedText、.md 同路径分支的 original emit），
// 用全文首现会把射程指到别的分支去——本文件首跑真踩过（docx/pdf/txt 三条全部假红）。
func TestFileMDFirstSourceOrderHeavyFormats(t *testing.T) {
	raw, err := os.ReadFile("file.go")
	if err != nil {
		t.Fatalf("读 file.go 失败: %v", err)
	}
	src := string(raw)
	// 写回段的起点取「图片分支里那句唯一注释」：`if writebackDelivery(ext) == "image"` 在
	// HandleFile 开头（defer ReleaseImageBoxes 那一处）也出现一次，用它会圈进纯文案分支。
	segStart := mdSrcIndex(t, src, "// ★ P1a：图片格式（经 OCR 投喂翻译）无原格式可原位回写")
	segEnd := mdSrcIndex(t, src, "// ★ 还原模式纯文案旁路产物")
	if segEnd <= segStart {
		t.Fatalf("写回段的锚点顺序异常（start=%d end=%d）：源码结构变了，需同步调整", segStart, segEnd)
	}
	seg := src[segStart:segEnd]
	// find 在写回段里取首次出现位置；找不到返回 -1（调用方在子测试里判红，别在父测试里 Fatalf）。
	find := func(needle string) int { return strings.Index(seg, needle) }
	// findLast 取最后一次（original emit 在 .md 同路径分支也出现一次，取最后那次＝写回成功处）。
	findLast := func(needle string) int { return strings.LastIndex(seg, needle) }

	// ⚠️ 台账事件发送函数名是 `emitOne`：原先叫 `send`，与同包的 `segEmitter.send` 同名，
	// 会让 `model_for_stage` 那条"按函数名归体"的锁失明（同名出现两次即判红），故 2026-10-30 改名。
	inplaceOriginal := "artStage.emitOne(ArtifactKindOriginal, lc, outPath, true, false)"
	imgMD := "artStage.emitOne(ArtifactKindMD, lc, imgPath, false, false)"
	imgWrite := "fileproc.RenderTranslatedImage(ctx, filePath, inPlacePath, RenderSafeTranslations(tr))"
	imgOriginal := "artStage.emitOne(ArtifactKindOriginal, lc, inPlacePath, true, false)"
	audMD := "artStage.emitOne(ArtifactKindMD, lc, audPath, false, false)"
	audWrite := "dubTranslatedAudio(lc, ttsLines)"
	audOriginal := "artStage.emitOne(ArtifactKindOriginal, lc, dubFiles[0], true, false)"

	cases := []struct {
		name  string
		write string
		md    string
		orig  string
		last  bool // original 取最后一次出现（办公/文本三类）
	}{
		{"docx", "fileproc.ApplyDocx(filePath, outPath, tr)", "writeLangMD(lc, tr, notes)", inplaceOriginal, true},
		{"pdf", "fileproc.ApplyTranslatedPdfOverlay(ctx, outPath, filePath, tr, lc)", "writeLangMD(lc, tr, notes)", inplaceOriginal, true},
		{"txt", "fileproc.ApplyAlignedText(ext, filePath, outPath, tr)", "writeLangMD(lc, tr, notes)", inplaceOriginal, true},
		{"image", imgWrite, imgMD, imgOriginal, false},
		{"audio", audWrite, audMD, audOriginal, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mdPos := find(c.md)
			writePos := find(c.write)
			origPos := find(c.orig)
			if c.last {
				origPos = findLast(c.orig)
			}
			if mdPos < 0 || writePos < 0 || origPos < 0 {
				t.Fatalf("%s：锚点缺失（md=%d write=%d orig=%d）——实现在别的形态上重写了，这条锁已失明，需同步调整",
					c.name, mdPos, writePos, origPos)
			}
			if !(mdPos < writePos) {
				t.Fatalf("%s：md 事件（pos=%d）必须先于写回调用（pos=%d）——md 先行被调回旧顺序", c.name, mdPos, writePos)
			}
			if !(writePos < origPos) {
				t.Fatalf("%s：original 事件（pos=%d）必须晚于写回调用（pos=%d）——否则会宣告一件还不存在的产物", c.name, origPos, writePos)
			}
		})
	}
	// 纯文案模式（delivery=text）与 .md 输入件：md 就是主产物 ⇒ primary=1（没有 original 可等）。
	if !strings.Contains(src, "artStage.emitOne(ArtifactKindMD, lc, outPath, true, false)") {
		t.Error("纯文案模式的 md 事件必须带 primary=1：否则前端永远显示『生成中…』等一个不存在的 original")
	}
	if !strings.Contains(src, "mdIsOriginal") {
		t.Error(".md 输入件的同路径复用（mdIsOriginal）不见了：B10 的同路径去重会退化成两行")
	}
}
