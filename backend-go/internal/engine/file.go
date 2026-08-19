package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"translator/internal/config"
	"translator/internal/fileproc"
)

// FileTranslateResult 文件翻译结果
type FileTranslateResult struct {
	Skill string            `json:"skill"`
	Reply string            `json:"reply"`
	Data  FileTranslateData `json:"data"`
	Files []string          `json:"files"`
	Error string            `json:"error"`
}

// FileTranslateData 文件翻译数据
type FileTranslateData struct {
	TotalTexts  int               `json:"total_texts"`
	TargetLangs []string          `json:"target_langs"`
	LangNames   map[string]string `json:"lang_names"`
	KBHits      int               `json:"kb_hits"`
	ModelHits   int               `json:"model_hits"`
	FileContext string            `json:"file_context"`
}

// HandleFile 文件翻译主流程（复刻 skill.py _handle_file_translate）
func (e *Engine) HandleFile(ctx context.Context, filePath string, options map[string]interface{}, prog Progress) *FileTranslateResult {
	if prog == nil {
		prog = func(string, int, int) {}
	}
	// ★ 租户可用性校验
	if err := e.tenantOK(ctx); err != nil {
		return &FileTranslateResult{Skill: "translation", Error: err.Error()}
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".docx" && ext != ".pptx" && ext != ".xlsx" {
		return &FileTranslateResult{Skill: "translation", Error: "仅支持 docx/pptx/xlsx 文件"}
	}
	if _, err := os.Stat(filePath); err != nil {
		return &FileTranslateResult{Skill: "translation", Error: "文件不存在或无法读取"}
	}

	langsRaw := TargetLangsFromOptions(options)
	if len(langsRaw) == 0 {
		langsRaw = []string{"en"}
	}
	kbLangs, directOther, _ := SplitOptions(langsRaw)
	finalLangs := append([]string{}, kbLangs...)
	finalLangs = append(finalLangs, directOther...)

	// 第1步：理解文件结构
	prog("第1步/3：理解文件结构...", 1, 3)
	texts, err := fileproc.ExtractTexts(filePath)
	if err != nil || len(texts) == 0 {
		return &FileTranslateResult{Skill: "translation", Error: "无法从文件提取文本或文件为空"}
	}

	// 语言名映射
	langNames := map[string]string{}
	for _, lc := range finalLangs {
		langNames[lc] = config.LangNames[lc]
	}
	label := "文档"
	if len(finalLangs) == 1 {
		label = config.LangNames[finalLangs[0]]
		if label == "" {
			label = finalLangs[0]
		}
	}

	// 第2步：翻译
	kbHits := 0
	modelHits := 0

	// 语言 → 原文 → 译文
	langTranslations := map[string]map[string]string{}

	translationMu := sync.Mutex{}
	kbHitsMu := sync.Mutex{}
	modelHitsMu := sync.Mutex{}
	addTrans := func(lc, orig, translated string) {
		translationMu.Lock()
		defer translationMu.Unlock()
		if langTranslations[lc] == nil {
			langTranslations[lc] = map[string]string{}
		}
		langTranslations[lc][orig] = translated
	}
	addKBHit := func() {
		kbHitsMu.Lock()
		kbHits++
		kbHitsMu.Unlock()
	}
	addModelHit := func() {
		modelHitsMu.Lock()
		modelHits++
		modelHitsMu.Unlock()
	}

	// KB 语言：先 KB 直配，未命中的批量模型
	var wg sync.WaitGroup
	if len(kbLangs) > 0 {
		prog(fmt.Sprintf("第2步/3：翻译%s（0/%d）...", label, len(texts)), 2, 3)
		type textItem struct {
			idx  int
			text string
		}
		for _, lc := range kbLangs {
			wg.Add(1)
			go func(lc string) {
				defer wg.Done()
				// 第一遍：KB 匹配
				needModelIdx := []int{}
				for i, t := range texts {
					r, _ := e.TranslateOne(ctx, t, []string{lc}, true)
					if v, ok := r.Translations[lc]; ok && v != "" {
						addTrans(lc, t, v)
						addKBHit()
					} else {
						needModelIdx = append(needModelIdx, i)
					}
				}
				// 第二遍：批量模型补漏
				if len(needModelIdx) > 0 {
					needTexts := make([]string, len(needModelIdx))
					for i, idx := range needModelIdx {
						needTexts[i] = texts[idx]
					}
					batch := e.BatchTranslate(ctx, needTexts, lc, 15, nil)
					for i, idx := range needModelIdx {
						if batch[i] != "" && batch[i] != "[翻译失败]" {
							addTrans(lc, texts[idx], batch[i])
							addModelHit()
						}
					}
				}
			}(lc)
		}
	}

	// 其他语言：批量模型
	for _, lc := range directOther {
		wg.Add(1)
		go func(lc string) {
			defer wg.Done()
			batch := e.BatchTranslate(ctx, texts, lc, 15, nil)
			for i, t := range texts {
				if batch[i] != "" && batch[i] != "[翻译失败]" {
					addTrans(lc, t, batch[i])
					addModelHit()
				}
			}
		}(lc)
	}
	wg.Wait()
	prog("第2步/3：翻译完成", 2, 3)

	// 第3步：写回文件
	prog("第3步/3：写回文件+修正排版...", 3, 3)
	srcDir := filepath.Dir(filePath)
	baseName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	outputDir := filepath.Join(srcDir, "translated")
	os.MkdirAll(outputDir, 0o755)

	isXlsx := ext == ".xlsx"
	filesOut := []string{}
	for _, lc := range finalLangs {
		tr := langTranslations[lc]
		if len(tr) == 0 {
			continue
		}
		outBase := fmt.Sprintf("%s_%s%s", baseName, lc, ext)
		outPath := filepath.Join(outputDir, outBase)
		if isXlsx {
			// xlsx: ctrl 页翻译所有 sheet，一份文件包含所有语言会冲突，保持各语言独立
		}
		var aerr error
		switch ext {
		case ".docx":
			aerr = fileproc.ApplyDocx(filePath, outPath, tr)
		case ".pptx":
			aerr = fileproc.ApplyPptx(filePath, outPath, tr)
		case ".xlsx":
			aerr = fileproc.ApplyXlsx(filePath, outPath, tr)
		}
		if aerr == nil {
			filesOut = append(filesOut, outPath)
		}
	}
	prog("第3步/3：完成", 3, 3)

	reply := fmt.Sprintf("✅ 文件翻译完成：共 %d 段文本，输出 %d 个文件（%s）",
		len(texts), len(filesOut), strings.Join(finalLangs, "/"))
	return &FileTranslateResult{
		Skill: "translation",
		Reply: reply,
		Data: FileTranslateData{
			TotalTexts:  len(texts),
			TargetLangs: finalLangs,
			LangNames:   langNames,
			KBHits:      kbHits,
			ModelHits:   modelHits,
		},
		Files: filesOut,
	}
}

// ============ 从 prompt 解析"其他语言" ============

var otherLangRegexes = []*regexp.Regexp{
	regexp.MustCompile(`(?:翻译成|翻成|译成|翻译为|翻为|译为)\s*(.+?)\s*[：:]\s*`),
	regexp.MustCompile(`用\s*(.+?)\s*翻译\s*[：:，,]?\s*`),
	regexp.MustCompile(`(?i)(?:translate\s+to|translat(?:e|ion)\s+in)\s+(.+?)\s*[：:，,]?\s*`),
	regexp.MustCompile(`^(\S+语)\s*[：:]\s*(.+)$`),
}

func (e *Engine) parseOtherLangsFromPrompt(ctx context.Context, text string) ([]string, string) {
	clean := text
	for _, re := range otherLangRegexes {
		if m := re.FindStringSubmatch(text); m != nil {
			hint := strings.TrimSpace(m[1])
			cleaned := ""
			if len(m) > 2 {
				cleaned = strings.TrimSpace(m[2])
			}
			code := LangCodeFromName(hint)
			if code == "" {
				code = e.LLMParseLang(ctx, hint)
			}
			if code == "" {
				// 去掉语言名部分，保留剩余作为正文
				cleaned = re.ReplaceAllString(text, "")
			}
			if code != "" {
				rest := strings.Replace(text, m[0], "", 1)
				cleaned = strings.TrimSpace(rest)
				cleaned = strings.TrimPrefix(cleaned, "：")
				cleaned = strings.TrimSpace(cleaned)
				if cleaned == "" {
					cleaned = m[2]
				}
				return []string{code}, cleaned
			}
		}
	}
	// 兜底：开头是语言名+冒号 "泰语：xxx"
	if m := regexp.MustCompile(`^(\S+?)\s*[：:]\s*(.+)$`).FindStringSubmatch(text); m != nil {
		code := LangCodeFromName(m[1])
		if code != "" {
			return []string{code}, strings.TrimSpace(m[2])
		}
	}
	return nil, clean
}