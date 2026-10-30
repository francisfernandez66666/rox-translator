// ============ ticket_test.go · 职责说明 ============
// 「工单交付不许再落盘 zip」的装配闸（★ 2026-10-30 三事合一批 · 组5 两段式交付）。
//
// 本文件的前身是 G18（2026-10-19）「多语言产物打包失败不许静默丢产物」的 AST 闸：
// 它钉的是 zipOutputs 的失败分支必须出声/发站内信/计数。两段式交付之后**落盘 zip 这一档整个退役**
// （产物逐件登记在 ticket_artifacts，取件由 API 按需流式打包，见 api/ticket_artifacts.go），
// G18 守的那个分支不复存在——但它的关切仍在：**产物不许因为"打包"这件事静默变少**，
// 只是答案从"打包失败要出声"变成"根本不该打包"（按需打包没有中间产物可丢）。
//
// 因此本文件改钉三件事（B8）：
//  1. service/ticket.go 里零 `zipOutputs(` 调用、零 `zip.NewWriter`、不 import archive/zip；
//  2. result_path 写入必须来自 firstArtifactPathFor（产物表的首件），并保留 res.Files[0] 兜底；
//  3. 反向自证：合成一段"把 zipOutputs 调回来"的源码必须被扫描器判红（免得扫描器恒绿）。
//
// 判据为什么用源码扫描而不是运行时计数：这两个函数已被删除，任何调用都是**编译期**错误，
// 源码扫描在这里只是把"有人把它加回来"这件事提前到测试红，避免靠"记得别加"。
// 本文件只读源码、不连库。
// =============================================
package service

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// persistedZipCallRe 命中「落盘 zip」的三类写法（函数调用 / 直接构造 writer / 打路径）。
var persistedZipCallRe = regexp.MustCompile(`zipOutputs\s*\(|zip\.NewWriter\s*\(|zipDeliveryName\s*\(`)

// scanPersistedZipCalls 返回源码里命中的行（去掉纯注释行：本仓允许注释记录"这里曾经打包"）。
func scanPersistedZipCalls(src string) []string {
	var hits []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if persistedZipCallRe.MatchString(line) {
			hits = append(hits, strings.TrimSpace(line))
		}
	}
	return hits
}

// TestNoPersistedZipAfterTwoStage B8：两段式之后 service 包**不许**再落盘 zip。
func TestNoPersistedZipAfterTwoStage(t *testing.T) {
	src, err := os.ReadFile("ticket.go")
	if err != nil {
		t.Fatalf("读 ticket.go 失败: %v", err)
	}
	if hits := scanPersistedZipCalls(string(src)); len(hits) > 0 {
		t.Fatalf("service/ticket.go 仍有落盘 zip 的写法（两段式交付已改按需流式打包，产物逐件登记）：%v", hits)
	}
	if strings.Contains(string(src), `"archive/zip"`) {
		t.Error("service/ticket.go 仍 import archive/zip：本包不该再产生 zip 文件")
	}
	// 正向对照：result_path 必须来自产物表首件（否则「不再打包」可能只是"没写 result_path"）。
	if !strings.Contains(string(src), "firstArtifactPathFor(") {
		t.Error("ticket.go 未使用 firstArtifactPathFor：result_path 的兼容写法丢了（保留期清理/历史客户端都读它）")
	}
	// 兜底腿仍在：引擎没发事件（历史/异常路径）时要回落 res.Files[0]，否则新单可能 result_path 为空。
	if !strings.Contains(string(src), "res.Files[0]") {
		t.Error("ticket.go 缺 res.Files[0] 兜底：引擎未发产物事件时 result_path 会留空")
	}
}

// TestPersistedZipScannerHasTeeth 反向自证：把打包调用写回去必须被扫描器抓到。
func TestPersistedZipScannerHasTeeth(t *testing.T) {
	const bad = `package service
func f() {
	zp, zerr := zipOutputs(paths, name)
	_ = zp
	_ = zerr
	zw := zip.NewWriter(w)
	_ = zw
}
`
	if hits := scanPersistedZipCalls(bad); len(hits) != 2 {
		t.Fatalf("扫描器应命中 2 行落盘 zip 写法，实得 %d: %v ⇒ 反证失败（扫描器可能恒绿）", len(hits), hits)
	}
	const ok = `package service
func f() {
	// 这里曾经 zipOutputs(...) 打过包，两段式之后退役
	p := s.firstArtifactPathFor(t.ID, 0)
	_ = p
}
`
	if hits := scanPersistedZipCalls(ok); len(hits) != 0 {
		t.Fatalf("注释行不该被判红（本仓允许注释记录历史）：%v", hits)
	}
}
