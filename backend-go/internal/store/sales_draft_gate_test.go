// ============ sales_draft_gate_test.go · 职责说明 ============
// 二期 §7 草稿池的三道判据：
//
//	M2 行为：新产物恒为 draft（调用方无法指定状态）；转正只经 ApproveSalesDraft，
//	   重复转正／跨租户一律 not-found；转正不自动写 kb_entries（自学习边界：
//	   挖掘说法永不自动进一期「已批准说法」，召回接线随 strategy 平移另落）；
//	M1 源码级：全仓非测试源码里 `UPDATE sales_drafts` 只许一处，且必须落在
//	   ApproveSalesDraft 函数体内（按花括号配平切体，防"下一个顶层声明"切过头）；
//	射程自证：同一个计数函数喂「真源码＋一段违规 UPDATE」必须报 2，
//	   防计数函数被写死成恒 1（AGENTS §三「判据级代替不了守卫级」同族）。
//
// ========================================
package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// newDraftTestStore 建一个自钉 SQLite 方言的测试 Store（AGENTS §一·4 防 PG 泄漏）。
func newDraftTestStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return newTestStore(t)
}

// M2 主判据：草稿态起步、转正唯一入口、跨租户不串、不自动进已批准说法。
func TestSalesDraftLifecycleIsHumanGated(t *testing.T) {
	s := newDraftTestStore(t)

	// 非法入参：无租户与空建议一律拒（草稿没有可审内容就不许落库）。
	if _, err := s.CreateSalesDraft(0, "a", "src", "sug", ""); err == nil {
		t.Fatal("tid<=0 必须拒")
	}
	if _, err := s.CreateSalesDraft(1, "a", "src", "  ", ""); err == nil {
		t.Fatal("空建议说法必须拒")
	}

	d, err := s.CreateSalesDraft(1, "price-anchor", "客户原话", "建议说法", "attr-001")
	if err != nil {
		t.Fatalf("建草稿失败: %v", err)
	}
	if d.Status != SalesDraftStatusDraft {
		t.Fatalf("M2：新产物必须是 draft，实得 %s ⇒ 自动生成不许直接生效", d.Status)
	}

	// 转正前 kb_entries 基线（同刻取数，只比增量）。
	kbBefore := countKBEntries(t, s)

	if err := s.ApproveSalesDraft(1, d.ID); err != nil {
		t.Fatalf("人审转正失败: %v", err)
	}
	got, err := s.GetSalesDraft(1, d.ID)
	if err != nil || got.Status != SalesDraftStatusApproved {
		t.Fatalf("转正后读回应为 approved: %+v err=%v", got, err)
	}
	// 重复转正与跨租户一律 not-found（不泄露存在性）。
	if err := s.ApproveSalesDraft(1, d.ID); !errors.Is(err, ErrSalesDraftNotFound) {
		t.Fatalf("重复转正必须 not-found，实得 %v", err)
	}
	if _, err := s.GetSalesDraft(2, d.ID); !errors.Is(err, ErrSalesDraftNotFound) {
		t.Fatalf("跨租户读必须 not-found，实得 %v", err)
	}
	if err := s.ApproveSalesDraft(2, d.ID); !errors.Is(err, ErrSalesDraftNotFound) {
		t.Fatalf("跨租户转正必须 not-found，实得 %v", err)
	}

	// 自学习边界：转正也不自动写 kb_entries（晋升召回池是引擎迁入后的显式路径）。
	if kbAfter := countKBEntries(t, s); kbAfter != kbBefore {
		t.Fatalf("M2/M3：草稿转正不许自动进 kb_entries（%d → %d）⇒ 越用越像幻觉的分界失守", kbBefore, kbAfter)
	}

	// 列表按状态过滤；空集出 []（不是 nil/null）。
	if list, err := s.ListSalesDrafts(1, SalesDraftStatusDraft); err != nil || len(list) != 0 || list == nil {
		t.Fatalf("draft 过滤应为空切片: %v err=%v", list, err)
	}
	if list, err := s.ListSalesDrafts(1, SalesDraftStatusApproved); err != nil || len(list) != 1 {
		t.Fatalf("approved 过滤应恰好 1 条: %v err=%v", list, err)
	}
	if list, err := s.ListSalesDrafts(2, ""); err != nil || len(list) != 0 {
		t.Fatalf("别的租户必须一条都看不到: %v err=%v", list, err)
	}
}

// countKBEntries 现读 kb_entries 总行数（草稿自学习边界的对账基线）。
func countKBEntries(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := db.QueryRow(s.db, db.CurrentDialect(), `SELECT COUNT(*) FROM kb_entries`).Scan(&n); err != nil {
		t.Fatalf("读 kb_entries 计数失败: %v", err)
	}
	return n
}

// M1 源码级：全仓非测试源码里 UPDATE sales_drafts 只许一处，且在 ApproveSalesDraft 体内。
func TestSalesDraftApproveIsSingleWriteSite(t *testing.T) {
	root := gateRepoRoot(t)
	total := 0
	var sites []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			if base == "node_modules" || base == ".git" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n := countSalesDraftUpdates(stripGoComments(string(raw)))
		if n > 0 {
			rel, _ := filepath.Rel(root, path)
			sites = append(sites, rel)
			total += n
		}
		return nil
	})
	if err != nil {
		t.Fatalf("全仓扫描失败: %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("M1 扫描失明：一处 UPDATE sales_drafts 都没扫到（锚点漂了？）")
	}
	if total != 1 || len(sites) != 1 || sites[0] != filepath.Join("internal", "store", "sales_draft.go") {
		t.Fatalf("M1：转正写点只许一处（store/sales_draft.go），实得 %d 处 %v ⇒ 谁在偷偷改草稿状态", total, sites)
	}

	// 那一处必须落在 ApproveSalesDraft 函数体内（花括号配平切体）。
	raw, err := os.ReadFile(filepath.Join(root, "internal", "store", "sales_draft.go"))
	if err != nil {
		t.Fatalf("读 sales_draft.go 失败: %v", err)
	}
	body := approveFuncBody(t, stripGoComments(string(raw)))
	if countSalesDraftUpdates(body) != 1 {
		t.Fatal("M1：唯一的 UPDATE sales_drafts 必须在 ApproveSalesDraft 函数体内")
	}
}

// 射程自证：计数函数喂「真源码＋违规第二处」必须报 2（防写死恒 1 的假扫描器）。
func TestSalesDraftUpdateScanHasTeeth(t *testing.T) {
	raw, err := os.ReadFile("sales_draft.go")
	if err != nil {
		t.Fatalf("读 sales_draft.go 失败: %v", err)
	}
	clean := stripGoComments(string(raw))
	if n := countSalesDraftUpdates(clean); n != 1 {
		t.Fatalf("真源码应恰好 1 处写点，实得 %d", n)
	}
	rogue := clean + "\nfunc rogue() { db.Exec(nil, \"\", `UPDATE sales_drafts SET status='approved'`) }\n"
	if n := countSalesDraftUpdates(rogue); n != 2 {
		t.Fatalf("反证：第二处违规 UPDATE 必须被数出来（期望 2，实得 %d）⇒ 扫描器失明", n)
	}
	// 注释里的 UPDATE 不算写点（剥注释腿自证：先拼注释再走同一条剥＋数管线）。
	if n := countSalesDraftUpdates(stripGoComments(clean + "\n// UPDATE sales_drafts SET status='approved'\n")); n != 1 {
		t.Fatalf("注释里的字面量不许算写点，实得 %d", n)
	}
}

// countSalesDraftUpdates 数剥注释后源码里的草稿状态写点（M1 的唯一计数口）。
// 参数：src=已剥注释的源码；返回命中数。
func countSalesDraftUpdates(src string) int {
	return strings.Count(src, "UPDATE sales_drafts")
}

// approveFuncBody 按花括号配平切出 ApproveSalesDraft 的函数体（含签名后第一对花括号之间）。
// 参数：t=测试上下文；src=已剥注释的 sales_draft.go 全文；返回函数体源码。
func approveFuncBody(t *testing.T, src string) string {
	t.Helper()
	sig := "func (s *Store) ApproveSalesDraft("
	i := strings.Index(src, sig)
	if i < 0 {
		t.Fatal("M1 锚点漂移：找不到 ApproveSalesDraft 签名")
	}
	open := strings.Index(src[i:], "{")
	if open < 0 {
		t.Fatal("M1 锚点漂移：签名后找不到函数体起点")
	}
	start := i + open
	depth := 0
	for j := start; j < len(src); j++ {
		switch src[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : j+1]
			}
		}
	}
	t.Fatal("M1 锚点漂移：函数体花括号不配平")
	return ""
}
