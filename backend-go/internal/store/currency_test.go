// ============ 本文件职责中文说明 ============
// 多币种报价数据层（★ #75，2026-09-23，store/currency.go）单测，钉死六条口径：
// （★ 2026-09-22 用户决策后出厂态为关闭封存：A–D 验证「重开后」的完整链路，
// 用例内显式翻开关；F 钉死关闭态收敛行为，即「关闭」的可执行定义。）
//
//	A) 配置读取优先序 = 环境变量 > 数据库配置 > 代码默认（AGENTS.md §3），
//	   且未知币种/非法倍率在「读取即清洗」中被丢弃；
//	B) ConvertFromCNY fail-closed：CNY 恒可换算（倍率 1、不依赖配置）；
//	   未知币种 / 缺倍率一律 ok=false——调用方必须回落 CNY，绝不瞎换算；
//	C) 写入防呆：币种白名单 + 倍率 >0 且 <1000，非法值整批拒写（不留半套汇率）；
//	D) StampOrderCurrency 快照落库：CNY 单 rate 恒 1；外币单 money_cny 与 amount_money
//	   一致（人民币事实源不因报价币种而漂移）；脏入参回落 CNY；
//	E) EnsureCurrencyMigration 幂等：跑两次不报错；money_cny 存量回填只在
//	   money_cny=0 AND amount_money>0 时执行一次，回填过的行再跑不被改写。
//
// ★ 单测自钉 sqlite 方言（AGENTS.md §4）：config.Default() 读 DB_DRIVER 且副作用写全局
//
//	config.C，run_uat 的 PG 模式下会泄漏方言给同包内存 SQLite 用例，故显式钉住并
//	t.Cleanup 复原。环境变量用 t.Setenv（用例结束自动还原）。
//
// ==========================================
package store

import (
	"database/sql"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// pinQuoteSQLite 把全局方言钉成 sqlite 并在用例结束时复原（AGENTS.md §4 模板）。
// 与 renewal_grace_test.go 的 pinSQLiteDialect 同款——不直接复用是为了
// 两个测试文件分属不同工单（#74/#75），互不绑架删除自由。
func pinQuoteSQLite(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// newQuoteStore 内存 SQLite 全量迁移 Store（Store.New 已含 EnsureCurrencyMigration）。
func newQuoteStore(t *testing.T) *Store {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	s, err := New(raw)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return s
}

// openQuoteFeature 把报价开关翻到开放态并登记复原（★ 2026-09-22 决策后出厂态为关闭封存，
// A–D 各组验证的是"重开后"的完整链路行为，必须显式开开关跑，不能依赖默认值）。
func openQuoteFeature(t *testing.T) {
	t.Helper()
	t.Cleanup(SetQuoteFeatureOpen(true))
}

// F（★ 关闭态红线）：功能封存中整条链路收敛为 CNY——
// 残留库配置/env 不生效、外币写入与外币倍率一律拒收、白名单只露 CNY、Resolve 恒 CNY/1。
// 这条用例就是"关闭"二字本身的可执行定义；重开（quoteFeatureOpen=true）时本用例应整体删除。
func TestQuoteFeatureClosedSealsForeignCurrencies(t *testing.T) {
	pinQuoteSQLite(t)
	t.Cleanup(SetQuoteFeatureOpen(false)) // 显式钉关闭态：不依赖出厂值，防别人重开时本用例静默失真
	t.Setenv(EnvQuoteCurrency, "")
	t.Setenv(EnvFxRates, "")
	s := newQuoteStore(t)

	if QuoteFeatureOpen() {
		t.Fatal("出厂态应为关闭")
	}
	// 白名单只暴露 CNY（管理台报价区块据此隐藏）
	if got := SupportedQuoteCurrencies(); len(got) != 1 || got[0] != "CNY" {
		t.Fatalf("关闭态白名单应仅 CNY，got=%v", got)
	}
	// 库里有残留外币配置也不生效（直接 SetConfig 绕过校验写入，模拟历史脏数据/重开前遗留）
	if err := s.SetConfig(ConfigQuoteCurrency, "USD"); err != nil {
		t.Fatalf("预置残留配置失败: %v", err)
	}
	if err := s.SetConfig(ConfigFxRates, `{"USD":7.2}`); err != nil {
		t.Fatalf("预置残留倍率失败: %v", err)
	}
	cfg := s.QuoteCurrencyCfg()
	if cfg.Currency != "CNY" {
		t.Fatalf("关闭态读取应恒 CNY，got=%q", cfg.Currency)
	}
	if code, rate := cfg.Resolve(); code != "CNY" || rate != 1 {
		t.Fatalf("关闭态 Resolve 应恒 CNY/1，got=%q/%v", code, rate)
	}
	// env 也压不动（短路在读库之前，env 接管只对开放态有意义）
	t.Setenv(EnvQuoteCurrency, "EUR")
	t.Setenv(EnvFxRates, `{"EUR":7.8}`)
	if got := s.QuoteCurrencyCfg(); got.Currency != "CNY" {
		t.Fatalf("关闭态 env 不应撬动报价币种，got=%q", got.Currency)
	}
	// 写入口拒收：外币币种 / 外币倍率（CNY 与"不表态"仍放行，保存动作不该被无意义拒绝）
	if err := s.SetQuoteCurrency(1, "USD"); err == nil {
		t.Fatal("关闭态写外币币种应被拒绝")
	} else if msg := err.Error(); !strings.Contains(msg, "暂未开放") {
		t.Fatalf("拒绝话术应讲明功能未开放，got=%q", msg)
	}
	if err := s.SetQuoteCurrency(1, "CNY"); err != nil {
		t.Fatalf("关闭态写 CNY 应放行: %v", err)
	}
	if err := s.SetQuoteCurrency(1, ""); err != nil {
		t.Fatalf("关闭态空币种=不表态应放行: %v", err)
	}
	if err := s.SetFxRates(map[string]float64{"USD": 7.2}); err == nil {
		t.Fatal("关闭态写外币倍率应被拒绝")
	}
	if err := s.SetFxRates(map[string]float64{"CNY": 1}); err != nil {
		t.Fatalf("关闭态写 CNY 基准应放行: %v", err)
	}
	// 换算口 fail-closed：即使有人手里攥着币种码也换不出外币金额
	if _, _, ok := s.ConvertFromCNY(299.0, "USD"); ok {
		t.Fatal("关闭态 ConvertFromCNY 外币必须 ok=false（调用方回落 CNY）")
	}
}

// A：默认值 + 环境变量压过库配置。
func TestQuoteCurrencyCfgPrecedence(t *testing.T) {
	pinQuoteSQLite(t)
	openQuoteFeature(t)
	t.Setenv(EnvQuoteCurrency, "")
	t.Setenv(EnvFxRates, "")
	s := newQuoteStore(t)

	// 代码默认：CNY、倍率表恒含 CNY:1（不依赖任何配置）
	cfg := s.QuoteCurrencyCfg()
	if cfg.Currency != "CNY" || cfg.Rates["CNY"] != 1 {
		t.Fatalf("无配置时应默认 CNY/1，got=%+v", cfg)
	}
	// 库配置生效
	if err := s.SetQuoteCurrency(1, "USD"); err != nil {
		t.Fatalf("写报价币种失败: %v", err)
	}
	if err := s.SetFxRates(map[string]float64{"USD": 7.2}); err != nil {
		t.Fatalf("写汇率倍率失败: %v", err)
	}
	if got := s.QuoteCurrencyCfg(); got.Currency != "USD" || got.Rates["USD"] != 7.2 {
		t.Fatalf("库配置未生效，got=%+v", got)
	}
	// 环境变量压过库值（应急闸门：运维注入 env 即接管，不必先改管理台）
	t.Setenv(EnvQuoteCurrency, "EUR")
	t.Setenv(EnvFxRates, `{"EUR":7.8}`)
	got := s.QuoteCurrencyCfg()
	if got.Currency != "EUR" {
		t.Fatalf("env 应压过库配置，got=%q", got.Currency)
	}
	if _, ok := got.Rates["EUR"]; !ok {
		t.Fatalf("env 倍率应参与生效，got=%+v", got.Rates)
	}
	// env 倍率里的脏键被清洗（未知币种丢弃；非法倍率不覆盖已有库值），且 CNY 恒 1 不可污染
	t.Setenv(EnvFxRates, `{"XXX":9.9,"USD":0,"CNY":7.2}`)
	cleaned := s.QuoteCurrencyCfg()
	if _, ok := cleaned.Rates["XXX"]; ok {
		t.Fatal("未知币种倍率应被丢弃")
	}
	if cleaned.Rates["USD"] != 7.2 {
		t.Fatalf("env 非法倍率(0)不应覆盖已有库值 7.2，got=%v", cleaned.Rates["USD"])
	}
	if cleaned.Rates["CNY"] != 1 {
		t.Fatal("CNY 倍率恒为 1，配置不得污染")
	}
}

// B：ConvertFromCNY fail-closed——未知币种/缺倍率一律 ok=false，CNY 恒通。
func TestConvertFromCNYFailClosed(t *testing.T) {
	pinQuoteSQLite(t)
	openQuoteFeature(t)
	t.Setenv(EnvQuoteCurrency, "")
	t.Setenv(EnvFxRates, "")
	s := newQuoteStore(t)

	// CNY 不依赖任何配置（全新库零配置也必须能报价）
	amt, rate, ok := s.ConvertFromCNY(299.0, "CNY")
	if !ok || amt != 299.0 || rate != 1 {
		t.Fatalf("CNY 应原样换算: amt=%v rate=%v ok=%v", amt, rate, ok)
	}
	if _, _, ok := s.ConvertFromCNY(299.0, ""); !ok {
		t.Fatal("空币种应视同 CNY 放行")
	}
	// 未配汇率的合法币种：拒绝换算（绝不允许拿默认倍率凑数）
	if _, _, ok := s.ConvertFromCNY(299.0, "USD"); ok {
		t.Fatal("缺倍率币种 ConvertFromCNY 必须 ok=false，由调用方回落 CNY")
	}
	if _, _, ok := s.ConvertFromCNY(299.0, "BTC"); ok {
		t.Fatal("非白名单币种必须 ok=false")
	}
	// 配置后按「1 外币 = X 人民币」除法换算，保留 2 位
	if err := s.SetFxRates(map[string]float64{"USD": 7.2}); err != nil {
		t.Fatalf("写倍率失败: %v", err)
	}
	amt, rate, ok = s.ConvertFromCNY(720.0, "usd") // 大小写宽容
	if !ok || rate != 7.2 || amt != 100.0 {
		t.Fatalf("USD 换算错误: amt=%v rate=%v ok=%v", amt, rate, ok)
	}
	if amt, _, _ := s.ConvertFromCNY(299.0, "USD"); amt != 41.53 {
		t.Fatalf("299/7.2 应四舍五入到 41.53，got=%v", amt)
	}
}

// C：写入防呆——币种白名单 + 倍率区间，非法值整批拒写。
func TestQuoteCurrencyWriteValidation(t *testing.T) {
	pinQuoteSQLite(t)
	openQuoteFeature(t)
	t.Setenv(EnvQuoteCurrency, "")
	t.Setenv(EnvFxRates, "")
	s := newQuoteStore(t)

	if err := s.SetQuoteCurrency(1, "BTC"); err == nil {
		t.Fatal("非白名单币种应被拒绝")
	}
	if err := s.SetQuoteCurrency(1, ""); err != nil {
		t.Fatalf("空币种=不表态，不应报错: %v", err)
	}
	// 币种 + 该币种倍率缺配置时，生效解析必须回落 CNY（不报错、不瞎换算）
	if err := s.SetQuoteCurrency(1, "USD"); err != nil {
		t.Fatalf("写合法币种失败: %v", err)
	}
	if code, rate := s.QuoteCurrencyCfg().Resolve(); code != "CNY" || rate != 1 {
		t.Fatalf("缺倍率应回落 CNY/1，got=%q/%v", code, rate)
	}
	for _, bad := range []map[string]float64{
		{"USD": 0},             // 零倍率
		{"USD": -7.2},          // 负倍率
		{"USD": 1000},          // 触防呆上限
		{"BTC": 1.1},           // 非白名单币种
		{"CNY": 7.2},           // CNY 不许配（第二个事实源）
		{"USD": 7.2, "EUR": 0}, // 任一键非法 → 整批拒写
	} {
		if err := s.SetFxRates(bad); err == nil {
			t.Fatalf("非法倍率应被拒绝: %+v", bad)
		}
	}
	if err := s.SetFxRates(map[string]float64{"USD": 7.2, "EUR": 7.8}); err != nil {
		t.Fatalf("合法倍率写入失败: %v", err)
	}
	cfg := s.QuoteCurrencyCfg()
	if cfg.Currency != "USD" || cfg.Rates["USD"] != 7.2 || cfg.Rates["EUR"] != 7.8 {
		t.Fatalf("倍率读回不一致，got=%+v", cfg)
	}
	// 空表=清除自定义倍率（回到仅 CNY），币种仍按库值表态
	if err := s.SetFxRates(map[string]float64{}); err != nil {
		t.Fatalf("清空倍率失败: %v", err)
	}
	if _, ok := s.QuoteCurrencyCfg().Rates["USD"]; ok {
		t.Fatal("清空后 USD 倍率不应残留")
	}
}

// D：快照落库——CNY 单 rate=1；外币单 money_cny 与 amount_money 一致；脏入参回落。
func TestStampOrderCurrencySnapshot(t *testing.T) {
	pinQuoteSQLite(t)
	openQuoteFeature(t)
	t.Setenv(EnvQuoteCurrency, "")
	t.Setenv(EnvFxRates, "")
	s := newQuoteStore(t)

	readQuote := func(orderID int64) (string, float64, float64) {
		var cur string
		var rate, cny float64
		if err := db.QueryRow(s.DB(), db.CurrentDialect(),
			"SELECT currency, fx_rate, money_cny FROM orders WHERE id=?", orderID).
			Scan(&cur, &rate, &cny); err != nil {
			t.Fatalf("读订单报价快照失败: %v", err)
		}
		return cur, rate, cny
	}

	// CNY 单（默认态）：rate 恒 1，money_cny=amount_money 双写
	o1, err := s.CreateOrderChannel(1, 1000, 299.0, 0, "offline", "")
	if err != nil {
		t.Fatalf("建充值单失败: %v", err)
	}
	if err := s.StampOrderCurrency(o1.ID, "CNY", 1); err != nil {
		t.Fatalf("CNY 快照落库失败: %v", err)
	}
	if cur, rate, cny := readQuote(o1.ID); cur != "CNY" || rate != 1 || cny != 299.0 {
		t.Fatalf("CNY 单快照错误: cur=%q rate=%v cny=%v", cur, rate, cny)
	}

	// USD 单：currency=USD、fx_rate=配置值；money_cny 仍是人民币实收（事实源不漂移）
	if err := s.SetFxRates(map[string]float64{"USD": 7.2}); err != nil {
		t.Fatalf("写倍率失败: %v", err)
	}
	o2, err := s.CreateOrderChannel(1, 1000, 299.0, 0, "manual", "")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	if err := s.StampOrderCurrency(o2.ID, "USD", 7.2); err != nil {
		t.Fatalf("USD 快照落库失败: %v", err)
	}
	cur, rate, cny := readQuote(o2.ID)
	if cur != "USD" || rate != 7.2 {
		t.Fatalf("USD 单快照错误: cur=%q rate=%v", cur, rate)
	}
	if cny != o2.AmountMoney {
		t.Fatalf("money_cny 必须与 amount_money（人民币实收）一致: cny=%v amount=%v", cny, o2.AmountMoney)
	}

	// 脏入参回落：非白名单币种 / 非法倍率一律落 CNY/1（宁可没外币信息，不落脏值）
	o3, err := s.CreateOrderChannel(1, 1000, 0, 0, "offline", "")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	if err := s.StampOrderCurrency(o3.ID, "BTC", 0); err != nil {
		t.Fatalf("脏入参快照不应报错: %v", err)
	}
	if cur, rate, _ := readQuote(o3.ID); cur != "CNY" || rate != 1 {
		t.Fatalf("脏入参应回落 CNY/1，got=%q/%v", cur, rate)
	}
	// CNY 单即使传了外币倍率也恒写 1（防「currency=CNY, fx_rate=7.2」的自相矛盾行）
	if err := s.StampOrderCurrency(o3.ID, "CNY", 7.2); err != nil {
		t.Fatalf("重打快照失败: %v", err)
	}
	if _, rate, _ := readQuote(o3.ID); rate != 1 {
		t.Fatalf("CNY 单倍率必须恒为 1，got=%v", rate)
	}
}

// E：迁移幂等——跑两次不报错；存量回填只发生一次，amount_money=0 的行不动。
func TestEnsureCurrencyMigrationIdempotent(t *testing.T) {
	pinQuoteSQLite(t)
	s := newQuoteStore(t) // Store.New 迁移链已跑过一遍 EnsureCurrencyMigration

	// 跑第二遍：补列与回填都必须静默通过（幂等红线）
	s.EnsureCurrencyMigration()

	// 模拟「迁移前写入的存量行」：money_cny 归零，amount_money>0
	o, err := s.CreateOrderChannel(1, 1000, 199.0, 0, "offline", "")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	if _, err := db.Exec(s.DB(), db.CurrentDialect(),
		"UPDATE orders SET money_cny=0 WHERE id=?", o.ID); err != nil {
		t.Fatalf("模拟存量行失败: %v", err)
	}
	s.EnsureCurrencyMigration()
	var cny float64
	if err := db.QueryRow(s.DB(), db.CurrentDialect(),
		"SELECT money_cny FROM orders WHERE id=?", o.ID).Scan(&cny); err != nil {
		t.Fatalf("读回填结果失败: %v", err)
	}
	if cny != 199.0 {
		t.Fatalf("存量行应回填 money_cny=amount_money，got=%v", cny)
	}
	// 回填只一次：手工改写后再跑迁移，不得被覆盖回 amount_money
	if _, err := db.Exec(s.DB(), db.CurrentDialect(),
		"UPDATE orders SET money_cny=888 WHERE id=?", o.ID); err != nil {
		t.Fatalf("手工改写失败: %v", err)
	}
	s.EnsureCurrencyMigration()
	if err := db.QueryRow(s.DB(), db.CurrentDialect(),
		"SELECT money_cny FROM orders WHERE id=?", o.ID).Scan(&cny); err != nil || cny != 888 {
		t.Fatalf("已回填/已改写的行不应再被迁移触碰: cny=%v err=%v", cny, err)
	}
	// amount_money=0 的行（建单时尚未回填应收）不满足回填条件，保持 0
	o2, err := s.CreateOrderChannel(1, 1000, 0, 0, "offline", "")
	if err != nil {
		t.Fatalf("建 0 元单失败: %v", err)
	}
	s.EnsureCurrencyMigration()
	if err := db.QueryRow(s.DB(), db.CurrentDialect(),
		"SELECT money_cny FROM orders WHERE id=?", o2.ID).Scan(&cny); err != nil || cny != 0 {
		t.Fatalf("amount_money=0 的行不应被回填: cny=%v err=%v", cny, err)
	}
}
