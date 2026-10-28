// ============ ticket_estimate_f72_test.go 职责中文说明 ============
// F-72（2026-09-27 〇-W，用户批准「计费做固定项＋线性项两段式」）的专属闸门：
//   - estTokensFixed：固定开销项走 system_config（est_tokens_fixed_pro/fast），
//     缺省 3,000／1,200；**0 是合法值**（显式退回纯线性），非数字/负数/空才回退默认；
//   - 参数读取不抛异常：Store 为 nil（无库单测）也必须拿到保守默认，
//     否则「配置读不到」会变成「固定项丢失」＝短单低估静默复发；
//   - 量级取证：钉住本次取数的两个实测锚点（短单实烧 p90=3,289、长单线性项 800 万），
//     防止后人把固定项配成短单量级的 10 倍而无人察觉。
//
// 相关既有锁：ticket_balance_test.go（公式等值＋短单下限＋长单不被撑爆）、
// ticket_estimate_f41_test.go（88/89 生产基准）、scripts/uat/api_uat_txn.sh T40（线上文案等值）。
// ========================================
package api

import "testing"

// TestEstTokensFixedDefaults 键缺失时走代码默认：
// ★ 2026-10-28 计价批把固定项拆成「每单 F0」+「每语种 F1」——F0 pro=600 / fast=300、
// F1 pro=2400 / fast=900，**单语种合计仍是 3,000 / 1,200**（与拆分前逐字相同，短单下限没动）。
// 取数依据写死在断言里，改默认值必须连同这里一起改，逼改动的人重新交代证据。
func TestEstTokensFixedDefaults(t *testing.T) {
	s := f41StoreServer(t)
	if got := s.estTokensFixed("pro"); got != 600 {
		t.Fatalf("缺省 F0(pro) 应为 600, got %v", got)
	}
	if got := s.estTokensFixed("fast"); got != 300 {
		t.Fatalf("缺省 F0(fast) 应为 300, got %v", got)
	}
	if got := s.estTokensFixedLang("pro"); got != 2400 {
		t.Fatalf("缺省 F1(pro) 应为 2400, got %v", got)
	}
	if got := s.estTokensFixedLang("fast"); got != 900 {
		t.Fatalf("缺省 F1(fast) 应为 900, got %v", got)
	}
	// 单语种合计＝拆分前的固定项（pro 3,000＝短单实烧 p90=3,289 的整档；fast 1,200）。
	if got := s.estTokensFixed("pro") + s.estTokensFixedLang("pro"); got != 3000 {
		t.Fatalf("pro 单语种固定合计应为 3000, got %v", got)
	}
	if got := s.estTokensFixed("fast") + s.estTokensFixedLang("fast"); got != 1200 {
		t.Fatalf("fast 单语种固定合计应为 1200, got %v", got)
	}
	// mode 非 fast 的一切取值（含空串/未知）都必须落到更贵的 pro 档，不能落到 fast 或 0。
	for _, m := range []string{"", "pro", "PRO", "ultra"} {
		if got := s.estTokensFixed(m); got != 600 {
			t.Fatalf("mode=%q 应按 pro 档取 F0=600, got %v", m, got)
		}
		if got := s.estTokensFixedLang(m); got != 2400 {
			t.Fatalf("mode=%q 应按 pro 档取 F1=2400, got %v", m, got)
		}
	}
}

// TestEstTokensFixedConfigurable 插键即生效（与 K 同族，无需发版；⚠️ 只能 psql 写 system_config，管理台无此表单）。
func TestEstTokensFixedConfigurable(t *testing.T) {
	s := f41StoreServer(t)
	if err := s.Store.SetConfig(cfgEstFixedPro, "5000"); err != nil {
		t.Fatalf("SetConfig 失败: %v", err)
	}
	if err := s.Store.SetConfig(cfgEstFixedFast, "2000"); err != nil {
		t.Fatalf("SetConfig 失败: %v", err)
	}
	if got := s.estTokensFixed("pro"); got != 5000 {
		t.Fatalf("F(pro) 未跟随配置: %v", got)
	}
	if got := s.estTokensFixed("fast"); got != 2000 {
		t.Fatalf("F(fast) 未跟随配置: %v", got)
	}
	// 0 合法：那是「我知道我在退回纯线性」的显式决定，读取侧不得替它回退成默认。
	if err := s.Store.SetConfig(cfgEstFixedPro, "0"); err != nil {
		t.Fatalf("SetConfig(0) 失败: %v", err)
	}
	if got := s.estTokensFixed("pro"); got != 0 {
		t.Fatalf("F(pro) 显式配 0 应原样返回，got %v", got)
	}
}

// TestEstTokensFixedIllegalFallsBack 非数字/负数/空白一律回退默认。
// 负数尤其要拦：它会把预估**往回扣**（19 字单估成 3,040-1=3,039 甚至更低），
// 比「固定项丢失」更隐蔽；0 与负数的这条分界是本档与 K 档唯一的判据差异。
func TestEstTokensFixedIllegalFallsBack(t *testing.T) {
	s := f41StoreServer(t)
	for _, bad := range []string{"abc", "-1", "-3000", "  ", "1e999"} {
		if err := s.Store.SetConfig(cfgEstFixedFast, bad); err != nil {
			t.Fatalf("SetConfig(%q) 失败: %v", bad, err)
		}
		if got := s.estTokensFixed("fast"); got != 300 {
			t.Fatalf("非法配置 %q 应回退 F0=300, got %v", bad, got)
		}
	}
	// 每语种固定项同样是「非法回退、0 合法」这一档。
	for _, bad := range []string{"abc", "-1", "  ", "1e999"} {
		if err := s.Store.SetConfig(cfgEstFixedLangFast, bad); err != nil {
			t.Fatalf("SetConfig(%q) 失败: %v", bad, err)
		}
		if got := s.estTokensFixedLang("fast"); got != 900 {
			t.Fatalf("非法配置 %q 应回退 F1=900, got %v", bad, got)
		}
	}
	// 键彻底读不到（Store=nil）同样必须拿到默认：固定项丢失＝短单低估静默复发。
	var nilStoreSrv Server
	if got := nilStoreSrv.estTokensFixed("pro"); got != 600 {
		t.Fatalf("无 Store 时 F0(pro) 应为 600, got %v", got)
	}
	if got := nilStoreSrv.estTokensFixedLang("pro"); got != 2400 {
		t.Fatalf("无 Store 时 F1(pro) 应为 2400, got %v", got)
	}
}

// TestEstimateTicketTokensUsesBothTerms 两段式的「两段都要在」取证：
// 同一档位下，把固定项从 0 抬到默认，短单必须显著上移、1 万字 ×5 语长单必须几乎不动。
// 这条防的是「加了配置键但公式里没接上」——那种情况下 K 档等值锁全绿，短单却照旧低估。
func TestEstimateTicketTokensUsesBothTerms(t *testing.T) {
	linearOnly := estParams{kPro: 160, kFast: 60}
	full := estDefaultParams()

	shortGainPro := estimateTicketTokens(19, 1, "pro", full) - estimateTicketTokens(19, 1, "pro", linearOnly)
	if shortGainPro != 3000 {
		t.Fatalf("pro 短单的固定项增量应为 3000, got %d", shortGainPro)
	}
	shortGainFast := estimateTicketTokens(19, 1, "fast", full) - estimateTicketTokens(19, 1, "fast", linearOnly)
	if shortGainFast != 1200 {
		t.Fatalf("fast 短单的固定项增量应为 1200, got %d", shortGainFast)
	}
	// 锚点（〇-V 实测）：1 万字 ×5 语的线性项 = 10000×5×160 = 8,000,000，固定项只能占零头。
	longPure := estimateTicketTokens(10000, 5, "pro", linearOnly)
	if longPure != 8000000 {
		t.Fatalf("长单线性项锚点漂移（应为 800 万）: %d", longPure)
	}
	// ★ 2026-10-28 拆项后：5 语种的固定项是 F0 + 5×F1 = 600 + 5×2400 = 12,600（旧形态只加一次 3,000）。
	if delta := estimateTicketTokens(10000, 5, "pro", full) - longPure; delta != 12600 {
		t.Fatalf("长单固定项增量应为 12600（F0+5×F1，占 800 万的 0.158%%）, got %d", delta)
	}
}
