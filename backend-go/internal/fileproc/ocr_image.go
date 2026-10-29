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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
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
	Text string `json:"text"` // 原文行文本
	// Box 文本框坐标。**现役 ocr_recognize.py 给的是四值矩形 [x0,y0,x1,y1]**
	// （`boxes_out.append([x0, y0, x1, y1])`，检测腿 _det_boxes 的产出形态）；
	// 八值四点（多边形，顺时针）也在渲染脚本的收款范围内，两种形态都可用。
	//
	// ★ 2026-10-17 校正：本字段注释原写「四点坐标 [x1,y1,…,y4]（顺时针）」，而渲染脚本
	// 当时按那句写死 `len(box) < 8` 判坏行 ⇒ 四值框**每行都被跳过** ⇒ 译后图＝重编码的原图
	// （工单与即时翻译两条链同时坏，用户报障）。判据已改为「≥4 且坐标数成对」，注释与实现对齐。
	Box []float64 `json:"box"`
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

// 原位回填失败的**档位名**（对外排障契约：与 ~/g 的 canned 分档同纪律，逐字钉进单测，禁止漂移）。
// 判据只问本包自己给出的档位，调用方（engine）据此写可行动的 GateWarnings。
const (
	ImageReasonCapabilityMissing = "capability_missing" // 缺依赖或 assets/ocr 权重
	ImageReasonNoBoxes           = "no_boxes"           // 进程内坐标缓存取空（OCR 未跑过/被淘汰）
	ImageReasonScriptMissing     = "script_missing"     // 回填脚本未随包部署
	ImageReasonWriteFailed       = "write_failed"       // 回填数据/产出暂存失败
	ImageReasonRenderFailed      = "render_failed"      // 子进程失败（字体缺失/超时等）
	ImageReasonEmptyOutput       = "empty_output"       // 子进程退出码 0 但产出空图
)

// ImageRenderError 带档位的原位回填失败（Reason 是机器可读档位，Msg 是人话）。
type ImageRenderError struct {
	Reason string
	Msg    string
}

// Error 实现 error 接口（对外排障只看到人话那一层）。
func (e *ImageRenderError) Error() string { return e.Msg }

// ImageRenderReason 取错误的档位名；非本包抛出的错误返回空串（调用方按"未知"处理，不硬猜）。
func ImageRenderReason(err error) string {
	if e, ok := err.(*ImageRenderError); ok {
		return e.Reason
	}
	return ""
}

// ImageRenderStats 一次原位回填的统计读数（G3-c：残留行必须在 GateWarnings 出声）。
type ImageRenderStats struct {
	Total   int // 送进回填的总行数（= 该图 OCR 坐标框数）
	Painted int // 真正带回译文的行数
	Residue int // 未译出、由原文顶上的行数（>0 时调用方须出声，不许静默）
	// Dropped 原样保留、**不重绘**的条数，两个来源同计这一个读数：
	//  ① 图形符号整行（箭头/三角/emoji，由回填脚本自报 dropped=，★ 2026-10-21 M9）；
	//  ② 单字符非汉字的行（开关/图标被误读成 `0`/`O`/`√` 那一族，见 IsIdentityOnlyText，★ 2026-10-29）。
	// 这些行不可译，原像素逐点保留才是正确交付；>0 时调用方须在 GateWarnings 出声，
	// 否则客户会以为"这一行漏翻了"而找不到成因。
	Dropped int
}

// IsIdentityOnlyText 判定一行 OCR 文本「没有可译内容、也不该被重绘」：去空白后恰好一个字符，且不是汉字。
//
// ★ 2026-10-29 用户实证（第二次复发）：开关按钮被识别成 `0`，原位回填把绿色开关整个盖成一个 "0"
// ——控件没了。这类行（数字/符号/单拉丁字母/单假名）本来就没有可译内容（AGENTS §19 族二「原样保留」），
// 而把它按原字形重画一遍没有任何收益：认对了＝与原像素逐点一致（白画），认错了＝抹掉底下的图形。
// ⇒ 回填阶段**整框不参与**（既不抹也不画），原图像素原样保留，这比「把原文画回去」严格更优。
//
// 为什么住在 fileproc 而不是 engine：判「画不画」与判「译不译」必须是同一把尺子，
// 而绘制发生在本包（engine.RenderSafeTranslations 薄委托到本函数，禁再各写一份）。
func IsIdentityOnlyText(s string) bool {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) != 1 {
		return false
	}
	return !unicode.Is(unicode.Han, rs[0])
}

// imageDroppedRe 从回填脚本的 INFO 行里取 dropped 计数。
// INFO 行形如 `INFO:painted=3/9 overflow=0 blank_skipped=5 dropped=1`（stderr，不参与业务判定）。
var imageDroppedRe = regexp.MustCompile(`dropped=(\d+)`)

// parseImageDropped 解析脚本自报的图形符号剔除条数；取不到回 0（旧脚本无此字段时静默为 0，
// 这是"读数缺失"而不是"确有 0 条"——调用方只在 >0 时出声，所以回 0 是安全的默认）。
func parseImageDropped(stderr string) int {
	m := imageDroppedRe.FindStringSubmatch(stderr)
	if len(m) != 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// RenderTranslatedImage 在原图上按 OCR 框坐标回填译文，产出 outPath 图片。
// 参数 tr 是 源文行 → 译文 映射；拿不到译文的行**保留原文**（吸收 render 脚本的同一口径）。
//
// ★ 失败即跳过：返回错误时调用方（engine）只记日志并退回「仅译文 .md」的既有形态，
//
//	绝不因为「图没画出来」把已经翻好的东西判死。错误一律是 *ImageRenderError（带档位）。
//
// ★ 返回 (统计, 错误)：统计里的 Residue 是「本轮没译出、由原文顶上」的行数——
//
//	那是客户要看见的读数，调用方据此写 GateWarnings（G3-c）。
func RenderTranslatedImage(ctx context.Context, srcPath, outPath string, tr map[string]string) (ImageRenderStats, error) {
	if !OcrAvailable() {
		return ImageRenderStats{}, &ImageRenderError{Reason: ImageReasonCapabilityMissing,
			Msg: "原位图片翻译不可用：OCR 能力未就绪（缺依赖或 assets/ocr/*.onnx）"}
	}
	boxes := ImageBoxes(srcPath)
	if len(boxes) == 0 {
		return ImageRenderStats{}, &ImageRenderError{Reason: ImageReasonNoBoxes,
			Msg: "原位图片翻译不可用：没有可用的文本框坐标（OCR 未跑过或结果为空）"}
	}
	render := imageRenderScriptPath()
	if _, err := os.Stat(render); err != nil {
		return ImageRenderStats{}, &ImageRenderError{Reason: ImageReasonScriptMissing,
			Msg: fmt.Sprintf("原位翻译脚本缺失：%s 未随二进制部署", imageRenderScriptName)}
	}
	lines := make([]imageRenderLine, 0, len(boxes))
	stats := ImageRenderStats{Total: len(boxes)}
	for _, b := range boxes {
		// ★ 2026-10-29：单字符非汉字（开关/图标被读成 `0`/`O`/`√`/`2`）**整框不参与回填**。
		//   旧形态在这里走「未译出 ⇒ 保留原文」那一支，把识别出的 `0` 画回开关位置——用户看到的就是
		//   「开关变成了 0」。这类行没有可译内容，也不算「未译出」（进 Residue 只会让客户收到一条
		//   指向不存在问题的质量提示），单独计入 Dropped（原样保留、不重绘）。
		if IsIdentityOnlyText(b.Text) {
			stats.Dropped++
			continue
		}
		t := tr[b.Text]
		if strings.TrimSpace(t) == "" {
			t = b.Text // 未译出的行保留原文：把原文抹掉却不写东西＝图片上出现一块空洞
			stats.Residue++
		} else {
			stats.Painted++
		}
		lines = append(lines, imageRenderLine{Text: b.Text, Box: b.Box, Tran: t})
	}
	payload, merr := json.Marshal(lines)
	if merr != nil {
		return stats, &ImageRenderError{Reason: ImageReasonWriteFailed,
			Msg: fmt.Sprintf("回填数据序列化失败: %v", merr)}
	}
	// 用一个临时文件传参而**不是命令行参数**：一行 echoed 行译文可以很长，
	// 命令行有长度上限且会让 ps 输出里出现整段客户内容（多租户机器上这是信息泄漏）。
	tmp := outPath + ".lines.json"
	if werr := os.WriteFile(tmp, payload, 0o644); werr != nil {
		return stats, &ImageRenderError{Reason: ImageReasonWriteFailed,
			Msg: fmt.Sprintf("回填数据暂存失败: %v", werr)}
	}
	defer os.Remove(tmp)
	if dir := filepath.Dir(outPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	bin, argv := wrapNice(pyBin(), []string{render, "render", srcPath, "--out", outPath, "--lines", tmp})
	_, stderr, err := runSubprocess(ctx, imageRenderTimeout(), bin, argv, nil)
	// ★ M9：剔除读数只解析、不参与成败判定（图已经画好了；这是给 GateWarnings 的计数）。
	stats.Dropped += parseImageDropped(string(stderr))
	if err != nil {
		if msg := friendlyOcrError(string(stderr)); msg != "" {
			return stats, &ImageRenderError{Reason: ImageReasonRenderFailed, Msg: msg}
		}
		return stats, &ImageRenderError{Reason: ImageReasonRenderFailed,
			Msg: fmt.Sprintf("原位图片翻译失败: %v\n%s", err, truncateTail(stderr))}
	}
	if info, serr := os.Stat(outPath); serr != nil || info.Size() == 0 {
		return stats, &ImageRenderError{Reason: ImageReasonEmptyOutput, Msg: "原位图片翻译产出了空图"}
	}
	return stats, nil
}
