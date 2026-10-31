// ============ audio_probe_test.go · 职责说明 ============
// N5（★ 2026-10-31 补齐）：`AudioDuration` 探测失败必须是**错误**，由调用方归 `unreadable`，
// 不许静默放行、也不许读成"0 秒免费档"。
//
//	① 空路径/不存在文件/非音频字节（ffprobe 非零）都必须 error；
//	② ffprobe 不在 PATH 时也必须 error（部署机漏装 ffmpeg 的表现正是这一档，
//	   而此时 asr_ready 恒 false＋零报错行——本腿保证"读不到时长"不会变成"放行"）。
//
// =============================================
package fileproc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAudioDurationRejectsBrokenInput(t *testing.T) {
	if _, err := AudioDuration(""); err == nil {
		t.Fatal("N5：空路径必须报错")
	}
	dir := t.TempDir()
	if _, err := AudioDuration(filepath.Join(dir, "missing.wav")); err == nil {
		t.Fatal("N5：不存在的文件必须报错")
	}
	notAudio := filepath.Join(dir, "not-audio.wav")
	if err := os.WriteFile(notAudio, []byte("this is not audio"), 0o600); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}
	if _, err := AudioDuration(notAudio); err == nil {
		t.Fatal("N5：非音频字节必须报错（不许把 0 秒读成免费档）")
	}
	// ffprobe 不在 PATH：与部署机漏装 ffmpeg 同形，必须 error 而不是 0。
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	if _, err := AudioDuration(notAudio); err == nil {
		t.Fatal("N5：ffprobe 缺失时必须报错（漏装 ffmpeg 不许静默放行）")
	}
}
