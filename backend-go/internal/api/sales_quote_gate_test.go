// ============ sales_quote_gate_test.go · 职责说明 ============
// 三期 W2 A06 锁（施工_三期 §4.2 A06 行）：报价域的三条硬口径一起钉——
//   1) **AcceptQuote 是唯一转正写口**（静态腿）：剥注释后全 backend-go 非测试源码里
//      `AcceptQuote(` 恰 2 处＝store/deal_quote.go 定义 + sales_quote_api.go 唯一
//      handler 调用；`/api/admin/sales/quote/accept` 注册恰 1 次。多一处＝第二写口
//      （M0213 靶），「一单两转正」没有任何闸门看得见。
//   2) **金额只有「分」一个量纲**（静态腿 + 行为反证）：sales_quote_api.go 里不许出现
//      `*100`/`/100`（分↔元换算是 C2「同名不同单位」旧账）；行为腿建 1500 分读回
//      仍 1500（M0215 靶：任何一侧 ×100 都会红）。
//   3) **跨租户不可串**（行为腿）：租户 2 直调 store.AcceptQuote(2, 租户1报价) 必须
//      ErrQuoteNotFound（M0214 靶：摘 WHERE 的 tenant_id 即红）。
// 行为腿另覆盖 401/403/200 分档、create→get→accept→accepted→再 accept 404、
// 同 deal 第二张 409（部分唯一索引 → ErrQuoteOneOpenPerDeal → writeQuoteStoreError）。
// 复用既有夹具：adminGateServer（全量路由表＋普通会员 token）/ salesConfigToken /
// salesConfigCall（AGENTS §三：不手挑清单）。反证＝M0213/M0214/M0215 由
// gate_mutants 执行器在 worktree 里真跑红在本文件消息上。
// =============================================
package api

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/iam"
	"translator/internal/store"
)

// backendGoNonTestSources 走 backend-go 根下全部非测试 .go（相对路径→剥注释源码）。
// A06 的「唯一转正写口」判据按全仓扫——只在 api 包内扫会让 store 侧第二写口漏网。
// ⚠️ 包测试的 cwd 是 internal/api，backend-go 根是 ../..；键统一为相对该根的路径。
func backendGoNonTestSources(t *testing.T) map[string]string {
	t.Helper()
	const root = "../.."
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil //nolint:nilerr // 读不动的条目跳过：本闸扫现存源码，不是文件系统故障
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("读 %s 失败: %v", path, rerr)
		}
		// 键统一为相对 backend-go 根的路径（Walk 路径带 ../.. 前缀，直接做键会让
		// 靶文件查找失明——本仓「判据对形状做假设」那一族，先归一再入表）。
		rel, rerr2 := filepath.Rel(root, path)
		if rerr2 != nil {
			return nil //nolint:nilerr // 归一不了的不进表：射程下限自会兜住
		}
		out[filepath.ToSlash(rel)] = stripGoCommentsForGate(string(b))
		return nil
	})
	if err != nil {
		t.Fatalf("扫 backend-go 失败: %v", err)
	}
	if len(out) < 300 { // 射程下限：全仓非测试 .go 量级远大于此，扫到个位数＝路径写错
		t.Fatalf("backend-go 非测试源码只扫到 %d 个文件，射程可疑", len(out))
	}
	return out
}

// TestSalesQuoteAcceptSingleWritePort A06 静态腿：唯一转正写口 + 路由注册恰 1 + 量纲零换算。
func TestSalesQuoteAcceptSingleWritePort(t *testing.T) {
	pinSQLite(t) // 硬约定：单测自钉方言（本用例纯静态，不连库）
	sources := backendGoNonTestSources(t)

	// 腿 1：AcceptQuote( 恰 2 处（定义 + 唯一 handler 调用）。多一处＝第二转正写口。
	total := 0
	for _, src := range sources {
		total += strings.Count(src, "AcceptQuote(")
	}
	if total != 2 {
		t.Fatalf("全仓 AcceptQuote( 出现 %d 处（期望恰 2＝定义+唯一调用）：多出即第二转正写口，少即写口被摘", total)
	}
	// 腿 1b：转正路由注册恰 1 次。
	// ★ W3 校准（改方案留痕）：锚从裸路径串换成 HandleFunc 形态——W3 的 store.PermForEndpoint
	// 端点→权限真值表（sales_role.go）天然逐字包含同一路径串（那是权限映射不是注册），
	// 裸串判据会把「单一真值表登记了该端点」误判成第二写口。
	reg := 0
	for _, src := range sources {
		reg += strings.Count(src, `HandleFunc("/api/admin/sales/quote/accept"`)
	}
	if reg != 1 {
		t.Fatalf("/api/admin/sales/quote/accept 注册 %d 次（期望恰 1）", reg)
	}
	// 腿 2：量纲——报价 handler 不许做分↔元换算（*100 / /100 是 C2 旧账形态）。
	qsrc, ok := sources["internal/api/sales_quote_api.go"]
	if !ok {
		t.Fatal("找不到 sales_quote_api.go，A06 量纲腿失明（靶文件搬家了，锁要跟着搬）")
	}
	// regexp 语义下 *100 / /100 两个方向都拦；反证 M0215 在任一侧插入即红。
	for _, pat := range []string{`*100`, `/100`} {
		if strings.Contains(qsrc, pat) {
			t.Fatalf("sales_quote_api.go 出现 %q：金额只许「分」一个量纲，不许在 handler 换算", pat)
		}
	}
}

// TestSalesQuoteLifecycle A06 行为腿：401/403/200 分档 + 全生命周期 + 量纲 + 跨租户。
func TestSalesQuoteLifecycle(t *testing.T) {
	srv, memberTok := adminGateServer(t)

	// ① 未登录 401 / 普通会员 403 / 租户管理员 200（分档各有判据，不许混成一条）。
	code, _ := salesConfigCall(srv, "POST", "/api/admin/sales/quote/create", "", map[string]any{})
	if code != 401 {
		t.Fatalf("未登录建报价应 401，实得 %d", code)
	}
	code, _ = salesConfigCall(srv, "POST", "/api/admin/sales/quote/create", memberTok, map[string]any{})
	if code != 403 {
		t.Fatalf("普通会员建报价应 403，实得 %d", code)
	}
	adm1 := salesConfigToken(t, srv, 1, iam.RoleTenantAdmin)
	amount := int64(1500)
	code, resp := salesConfigCall(srv, "POST", "/api/admin/sales/quote/create", adm1,
		map[string]any{"deal_id": "deal-a", "total_cents": amount})
	if code != 200 {
		t.Fatalf("租户管理员建报价应 200，实得 %d（resp=%v）", code, resp)
	}
	quoteID, _ := resp["quote"].(map[string]interface{})["id"].(string)
	if quoteID == "" {
		t.Fatal("建报价回执缺 quote.id")
	}

	// ② 量纲：建 1500 分，读回必须仍是 1500（任何一侧 ×100/÷100 即红，M0215）。
	code, resp = salesConfigCall(srv, "GET", "/api/admin/sales/quote?quote_id="+quoteID, adm1, nil)
	if code != 200 {
		t.Fatalf("读报价应 200，实得 %d", code)
	}
	if got, _ := resp["quote"].(map[string]interface{})["total_cents"].(float64); int64(got) != 1500 {
		t.Fatalf("金额量纲被换算：建 1500 分读回 %v（分↔元只许一处口径）", got)
	}

	// ③ 同 deal 第二张 → 409（部分唯一索引 → ErrQuoteOneOpenPerDeal → 唯一错误折出口）。
	code, _ = salesConfigCall(srv, "POST", "/api/admin/sales/quote/create", adm1,
		map[string]any{"deal_id": "deal-a", "total_cents": amount})
	if code != 409 {
		t.Fatalf("同商机第二张在途报价应 409，实得 %d", code)
	}

	// ④ 生命周期：accept → accepted；再 accept → 404（状态已流转，不泄露存在性）。
	code, resp = salesConfigCall(srv, "POST", "/api/admin/sales/quote/accept", adm1,
		map[string]any{"quote_id": quoteID})
	if code != 200 {
		t.Fatalf("转正应 200，实得 %d（resp=%v）", code, resp)
	}
	if st, _ := resp["quote"].(map[string]interface{})["status"].(string); st != store.QuoteStatusAccepted {
		t.Fatalf("转正后状态应 accepted，实得 %q", st)
	}
	code, _ = salesConfigCall(srv, "POST", "/api/admin/sales/quote/accept", adm1,
		map[string]any{"quote_id": quoteID})
	if code != 404 {
		t.Fatalf("已转正再 accept 应 404，实得 %d", code)
	}

	// ⑤ 跨租户不可串（M0214 靶）：租户 2 管理员直调 store 层转租户 1 的报价，
	// 必须 ErrQuoteNotFound——WHERE 里摘掉 tenant_id 即红。
	// ⚠️ 靶报价必须**留在 draft 态**：已转正的报价再 accept 会撞
	// status IN ('draft','sent') 同样回 ErrQuoteNotFound，那样摘掉隔离也红不了
	// （判据空转那一族：红不了≠隔离在）。所以这里另建一张 draft 靶报价专供本腿。
	adm2 := salesConfigToken(t, srv, 2, iam.RoleTenantAdmin)
	_ = adm2 // HTTP 面由 GetQuote 的 effTenant 收口；store 面直调证隔离本身
	if err := srv.Store.AcceptQuote(2, quoteID); !errors.Is(err, store.ErrQuoteNotFound) {
		t.Fatalf("跨租户转正必须 ErrQuoteNotFound，实得 %v（tenant_id 隔离被摘＝M0214）", err)
	}
	code, resp = salesConfigCall(srv, "POST", "/api/admin/sales/quote/create", adm1,
		map[string]any{"deal_id": "deal-ct", "total_cents": int64(1)})
	if code != 200 {
		t.Fatalf("建跨租户靶报价（draft 态）应 200，实得 %d", code)
	}
	ctID, _ := resp["quote"].(map[string]interface{})["id"].(string)
	if ctID == "" {
		t.Fatal("跨租户靶报价回执缺 id")
	}
	if err := srv.Store.AcceptQuote(2, ctID); !errors.Is(err, store.ErrQuoteNotFound) {
		t.Fatalf("跨租户转正必须 ErrQuoteNotFound（draft 靶报价实得 %v；隔离被摘时这张会被真转正＝M0214）", err)
	}
	// ⑥ 存在的真实报价，租户 2 经 HTTP 读也必须 404（读侧同尺）。
	code, _ = salesConfigCall(srv, "GET", "/api/admin/sales/quote?quote_id="+quoteID, adm2, nil)
	if code != 404 {
		t.Fatalf("跨租户读报价应 404（不泄露存在性），实得 %d", code)
	}

	// ⑦ 负数金额拒收（handler 侧判据给可辨文案）。
	code, _ = salesConfigCall(srv, "POST", "/api/admin/sales/quote/create", adm1,
		map[string]any{"deal_id": "deal-b", "total_cents": -1})
	if code != 400 {
		t.Fatalf("负数金额应 400，实得 %d", code)
	}
}
