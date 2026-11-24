// ============ qualityjudge_test.go · 职责说明 ============
// 语义判据（HY 回译 + evals 打分）纯函数反证：回译指令形态、JSON 解析。
// 网络/DB 相关的端到端判别力由服务器离线回放验证（方案 §九）。
// ============================================================
package qualityjudge

import (
	"fmt"
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

// 2026-11-24 打分腿提示词锁：两枚关键口径不许被"顺手精简"掉。
//
// 为什么钉这个：real 组 6/9 → 9/9 的全部收益来自这两句——
//
//	① "可能取错义项"（回译腿会误导，别让模型把回译当铁证）
//	② "以译文本身为准"（多义词按译文判断，而不是按回译压分）
//
// 少一句就会退回"卖家/流量统计/轮胎被回译误导压死"那一族（现网 336 条误丢的成因）。
func TestJudgePromptKeepsBacktranslateDisclaimer(t *testing.T) {
	if strings.Contains(judgeSysPrompt, "ליד") || strings.Contains(judgeSysPrompt, "צ'רי") {
		t.Fatal("希伯来语举例是旧口径的噪音源，不许写回通用提示词")
	}
	for _, must := range []string{"term", "semantic", "可能取错义项", "以译文本身为准", "只输出 JSON"} {
		if !strings.Contains(judgeSysPrompt, must) {
			t.Fatalf("打分提示词缺关键口径 %q: %q", must, judgeSysPrompt)
		}
	}
	got := fmt.Sprintf(judgeUserPromptFmt, "卖家", "希伯来语", "מוכר", "卖方")
	for _, must := range []string{"卖家", "希伯来语", "מוכר", "卖方"} {
		if !strings.Contains(got, must) {
			t.Fatalf("user 提示词未带 %q: %q", must, got)
		}
	}
}

func TestParseJudgeScoresStrict(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{"正常", `{"term":70,"semantic":100}`, ""},
		{"零分是合法值", `{"term":0,"semantic":0}`, ""},
		{"允许代码块", "```json\n{\"term\":80,\"semantic\":90}\n```", ""},
		{"空对象", `{}`, "缺少"},
		{"缺term", `{"semantic":90}`, "缺少"},
		{"缺semantic", `{"term":90}`, "缺少"},
		{"负分", `{"term":-1,"semantic":90}`, "越界"},
		{"超百分", `{"term":90,"semantic":101}`, "越界"},
		{"类型错", `{"term":"90","semantic":90}`, "非 JSON"},
		{"非JSON", `完全看不懂`, "非 JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term, semantic, err := ParseJudgeScores(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("合法评分不应报错: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("应报 %q，实得 term=%v semantic=%v err=%v", tc.wantErr, term, semantic, err)
			}
		})
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
