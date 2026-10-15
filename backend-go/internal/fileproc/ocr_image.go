// ============ ocr_image.go · 职责说明 ============
// P4「图片原位翻译」能力件：把图片 OCR 出的**文本框坐标**连同译文一起交给
// ocr_translate_image.py，在原图上抹掉原文、按原位置重写译文，产出一张**译好的图**。
//
// ★ 它为什么是另一组文件而不是塞进 ocr.go（可插拔硬标准）：
//
//	ocr.go 那条腿的交付是**文本**（投喂翻译引擎，产出译文 .md）；
//	本文件的交付是**图**（在原图上回填译文）。同一个 OCR 上游，两条不同的产出形态。
//
//	★★ **交付腿已接线（2026-10-15 P4 收口）**：`engine/file.go` 的 image 分支在译文 .md 之后
//	追加调用 `RenderTranslatedImage`（`file.go:955`），`ImageBoxes` 由它内部取用（`ocr_image.go:139`），
//	上游那端 `RememberImageBoxes` 的调用点在 `ocr.go:158`——三处都有真调用方，不再是"没人来取"。
//	所以回退口径升级为：删本文件＋删 `ocr_translate_image.py`＋删 file.go 里那个 if 块，
//	三处一起删才回到 P1a 形态（只删前两处会留下一个编译不过的调用点）。
//	⇒ 排障口径：原位图是**择优产出**，渲染失败（缺中文字体／坐标没缓存住／行长溢出）只跳过该产物，
//	  `observability.Warn` 记一行，产物回到「译文 .md」（P1a 既有形态），**不判整单失败**——译文第一。
//
// ★ 为什么要有每坐标缓存（imageBoxCache，形制照 asr.go 的 asrCueCache）：
//
//	OCR 已经在第 1 步跑过一次并拿到了框坐标，第 3 步写回若再跑一次，
//	两次 VAD/切分不一定完全对齐，而且白付一倍 CPU。**必须用同一份坐标**才能保证
//	「第 1 步送去翻译的那行」与「第 3 步要覆盖的那个框」是同一个东西。
//
// ★ 译文第一：原位翻译是**择优产出**。抹位置失败/字体缺失/行长溢出
//
//	⇒ 跳过这张图，产物回到「译文 .md」（P1a 既有形态），工单状态不受影响。
//
// =============================================
package fileproc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ImageRenderScriptEnv ocr_translate_image.py 路径覆盖环境变量（开发/测试用）。
const ImageRenderScriptEnv = "OCR_IMAGE_SCRIPT"

// imageRenderScriptName 远端/本机同一份图像处理脚本的文件名（与 fileproc 其余脚本命名同源）。
// ★ 出现在派发 argv 里时它就是 argv[0] 那一个脚本名，只许由调用方或 DispatchRun 补一次，
//
//	两边各写一次会被远端当成子命令（AGENTS §一·12 补丁五那条真机首跑抓到的双写）。
const imageRenderScriptName = "ocr_translate_image.py"

// imageRenderScriptPath 定位 ocr_translate_image.py（与可执行文件同目录，同其余脚本的部署约定）。
func imageRenderScriptPath() string {
	if v := strings.TrimSpace(os.Getenv(ImageRenderScriptEnv)); v != "" {
		return v
	}
	return filepath.Join(filepath.Dir(os.Args[0]), imageRenderScriptName)
}

// imageRenderTimeout 原位翻译墙钟预算（纯 CPU：一次检测已在提取期做过，这里只做图像处理+绘制）。
func imageRenderTimeout() time.Duration {
	if v := os.Getenv("OCR_IMAGE_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 120 * time.Second
}

// OcrBox OCR 一行的文本框与原文（用于原位回填，坐标单位＝**原图像素**）。
type OcrBox struct {
	Text string    `json:"text"` // 原文行文本
	Box  []float64 `json:"box"`  // 四点坐标 [x1,y1,x2,y2,x3,y3,x4,y4]（顺时针）
}

// ===== 提取期坐标缓存（形制与语义照 asr.go 的 asrCueCache，不再重复论证） =====

// imageBoxCacheMax 缓存条数上限。32 是按 worker 并发默认 4（api/server.go 的 WORKER_CONCURRENCY）
// 留的 8 倍余量；真正保证"在途件不被挤掉"的是下面的 pinned 标记，不是这个数字——
// 调大 WORKER_CONCURRENCY 时不必同步改它，pinned 会兜住。
const imageBoxCacheMax = 32

// imageBoxCache 是「提取期算好的 OCR 行坐标」这份进程内缓存：
// 提取（OCR）与回填（绘制）是两个调用口，坐标只该算一次；同 asr.go 那份缓存一样
// 只在内存留最近 imageBoxCacheMax 条，不落库、跨进程不共享（回填失败宁可重算也不许拿旧坐标画新图）。
// ★ 为什么**不需要**持久载体：提取与回填在同一个函数调用里（engine/file.go 的 HandleFile，
//
//	L375/377/392 提取 → L942 回填），中间只隔 LLM 调用，不存在"提到 A 进程、写回 B 进程"的形态。
//	早期注释写的"跨实例即失效"是把还没发生过的部署形态当成现存风险——真正会挤掉坐标的是
//	**同实例并发**（另一个工单也在提取），那条由 pinned 标记解决。
var (
	imageBoxMu    sync.Mutex // 保护下面这片 slice（并发建单/并发回填都会碰）
	imageBoxCache []imageBoxEntry
)

// imageBoxEntry 一条缓存：某图片路径 → 它那一版 OCR 的行坐标与原文。
type imageBoxEntry struct {
	path   string   // 图片在本机的绝对路径（缓存键）
	box    []OcrBox // 与提取期那次读数同序，回填时按序号对齐译文行
	pinned bool     // 是否在途（已提取、尚未写完回）——在途条目不参与淘汰
}

// RememberImageBoxes 记一份某图片的 OCR 行坐标，并**钉住**它（标记在途）。
// 钉住而不是只入列：提取与写回之间隔着一整轮 LLM 调用，期间同实例其他工单也在提取，
// 若按纯 FIFO 淘汰，>imageBoxCacheMax 个在途图片就会把还没写回的坐标挤掉——
// 表现是那张图静默退回「仅译文 .md」（不判整单失败，但产物少一件）。
func RememberImageBoxes(path string, boxes []OcrBox) {
	imageBoxMu.Lock()
	defer imageBoxMu.Unlock()
	for i := range imageBoxCache {
		if imageBoxCache[i].path == path {
			imageBoxCache[i].box = boxes
			imageBoxCache[i].pinned = true
			return
		}
	}
	imageBoxCache = append(imageBoxCache, imageBoxEntry{path: path, box: boxes, pinned: true})
	evictImageBoxesLocked()
}

// evictImageBoxesLocked 超上限时淘汰。**优先淘汰未钉住的**（已写完回的旧条目）；
// 全是钉住条目时仍淘汰最旧的一条——那一条大概率是更早的在途件，牺牲它换边界，
// 而牺牲的代价只是那一张图退回仅 .md（择优产出，不是数据丢失）。调用方须持锁。
func evictImageBoxesLocked() {
	for len(imageBoxCache) > imageBoxCacheMax {
		idx := -1
		for i := range imageBoxCache {
			if !imageBoxCache[i].pinned {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = 0 // 没有可淘汰的未钉住条目：退化为淘汰最旧一条，保住内存边界
		}
		imageBoxCache = append(imageBoxCache[:idx], imageBoxCache[idx+1:]...)
	}
}

// ReleaseImageBoxes 解除某图片的在途钉（写回阶段结束后调用）。
// 不调也不会漏数据，只会让那条记录一直占着"不可淘汰"名额 ⇒ 高并发下最终拖到全员钉住、
// 淘汰退化成纯 FIFO。所以它是**资源释放**而不是正确性依赖，调用方在 defer 里调最稳。
func ReleaseImageBoxes(path string) {
	imageBoxMu.Lock()
	defer imageBoxMu.Unlock()
	for i := range imageBoxCache {
		if imageBoxCache[i].path == path {
			imageBoxCache[i].pinned = false
			return
		}
	}
}

// ImageBoxes 取回某图片已识别出的行坐标（写回阶段用）；未命中返回 nil。
// ★ 只读不销账：多目标语言时写回分支会对同一张图调用多次（每语言一次），
//
//	所以这里不能"取一次就释放"——释放由调用方在语言循环结束后统一做（ReleaseImageBoxes）。
func ImageBoxes(path string) []OcrBox {
	imageBoxMu.Lock()
	defer imageBoxMu.Unlock()
	for i := len(imageBoxCache) - 1; i >= 0; i-- {
		if imageBoxCache[i].path == path {
			return imageBoxCache[i].box
		}
	}
	return nil
}

// imageRenderLine 传给渲染脚本的一行：原坐标 + 该行译文。
type imageRenderLine struct {
	Text string    `json:"text"`
	Box  []float64 `json:"box"`
	Tran string    `json:"tran"`
}

// RenderTranslatedImage 在原图上按 OCR 框坐标回填译文，产出 outPath 图片。
// 参数 tr 是 源文行 → 译文 映射；拿不到译文的行**保留原文**（吸收 render 脚本的同一口径）。
//
// ★ 失败即跳过：返回错误时调用方（engine）只记日志并退回「仅译文 .md」的既有形态，
//
//	绝不因为「图没画出来」把已经翻好的东西判死。
func RenderTranslatedImage(ctx context.Context, srcPath, outPath string, tr map[string]string) error {
	if !OcrAvailable() {
		return fmt.Errorf("原位图片翻译不可用：OCR 能力未就绪（缺依赖或 assets/ocr/*.onnx）")
	}
	boxes := ImageBoxes(srcPath)
	if len(boxes) == 0 {
		return fmt.Errorf("原位图片翻译不可用：没有可用的文本框坐标（OCR 未跑过或结果为空）")
	}
	render := imageRenderScriptPath()
	if _, err := os.Stat(render); err != nil {
		return fmt.Errorf("原位翻译脚本缺失：%s 未随二进制部署", imageRenderScriptName)
	}
	lines := make([]imageRenderLine, 0, len(boxes))
	for _, b := range boxes {
		t := tr[b.Text]
		if strings.TrimSpace(t) == "" {
			t = b.Text // 未译出的行保留原文：把原文抹掉却不写东西＝图片上出现一块空洞
		}
		lines = append(lines, imageRenderLine{Text: b.Text, Box: b.Box, Tran: t})
	}
	payload, merr := json.Marshal(lines)
	if merr != nil {
		return fmt.Errorf("回填数据序列化失败: %w", merr)
	}
	// 用一个临时文件传参而**不是命令行参数**：一行 echoed 行译文可以很长，
	// 命令行有长度上限且会让 ps 输出里出现整段客户内容（多租户机器上这是信息泄漏）。
	tmp := outPath + ".lines.json"
	if werr := os.WriteFile(tmp, payload, 0o644); werr != nil {
		return fmt.Errorf("回填数据暂存失败: %w", werr)
	}
	defer os.Remove(tmp)
	if dir := filepath.Dir(outPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	bin, argv := wrapNice(pyBin(), []string{render, "render", srcPath, "--out", outPath, "--lines", tmp})
	_, stderr, err := runSubprocess(ctx, imageRenderTimeout(), bin, argv, nil)
	if err != nil {
		if msg := friendlyOcrError(string(stderr)); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return fmt.Errorf("原位图片翻译失败: %w\n%s", err, truncateTail(stderr))
	}
	if info, serr := os.Stat(outPath); serr != nil || info.Size() == 0 {
		return fmt.Errorf("原位图片翻译产出了空图")
	}
	return nil
}
