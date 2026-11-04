// ============ usage_export_walk.go · 职责说明 ============
// store 包内部实现文件。
//
// ============ 本文件职责中文说明 ============
// ★ 2026-11-04 体验批 A8：用量明细导出从「一条查询把最多 10 万行全部进内存」改成
//
//	**分批游标（keyset）＋ 回调**。
//
// 改造前的形态不是慢，是**内存与尾延迟**：`UsageLedgerForExport` 单查询 `LIMIT 100000`
// 把 13 列 × 10 万行一次性 materialize 成 []*UsageLedger，api 侧再逐行写 CSV。
// 财务对账季一个租户全量导出，峰值堆就是这一口；而它发生在**别的请求也在跑的进程里**。
//
// 三条判据：
//
//	① 谓词只有一份（usageExportWhere）——旧的 UsageLedgerForExport 里那一串 WHERE 与这里
//	   迟早打架，所以旧函数改成**薄委托**，不再自己拼；
//	② 翻页走 **keyset（id < 上一批最小 id）** 而不是 OFFSET：
//	   OFFSET 在大表上是"扫过再丢掉"，越翻越慢，而导出本来就要翻到底；
//	③ 行数硬上限语义逐字不变（limit<=0 或 >100000 ⇒ 100000；超出按 id 倒序截最新段）。
//
// =============================================
package store

import (
	"database/sql"

	"translator/internal/db"
)

// UsageLedgerExportPageSize 导出分批页大小（唯一写点）。
// 5000 行的依据：一次批 ≈ 13 列 × 5000 行 ≈ 几百 KB 的临时对象，
// 够 amortize 掉一次往返，又不至于把"分批"退化成"一次全拿"。
const UsageLedgerExportPageSize = 5000

// usageLedgerExportCols 导出查询的列清单（与 Scan 顺序同契约；两处引用同一份）。
const usageLedgerExportCols = "id, tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, COALESCE(biz_kind,''), COALESCE(biz_mode,''), COALESCE(charge_kind,''), created_at"

// usageExportWhere 拼导出谓词（含客户面谓词与时间边界）。
// 时间口径不变：usage_ledger.created_at 是 UTC RFC3339（C22 写点约定），
// 故 from/to 按前缀字典序比较即可（from 补 00:00:00，to 补 23:59:59Z）。
// afterID>0 时追加 keyset 条件 `id<?`——它是**唯一一个**翻页条件，别再叠 OFFSET。
func usageExportWhere(tid int64, from, to string, chargedOnly bool, afterID int64) (string, []interface{}) {
	q := "WHERE tenant_id=?"
	args := []interface{}{tid}
	if chargedOnly {
		q += " AND " + CustomerUsagePred
	}
	if from != "" {
		q += " AND created_at>=?"
		args = append(args, from+"T00:00:00")
	}
	if to != "" {
		q += " AND created_at<=?"
		args = append(args, to+"T23:59:59Z")
	}
	if afterID > 0 {
		q += " AND id<?"
		args = append(args, afterID)
	}
	return q, args
}

// scanUsageLedgerRow 扫一行导出记录（单行坏数据跳过，与改造前同口径）。
func scanUsageLedgerRow(rows *sql.Rows) (*UsageLedger, bool) {
	var u UsageLedger
	if err := rows.Scan(&u.ID, &u.TenantID, &u.UserID, &u.TaskType, &u.Provider, &u.Model,
		&u.Quantity, &u.UnitPrice, &u.Cost, &u.BizKind, &u.BizMode, &u.ChargeKind, &u.CreatedAt); err != nil {
		return nil, true // 继续下一行
	}
	return &u, false
}

// WalkUsageLedgerForExport 分批取导出行，每批回调一次 onBatch。
// 参数：limit=总行数硬上限（<=0 或 >100000 收敛到 100000）；onBatch 返回错误即**中止**
// （CSV 写不下去＝连接断了，别再往下取——旧形态是先把 10 万行全取完再写，写失败时那 10 万行白烧）。
// 返回：已交给回调的行数（用于"导出了多少行"的出声读数）。
func (s *Store) WalkUsageLedgerForExport(tid int64, from, to string, limit int, chargedOnly bool,
	onBatch func([]*UsageLedger) error) (int, error) {
	if limit <= 0 || limit > 100000 {
		limit = 100000
	}
	var afterID int64
	sent := 0
	for sent < limit {
		want := UsageLedgerExportPageSize
		if limit-sent < want {
			want = limit - sent
		}
		q, args := usageExportWhere(tid, from, to, chargedOnly, afterID)
		q = "SELECT " + usageLedgerExportCols + " FROM usage_ledger " + q + " ORDER BY id DESC LIMIT ?"
		args = append(args, want)
		rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
		if err != nil {
			return sent, err
		}
		var batch []*UsageLedger
		var lastID int64
		for rows.Next() {
			u, skip := scanUsageLedgerRow(rows)
			if skip {
				continue
			}
			batch = append(batch, u)
			lastID = u.ID
		}
		rows.Close()
		if len(batch) == 0 {
			break // 取完了
		}
		if err := onBatch(batch); err != nil {
			return sent + len(batch), err
		}
		sent += len(batch)
		if lastID == 0 {
			break // 没有可用游标（理论上不可达：id 是主键），宁可少导也不要写死循环
		}
		afterID = lastID
		if len(batch) < want {
			break // 这批没装满 ⇒ 后面没有了
		}
	}
	return sent, nil
}

// UsageLedgerForExport 用量明细导出查询（★ P2 报表导出 2026-09-15）。
// ★ 2026-11-04 A8 起改为**薄委托**到 WalkUsageLedgerForExport：谓词与翻页只有一份实现，
//
//	出参形状（按 id 倒序的整段切片）与行数上限一字未动 ⇒ 现有调用方与单测零改动。
//	api 的 CSV 那条腿已直接走 Walk（不再把整段进内存）；本函数留给"必须要整段"的消费方。
func (s *Store) UsageLedgerForExport(tid int64, from, to string, limit int, chargedOnly bool) ([]*UsageLedger, error) {
	var out []*UsageLedger
	if _, err := s.WalkUsageLedgerForExport(tid, from, to, limit, chargedOnly, func(b []*UsageLedger) error {
		out = append(out, b...)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}
