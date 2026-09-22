// ============ 本文件职责中文说明 ============
// 多币种报价（★ #75，2026-09-23）的数据访问层：报价币种/汇率倍率配置读写、
// CNY→本币换算、orders 表报价快照列（currency / fx_rate / money_cny）的幂等迁移与落库。
//
// ★ 现状（2026-09-22 用户决策）：功能关闭封存。收单能力仅微信/支付宝（CNY）+ 币安钱包
// USDT 两条，十二币种报价暂无业务落点——quoteFeatureOpen=false 时本文件所有对外行为
// 收敛为 CNY（读恒 CNY、外币写入拒收、白名单只露 CNY），快照列与 orders 迁移照常保留。
// 重开方式：翻转 quoteFeatureOpen 并补一条 T54 口径复核，其余代码零改动。
//
// 为什么只做「报价展示」而不做「外币实扣」（设计口径，勿扩大范围）：
//
//	微信/支付宝对本系统的收款主体永远是人民币（gateway_sdk.go 下单报文的
//	amount.currency 恒为 CNY），未签约外币收单前，任何"以外币实际扣款"的路径
//	都是伪造资金流。故本切片仅解决两件事：
//	① 海外客户在定价页/收银台看到本币报价（price_display）；
//	② 下单时把「当时用的币种与汇率」快照进订单（currency + fx_rate + money_cny），
//	   留作展示回显与审计对账，绝不参与资金判定。
//	fail-closed：换算只认白名单币种 + 已配置倍率，缺任何一项一律 ok=false，
//	调用方必须回落 CNY，绝不拿臆测汇率瞎换算。
//	（签约外币收单后，可在此域基础上升级为真实外币下单，历史快照列即审计基线。）
//
// 语义红线（存量对账/退款全依赖，一字不可改）：
//   - orders.amount_money 保持历史含义 = 人民币实收金额；
//   - money_cny 是显式化的"本单人民币金额事实源快照"，新单一律双写（=amount_money），
//     存量行迁移时一次性回填（仅 money_cny=0 AND amount_money>0，天然幂等）；
//   - currency + fx_rate 只为展示与审计，任何资金逻辑不得读取它们做判定。
//
// 配置口径（AGENTS.md §3）：环境变量 > 数据库配置 > 代码默认。
//   - system_config 键 quote_currency（默认 CNY）与 fx_rates（JSON：{"USD":7.2,...}，
//     基准 CNY，1 外币 = X 人民币；CNY 恒为 1，不依赖配置）；
//   - 应急环境变量 QUOTE_CURRENCY / FX_RATES 与支付渠道凭据同思路：
//     库配置误改时运维注入 env 重启即可压过，不必先进管理台救火。
//   - 报价币种是平台/租户运营级配置（不是用户级自选），落 system_config 单例 KV，
//     与 paych_* / usdt_* 同类；若将来要按租户分别配币种，再升级为带 tenant_id 的表。
//
// 日志走 internal/observability（AGENTS.md §2）；SQL 全部经 db.Exec/QueryRow +
// CurrentDialect 包装，SQLite 方言为真源（AGENTS.md §4）。
// store 冻结规则（AGENTS.md §1）禁止把新方法追加进 billing.go，故独立成域文件；
// 落库封装 StampOrderCurrency* 供调用点在 CreatePackageOrder/CreateOrderChannel 之后
// 按需补写快照，不改冻结文件的任何函数签名。
// ==========================================
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"sort"
	"strings"

	"translator/internal/db"
	"translator/internal/observability"
)

// 报价配置的 system_config 键与环境变量应急键。
const (
	ConfigQuoteCurrency = "quote_currency" // 报价币种码（默认 CNY）
	ConfigFxRates       = "fx_rates"       // 汇率倍率 JSON：{"USD":7.2,...}，1 外币 = X CNY

	EnvQuoteCurrency = "QUOTE_CURRENCY" // 应急覆盖：报价币种（优先于库配置）
	EnvFxRates       = "FX_RATES"       // 应急覆盖：汇率倍率 JSON（优先于库配置）

	QuoteCurrencyCNY = "CNY" // 计价与结算事实源币种，恒可报价（倍率恒为 1）
)

// quoteFeatureOpen 多币种报价总开关（★ 2026-09-22 用户决策：关闭封存，"后续有能力了再打开"）。
// 当前收单能力只有微信/支付宝（人民币）与币安钱包 USDT，没有外币收单资质，十二币种展示
// 报价没有业务落点，整条链路按关闭态封存：
//   - 读取口 QuoteCurrencyCfg 恒回 CNY（库/env 里残留的外币配置不生效）；
//   - 写入口校验拒绝一切非 CNY 币种与外币倍率；
//   - 白名单只暴露 CNY（管理台据 supported_currencies 无外币项自行隐藏报价区块）。
//
// USDT 海外收款是另一条既有渠道（usdt_* 配置，固定倍率+链上到账），不受本开关影响。
// 真签下外币收单后把此值改 true 即完整重开（orders 快照列/前端渲染分支/十语种词典都原样保留）。
var quoteFeatureOpen = false

// QuoteFeatureOpen 只读暴露开关状态（API 出参 feature_open 标注用）。
func QuoteFeatureOpen() bool { return quoteFeatureOpen }

// SetQuoteFeatureOpen 仅供同仓测试切换开放态并返还复原函数（生产代码不得调用；
// 真实重开走改 quoteFeatureOpen 出厂值 + 发版，不提供运行时后门）。
func SetQuoteFeatureOpen(open bool) (restore func()) {
	old := quoteFeatureOpen
	quoteFeatureOpen = open
	return func() { quoteFeatureOpen = old }
}

// maxFxRateMultiplier 汇率倍率防呆上限（>0 且 <1000 才接受）：
// 现实中不存在 1 外币 = 1000 人民币 的主流币种，越界必是误配（多打几个零），
// 参考 clampGraceDays 的思路——在写入口拦，而不是让脏数据流到展示与快照里。
const maxFxRateMultiplier = 1000.0

// supportedQuoteCurrencies 报价币种白名单（ISO 4217 常用 12 个，含结算币种 CNY）。
// 为什么硬编码而不是做成可配：本切片只做展示报价，币种扩容随发版加一行即可；
// 做成可配反而引入「往 rates 里塞任意键」的注入面。
var supportedQuoteCurrencies = []string{
	"CNY", "USD", "EUR", "JPY", "GBP", "HKD",
	"KRW", "SGD", "AUD", "CAD", "CHF", "THB",
}

// SupportedQuoteCurrencies 返回白名单副本（管理台渲染下拉框用）。
// 关闭态只暴露 CNY——前端报价区块以「无外币可选项」为条件整体隐藏。
func SupportedQuoteCurrencies() []string {
	if !quoteFeatureOpen {
		return []string{QuoteCurrencyCNY}
	}
	out := make([]string, len(supportedQuoteCurrencies))
	copy(out, supportedQuoteCurrencies)
	return out
}

// normalizeQuoteCode 币种码归一：去空白 + 转大写（" usd "→"USD"）。
func normalizeQuoteCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// isSupportedQuoteCode 币种码是否在白名单内（入参须已 normalizeQuoteCode）。
func isSupportedQuoteCode(code string) bool {
	for _, c := range supportedQuoteCurrencies {
		if c == code {
			return true
		}
	}
	return false
}

// fxRateSane 倍率防呆：必须 >0 且 <1000（NaN 与负数、零一并拒绝）。
func fxRateSane(rate float64) bool {
	return rate > 0 && rate < maxFxRateMultiplier
}

// QuoteCurrencyConfig 报价配置快照（值均为"生效值"：环境变量 > 库配置 > 代码默认）。
type QuoteCurrencyConfig struct {
	Currency string             // 生效报价币种（白名单内；脏配置已回落 CNY）
	Rates    map[string]float64 // 币种码 → 倍率（1 外币 = X CNY）；必含 "CNY":1
}

// Resolve 给出「可直接用于展示换算」的币种与倍率：
// 配置的币种若缺倍率（或倍率非法）→ 回落 CNY/1。
// WHY 回落而不是报错：报价是展示层能力，缺汇率时客户看到人民币价
// 远好于看到报错——fail-closed 的"关"是关掉外币展示，不是关掉生意。
func (c QuoteCurrencyConfig) Resolve() (code string, rate float64) {
	if c.Currency != "" && c.Currency != QuoteCurrencyCNY {
		if r, ok := c.Rates[c.Currency]; ok && fxRateSane(r) {
			return c.Currency, r
		}
		return QuoteCurrencyCNY, 1
	}
	return QuoteCurrencyCNY, 1
}

// QuoteCurrencyCfg 读取当前生效的报价配置（env > DB > 代码默认）。
// 读取即清洗：未知币种码、非法倍率一律丢弃并告警，保证调用方拿到的
// Currency 必在白名单内、Rates 必健全，不需要各自再做防呆。
// ★ 关闭态短路：报价功能封存中（quoteFeatureOpen=false）时恒回 CNY 默认值，
// 库/env 里残留的外币配置一律不生效——重开只需翻转开关，不必先清历史配置。
func (s *Store) QuoteCurrencyCfg() QuoteCurrencyConfig {
	if !quoteFeatureOpen {
		return QuoteCurrencyConfig{Currency: QuoteCurrencyCNY, Rates: map[string]float64{QuoteCurrencyCNY: 1}}
	}
	out := QuoteCurrencyConfig{Currency: QuoteCurrencyCNY, Rates: map[string]float64{QuoteCurrencyCNY: 1}}
	// ① 数据库配置（管理台所见即所存）
	if v, err := s.GetConfig(ConfigQuoteCurrency); err == nil {
		applyQuoteCurrencyCode(&out, v, "db")
	}
	if v, err := s.GetConfig(ConfigFxRates); err == nil && strings.TrimSpace(v) != "" {
		mergeFxRates(&out, v, "db")
	}
	// ② 环境变量压过库值（应急/灰度闸门，与 paych_* 同一优先序口径）
	if v := strings.TrimSpace(os.Getenv(EnvQuoteCurrency)); v != "" {
		applyQuoteCurrencyCode(&out, v, "env")
	}
	if v := strings.TrimSpace(os.Getenv(EnvFxRates)); v != "" {
		mergeFxRates(&out, v, "env")
	}
	// CNY 恒为 1：即使配置里塞了 "CNY":7.2 也不认（结算事实源不允许被配置污染）
	out.Rates[QuoteCurrencyCNY] = 1
	return out
}

// applyQuoteCurrencyCode 写入币种码（非白名单则保留旧值并告警，不炸接口）。
func applyQuoteCurrencyCode(cfg *QuoteCurrencyConfig, raw, src string) {
	code := normalizeQuoteCode(raw)
	if code == "" {
		return
	}
	if !isSupportedQuoteCode(code) {
		observability.Warn(context.Background(), "报价币种不在白名单（按当前值处理）",
			"src", src, "value", code)
		return
	}
	cfg.Currency = code
}

// mergeFxRates 解析倍率 JSON 并合并进 cfg.Rates（脏键逐条丢弃，好键照常生效）。
func mergeFxRates(cfg *QuoteCurrencyConfig, raw, src string) {
	var m map[string]float64
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		observability.Warn(context.Background(), "汇率倍率配置 JSON 解析失败（忽略该来源）",
			"src", src, "err", err.Error())
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 告警顺序稳定可复现（map 遍历随机）
	for _, k := range keys {
		code := normalizeQuoteCode(k)
		if !isSupportedQuoteCode(code) || !fxRateSane(m[k]) {
			observability.Warn(context.Background(), "汇率倍率配置项非法（已丢弃）",
				"src", src, "currency", code, "rate", m[k])
			continue
		}
		cfg.Rates[code] = m[k]
	}
}

// ValidateQuoteCurrency 校验币种码（""=不表态，视为维持现状；否则必须白名单命中）。
// 导出给 api 层做「校验先行、整批落库在后」，数据层 setter 也复用它兜底。
// 关闭态下非 CNY 币种直接拒绝（拒绝话术讲清"为什么"，不是笼统的白名单报错）。
func ValidateQuoteCurrency(code string) error {
	code = normalizeQuoteCode(code)
	if code == "" {
		return nil
	}
	if !quoteFeatureOpen && code != QuoteCurrencyCNY {
		return &errTxt{"多币种报价暂未开放：当前收单渠道仅支持人民币（微信/支付宝）与 USDT 链上收款，" +
			"外币报价将在具备外币收单能力后开启"}
	}
	if !isSupportedQuoteCode(code) {
		return &errTxt{"不支持的报价币种：" + code + "（仅支持 " + strings.Join(supportedQuoteCurrencies, "/") + "）"}
	}
	return nil
}

// ValidateFxRates 校验倍率表：币种必须在白名单、倍率必须 >0 且 <1000。
// CNY 键允许缺席（恒为 1）；若显式给了非 1 的 CNY 值按非法拒绝——
// 与其"读的时候悄悄纠正"，不如"写的时候就报错"，避免留下第二个事实源。
// 关闭态下任何外币倍率都拒收（没币种能用到它，收了就是脏配置）。
func ValidateFxRates(rates map[string]float64) error {
	keys := make([]string, 0, len(rates))
	for k := range rates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		code := normalizeQuoteCode(k)
		if !quoteFeatureOpen && code != QuoteCurrencyCNY {
			return &errTxt{"多币种报价暂未开放：不支持配置外币汇率（" + code + "）"}
		}
		if !isSupportedQuoteCode(code) {
			return &errTxt{"不支持的报价币种：" + code + "（仅支持 " + strings.Join(supportedQuoteCurrencies, "/") + "）"}
		}
		if code == QuoteCurrencyCNY {
			if rates[k] != 1 {
				return &errTxt{"CNY 为计价基准币种，倍率恒为 1，无需也不可配置"}
			}
			continue
		}
		if !fxRateSane(rates[k]) {
			return &errTxt{"币种 " + code + " 的汇率倍率非法（须大于 0 且小于 1000，含义：1 " + code + " = X 人民币）"}
		}
	}
	return nil
}

// SetQuoteCurrency 持久化报价币种（system_config 单键）。
// 参数：tid=操作者所属租户上下文（当前仅签名预留——报价币种是平台级单配置，
// 按租户分别配币种时再升级为租户维度）；code=币种码（大小写宽容，白名单校验）。
func (s *Store) SetQuoteCurrency(tid int64, code string) error {
	if err := ValidateQuoteCurrency(code); err != nil {
		return err
	}
	code = normalizeQuoteCode(code)
	if code == "" {
		return nil // ""=不表态：与支付渠道配置"未填不覆盖"同一语义
	}
	_ = tid // 见上方签名说明：平台级单配置，租户维度为将来预留
	return s.SetConfig(ConfigQuoteCurrency, code)
}

// SetFxRates 持久化汇率倍率表（整体覆盖写：传空表=清除全部自定义倍率，回到仅 CNY）。
// 校验先行：任一键非法即整批拒写（不留"半套汇率"，与 pay_channels_save 同口径）。
func (s *Store) SetFxRates(rates map[string]float64) error {
	if err := ValidateFxRates(rates); err != nil {
		return err
	}
	clean := make(map[string]float64, len(rates))
	for k, v := range rates {
		code := normalizeQuoteCode(k)
		if code == QuoteCurrencyCNY {
			continue // CNY 恒为 1，不落库（不落库才不会有第二个 CNY 事实源）
		}
		clean[code] = v
	}
	if len(clean) == 0 {
		_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM system_config WHERE key=?", ConfigFxRates)
		return err
	}
	packed, err := json.Marshal(clean)
	if err != nil {
		return err
	}
	return s.SetConfig(ConfigFxRates, string(packed))
}

// ConvertFromCNY 把人民币金额换算为报价币种展示金额（★ 只做展示换算，不做资金判定）。
// 参数：cny=人民币金额（事实源）；code=报价币种。
// 返回 (本币金额[2 位], 倍率[1 外币=?CNY], ok)：
//   - CNY（或空币种）→ 原样 (cny, 1, true)，不依赖任何配置；
//   - 未知币种 / 缺倍率 / 倍率非法 → (0, 0, false)，调用方必须回落 CNY 展示，
//     绝不允许拿默认倍率凑数（fail-closed，见文件头）。
func (s *Store) ConvertFromCNY(cny float64, code string) (float64, float64, bool) {
	code = normalizeQuoteCode(code)
	if code == "" || code == QuoteCurrencyCNY {
		return cny, 1, true
	}
	cfg := s.QuoteCurrencyCfg()
	rate, ok := cfg.Rates[code]
	if !ok || !fxRateSane(rate) {
		return 0, 0, false
	}
	// 倍率含义是「1 外币 = rate 人民币」，CNY → 外币即除法；分位四舍五入与订单金额口径一致
	return roundMoney(cny / rate), rate, true
}

// EnsureCurrencyMigration 多币种报价迁移入口（Store.New 迁移链调用，幂等）：
// ① orders 补 currency / fx_rate / money_cny 三列（db.EnsureColumns，禁裸 ALTER）；
// ② 存量行回填 money_cny = amount_money（仅 money_cny=0 AND amount_money>0 的行——
//
//	新列默认 0 即"未回填"哨兵值，回填后再跑匹配 0 行，天然幂等，只回刷一次）。
//
// 存量行币种语义：三列默认值即 'CNY'/1/0→回填后=amount_money，
// 历史订单全部是人民币成交，无需逐行改写 currency/fx_rate。
func (s *Store) EnsureCurrencyMigration() {
	d := db.CurrentDialect()
	if err := db.EnsureColumns(s.db, d, "orders", map[string]string{
		"currency":  "TEXT NOT NULL DEFAULT 'CNY'",
		"fx_rate":   "REAL NOT NULL DEFAULT 1",
		"money_cny": "REAL NOT NULL DEFAULT 0",
	}); err != nil {
		observability.Error(context.Background(), "orders 报价快照列补列失败", "err", err.Error())
		return // 列都没补上，回填无从谈起
	}
	res, err := db.Exec(s.db, d,
		"UPDATE orders SET money_cny=amount_money WHERE money_cny=0 AND amount_money>0")
	if err != nil {
		observability.Error(context.Background(), "orders.money_cny 存量回填失败", "err", err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		observability.Info(context.Background(), "orders.money_cny 存量回填完成", "rows", int(n))
	}
}

// normalizeOrderQuote 快照入参清洗：非白名单币种或非法倍率一律回落 CNY/1。
// WHY 在数据层再兜一次：调用方（api）拿的是 Resolve() 结果本应干净，
// 但快照列直接参与审计读法，宁可静默回落 CNY（=无外币信息可伪造）也不落脏值。
func normalizeOrderQuote(currency string, rate float64) (string, float64) {
	code := normalizeQuoteCode(currency)
	if !isSupportedQuoteCode(code) || !fxRateSane(rate) {
		return QuoteCurrencyCNY, 1
	}
	if code == QuoteCurrencyCNY {
		return QuoteCurrencyCNY, 1 // CNY 单倍率恒写 1，不信任入参
	}
	return code, rate
}

// StampOrderCurrency 为订单落报价快照（currency + fx_rate + money_cny 双写）。
// 参数：orderID=订单 ID；currency=下单时报价币种；rate=当时倍率（1 外币=?CNY，CNY 恒 1）。
// money_cny 取行内 amount_money 现值（SQL 内自读，不给调用方传两份钱的机会）——
// 因此必须在金额最终确定后调用（建单 → 回填应收 → 券折让，都完成后才是"最终金额"）。
func (s *Store) StampOrderCurrency(orderID int64, currency string, rate float64) error {
	code, r := normalizeOrderQuote(currency, rate)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE orders SET currency=?, fx_rate=?, money_cny=amount_money WHERE id=?", code, r, orderID)
	return err
}

// StampOrderCurrencyTx 事务版快照落库（供已在事务里的调用点复用，语义同 StampOrderCurrency）。
// 参数：tx=开启中的事务句柄；连接与事务同构（db.Exec 接受 Execer），方言照常改写占位符。
func (s *Store) StampOrderCurrencyTx(tx *sql.Tx, orderID int64, currency string, rate float64) error {
	code, r := normalizeOrderQuote(currency, rate)
	_, err := db.Exec(tx, db.CurrentDialect(),
		"UPDATE orders SET currency=?, fx_rate=?, money_cny=amount_money WHERE id=?", code, r, orderID)
	return err
}
