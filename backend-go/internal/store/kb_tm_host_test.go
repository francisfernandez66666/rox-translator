// ============ kb_tm_host_test.go · 职责说明 ============
// TM 宿主租户规则锁：行业/语言文化包落平台 0，其余包落本租户；缺包必须报错。
// ================================================================
package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"translator/internal/config"

	_ "modernc.org/sqlite"
)

func TestTMHostTenantForPackage(t *testing.T) {
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "host.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TABLE kb_packages(id INTEGER PRIMARY KEY, pack_type TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO kb_packages(id,pack_type) VALUES(1,'department'),(2,'tenant'),(3,'industry'),(4,'locale'),(5,'persona')`); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]int64{1: 7, 2: 7, 3: 0, 4: 0, 5: 7} {
		got, err := TMHostTenantForPackage(raw, id, 7)
		if err != nil || got != want {
			t.Fatalf("包 %d 宿主应=%d，实得=%d err=%v", id, want, got, err)
		}
	}
	if _, err := TMHostTenantForPackage(raw, 999, 7); err == nil {
		t.Fatal("缺包必须报错，不能静默按 0 宿主继续")
	}
}
