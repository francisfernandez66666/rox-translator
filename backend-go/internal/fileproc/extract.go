// ============ extract.go · 职责说明 ============
// fileproc 包办公文档文本提取入口。
// 按扩展名分发到 docx/pptx/xlsx 提取器。
// Extractor 负责去重与规整（trim、跳过空串），并提供句末标点判断（endsSentence），
// 供 pptx 段落分组（连续不以句末标点结尾的段落合并为一条翻译单元）使用。
// =============================================
package fileproc

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Extractor 从 office 文件中提取文本片段（复刻 lib.file_extract_texts）
type Extractor struct {
	Texts []string        // 提取出的文本片段列表（去重后）
	seen  map[string]bool // 已见文本集合（内部去重用）
}

// add 添加一条文本：trim 空白、跳过空串与重复项。
// 参数：text=待添加文本；无返回值（内部维护去重集合）。
func (e *Extractor) add(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return // 空文本不入库
	}
	if e.seen == nil {
		e.seen = map[string]bool{}
	}
	if e.seen[text] {
		return // 重复文本跳过（文档中相同片段只翻译一次）
	}
	e.seen[text] = true
	e.Texts = append(e.Texts, text)
}

// endsSentence 判断字符串是否以句末标点结尾（中文/英文/常见标点）。
// 参数：s=待判断字符串；空串视为句末（用于分组边界）；返回是否以句末标点结尾。
func endsSentence(s string) bool {
	if s == "" {
		return true
	}
	last := s[len(s)-1:]
	return strings.Contains("。！？.!?:：;；…", last)
}

// ExtractTexts 提取文本，按扩展名分发。
// 参数：filepath=文件路径；支持 .docx/.pptx/.xlsx/.pdf/.txt/.csv/.srt/.vtt/.md/.json/.yaml/.yml、
// 图片格式（.png/.jpg/.jpeg/.bmp/.tif/.tiff/.webp/.heic，P1a 经 OCR 投喂翻译引擎）
// 与音频格式（.mp3/.m4a/.wav/.ogg/.flac/.aac/.opus，P2 经 ASR 转写投喂翻译引擎）；
// 返回提取的文本片段列表。
func ExtractTexts(filepath string) ([]string, error) {
	ext := strings.ToLower(filepathExt(filepath))
	e := &Extractor{}
	var err error
	switch ext {
	case ".docx":
		err = extractDocx(filepath, e) // 解析 docx
	case ".pptx":
		err = extractPptx(filepath, e) // 解析 pptx
	case ".xlsx":
		err = extractXlsx(filepath, e) // 解析 xlsx
	case ".pdf":
		err = extractPdfText(filepath, e) // pdf（降级路径见 pdf.go，含空层 OCR 降级）
	case ".txt", ".csv":
		err = extractLines(filepath, e, func(l string) string { return strings.TrimSpace(l) }) // 整行入库
	case ".srt", ".vtt":
		err = extractSubtitle(filepath, e) // 字幕块
	case ".md":
		err = extractMarkdown(filepath, e) // Markdown 正文
	case ".json":
		err = extractJSON(filepath, e) // JSON 字符串值
	case ".yaml", ".yml":
		err = extractYAML(filepath, e) // YAML 标量值
	case ".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff", ".webp", ".heic":
		// ★ P1a：图片经 OCR 识别文字后投喂翻译引擎（上游适配器定位）。
		// OcrAvailable()==false 时 ExtractImageOCR 返回友好错误，不把整单判死。
		err = ExtractImageOCR(context.Background(), filepath, e)
	case ".mp3", ".m4a", ".wav", ".ogg", ".flac", ".aac", ".opus":
		// ★ P2：音频经 ASR 转写成文本后投喂翻译引擎（上游适配器定位，与 OCR 同质地兄弟）。
		// AsrAvailable()==false 时 ExtractAudioASR 返回友好错误，不把整单判死。
		// 时间轴由 TranscribeAudio 缓存下来，写回阶段经 AudioCues 取回生成 .srt。
		err = ExtractAudioASR(context.Background(), filepath, e)
	default:
		return nil, fmt.Errorf("不支持的格式: %s", ext)
	}
	if err != nil {
		return nil, err
	}
	return e.Texts, nil
}

// filepathExt 获取文件扩展名（封装 filepath.Ext）。
func filepathExt(p string) string {
	return filepath.Ext(p)
}
