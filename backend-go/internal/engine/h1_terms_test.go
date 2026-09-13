// ============================================================================
// H1 术语强制闭环：命中术语 → 译文残留字面强制覆写 / 违规带反馈重翻（闸门集成测试）
// ============================================================================
package engine

import (
	"context"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/tenant"
)

func TestH1TermForceGate(t *testing.T) {
	st := newTestStore(t)
	db := st.DB()
	if _, err := db.Exec(`INSERT INTO kb_packages (tenant_id, parent_id, code, name, pack_type, role, org_id, sort_order, created_at, updated_at)
		VALUES (1,0,'h1_terms','H1术语包','tenant','source',0,0,datetime('now'),datetime('now'))`); err != nil {
		t.Fatalf("建包失败: %v", err)
	}
	var pkgID int64
	if err := db.QueryRow("SELECT id FROM kb_packages WHERE code='h1_terms' AND tenant_id=1").Scan(&pkgID); err != nil {
		t.Fatalf("取包 ID 失败: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO kb_entries
		(tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at)
		VALUES (1,?,1,'zh','极石','en','ROX','term',datetime('now'),datetime('now'))`, pkgID); err != nil {
		t.Fatalf("插入术语失败: %v", err)
	}

	e := &Engine{St: st, Cfg: config.Default()}
	ctx := tenant.WithTenant(context.Background(), 1)
	source := "极石汽车发布新车"

	t.Run("残留源术语字面→强制覆写为规定译法", func(t *testing.T) {
		tr := map[string]string{"en": "极石 Motors launched a new car"}
		ws := e.applyOutputGates(ctx, source, tr, false)
		if tr["en"] != "ROX Motors launched a new car" {
			t.Fatalf("期望强制覆写为 ROX，得 %q", tr["en"])
		}
		joined := strings.Join(ws, "|")
		if !strings.Contains(joined, "术语强制替换") {
			t.Fatalf("期望警告含术语强制替换计数，得 %v", ws)
		}
	})

	t.Run("已合规译文不动", func(t *testing.T) {
		tr := map[string]string{"en": "ROX Motors launched a new car"}
		ws := e.applyOutputGates(ctx, source, tr, false)
		if tr["en"] != "ROX Motors launched a new car" {
			t.Fatalf("合规译文被改动: %q", tr["en"])
		}
		for _, w := range ws {
			if strings.Contains(w, "未通过") || strings.Contains(w, "术语") {
				t.Fatalf("合规译文不应产生术语警告: %v", ws)
			}
		}
	})

	t.Run("第三种写法无法覆写→记违规警告（retry=false 不重翻）", func(t *testing.T) {
		tr := map[string]string{"en": "JiShi Motors launched a new car"}
		ws := e.applyOutputGates(ctx, source, tr, false)
		if tr["en"] != "JiShi Motors launched a new car" {
			t.Fatalf("不可确定覆写的译文被强改: %q", tr["en"])
		}
		joined := strings.Join(ws, "|")
		if !strings.Contains(joined, "术语遵循") {
			t.Fatalf("期望术语遵循违规警告，得 %v", ws)
		}
	})

	t.Run("源文未命中术语→不适用", func(t *testing.T) {
		tr := map[string]string{"en": "The weather is fine today"}
		ws := e.applyOutputGates(ctx, "今天天气不错", tr, false)
		for _, w := range ws {
			if strings.Contains(w, "术语") {
				t.Fatalf("无关文本出现术语警告: %v", ws)
			}
		}
	})
}
