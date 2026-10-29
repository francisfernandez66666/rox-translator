// ============ term_reqs_test.go · 职责说明 ============
// P2-03（2026-10-26）第二道防线：存量脏术语不得进硬闸。
// 典型脏行＝target_lang=en、target_text=中文「英语」——任何含源词的正文都会被判
// 「术语未体现」，pro 模式还会多一次无意义重译。termReqs 必须跳过它，
// 同时正常术语（服务→Service）不受影响。
// ============================================
package engine

import (
	"context"
	"testing"

	"translator/internal/config"
	"translator/internal/tenant"
)

// TestTermReqsSkipsTargetScriptMismatch 脏行被跳过、正常行保留。
func TestTermReqsSkipsTargetScriptMismatch(t *testing.T) {
	st := newTestStore(t)
	db := st.DB()
	pkgID := createBrandPackage(t, db) // 借用同一套「租户级 source 包」夹具（查询按包 join）
	if _, err := db.Exec(`INSERT INTO kb_entries
		(tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at)
		VALUES (1,?,1,'zh','中文','en','英语','manual',datetime('now'),datetime('now'))`, pkgID); err != nil {
		t.Fatalf("插入脏术语失败: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO kb_entries
		(tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at)
		VALUES (1,?,1,'zh','服务','en','Service','manual',datetime('now'),datetime('now'))`, pkgID); err != nil {
		t.Fatalf("插入正常术语失败: %v", err)
	}
	e := NewEngine(config.Default(), nil, nil, nil)
	e.St = st
	ctx := tenant.WithTenant(context.Background(), 1)
	gc := e.newGateCtx(ctx)
	reqs := gc.termReqs(ctx, "我们提供中文服务", "en")
	if len(reqs) != 1 {
		t.Fatalf("脏术语应按书写系统被跳过，只留 1 条正常术语，实际 %d 条：%+v", len(reqs), reqs)
	}
	if reqs[0].Source != "服务" || reqs[0].Target != "Service" {
		t.Fatalf("留下的应是「服务→Service」：%+v", reqs[0])
	}
}
