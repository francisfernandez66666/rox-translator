// ============ 本文件职责中文说明 ============
// ⑮（2026-10-05 第 3 波）USDT 到账监听存活态断言：
//
//	状态词四档（disabled/unknown/ok/failing）· 连续 3 轮才落 usdt_watch_dead 告警 ·
//	恢复自动收敛告警 · 收银台「自动入账」承诺跟着监听实况走（auto_settle_live）·
//	/api/health 只出状态词不出上游地址与收款地址 · 周期任务接线（真拨一次死上游）。
//
// 反证口径见各用例注释；本批现网实证：裸打 /v1/blocks 恒 404、8 天零成功而界面照旧承诺自动入账。
// =============================================
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/store"
)

// newUsdtWatchServer 内存库 + USDT「开关全开、只挂 trc20 一条链」的最小服务器。
// ★ 进出各清一次进程级监听状态：usdtWatch 是包级单例，用例共享它——
//
//	不归零就是「上一个用例的失败计数决定这一个用例的状态词」，同族假绿/假红。
func newUsdtWatchServer(t *testing.T) *Server {
	t.Helper()
	s := newProbeStoreServer(t)
	for k, v := range map[string]string{
		"usdt_enabled":           "1",
		"usdt_auto_settle":       "1",
		"usdt_addr_trc20":        "T" + strings.Repeat("a", 33),
		"usdt_rate_fen_per_usdt": "720",
	} {
		if err := s.Store.SetConfig(k, v); err != nil {
			t.Fatalf("置 %s: %v", k, err)
		}
	}
	usdtWatch.markDisabled()
	t.Cleanup(usdtWatch.markDisabled)
	return s
}

// mustSetCfg 写一条 system_config（用例里频繁翻档位，统一报错口径）
func mustSetCfg(t *testing.T, s *Server, key, value string) {
	t.Helper()
	if err := s.Store.SetConfig(key, value); err != nil {
		t.Fatalf("置 %s=%s: %v", key, value, err)
	}
}

// openWatchAlerts 库里 open 状态的 usdt_watch_dead 行
func openWatchAlerts(t *testing.T, s *Server) []*store.Alert {
	t.Helper()
	all, err := s.Store.ListAlerts(0, "open", 200)
	if err != nil {
		t.Fatalf("查告警: %v", err)
	}
	var out []*store.Alert
	for _, a := range all {
		if a.Kind == usdtWatchAlertKind {
			out = append(out, a)
		}
	}
	return out
}

// TestUsdtWatchHealthWordFourTiers 状态词四档逐档实测（含"一轮即 failing"那条早档）。
// 反证：把 word() 的判据从 fails>0 放宽回 fails>=usdtWatchFailAlert ⇒
// 「头两轮失败仍报 ok」，本用例第三段当场红（现网 8 天零成功却无人可见的形态就是这么留着的）。
func TestUsdtWatchHealthWordFourTiers(t *testing.T) {
	s := newUsdtWatchServer(t)
	if got := s.usdtWatchHealthWord(); got != usdtWatchUnknown {
		t.Fatalf("开关全开、一轮没跑应 unknown，实得 %q", got)
	}
	// ① 开关关掉 ⇒ disabled（不是 unknown：根本没在跑）
	if err := s.Store.SetConfig("usdt_enabled", "0"); err != nil {
		t.Fatal(err)
	}
	if got := s.usdtWatchHealthWord(); got != usdtWatchDisabled {
		t.Fatalf("关闸应回 %q，实得 %q", usdtWatchDisabled, got)
	}
	if s.usdtWatchAllowsAutoPromise() {
		t.Fatal("关闸时不许承诺自动入账")
	}
	// ② 开闸、第一 tick 未到 ⇒ unknown（"刚启动"不是"没问题"）
	if err := s.Store.SetConfig("usdt_enabled", "1"); err != nil {
		t.Fatal(err)
	}
	if got := s.usdtWatchHealthWord(); got != usdtWatchUnknown {
		t.Fatalf("未跑过应回 %q，实得 %q", usdtWatchUnknown, got)
	}
	if s.usdtWatchAllowsAutoPromise() {
		t.Fatal("unknown 档不许承诺自动入账（拿没探过当没问题＝⑮ 假承诺的原始形态）")
	}
	// ③ 一轮全链成功 ⇒ ok
	s.usdtWatchReportRound("trc20", nil)
	if got := s.usdtWatchHealthWord(); got != usdtWatchOK {
		t.Fatalf("成功一轮应回 %q，实得 %q", usdtWatchOK, got)
	}
	if !s.usdtWatchAllowsAutoPromise() {
		t.Fatal("ok 档应允许承诺自动入账")
	}
	// ④ 失败一轮 ⇒ 立刻 failing（对客承诺这一档就停）
	s.usdtWatchReportRound("trc20", errors.New("上游返回 404"))
	if got := s.usdtWatchHealthWord(); got != usdtWatchFailing {
		t.Fatalf("失败一轮应回 %q，实得 %q", usdtWatchFailing, got)
	}
	if s.usdtWatchAllowsAutoPromise() {
		t.Fatal("failing 档不许承诺自动入账")
	}
	// 告警腿在这一档**还没**触发（3 轮才判死，见下一个用例）
	if n := len(openWatchAlerts(t, s)); n != 0 {
		t.Fatalf("单轮失败不该落告警（抖动刷屏），实得 %d 行", n)
	}
}

// TestUsdtWatchAlertsAfterThreeConsecutiveRounds 连续 3 轮 ⇒ 恰好 1 条 critical usdt_watch_dead；
// 第 4、5 轮不再堆行（CreateAlert 的 open 去重）；告警正文不得带上游地址/收款地址。
func TestUsdtWatchAlertsAfterThreeConsecutiveRounds(t *testing.T) {
	s := newUsdtWatchServer(t)
	for i := 1; i <= usdtWatchFailAlert-1; i++ {
		s.usdtWatchReportRound("trc20", errors.New("链头 404"))
		if n := len(openWatchAlerts(t, s)); n != 0 {
			t.Fatalf("第 %d 轮（未到阈值）不该落告警，实得 %d 行", i, n)
		}
	}
	s.usdtWatchReportRound("trc20", errors.New("链头 404"))
	alerts := openWatchAlerts(t, s)
	if len(alerts) != 1 {
		t.Fatalf("第 %d 轮应恰好落 1 条告警，实得 %d 条", usdtWatchFailAlert, len(alerts))
	}
	a := alerts[0]
	if a.Level != "critical" {
		t.Fatalf("告警级别应为 critical，实得 %q", a.Level)
	}
	if a.TenantID != 0 {
		t.Fatalf("监听死亡属平台级（tid=0），实得 %d", a.TenantID)
	}
	if !strings.Contains(a.Message, "自动入账") {
		t.Fatalf("告警正文要点明对客承诺已停摆，实得 %q", a.Message)
	}
	// 纪律（同 AGENTS §一·12）：这条会进管理台告警中心 ⇒ 不带 URL／主机／收款地址
	for _, bad := range []string{"://", "trongrid", "T" + strings.Repeat("a", 33)} {
		if strings.Contains(strings.ToLower(a.Message), strings.ToLower(bad)) {
			t.Fatalf("告警正文不得出现 %q（只出状态与链名），实得 %q", bad, a.Message)
		}
	}
	// 第 4、5 轮不堆行
	for i := 4; i <= 5; i++ {
		s.usdtWatchReportRound("trc20", errors.New("链头 404"))
		if n := len(openWatchAlerts(t, s)); n != 1 {
			t.Fatalf("第 %d 轮仍应只有 1 条 open 告警（去重），实得 %d", i, n)
		}
	}
}

// TestUsdtWatchRecoveryResolvesAlert 恢复一轮 ⇒ open 告警收敛为 resolved（不靠人记着去点）。
// 反证：摘掉 usdtWatchReportRound 里的 tookRecovery 分支 ⇒ 告警永久挂着，
// 运营对着"监听已死"的 critical 却已经是好的，第二天下一次真故障反而看不见。
func TestUsdtWatchRecoveryResolvesAlert(t *testing.T) {
	s := newUsdtWatchServer(t)
	for i := 0; i < usdtWatchFailAlert; i++ {
		s.usdtWatchReportRound("trc20", errors.New("链头 404"))
	}
	if len(openWatchAlerts(t, s)) != 1 {
		t.Fatal("前置：应先有 1 条 open 告警")
	}
	s.usdtWatchReportRound("trc20", nil)
	if n := len(openWatchAlerts(t, s)); n != 0 {
		t.Fatalf("恢复后 open 告警应归零，实得 %d", n)
	}
	resolved, err := s.Store.ListAlerts(0, "resolved", 200)
	if err != nil {
		t.Fatal(err)
	}
	hit := 0
	for _, a := range resolved {
		if a.Kind == usdtWatchAlertKind && a.ResolvedAt != "" {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("应有 1 条带 resolved_at 的收敛记录，实得 %d", hit)
	}
	if got := s.usdtWatchHealthWord(); got != usdtWatchOK {
		t.Fatalf("恢复后状态词应回 %q，实得 %q", usdtWatchOK, got)
	}
	// 再死一次要能再报（wasFailing 标记被消费过）
	for i := 0; i < usdtWatchFailAlert; i++ {
		s.usdtWatchReportRound("trc20", errors.New("链头 404"))
	}
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("二次死亡应再落 1 条告警，实得 %d", n)
	}
}

// TestUsdtWatchTickMarksDisabledWhenSwitchOff 周期任务接线：开关关掉时把状态收进 disabled
// 并清掉历史计数（重新开闸后不许带着旧失败立刻误报 failing）。
func TestUsdtWatchTickMarksDisabledWhenSwitchOff(t *testing.T) {
	s := newUsdtWatchServer(t)
	s.usdtWatchReportRound("trc20", errors.New("链头 404")) // 先留一笔失败计数
	if err := s.Store.SetConfig("usdt_auto_settle", "0"); err != nil {
		t.Fatal(err)
	}
	s.usdtReconcileTick() // 不拨上游（开关关着就在第一道分支返回）
	if got := s.usdtWatchHealthWord(); got != usdtWatchDisabled {
		t.Fatalf("关自动核销应回 %q，实得 %q", usdtWatchDisabled, got)
	}
	if err := s.Store.SetConfig("usdt_auto_settle", "1"); err != nil {
		t.Fatal(err)
	}
	if got := s.usdtWatchHealthWord(); got != usdtWatchUnknown {
		t.Fatalf("重新开闸后应回到 unknown（旧计数已清），实得 %q", got)
	}
}

// TestUsdtWatchTickDialsDeadUpstreamThenRecovers 真拨一次监听链（不起 ticker，直接调 tick）：
// 端点指向一个没人听的端口 ⇒ 连续 3 次 tick 后落告警；把端点换成认 /v1/blocks/latest 的假上游
// ⇒ 下一次 tick 恢复并收敛告警。
// ★ 这条是「有 tracker 但周期任务根本没接上」的防空转锁：只测 usdtWatchReportRound
//
//	的纯逻辑，把 tick 里那两行调用删掉照样全绿——现网 ⑮ 恰恰是"代码在、腿没接"的形态。
func TestUsdtWatchTickDialsDeadUpstreamThenRecovers(t *testing.T) {
	s := newUsdtWatchServer(t)
	t.Setenv("USDT_TRON_BASE", "http://127.0.0.1:1") // 立刻 connection refused，不真出网
	for i := 0; i < usdtWatchFailAlert; i++ {
		s.usdtReconcileTick()
	}
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("3 轮拨不通应落 1 条告警，实得 %d（tick 里的 reportRound 接线是否被摘？）", n)
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v1/blocks/latest") {
			_, _ = w.Write([]byte(`{"block_header":{"number":1200000}}`))
			return
		}
		_, _ = w.Write([]byte(`{"transfers":[]}`))
	}))
	defer mock.Close()
	t.Setenv("USDT_TRON_BASE", mock.URL)
	s.usdtReconcileTick()
	if got := s.usdtWatchHealthWord(); got != usdtWatchOK {
		t.Fatalf("换成可用上游后一轮应回 %q，实得 %q", usdtWatchOK, got)
	}
	if n := len(openWatchAlerts(t, s)); n != 0 {
		t.Fatalf("恢复后 open 告警应归零，实得 %d", n)
	}
}

// TestUsdtCheckoutPromiseFollowsWatch auto_settle_live 联动腿：
// 开关（AutoSettleOn）与能力（AutoSettleLive）**必须分开出栈**——旧形态只投开关，
// 于是"开关开着但一次都没扫成功"这一整段（现网 8 天）界面上照样写"自动入账"。
func TestUsdtCheckoutPromiseFollowsWatch(t *testing.T) {
	s := newUsdtWatchServer(t)
	cfg := s.Store.GetUSDTCfg()
	meta := &store.USDTOrderMeta{
		Chain: "trc20", ToAddr: cfg.Addrs["trc20"],
		AmountMicro: 1_245_833, TailMicro: 3641, RateFen: 720, ExpiresAt: "2026-10-06T00:00:00Z",
	}
	p := s.usdtPayPayload(meta, cfg)
	if !p.AutoSettleOn {
		t.Fatal("前置：库里自动核销开关应是开的")
	}
	if p.AutoSettleLive {
		t.Fatal("unknown 档（还没跑过一轮）不许出 live=true")
	}
	s.usdtWatchReportRound("trc20", nil)
	if !s.usdtPayPayload(meta, cfg).AutoSettleLive {
		t.Fatal("ok 档应允许 live=true")
	}
	s.usdtWatchReportRound("trc20", errors.New("链头 404"))
	p2 := s.usdtPayPayload(meta, cfg)
	if !p2.AutoSettleOn {
		t.Fatal("开关档位不该被监听状态改写（那是运营的配置，不是我们的读数）")
	}
	if p2.AutoSettleLive {
		t.Fatal("failing 档必须把 live 翻成 false，收银台据此降级为人工核销文案")
	}
}

// TestHealthExposesUsdtWatchWordOnly /api/health 出 usdt_watch 状态词，且四种状态都不带敏感值。
// ★ 本端点无鉴权（同 dispatch 那一族纪律）：只许出状态词，绝不出上游主机、路径或收款地址。
func TestHealthExposesUsdtWatchWordOnly(t *testing.T) {
	s := newUsdtWatchServer(t)
	words := []string{}
	// 四档逐档拨一次（顺序即档位：disabled → unknown → ok → failing）
	drive := []func(){
		func() { mustSetCfg(t, s, "usdt_auto_settle", "0") },
		func() { mustSetCfg(t, s, "usdt_auto_settle", "1"); usdtWatch.markDisabled() },
		func() { s.usdtWatchReportRound("trc20", nil) },
		func() { s.usdtWatchReportRound("trc20", errors.New("链头 404")) },
	}
	want := []string{usdtWatchDisabled, usdtWatchUnknown, usdtWatchOK, usdtWatchFailing}
	for i, d := range drive {
		d()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		s.handleHealth(rec, req)
		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("health 出参非法 JSON: %v", err)
		}
		got, ok := body["usdt_watch"].(string)
		if !ok {
			t.Fatalf("health 缺 usdt_watch 字段：%s", rec.Body.String())
		}
		if got != want[i] {
			t.Fatalf("第 %d 档 health 应回 %q，实得 %q", i+1, want[i], got)
		}
		raw := rec.Body.String()
		for _, bad := range []string{"trongrid", "://", "T" + strings.Repeat("a", 33)} {
			if strings.Contains(strings.ToLower(raw), strings.ToLower(bad)) {
				t.Fatalf("health 出参不得出现 %q，实得 %s", bad, raw)
			}
		}
		words = append(words, got)
	}
	if strings.Join(words, ",") != "disabled,unknown,ok,failing" {
		t.Fatalf("四档逐字序列不符：%v", words)
	}
}
