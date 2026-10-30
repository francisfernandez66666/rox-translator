// ============ 本文件职责中文说明 ============
// 纯文本类格式提取器的单元测试（txt/csv/md 三条在役提取器 + 已撤格式的行为判据）。
//
// ★ 2026-10-30 三事合一批（AGENTS §一·14）：`.srt/.vtt/.json/.yaml/.yml` 撤出准入、
// 「xlsx 双语对照表」交付形态连同 `WriteComparisonXlsx` 退役，所以本文件里那四条
// 「srt 提取／json 字符串值／yaml 标量／对照表产物」的断言**随实现一起删除**。
// 替代腿不是放松判据，而是把它换到**仍然成立的那一维**：
// 这五种格式现在必须在**两个口**都被拒（建单口 fileproc.IsTranslateFormat、
// 执行口 ExtractTexts），且拒绝句必须给出「请直接粘贴内容到文本翻译」这条出路——
// 只回一句「不支持」等于把一个更省事的路藏起来（用户口径，方案一 §1.1 同族）。
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemp 写临时文件并返回路径。
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// retiredNow 是本批撤出准入的五种格式（写在这里而不是从清单反推：清单如果被改回去，
// 下面两条判据必须当场红，那才叫"改对外承诺要逼出一次显式决策"）。
var retiredNow = []string{".srt", ".vtt", ".json", ".yaml", ".yml"}

// TestExtractMd Markdown：代码块剔除、图片/链接剥离、正文保留。
func TestExtractMd(t *testing.T) {
	md := "# 标题\n\n看这张 ![图](https://x/a.png) 和 [链接](https://y)\n\n```go\ncode_should_skip()\n```\n\n**加粗**正文行\n---\n"
	texts, err := ExtractTexts(writeTemp(t, "a.md", md))
	if err != nil {
		t.Fatalf("ExtractTexts 失败: %v", err)
	}
	joined := strings.Join(texts, "|")
	if strings.Contains(joined, "code_should_skip") || strings.Contains(joined, "https://") || joined == "" {
		t.Fatalf("md 应剔除代码块与 URL: %#v", texts)
	}
	if !strings.Contains(joined, "标题") || !strings.Contains(joined, "加粗") {
		t.Fatalf("md 正文应保留: %#v", texts)
	}
}

// TestRetiredFormatsNotAdmitted C1 的行为面：五种格式在**两种交付模式**下都不准入，
// 而在役格式必须仍然准入（正向对照——没有这条，"全部 false" 这种把闸门焊死的改法也会全绿）。
func TestRetiredFormatsNotAdmitted(t *testing.T) {
	for _, ext := range retiredNow {
		if IsTranslateFormat(ext, false) {
			t.Errorf("%s 还原模式仍准入（本批已撤）", ext)
		}
		if IsTranslateFormat(ext, true) {
			t.Errorf("%s 纯文案模式仍准入（本批已撤，两种模式都要撤）", ext)
		}
		for _, e := range TranslateFormatOrder {
			if e == ext {
				t.Errorf("%s 还留在 TranslateFormatOrder 里 ⇒ 出栈文案与开放文档都会继续承诺它", ext)
			}
		}
	}
	for _, ext := range []string{".docx", ".xlsx", ".pptx", ".pdf", ".txt", ".csv", ".md", ".png", ".mp3"} {
		if !IsTranslateFormat(ext, false) {
			t.Fatalf("正向对照失败：%s 应仍准入 ⇒ 清单被删过头了", ext)
		}
	}
	// 出栈清单句同步收缩：FormatHintBase 不许还留着这五个里的任何一个。
	hint := FormatHintBase()
	for _, ext := range retiredNow {
		name := strings.TrimPrefix(ext, ".")
		for _, tok := range strings.Split(hint, "/") {
			if tok == name {
				t.Fatalf("对外清单句里仍出现 %s（改档位没改文案＝在承诺一个收不进来的格式）：%s", name, hint)
			}
		}
	}
}

// TestRetiredFormatsRejectedAtExecutor C4 的第三口：执行口（ExtractTexts）拿到这五种格式时
// 必须**报错**而不是提空交白卷，且错误句给得出路。
// 为什么这一口单独判：它是"单建起来了、跑一半报不支持"那一族的最后一道（AGENTS §一·14 头注）。
func TestRetiredFormatsRejectedAtExecutor(t *testing.T) {
	for _, ext := range retiredNow {
		p := writeTemp(t, "sample"+ext, "hello\n")
		_, err := ExtractTexts(p)
		if err == nil {
			t.Errorf("%s 在执行口仍被接受（该拒）⇒ 会走回已删除的提取分支", ext)
			continue
		}
		if !strings.Contains(err.Error(), PlainTextContainerHint) {
			t.Errorf("%s 的拒绝句没给可行动出路，实得 %q", ext, err.Error())
		}
	}
	// 射程自证：在役格式走执行口不得报"不支持"（否则上面那条"报错"是恒真）。
	if _, err := ExtractTexts(writeTemp(t, "a.txt", "第一句\n第二句\n")); err != nil {
		t.Fatalf("正向对照失败：.txt 执行口报错 %v", err)
	}
}

// TestRejectHintTwoBranches C-10/ C11 的判据本体：撤除档给出路、其它格式给通用清单句，
// 两侧都只许由这一个出口产出（前端逐字对齐由 translateFormats.lock.test.ts 的 P5 判）。
func TestRejectHintTwoBranches(t *testing.T) {
	for _, ext := range retiredNow {
		got := RejectHint(ext, true, "不支持的格式")
		if !strings.Contains(got, PlainTextContainerHint) {
			t.Errorf("%s 纯文案模式拒绝句缺出路：%q", ext, got)
		}
	}
	got := RejectHint(".exe", false, "不支持的格式: a.exe")
	if strings.Contains(got, PlainTextContainerHint) {
		t.Errorf(".exe 与那五种格式不同因，不该给「粘贴文本」出路：%q", got)
	}
	if !strings.Contains(got, "仅支持") || !strings.Contains(got, "docx") {
		t.Errorf("通用档拒绝句必须带现值清单（改档位这句话要自己跟着变），实得 %q", got)
	}
	// 现算自证：清单句里绝不能出现已撤格式（出现＝第二份清单，迟早与准入打架）。
	if strings.Contains(RejectHint(".exe", false, "x"), ".srt") || strings.Contains(RejectHint(".exe", true, "x"), "srt/") {
		t.Fatal("清单句仍列着撤除格式")
	}
	// 正锁兜射程：清单句必须真含在役档位，否则"不含 srt"是因为整句空了。
	if !strings.Contains(RejectHint(".exe", false, "x"), FormatHintBase()) {
		t.Fatal("射程自证失败：清单句里取不到 FormatHintBase()")
	}
}

// TestWriteTranslationMdKeepsSourceWhenUntranslated 纯文案交付：未命中段保留原文（不留白）。
// 这一档服务的是**在役**链（pdf/docx/txt/csv/md 写回失败的兜底、图片与音频的译文主面），
// 撤除格式那批旧断言删掉后，这条是 .md 交付面唯一的行为锁，别当成"顺手加的测试"。
func TestWriteTranslationMdKeepsSourceWhenUntranslated(t *testing.T) {
	out := filepath.Join(t.TempDir(), "sub", "a-en.md")
	srcs := []string{"你好", "世界"}
	tr := map[string]string{"你好": "hello"} // 第二段故意没译
	if err := WriteTranslationMd(out, srcs, tr); err != nil {
		t.Fatalf("WriteTranslationMd 失败: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读产物失败（含子目录创建）: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "hello") || !strings.Contains(got, "世界") {
		t.Fatalf("译文与未译出原文必须都在，实得 %q", got)
	}
	if strings.Contains(got, "你好") {
		t.Fatalf("已译出的那一段不该再以原文出现（那会把原文当译文交付）：%q", got)
	}
}
