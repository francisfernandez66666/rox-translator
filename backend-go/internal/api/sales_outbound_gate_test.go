// ============ sales_outbound_gate_test.go · 职责说明 ============
// 三期 W5 外联域的静态锁（A16–A18，靶真实存在，反证可入 M0246–M0255）：
//   A16：sendOne 是全仓唯一发送出口（定义恰 1）；SMTP 在位性判据先于占位。
//   A17：先占位后发送——ClaimOutboundSend 必须排在 mailer().Send 之前（多实例只发一次的序）。
//   A18：runOutboundSequenceOnce 的 kill-switch（outbound_enabled）先于任何 sendOne 调用，
//        且不做 @startup 启动即跑（watchdog.go 里 runOutboundSequenceOnce 恰 1 次且只在 ticker 循环里）。
//   A2/M0247：外联频限独立——本文件不出现 salesflow.DailyFreeLimit（那是 AI 免费额度）。
// ================================================
package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readNonTestSource 读 internal/api 下一个非测试源文件。
func readNonTestSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(".", name))
	if err != nil {
		t.Fatalf("读 %s 失败: %v", name, err)
	}
	return string(b)
}

// TestSendOneSingleOutlet A16：sendOne 唯一出口 + SMTP 判据先于占位。
func TestSendOneSingleOutlet(t *testing.T) {
	src := readNonTestSource(t, "sales_outbound_api.go")
	if n := strings.Count(src, "func (s *Server) sendOne("); n != 1 {
		t.Fatalf("sendOne 定义出现 %d 次（期望恰 1）：多一处＝第二发送出口（A16/M0246）", n)
	}
	idxLive := strings.Index(src, "if !smtpLive()")
	idxClaim := strings.Index(src, "ClaimOutboundSend")
	if idxLive < 0 || idxClaim < 0 || idxLive > idxClaim {
		t.Fatalf("SMTP 在位判据必须先于占位（live@%d claim@%d）：测试环境会把 Noop 当发送（A16）", idxLive, idxClaim)
	}
}

// TestOutboundClaimBeforeSend A17：先占位再发送的顺序锁。
// ★ 锚必须带 `rowID, first, err :=` 前缀形态——④ 分支（smtp_disabled 的 skip 占位）也有一次
//   ClaimOutboundSend 调用且天然靠前，裸串判据会把「合规 skip 占位」当成主路径占位（M0248 首跑实证）。
func TestOutboundClaimBeforeSend(t *testing.T) {
	src := readNonTestSource(t, "sales_outbound_api.go")
	body := src[strings.Index(src, "func (s *Server) sendOne("):]
	end := strings.Index(body, "\nfunc ")
	if end > 0 {
		body = body[:end]
	}
	idxClaim := strings.Index(body, "rowID, first, err := s.Store.ClaimOutboundSend")
	idxSend := strings.Index(body, "err = s.mailer().Send(")
	if idxClaim < 0 || idxSend < 0 {
		// 锚形态被改坏（如把 rowID 改名 / Send 不再复用 err）＝先占位的结构没了，一样判红。
		t.Fatal("先发送后占位（A17/M0248）：主路径占位与发送的锚形态失明")
	}
	if idxClaim > idxSend {
		t.Fatal("先发送后占位（A17/M0248）：多实例会重复发信")
	}
	// 发送结果只认终态回写：失败必须落 failed，不许落 sent。
	if i := strings.Index(body, "MarkOutboundFailed"); i < 0 {
		t.Fatal("sendOne 没有失败终态回写：发送失败会被写成「已发送」")
	}
}

// TestOutboundSweepQuiet A18：kill-switch 先行 + 不做 @startup 启动即跑。
func TestOutboundSweepQuiet(t *testing.T) {
	src := readNonSource(t, "sales_outbound_api.go")
	body := src[strings.Index(src, "func (s *Server) runOutboundSequenceOnce("):]
	end := strings.Index(body, "\nfunc ")
	if end > 0 {
		body = body[:end]
	}
	idxGate := strings.Index(body, `GetConfig("outbound_enabled")`)
	idxSend := strings.Index(body, "s.sendOne(")
	if idxGate < 0 || idxSend < 0 {
		t.Fatal("runOutboundSequenceOnce 截不到 kill-switch 或 sendOne 调用（A18 失明）")
	}
	if idxGate > idxSend {
		t.Fatalf("kill-switch 判据（@%d）必须先于发送调用（@%d）：开关成了摆设（A18/M0249）", idxGate, idxSend)
	}
	// watchdog 槽形态：拨轮在 ticker 循环里，不做 @startup 即跑（与 W2 sm-sweep 同形状）。
	wd := readNonSource(t, "watchdog.go")
	if n := strings.Count(wd, "s.runOutboundSequenceOnce"); n != 1 {
		t.Fatalf("watchdog.go 里 runOutboundSequenceOnce 出现 %d 次（期望恰 1）：多处＝@startup 启动即跑复活", n)
	}
}

// TestOutboundFreqCapIndependent A2/M0247：外联频限独立，不借 AI 免费额度。
func TestOutboundFreqCapIndependent(t *testing.T) {
	src := readNonSource(t, "sales_outbound_api.go")
	if strings.Contains(src, "DailyFreeLimit") || strings.Contains(src, "daily_free_limit") {
		t.Fatal("外联面出现 AI 每日免费额度键（DailyFreeLimit/daily_free_limit）：外联频限必须用独立的 freq_cap_per_day（A2 裁定）")
	}
	if !strings.Contains(src, "GuardOutboundConfigKey") {
		t.Fatal("外联频限没有走 store.GuardOutboundConfigKey（freq_cap_per_day）单一真值")
	}
}

// readNonSource 语义化包装（与 readNonTestSource 同实现，名字服务可读性）。
func readNonSource(t *testing.T, name string) string {
	return readNonTestSource(t, name)
}
