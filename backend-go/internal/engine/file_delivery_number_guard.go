// ============ file_delivery_number_guard.go · 职责说明（2026-11-23）============
// 最终交付物上的数字守恒兜底：采纳判据在写入前跑过，但写回/剥标记这一跳仍可能
// 把长句格里的数字吞掉（tv13 终轮 L137 的 "+150%"）。这里在 .md 写回完成后
// 逐格对源文与交付文做数值比对（与 gate.NumbersPreserved 同一把尺子），
// 缺数的格子回落源文原文并进入「未译出内容」点名，绝不留静默漏数。
// ================================================
package engine

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"

	"translator/internal/fileproc"
	"translator/internal/gate"
)

// verifyDeliveredNumbers 校验并修正 .md 交付件的数字守恒。
// 返回：需要点名的回落条目、告警、错误。只处理源/产物行号对齐且列数相同的行。
func (e *Engine) verifyDeliveredNumbers(ctx context.Context, srcPath, outPath, lang string) ([]fileproc.UntranslatedItem, []string, error) {
	sb, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, nil, err
	}
	db, err := os.ReadFile(outPath)
	if err != nil {
		return nil, nil, err
	}
	srcLines := strings.Split(strings.ReplaceAll(string(sb), "\r\n", "\n"), "\n")
	dstLines := strings.Split(strings.ReplaceAll(string(db), "\r\n", "\n"), "\n")
	// 交付物末尾的「未译出内容」一节不是译文，不参与比对。
	for i, ln := range dstLines {
		if strings.HasPrefix(strings.TrimSpace(ln), "## 未译出内容") {
			dstLines = dstLines[:i]
			break
		}
	}
	n := len(srcLines)
	if len(dstLines) < n {
		n = len(dstLines)
	}
	var notes []fileproc.UntranslatedItem
	var warnings []string
	changed, changedRepair := false, false
	for i := 0; i < n; i++ {
		sc, dc, lead, tail := splitMdCells(srcLines[i]), splitMdCells(dstLines[i]), strings.HasPrefix(srcLines[i], "|"), strings.HasSuffix(srcLines[i], "|")
		if len(sc) != len(dc) {
			continue // 列数不齐是另一类问题，不在本闸门射程
		}
		lineChanged := false
		for j := range sc {
			if !hasDigitForGuard(sc[j]) || strings.TrimSpace(sc[j]) == strings.TrimSpace(dc[j]) {
				continue
			}
			if gate.NumbersPreserved(sc[j], dc[j]) {
				continue
			}
			// 先尝试**就地补齐**缺失的数字字面量（长句格里的 "+150%" 是语言无关的，
			// 直接拼回译文即可，既不丢译文也不制造未译段）；补不回来才逐格回落源文。
			if repaired := repairMissingNumberTokens(sc[j], dc[j]); repaired != dc[j] &&
				gate.NumbersPreserved(sc[j], repaired) {
				dc[j] = repaired
				lineChanged, changed = true, true
				changedRepair = true
				continue
			}
			// 就地补不齐 ⇒ 先对该格单独重译一次（最小指令腿；现网最小样本实证该格可翻对），
			// 重译产物必须同时过可用性与数字守恒两把尺子；仍不行才逐字节回落源文并点名。
			if v, ok := e.translateSegmentMinimal(ctx, sc[j], lang); ok &&
				e.fileTranslationUsable(ctx, sc[j], v, lang) && gate.NumbersPreserved(sc[j], v) {
				dc[j] = v
				lineChanged, changed, changedRepair = true, true, true
				continue
			}
			dc[j] = sc[j] // 仍不行 ⇒ 逐字节回落源文，随后进未译出清单
			lineChanged, changed = true, true
			notes = append(notes, fileproc.UntranslatedItem{
				Line: i + 1, Source: sc[j], Unit: "行",
				Reason: "译文数字未保持（写回后校验），已按原文交付",
			})
		}
		if lineChanged {
			dstLines[i] = joinMdCells(dc, lead, tail)
		}
	}
	if !changed {
		return nil, nil, nil
	}
	if err := os.WriteFile(outPath, []byte(strings.Join(dstLines, "\n")), 0o644); err != nil {
		return nil, nil, err
	}
	if changedRepair {
		warnings = append(warnings, fmt.Sprintf("%s 数字未保持的格已就地补齐", langLabel(lang)))
	}
	if len(notes) > 0 {
		warnings = append(warnings, fmt.Sprintf("%s 数字未保持的 %d 格已按原文回落并点名", langLabel(lang), len(notes)))
	}
	return notes, warnings, nil
}

// splitMdCells 与 fileproc 的表格拆分同口径：只拆首尾完整的竖线表格行。
func splitMdCells(line string) []string {
	if strings.Count(line, "|") < 2 {
		return []string{line}
	}
	p := strings.Split(line, "|")
	if p[0] == "" {
		p = p[1:]
	}
	if len(p) > 0 && p[len(p)-1] == "" {
		p = p[:len(p)-1]
	}
	return p
}

// joinMdCells 按原行是否带首尾竖线重建 Markdown 表格格，避免补数字时破坏表格形状。
func joinMdCells(cells []string, lead, tail bool) string {
	var b strings.Builder
	if lead {
		b.WriteByte('|')
	}
	b.WriteString(strings.Join(cells, "|"))
	if tail {
		b.WriteByte('|')
	}
	return b.String()
}

// repairMissingNumberTokens 把源格里缺失的数字字面量（含正负号/百分号）就地补回译文格。
// 只补数字本身，不动译文的词语；Markdown 反斜杠转义先剥掉，避免把 `\+150%` 原样带进去。
var numberTokenRe = regexp.MustCompile(`[+\-–−]?[0-9][0-9,\.]*\s*%?`)

// repairMissingNumberTokens 只补源格缺失的数字字面量，不改译文词语；数字已守恒时立即收手。
func repairMissingNumberTokens(src, dst string) string {
	clean := strings.NewReplacer("\\+", "+", "\\-", "-", "\\%", "%", "\\.", ".").Replace(src)
	out := dst
	for _, tok := range numberTokenRe.FindAllString(clean, -1) {
		tok = strings.TrimSpace(tok)
		if tok == "" || gate.NumbersPreserved(src, out) {
			break
		}
		if strings.Contains(out, tok) {
			continue
		}
		out = strings.TrimRight(out, " ") + " " + tok
	}
	return out
}

// hasDigitRune 快筛：源格无数字就不必跑正则。
func hasDigitForGuard(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
