// ============================================================================
// gdpr_export_test.go · 职责说明
// 数据主体导出（ExportTenantData）的**完整性**常设锁（★ 2026-09-29 092x）。
//
// 钉死的口径：
//  1. 用量流水与审计日志按 ID 游标取到表尾，一行不少（造 520 条就必须回 520 条）；
//  2. 前台分页的防拖库闸**不许因为导出而被放宽**——UsageLedgerList 仍把 100000 收敛到 50、
//     ListAuditFilter 仍收敛到 100。这条是反向对照：哪天有人图省事去抬那两处上限，本条当场红；
//  3. 取不全/取不到一律返回 error，不许再交「长得和完整包一模一样」的残缺副本；
//  4. 导出副本必须带 charge_kind——「这一行到底扣了客户多少钱」是数据主体最该拿到的一维
//     （前台那条 SELECT 没有这列）；
//  5. tid<=0 直接拒：audit_logs.tenant_id=0 是平台级操作，放行 0 等于把租户导出
//     变成导出平台自己的审计流水。
//
// 现网病灶（本批修复的动因，非假设）：导出侧请求写的是 100000 行，
// 底层闸把它收敛成 50 条流水／100 条审计，且 error 是 nil ⇒ 客户依法索数拿到残缺副本零提示，
// 拿去申报或举证才是事故。
//
// 方言：钉 SQLite 内存库并显式改 config.C（AGENTS.md §一·4——run_uat 的 PG 模式会把
// 方言泄漏给同包内存库用例）。PG 侧覆盖由 run_uat 矩阵承担（本批已随批跑）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run GdprExport
// ============================================================================
package store

import (
	"database/sql"
	"testing"
	"time"

	"translator/internal/config"
)

// newGdprExportEnv 建导出测试环境：钉方言 + 内存库 + 迁移建表。
func newGdprExportEnv(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	st, err := New(sqlDB)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return st
}

// seedUsageRows 直插 usage_ledger 造 n 条流水（走 RecordUsage 会连带扣余额与日计数器，
// 本锁只关「取数取不取得全」，造数用直插把旁支副作用摘干净；charge_kind 按 n 的奇偶交替）。
func seedUsageRows(t *testing.T, st *Store, tid int64, n int) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 1; i <= n; i++ {
		kind := "charge"
		if i%5 == 0 {
			kind = "log" // 每五条留一条平台承担/免扣留痕，供 charge_kind 出栈断言
		}
		if _, err := st.db.Exec(
			`INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at)
			 VALUES (?,?, 'translate','bigmodel','glm',?,1,?, 'text','pro',?,?)`,
			tid, int64(1000+int(tid)), int64(i), int64(i), kind, now); err != nil {
			t.Fatalf("租户 %d 第 %d 条用量流水造数失败: %v", tid, i, err)
		}
	}
}

// idsOf 取导出流水的 ID 集合（重复即少行——去重后计数能同时抓住「重行」与「漏行」两种形态）。
func idsOfUsage(rows []*UsageLedger) map[int64]bool {
	m := map[int64]bool{}
	for _, r := range rows {
		m[r.ID] = true
	}
	return m
}

// TestGdprExportScansEveryRowBeyondClamp ①②③④：导出必须跨过前台分页闸门取到全量，
// 而闸门本身一档都不许松。
func TestGdprExportScansEveryRowBeyondClamp(t *testing.T) {
	st := newGdprExportEnv(t)

	// 租户 7：520 条流水（**跨过 500 的批边界**＝500＋20 两页）＋ 250 条审计
	// 租户 8：恰好 500 条流水（满一批后只能靠「下一页为空」收敛——游标循环的另一个出口）
	seedUsageRows(t, st, 7, 520)
	seedUsageRows(t, st, 8, 500)
	for i := 1; i <= 250; i++ {
		st.LogAudit(7, int64(1007), "update", "kb_package", "第"+time.Now().Format("150405")+"条")
	}

	t.Run("反向对照：前台分页闸一格没松（谁抬上限谁红）", func(t *testing.T) {
		if got, err := st.UsageLedgerList(7, 100000, 0, false); err != nil {
			t.Fatalf("前台列表查询失败: %v", err)
		} else if len(got) != 50 {
			t.Fatalf("前台用量列表的 limit 闸应仍把 100000 收敛到 50，实得 %d 条（防拖库闸被放宽了？）", len(got))
		}
		if got, err := st.ListAuditFilter(7, "", "", 0, "", "", 100000); err != nil {
			t.Fatalf("前台审计列表查询失败: %v", err)
		} else if len(got) != 100 {
			t.Fatalf("前台审计列表的 limit 闸应仍把 100000 收敛到 100，实得 %d 条", len(got))
		}
	})

	t.Run("520 条流水导出＝520 条，审计 250 条＝250 条", func(t *testing.T) {
		data, err := st.ExportTenantData(7)
		if err != nil {
			t.Fatalf("导出失败: %v", err)
		}
		usage, ok := data["usage"].([]*UsageLedger)
		if !ok {
			t.Fatalf("usage 出栈类型不是 []*UsageLedger：%T", data["usage"])
		}
		if len(usage) != 520 {
			t.Fatalf("用量流水应导全 520 条，实得 %d 条（50⇒还在吃前台分页闸门；其余数字⇒游标翻页漏行）", len(usage))
		}
		if ids := idsOfUsage(usage); len(ids) != 520 {
			t.Fatalf("导出流水去重后只剩 %d 个 ID，说明游标翻页出了重行", len(ids))
		}
		audit, ok := data["audit"].([]*AuditLog)
		if !ok {
			t.Fatalf("audit 出栈类型不是 []*AuditLog：%T", data["audit"])
		}
		if len(audit) != 250 {
			t.Fatalf("审计日志应导全 250 条，实得 %d 条（100⇒还在吃前台闸门）", len(audit))
		}
		seen := map[int64]bool{}
		for _, a := range audit {
			if seen[a.ID] {
				t.Fatalf("审计导出出现重行 id=%d", a.ID)
			}
			seen[a.ID] = true
		}
	})

	t.Run("恰满一批（500 条）的租户也能收敛", func(t *testing.T) {
		data, err := st.ExportTenantData(8)
		if err != nil {
			t.Fatalf("导出失败: %v", err)
		}
		usage := data["usage"].([]*UsageLedger)
		if len(usage) != 500 {
			t.Fatalf("恰好 500 条的租户应导全 500 条，实得 %d 条", len(usage))
		}
	})

	t.Run("副本必须答得出「这一行扣没扣钱」（charge_kind 出栈）", func(t *testing.T) {
		data, err := st.ExportTenantData(7)
		if err != nil {
			t.Fatalf("导出失败: %v", err)
		}
		charge, log := 0, 0
		for _, u := range data["usage"].([]*UsageLedger) {
			switch u.ChargeKind {
			case "charge":
				charge++
			case "log":
				log++
			}
		}
		if charge == 0 || log == 0 {
			t.Fatalf("导出副本里 charge_kind 应有实扣与留痕两态（前台那条 SELECT 没这列，补齐正是本次要修的口径），实得 charge=%d log=%d", charge, log)
		}
	})

	t.Run("行数自证随包出栈", func(t *testing.T) {
		data, err := st.ExportTenantData(7)
		if err != nil {
			t.Fatalf("导出失败: %v", err)
		}
		meta, ok := data["export_meta"].(map[string]interface{})
		if !ok {
			t.Fatalf("缺 export_meta：%T", data["export_meta"])
		}
		if meta["usage_rows"] != 520 || meta["audit_rows"] != 250 {
			t.Fatalf("export_meta 行数与实际出栈不符：%v", meta)
		}
		if meta["completeness"] != "full-scan-by-id-cursor" {
			t.Fatalf("export_meta 没写明取数口径：%v", meta["completeness"])
		}
	})

	t.Run("租户边界：导出 7 不许夹带 8 的行", func(t *testing.T) {
		data, err := st.ExportTenantData(7)
		if err != nil {
			t.Fatalf("导出失败: %v", err)
		}
		for _, u := range data["usage"].([]*UsageLedger) {
			if u.TenantID != 7 {
				t.Fatalf("导出串租户：usage id=%d tenant_id=%d", u.ID, u.TenantID)
			}
		}
		for _, a := range data["audit"].([]*AuditLog) {
			if a.TenantID != 7 {
				t.Fatalf("导出串租户：audit id=%d tenant_id=%d", a.ID, a.TenantID)
			}
		}
	})
}

// TestGdprExportRejectsNonTenantScope ⑤：tid<=0 必须拒，不能把平台级（tenant_id=0）
// 的审计流水当成「某个租户的数据主权包」送出去。
func TestGdprExportRejectsNonTenantScope(t *testing.T) {
	st := newGdprExportEnv(t)
	st.LogAudit(0, 0, "system", "platform", "平台级操作")
	if _, err := st.ExportTenantData(0); err == nil {
		t.Fatalf("tid=0 竟然导出成功：等于把平台审计流水交给租户数据主权导出")
	}
	if _, err := st.ExportTenantData(-3); err == nil {
		t.Fatalf("tid=-3 竟然导出成功")
	}
}
