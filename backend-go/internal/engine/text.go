// ============ 本文件职责中文说明 ============
// 文本翻译主流程（复刻 skill.py _handle_text_translate）：面向对话/文本翻译入口。
// 从 options 与用户 prompt 解析目标语言（KB 语言 + 其他语言），
// 知识库语言走 TranslateOne 四段匹配（命中标注 kb 来源），其他语言走纯模型并发翻译，
// 最终合并所有语言译文、构建展示 reply 与结构化 TextTranslateData 返回。
// ========================================
package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"translator/internal/config"
)

// Progress 进度回调（step=阶段描述，done/total=当前/总步数）
type Progress func(step string, done, total int)

// TextTranslateResult 文本翻译结果（ChatResponse 契约）
type TextTranslateResult struct {
	Skill string            `json:"skill"` // 能力标识（固定 "translation"）
	Reply string            `json:"reply"` // 面向用户的完成话术（含译文与模式说明）
	Data  TextTranslateData `json:"data"`  // 结构化翻译数据
	Files []string          `json:"files"` // 关联文件（文本翻译通常为空）
	Error string            `json:"error"` // 失败原因（成功时为空）
}

// TextTranslateData 文本翻译结构化数据
type TextTranslateData struct {
	Translations       map[string]string `json:"translations"`        // 目标语言 → 译文
	TranslationsSource map[string]string `json:"translations_source"` // 目标语言 → 来源（kb/model）
	LangNames          map[string]string `json:"lang_names"`          // 目标语言代码 → 中文名
	KBLangs            []string          `json:"kb_langs"`            // 走知识库翻译的目标语言
	OtherLangs         []string          `json:"other_langs"`         // 走纯模型翻译的其他语言
	SourceText         string            `json:"source_text"`         // 实际被翻译的原文（剥离指令后）
	Mode               string            `json:"mode"`                // 命中模式描述（精确命中/纯模型等）
	Similarity         *float64          `json:"similarity"`          // 语义命中的相似度（未命中为 nil）
	MatchedZH          string            `json:"matched_zh"`          // 命中的知识库中文原文
	TargetLangs        []string          `json:"target_langs"`        // 全部目标语言（KB + 其他）
}

// TargetLangsFromOptions 从 options 提取语言列表
func TargetLangsFromOptions(options map[string]interface{}) []string {
	if options == nil {
		return nil
	}
	v, ok := options["target_langs"]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []interface{}:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		parts := strings.Split(t, ",")
		var out []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

// SplitOptions 分离 KB / other / directOther
func SplitOptions(langs []string) (kbTarget, directOther []string, hasOther bool) {
	for _, lc := range langs {
		if lc == "other" {
			hasOther = true
		} else if IsKBLang(lc) {
			kbTarget = append(kbTarget, lc)
		} else {
			directOther = append(directOther, lc)
		}
	}
	return
}

// HandleText 文本翻译主流程（复刻 skill.py _handle_text_translate）
func (e *Engine) HandleText(ctx context.Context, text string, options map[string]interface{}, prog Progress) *TextTranslateResult {
	// 注入请求级用量记录器（供计量成本核算）
	ctx = e.WithUsageRecorder(ctx)
	if prog == nil {
		prog = func(string, int, int) {}
	}
	if strings.TrimSpace(text) == "" {
		return &TextTranslateResult{Skill: "translation", Reply: "请输入要翻译的文本"}
	}
	// ★ 租户可用性校验
	if err := e.tenantOK(ctx); err != nil {
		return &TextTranslateResult{Skill: "translation", Reply: "❌ " + err.Error()}
	}

	langs := TargetLangsFromOptions(options)
	kbTarget, directOther, hasOther := SplitOptions(langs)
	cleanText := text
	// 实际源语言：优先用户显式指定（source_lang），否则自动检测
	srcLang := DetectSourceLang(cleanText)
	if sl, ok := options["source_lang"].(string); ok && sl != "" {
		srcLang = sl
	}

	// 未指定语言 → 从 prompt 解析
	if len(kbTarget) == 0 && len(directOther) == 0 {
		clean, parsed := StripLangInstruction(text)
		if len(parsed) > 0 {
			kbTarget, directOther, hasOther = SplitOptions(parsed)
			cleanText = clean
		} else {
			kbTarget = []string{"en"}
		}
	}
	// 选了 other 但无法解析 → 从 prompt 解析其他语言
	if hasOther && len(directOther) == 0 {
		parsed, cleaned := e.parseOtherLangsFromPrompt(ctx, cleanText)
		if len(parsed) == 0 {
			return &TextTranslateResult{
				Skill: "translation",
				Reply: "你选择了「其他语言」，但没告诉我翻译成什么语言。请输入类似「翻译成泰语：xxx」",
			}
		}
		directOther = parsed
		cleanText = cleaned
	}

	// KB 翻译
	kbResult := &TranslateResult{Translations: map[string]string{}, Mode: "模型翻译（无知识库）"}
	kbSrc := map[string]string{}
	if len(kbTarget) > 0 {
		prog("知识库匹配中...", 1, 4)
		prevOnPhase := e.OnPhase
		e.OnPhase = func(phase string) {
			if phase == "ai_generating" {
				prog("AI生成中...", 2, 4)
			}
		}
		kbResult, _ = e.TranslateOne(ctx, cleanText, kbTarget, false)
		e.OnPhase = prevOnPhase
		for lc := range kbResult.Translations {
			src := "model"
			if kbResult.MatchedZH != "" {
				src = "kb"
			}
			kbSrc[lc] = src
		}
	}

	// 其他语言 → 纯模型（并发）
	otherTr := map[string]string{}
	otherNames := map[string]string{}
	for i, code := range directOther {
		name := config.LangNames[code]
		if name == "" {
			name = code
		}
		otherNames[code] = name
		prog(fmt.Sprintf("AI翻译%s...", name), 2+i, 4+len(directOther))
	}
	if len(directOther) > 0 {
		// 并发限制（信号量）：最多 3 路并发调用其他语言模型翻译，避免打爆 LLM API
		const maxConcurrent = 3
		sem := make(chan struct{}, maxConcurrent)
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, code := range directOther {
			wg.Add(1)
			go func(lc string) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()
				tr, _ := e.TranslateOtherLang(ctx, cleanText, lc, srcLang)
				mu.Lock()
				otherTr[lc] = tr
				mu.Unlock()
			}(code)
		}
		wg.Wait()
	}

	prog("翻译完成", 4, 4)

	// 合并
	allTr := map[string]string{}
	allSrc := map[string]string{}
	for lc, v := range kbResult.Translations {
		allTr[lc] = v
		allSrc[lc] = kbSrc[lc]
	}
	for lc, v := range otherTr {
		allTr[lc] = v
		allSrc[lc] = "model"
	}

	langNames := map[string]string{}
	for lc := range allTr {
		langNames[lc] = config.LangNames[lc]
	}
	for lc, n := range otherNames {
		langNames[lc] = n
	}

	// 构建 reply
	var sb strings.Builder
	sb.WriteString("📝 「" + cleanText + "」翻译结果：\n\n")
	order := kbTarget
	order = append(order, directOther...)
	for _, lc := range order {
		if v, ok := allTr[lc]; ok && v != "" {
			sb.WriteString(fmt.Sprintf("  %s：%s 🤖\n", langNames[lc], v))
		}
	}

	modelCount := 0
	for _, src := range allSrc {
		if src == "model" {
			modelCount++
		}
	}
	mode := kbResult.Mode
	if mode == "" {
		mode = "模型翻译（无知识库）"
	}
	if modelCount > 0 {
		mode = "纯模型翻译"
	}
	kbHitName := ""
	for lc, src := range allSrc {
		if src == "kb" {
			kbHitName = config.LangNames[lc]
			break
		}
	}
	if kbHitName != "" {
		mode = mode + " | " + kbHitName + " 命中知识库"
	}
	sb.WriteString("\n📊 模式：" + mode)

	var sim *float64
	if kbResult.Similarity > 0 {
		s := kbResult.Similarity
		sim = &s
	}

	return &TextTranslateResult{
		Skill: "translation",
		Reply: sb.String(),
		Data: TextTranslateData{
			Translations:       allTr,
			TranslationsSource: allSrc,
			LangNames:          langNames,
			KBLangs:            kbTarget,
			OtherLangs:         directOther,
			SourceText:         cleanText,
			Mode:               mode,
			Similarity:         sim,
			MatchedZH:          kbResult.MatchedZH,
			TargetLangs:        append(append([]string{}, kbTarget...), directOther...),
		},
	}
}
