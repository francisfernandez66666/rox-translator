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
	Text       string      `json:"text"`       // 按阅读顺序拼好的多行文本（\n 分隔）
	Boxes      [][]float64 `json:"boxes"`      // 每行文本框（可选，保真比对应读用）
	Confidence float64     `json:"confidence"` // 整体平均置信度（可选）
}

// ExtractImageOCR 图片 → 文本提取（P1a 识别本体）。
// 把识别出的文字逐行喂给提取器 e（去重/trim 由 Extractor.add 负责），进入既有翻译管线。
// ★ 译文第一：OCR 不可用（OcrAvailable()==false）或脚本缺失时返回**友好错误**，不把整单判死；
//
//	上游调用方（extract.go / engine）据此可降级或提示，但翻译资产（已识别文字）优先出。
//
// 识别失败（模型加载/图片损坏）由 friendlyOcrError 解析脚本 stderr 的 ERR:<Variant> 行 → 用户话术。
func ExtractImageOCR(ctx context.Context, path string, e *Extractor) error {
	if !OcrAvailable() {
		return fmt.Errorf("图片文字识别（OCR）不可用：服务器未安装 onnxruntime/numpy/Pillow 或未部署 OCR 模型资产（assets/ocr/*.onnx），取权重步骤见 assets/ocr/README.md")
	}
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
	lines := strings.Split(res.Text, "\n")
	for _, line := range lines {
		e.add(line)
	}
	// ★ P4：把「行文本 ↔ 坐标」这一对关系留下来（原位回填要用同一份坐标，见 ocr_image.go 头注）。
	//   只有两者数量**严格相等**时才敢配对——数量对不上说明脚本那侧的拼法变了，
	//   这时宁可不缓存（原位回填整体跳过）也不能把译文盖到错误的框上。
	if len(res.Boxes) == len(lines) {
		boxes := make([]OcrBox, 0, len(lines))
		for i, ln := range lines {
			if strings.TrimSpace(ln) == "" {
				continue
			}
			boxes = append(boxes, OcrBox{Text: ln, Box: res.Boxes[i]})
		}
		RememberImageBoxes(ocrPathOfLastRun, boxes)
	}
	return nil
}

// ocrPathOfLastRun 供 parseOcrJSON 回填坐标缓存用的「本次识别的图片路径」。
// 为什么是包级变量而不是参数：parseOcrJSON 的签名被 ExtractImageOCR 与单测共用，
// 为了加一个纯可选的旁路缓存去改所有调用点不划算；它是**单线程写入、紧邻读取**的
// 短生命周期值，语义上等价于一次调用的上下文。
var ocrPathOfLastRun string

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
	case "Import":
		return "图片文字识别（OCR）依赖未就绪：服务器未安装 onnxruntime/numpy/Pillow"
	default:
		return ""
	}
}
