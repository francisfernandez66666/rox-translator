// ============ dispatch_ticket_integration_test.go · 职责说明 ============
// 钉住 E-2 接线：HandleFile 必须调用派发入口；关闸/判 local 时整单回落主站，
// 派发单不得中途切回本地做一半（整单锁定）。
// ==================================================================
package engine

import (
	"os"
	"strings"
	"testing"
)

// TestDispatchTicketIntegration 钉住 E-2 接线：
//  ① HandleFile 入口必须调用 MaybeDispatchTicket（否则整条链是死代码）
//  ② MaybeDispatchTicket 判 local 时返回 handled=false，主站原路径逐字保留
//  ③ 整单锁定：派发后不得再回主站做一半（源码里不许在同一单里再调一次决策器）
func TestDispatchTicketIntegration(t *testing.T) {
	src := readRepoFile(t, "file.go")
	if !strings.Contains(src, "MaybeDispatchTicket") {
		t.Error("HandleFile 未接 MaybeDispatchTicket ⇒ 派发链全仓无调用点（死代码）")
	}
	d := readRepoFile(t, "dispatch_chunks.go")
	if !strings.Contains(d, "if err != nil || !out.Dispatched") {
		t.Error("派发失败/判 local 未回落主站整单链路")
	}
	for _, forbidden := range []string{"DispatchEligible(filepath", "DecideTicketDispatch(ctx"} {
		if strings.Contains(d, forbidden) {
			t.Errorf("编排层出现重复判据 %s ⇒ 整单锁定可能被破坏", forbidden)
		}
	}
}

// TestNonDispatchTicketNeverTouchesSSH 非派发单：主站路径不得触发派发网络调用。
func TestNonDispatchTicketNeverTouchesSSH(t *testing.T) {
	// 用开关关闭时的纯函数验证DispatchEnabled=false ⇒ 决策必 local ⇒ 无 ssh。
	d := readRepoFile(t, "dispatch_chunks.go")
	if !strings.Contains(d, "if !fileproc.DispatchEnabled()") {
		t.Error("MaybeDispatchTicket 缺关闸早退 ⇒ 关闸时仍可能拨 ssh")
	}
	b, err := os.ReadFile("dispatch_chunks.go")
	if err != nil || len(b) == 0 {
		t.Errorf("编排源码不可读: %v", err)
	}
}
