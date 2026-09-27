// ============ 本文件职责中文说明 ============
// ★ F-78（2026-09-28 〇-X）积分汇率 1:300 → 1:400 的「存量等值补发」一次性迁移。
//
// 要解决的问题：积分↔token 汇率（points_tokens_rate）是对外售卖单位的计价基准，
// 改档等于给所有**已按旧汇率入账的 token 余额**重新定价。若只改常量就上线，
// 老客户账上 90,000 token 会从 300 积分变成 225 积分——积分凭空白掉 25%，属于资损侧事故。
// 本迁移在启动链里把这类「按积分铸出来的额度」整体 ×4/3，使每一笔存量的积分面值分毫不差，
// 然后才把汇率与充值尺子翻到新档。
//
// 判据（唯一口径，可逐列复核）：**只看写入路径，不看列名。**
//   - 该列的值当初是由 TokensFromPoints()（积分 × 汇率）铸出来的 ⇒ 它是「积分面值的 token 表示」，
//     随汇率缩放：余额账本、双桶台账、订单/支付流水的 amount_tokens、任务定义奖励、组织 token_limit、
//     system_config 里三枚积分面值键折出的 token、租户日限额的 token 腿。
//   - 该列的值是「真实消耗/成本事实」（usage_ledger / usage_daily、tickets.tokens_billed、
//     user_task_claims 历史领取、referral_rewards 与 kb_rewards 奖励流水、invoices）⇒ **一律不动**。
//     这些数字记的是「已经发生的事」，把它们 ×4/3 等于伪造历史成本；
//   - 以「句/字符」为单位的列（packages.points/sentences、max_daily_chars、orgs.sentence_limit）
//     与汇率无关 ⇒ 不动。日限额的 token 腿（permissions.max_daily_tokens）历史上被
//     api/billing_api.go 用 max_daily_chars 直接塞过值（两种口径并存），故按「是否恰等于字符值」
//     逐租户判定：相等＝字符派生值，跳过；不等＝当初按积分钉的墙，随汇率缩放。
//
// 取整规则（两套，各自对应自己的真值源，不可混用）：
//
//	① 额度桶：纯 ×新/旧 四舍五入，即 (t*new + old/2)/old；
//	② system_config 三枚积分面值键：先按旧汇率回读成「取整后的积分面值」，再 ×新汇率重铸——
//	   这样老库补发后的值与全新库 EnsureBillingDefaults 的种子值**逐字节相等**
//	   （1,000/1,000/1,667 积分 → 400000/400000/666800），不会出现「新库老库两套数」。
//
// 钱的口径为什么不会被动：裸充值应收走 TokensToFen(amount_tokens)，而尺子
// price_fen_per_million_tokens 按 3/4 反向缩放（33222→24917），token ×4/3 与尺子 ×3/4 精确抵消，
// 所以「3,000 积分 = ¥299」「升级差额 ComputeUpgradeCredit」「orderMoneyBackfill 防重复回填」
// 三处金额口径全部保持原值（这正是必须成对改两个旋钮的原因——历史上 F-12 只改一头就偏了 10%）。
//
// 幂等与并发：整段单一 IMMEDIATE 事务；先以 system_config 主键 INSERT OR IGNORE 抢「补发凭证」，
// RowsAffected=0 即认定已有实例（或更早的进程）完成过，直接放弃。凭证一旦落下，
// 即便日后有人把 points_tokens_rate 手工改回 300，本迁移也不会二次缩放（防重复补发＝防超发）。
// 前置判定只看库里 points_tokens_rate 的字面值是否为 "300"：新库出厂即 400 → 跳过且不占凭证；
// 键缺失/值异常一律不猜（宁可漏补也不误补），交运维核对。
//
// SQL 全部走 db.Exec/db.Query + CurrentDialect（SQLite 为真源，AGENTS.md §4）；
// 日志走 internal/observability（§2）；store 冻结规则（§1）禁止往 billing.go 追加方法，故独立成域文件。
// ==========================================
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/observability"
)

// F-78 补发涉及的 system_config 键（键名集中在此，避免与读取侧字符串各写一份而漂移）。
const (
	// ConfigPointsRateRebase 补发凭证/审计位：value 先落 "claimed"（认领），提交前改写为摘要 JSON。
	ConfigPointsRateRebase = "points_rate_rebase"
	// keyPointsRate 积分↔token 汇率键（PointsTokensRate 的读键）。
	keyPointsRate = "points_tokens_rate"
	// keyRulerFen 裸充值尺子键（PriceFenPerMillionTokens 的读键）。
	keyRulerFen = "price_fen_per_million_tokens"
)

// pointsMintedConfigKeys 「值＝积分面值折出的 token」的配置键，补发时走上面第②套取整规则。
// 刻意不在此列的：free_trial_days / invite_extend_days（天数）、
// low_balance_alert_tokens 与 kb_upload_reward_*（token 单位的运营阈值，不是积分面值）、
// estimate_tokens_per_sentence 与 est_tokens_*（句/段↔token 口径，F-78 未动）。
var pointsMintedConfigKeys = []string{
	"free_trial_tokens",
	"invite_reward_tokens",
	"inviter_paid_reward_tokens",
}

// rebaseValueHolders 需要等值缩放（第①套取整规则）的「表.列」清单。
// 表列名均为本文件固定字面量（不接受外部输入拼接），SQL 里只有倍率参与参数绑定。
var rebaseValueHolders = []struct{ table, column string }{
	{"balance_accounts", "balance"}, // 租户余额账本（剩余 token）
	{"quota_grants", "total"},       // 双桶台账：发放总量
	{"quota_grants", `"left"`},      // 双桶台账：剩余量（left 在 PG 是保留字，两方言均需引号）
	{"orders", "amount_tokens"},     // 订单计量：全部状态一起缩——ComputeUpgradeCredit 以它作分母，必须与 grants 同标；pending 单稍后结算时也拿得到原积分面值
	{"payments", "amount_tokens"},   // 支付流水：与订单同标，防对账两侧口径错位
	{"user_tasks", "reward_tokens"}, // 任务定义奖励（未来发放的积分面值；已领取流水不动）
	{"orgs", "token_limit"},         // 组织额度上限（积分享受档）
}

// PointsRateRebase 把旧汇率（1:300）库里「按积分铸出的额度」等值补发到新汇率（1:400）口径，
// 并翻写汇率、重锚充值尺子（幂等，一次性；非旧档库直接跳过）。
//
// 调用位置：store.New() 里紧接 EnsureBillingDefaults 之后、TaskRewardMigrate /
// orderMoneyBackfill / PackageOrderTokenBackfill 之前——这三处都会按当前汇率折算或回填，
// 必须让它们看到补发完成后的库，否则会把旧口径的数再折一遍（双重放大）。
func (s *Store) PointsRateRebase() {
	d := db.CurrentDialect()

	// ---------- 前置判定：库里是不是旧档？（直读原始行，不走任何缓存/默认值兜底）----------
	if strings.TrimSpace(s.readConfigLiteral(d, keyPointsRate)) != strconv.FormatInt(legacyPointsTokensRate, 10) {
		return
	}

	tx, err := s.db.Begin() // DSN _txlock=immediate ⇒ BEGIN IMMEDIATE（PG 同义：尽早取写锁）
	if err != nil {
		observability.Error(context.Background(), "F-78 积分汇率补发：开启事务失败（本轮跳过）", "err", err.Error())
		return
	}
	defer tx.Rollback()

	// ---------- 认领补发凭证（多实例/重启安全：谁插进去谁做，插不进去说明已做过）----------
	res, err := db.Exec(tx, d,
		"INSERT OR IGNORE INTO system_config (key,value,updated_at) VALUES (?,?,?)",
		ConfigPointsRateRebase, "claimed", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		observability.Error(context.Background(), "F-78 积分汇率补发：落凭证失败（整体回滚）", "err", err.Error())
		return
	}
	if n, perr := res.RowsAffected(); perr != nil || n != 1 {
		// 读不到行数按「未认领」处理（宁可停手等人工核对，也不冒二次放大的风险）
		return
	}

	// ---------- 记录补发前读数（逐租户余额快照，供摘要与事后对账复核）----------
	before := s.balanceSnapshotTx(tx, d)

	// ---------- ① 额度桶等值缩放 ----------
	var scaledRows int64
	for _, h := range rebaseValueHolders {
		n, serr := scaleColumnTx(tx, d, h.table, h.column)
		if serr != nil {
			observability.Error(context.Background(), "F-78 积分汇率补发：额度列缩放失败（整体回滚）",
				"table", h.table, "column", h.column, "err", serr.Error())
			return
		}
		scaledRows += n
	}

	// ---------- ② 配置键：先回读积分面值，再按新汇率重铸（与全新库种子逐字节一致）----------
	for _, k := range pointsMintedConfigKeys {
		if cerr := rebasePointsMintedConfig(tx, d, k); cerr != nil {
			observability.Error(context.Background(), "F-78 积分汇率补发：配置键重锚失败（整体回滚）",
				"key", k, "err", cerr.Error())
			return
		}
	}

	// ---------- ③ 租户日限额的 token 腿（字符派生值跳过）----------
	tenantsTouched, terr := rebaseTenantDailyTokens(tx, d)
	if terr != nil {
		observability.Error(context.Background(), "F-78 积分汇率补发：日限额重锚失败（整体回滚）", "err", terr.Error())
		return
	}

	// ---------- ④ 翻汇率 + 尺子反向缩放（钱口径不动）----------
	// ⚠ 必须用事务内读（readConfigTxLiteral）：此刻 IMMEDIATE 事务已持有写锁，
	// 走 s.db 的第二条连接读同一张表会被阻塞（SQLite）或读到另一份快照（PG），
	// 一旦读失败又落到"反推默认值"分支，就会把尺子按错误方向改写（单测实测：33222→18687 而非 24917）。
	// 缺行时**不补写**：库里没有显式值，PriceFenPerMillionTokens 的代码默认即新档 24917，本来就是对的。
	rulerFrom, rulerTo := int64(0), int64(0)
	if raw := strings.TrimSpace(readConfigTxLiteral(tx, d, keyRulerFen)); raw != "" {
		if v, perr := strconv.ParseInt(raw, 10, 64); perr == nil && v > 0 {
			rulerFrom = v
			rulerTo = (v*legacyPointsTokensRate + DefaultPointsTokensRate/2) / DefaultPointsTokensRate // ×旧/新＝×3/4，与 token 的 ×4/3 精确抵消
			if err := upsertConfigTx(tx, d, keyRulerFen, strconv.FormatInt(rulerTo, 10)); err != nil {
				observability.Error(context.Background(), "F-78 积分汇率补发：尺子重锚失败（整体回滚）", "err", err.Error())
				return
			}
		}
	}
	if err := upsertConfigTx(tx, d, keyPointsRate, strconv.FormatInt(DefaultPointsTokensRate, 10)); err != nil {
		observability.Error(context.Background(), "F-78 积分汇率补发：汇率落库失败（整体回滚）", "err", err.Error())
		return
	}

	// ---------- ⑤ 凭证改写成审计摘要（前后余额 + 影响面 + 钱口径声明）----------
	var moves []balanceMove
	after := s.balanceSnapshotTx(tx, d)
	afterByTid := make(map[int64]int64, len(after))
	for _, b := range after {
		afterByTid[b.tid] = b.balance
	}
	for _, b := range before {
		moves = append(moves, balanceMove{b.tid, b.balance, afterByTid[b.tid]}) // 按 tid 配对，不靠行序下标
	}
	summary, _ := json.Marshal(map[string]interface{}{
		"from_rate":           legacyPointsTokensRate,
		"to_rate":             DefaultPointsTokensRate,
		"scaled_rows":         scaledRows,
		"ruler_fen_from":      rulerFrom,
		"ruler_fen_to":        rulerTo,
		"config_keys":         pointsMintedConfigKeys,
		"tenants_perm":        tenantsTouched,
		"balances":            moves,
		"done_at":             time.Now().UTC().Format(time.RFC3339),
		"money_invariant":     "token×4/3 与尺子×3/4 精确抵消：3,000 积分仍折 ¥299，升级折算与订单应收回填零漂移",
		"untouched_principle": "真实消耗/历史流水（usage_ledger、tickets.tokens_billed、任务与邀请领取、奖励流水、账单）不缩放：它们是成本事实，缩放＝伪造历史",
	})
	if err := upsertConfigTx(tx, d, ConfigPointsRateRebase, string(summary)); err != nil {
		observability.Error(context.Background(), "F-78 积分汇率补发：审计摘要落库失败（整体回滚）", "err", err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		observability.Error(context.Background(), "F-78 积分汇率补发：提交失败（整体回滚）", "err", err.Error())
		return
	}
	observability.Info(context.Background(), "F-78 积分汇率存量等值补发完成",
		"from_rate", legacyPointsTokensRate, "to_rate", DefaultPointsTokensRate,
		"scaled_rows", scaledRows, "ruler_from", rulerFrom, "ruler_to", rulerTo,
		"tenants_perm_touched", tenantsTouched)

	// 启动期补发，此刻 billing 影子缓存尚未有流量灌入；仍逐个租户通知失效，
	// 防「运维在同一进程里先查过余额」这种少见的旧值残留（未接线时 notify 自身空转）。
	for _, b := range before {
		notifyTenantBalanceChanged(b.tid)
	}
}

// scaleColumnTx 把某表某列的全部非零值按 ×新/旧 四舍五入等值放大，返回受影响行数。
// 参数：tx=事务句柄；d=方言；table/column=目标表列（"left" 这类保留字自带引号传入）。
// 取整：(t*new + old/2)/old——两方言的整数除法都向零截断，加 old/2 即非负值四舍五入。
// 生产无负余额（2026-09-28 已核两库）；若出现负值会朝正方向取整（对平台有利的一侧），不特殊处理。
func scaleColumnTx(tx *sql.Tx, d db.Dialect, table, column string) (int64, error) {
	sqlText := "UPDATE " + table + " SET " + column + " = (" + column + "*? + ?) / ? WHERE " + column + " <> 0"
	res, err := db.Exec(tx, d, sqlText,
		DefaultPointsTokensRate, legacyPointsTokensRate/2, legacyPointsTokensRate)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// rebasePointsMintedConfig 把「值＝积分面值折出的 token」的配置键按第②套规则重锚：
// 旧 token → 旧汇率回读成积分面值（四舍五入）→ ×新汇率重铸 token。
// 参数：tx=事务；d=方言；key=配置键。键缺失或值非正整数即跳过：本迁移之前
// EnsureBillingDefaults 已补过种子，此处读不到只可能是人工删键，不属于补发射程。
func rebasePointsMintedConfig(tx *sql.Tx, d db.Dialect, key string) error {
	oldTokens, perr := strconv.ParseInt(strings.TrimSpace(readConfigTxLiteral(tx, d, key)), 10, 64)
	if perr != nil || oldTokens <= 0 {
		return nil
	}
	pts := (oldTokens + legacyPointsTokensRate/2) / legacyPointsTokensRate // 旧汇率下的积分面值
	want := pts * DefaultPointsTokensRate                                  // 新汇率重铸
	if want == oldTokens {
		return nil // 无需变动（理论不发生；留此分支让重复调用的语义保持干净）
	}
	_, err := db.Exec(tx, d, "UPDATE system_config SET value=?, updated_at=? WHERE key=?",
		strconv.FormatInt(want, 10), time.Now().UTC().Format(time.RFC3339), key)
	return err
}

// rebaseTenantDailyTokens 逐租户重锚 permissions.max_daily_tokens（日限额的 token 腿），返回被改写租户数。
// 跳过条件：无该键 / 值非正（未配限额）/ 值恰等于 max_daily_chars（字符上限被塞进 token 字段的历史口径，
// 与积分汇率无关，缩放等于误抬字符墙）。非法 JSON 行跳过——读取侧自有兜底，一个租户解析失败不该阻断其余补发。
func rebaseTenantDailyTokens(tx *sql.Tx, d db.Dialect) (int64, error) {
	rows, err := db.Query(tx, d, `SELECT id, COALESCE(permissions,'') FROM tenants`)
	if err != nil {
		return 0, err
	}
	type move struct {
		tid int64
		to  int64
	}
	var todo []move
	for rows.Next() {
		var tid int64
		var raw string
		if serr := rows.Scan(&tid, &raw); serr != nil {
			rows.Close()
			return 0, serr
		}
		var m map[string]json.Number
		dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
		dec.UseNumber() // 数字解成 json.Number：Int64() 只对整数面值成功，带小数的脏值会被判非法并跳过
		if dec.Decode(&m) != nil {
			continue // 非法 JSON：读取侧自有兜底，一个租户解析失败不该阻断其余补发
		}
		tokN, hasTok := m["max_daily_tokens"]
		if !hasTok {
			continue
		}
		tok, perr := tokN.Int64()
		if perr != nil || tok <= 0 {
			continue
		}
		chars := int64(0)
		if cN, ok := m["max_daily_chars"]; ok {
			chars, _ = cN.Int64()
		}
		if tok == chars {
			continue // 字符派生值：不动
		}
		todo = append(todo, move{tid, (tok*DefaultPointsTokensRate + legacyPointsTokensRate/2) / legacyPointsTokensRate})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, mv := range todo {
		patch, _ := json.Marshal(map[string]int64{"max_daily_tokens": mv.to})
		if _, err := db.Exec(tx, d, "UPDATE tenants SET "+db.JSONPatchSet(d, "permissions")+", updated_at=? WHERE id=?",
			string(patch), time.Now().UTC().Format(time.RFC3339), mv.tid); err != nil {
			return int64(len(todo)), err
		}
	}
	return int64(len(todo)), nil
}

// balanceSnap 单租户余额快照行（补发摘要用）。
type balanceSnap struct {
	tid     int64
	balance int64
}

// balanceMove 补发摘要里的单租户余额位移（前后值均为内部 token 口径，JSON 键对外可读）。
type balanceMove struct {
	TenantID int64 `json:"tenant_id"`
	Before   int64 `json:"before"`
	After    int64 `json:"after"`
}

// balanceSnapshotTx 取事务内逐租户余额快照（按租户 ID 升序，保证摘要可逐字节比对）。
func (s *Store) balanceSnapshotTx(tx *sql.Tx, d db.Dialect) []balanceSnap {
	out := []balanceSnap{}
	rows, err := db.Query(tx, d, "SELECT tenant_id, balance FROM balance_accounts ORDER BY tenant_id")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var tid, bal int64
		if rows.Scan(&tid, &bal) == nil {
			out = append(out, balanceSnap{tid, bal})
		}
	}
	return out
}

// readConfigLiteral 直读 system_config 原始值（与 GetConfig 同 SQL，独立命名只为点明
// 「补发判定看的是数据库里那一行」，不接受任何默认值兜底）。键不存在回空串。
func (s *Store) readConfigLiteral(d db.Dialect, key string) string {
	var v string
	if err := db.QueryRow(s.db, d, "SELECT value FROM system_config WHERE key=?", key).Scan(&v); err != nil {
		return ""
	}
	return v
}

// readConfigTxLiteral 事务内直读配置原始值（补发读旧值必须看见自己事务里的写）。
func readConfigTxLiteral(tx *sql.Tx, d db.Dialect, key string) string {
	var v string
	if err := db.QueryRow(tx, d, "SELECT value FROM system_config WHERE key=?", key).Scan(&v); err != nil {
		return ""
	}
	return v
}

// upsertConfigTx 事务内写配置（与 SetConfig 同一 ON CONFLICT 语义，供补发链在事务里落键值）。
func upsertConfigTx(tx *sql.Tx, d db.Dialect, key, value string) error {
	_, err := db.Exec(tx, d,
		"INSERT INTO system_config (key, value, updated_at) VALUES (?,?,?) "+
			"ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at",
		key, value, time.Now().UTC().Format(time.RFC3339))
	return err
}
