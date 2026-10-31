// ============ instant_free_points_test.go · 职责说明 ============
// 组2a／组4 的计数与留痕锁（★ 2026-10-31 补齐 K3／K5／N2／W24）：
//
//	K3  free_wall 只在**拒绝**路径 +1：成功路径跑完必须为 0（成功也加 ⇒ 助手 hit_wall 恒真）。
//	K5  计数器库故障必须 fail-open（按 0 放行＋出声），不许把一次读写失败打成"客户被拦死"。
//	N2  免费标记唯一写点／扣费唯一读点：源码级按函数全集数，两处各只许一次。
//	W24 免费即时每笔 usage_ledger：charge_kind='log'、task_type='instant_free'、quantity=真实 billed。
//	    行为腿查库；另配一条源码腿钉住"传进去的是 billed 而不是 0"。
//
// =============================================
package api

import (
	"database/sql"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/store"
)

// newFreeCounterServer 建一个只用内存 SQLite 的 Server（本文件全部是计数器/留痕，不碰网络与 LLM）。
func newFreeCounterServer(t *testing.T) (*Server, *store.Store, *sql.DB) {
	t.Helper()
	// 方言自钉（AGENTS §一·4）：config.Default() 会读 DB_DRIVER 并写全局，必须还原。
	oldCfg := config.C
	config.C = config.Default()
	config.C.DatabaseDriver = "sqlite"
	t.Cleanup(func() { config.C = oldCfg })
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return &Server{Store: st, Cfg: config.C}, st, db
}

// K3：成功路径不写 free_wall；只有"用户积分到量"那一枪才 +1。
func TestInstantFree_K3FreeWallOnlyOnReject(t *testing.T) {
	s, st, _ := newFreeCounterServer(t)
	const uid, tid = int64(42), int64(1)
	req := httptest.NewRequest("POST", "/api/chat", nil)
	req.RemoteAddr = "203.0.113.5:51234"

	// ① 成功一枪：免费、free_wall 必须 0。
	route := s.instantDecide(req, tid, uid, instantInput{Kind: "text", LangCount: 1})
	if !route.Free {
		t.Fatalf("K3：默认档应判免费，实得 %+v", route)
	}
	if stt, err := st.RateDayLoad(store.RateScopeFreeWall, "42"); err != nil || stt.Used != 0 {
		t.Fatalf("K3：成功路径不许写 free_wall（used=%d err=%v）", stt.Used, err)
	}

	// ② 把账号日用量顶到默认上限 300，再打一枪 ⇒ 必须因 quota_user 被拒，且 free_wall=1。
	if _, err := st.RateDayAdd(store.RateScopeFreePointUser, "42", 301); err != nil {
		t.Fatalf("K3：预置账号用量失败：%v", err)
	}
	route = s.instantDecide(req, tid, uid, instantInput{Kind: "text", LangCount: 1})
	if route.Free || route.Reason != instantReasonQuotaUser {
		t.Fatalf("K3：到量应转付费（quota_user），实得 %+v", route)
	}
	if stt, err := st.RateDayLoad(store.RateScopeFreeWall, "42"); err != nil || stt.Used != 1 {
		t.Fatalf("K3：拒绝那一枪应给 free_wall +1（used=%d err=%v）", stt.Used, err)
	}
}

// K5：rate_limits 读写报错 ⇒ 按未用放行（fail-open），绝不拦请求。
func TestInstantFree_K5CounterFailureFailsOpen(t *testing.T) {
	s, _, db := newFreeCounterServer(t)
	// 关掉底层连接：后续 RateDayLoad/RateDayAdd 必错。
	if err := db.Close(); err != nil {
		t.Fatalf("关库失败：%v", err)
	}
	if got := s.dayUsed(store.RateScopeFreePointUser, "42"); got != 0 {
		t.Fatalf("K5：读失败必须按 0 处理，实得 %d", got)
	}
	req := httptest.NewRequest("POST", "/api/chat", nil)
	req.RemoteAddr = "203.0.113.9:51234"
	route := s.instantDecide(req, 1, 42, instantInput{Kind: "text", LangCount: 1})
	if !route.Free {
		t.Fatalf("K5：计数器故障必须 fail-open 放行，实得 %+v", route)
	}
}

// N2：免费标记只有一个写点、扣费只有一个读点（按 api 包生产代码现算）。
func TestInstantFree_N2SingleWriteReadPoint(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读 api 目录失败：%v", err)
	}
	writeSites, readSites := 0, 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			continue
		}
		code := stripGoLineComments(string(b))
		writeSites += strings.Count(code, "llm.WithFreeInstant(")
		readSites += strings.Count(code, "llm.FreeInstantFromCtx(")
	}
	if writeSites != 1 {
		t.Fatalf("N2：免费标记写点必须唯一，实得 %d 处（多一处就有两条腿各判一次「扣不扣」）", writeSites)
	}
	if readSites != 1 {
		t.Fatalf("N2：免费标记读点必须唯一，实得 %d 处（出口只许 ChargeUsageRealtime 一个）", readSites)
	}
}

// W24：免费即时留痕行的形状（charge_kind=log／task_type=instant_free／quantity=真实 billed）。
func TestInstantFree_W24LedgerRowShape(t *testing.T) {
	s, st, _ := newFreeCounterServer(t)
	const billed = int64(1234)
	if err := st.LogUsage(1, 42, store.TaskTypeInstantFree, "prov", "model", "en", billed, "text", "pro"); err != nil {
		t.Fatalf("W24：写留痕失败：%v", err)
	}
	var kind, task string
	var qty int64
	if err := s.Store.DB().QueryRow("SELECT charge_kind, task_type, quantity FROM usage_ledger WHERE task_type=?", store.TaskTypeInstantFree).Scan(&kind, &task, &qty); err != nil {
		t.Fatalf("W24：读留痕失败：%v", err)
	}
	if kind != "log" || task != store.TaskTypeInstantFree || qty != billed {
		t.Fatalf("W24：留痕行形状不对（kind=%q task=%q qty=%d，期望 log/%s/%d）", kind, task, qty, store.TaskTypeInstantFree, billed)
	}
	// 源码腿：免费分支必须把**真实 billed** 传进去（写 0 占位＝成本账无从对，§三「假成功」那条①）。
	src, err := os.ReadFile("billing_api.go")
	if err != nil {
		t.Fatalf("W24：读 billing_api.go 失败：%v", err)
	}
	if !strings.Contains(string(src), "store.TaskTypeInstantFree") ||
		!strings.Contains(string(src), `tenant.LangFromContext(ctx), billed, "text", mode)`) {
		t.Fatal("W24：免费分支的 LogUsage 未按真实 billed 落账（或参数顺序漂了）")
	}
}
