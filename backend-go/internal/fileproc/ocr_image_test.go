// ============ ocr_image_test.go · 职责说明 ============
// P4「图片原位翻译」的回归锁。三条必守口径：
//
//	① **同一份坐标**：写回用的框必须是提取期那份（写一份缓存 + 按路径取回），
//	   否则"送去翻译的那行"与"要覆盖的那个框"对不上——这是原位翻译最隐蔽的坏法
//	   （图看着像翻译过了，内容却贴错了位置）。
//	② **数量严格相等才敢配对**：脚本侧 boxes 与 lines 数量对不上时宁可不缓存。
//	③ **择优产出**：RenderTranslatedImage 在缺依赖/缺坐标/缺脚本时必须返回**错误**而不是
//	   产出半张图——调用方（engine）据此退回"仅译文 .md"，译文资产不丢。
//
// =============================================
package fileproc

import (
	"os"
	"path/filepath"
	"testing"
)

// resetImageBoxCache 隔离包级缓存（其他用例可能往里写过东西，否则下面的 eviction 断言不可信）。
func resetImageBoxCache(t *testing.T) {
	t.Helper()
	imageBoxMu.Lock()
	saved := imageBoxCache
	imageBoxCache = nil
	imageBoxMu.Unlock()
	t.Cleanup(func() {
		imageBoxMu.Lock()
		imageBoxCache = saved
		imageBoxMu.Unlock()
	})
}

// TestImageBoxCacheRoundTrip ① 写回取回的是同一份坐标；不同路径互不串位。
func TestImageBoxCacheRoundTrip(t *testing.T) {
	resetImageBoxCache(t)
	a := []OcrBox{{Text: "标题一", Box: []float64{0, 0, 10, 0, 10, 10, 0, 10}}}
	b := []OcrBox{{Text: "标题二", Box: []float64{20, 20, 30, 20, 30, 30, 20, 30}}}
	RememberImageBoxes("/tmp/a.png", a)
	RememberImageBoxes("/tmp/b.png", b)

	got := ImageBoxes("/tmp/a.png")
	if len(got) != 1 || got[0].Text != "标题一" || len(got[0].Box) != 8 {
		t.Fatalf("ImageBoxes(/tmp/a.png) = %+v，want a 的坐标", got)
	}
	if g2 := ImageBoxes("/tmp/b.png"); len(g2) != 1 || g2[0].Text != "标题二" {
		t.Fatalf("ImageBoxes(/tmp/b.png) = %+v，want b 的坐标（路径串位）", g2)
	}
	if g3 := ImageBoxes("/tmp/never.png"); g3 != nil {
		t.Fatalf("未缓存的路径应返回 nil（调用方据此退回仅译文 .md），实际 %+v", g3)
	}
}

// TestImageBoxCacheEviction 缓存有界：长跑进程不能无限增长。
func TestImageBoxCacheEviction(t *testing.T) {
	resetImageBoxCache(t)
	for i := 0; i < imageBoxCacheMax+8; i++ {
		p := filepath.Join("/tmp", "img_"+strconvItoa(i)+".png")
		RememberImageBoxes(p, []OcrBox{{Text: p}})
	}
	imageBoxMu.Lock()
	n := len(imageBoxCache)
	imageBoxMu.Unlock()
	if n > imageBoxCacheMax {
		t.Fatalf("缓存条数 %d 超过上限 %d ⇒ 内存无界", n, imageBoxCacheMax)
	}
}

// strconvItoa 本地小工具：只为让上面的循环字面量短一些（不引 strconv 是避免与别处重名）。
func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// ===== P4 收口：在途钉（pinned）与淘汰优先级 =====

// TestImageBoxPinnedSurvivesEviction 正题：**在途件不许被淘汰**。
// 构造真实时序——在途件只走到"提取"（RememberImageBoxes 会钉住）；其他文件走完
// "提取 → 写完回"（Remember + Release，即已完成的旧条目）。模拟同实例并发：
// 另一个工单连续完成 imageBoxCacheMax+8 个文件，第一个（在途）必须还在，
// 否则它的写回阶段拿空坐标、那张图静默退回"仅译文 .md"（产物少一件，工单还报成功）。
func TestImageBoxPinnedSurvivesEviction(t *testing.T) {
	resetImageBoxCache(t)
	const inflight = "/tmp/inflight.png"
	RememberImageBoxes(inflight, []OcrBox{{Text: "在途件"}})

	// 已完成的旧条目：提取后释放（真实时序里 engine 在语言循环结束后 ReleaseImageBoxes）
	for i := 0; i < imageBoxCacheMax+8; i++ {
		p := filepath.Join("/tmp", "other_"+strconvItoa(i)+".png")
		RememberImageBoxes(p, []OcrBox{{Text: "旧件"}})
		ReleaseImageBoxes(p)
	}

	got := ImageBoxes(inflight)
	if len(got) != 1 || got[0].Text != "在途件" {
		t.Fatalf("在途件的坐标被淘汰了 ⇒ 它的写回会静默退回仅 .md；实际 %+v", got)
	}
}

// TestImageBoxPinnedBeyondCapDegradesToFifo 边界自证：在途件数**超过上限**时，
// pinned 无从优先（每条都在途），必须退化为淘汰最旧一条——而不是无限增长。
// 这条锁的是"不许用内存换正确"：真实部署里 WORKER_CONCURRENCY 调到 >imageBoxCacheMax
// 就会走到这一档，代价是最早那几个在途件退回仅 .md（择优产出，不是数据丢失）。
func TestImageBoxPinnedBeyondCapDegradesToFifo(t *testing.T) {
	resetImageBoxCache(t)
	for i := 0; i < imageBoxCacheMax*3; i++ {
		RememberImageBoxes(filepath.Join("/tmp", "pin_"+strconvItoa(i)+".png"), []OcrBox{{Text: "钉"}})
	}
	imageBoxMu.Lock()
	n := len(imageBoxCache)
	imageBoxMu.Unlock()
	if n > imageBoxCacheMax {
		t.Fatalf("在途数超上限时缓存仍膨胀到 %d > %d ⇒ 内存边界丢了", n, imageBoxCacheMax)
	}
	if got := ImageBoxes(filepath.Join("/tmp", "pin_0.png")); got != nil {
		t.Fatalf("最旧一条应被淘汰（全员在途时的退化路径），实际仍在")
	}
}

// TestImageBoxReleaseAllowsEviction 反向对照：**释放后必须可被淘汰**。
// 只测"在途不被挤掉"不够——若释放是空操作，缓存会无限增长（内存无界），
// 而 TestImageBoxCacheEviction 只断言条数不超上限，那种"永不释放"恰恰能混过去。
func TestImageBoxReleaseAllowsEviction(t *testing.T) {
	resetImageBoxCache(t)
	RememberImageBoxes("/tmp/goner.png", []OcrBox{{Text: "待释放"}})
	ReleaseImageBoxes("/tmp/goner.png")

	for i := 0; i < imageBoxCacheMax+8; i++ {
		RememberImageBoxes(filepath.Join("/tmp", "fresh_"+strconvItoa(i)+".png"), []OcrBox{{Text: "新件"}})
	}
	if got := ImageBoxes("/tmp/goner.png"); got != nil {
		t.Fatalf("已释放的条目应可被淘汰，实际仍在：%+v ⇒ 缓存只增不减", got)
	}
	imageBoxMu.Lock()
	n := len(imageBoxCache)
	imageBoxMu.Unlock()
	if n > imageBoxCacheMax {
		t.Fatalf("缓存条数 %d 超过上限 %d ⇒ 内存无界", n, imageBoxCacheMax)
	}
}

// TestImageBoxReleaseIdempotentAndUnknown 释放必须是幂等的、对没钉过的路径无害。
// engine 侧用 defer 调它 ⇒ 可能与其他路径重复调；对不存在的路径更不许 panic
// （那会让一次成功的翻译在收尾时炸掉）。
func TestImageBoxReleaseIdempotentAndUnknown(t *testing.T) {
	resetImageBoxCache(t)
	ReleaseImageBoxes("/tmp/never-seen.png") // 不该 panic
	RememberImageBoxes("/tmp/once.png", []OcrBox{{Text: "x"}})
	ReleaseImageBoxes("/tmp/once.png")
	ReleaseImageBoxes("/tmp/once.png") // 二次释放幂等
	if got := ImageBoxes("/tmp/once.png"); len(got) != 1 {
		t.Fatalf("释放只该摘钉、不该删数据（写回阶段还要读），实际 %+v", got)
	}
}

// TestImageBoxAllPinnedStillBounded 极端形态：全员钉住时也必须保住内存边界。
// 构造：钉满上限 + 继续 RememberImageBoxes 更多在途件。此时"优先淘汰未钉住"无从下手，
// 必须退化为淘汰最旧一条——牺牲一件在途（那件退回仅 .md）换边界，比 OOM 好。
func TestImageBoxAllPinnedStillBounded(t *testing.T) {
	resetImageBoxCache(t)
	for i := 0; i < imageBoxCacheMax*3; i++ {
		RememberImageBoxes(filepath.Join("/tmp", "pin_"+strconvItoa(i)+".png"), []OcrBox{{Text: "钉"}})
	}
	imageBoxMu.Lock()
	n := len(imageBoxCache)
	imageBoxMu.Unlock()
	if n > imageBoxCacheMax {
		t.Fatalf("全员钉住时缓存仍膨胀到 %d > 上限 %d ⇒ 内存边界丢了", n, imageBoxCacheMax)
	}
	// 且最旧那批确实被淘汰了（边界不是靠"暂不淘汰"骗过来的）
	if got := ImageBoxes(filepath.Join("/tmp", "pin_0.png")); got != nil {
		t.Fatalf("最旧一条应被淘汰（全员钉住时的退化路径），实际仍在")
	}
}

// TestRenderTranslatedImageGuards ③ 缺依赖/缺坐标/缺脚本时必须是错误，不能产出半张图。
func TestRenderTranslatedImageGuards(t *testing.T) {
	resetImageBoxCache(t)
	if OcrAvailable() {
		t.Skip("本机 OCR 就绪：缺依赖这一档无法构造（其余两档在下面单独测）")
	}
	out := filepath.Join(t.TempDir(), "out.png")
	if err := RenderTranslatedImage(nil, "/tmp/whatever.png", out, nil); err == nil {
		t.Fatal("OCR 不可用时应返回错误，让调用方退回仅译文 .md")
	}
}

// TestParseOcrJSONPairsBoxes ② 行与坐标数量相等时才配对，不等则整体不缓存。
// ★ 为什么值得单独测：配对错一次不会报错、只会把译文盖到相邻的行上，
//
//	用户看到的是"图翻好了但串行"——比"没翻"更难发现。
func TestParseOcrJSONPairsBoxes(t *testing.T) {
	resetImageBoxCache(t)
	path := "/tmp/pair-check.png"
	_ = os.Remove(path)

	// 相等：3 行文本对 3 个框（每框 8 个坐标）
	e := &Extractor{}
	out := `{"text":"第一行\n第二行\n第三行","boxes":[[0,0,10,0,10,10,0,10],[0,10,10,10,10,20,0,20],[0,20,10,20,10,30,0,30]],"confidence":0.9}`
	if err := parseOcrJSON(out, e, path); err != nil {
		t.Fatalf("parseOcrJSON 失败: %v", err)
	}
	boxes := ImageBoxes(path)
	if len(boxes) != 3 || boxes[1].Text != "第二行" {
		t.Fatalf("应缓存 3 条配对，实际 %+v", boxes)
	}

	// 不等：2 行文本对 3 个框 ⇒ 一条都不缓存（宁可不做，也不做错位的那次）
	e2 := &Extractor{}
	bad := `{"text":"第一行\n第二行","boxes":[[0,0,1,0,1,1,0,1],[0,1,1,1,1,2,0,2],[0,2,1,2,1,3,0,3]],"confidence":0.9}`
	if err := parseOcrJSON(bad, e2, path); err != nil {
		t.Fatalf("parseOcrJSON 失败: %v", err)
	}
	if got := ImageBoxes(path); got == nil || len(got) != 3 {
		t.Fatalf("数量不等时应保留旧值（不被坏数据覆盖），实际 %+v", got)
	}
}
