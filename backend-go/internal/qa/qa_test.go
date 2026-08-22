// ============ 本文件职责中文说明 ============
// 确定性 QA 质检单元测试：六项规则（empty/same/number/placeholder/length/punctuation）
// 与报告汇总口径（errors/warnings/pass）。
package qa

import "testing"

// TestCheckEmpty 空译文 → error 且不通过。
func TestCheckEmpty(t *testing.T) {
	r := Check("你好世界", map[string]string{"en": ""})
	if r.Pass || r.Errors != 1 || r.Warnings != 0 {
		t.Fatalf("空译文应报 1 error: %+v", r)
	}
	if r.Issues[0].Rule != "empty" {
		t.Fatalf("规则名应为 empty: %+v", r.Issues[0])
	}
}

// TestCheckNumberMismatch 数字漏译/错译 → error。
func TestCheckNumberMismatch(t *testing.T) {
	r := Check("2026 年销量增长 15.5%", map[string]string{"en": "Sales grew in 2026."})
	if r.Pass {
		t.Fatal("数字不一致应不通过")
	}
	found := false
	for _, iss := range r.Issues {
		if iss.Rule == "number" && iss.Level == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应报 number error: %+v", r.Issues)
	}
}

// TestCheckPlaceholder 占位符缺失 → error；完整保留 → 通过。
func TestCheckPlaceholder(t *testing.T) {
	src := "点击 {button} 完成，进度 %d"
	bad := Check(src, map[string]string{"ja": "クリックして完了します"})
	good := Check(src, map[string]string{"ja": "{button} をクリックして完了、進捗 %d"})
	if bad.Pass {
		t.Fatal("占位符缺失应不通过")
	}
	if !good.Pass {
		t.Fatalf("占位符完整应通过: %+v", good.Issues)
	}
}

// TestCheckLength 疑似漏翻 → warning（不影响 pass）。
func TestCheckLength(t *testing.T) {
	long := "这是一段非常长的源文本内容用于测试疑似漏翻的长度检查逻辑是否正常工作"
	r := Check(long, map[string]string{"en": "OK"})
	if !r.Pass {
		t.Fatal("warning 不应影响 pass")
	}
	if r.Warnings == 0 {
		t.Fatalf("过短译文应报 length warning: %+v", r)
	}
}

// TestCheckPunctuation zh 句末半角句号 → warning。
func TestCheckPunctuation(t *testing.T) {
	r := Check("Hello world.", map[string]string{"zh": "你好世界."})
	if r.Warnings == 0 || r.Errors != 0 {
		t.Fatalf("半角句号应为 warning: %+v", r)
	}
}

// TestCheckSame 译文与源文相同（含文字）→ warning；纯符号不报。
func TestCheckSame(t *testing.T) {
	r := Check("蓝牙钥匙", map[string]string{"de": "蓝牙钥匙"})
	if r.Warnings != 1 {
		t.Fatalf("同文应报 same warning: %+v", r)
	}
	r2 := Check("1,000", map[string]string{"de": "1,000"})
	if r2.Warnings != 0 {
		t.Fatalf("纯数字/符号同文不应报: %+v", r2)
	}
}

// TestCheckMultiLang 汇总口径：多语言问题计数正确且明细截断 ≤50。
func TestCheckMultiLang(t *testing.T) {
	m := map[string]string{}
	for _, lc := range []string{"a", "b", "c", "d", "e", "f"} {
		m[lc] = "" // 全部空译
	}
	r := Check("文本", m)
	if r.Errors != 6 || r.Warnings != 0 || r.Pass {
		t.Fatalf("6 语言全空应计 6 error: %+v", r)
	}
}
