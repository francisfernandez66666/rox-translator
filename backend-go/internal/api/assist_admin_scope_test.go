// ============ assist_admin_scope_test.go · 职责说明 ============
// 挂件管理面鉴权的**跨侧等值锁**（★ 2026-10-30 三事合一批 Q9／D-13／S6）。
//
// 守的事：旧面 `/api/admin/assist/*` 曾用 `requireAdminUser`（只判等级≥4、**不查 tenant_id**），
// 比新面松一档——任何租户管理员都能改全网共用的挂件话术与知识库。
// 用户 2026-10-30 明令「直接收」：旧面与新面收成同一把尺子 `auth.IsSuperAdmin`（等级≥4 且 tenant_id=0）。
//
// 判据按**源码**扫（不写死行号）：
//
//	A. 该文件的鉴权入口只许是 `requireAssistAdmin`，不许再出现 `requireAdminUser(`；
//	B. `IsSuperAdmin` 必须是该唯一助手里的判据；
//	C. 助手定义必须只有一份（两处各写一份＝迟早只改一处，正是 S6 要防的形态）。
//
// 反证：把任一调用点改回 `requireAdminUser(r)` ⇒ A 红；把助手里的 IsSuperAdmin 换成 RequireRole(u,4) ⇒ B 红。
// =============================================
package api

import (
	"os"
	"strings"
	"testing"
)

func TestAssistAdminScope_SameGaugeAcrossFaces(t *testing.T) {
	raw, err := os.ReadFile("admin_assist_proxy.go")
	if err != nil {
		t.Fatalf("读不到 admin_assist_proxy.go（闸门失明）: %v", err)
	}
	src := string(raw)

	// A. 旧判据不许回来
	if strings.Contains(src, "requireAdminUser(r)") {
		t.Fatal("Q9/S6：挂件管理面又出现了 requireAdminUser(r)（只判等级、不查 tenant_id）——旧面比新面松一档")
	}
	if n := strings.Count(src, "requireAssistAdmin(r)"); n < 2 {
		t.Fatalf("Q9/S6：两个入口都必须走 requireAssistAdmin（实得 %d 处调用）", n)
	}
	// C. 助手只有一份
	if n := strings.Count(src, "func (s *Server) requireAssistAdmin("); n != 1 {
		t.Fatalf("Q9/S6：requireAssistAdmin 必须恰好定义一次（实得 %d）——两处各写一份就会只改一处", n)
	}
	// B. 判据本体是 IsSuperAdmin，且不许退回按等级判
	idx := strings.Index(src, "func (s *Server) requireAssistAdmin(")
	if idx < 0 {
		t.Fatal("Q9/S6：找不到 requireAssistAdmin 定义")
	}
	body := src[idx:]
	if end := strings.Index(body[1:], "\nfunc "); end > 0 {
		body = body[:end+1]
	}
	if !strings.Contains(body, "auth.IsSuperAdmin(u)") {
		t.Fatal("Q9/S6：判定必须是 auth.IsSuperAdmin（等级≥4 且 tenant_id=0），不许退回 RequireRole(…, 4)")
	}
	if strings.Contains(body, "RequireRole(") {
		t.Fatal("Q9/S6：助手体内不许再用 RequireRole 判——那正是 Q9 的旧形态")
	}
}
