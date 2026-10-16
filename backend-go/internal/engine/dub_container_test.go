// ============ dub_container_test.go · 职责说明 ============
// 音频工单「译后音频回到原文件格式」这条承诺的**决策层**单测（2026-10-16 用户令）。
//
// 被锁的东西只有一件：dubWithFallback 的两段式决策——先按输入容器合成，转码不可用才回落 mp3，
// 且"回落也失败"与"本来是 mp3 还失败"都必须如实报错（调用方据此只记 warning，绝不判死整单）。
//
// 为什么必须有这个文件：这段逻辑过去活在 file.go 音频分支里、要真 TTS 引擎才会走到，
// 于是「回原格式」这条承诺**一条断言都没有**（容器映射表本身的等值锁在 fileproc 侧，
// 但"回落该不该发生、发生几次"没人判）。这里用注入的 synth 把三条分支各钉一次。
// =============================================
package engine

import (
	"errors"
	"testing"

	"translator/internal/fileproc"
)

// TestDubWithFallbackTakesInputContainer 正路：输入容器可编码 ⇒ 就用它，不发生回落。
func TestDubWithFallbackTakesInputContainer(t *testing.T) {
	cases := []struct{ srcExt, want string }{
		{".mp3", ".mp3"},
		{".wav", ".wav"},
		{".m4a", ".m4a"},
		{".ogg", ".ogg"},
		{".opus", ".opus"},
		{".flac", ".flac"},
		{".aac", ".aac"},
		{".MP3", ".mp3"}, // 大小写归一
	}
	for _, c := range cases {
		var tried []string
		got, fellBack, err := dubWithFallback(c.srcExt, func(container string) error {
			tried = append(tried, container)
			return nil
		})
		if err != nil || fellBack {
			t.Fatalf("%s：应一次成功且不回落，实得 container=%q fallback=%v err=%v", c.srcExt, got, fellBack, err)
		}
		if got != c.want {
			t.Fatalf("%s：容器应为 %q，实得 %q ⇒ 产物名与原件容器不一致", c.srcExt, c.want, got)
		}
		if len(tried) != 1 || tried[0] != c.want {
			t.Fatalf("%s：应只在 %q 上试一次，实得 %v", c.srcExt, c.want, tried)
		}
	}
}

// TestDubWithFallbackFallsBackToMP3 转码不可用（缺 ffmpeg／该容器编码器）⇒ 回落 mp3，
// 且要如实把 fellBack 报出来（调用方据此在 GateWarnings 里点明容器已改，不静默换格式）。
func TestDubWithFallbackFallsBackToMP3(t *testing.T) {
	for _, srcExt := range []string{".m4a", ".ogg", ".opus", ".flac", ".aac", ".wav"} {
		var tried []string
		got, fellBack, err := dubWithFallback(srcExt, func(container string) error {
			tried = append(tried, container)
			if container != ".mp3" {
				return errors.New("ERR:Encode:该容器转码不可用")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s：回落 mp3 应当成功，实得 err=%v", srcExt, err)
		}
		if !fellBack || got != ".mp3" {
			t.Fatalf("%s：应回落 mp3 并置 fellBack，实得 container=%q fellBack=%v", srcExt, got, fellBack)
		}
		if len(tried) != 2 || tried[0] != fileproc.DubContainerFor(srcExt) || tried[1] != ".mp3" {
			t.Fatalf("%s：应先试原容器再试 mp3，实得 %v", srcExt, tried)
		}
	}
}

// TestDubWithFallbackReportsFailures 失败要如实报错（不许把"没出声"写成"成功"）：
// ① 输入本就是 mp3 且失败 ⇒ 不重试、直接报错；② 原容器失败且 mp3 也失败 ⇒ 报错（不回空容器）。
func TestDubWithFallbackReportsFailures(t *testing.T) {
	// ① 输入 mp3
	tried := 0
	got, fellBack, err := dubWithFallback(".mp3", func(string) error {
		tried++
		return errors.New("ERR:ModelLoad:合成器构造失败")
	})
	if err == nil || got != "" || fellBack {
		t.Fatalf("mp3 合成失败应直接报错，实得 container=%q fellBack=%v err=%v", got, fellBack, err)
	}
	if tried != 1 {
		t.Fatalf("mp3 输入失败时不该重试（第一次就是 mp3），实试 %d 次", tried)
	}
	// ② 非 mp3 且回落也失败
	tried = 0
	got, fellBack, err = dubWithFallback(".ogg", func(string) error {
		tried++
		return errors.New("ERR:Encode:均不可用")
	})
	if err == nil || got != "" || fellBack {
		t.Fatalf("回落也失败时应报错并回空容器，实得 container=%q fellBack=%v err=%v", got, fellBack, err)
	}
	if tried != 2 {
		t.Fatalf("应先试原容器再试 mp3 共 2 次，实试 %d 次", tried)
	}
}

// TestDubWithFallbackUnknownContainerGoesMP3 白名单外的后缀（音频入口不该出现，但上游形态一变就会）
// 一律按 mp3 处理——"有声音"优先于"容器对得上"，且不重试两次（DubContainerFor 已回 mp3）。
func TestDubWithFallbackUnknownContainerGoesMP3(t *testing.T) {
	var tried []string
	got, fellBack, err := dubWithFallback(".weird", func(container string) error {
		tried = append(tried, container)
		return errors.New("ERR:Encode:不可用")
	})
	if err == nil || got != "" || fellBack {
		t.Fatalf("表外后缀按 mp3 处理且失败 ⇒ 应报错，实得 container=%q fellBack=%v err=%v", got, fellBack, err)
	}
	if len(tried) != 1 || tried[0] != ".mp3" {
		t.Fatalf("表外后缀只该在 mp3 上试一次，实得 %v", tried)
	}
}
