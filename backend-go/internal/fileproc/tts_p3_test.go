// ============ tts_p3_test.go · 职责说明 ============
// P3 译文朗读（TTS）的纯函数回归锁。三条必守口径：
//
//	①**入参护栏必须在子进程之前**：空文本/超长文本若让它们跑到 CreateTemp+子进程，
//	  浪费的是几十秒本机 CPU——这类外部可控的低质请求最擅长把执行队列占满。
//	②**错误必须有人话**：每种 ERR: variant 都要落到面向用户的一句；
//	  落到 default（返回空串）等于把 Python 的异常堆栈原样抛给用户。
//	③**译文第一**：本文件的断言只涉及"这次朗读能不能成"，
//	  朗读失败的任何形态都不许牵动译文（那是调用方的职责，见 chat_audio.go 头注）。
//
// =============================================
package fileproc

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpeakToWavInputGuards ① 空/超长文本拦在创建临时文件之前。
// ★ 判据是「错误消息里不含子进程失败的字样」——只断言"报错了"是不够的：
//
//	护栏摘掉之后流程照样会报错（脚本缺失/依赖缺），那种断言会恒绿。
func TestSpeakToWavInputGuards(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"空文本", "   "},
		{"纯空白换行", "\n\t "},
		{"超长文本", strings.Repeat("译", ttsMaxChars+1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "never.wav")
			err := SpeakToWav(context.Background(), c.text, "en", out)
			if err == nil {
				t.Fatalf("%s：应被护栏拒绝，实际返回 nil", c.name)
			}
			if strings.Contains(err.Error(), "TTS 合成失败") || strings.Contains(err.Error(), "TTS 脚本缺失") {
				t.Fatalf("%s：这一形态不该走到子进程，实际错误：%v", c.name, err)
			}
		})
	}
}

// TestSpeakToWavAcceptsNormalLength 反向对照：正常长度的译文**不该**被护栏拦掉
// （否则上一条的"拒绝"可能只是判据恒红，护栏合不合理看不出）。
func TestSpeakToWavAcceptsNormalLength(t *testing.T) {
	text := strings.Repeat("译", ttsMaxChars-1)
	out := filepath.Join(t.TempDir(), "should-pass-guard.wav")
	err := SpeakToWav(context.Background(), text, "en", out)
	if err == nil {
		return // 环境真装了依赖并合成成功：护栏显然没误拦
	}
	// 走到这里必须是"护栏之外"的失败形态：能力不可用 / 脚本缺失 / 子进程失败。
	for _, blocked := range []string{"缺乏", "TTS 语音合成（TTS）不可用", "TTS 脚本缺失", "TTS 合成失败"} {
		if strings.Contains(err.Error(), blocked) {
			return
		}
	}
	if strings.Contains(err.Error(), "译文过长") {
		t.Fatalf("正常长度（%d 字，上限 %d）被长度护栏误拦：%v", len([]rune(text)), ttsMaxChars, err)
	}
}

// TestFriendlyTtsError ② 错误 variant → 用户话术的映射锁。
func TestFriendlyTtsError(t *testing.T) {
	cases := map[string]bool{ // variant → 是否必须有话术
		"ModelLoad":       true,
		"Asset":           true,
		"Voice":           true,
		"UnsupportedLang": true,
		"Empty":           true,
		"Import":          true,
		"Encode":          true,
		"TotallyUnknown":  false, // 未知 variant：返回空串，由调用方给原始错误
	}
	for variant, want := range cases {
		got := friendlyTtsError("ERR:" + variant + ":某处细节")
		if want && got == "" {
			t.Errorf("friendlyTtsError(%s) 返回空串 ⇒ 该 variant 没有落到用户话术", variant)
		}
		if !want && got != "" {
			t.Errorf("friendlyTtsError(%s) 不该有话术，实际 %q", variant, got)
		}
	}
	if got := friendlyTtsError("没有 ERR 前缀的普通 stderr"); got != "" {
		t.Errorf("非 ERR: 行应返回空串，实际 %q", got)
	}
}
