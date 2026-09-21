// ============ 本文件职责中文说明 ============
// 产物文件名翻译（RC-4，2026-09-22 用户裁定「文件名也翻掉」，此前零实现）：
// 把上传文件的 base 名（不含扩展名）翻成目标语言，供 HandleFile 的 4 处产物命名共用，
// 使中文原件 `产品方案书_v5.1.md` 翻成英文后交付 `Product Proposal_v5.1_en.md`。
//
// 三条硬性安全口径（都在纯函数里，便于不打真模型做断言）：
//  1. **翻译失败绝不判工单失败**——文件名只是体验项，任何错误/回显/清洗后为空一律回落原名；
//  2. **扩展名永不参与翻译、永不丢失**——只翻 base，扩展名由各产物命名模板决定；
//  3. **落盘安全**——清理 `/ \ : * ? " < > |`、控制字符与首尾空白/点，并截断到单个文件名
//     成分的 UTF-8 字节上限（255），且为后缀（`_en_text.md` 等）预留字节，
//     保证模型返回的字符串既不能带路径分隔符逃逸出 translated/ 目录，也不会超限写入失败。
//  4. **提示词残留零透传**——模型返回多行或原样包含整段原名时判为回显，直接回落原名，
//     绝不让清洗链把「指令行 + 原名」粘成一个交付文件名（见 isFilenameEchoResidue）。
//
// ========================================
package engine

import (
	"context"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"translator/internal/config"
	"translator/internal/observability"
)

// artifactBaseMaxBytes 单个文件名成分（base 或整个文件名）的 UTF-8 字节上限。
// 取 255：ext4/HFS+/NTFS(单段) 通行上限，超限不会报错但会被静默截断，跨平台会撕裂文件名。
const artifactBaseMaxBytes = 255

// illegalFileNameRunes 模型返回里必须剔除的危险字符：
// 路径分隔符（可逃逸 outputDir）、Windows 保留字符 `\ / : * ? " < > |`、NUL。
// 注意：不剔除 '.'（版本号 v5.1 合法），只 trim 首尾的点（Windows 不允许结尾点）。
const illegalFileNameRunes = `/\/:*?"<>|`

// sanitizeArtifactBaseName 清洗「待用作文件名主干」的字符串（纯函数，不打模型）：
// 去危险字符与控制字符 → 折叠连续空白 → trim 首尾空白与点 → 为后缀预留字节后按 UTF-8 截断。
// 参数 name: 待清洗名；suffix: 最终文件名里紧跟 base 的部分（如 "_en_text.md"），
// 预留其长度以保证**整体**不超 artifactBaseMaxBytes。返回空串表示不可用，调用方须回落原名。
func sanitizeArtifactBaseName(name, suffix string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == utf8.RuneError:
			continue // 截断/非法字节序列：宁可丢掉也不写出坏字符
		case r < 0x20 || r == 0x7f:
			continue // 控制字符（含 \n \r \t 与 NUL）：换行会被前端下载头截断响应
		case unicode.IsControl(r):
			continue
		case strings.ContainsRune(illegalFileNameRunes, r):
			continue
		case r == '\u3000':
			b.WriteRune(' ') // 全角空格统一成半角，避免命令行下不便处理
		default:
			b.WriteRune(r)
		}
	}
	// 折叠连续空白：模型常返回 "Product   Proposal"，多空格名在 shell/URL 里易出错
	cleaned := strings.Join(strings.Fields(b.String()), " ")
	cleaned = strings.Trim(cleaned, " .") // 首尾空白与点：Windows/POSIX 均不允许结尾点
	if cleaned == "" {
		return ""
	}
	return truncateUTF8Bytes(cleaned, artifactBaseMaxBytes-len(suffix))
}

// truncateUTF8Bytes 按 **字节** 上限截断且不切断多字节字符（rune 累加判断）。
// 为什么按字节：文件系统限制的是字节数，中文一字 3 字节，按 rune 截断仍会超限被静默截。
func truncateUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	n := 0
	for i, r := range s { // range 给出的是 rune 起始下标，天然不会切在多字节中间
		w := utf8.RuneLen(r)
		if n+w > maxBytes {
			return strings.Trim(s[:i], " .")
		}
		n += w
	}
	return s
}

// isFilenameEchoResidue 结构性识别「模型把提示词/原样回显吐回来」的坏返回（不看措辞，见下方两条判据）。
//
// 判据①含换行：文件名主干天生是单行串。模型返回多行 ⇒ 它把整条 prompt 逐行复述了
// （实测 UAT mock 形态：`TranslatedEN(把下面的英语翻译为日语，必须使用规范的日语汉字+假名混合书写)`
// 换行 `TranslatedEN(1790026522069352000_e2e_m1)`）。旧实现让 sanitizeArtifactBaseName
// 把换行当控制字符删掉后**拼成一整串**，内部提示词就原样进了交付物文件名——与 P0「表格分隔行
// 泄漏进交付物」同一类缺陷，只是载体从正文换成了名字。
// 判据②原样包含整个原 base：说明它根本没翻这个名字，只在外面套了层壳（回显），交付原名即可。
// 只认**完整**包含（不是片段），避免把「製品計画書_v5.1」这类正常译名误杀；
// 且要求原 base ≥3 个字符，防止 "AI"→"AI技術" 这类合理保留被误判。
func isFilenameEchoResidue(origBase, modelOut string) bool {
	if strings.ContainsAny(modelOut, "\n\r") {
		return true
	}
	o := strings.TrimSpace(origBase)
	return utf8.RuneCountInString(o) >= 3 && strings.Contains(modelOut, o)
}

// resolveTranslatedBaseName 纯函数：模型对文件名的原始返回 → 交付用 base（不可用则回落原名）。
// 参数 origBase: 原文件名主干（兜底值）；modelOut: 模型返回；callErr: 调用是否失败；lc: 目标语言码。
// 依次过：调用失败 → 回落；提示词回显残留 → 回落；IsTranslationUsable（回显/空/指令残留）→ 回落；
// PostProcessTranslation 清洗链（去中文残留/伪标签）→ 再判一次可用性 → 落盘安全清洗 → 空则回落。
//
// 顺序说明：先对**原始返回**判同文（模型没翻时返回的就是原 base，此时若先去清洗会因
// 汉字被剥离而变成 "v5.1" 这类残缺串，同文判定就失效了）。
func resolveTranslatedBaseName(origBase, modelOut string, callErr error, lc string) string {
	if callErr != nil || strings.TrimSpace(modelOut) == "" {
		return origBase
	}
	// ★ 回显/提示词残留必须在任何清洗之前拦住：清洗链会抹掉换行，坏串就被粘成合法文件名
	if isFilenameEchoResidue(origBase, modelOut) {
		return origBase
	}
	if !IsTranslationUsable(origBase, modelOut) {
		return origBase
	}
	cleaned := PostProcessTranslation(modelOut, lc)
	if !IsTranslationUsable(origBase, cleaned) {
		return origBase
	}
	if safe := sanitizeArtifactBaseName(cleaned, ""); safe != "" {
		return safe
	}
	return origBase
}

// translateFileNameBase 把 filePath 的 base 名翻成目标语言 lc（不含扩展名，失败回落原名）。
// 单次 TranslateOne（非 KB 直配）：文件名是无上下文短串，走 KB 极易命中脏行，
// 且整份文件只需一次调用，成本可忽略。调用方负责按语言做缓存，勿逐产物点各调一次。
func (e *Engine) translateFileNameBase(ctx context.Context, filePath, lc string) string {
	base := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	if strings.TrimSpace(base) == "" {
		return base
	}
	// 已是目标语书写系统的名字不必浪费一次调用（如英文原件翻成英文）
	if !fileNameNeedsTranslation(base, lc) {
		return base
	}
	r, err := e.TranslateOne(ctx, base, []string{lc}, false, config.StageAIInitial)
	if err != nil || r == nil {
		// 文件名翻译失败不影响交付：只记一笔，工单继续用原名出产物
		observability.Warn(ctx, "文件名翻译调用失败（产物回落原文件名）", "err", err, "lang", lc, "base", base)
		return base
	}
	return resolveTranslatedBaseName(base, r.Translations[lc], nil, lc)
}

// fileNameNeedsTranslation 判断 base 名是否需要送模型：
// CJK 目标语（zh/zh_hant/ja/ko）不看书写系统（翻成中文的译文本就是汉字，无法据字符判定）；
// 非 CJK 目标语（en/ru/…）若名字里**没有任何源语（中文）字符**，说明已是目标语形态
// （如 "Q3-report_v2"），翻一次只会拿到同文回显 ⇒ 直接省掉这次调用。
func fileNameNeedsTranslation(base, lc string) bool {
	if targetUsesCJKScript(lc) {
		return true
	}
	return HasCJK(base)
}

// targetUsesCJKScript 目标语言是否使用汉字书写系统（zh/zh_hant/ja/ko）。
// 与 postprocess.go 里散落的同源判定同口径；抽出来是因为文件名、保真闸门两处都要用。
func targetUsesCJKScript(langCode string) bool {
	switch langCode {
	case "zh", "zh_hant", "ja", "ko":
		return true
	}
	return false
}
