package fileproc

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ============ pptx 解析（slide 文本 + 表格） ============

// pptxTextEl 宽松结构：解析 sp 的 txBody 和表格
type pptxTextEl struct {
	Paragraphs []pptxPara `xml:"p"`
}

type pptxPara struct {
	Runs []pptxRun `xml:"r"`
}

type pptxRun struct {
	Text string `xml:"t"`
}

// 用宽松 Unmarshal：先取所有 <a:t> 文本 + 段落边界
// 这里直接用正则扫描 a:t 文本，再按 p 边界切分

type pptxShapeRaw struct {
	XMLName xml.Name   `xml:"sp"`
	Content []pptxText `xml:",any"`
}

type pptxText struct {
	XMLName xml.Name
	Content string `xml:",chardata"`
}

func extractPptx(path string, e *Extractor) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("pptx 打开失败: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		name := f.Name
		if !strings.HasPrefix(name, "ppt/slides/slide") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		data, err := readZipEntry(zr, name)
		if err != nil {
			continue
		}
		extractPptxSlide(data, e)
	}
	return nil
}

// extractPptxSlide 解析单个 slide 的文本
func extractPptxSlide(data []byte, e *Extractor) {
	// 用正则按段落块 <a:p>...</a:p> 提取
	paraRe := regexp.MustCompile(`<a:p>(.*?)</a:p>`)
	tTextRe := regexp.MustCompile(`<a:t[^>]*>(.*?)</a:t>`)

	matches := paraRe.FindAllStringSubmatch(string(data), -1)
	var merged []string
	var current strings.Builder
	for _, m := range matches {
		var sb strings.Builder
		for _, t := range tTextRe.FindAllStringSubmatch(m[1], -1) {
			sb.WriteString(t[1])
		}
		text := strings.TrimSpace(sb.String())
		if text == "" {
			continue
		}
		if current.Len() > 0 && !endsSentence(current.String()) {
			current.WriteString("\n")
			current.WriteString(text)
		} else {
			if current.Len() > 0 {
				merged = append(merged, strings.TrimSpace(current.String()))
			}
			current.Reset()
			current.WriteString(text)
		}
	}
	if current.Len() > 0 {
		merged = append(merged, strings.TrimSpace(current.String()))
	}
	for _, m := range merged {
		e.add(m)
	}
}

// ============ pptx 写回 ============

// ApplyPptx 写回 pptx：按段落原文映射替换文本
func ApplyPptx(path, outPath string, translations map[string]string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()

	osFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer osFile.Close()
	zw := zip.NewWriter(osFile)

	for _, f := range zr.File {
		data, err := readZipEntry(zr, f.Name)
		if err != nil {
			continue
		}
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			data = translatePptxXML(data, translations)
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return osFile.Close()
}

var (
	pptParaRe = regexp.MustCompile(`<a:p>.*?</a:p>`)
	aTextRe   = regexp.MustCompile(`(<a:t[^>]*>)([^<]*)(</a:t>)`)
)

// translatePptxXML 对每个 <a:p> 段落做替换
func translatePptxXML(data []byte, translations map[string]string) []byte {
	s := string(data)
	out := pptParaRe.ReplaceAllStringFunc(s, func(para string) string {
		// 提取段落纯文本
		var plain strings.Builder
		for _, m := range aTextRe.FindAllStringSubmatch(para, -1) {
			plain.WriteString(m[2])
		}
		orig := strings.TrimSpace(plain.String())
		if orig == "" {
			return para
		}
		translated, ok := translations[orig]
		if !ok || translated == "" {
			return para
		}
		// 替换第一个 a:t，清空其余
		mm := aTextRe.FindAllStringSubmatchIndex(para, -1)
		if len(mm) == 0 {
			return para
		}
		first := mm[0]
		newPara := para[:first[2]] + translated + para[first[3]:]
		// 清空第一个之后的所有 a:t 内容
		rest := aTextRe.ReplaceAllString(newPara, `${1}${3}`)
		return rest
	})
	return []byte(out)
}
