// ============ 本文件职责中文说明 ============
// ⑮（2026-10-05 第 3 波）USDT 到账监听存活态断言：
//
//	状态词四档（disabled/unknown/ok/failing）· 连续 3 轮才落 usdt_watch_dead 告警 ·
//	恢复自动收敛告警 · 收银台「自动入账」承诺跟着监听实况走（auto_settle_live）·
//	/api/health 只出状态词不出上游地址与收款地址 · 周期任务接线（真拨一次死上游）。
//
//	★ (53) 2026-10-10 补的三条腿：换件／重启之后才好的那一族也必须收敛遗留告警（恢复腿有两条）、
//	每进程只核一次（不许每 30s 查库）、没跑过／链清单空／清单外仍有失败计数这三种形态宁可不收敛。
//
// 反证口径见各用例注释；本批现网实证：裸打 /v1/blocks 恒 404、8 天零成功而界面照旧承诺自动入账。
// =============================================
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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
// 端点指向一个没人听的端口 ⇒ 连续 3 次 tick 后落告警；把端点换成**按真上游形态应答**的假上游
// （★ ㊾ 2026-10-08：POST /wallet/getnowblock ＋ 高度在 block_header.raw_data.number）
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
		// ★ 桩的形态**来源**＝㊾ 本机独立网络对真上游的实测读数（只认 POST 那一族，
		//   高度在嵌套的 raw_data.number）。旧桩在这里服务 /v1/blocks/latest，
		//   等于给一条不存在的路发证——T42 那批"全绿而现网恒 404"的同形死法不许再来一次。
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/wallet/getnowblock") {
			_, _ = w.Write([]byte(`{"block_header":{"raw_data":{"number":1200000}}}`))
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

// TestUsdtWatchStaleAlertResolvedAfterRestart ★ (53) 本体：换件／重启**之后**才好的那一族，
// 库里那条 open 的 usdt_watch_dead 必须被收敛。
//
//	现网实证（2026-10-10 复查）：10-06 12:17 落的那条 open 行一直挂着，而监听自 10-10 换件起
//	已连跑多轮 ok ⇒ 告警中心长期对着一个健康的事实报"监听已死"。旧形态的恢复腿只认
//	进程态 wasFailing，重启即清零，于是这一族告警**永远等不到**收敛那一刻。
//	危害不是难看：下一次真故障落在去重判据上（同 kind 已有 open 即跳过），
//	运营看到的仍是那条旧行，分不清"今天又死了"还是"上次那条还没关"。
//
// 反证：摘掉 usdtWatchReportRound 里 takeAlertReconcile 那一支 ⇒ 第一段当场红；
//
//	把 alertReconciled 一上来就置 true ⇒ 第一段同样红（"压根没核过"与"核过了"必须是可区分的读数）。
func TestUsdtWatchStaleAlertResolvedAfterRestart(t *testing.T) {
	s := newUsdtWatchServer(t)
	// 前置：手工放一条 open 行，**刻意不走 reportRound**——那条路会把 wasFailing 一起置上，
	//	就测不到"进程态已被重启清零、只有库里那行还活着"这一现网形态。
	if err := s.Store.CreateAlert(0, "critical", usdtWatchAlertKind, "前置：上一个进程留下的告警行"); err != nil {
		t.Fatalf("前置放告警: %v", err)
	}
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("前置：应有 1 条 open，实得 %d", n)
	}
	if got := s.usdtWatchHealthWord(); got != usdtWatchUnknown {
		t.Fatalf("前置：进程刚起应回 %q，实得 %q", usdtWatchUnknown, got)
	}
	// ① 第一轮就健康 ⇒ 遗留行收敛
	s.usdtWatchReportRound("trc20", nil)
	if n := len(openWatchAlerts(t, s)); n != 0 {
		t.Fatalf("重启后首轮健康应收敛遗留 open 行，实得 %d", n)
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
		t.Fatalf("应有 1 条带 resolved_at 的收敛记录（不是'行消失了'），实得 %d", hit)
	}
	// ② 同一进程内只核这一次：后面再冒出来的 open 行归**阈值腿＋同进程恢复腿**管，
	//	本进程不该每 30s 拿遗留核对去动告警面（开销口径，也是"谁负责收敛"的归属口径）。
	if err := s.Store.CreateAlertPerOrder(0, "critical", usdtWatchAlertKind, "②：绕过 open 去重再放一条"); err != nil {
		t.Fatalf("②放告警: %v", err)
	}
	s.usdtWatchReportRound("trc20", nil)
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("资格已消费后不该再自动关新行，实得 %d", n)
	}
	// ③ 两条恢复腿互不替代：同进程内死而复愈那一腿仍然在收（把 ② 那一行一起收敛）
	for i := 0; i < usdtWatchFailAlert; i++ {
		s.usdtWatchReportRound("trc20", errors.New("链头 404"))
	}
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("阈值腿遇已有 open 行应去重（仍是 1 条），实得 %d", n)
	}
	s.usdtWatchReportRound("trc20", nil)
	if n := len(openWatchAlerts(t, s)); n != 0 {
		t.Fatalf("同进程恢复腿应把 ② 那一行也收敛，实得 %d", n)
	}
}

// TestUsdtWatchStaleAlertNotResolvedWhenNotFullyHealthy (53) 的"宁可不收敛"三档：
// 一轮都没跑过／链清单是空的／当前这一轮失败——三种形态都不许动告警面，
// 而且**不许把领取资格的闸门提前消费掉**（否则等真健康那一轮来临时已经没机会了）。
func TestUsdtWatchStaleAlertNotResolvedWhenNotFullyHealthy(t *testing.T) {
	s := newUsdtWatchServer(t)
	if err := s.Store.CreateAlert(0, "critical", usdtWatchAlertKind, "前置：遗留的 open 行"); err != nil {
		t.Fatalf("前置放告警: %v", err)
	}
	// ① 一轮都没跑（unknown 档）：光读健康词不拨任何腿，告警面一字不动
	if got := s.usdtWatchHealthWord(); got != usdtWatchUnknown {
		t.Fatalf("①前置应 unknown，实得 %q", got)
	}
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("①没探过就收敛＝把假告警换成假绿灯，实得 %d", n)
	}
	// ② 失败一轮 ⇒ 不放行（这一档与"该不该关遗留行"是同一个判据：全链无失败）
	s.usdtWatchReportRound("trc20", errors.New("链头 404"))
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("②仍有失败计数时不许收敛，实得 %d", n)
	}
	// ③ 链清单空（把 chains 指到一条没配收款地址的链）⇒ 不许拿"空清单"当"全链健康"，
	//	且这一轮**没消费资格**：把清单恢复后再跑一轮健康就该收敛。
	mustSetCfg(t, s, "usdt_chains", "erc20") // erc20 没有 usdt_addr_erc20 ⇒ GetUSDTCfg 把它跳过
	s.usdtWatchReportRound("trc20", nil)     // trc20 这轮成功，失败计数已清零
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("③链清单为空时不许收敛，实得 %d", n)
	}
	mustSetCfg(t, s, "usdt_chains", "trc20")
	s.usdtWatchReportRound("trc20", nil)
	if n := len(openWatchAlerts(t, s)); n != 0 {
		t.Fatalf("③恢复清单后的健康轮应完成收敛（资格不许被空清单那轮提前吃掉），实得 %d", n)
	}
}

// TestUsdtWatchStaleAlertNotResolvedByOffListFailure (53) 的第四条：清单**外**那条链的历史失败
// 照样说明"监听不是全绿的"。运营中途收窄 usdt_chains 时，只扫当前清单的写法会把
// 那条其实还挂着的链当不存在，顺手把告警关掉。
// 反证：takeAlertReconcile 里那个 `for _, n := range w.fails` 换成 `for _, c := range chains` ⇒ 本用例第二段红。
func TestUsdtWatchStaleAlertNotResolvedByOffListFailure(t *testing.T) {
	s := newUsdtWatchServer(t)
	mustSetCfg(t, s, "usdt_addr_erc20", "0x"+"b"+strings.Repeat("0", 39))
	mustSetCfg(t, s, "usdt_chains", "trc20,erc20")
	if err := s.Store.CreateAlert(0, "critical", usdtWatchAlertKind, "前置：遗留的 open 行"); err != nil {
		t.Fatalf("前置放告警: %v", err)
	}
	if got := s.usdtWatchHealthWord(); got != usdtWatchUnknown {
		t.Fatalf("前置：两条链都还没探过应 unknown，实得 %q", got)
	}
	// erc20 挂一轮（此刻它还在清单里）
	s.usdtWatchReportRound("erc20", errors.New("EVM RPC 不通"))
	if got := s.usdtWatchHealthWord(); got != usdtWatchFailing {
		t.Fatalf("erc20 失败一轮应 failing，实得 %q", got)
	}
	// 运营把链收窄成只有 trc20：erc20 掉出清单，但它的失败计数还在
	mustSetCfg(t, s, "usdt_chains", "trc20")
	s.usdtWatchReportRound("trc20", nil)
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("清单外仍有失败计数时不许收敛遗留告警，实得 %d", n)
	}
	// 把 erc20 也探一次健康 ⇒ 全部已记录链无失败 ⇒ 收敛
	s.usdtWatchReportRound("erc20", nil)
	if n := len(openWatchAlerts(t, s)); n != 0 {
		t.Fatalf("全链健康后应完成收敛，实得 %d", n)
	}
}

// TestUsdtWatchReconcileGateZeroValueContract 领取资格那条判据的**状态机契约**（不经过服务器）：
// 零值态＝"还没核过"＋"一轮都没跑过所以不许领"；跑过一轮全绿才发资格；发过就不再发第二次。
// 反证：摘掉 takeAlertReconcile 里 `!w.everRan` 那一判 ⇒ 第二段红；
//
//	摘掉末尾 `w.alertReconciled = true` 那次回写 ⇒ 第四段红（每 30s 查一次库的形态）。
func TestUsdtWatchReconcileGateZeroValueContract(t *testing.T) {
	w := &usdtWatchState{fails: map[string]int{}}
	if !w.alertReconcilePending() {
		t.Fatal("零值态必须还没核过遗留告警")
	}
	if w.takeAlertReconcile([]string{"trc20"}) {
		t.Fatal("一轮都没跑过（everRan=false）不许领资格")
	}
	w.note("trc20", nil)
	if !w.takeAlertReconcile([]string{"trc20"}) {
		t.Fatal("跑过一轮且全链无失败应领到资格")
	}
	if w.takeAlertReconcile([]string{"trc20"}) {
		t.Fatal("资格只能领一次（每进程最多一次查库）")
	}
	if w.alertReconcilePending() {
		t.Fatal("领过之后快判档应翻成『已核过』")
	}
	// 关闸再开闸＝新的监控周期，那一课要能重做（否则"开关被拨了一下"就把补腿永久吃掉）
	w.markDisabled()
	if !w.alertReconcilePending() {
		t.Fatal("markDisabled 应把补腿闸门重新打开")
	}
}

// TestUsdtWatchSingletonNotPreReconciled ★ 反证 B 的落点，也是这条补腿唯一抓得到的形态：
// 把包级单例写成 `&usdtWatchState{..., alertReconciled: true}` 时，**上面所有行为用例照样全绿**——
// 因为 newUsdtWatchServer 每次先调 markDisabled()，把这一档重置回 false，测试永远看不到声明期的预置值。
// 于是"补腿在现网永不生效、遗留告警永远关不掉"这一族破坏只有源码级判据抓得到。
// ⚠️ 判据只扫 `var usdtWatch = ` 那一行（扫全文会被说明文字里的同名样字误伤，AGENTS §三 那条老坑），
//
//	且必须**找得到那一行**——找不到就是改名式空转，本用例直接红（负向锁一律配正锁）。
func TestUsdtWatchSingletonNotPreReconciled(t *testing.T) {
	src, err := os.ReadFile("pay_usdt_watch.go")
	if err != nil {
		t.Fatalf("读源文件失败（判据射程没了）: %v", err)
	}
	found := 0
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, "var usdtWatch = ") {
			continue
		}
		found++
		if strings.Contains(line, "alertReconciled: true") {
			t.Fatalf("单例不许预置『已核过』（那样 (53) 的补腿在现网永不跑）：%s", strings.TrimSpace(line))
		}
		if !strings.Contains(line, "fails: map[string]int{}") {
			t.Fatalf("单例声明形态变了，这条源码锁要跟着重写：%s", strings.TrimSpace(line))
		}
	}
	if found != 1 {
		t.Fatalf("`var usdtWatch = ` 应恰好 1 行，实得 %d（0＝判据空转）", found)
	}
}

// ============ (54) 资金腿三条：确认数、窗口、查询失败 ============

// usdtMockTron 起一个**按 ㊾ 实测形态**应答的假 TRON 上游，返回它的基础 URL。
// 两条腿的形态与 payment.TronFetcher 实际请求的保持一致：
//
//	POST <base>/wallet/getnowblock      → {"block_header":{"raw_data":{"number":<newest>}}}
//	GET  <base>/v1/accounts/<addr>/...  → transfersBody（mock_chain 简形态）
//
// ★ 桩的形态必须来自**客户端真实请求的那一条**，不是"我以为的契约"
//
//	（旧桩服务 /v1/blocks/latest ⇒ T42 那批"全绿而现网恒 404"的同形死法）。
func usdtMockTron(t *testing.T, newest int64, transfersBody string) string {
	t.Helper()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/wallet/getnowblock") {
			_, _ = fmt.Fprintf(w, `{"block_header":{"raw_data":{"number":%d}}}`, newest)
			return
		}
		_, _ = w.Write([]byte(transfersBody))
	}))
	t.Cleanup(mock.Close)
	return mock.URL
}

// newPendingUSDTOrder 建一笔 pending 的 USDT 订单＋收款要素（含尾数后的应收金额在 meta 里）。
func newPendingUSDTOrder(t *testing.T, s *Server) (orderID int64, m *store.USDTOrderMeta) {
	t.Helper()
	o, err := s.Store.CreateOrderChannel(1, 30000, 8.97, 0, "usdt", "")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	m, err = s.Store.CreateUSDTOrderMeta(o.ID, 1, "trc20", s.Store.GetUSDTCfg().Addrs["trc20"], 1_245_833, 720, true)
	if err != nil {
		t.Fatalf("挂收款要素失败: %v", err)
	}
	return o.ID, m
}

// usdtOrderStatus 直读 orders.status（判"钱有没有被自动置 paid"的唯一事实）。
func usdtOrderStatus(t *testing.T, s *Server, id int64) string {
	t.Helper()
	var st string
	if err := s.Store.DB().QueryRow(`SELECT status FROM orders WHERE id=?`, id).Scan(&st); err != nil {
		t.Fatalf("读订单 #%d 状态失败: %v", id, err)
	}
	return st
}

// alertsOfKind 库里 open 状态、指定 kind 的告警行数。
func alertsOfKind(t *testing.T, s *Server, kind string) int {
	t.Helper()
	all, err := s.Store.ListAlerts(0, "open", 200)
	if err != nil {
		t.Fatalf("查告警: %v", err)
	}
	n := 0
	for _, a := range all {
		if a.Kind == kind {
			n++
		}
	}
	return n
}

// TestUsdtScanSkipsDepositWithoutBlockHeight ★ (54) 确认数腿：块高读不到＝**未知**，不是"很久以前"。
//
//	旧形态 c = newest - d.BlockNo + 1，而 usdt_deposits.block_no 默认 0 ⇒
//	c ≈ 当前链头高度（现网 TRON 是 860 万级），"未达确认阈值"那条判据被**直接顶穿**：
//	一笔未确认、可回滚的转账立刻被置 paid，全程零告警。
//	上游漏发/换名字是现实存在的形态（㊾ 就是专门为链头读数写的容错读法），所以缺读数必须停在池里等下一轮。
//
// 反证：删掉 `if d.BlockNo <= 0 { continue }` 那块 ⇒ 第一段当场红（单测里 newest=200 就会把它顶穿）。
// ★ 第二段对照腿（同一轮里补上块高的一笔必须真的入账）：没有它，第一段就是在测一条本来就不通的链路。
func TestUsdtScanSkipsDepositWithoutBlockHeight(t *testing.T) {
	s := newUsdtWatchServer(t)
	cfg := s.Store.GetUSDTCfg()

	// ① 上游回执里**没有 block_number**（解析成 0），金额精确等于应收
	o1, m1 := newPendingUSDTOrder(t, s)
	bodyNoHeight := fmt.Sprintf(`{"transfers":[{"tx_id":"0xno-height","from":"0xVisitor","value":"%d"}]}`, m1.AmountMicro)
	t.Setenv("USDT_TRON_BASE", usdtMockTron(t, 200, bodyNoHeight))
	if err := s.usdtScanChain("trc20", cfg); err != nil {
		t.Fatalf("第一轮扫描应正常返回（缺块高不是扫描失败）: %v", err)
	}
	if st := usdtOrderStatus(t, s, o1); st != "pending" {
		t.Fatalf("缺块高的入账绝不许自动置 paid（旧形态 newest-0+1 顶穿确认闸），实得 %q", st)
	}
	// 那笔钱必须还留在未匹配池里（不是被丢掉、不是被判成孤儿）
	var stillThere bool
	for _, d := range s.Store.ListUnmatchedDepositsForChain("trc20", 200) {
		if d.TxHash == "0xno-height" {
			stillThere = true
		}
	}
	if !stillThere {
		t.Fatal("缺块高的入账应留在未匹配池等下轮，不该消失")
	}

	// ② 对照腿：同一时刻另一笔**带块高**的入账必须真的自动入账
	o2, m2 := newPendingUSDTOrder(t, s)
	bodyWithHeight := fmt.Sprintf(`{"transfers":[{"tx_id":"0xwith-height","block_number":100,"from":"0xVisitor","value":"%d"}]}`, m2.AmountMicro)
	t.Setenv("USDT_TRON_BASE", usdtMockTron(t, 200, bodyWithHeight))
	if err := s.usdtScanChain("trc20", cfg); err != nil {
		t.Fatalf("对照轮扫描失败: %v", err)
	}
	if st := usdtOrderStatus(t, s, o2); st != "paid" {
		t.Fatalf("对照腿失效：带块高、确认数足够（200-100+1=101 ≥ 默认 19）的那笔必须置 paid，实得 %q", st)
	}
}

// TestUsdtScanWindowIsPerChain ★ (54) 窗口腿：未匹配池的评估窗口必须**按链各开一个**。
//
//	旧形态是 `ListUnmatchedDeposits(200)`（全链共用一个 ORDER BY id ASC LIMIT 200）
//	再在 Go 里 `if d.Chain != chain { continue }`——一条链的孤儿积压过 200 行后，
//	最旧那 200 行被反复评估，**新到的钱永远进不了匹配**，而且零错误零日志
//	（表现是「客户转了账但一直不入账」，排障会先从链上端点找起，根因却在池子窗口）。
//
// 反证：把 usdtScanChain 那行换回全链窗口＋按链筛 ⇒ 本用例红（trc20 这笔在 205 行 erc20 后面）。
func TestUsdtScanWindowIsPerChain(t *testing.T) {
	s := newUsdtWatchServer(t)
	cfg := s.Store.GetUSDTCfg()
	// 205 条 erc20 孤儿先把全链窗口占满（id 全部靠前）
	for i := 0; i < 205; i++ {
		if _, err := s.Store.InsertUSDTDeposit(&store.USDTDeposit{
			Chain: "erc20", TxHash: fmt.Sprintf("0xstarved-%03d", i), FromAddr: "0xOld",
			AmountMicro: 1_300_000, BlockNo: 100, NewestBlockNo: 200}); err != nil {
			t.Fatalf("灌 erc20 孤儿 #%d: %v", i, err)
		}
	}
	o, m := newPendingUSDTOrder(t, s)
	body := fmt.Sprintf(`{"transfers":[{"tx_id":"0xfresh-trc","block_number":100,"from":"0xVisitor","value":"%d"}]}`, m.AmountMicro)
	t.Setenv("USDT_TRON_BASE", usdtMockTron(t, 200, body))
	if err := s.usdtScanChain("trc20", cfg); err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if st := usdtOrderStatus(t, s, o); st != "paid" {
		t.Fatalf("另一条链的孤儿积压不许饿死本链新入账，实得 %q", st)
	}
}

// TestUsdtMatchQueryFailureKeepsDepositUnmatched ★ (54) 诚实腿：匹配查询失败＝**结论未知**。
//
//	这一条测的是 api 侧分支的**接线**：查询报错时既不许把它读成"库里没有对应单"（那是
//	usdtTryMatch 里 order==nil 的静默分支），也不许把它读成"命中多单"（那会在告警中心
//	刷一条 usdt_ambiguous critical，让运营去人工裁决一笔根本没查过的钱）。
//	"err 必须原样交出来"那一层的判别锁在 store 包：TestFindPendingUSDTOrderKeepsQueryErrorDistinct
//	（把 store 的 `return nil, false, err` 折回 nil 只会红那一条，本用例两侧同形、无法区分）。
//
//
//	反证实测（/tmp 副本树，2026-10-10，五改四红一绿）：
//	① 把 qerr 折进 ambiguous 分支 ⇒ **红**（第二段抓到，usdt_ambiguous 凭空多一条 critical）；
//	② 把 `if qerr != nil {…return}` 整块删掉（＝旧形态"折成没有单"）⇒ **绿**——
//	   这一族在**状态层面与正确行为同形**（都是停在 pending、钱留在池里），
//	   api 侧的任何状态断言都区分不了，唯一的差别是那一条 WARN 日志。
//	   所以判别锁放在决策发生的那一层：store 包的
//	   TestFindPendingUSDTOrderKeepsQueryErrorDistinct（把 `return nil, false, err` 改回
//	   `return nil, false, nil` 实测**红**）。本用例抓的是②之外那两族能被状态看见的破坏。
func TestUsdtMatchQueryFailureKeepsDepositUnmatched(t *testing.T) {
	s := newUsdtWatchServer(t)
	cfg := s.Store.GetUSDTCfg()
	o, m := newPendingUSDTOrder(t, s)
	// 入账先幂等落库：块高与确认数都够，唯一坏的是"匹配那一次查询"
	if _, err := s.Store.InsertUSDTDeposit(&store.USDTDeposit{
		Chain: "trc20", TxHash: "0xquery-broken", FromAddr: "0xVisitor",
		AmountMicro: m.AmountMicro, BlockNo: 100, NewestBlockNo: 200}); err != nil {
		t.Fatalf("灌入账失败: %v", err)
	}
	if _, err := s.Store.DB().Exec("DROP TABLE usdt_orders"); err != nil {
		t.Fatalf("打掉 usdt_orders 失败: %v", err)
	}
	t.Setenv("USDT_TRON_BASE", usdtMockTron(t, 200, `{"transfers":[]}`))
	if err := s.usdtScanChain("trc20", cfg); err != nil {
		t.Fatalf("扫描本身应正常返回（匹配失败在池子里处理）: %v", err)
	}
	if n := alertsOfKind(t, s, "usdt_ambiguous"); n != 0 {
		t.Fatalf("查询失败不是「命中多单」，不许报 usdt_ambiguous（实得 %d 条）", n)
	}
	if n := alertsOfKind(t, s, "usdt_settled"); n != 0 {
		t.Fatalf("查询失败不许把订单写成已入账，实得 %d 条 usdt_settled", n)
	}
	if st := usdtOrderStatus(t, s, o); st != "pending" {
		t.Fatalf("订单必须原样停在 pending 等下一轮重评，实得 %q", st)
	}
	var stillThere bool
	for _, d := range s.Store.ListUnmatchedDepositsForChain("trc20", 200) {
		if d.TxHash == "0xquery-broken" {
			stillThere = true
		}
	}
	if !stillThere {
		t.Fatal("一次查询故障不许把这笔钱评估掉（留在池里下一轮自愈）")
	}
}

// ============ (54) 清单漂移那条腿：撤链 vs 配坏 ============

// TestUsdtWatchForgetUnlistedContract forgetUnlisted 的**状态机契约**（不经过服务器）：
// 只有"既不被扫描、又不在保留名单里"的链才被丢；返回值只说"有没有丢掉一条曾达告警档的"。
// 反证（/tmp 副本树实测）：把那个阈值分支改成"判了但不报"（`droppedAlerted = true` → `_ = n`）⇒ 第三段红
//
//	（直接删掉整个 if 块不算合法反证：`n` 随之成为未使用变量，红在编译期而不是行为上）；
//
//	把 `delete(w.fails, c)` 那行删掉 ⇒ 第四段红（计数还在 ⇒ takeAlertReconcile 永远领不到资格，
//	这正是 (53) 留下的那个"谁都关不掉"的死局）。
func TestUsdtWatchForgetUnlistedContract(t *testing.T) {
	w := &usdtWatchState{fails: map[string]int{}}
	for i := 0; i < usdtWatchFailAlert; i++ {
		w.note("erc20", errors.New("EVM RPC 不通"))
	}
	w.note("bep20", errors.New("短退避")) // 没到告警档的那一条，用来验返回值不被它翻成 true
	if w.forgetUnlisted([]string{"trc20", "erc20", "bep20"}) {
		t.Fatal("①都在保留名单里 ⇒ 一条都不许丢，也不许报『丢掉过达告警档的链』")
	}
	if w.fails["erc20"] != usdtWatchFailAlert || w.fails["bep20"] != 1 {
		t.Fatalf("①前置计数被改动：erc20=%d bep20=%d", w.fails["erc20"], w.fails["bep20"])
	}
	// ② 运营把 erc20／bep20 都从 usdt_chains 里删掉
	if !w.forgetUnlisted([]string{"trc20"}) {
		t.Fatal("②丢掉一条曾达告警档的链必须回 true（调用方据此收敛那条告警）")
	}
	if _, ok := w.fails["erc20"]; ok {
		t.Fatal("②已撤链的计数必须真删掉，留着就是把恢复腿永久顶死")
	}
	if _, ok := w.fails["bep20"]; ok {
		t.Fatal("②bep20 同样已撤链，应一并清掉")
	}
	// ③ 幂等：再跑一次没有东西可丢 ⇒ 回 false（不许每轮都"收敛"一次）
	if w.forgetUnlisted([]string{"trc20"}) {
		t.Fatal("③没有可丢的链时不许回 true")
	}
	// ④ 顶死被解开的那一侧：清掉之后，健康一轮就该领到遗留告警的核对资格
	w.note("trc20", nil)
	if !w.takeAlertReconcile([]string{"trc20"}) {
		t.Fatal("④撤链后全链无失败 ⇒ 必须能领到收敛遗留告警的资格（这一条就是 (54) 要解的死局）")
	}
}

// TestUsdtListedChainsKeepsChainWithoutAddress ★ (54) 的"不许洗绿"那一半：
// 一条链**还写在 usdt_chains 里、只是收款地址被清空**时，它不在这轮被扫描（GetUSDTCfg 把它剔了），
// 但保留名单必须还认它 —— 那种形态是配置故障，不是运营撤链。
// 拿 cfg.Chains 单当保留名单的后果：一次误清地址就把 critical 告警收敛成"已处理"，
// 而客户的钱从这一刻起真的不再自动入账（比"永不收敛"贵得多：那台会让人去查，这不会）。
// 反证：把 usdtListedChains 里读 `usdt_chains` 原文那三行删掉 ⇒ 第二段红（名单缩成只剩 trc20）。
func TestUsdtListedChainsKeepsChainWithoutAddress(t *testing.T) {
	s := newUsdtWatchServer(t)
	mustSetCfg(t, s, "usdt_chains", "trc20,erc20,bep20") // erc20/bep20 都不配收款地址
	cfg := s.Store.GetUSDTCfg()
	if len(cfg.Chains) != 1 || cfg.Chains[0] != "trc20" {
		t.Fatalf("前置：没配收款地址的链不该被扫描，实得 %v", cfg.Chains)
	}
	if got := strings.Join(s.usdtListedChains(cfg), ","); got != "trc20,erc20,bep20" {
		t.Fatalf("保留名单必须含配置里还写着的链（撤链≠配坏），实得 %q", got)
	}
	// 行为腿：erc20 先带着告警档的失败把告警落下来，再**只清空地址**（链还在配置里）
	mustSetCfg(t, s, "usdt_addr_erc20", "0x"+strings.Repeat("b", 40))
	mustSetCfg(t, s, "usdt_chains", "trc20,erc20")
	t.Setenv("USDT_TRON_BASE", usdtMockTron(t, 200, `{"transfers":[]}`)) // trc20 这一腿健康
	t.Setenv("USDT_ETH_RPC", "http://127.0.0.1:1")                       // erc20 这一腿立刻 refused
	for i := 0; i < usdtWatchFailAlert; i++ {
		s.usdtReconcileTick()
	}
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("前置：erc20 连续 3 轮不通应落 1 条 open 告警，实得 %d", n)
	}
	mustSetCfg(t, s, "usdt_addr_erc20", "") // 地址被清空，链名还留在 usdt_chains
	s.usdtReconcileTick()
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("链名还在配置里时不许把告警收敛掉（那是配置故障不是撤链），实得 %d", n)
	}
}

// TestUsdtWatchAlertResolvedWhenChainDelisted ★ (54) 的"能收敛"那一半（与上面那条配对，缺一半就是空转锁）：
// 运营把 erc20 **从 usdt_chains 里删掉**之后，那条"取不到 erc20 链头"的告警不再是关于这台的事实，
// 下一轮必须收敛掉，并且状态机不许再带着它的旧计数（否则收银台会被一条已经不存在的链钉死）。
// 反证：摘掉 usdtReconcileTick 里 forgetUnlisted 那三行 ⇒ 本用例红（告警永久挂着，
//
//	而 word() 已经报 ok——正是 (53) 修完之后仍然存在的最后一类假告警）。
func TestUsdtWatchAlertResolvedWhenChainDelisted(t *testing.T) {
	s := newUsdtWatchServer(t)
	mustSetCfg(t, s, "usdt_addr_erc20", "0x"+strings.Repeat("c", 40))
	mustSetCfg(t, s, "usdt_chains", "trc20,erc20")
	t.Setenv("USDT_TRON_BASE", usdtMockTron(t, 200, `{"transfers":[]}`))
	t.Setenv("USDT_ETH_RPC", "http://127.0.0.1:1")
	for i := 0; i < usdtWatchFailAlert; i++ {
		s.usdtReconcileTick()
	}
	if n := len(openWatchAlerts(t, s)); n != 1 {
		t.Fatalf("前置：应有 1 条 open 告警，实得 %d", n)
	}
	mustSetCfg(t, s, "usdt_chains", "trc20") // 撤链（与上面那条"只清地址"正好成对）
	s.usdtReconcileTick()
	if n := len(openWatchAlerts(t, s)); n != 0 {
		t.Fatalf("已撤链的告警应在下一轮收敛，实得 %d 条 open", n)
	}
	resolved, err := s.Store.ListAlerts(0, "resolved", 200)
	if err != nil {
		t.Fatal(err)
	}
	hit := 0
	for _, a := range resolved {
		if a.Kind == usdtWatchAlertKind {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("应留下 1 条带 resolved_at 的收敛记录，实得 %d", hit)
	}
	// 撤链之后：健康的那条链照常 ok，收银台照常承诺（旧计数不许把它钉在 failing）
	s.usdtReconcileTick()
	if got := s.usdtWatchHealthWord(); got != usdtWatchOK {
		t.Fatalf("撤链后 trc20 健康应回 %q，实得 %q（erc20 的旧计数是否还留着？）", usdtWatchOK, got)
	}
}

// lockedLogBuf 线程安全的日志捕获缓冲（与 internal/engine/review_purity_test.go 的 lockedBuf 同口径）。
// ★ 为什么上锁而不是裸 bytes.Buffer：observability 走的是 slog.Default()，换默认器的那一瞬
//
//	同进程其他 goroutine（本包用例里有后台周期任务）照样在写日志；裸 buffer 在 `-race` 下
//	是 **fatal error（concurrent map / slice 读写那族）**，recover 兜不住，会把整包用例带走。
type lockedLogBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write 实现 io.Writer（slog 的 handler 只认这一个入口）。
func (b *lockedLogBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String 取当前已捕获内容（加锁读，别绕过 Write 那把锁）。
func (b *lockedLogBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Reset 清空捕获内容——两段判据之间必须清，否则第 ② 段会看见第 ① 段那行而假绿。
func (b *lockedLogBuf) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// captureWatchLogs 把 slog 默认器指到内存缓冲（observability.Log 每次调用都现读 slog.Default()，
// 见 internal/observability/log.go；所以换默认器就能抓到，不需要给 observability 开测试后门）。
func captureWatchLogs(t *testing.T) *lockedLogBuf {
	t.Helper()
	b := &lockedLogBuf{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return b
}

// TestUsdtWatchDelistedResolutionIsNotLoggedAsRecovery ★ (54) 第三条腿的**文案档锁**。
//
//	「运营撤掉一条链」与「监听真的恢复了」是两件事，日志必须分得开：排障的人只会 grep「已恢复」，
//	把撤链写成恢复＝下一次同样的故障藏在一条假的健康读数后面（AGENTS §一·3「日志里的 from 档位是
//	对外排障契约」同族——文案与档位一旦冒充，读日志的人就被指到错误的方向上）。
//
// 两条腿一负一正，缺正锁的那条负向是空转（本批实测：先写完①去跑反证 N4，把分派那句摘掉**照样绿**，
// 因为整段"已恢复"不写也能满足"不许出现已恢复"）：
// ① chain_unlisted ⇒ 出「因链清单收窄而收敛（非恢复，请核配置）」＋零「USDT 到账监听已恢复」；
// ② post_restart_reconcile ⇒ 出「USDT 到账监听已恢复」（同一段代码的另一分支，正对照）。
func TestUsdtWatchDelistedResolutionIsNotLoggedAsRecovery(t *testing.T) {
	s := newUsdtWatchServer(t)
	buf := captureWatchLogs(t)

	// ① 撤链档：收敛一行，但文案必须是"非恢复"那一档。
	if err := s.Store.CreateAlert(0, "critical", usdtWatchAlertKind, "反证用遗留 open 行（撤链档）"); err != nil {
		t.Fatalf("前置：落一条 open 告警失败：%v", err)
	}
	s.resolveOpenWatchAlerts(usdtWatchWhyChainUnlisted)
	got := buf.String()
	if !strings.Contains(got, "因链清单收窄而收敛") {
		t.Fatalf("撤链档必须出声，且写的是『因链清单收窄而收敛』，实得日志=%q", got)
	}
	if strings.Contains(got, "USDT 到账监听已恢复") {
		t.Fatalf("撤链**不许**写成『已恢复』（监听并没有恢复）：实得日志=%q", got)
	}
	if !strings.Contains(got, `"why":"chain_unlisted"`) {
		t.Fatalf("撤链那行的 why 档名必须是 chain_unlisted（三个档名同为对外排障契约，逐字钉）：实得=%q", got)
	}

	// ② 正对照：恢复档必须写「已恢复」，且带 post_restart_reconcile。
	//    ★ 先清缓冲：①那一行还留在里面，不清的话这一段的"不许出现收窄"负向判据恒真。
	buf.Reset()
	if err := s.Store.CreateAlert(0, "critical", usdtWatchAlertKind, "反证用遗留 open 行（恢复档）"); err != nil {
		t.Fatalf("前置：再落一条 open 告警失败：%v", err)
	}
	s.resolveOpenWatchAlerts(usdtWatchWhyPostRestart)
	got2 := buf.String()
	if !strings.Contains(got2, "USDT 到账监听已恢复") {
		t.Fatalf("恢复档必须出『USDT 到账监听已恢复』（①那条负向判据的射程全靠这一条正锁撑着）：实得=%q", got2)
	}
	if strings.Contains(got2, "因链清单收窄") {
		t.Fatalf("真恢复不许写成撤链档：实得=%q", got2)
	}
	if !strings.Contains(got2, `"why":"post_restart_reconcile"`) {
		t.Fatalf("恢复那行的 why 必须是 post_restart_reconcile：实得=%q", got2)
	}
}
