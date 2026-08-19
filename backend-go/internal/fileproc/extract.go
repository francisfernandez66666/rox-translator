package fileproc

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Extractor 从 office 文件中提取文本片段（复刻 lib.file_extract_texts）
type Extractor struct {
	Texts []string
	seen  map[string]bool
}

func (e *Extractor) add(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if e.seen == nil {
		e.seen = map[string]bool{}
	}
	if e.seen[text] {
		return
	}
	e.seen[text] = true
	e.Texts = append(e.Texts, text)
}

// endsSentence 是否以句末标点结尾
func endsSentence(s string) bool {
	if s == "" {
		return true
	}
	last := s[len(s)-1:]
	return strings.Contains("。！？.!?:：;；…", last)
}

// ExtractTexts 提取文本，按扩展名分发
func ExtractTexts(filepath string) ([]string, error) {
	ext := strings.ToLower(filepathExt(filepath))
	e := &Extractor{}
	var err error
	switch ext {
	case ".docx":
		err = extractDocx(filepath, e)
	case ".pptx":
		err = extractPptx(filepath, e)
	case ".xlsx":
		err = extractXlsx(filepath, e)
	default:
		return nil, fmt.Errorf("不支持的格式: %s", ext)
	}
	if err != nil {
		return nil, err
	}
	return e.Texts, nil
}

func filepathExt(p string) string {
	return filepath.Ext(p)
}
