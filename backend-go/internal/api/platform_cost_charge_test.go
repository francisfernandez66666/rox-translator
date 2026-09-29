// ============================================================================
// platform_cost_charge_test.go · 职责说明
// 「系统任务与知识库 Embedding 改平台承担 ＋ 用量看板实扣/留痕分桶」的 api 层常设锁
// （★ 2026-09-29 〇-AD；store 层同批分桶锁见 internal/store/billing_platform_cost_test.go）。
//
// 钉死四条只有过 handler / 过计费咽喉才测得到的口径：
//
//	① 计费咽喉（ChargeUsageRealtime）：ctx 带 llm.WithPlatformCost 的用量**只落留痕行**
//	   （charge_kind='log'、task_type=原因标签），且**绝不写对外用量收集器**——
//	   收集器是 F-49①「出参 points_used ＝ 真扣积分」的唯一口径，把平台承担的量记进去，
//	   客户报文就会比自己实际掉的积分多一截，正是 F-49① 立约要消灭的形态；
//	② 留痕金额与实扣同尺子：同一模型同一 token 量，标记腿落的 quantity 必须等于未标记腿
//	   的对外计费量（否则平台承担账与客户账不可比，「垫了多少」就是个假数）；
//	③ 可见性收口：平台承担金额只出现在 /usage/cost（level-4 专属）的 platform 块里，
//	   租户管理员拿它必须 403；组织看板 /usage/org 从此不再出现「系统/后台任务」那一行
//	   ——现网 337,819 积分冒充客户消耗的形成路径就是这一行；
//	④ settle（欠费清零调整）单列且不并入平台承担合计——坏账混进"平台垫资"会把两者都读错。
//
// ⚠️ 本文件刻意**不**断言「未标记腿落了 charge 行」：实扣走 billing.DefaultSink 异步批量落库，
//
//	内存单测里全局 sink 未 InitGlobalSink（svc==nil 时 Record 安全丢弃），落库与扣减由
//	run_uat 的 PG 矩阵承担。这里只锁「标记腿不进扣费通道」这一侧。
//
// 方言：自钉 SQLite 内存库并显式钉死 config.C（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run PlatformCost08AD
// ============================================================================
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/config"
	"translator/internal/iam"
	"translator/internal/llm"
	"translator/internal/store"
	"translator/internal/tenant"
)

// platformCostProbe 平台承担批次的 api 层探针环境：
// 内存 SQLite ＋ 一个租户（含 tenant_admin 与一名成员）＋ 平台超管。
type platformCostProbe struct {
	s        *Server
	sqlDB    *sql.DB
	tid      int64
	memberID int64
	adminTok string // 该租户 tenant_admin（客户侧视角）
	superTok string // 平台超管（level 4）
}

// newPlatformCostProbe 装配探针环境（口径同 billing_uat_batchb_test.go 的 newQuotaSaveProbe）。
func newPlatformCostProbe(t *testing.T) *platformCostProbe {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := tenant.NewStore(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	tn, err := ts.Create("ac08x", "平台承担探针租", "", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := st.CreateUser(tn.ID, "ac08x-admin", auth.PasswordHash("pw123456"), "探针租户管理员", iam.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(tn.ID, "ac08x-user", auth.PasswordHash("pw123456"), "探针成员", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 平台超管（tid=0 ⇒ level 4 且 IsSuperAdmin）——/usage/cost 的唯一合法读者
	if err := st.EnsureAdmin(0, "ac08x-super", auth.PasswordHash("pw123456"), "探针超管", ""); err != nil {
		t.Fatal(err)
	}
	// token 必须由库内真实用户行签发（authUser 会回查库，uid/tid/令牌版本都要对得上）
	tokOf := func(u *store.User) string {
		tk, serr := auth.Sign(u, time.Hour)
		if serr != nil {
			t.Fatalf("签发 %s 的 JWT 失败: %v", u.Username, serr)
		}
		return tk
	}
	all, err := st.ListAllUsers()
	if err != nil {
		t.Fatal(err)
	}
	superTok := ""
	for _, u := range all {
		if auth.IsSuperAdmin(u) {
			superTok = tokOf(u)
		}
	}
	if superTok == "" {
		t.Fatal("探针超管没建出来（判据失去落点）")
	}
	return &platformCostProbe{
		s:        &Server{Store: st, Ten: ts, Cfg: config.C, Bill: billing.NewService(st)},
		sqlDB:    sqlDB,
		tid:      tn.ID,
		memberID: member.ID,
		adminTok: tokOf(admin),
		superTok: superTok,
	}
}

// pcBaseCtx 造一条「真人在租户内做翻译」的 context（计费咽喉读的全部维度）。
// 参数 p=探针、带 uid=归属用户（0＝后台任务无归属，正是现网 337,819 那一行的形态）。
func pcBaseCtx(p *platformCostProbe, uid int64) context.Context {
	ctx := tenant.WithTenant(context.Background(), p.tid)
	ctx = tenant.WithUser(ctx, uid)
	ctx = tenant.WithMode(ctx, "pro")
	return tenant.WithLang(ctx, "en")
}

// pcLedgerRow 一条 usage_ledger 造数（列口径与 store 侧同名测试一致）。
type pcLedgerRow struct {
	tid, uid, cost int64
	taskType       string
	chargeKind     string
}

// seed 直接落库造数（绕开异步 sink，让看板读法拿到确定盘面）。
func (p *platformCostProbe) seed(t *testing.T, rows []pcLedgerRow) {
	t.Helper()
	ts := time.Now().UTC().Format(time.RFC3339)
	for i, r := range rows {
		if _, err := p.sqlDB.Exec(
			`INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at)
			 VALUES (?,?,?,?,?,?,1,?, 'text','pro',?,?)`,
			r.tid, r.uid, r.taskType, "bigmodel", "m", r.cost, r.cost, r.chargeKind, ts); err != nil {
			t.Fatalf("第 %d 条造数失败: %v", i, err)
		}
	}
}

// logRows 读回当前全部留痕行（charge_kind='log'），用于咽喉判据。
func (p *platformCostProbe) logRows(t *testing.T) []pcLedgerRow {
	t.Helper()
	rows, err := p.sqlDB.Query(
		`SELECT tenant_id, user_id, task_type, cost, charge_kind FROM usage_ledger WHERE charge_kind='log' ORDER BY id`)
	if err != nil {
		t.Fatalf("读留痕行失败: %v", err)
	}
	defer rows.Close()
	var out []pcLedgerRow
	for rows.Next() {
		var r pcLedgerRow
		if err := rows.Scan(&r.tid, &r.uid, &r.taskType, &r.cost, &r.chargeKind); err != nil {
			t.Fatalf("扫描留痕行失败: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历留痕行失败: %v", err)
	}
	return out
}

// callJSON 直调 handler 并解析响应（带 Bearer，鉴权走真实链路）。
func callJSON(t *testing.T, s *Server, handler http.HandlerFunc, tok, query string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/billing/usage"+query, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应非法 JSON（%d）: %v / body=%s", rec.Code, err, rec.Body.String())
	}
	return rec.Code, body
}

// TestPlatformCost08AD_ChargeThroat ①＋②：标记腿只留痕、不进对外收集器，且金额与实扣同尺子。
func TestPlatformCost08AD_ChargeThroat(t *testing.T) {
	p := newPlatformCostProbe(t)

	// 未标记（客户自己的翻译）：应写收集器，量＝对外计费口径
	ucUser := &llm.UsageCollector{}
	if err := p.s.ChargeUsageRealtime(llm.WithUsageCollector(pcBaseCtx(p, p.memberID), ucUser),
		"bigmodel/glm-4", 60, 40); err != nil {
		t.Fatalf("未标记腿不该报错: %v", err)
	}
	billedUser := ucUser.BilledTotal()
	if billedUser <= 0 {
		t.Fatalf("未标记腿应写入对外计费口径（>0），实得 %d（说明 markup/策略链路没跑到，本判据失去落点）", billedUser)
	}

	// 标记腿（知识库 Embedding）：同一 token 量，必须只留痕、收集器恒 0
	ucEmbed := &llm.UsageCollector{}
	ctxEmbed := llm.WithUsageCollector(
		llm.WithPlatformCost(pcBaseCtx(p, p.memberID), llm.PlatformKBEmbed), ucEmbed)
	if err := p.s.ChargeUsageRealtime(ctxEmbed, "siliconflow/bge-m3", 60, 40); err != nil {
		t.Fatalf("标记腿不该报错: %v", err)
	}
	if got := ucEmbed.BilledTotal(); got != 0 {
		t.Fatalf("平台承担用量绝不能进对外收集器（F-49① 出参=实扣），实得 %d", got)
	}
	logs := p.logRows(t)
	if len(logs) != 1 {
		t.Fatalf("标记腿应且只应落 1 条留痕行，实得 %d 条: %+v", len(logs), logs)
	}
	if logs[0].taskType != llm.PlatformKBEmbed {
		t.Fatalf("留痕行必须按原因打标签（超管侧 by_reason 的唯一分组键），实得 task_type=%q", logs[0].taskType)
	}
	if logs[0].cost != billedUser {
		t.Fatalf("留痕金额必须等于同量实扣金额（否则平台承担账与客户账不可比），实得 %d vs %d", logs[0].cost, billedUser)
	}
	if logs[0].uid != p.memberID {
		t.Fatalf("留痕行仍要记发起用户（后台任务才有 uid=0），实得 %d", logs[0].uid)
	}
}

// TestPlatformCost08AD_CostBoardPlatformBlock ③＋④：/usage/cost 的 platform 块分桶正确，
// 且该接口只有超管拿得到（租户管理员 403、未登录 401）。
func TestPlatformCost08AD_CostBoardPlatformBlock(t *testing.T) {
	p := newPlatformCostProbe(t)
	p.seed(t, []pcLedgerRow{
		{tid: p.tid, uid: p.memberID, taskType: "translate", cost: 100, chargeKind: "charge"},
		{tid: p.tid, uid: 0, taskType: llm.PlatformKBEmbed, cost: 5000, chargeKind: "log"},
		{tid: p.tid, uid: 0, taskType: "translate", cost: 900, chargeKind: "log"}, // 历史推广期留痕
		{tid: p.tid, uid: 0, taskType: "settle_exhausted", cost: 70, chargeKind: "settle"},
	})
	code, body := callJSON(t, p.s, p.s.handleUsageCost, p.superTok, "/cost")
	if code != 200 {
		t.Fatalf("超管读成本看板应 200，实得 %d body=%v", code, body)
	}
	blk, ok := body["platform"].(map[string]interface{})
	if !ok {
		t.Fatalf("出参缺 platform 块（平台承担金额无处可读）: %v", body)
	}
	norm := func(q int64) float64 { return float64(p.s.Store.PointsFromTokens(q)) } // 出参经 JSON 恒为 float64
	wantTotal := norm(5900)                                                         // 两条 'log' 合计，'settle' 绝不并入
	if blk["total"] != wantTotal {
		t.Fatalf("platform.total 应=%v（Σ log），实得 %v", wantTotal, blk["total"])
	}
	if blk["policy_borne"] != norm(5000) {
		t.Fatalf("policy_borne 应只含政策该吃的 kb_embed=5000 折积分，实得 %v", blk["policy_borne"])
	}
	if blk["other_log"] != norm(900) {
		t.Fatalf("other_log 应为历史留痕 900 折积分，实得 %v", blk["other_log"])
	}
	if blk["settled"] != norm(70) {
		t.Fatalf("settled 应单列坏账 70 折积分，实得 %v", blk["settled"])
	}
	if blk["policy_borne"].(float64)+blk["other_log"].(float64) != blk["total"].(float64) {
		t.Fatal("两档合计必须等于 total（口径不闭合就是多套尺子）")
	}
	if r, ok := blk["by_reason"].(map[string]interface{}); !ok || r[llm.PlatformKBEmbed] != norm(5000) {
		t.Fatalf("by_reason 必须按标签给出 kb_embed 一档: %v", blk["by_reason"])
	}

	// 可见性收口：客户侧（租户管理员）与匿名都读不到这只接口。
	if code, body := callJSON(t, p.s, p.s.handleUsageCost, p.adminTok, "/cost"); code != 403 {
		t.Fatalf("租户管理员读 /usage/cost 必须 403（平台承担金额不对客户露出），实得 %d body=%v", code, body)
	}
	if code, _ := callJSON(t, p.s, p.s.handleUsageCost, "", "/cost"); code != 401 {
		t.Fatalf("未登录读 /usage/cost 必须 401，实得 %d", code)
	}
}

// TestPlatformCost08AD_OrgBoardHidesSystemRow ③：组织看板不再把留痕当客户消耗。
// 现网病灶的形态就在这里复现：uid=0 的 5,000 积分留痕行 ── 旧实现整段进「系统/后台任务」
// 一行并把 100 积分的真实消费淹掉；新实现该行根本不出现，total 只等于实扣。
func TestPlatformCost08AD_OrgBoardHidesSystemRow(t *testing.T) {
	p := newPlatformCostProbe(t)
	p.seed(t, []pcLedgerRow{
		{tid: p.tid, uid: p.memberID, taskType: "translate", cost: 100, chargeKind: "charge"},
		{tid: p.tid, uid: 0, taskType: llm.PlatformKBEmbed, cost: 5000, chargeKind: "log"},
	})
	code, body := callJSON(t, p.s, p.s.handleUsageOrg, p.adminTok, "/org")
	if code != 200 {
		t.Fatalf("租户管理员读组织看板应 200，实得 %d body=%v", code, body)
	}
	if got := body["total"]; got != float64(p.s.Store.PointsFromTokens(100)) {
		t.Fatalf("看板 total 必须只等于真实消费（留痕不进），实得 %v", got)
	}
	users, ok := body["users"].([]interface{})
	if !ok {
		t.Fatalf("users 出参形态异常: %T", body["users"])
	}
	for _, raw := range users {
		row, _ := raw.(map[string]interface{})
		if id, _ := row["id"].(float64); id == 0 {
			t.Fatalf("「系统/后台任务」行不该再出现在客户看板: %+v", row)
		}
		if c, _ := row["cost"].(float64); c == float64(p.s.Store.PointsFromTokens(5000)) {
			t.Fatalf("平台承担金额不许挂在任何客户行上: %+v", row)
		}
	}
}

// TestPlatformCost08AD2_SelfLedgeredSkipsSecondRow 〇-AD 补丁二·抑制位。
// KB 索引重建自己按租户字符占比分摊后走 LogUsageBatch 落账；EmbedBatch 又给 ctx 打了
// 平台承担标记 ⇒ 同一笔 token 会被记两次，超管看板的「平台承担」凭空翻倍。
// 判据成对写：抑制态 0 行 + 无抑制态 1 行，缺反向对照的话「抑制位恒真」和「钩子根本没跑」
// 都会表现成绿灯（AGENTS.md §一·5 的等值锁口径）。
func TestPlatformCost08AD2_SelfLedgeredSkipsSecondRow(t *testing.T) {
	p := newPlatformCostProbe(t)

	ctxSup := llm.WithSelfLedgeredUsage(llm.WithPlatformCost(pcBaseCtx(p, p.memberID), llm.PlatformKBEmbed))
	if err := p.s.ChargeUsageRealtime(ctxSup, "siliconflow/bge-m3", 60, 40); err != nil {
		t.Fatalf("抑制腿不该报错: %v", err)
	}
	if got := len(p.logRows(t)); got != 0 {
		t.Fatalf("自备台账的 ctx 不该再由钩子补留痕行（双写＝平台承担账翻倍），实得 %d 条", got)
	}
	if uc := llm.CollectorFrom(ctxSup); uc != nil && uc.BilledTotal() != 0 {
		t.Fatalf("抑制腿仍不许把平台成本送进对外收集器，实得 %d", uc.BilledTotal())
	}

	// 反向对照：同一 ctx 去掉抑制位必须落 1 行，否则上面那条 0 是「链路没跑」的假绿
	if err := p.s.ChargeUsageRealtime(
		llm.WithPlatformCost(pcBaseCtx(p, p.memberID), llm.PlatformKBEmbed), "siliconflow/bge-m3", 60, 40); err != nil {
		t.Fatalf("对照腿不该报错: %v", err)
	}
	rows := p.logRows(t)
	if len(rows) != 1 || rows[0].taskType != llm.PlatformKBEmbed {
		t.Fatalf("无抑制态必须且只落 1 条 kb_embed 留痕行（否则抑制位判据失去落点），实得 %+v", rows)
	}
}

// TestPlatformCost08AD2_LabelListsAgree 跨包名单等值锁：
// store 侧「额度腿排掉的标签名单」与 llm 侧「打标记时用的 reason 常量」必须是同一份，
// 任一侧改名/增删而另一侧没跟上，平台成本就会重新落回客户的额度里（且没有任何报错）。
// 这是典型的「改名式破坏」，只能靠这种同形锁拦，靠读代码发现不了。
func TestPlatformCost08AD2_LabelListsAgree(t *testing.T) {
	reasons := []string{llm.PlatformKBEmbed, llm.PlatformSystemTask, llm.PlatformPackScrape, llm.PlatformEvals}
	for _, r := range reasons {
		if !strings.Contains(store.PlatformTaskTypeExclPred, "'"+r+"'") {
			t.Fatalf("额度侧谓词漏掉平台标签 %q ⇒ 该标签的用量会继续吃客户日额/部门预算：%s",
				r, store.PlatformTaskTypeExclPred)
		}
	}
	// 反向锁：名单里不许藏额外标签（多塞一个＝把客户的真实用量当平台成本免掉）
	if n := strings.Count(store.PlatformTaskTypeExclPred, "'") / 2; n != len(reasons) {
		t.Fatalf("额度侧标签名单应恰为 %d 项（与 llm.Platform* 同数量），实得 %d 项：%s",
			len(reasons), n, store.PlatformTaskTypeExclPred)
	}
	// 尺子字面值等值锁（与 store 侧 realDebitPred 那条同族）
	if store.PlatformTaskTypeExclPred != "task_type NOT IN ('kb_embed','system_task','pack_scrape','evals')" {
		t.Fatalf("额度侧谓词字面值被改动：%q", store.PlatformTaskTypeExclPred)
	}
}
