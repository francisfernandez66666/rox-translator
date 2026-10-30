// ============================================================================
// capsvc_unit_gate_test.go —— systemd 单元文件的机械锁（方案二 A7）。
//
// 为什么锁文件而不是锁代码：capsvc 的可靠性有一半在**单元配置**里——
//
//	· `Restart=always`：权限/临时故障不许把它永久留在 dead（那一秒之后所有能力都在回落子进程，
//	  用户无感、日志不红，只有下次用的人发现冷启动又回来了）；
//	· `MemoryMax`：进程内 LRU 预算（CAPSVC_MAX_RSS_MB）漏掉的大缓冲由内核硬兜底，
//	  1.6G 生产机上这一行是"常驻件不许拖垮整机"的最后一道；
//	· `RuntimeDirectory=langcross`：socket 目录由 systemd 建/清（手工 mkdir 会留 stale socket）；
//	· ExecStart 的 `--socket` 必须与 Go 侧 `CapsvcDefaultSocket` **等值**——
//	  两边各写一份是"部署了但客户端永远 off"的经典形态（读数与事实相反）。
//
// 读法沿用 postdeploy_exit_gate_test.go 的跨目录只读口径：本文件只读仓库文本，不碰现网。
// ============================================================================
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// capsvcUnitPath 单元文件相对包目录的位置（internal/fileproc → 仓库根 → deploy/capsvc）。
func capsvcUnitPath() string {
	return filepath.Join("..", "..", "..", "deploy", "capsvc", "translator-capsvc.service")
}

func readCapsvcUnit(t *testing.T) []string {
	t.Helper()
	p := capsvcUnitPath()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读不到单元文件 %s: %v（A7 的靶子没了 → 闸门失明，别放宽）", p, err)
	}
	return strings.Split(string(b), "\n")
}

func TestCapsvcUnitHasRestartAlways(t *testing.T) {
	lines := readCapsvcUnit(t)
	for _, l := range lines {
		if strings.TrimSpace(l) == "Restart=always" {
			return
		}
	}
	t.Fatalf("%s 缺少 Restart=always（自动恢复档）：临时故障会把常驻件永久留在 dead，而上游回落让这件事没有任何症状", capsvcUnitPath())
}

func TestCapsvcUnitHasRestartSec(t *testing.T) {
	lines := readCapsvcUnit(t)
	for _, l := range lines {
		if strings.TrimSpace(l) == "RestartSec=3" {
			return
		}
	}
	t.Fatalf("%s 缺少 RestartSec=3（重启节流：没有它坏配置会打成重启风暴）", capsvcUnitPath())
}

func TestCapsvcUnitHasMemoryMax(t *testing.T) {
	lines := readCapsvcUnit(t)
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "MemoryMax=") && strings.TrimPrefix(trimmed, "MemoryMax=") != "" {
			return
		}
	}
	t.Fatalf("%s 缺少 MemoryMax（内核硬兜底：进程内估算漏掉的大缓冲会拖垮 1.6G 整机）", capsvcUnitPath())
}

func TestCapsvcUnitHasRuntimeDirectory(t *testing.T) {
	lines := readCapsvcUnit(t)
	for _, l := range lines {
		if strings.TrimSpace(l) == "RuntimeDirectory=langcross" {
			return
		}
	}
	t.Fatalf("%s 缺少 RuntimeDirectory=langcross（socket 目录必须由 systemd 建/清）", capsvcUnitPath())
}

// ExecStart 的 socket 路径必须与 Go 侧默认值**等值**；解释器必须落在生产 venv 判定链上。
func TestCapsvcUnitSocketPathMatchesGoDefault(t *testing.T) {
	lines := readCapsvcUnit(t)
	want := "--socket " + CapsvcDefaultSocket
	var exec string
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "ExecStart=") {
			exec = strings.TrimSpace(l)
			break
		}
	}
	if exec == "" {
		t.Fatalf("%s 没有 ExecStart（单元根本起不来）", capsvcUnitPath())
	}
	if !strings.Contains(exec, want) {
		t.Fatalf("ExecStart 的 socket 路径必须与 Go 侧 CapsvcDefaultSocket 等值（期望含 %q，实际 %q）", want, exec)
	}
	if !strings.Contains(exec, "/opt/translator/.venv/bin/python3") {
		t.Fatalf("ExecStart 的解释器必须是 pyBin() 判定链的生产档（/opt/translator/.venv/bin/python3），实际 %q", exec)
	}
}

// 预算/空闲/并发三个 env 与代码里的默认值同源；少一个就等于"运营以为配了、其实走默认"。
func TestCapsvcUnitEnvironments(t *testing.T) {
	lines := readCapsvcUnit(t)
	want := []string{
		"Environment=CAPSVC_MAX_RSS_MB=700",
		"Environment=CAPSVC_IDLE_UNLOAD_SEC=600",
		"Environment=CAPSVC_OCR_WORKERS=2",
	}
	joined := strings.Join(lines, "\n")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Fatalf("%s 缺少 %s（与 fileproc/capsvc.py 的默认值同源，改一侧必红）", capsvcUnitPath(), w)
		}
	}
}
