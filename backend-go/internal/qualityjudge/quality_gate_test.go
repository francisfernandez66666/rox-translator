// ============ qualityjudge_test.go · 职责说明 ============
// 语义判据（HY 回译 + evals 打分）纯函数反证：回译指令形态、JSON 解析。
// 网络/DB 相关的端到端判别力由服务器离线回放验证（方案 §九）。
// ============================================================
package qualityjudge

import (
	"strings"
	"testing"

	"translator/internal/config"
)

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"term":80,"semantic":90}`, `{"term":80,"semantic":90}`},
		{"打分：{\"term\":0,\"semantic\":30} 完", `{"term":0,"semantic":30}`},
		{"不确定", "不确定"},
	}
	for _, tc := range cases {
		if got := ExtractJSON(tc.in); got != tc.want {
			t.Fatalf("in=%q got=%q want=%q", tc.in, got, tc.want)
		}
	}
}

func TestBackInstructionShape(t *testing.T) {
	for lang, name := range map[string]string{"he": "希伯来语", "ru": "俄语", "ar": "阿拉伯语"} {
		instr := backInstruction(lang)
		if config.LangNames[lang] == "" {
			t.Fatalf("语种 %s 无中文名，判据无法生成正确回译指令", lang)
		}
		if !strings.Contains(instr, "翻译为简体中文") || !strings.Contains(instr, "不得复述原文") {
			t.Fatalf("lang=%s 回译指令缺关键约束: %q", lang, instr)
		}
		if !strings.Contains(instr, name) {
			t.Fatalf("lang=%s 回译指令未点名目标语(%s): %q", lang, name, instr)
		}
	}
}

func TestDefaults(t *testing.T) {
	if DefaultBacktranslateModel != "tencent/Hunyuan-MT-7B" {
		t.Fatalf("回译默认模型被改: %q", DefaultBacktranslateModel)
	}
	if DefaultMinScore != 70 {
		t.Fatalf("默认阈值被改: %v", DefaultMinScore)
	}
	if ConfigKeyBacktranslateModel != "scrape_tier3_backtranslate_model" {
		t.Fatalf("可配键名被改（管理台/判据会脱钩）: %q", ConfigKeyBacktranslateModel)
	}
}
