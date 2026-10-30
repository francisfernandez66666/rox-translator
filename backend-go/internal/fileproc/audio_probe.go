// ============ audio_probe.go · 职责说明 ============
// 音频时长探测（★ 2026-10-30 三事合一批 组4，C-3）。
//
// 为什么必须探测而不是"看体积估"：同是 30 秒，opus 24kbps 约 90 KB、wav 44.1k 约 2.6 MB——
// 按体积判会把"一段 3 分钟的 wav"放行成免费档，也会把"一段 20 秒的高码率录音"误判成超限。
//
// 口径三条：
//
//	① 唯一实现：`ffprobe -v error -show_entries format=duration -of csv=p=0 <path>`；
//	② **探测失败一律报错**（`unreadable`），不做静默放行——放行等于"解不开的长音频全免费"，
//	   而且客户拿到的是 500 或者一个空结果，比明确说"这段录音我听不了"更坏；
//	③ ffmpeg 已是部署依赖（ASR 归一化在用），不新增依赖。
//
// =============================================
package fileproc

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// audioProbeTimeout ffprobe 的超时（它只读容器头，正常几十毫秒）。
const audioProbeTimeout = 5 * time.Second

// AudioDuration 返回音频时长（秒）。失败时返回 error（调用方必须按 `unreadable` 处置）。
func AudioDuration(path string) (float64, error) {
	if strings.TrimSpace(path) == "" {
		return 0, fmt.Errorf("音频路径为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), audioProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, fmt.Errorf("无法读取音频时长（ffprobe）: %w", err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" || s == "N/A" {
		return 0, fmt.Errorf("无法读取音频时长（ffprobe 未返回 duration）")
	}
	sec, perr := strconv.ParseFloat(strings.SplitN(s, "\n", 2)[0], 64)
	if perr != nil || sec < 0 {
		return 0, fmt.Errorf("音频时长读数非法: %q", s)
	}
	return sec, nil
}
