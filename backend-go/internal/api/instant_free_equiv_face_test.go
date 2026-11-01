// ============ instant_free_equiv_face_test.go · 职责说明 ============
// ★ 2026-11-01（用户拍板「显示等值积分并注明免扣」）的**真接线面**锁。
//
// 引擎侧那四条（internal/engine/free_equiv_face_test.go）只证明「映射写对了」；
// 本文件证明的是「这一单真的走完了整条链」：
//
// handler → instantDecide 判成免费 → ctx 打免费标记 → 引擎调上游 →
//
//	llm.Client 回调 ChargeUsageRealtime（免费分支：留痕一行 + 写 freeBilled、**不写 billed**）→
//	HandleText 收尾按两只桶分派出参 → HTTP 200 的响应体。
//
// 三条判据各对一个盲区：
//
//	F1 免费腿出参三件（points_used=0 / points_equiv>0 / points_free=true）——
//	   出参是**这条链的最后一跳**，只测收集器或只测映射都会漏掉「标记没挂上」
//	   「handler 直接返回 res 之外另拼了一份 body」这类形态（AGENTS §三「判据级代替不了守卫级」）。
//	F2 等值与台账**逐字节同源**：points_equiv ＝ PointsFromTokens(∑ instant_free 留痕行的 quantity)。
//	   拿预估、拿真实 token、或另乘一次系数都会在这里红（§一·5④「预估与实扣同一把尺子」的第三维：等值也是账）。
//	F3 付费腿对照（两个目标语种 ⇒ 语种数是付费权益 ⇒ 同一条判据走另一支）：
//	   points_used>0 且两个新键**整条响应体里不存在**，并且**没有** instant_free 留痕行。
//	   没有这条对照，F1 在「无条件报免扣」的实现下照样绿。
//
// ⚠️ 本文件**不**断言「余额严格减少」——扣减走 `billing` 全局 sink，内存单测里未 InitGlobalSink，
// Record 被安全丢弃（同 platform_cost_charge_test.go / instant_free_charge_test.go 文件头那条 ⚠️）；
// 那一侧由 `run_uat.sh` 的 PG 矩阵 A7 两条分支承担。
//
// 方言口径（AGENTS §一·4）：自钉 sqlite 并恢复 config.C。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run 'TestInstantFreeEquiv'
// =============================================
package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/iam"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ifeFixture 一套「Store＋租户＋引擎＋假上游（带回 usage）」的对话面环境。
//
//	上游必须回 usage：token 数取不到 ⇒ 收集器两桶恒 0 ⇒ 三条判据全部空转（「没账」与「账是 0」同形）。
//	⚠️ 库句柄一律走 `s.Store.DB()`：另存一份 `*sql.DB` 在 `:memory:` 下可能是**另一个连接＝另一个空库**
//	（本文件首跑就以 "no such table: usage_ledger" 现形，那是夹具错不是产品缺陷）。
type ifeFixture struct {
	s    *Server
	tid  int64
	uid  int64
	tok  string
	hits *int64
}

// ifeUpstream 假上游：恒回同一句英文译文，并带 25k/10k 的真实用量。
func ifeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"The company focuses on smart hardware."}}],`+
			`"usage":{"prompt_tokens":25000,"completion_tokens":10000}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newIfeFixture(t *testing.T) *ifeFixture {
	t.Helper()
	pinSqliteDialect(t)
	srv := ifeUpstream(t)

	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "sk-test"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = cfg })

	// ★ 内存库必须用**具名 shared-cache**，不能用裸 `:memory:`：裸形式是"每个连接一个库"，
	//   store.New 的迁移跑在连接 A、后面那条对账 SELECT 可能落到连接 B ⇒ "no such table"（本批夹具首跑真踩，
	//   两条腿一红一绿、和产品一行代码的关系都没有）。
	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) +
		"?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	ts, err := tenant.NewStore(db)
	if err != nil {
		t.Fatalf("创建租户 Store 失败: %v", err)
	}
	co, err := ts.Create("ife1", "等值面探针租户", "", `{}`)
	if err != nil {
		t.Fatalf("创建租户失败: %v", err)
	}
	// 付费腿对照要给余额：语种数>1 就进付费闸门，没余额会被 402 挡在引擎之前，
	// 那条对照就测不到「出参分派」而是测「余额墙」。发放一份订阅额度即可过 CheckBalance。
	pkg, err := st.CreatePackage(&store.Package{
		Code: "ife_pack", Name: "等值面探针套餐", PType: store.PackagePaid,
		Sentences: 100000, PriceMoney: 199, DurationDays: 30, Enabled: 1,
	})
	if err != nil {
		t.Fatalf("创建套餐失败: %v", err)
	}
	if _, err := st.GrantPackageSentences(co.ID, pkg); err != nil {
		t.Fatalf("发放套餐额度失败: %v", err)
	}
	u, err := st.CreateUser(co.ID, "ife_member", auth.PasswordHash("pw123456"), "探针成员", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发 JWT 失败: %v", err)
	}

	eng := engine.NewEngine(cfg, nil, nil, nil)
	eng.St = st
	s := &Server{Store: st, Ten: ts, Cfg: cfg, metrics: newMetrics(), Engine: eng, Bill: billing.NewService(st)}
	// ★ 这条接线就是 cmd/server/main.go 里那句 `eng.LLM.OnUsage = srv.ChargeUsageRealtime`：
	//   不挂它，本文件测的是「引擎出参」而不是「计费现场→出参」，F2 那条台账等值判据根本不会有人写行。
	//   hits 同时是「上游真被打过」的证据（同 failingChatHarness 的判据取向：零命中＝用例空转）。
	var hits int64
	eng.LLM.OnUsage = func(ctx context.Context, model string, prompt, completion int64) error {
		atomic.AddInt64(&hits, 1)
		return s.ChargeUsageRealtime(ctx, model, prompt, completion)
	}
	return &ifeFixture{s: s, tid: co.ID, uid: u.ID, tok: tok, hits: &hits}
}

// ifeText／ifeFakeIP 夹具的两个共同前提：同一句话、同一个出口 IP。
// 抽出来是因为**判定腿与请求腿必须是同一次输入**——判定用一句、请求发另一句，
// 那条"前提自证"就成了摆设（同 AGENTS §一·24「夹具与判据同源」）。
const (
	ifeText   = "本公司专注于智能硬件的研发与设计"
	ifeFakeIP = "203.0.113.77" // TEST-NET-3：免费档按 IP 分桶，给一个稳定的假出口
)

// ifeRoute 前提自证：按**与 ifePostChat 完全相同的参数**直接问一次免费判定。
// 为什么必须有这一条：出参三件（points_used=0／points_equiv>0／points_free=true）在
// 「这一单根本没被判成免费」时会整段变成"扣费腿的判据"，而红字指不到真凶。
// 判定腿先自证，出参腿的红才只可能来自「标记挂了却没透到出参」那一族。
func (f *ifeFixture) ifeRoute(t *testing.T, langs []string) instantRoute {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/translate", nil)
	r.Header.Set("X-Forwarded-For", ifeFakeIP)
	// 没有鉴权中间件的那条腿：租户上下文必须夹具自己钉上，
	// 否则 ChargeUsageRealtime 的 `tid<=0 直接放行` 会让整条链"看着跑完了、其实一分钱一度都没记"。
	r = r.WithContext(tenant.WithTenant(r.Context(), f.tid))
	return f.s.instantDecide(r, f.tid, f.uid, instantInput{
		Kind:      "text",
		LangCount: len(langs),
		EstPoints: f.s.instantEstimatePoints(ifeText, len(langs), "fast", "", "zh"),
	})
}

// ifePostChat 直调 handleChat（/api/translate 非流式：出参＝HandleText 结果本体，最省解析）。
// 参数 langs: 目标语种数组——1 个＝免费档，2 个＝语种数是付费权益（F3 的分派开关）。
func (f *ifeFixture) ifePostChat(t *testing.T, langs []string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"message": ifeText,
		"options": map[string]interface{}{"target_langs": langs, "mode": "fast", "lang": "zh"},
	})
	r := httptest.NewRequest(http.MethodPost, "/api/translate", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+f.tok)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-For", ifeFakeIP)               // 免费档按 IP 分桶：给一个稳定的假出口
	r = r.WithContext(tenant.WithTenant(r.Context(), f.tid)) // 同上：无中间件⇒夹具钉租户
	w := httptest.NewRecorder()
	f.s.handleChat(w, r)
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析 /api/translate 出参失败: %v，响应体：%s", err, w.Body.String())
	}
	return w, out
}

// ifeFreeRows 读 instant_free 留痕行的 quantity 合计与条数（F2 的台账那一侧）。
func (f *ifeFixture) ifeFreeRows(t *testing.T) (int64, int) {
	t.Helper()
	var sum sql.NullInt64
	var n int
	err := f.s.Store.DB().QueryRow(`SELECT COALESCE(SUM(quantity),0), COUNT(1) FROM usage_ledger WHERE task_type='instant_free'`).Scan(&sum, &n)
	if err != nil {
		t.Fatalf("读 instant_free 留痕行失败: %v", err)
	}
	return sum.Int64, n
}

// F1＋F2 免费腿：出参三件齐、且等值与台账那一行逐字节同源；同时零扣费流水。
func TestInstantFreeEquivFreeLeg(t *testing.T) {
	f := newIfeFixture(t)
	// 前提自证：这一单必须先被判成免费，否则下面三条判据测的是扣费腿。
	if rt := f.ifeRoute(t, []string{"en"}); !rt.Free {
		t.Fatalf("夹具前提失效：单语种这一跑没被判成免费（reason=%q，档位＝ %+v）"+
			"⇒ 下面的出参三件全部失去落点。改夹具（补额度/换语种数/开总闸），**不许**放宽判据。",
			rt.Reason, f.s.instantEffective(f.tid))
	}
	w, out := f.ifePostChat(t, []string{"en"})

	if w.Code != http.StatusOK {
		t.Fatalf("免费腿不该被闸门挡掉（期望 200，实得 %d）：\n%s", w.Code, w.Body.String())
	}
	if e, ok := out["error"].(string); ok && e != "" {
		t.Fatalf("免费腿这一单失败了（error=%s），后面的等值判据全部失去落点：\n%s", e, w.Body.String())
	}
	if *f.hits == 0 {
		t.Fatal("上游一次都没被打：这一单没走完整条链，本用例是空转（判据会在离线态假绿）")
	}

	// ① 三件出参
	if v, ok := out["points_used"]; !ok {
		t.Fatalf("免费腿仍必须出 points_used（值＝实扣 0），不许整条摘掉键——前端按它判「有没有这一行」：\n%s", w.Body.String())
	} else if iv, _ := v.(float64); iv != 0 {
		// 两种红分开报（同 §三 那条「分不清是『没扣』还是『没记』就没法定位」）：
		// rows=0 ⇒ 免费标记压根没挂到扣费现场（handler／ctx 传递那一层）；
		// rows>0 却 points_used≠0 ⇒ 现场记了等值、出参却没读它（映射那一层）。
		dsum, drows := f.ifeFreeRows(t)
		var dkinds string
		_ = f.s.Store.DB().QueryRow(`SELECT COALESCE(GROUP_CONCAT(DISTINCT charge_kind), '-') FROM usage_ledger`).Scan(&dkinds)
		t.Fatalf("免费腿 points_used=%v≠0（实扣必须是 0）。诊断：instant_free 留痕 rows=%d ∑quantity=%d，全表 charge_kind=%s"+
			"　⇒ rows=0 是标记没挂上；rows>0 是出参没读等值桶。响应体：%s", iv, drows, dsum, dkinds, w.Body.String())
	}
	free, _ := out["points_free"].(bool)
	if !free {
		t.Fatalf("免费腿 points_free 必须为 true（只出数字不点名免扣＝把等值读成扣费）：\n%s", w.Body.String())
	}
	eq, ok := out["points_equiv"].(float64)
	if !ok || eq <= 0 {
		t.Fatalf("免费腿 points_equiv 必须>0，实得 %v：客户看不到这一单值多少", out["points_equiv"])
	}

	// ② 等值与台账同源（∑ instant_free.quantity 折积分 ＝ 出参那一格）
	sum, rows := f.ifeFreeRows(t)
	if rows == 0 {
		t.Fatal("F2：免费腿没有留下 instant_free 留痕行——出参的等值是从哪来的？（无源读数）")
	}
	want := f.s.Store.PointsFromTokens(sum)
	if want <= 0 {
		t.Fatalf("夹具前提失效：PointsFromTokens(∑quantity=%d)=%d，折不出积分 ⇒ F2 的等值判据无从判定", sum, want)
	}
	if int64(eq) != want {
		t.Fatalf("F2：出参等值 %v ≠ 台账留痕折算 %d（∑quantity=%d，task_type=instant_free）⇒ 两只尺子又各数各的账", eq, want, sum)
	}

	// ③ 钱那一侧必须干净：没有实扣留痕（charge_kind 非 log）、没有扣费流水
	var debits int
	if err := f.s.Store.DB().QueryRow(`SELECT COUNT(1) FROM usage_ledger WHERE charge_kind IN ('','charge')`).Scan(&debits); err == nil && debits != 0 {
		t.Fatalf("F1：免费腿却产生 %d 行实扣留痕 ⇒ 与 points_used=0 相互矛盾，必有一边在撒谎", debits)
	}
	var bills int
	if err := f.s.Store.DB().QueryRow(`SELECT COUNT(1) FROM billing_ledger`).Scan(&bills); err == nil && bills != 0 {
		t.Fatalf("F1：免费腿却产生 %d 条扣费流水（白扣）", bills)
	}
}

// F3 付费腿对照：语种数>1 走扣费那一支 ⇒ points_used>0、两个新键整条不存在、零免费留痕。
// 没有这条，F1 在「无条件报免扣」的实现下照样绿（同 §三「反向对照必须真走到另一支」）。
func TestInstantFreeEquivPaidLegControl(t *testing.T) {
	f := newIfeFixture(t)
	// 对照腿同样要前提自证：这一跑必须**不**被判成免费，否则"没出现等值键"只是因为压根没走分派。
	if rt := f.ifeRoute(t, []string{"en", "ja"}); rt.Free {
		t.Fatal("夹具前提失效：双语种这一跑被判成了免费 ⇒ 对照腿与 F1 同支，本用例失去判别力（免费档的语种上限不是 1 了，改夹具并同步这条前提）")
	}
	w, out := f.ifePostChat(t, []string{"en", "ja"})

	if w.Code != http.StatusOK {
		t.Fatalf("付费腿（已发放套餐额度）不该被闸门挡掉（期望 200，实得 %d）：\n%s", w.Code, w.Body.String())
	}
	if e, ok := out["error"].(string); ok && e != "" {
		t.Fatalf("付费腿这一单失败了（error=%s）：对照腿必须真走到扣费那一支，否则测的是错误分支：\n%s", e, w.Body.String())
	}
	if *f.hits == 0 {
		t.Fatal("上游一次都没被打：付费腿对照失去落点")
	}
	used, _ := out["points_used"].(float64)
	if used <= 0 {
		t.Fatalf("F3：付费腿 points_used=%v 必须>0（实扣口径）", out["points_used"])
	}
	for _, bad := range []string{"points_equiv", "points_free"} {
		if strings.Contains(w.Body.String(), bad) {
			t.Fatalf("F3：付费腿的响应体里出现了 %s ⇒ 免费那一支的判据漏到了扣费面：%s", bad, w.Body.String())
		}
	}
	if _, rows := f.ifeFreeRows(t); rows != 0 {
		t.Fatalf("F3：付费腿却留了 %d 行 instant_free 留痕（同一笔用量出两行，超管成本账翻倍）", rows)
	}
}
