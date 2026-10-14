// ============ ocr_image.go · 职责说明 ============
// P4「图片原位翻译」能力件：把图片 OCR 出的**文本框坐标**连同译文一起交给
// ocr_translate_image.py，在原图上抹掉原文、按原位置重写译文，产出一张**译好的图**。
//
// ★ 它为什么是另一组文件而不是塞进 ocr.go（可插拔硬标准）：
//
//	ocr.go 那条腿的交付是**文本**（投喂翻译引擎，产出译文 .md）；
//	本文件的交付是**图**（在原图上回填译文）。同一个 OCR 上游，两条不同的产出形态。
//
//	★★ **本文件目前是「只有能力腿、没有交付腿」的形态（2026-10-15 收口批实读）**：
//	`RenderTranslatedImage` 与 `ImageBoxes` 在全仓**零调用方**（连测试都没有），
//	engine/file.go 里**不存在**那个 image_inplace 分支——写回链只在计划稿 §五 的 P4 里，
//	尚未实施（同一批的 `RememberImageBoxes` 有真调用方＝`ocr.go:157`，所以框坐标缓存那条腿是活的，
//	只是暂时没人来取）。所以这里的回退口径就是最朴素的"删本文件＋删 `ocr_translate_image.py`"，
//	**不需要**动 engine（没有分支可动）。别把这段当成"分支被谁摘掉了"去找，它从没长出来过。
//	⇒ 排障口径：现网图片件的产物只有译文 .md（P1a 形态），不是缺陷；
//	  要拿到"译好的图"是**对外交付契约改动**（客户看到的产物集合变了），已进《PROGRESS》§十四 待决策账等拍板。
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
const imageBoxCacheMax = 32

// imageBoxCache 是「提取期算好的 OCR 行坐标」这份进程内缓存：
// 提取（OCR）与回填（绘制）是两个调用口，坐标只该算一次；同 asr.go 那份缓存一样
// 只在内存留最近 imageBoxCacheMax 条，不落库、跨进程不共享（回填失败宁可重算也不许拿旧坐标画新图）。
var (
	imageBoxMu    sync.Mutex // 保护下面这片 slice（并发建单/并发回填都会碰）
	imageBoxCache []imageBoxEntry
)

// imageBoxEntry 一条缓存：某图片路径 → 它那一版 OCR 的行坐标与原文。
type imageBoxEntry struct {
	path string   // 图片在本机的绝对路径（缓存键）
	box  []OcrBox // 与提取期那次读数同序，回填时按序号对齐译文行
}

// RememberImageBoxes 记一份某图片的 OCR 行坐标（覆盖同 key 旧值，超上限淘汰最早的一条）。
func RememberImageBoxes(path string, boxes []OcrBox) {
	imageBoxMu.Lock()
	defer imageBoxMu.Unlock()
	for i := range imageBoxCache {
		if imageBoxCache[i].path == path {
			imageBoxCache[i].box = boxes
			return
		}
	}
	imageBoxCache = append(imageBoxCache, imageBoxEntry{path: path, box: boxes})
	if len(imageBoxCache) > imageBoxCacheMax {
		imageBoxCache = imageBoxCache[1:]
	}
}

// ImageBoxes 取回某图片已识别出的行坐标（写回阶段用）；未命中返回 nil。
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
