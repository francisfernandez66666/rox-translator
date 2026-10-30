// ============ ticketartifacts_pg_test.go · 职责说明 ============
// ticket_artifacts 的 **PostgreSQL 真库**腿（★ 2026-10-30 组5 两段式交付，B1 的「双方言各跑一次」）。
//
// 为什么单开一份：ticket_artifacts 的写路径用了 `ON CONFLICT(...) DO UPDATE` 与
// `INTEGER PRIMARY KEY AUTOINCREMENT`（经 db.ExecDDL 译成 BIGSERIAL）。这两处
// **SQLite 全绿而 PG 可能语法/目标不匹配**是本仓踩过的形态（见 f44_pg_test.go 的教训）：
// 本地快跑替代不了真驱动跑一次（AGENTS §一·4）。
//
// 依赖：PG_TEST_DSN（默认 postgres://$USER@127.0.0.1:5432/pgtest），无 PG 即 Skip。
// 方言自钉 postgres 并在 Cleanup 恢复（config.C 是全局的，不恢复会漏给同包内存 SQLite 用例）。
// 运行：env DB_DRIVER=postgres PG_TEST_DSN=... go test -count=1 ./internal/store/ -run TicketArtifactPG
// =============================================
package store_test

import (
	"os"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/store"

	_ "github.com/lib/pq"
)

// newPGStoreForArtifacts 建真 PG 上的 Store（跑完整幂等迁移链）。
func newPGStoreForArtifacts(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://" + os.Getenv("USER") + "@127.0.0.1:5432/pgtest?sslmode=disable"
	}
	conn, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: dsn})
	if err != nil {
		t.Skipf("无可用 PostgreSQL，跳过：%v", err)
	}
	t.Cleanup(func() { conn.Close() })
	prevC := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "postgres"
	config.C = cfg
	t.Cleanup(func() { config.C = prevC })
	s, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New(PG) 失败: %v", err)
	}
	return s
}

// TestTicketArtifactPGUpsertAndList B1 的 PG 腿：写入→同键重发→读回等值→删行。
// ID 随机化（上限压进 int32：这几列在 PG 侧是 INTEGER），避免多次运行互踩唯一键。
func TestTicketArtifactPGUpsertAndList(t *testing.T) {
	s := newPGStoreForArtifacts(t)
	ticketID := time.Now().Unix()%1_000_000_000 + 1_000_000_000
	tenantID := ticketID%800_000 + 100_000
	defer func() {
		_, _ = db.Exec(s.DB(), db.CurrentDialect(), `DELETE FROM ticket_artifacts WHERE ticket_id=?`, ticketID)
	}()

	a := &store.TicketArtifact{TenantID: tenantID, TicketID: ticketID, FileID: 3, Lang: "en",
		Kind: store.TicketArtifactKindMD, Path: "/uat/pg/a_en-text.md", Name: "a_en-text.md", Size: 12}
	if _, err := s.UpsertTicketArtifact(a); err != nil {
		t.Fatalf("PG 首次登记失败（ON CONFLICT/DDL 方言问题？）: %v", err)
	}
	a2 := &store.TicketArtifact{TenantID: tenantID, TicketID: ticketID, FileID: 3, Lang: "en",
		Kind: store.TicketArtifactKindMD, Path: "/uat/pg/a_en-text.md", Name: "a_en.md", Size: 13, Degraded: true}
	if _, err := s.UpsertTicketArtifact(a2); err != nil {
		t.Fatalf("PG 同键重发失败: %v", err)
	}
	list, err := s.ListTicketArtifacts(ticketID)
	if err != nil {
		t.Fatalf("PG 列表失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("PG 上同键应只留一行，实得 %d 行（唯一索引/ON CONFLICT 目标不匹配？）", len(list))
	}
	got := list[0]
	if !got.Degraded || got.Name != "a_en.md" || got.Size != 13 {
		t.Fatalf("PG 上重发应覆盖 name/size/degraded：%+v", got)
	}
	// 归属强校验：别单的 ticketID 查不到。
	if other, _ := s.GetTicketArtifact(got.ID, ticketID+1); other != nil {
		t.Fatalf("PG 上 aid 不属于该单时必须查不到，实得 %+v", other)
	}
	paths, err := s.TicketArtifactPaths(ticketID)
	if err != nil || len(paths) != 1 || paths[0] != "/uat/pg/a_en-text.md" {
		t.Fatalf("PG 路径集合不符：paths=%v err=%v", paths, err)
	}
	if err := s.DeleteTicketArtifactsForTicket(ticketID); err != nil {
		t.Fatalf("PG 删行失败: %v", err)
	}
	if rest, _ := s.ListTicketArtifacts(ticketID); len(rest) != 0 {
		t.Fatalf("PG 删行后应零行，实得 %d", len(rest))
	}
}
