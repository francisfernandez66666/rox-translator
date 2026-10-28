// ============ 本文件职责中文说明 ============
// 「占位符绝不入图」闸门（★ 2026-10-28 图片版式回退批，方案 §5 断言 B1）。
//
// 现网实证形态：同一张界面截图，回退前那一版的**交付图上顶着一行 `[已拦截·REDACTED]`**——
// 敏感词拦截段与上游失败槽 `[翻译失败]` 都被当成"译文"画进了客户看到的图里。
// 本仓口径（AGENTS §19 族一）：兜底形态一律**回落原文＋点名**，点名发生在 .md 与工单读数，
// 图上画原文＝"这一行保持原样"（客户认得出），画占位符＝把内部记号交给客户（最坏的那类静默坏）。
//
// 三条腿：
//
//	R1 行为腿：`RenderSafeTranslations` 对 正常译文／`[翻译失败]`／`[已拦截·REDACTED]`／纯空白
//	   四态的处置逐字点名——正常值保留，三态一律**缺席**（缺席＝fileproc 走「保留原文」那一支）。
//	R2 接线腿（守卫级）：扫**全集**调用点——`fileproc.RenderTranslatedImage(` 的每一处都必须
//	   经 `RenderSafeTranslations` 包一层。只测助手函数本身是判据级，代替不了"每个调用方真调了它"
//	   （AGENTS §三 反证 A12 那一族：守卫拿到的输入是算出来的，必须在守卫入口再验一次同一形态）。
//	R3 名单腿：两份占位符常量不许在 engine 之外长出第二份字面量抄本（§一·14「清单只有一份」）——
//	   扫 `internal/` 全部非测试 Go 文件，`[翻译失败]`／`[已拦截·REDACTED]` 的字面量只许出现在
//	   各自唯一的定义处。
//
// ============================================
package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderSafeTranslationsDropsPlaceholders 行为腿：正常译文保留，三态占位符一律摘成缺席。
func TestRenderSafeTranslationsDropsPlaceholders(t *testing.T) {
	tr := map[string]string{
		"正常行":   "hello",
		"失败槽行":  "[翻译失败]",
		"拦截行":   "[已拦截·REDACTED]",
		"空白行":   "   ",
		"带空格失败": "  [翻译失败] ",
	}
	out := RenderSafeTranslations(tr)
	if got := out["正常行"]; got != "hello" {
		t.Fatalf("正常译文被摘掉了（got=%q）——摘除判据过宽会把好译文一起抹了", got)
	}
	for _, k := range []string{"失败槽行", "拦截行", "空白行", "带空格失败"} {
		if _, ok := out[k]; ok {
			t.Fatalf("占位符入图风险：%q 未被摘除（值=%q）", k, out[k])
		}
	}
	if len(out) != 1 {
		t.Fatalf("应只剩 1 条可用译文，实得 %d 条", len(out))
	}
}

// TestRenderTranslatedImageCallSitesAreWrapped 接线腿：按**调用点全集**判，不按点位判。
// 新增一个回填调用方而忘了包一层，必须当场红——那正是本批修掉的现网形态。
func TestRenderTranslatedImageCallSitesAreWrapped(t *testing.T) {
	sites := []string{
		filepath.Join("..", "engine", "file.go"),
		filepath.Join("..", "api", "chat_image.go"),
	}
	total := 0
	for _, f := range sites {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读不到调用方文件 %s（被移动？接线腿的射程就没了）: %v", f, err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "fileproc.RenderTranslatedImage(") {
				continue
			}
			total++
			if !strings.Contains(line, "RenderSafeTranslations(") {
				t.Fatalf("调用点未包 RenderSafeTranslations（占位符会直接画进图）：%s ⇒ %s", f, strings.TrimSpace(line))
			}
		}
	}
	if total < 2 {
		t.Fatalf("回填调用点只数到 %d 处（应 ≥2）——判据已空转，先核调用方是否被搬走", total)
	}
}

// TestPlaceholderLiteralsHaveSingleDefinition 名单腿：两份占位符字面量在**代码行**上只许有定义处那一处。
// 注释里出现是允许的（它们记录的是"为什么"，剥注释的形同 §一·5 那条老纪律）；
// 代码行上长出第二份抄本＝两份尺子，改一处漏一处（AGENTS §一·14「清单只有一份」）。
// 本批真收了两处存量抄本：`engine.go` 的失败槽写点、`file_events.go` 的失败段比对。
func TestPlaceholderLiteralsHaveSingleDefinition(t *testing.T) {
	for _, lit := range []string{translationFailureMark, SensitivePlaceholderText} {
		var hits []string
		err := filepath.Walk("..", func(p string, info os.FileInfo, werr error) error {
			if werr != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			for _, line := range strings.Split(string(b), "\n") {
				t0 := strings.TrimSpace(line)
				if strings.HasPrefix(t0, "//") {
					continue // 整行注释：记录原因的，不算抄本
				}
				if strings.Contains(t0, "\""+lit+"\"") {
					hits = append(hits, p+": "+t0)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("扫描失败：%v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("占位符名单长出第二份抄本：%q 在代码行上出现 %d 处（应恰 1 处定义）⇒ %s",
				lit, len(hits), strings.Join(hits, " ｜ "))
		}
	}
}

// TestRenderSafeTranslationsSkipsSingleSymbolSources 单字符、非汉字的源文一律不回填（图上保留原样）。
// 现网实证（2026-10-29）：开/关按钮被识别成 `0`，回填把绿色开关整个盖成一个 "0"。
// 这类行本来就没有可译内容（AGENTS §19 族二），画上去只会抹掉底下的图形；
// 而**单字中文**是可译正文，必须照常回填——这条腿把两边都钉住。
func TestRenderSafeTranslationsSkipsSingleSymbolSources(t *testing.T) {
	tr := map[string]string{
		"0":      "0",
		"√":      "√",
		"の":      "の",
		"。":      "。",
		"D":      "D",
		"是":      "Yes",
		"manual": "Manual",
	}
	out := RenderSafeTranslations(tr)
	for _, k := range []string{"0", "√", "の", "。", "D"} {
		if _, ok := out[k]; ok {
			t.Fatalf("单字符非汉字源文不应回填（会盖掉底下的控件/符号）：%q", k)
		}
	}
	if out["是"] != "Yes" {
		t.Fatalf("单字中文是可译正文，必须照常回填（got=%q）", out["是"])
	}
	if out["manual"] != "Manual" {
		t.Fatalf("多字符正常译文被误伤：%q", out["manual"])
	}
}
