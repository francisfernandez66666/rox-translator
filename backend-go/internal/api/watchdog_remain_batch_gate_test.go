// ============ watchdog_remain_batch_gate_test.go · 职责说明 ============
// ★ 2026-11-04 体验批 §2.12 A12：watchdog 两处「遍历租户读余额」都必须接在批量口
//
//	  TenantsRemainTotalMap 上（同一纪律两个实例，只钉一个就是「两个实例只钉了一个」的空转）。
//
//		W1 runGrowthScan 函数体：含批量口、不含逐租户 TenantRemainTotal、含「读数缺席才跳过」；
//		W2 runWatchdogCheck 函数体：同上（本批才改的那一处，守的就是这处接线）；
//		W3 反证：把「改回逐租户」/「摘掉缺席判定」的变异体喂同一个判据函数必须判红，
//		   否则上面两条负向腿是空转（AGENTS §三）。
//
// 纯静态源码扫描（不连库、不读 config）；按硬约定仍在用例开头自钉一次 sqlite 方言
// （AGENTS §一·4，config.Default() 有全局副作用）。
// =============================================
package api

import (
	"strings"
	"testing"
)

// watchdogRemainBatchViolations 接线判据本体：吃一个巡检函数体（剥注释后），返回违规原因列表。
func watchdogRemainBatchViolations(body string) []string {
	var bad []string
	if !strings.Contains(body, "TenantsRemainTotalMap(") {
		bad = append(bad, "没接批量口 TenantsRemainTotalMap")
	}
	if strings.Contains(body, "TenantRemainTotal(") {
		bad = append(bad, "仍残留逐租户 TenantRemainTotal 调用（N+1 又回来了）")
	}
	if !strings.Contains(body, "permanentByTID[") || !strings.Contains(body, "if !okP") || !strings.Contains(body, "continue") {
		bad = append(bad, "缺『读数缺席才跳过』：把读失败当余额 0 会把挽回礼头发错人")
	}
	return bad
}

// watchdogFuncBody 从 watchdog.go 剥注释源码里截出某个顶层函数体（到下一个顶格 func 声明为止）。
func watchdogFuncBody(src, signature string) string {
	start := strings.Index(src, signature)
	if start < 0 {
		return ""
	}
	rest := src[start+len(signature):]
	if i := strings.Index(rest, "\nfunc "); i >= 0 {
		return src[start : start+len(signature)+i]
	}
	return src[start:]
}

// TestWatchdogBothRemainScansUseBatch 源码级接线锁：两处巡检都走批量口且保住缺席跳过。
func TestWatchdogBothRemainScansUseBatch(t *testing.T) {
	pinSQLite(t) // 硬约定：新单测自钉方言（本用例纯静态，不连库）
	sources := apiNonTestSources(t)
	src, ok := sources["watchdog.go"]
	if !ok {
		t.Fatalf("watchdog.go 不在扫描结果里（改名/移动会让本闸静默失明）")
	}
	for _, sig := range []string{
		"func (s *Server) runGrowthScan()",
		"func (s *Server) runWatchdogCheck()",
	} {
		body := watchdogFuncBody(src, sig)
		if body == "" {
			t.Errorf("找不到 %s 的函数体（签名变了要同步改这条锁，别删断言）", sig)
			continue
		}
		if bad := watchdogRemainBatchViolations(body); len(bad) > 0 {
			t.Errorf("%s 接线不合规：%v", sig, bad)
		}
	}

	// 反证（★ 恒零命中的负向腿必须配正对照）：同一判据喂合规体必须放行、喂变异体必须判红。
	clean := "func f() {\n\tgrantsByTID, permanentByTID, e := s.Store.TenantsRemainTotalMap(active)\n\t_ = e\n\tperm, okP := permanentByTID[t.ID]\n\tif !okP {\n\t\tcontinue\n\t}\n\t_ = grantsByTID[t.ID] + perm\n}"
	if bad := watchdogRemainBatchViolations(clean); len(bad) != 0 {
		t.Fatalf("合规体被判红（判据过宽，迟早被加白名单绕过）：%v", bad)
	}
	mutants := []struct{ name, body, why string }{
		{"改回逐租户", strings.Replace(clean, "TenantsRemainTotalMap(", "TenantRemainTotal(", 1), "逐租户调用"},
		{"摘掉缺席判定", strings.Replace(clean,
			"perm, okP := permanentByTID[t.ID]\n\tif !okP {\n\t\tcontinue\n\t}",
			"perm := permanentByTID[t.ID]", 1), "缺席跳过"},
	}
	for _, m := range mutants {
		if bad := watchdogRemainBatchViolations(m.body); len(bad) == 0 {
			t.Errorf("反证 %q 未被判出 ⇒ 判据对「%s」无射程（真缺陷会一起绿灯）", m.name, m.why)
		}
	}
}
