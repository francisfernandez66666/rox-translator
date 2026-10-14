// ============ srt.go · 职责说明 ============
// 字幕（SRT）交付产物写入：音频经 ASR 转写后，译文按**原时间轴**写回 .srt（P2）。
// 与 text_formats.go 的 extractSubtitle（读）对称：这里是「写」——译文段落对齐到
// ASR 的原始时间轴，保证翻译后的字幕与原音轨同步（这是音频翻译产品的基本要求）。
// =============================================
package fileproc

import (
	"fmt"
	"os"
	"strings"
)

// SubtitleCue 单条字幕（时间轴 + 文本）。Start/End 单位为秒（浮点）。
// 文本为**源语言**原文（与 ASR 段顺序一致），译文由调用方按 cues[i].Text 查翻译表得到。
type SubtitleCue struct {
	Index int
	Start float64
	End   float64
	Text  string
}

// formatSrtTime 把秒数格式化为 SRT 时间轴 "HH:MM:SS,mmm"。
func formatSrtTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int((sec - float64(int(sec))) * 1000)
	h := int(sec) / 3600
	m := (int(sec) % 3600) / 60
	s := int(sec) % 60
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// WriteTranslationSrt 把译文按 cues 的时间轴写出 .srt。
// 参数：cues=ASR 原始时间轴（Text 为源文，与 texts 同源同序）；
//
//	texts=按提取顺序的源文段（cues 为空时的有序兜底，保证产物顺序稳定）；
//	tr=源文→译文映射。
//
// cues 为空时退化为「无时间轴」顺序编号（每段 0 时长），保证产物仍可读有序——
// 缓存丢失/进程重启都会走到这里，是**降级不是失败**（译文资产优先出）。
func WriteTranslationSrt(path string, cues []SubtitleCue, texts []string, tr map[string]string) error {
	var b strings.Builder
	if len(cues) == 0 {
		for i, src := range texts {
			text := tr[src]
			if text == "" {
				text = src // 未译出保留原文
			}
			b.WriteString(fmt.Sprintf("%d\n00:00:00,000 --> 00:00:00,000\n%s\n\n", i+1, text))
		}
	} else {
		for i, c := range cues {
			text := tr[c.Text]
			if text == "" {
				text = c.Text // 未译出保留原文
			}
			b.WriteString(fmt.Sprintf("%d\n%s --> %s\n%s\n\n", i+1, formatSrtTime(c.Start), formatSrtTime(c.End), text))
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
