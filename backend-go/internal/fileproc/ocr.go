// ============ ocr.go · 职责说明 ============
// OCR（图片文字识别）能力件：PP-OCR 的 ONNX 权重 + onnxruntime 推理，经 ocr_recognize.py
// 子命令壳把「图片 → 文本」投喂给既有翻译引擎（上游适配器定位，见
// 《改造方案_OCR与ASR接入_20261014_v2.md》§二：OCR/ASR/TTS 与文本/文件翻译同质，
// 只产输入，不碰流程编排）。
//
// 可插拔硬标准（用户 2026-10-14 立）：本文件 + ocr_recognize.py + assets/ocr/ 是 OCR 的全部家当；
// 现有文件只留两处接线（healthcheck.go 探测块、api/server.go 就绪字段）。
// 删掉这三组新文件与接线行 ⇒ 逐字节回退到 2026-10-14 前。
//
// 边界（刻意不做）：① 不建任务类型/队列/状态机（那是引擎的流程编排）；
// ② 不联网下载模型（私有化机器可能无外网，权重随 sync_fileproc_assets.sh 落盘并核对 sha256）；
// ③ P0 只交付探测与脚本壳，识别入口（ExtractImageOCR）在 P1a/P1b 落地，届时只追加本文件。
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
	"time"
)

// ocrPythonModules OCR 真实依赖的**导入名**清单（与 ocr_recognize.py 的 MODULES 同源维护，
// 两侧改一侧必红在 capabilities_p0_test.go 的协议断言上）。
// ★ pip 包名≠导入名的坑见 healthcheck.go 的 fpdf2 一条，这里一律按导入名写。
// ★ 刻意**不装 paddleocr/paddlepaddle**：PP-OCR 的 ONNX 权重由 onnxruntime 直接驱动，
//
//	预处理（resize/normalize/permute）numpy+Pillow 自足；paddlepaddle 是 GPU 框架，
//	装上多约 500MB+ 依赖树，纯 CPU 推理一分钱用不上（2026-10-14 选型口径）。
var ocrPythonModules = []string{"onnxruntime", "numpy", "PIL"}

// ocrAssetGlob OCR 模型资产判别串：assets/ocr/ 下至少一个 .onnx 权重才算资产落盘。
const ocrAssetGlob = "*.onnx"

// OcrScriptEnv ocr_recognize.py 路径覆盖环境变量（开发/测试用，同 ANYDOC_SCRIPT 约定）。
const OcrScriptEnv = "OCR_SCRIPT"

// OcrAvailable OCR 能力是否就绪（healthcheck 缓存）。
// ★ D5 口径：Python 模块与模型资产**两者齐备**才为真——只探模块不探资产，
//
//	就是「健康检查全绿、一进去必崩」的形态（pymupdf 那条教训的同族）。
func OcrAvailable() bool { return CheckHealth().OcrAvail }

// probeOcr 在指定 Python 解释器下探测 OCR 依赖。
// 返回 (就绪, 不就绪时的 actionable 告警)——告警话术在这里单源维护，
// healthcheck.go 只做两行转发，删本文件即失去编译（回退成本可见）。
func probeOcr(pythonPath string) (bool, string) {
	for _, m := range ocrPythonModules {
		if !checkPythonModule(pythonPath, m) {
			return false, "Python " + m + " 库未安装，图片/扫描件文字识别（OCR）不可用（pip install onnxruntime numpy Pillow）"
		}
	}
	if !ocrAssetsReady() {
		return false, "OCR 模型资产缺失（assets/ocr/*.onnx 未随包落盘），图片/扫描件文字识别不可用，取权重步骤见 assets/ocr/README.md"
	}
	return true, ""
}

// ocrAssetsReady OCR 模型资产是否落盘（脚本同目录 assets/ocr/ 至少一个 .onnx）。
func ocrAssetsReady() bool {
	matches, err := filepath.Glob(filepath.Join(ocrAssetsDir(), ocrAssetGlob))
	return err == nil && len(matches) > 0
}

// ocrScriptPath 定位 ocr_recognize.py（与可执行文件同目录，同 anydoc_md.py 部署约定；
// OCR_SCRIPT 环境变量覆盖，供开发/测试环境使用）。
func ocrScriptPath() string {
	if v := strings.TrimSpace(os.Getenv(OcrScriptEnv)); v != "" {
		return v
	}
	return filepath.Join(filepath.Dir(os.Args[0]), "ocr_recognize.py")
}

// ocrAssetsDir 定位 OCR 资产目录（相对**脚本**同目录，随 sync_fileproc_assets.sh 同步；
// 脚本被 OCR_SCRIPT 指到别处时资产跟着脚本走，不会与二进制目录错位）。
func ocrAssetsDir() string {
	return filepath.Join(filepath.Dir(ocrScriptPath()), "assets", "ocr")
}

// ocrTimeout OCR 识别墙钟预算（本地 ONNX 推理中位亚秒级；60s 富余含闸排队与超大图）。
func ocrTimeout() time.Duration {
	if v := os.Getenv("OCR_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 60 * time.Second
}

// ocrResult OCR 识别结果 JSON 形状（与 ocr_recognize.py image 子命令 stdout 一致）。
type ocrResult struct {
	Text         string      `json:"text"`          // 按阅读顺序拼好的多行文本（\n 分隔）
	Boxes        [][]float64 `json:"boxes"`         // 每行文本框（可选，保真比对应读用）
	Confs        []float64   `json:"confs"`         // 逐框识别置信度（与 Boxes 等长同序；老脚本无此字段）
	Confidence   float64     `json:"confidence"`    // 整体平均置信度（可选）
	SkippedPages int         `json:"skipped_pages"` // 因整件预算不足被跳过的页数（P4；老脚本为 0）
}

// ExtractImageOCR 图片 → 文本提取（P1a 识别本体）。
// 把识别出的文字逐行喂给提取器 e（去重/trim 由 Extractor.add 负责），进入既有翻译管线。
// ★ 译文第一：OCR 不可用（OcrAvailable()==false）或脚本缺失时返回**友好错误**，不把整单判死；
//
//	上游调用方（extract.go / engine）据此可降级或提示，但翻译资产（已识别文字）优先出。
//
// 识别失败（模型加载/图片损坏）由 friendlyOcrError 解析脚本 stderr 的 ERR:<Variant> 行 → 用户话术。
//
// ★ 2026-10-30（组 6 capsvc，方案 A）：**签名不变、内部薄替换**——先试常驻服务（warm_caps.go，
// 不进 procGate），常驻腿不可用才回落子进程（extractImageOCRSubprocess，仍走 procGate）。
// 两条腿的失败档位是同一套 `ERR:<Variant>`（识别逻辑只有 ocr_recognize.py 一份），
// 所以 friendlyOcrError 对两侧共用，用户可见话术逐字一致（A10）。
func ExtractImageOCR(ctx context.Context, path string, e *Extractor) error {
	if !OcrAvailable() {
		return fmt.Errorf("图片文字识别（OCR）不可用：服务器未安装 onnxruntime/numpy/Pillow 或未部署 OCR 模型资产（assets/ocr/*.onnx），取权重步骤见 assets/ocr/README.md")
	}
	// 常驻腿优先：服务端回了结果（成功或带 variant 的失败）就直接采用；
	// 只有 (nil,false)＝"常驻腿不可用"才回落子进程一次（fallback 计数已在 warmCall 里记好）。
	if res, ok := warmOcrImage(ctx, path, ocrTimeout()); ok {
		if res.Variant != "" {
			return ocrWarmError(res)
		}
		return parseOcrJSON(string(res.Stdout), e, path)
	}
	return extractImageOCRSubprocess(ctx, path, e)
}

// ocrWarmError 把常驻腿的失败档位渲染成与子进程路径**逐字相同**的用户话术。
// 服务端保证 stderr 里带 `ERR:<Variant>:` 行（与脚本同源），所以直接走 friendlyOcrError；
// 服务端自己产生的 Timeout（推理被预算掐断、无 stderr）按同一档位补一行再渲染——
// 绝不在这里造第二套话术（那是两把尺子）。
func ocrWarmError(res *warmResult) error {
	if msg := friendlyOcrError(string(res.Stderr)); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	if msg := friendlyOcrError("ERR:" + res.Variant + ":"); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return fmt.Errorf("OCR 识别失败（capsvc variant=%s）", res.Variant)
}

// extractImageOCRSubprocess 子进程回落腿（与改造前逐字节等价：脚本存在性检查 + 受控执行 + 解析）。
// 它仍然走 procGate（runSubprocess），因为这是一次几百 MB 的 Python 进程——与常驻腿分闸。
func extractImageOCRSubprocess(ctx context.Context, path string, e *Extractor) error {
	if _, err := os.Stat(ocrScriptPath()); err != nil {
		return fmt.Errorf("OCR 脚本缺失：ocr_recognize.py 未随二进制部署到 %s", filepath.Dir(os.Args[0]))
	}
	bin, argv := wrapNice(pyBin(), []string{ocrScriptPath(), "image", path})
	stdout, stderr, err := runSubprocess(ctx, ocrTimeout(), bin, argv, nil)
	if err != nil {
		if msg := friendlyOcrError(string(stderr)); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return fmt.Errorf("OCR 识别失败: %w\n%s", err, truncateTail(stderr))
	}
	return parseOcrJSON(string(stdout), e, path)
}

// parseOcrJSON 把 ocr_recognize.py 的 stdout JSON 解析为提取器文本行。
// 参数 srcPath：被识别的图片路径，仅用于把「行↔坐标」写进 P4 原位回填缓存（见 RememberImageBoxes）。
func parseOcrJSON(out string, e *Extractor, srcPath string) error {
	out = strings.TrimSpace(out)
	// 容错：脚本可能在前缀打印非 JSON 日志，取最后一个 {…} 块
	start := strings.Index(out, "{")
	if start < 0 {
		return fmt.Errorf("OCR 输出格式异常（未找到 JSON）: %s", truncateTail([]byte(out)))
	}
	var res ocrResult
	if err := json.Unmarshal([]byte(out[start:]), &res); err != nil {
		return fmt.Errorf("OCR 输出 JSON 解析失败: %w", err)
	}
	if strings.TrimSpace(res.Text) == "" {
		return fmt.Errorf("OCR 未识别出文字（图片可能无文字内容、或为手写/艺术字）")
	}
	// ★ 2026-10-28：识别结果先过清洗层（②），再交给上层（③ 在 engine 侧跑）。
	//   OcrDoc 是 ①→②→③→④ 之间唯一载体：这里构造并挂进在途队列，
	//   上层用 PendingOcrDoc 取、回写 Final 后用 CommitOcrDoc 重建映射。
	doc := BuildOcrDoc(srcPath, res)
	RememberOcrDoc(srcPath, doc)
	// 兼容腿：**先用 ② 的 Final 写一次提取器文本与回填映射**。
	// 从不跑 ③ 的调用方（单测、尚未接线的路径）行为仍正确；CommitOcrDoc 只是幂等覆盖这一次写入。
	for _, ln := range doc.Lines {
		e.add(ln.Final)
	}
	if _, st := CommitOcrDoc(srcPath, doc, e.Texts); !st.Mapping && st.Delivered > 0 {
		// 坐标缺失/不成对：整体跳过映射（原位回填退回"仅译文 .md"），绝不把译文盖到错误的框上。
		// 这里不再写映射，但文本已经通过 e.add 交付，符合"译文第一"。
		_ = st
	}
	return nil
}

// friendlyOcrError 解析 ocr_recognize.py stderr 的 ERR:<Variant>:<msg> 行 → 面向用户话术；
// 无法识别时返回 ""（由调用方给原始错误）。
func friendlyOcrError(stderr string) string {
	line := ""
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "ERR:") {
			line = l
			break
		}
	}
	if line == "" {
		return ""
	}
	variant := strings.SplitN(strings.TrimPrefix(line, "ERR:"), ":", 2)[0]
	switch variant {
	case "ModelLoad", "Asset":
		return "OCR 模型资产缺失或加载失败：assets/ocr/*.onnx 未随包落盘，取权重步骤见 assets/ocr/README.md"
	case "ImageOpen", "ImageCorrupt":
		return "图片无法读取或已损坏，请更换图片后重试"
	case "Unsupported", "ImageFormat":
		return "不支持的图片格式，请使用 png/jpg/jpeg/bmp/tif/tiff/webp/heic"
	case "Empty":
		return "OCR 未识别出文字（图片可能无文字内容、或为手写/艺术字）"
	case "Timeout":
		// 预算不足导致的跳过必须与"图里真没字"分开报：前者是运维档位问题，后者是内容问题。
		return "文档识别超时（超出单件预算，已按可用页交付）；可调大 OCR_TIMEOUT_SEC 后重试"
	case "NothingPainted":
		return "图片原位回填未产出（没有一行拿到可用文本框坐标），已退回仅译文文档"
	// ★ 2026-10-17：原位回填脚本（ocr_translate_image.py）的五个档位也收进来。
	//   之前它们落到 default ⇒ 返回 ""，调用方只能把脚本 stderr 原文（含内部路径）抛给日志；
	//   补上后 RenderTranslatedImage 的错误就是给运维看的人话。
	case "Font":
		return "图片原位回填失败：服务器上没有可用字体（随包 assets/fonts 缺失且系统字体候选也取不到）"
	case "Lines":
		return "图片原位回填失败：行数据读取失败"
	case "Draw":
		return "图片原位回填失败：绘制阶段出错"
	case "Out":
		return "图片原位回填失败：产物写入失败"
	case "Import":
		return "图片文字识别（OCR）依赖未就绪：服务器未安装 onnxruntime/numpy/Pillow"
	default:
		return ""
	}
}
