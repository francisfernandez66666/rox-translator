// ============ segments.go · 职责说明 ============
// store 包「逐段对照真值表」数据层（ticket_segments）。
//
// 与 edits.go 的分工：
//   - translation_edits：人**改了什么**（edited_text/status/note），按 seg_index 挂在对照段上；
//   - ticket_segments ：系统当初**把哪段译成了哪段**（源文→译文权威配对），是前者的对齐基准。
//
// 为什么需要本表（2026-09-18）：PDF 文件工单的源文与译本是**两次独立**的 pdf2docx 转换产物，
// 段落切分粒度必然不同（实测同一工单源 504 段 / 译文 578 段）。旧实现按下标 min() 硬对齐，
// 抽查 20 对全部错位（源「新车上市当天官网多语齐发」↔ 译「Method B: Integrate into Skills
// for calling via Lark」），前端双栏编辑器因此显示成「大量块不匹配」。翻译当时手上本就有
// texts[i] ↔ translations[texts[i]] 的精确配对，落这张表即可让对照端直接读真值。
//
// 本表是**附加**数据：缺失时调用方回退旧口径，不影响任何既有功能。
// 前端接口形状（EditorSegment）不变，故前端无感知。
//
// seg_index 的口径（易被误读）：它是「引擎提取顺序的下标」，不是 PDF/DOCX 的物理段落号——
// 提取侧按内容去重（Go 侧 Extractor.add 与 pdf_overlay.py cmd_extract 各有 seen 集合），
// 同一句在文件里出现 3 次只占 1 个段序（相同片段只翻一次）。因此本表的段数通常**少于**
// 按段落解析出的数量，不要拿它与产物文件的段落数直接对比，也不要期望逐物理段落还原。
// =============================================
package store

import (
	"time"

	"translator/internal/db"
)

// TicketSegment 逐段对照真值（源文段 → 该语言译文段）。
type TicketSegment struct {
	ID       int64  `json:"id"`
	TenantID int64  `json:"tenant_id"`
	TicketID int64  `json:"ticket_id"`
	FilePath string `json:"file_path"` // 该段所属源文件（多文件工单区分用）
	Lang     string `json:"lang"`
	SegIndex int    `json:"seg_index"` // 该文件提取顺序下标（与源文一侧一致）
	Source   string `json:"source"`    // 源文段落
	Target   string `json:"target"`    // 该段译文（未译出为空串）
	// 不映射 created_at/updated_at：本表只整批覆盖写、不参与展示与排序，
	// 时间列仅供排障时人工查库看「这一轮是什么时候落的」。
}

// SaveTicketSegments 覆盖式写入某工单某文件某语言的逐段对照真值（重跑幂等）。
// 参数：tenantID=租户；ticketID=工单；filePath=源文件路径；lang=目标语言；segs=按提取顺序的段列表。
// 语义：先删该 (ticket_id, file_path, lang) 的旧行再批量插入——重跑同一工单时不会残留上一轮
// 的旧段（否则段落数变少会留下尾部孤儿行，对照里表现为「多出一截不存在的段」）。
// 返回：错误（事务内任一步失败即整体回滚，不会留下「删了但没插」的半成品状态）。
func (s *Store) SaveTicketSegments(tenantID, ticketID int64, filePath, lang string, segs []TicketSegment) error {
	d := db.CurrentDialect()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // Commit 成功后 Rollback 是无害的 no-op

	if _, err := db.Exec(tx, d,
		`DELETE FROM ticket_segments WHERE ticket_id=? AND file_path=? AND lang=?`,
		ticketID, filePath, lang); err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	// 逐行 Exec 而非拼一条多值 INSERT：占位符方言差异全交给 db.Exec 处理（PG 下 ? → $n），
	// 少一处手工拼 SQL 的踩坑面；单文件段数量级为数百，且整批在同一事务内，开销可忽略。
	for _, sg := range segs {
		if _, err := db.Exec(tx, d, `INSERT INTO ticket_segments
			(tenant_id, ticket_id, file_path, lang, seg_index, source_text, target_text, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tenantID, ticketID, filePath, lang, sg.SegIndex, sg.Source, sg.Target, now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetTicketSegments 读取某工单某文件的逐段对照真值（按段序号升序）。
// 参数：ticketID=工单；filePath=源文件路径（空串=不限文件，用于调用方只关心单文件的场景）；
//
//	lang=目标语言。
//
// 返回：段列表；无记录时返回 nil, nil（调用方据此回退旧口径，不要把「无数据」当错误）。
func (s *Store) GetTicketSegments(ticketID int64, filePath, lang string) ([]TicketSegment, error) {
	// COALESCE 兜 NULL：列取值不裸读是 AGENTS §4 记的高频踩坑点（切流导入/手工 DDL 的
	// 存量库里 file_path 可能为 NULL，直接 Scan 进 string 会整查询报错）。
	q := `SELECT id, tenant_id, ticket_id, COALESCE(file_path,''), lang, seg_index, source_text, target_text
		 FROM ticket_segments WHERE ticket_id=? AND lang=?`
	args := []interface{}{ticketID, lang}
	if filePath != "" {
		q += ` AND file_path=?`
		args = append(args, filePath)
	}
	q += ` ORDER BY seg_index ASC`
	// ★ F-44（〇-U 批）已修：原注释记录的方言待办在此收口——`?`→`$n` 的改写只在
	// db.Query 包装器内做（见 db/query.go RewritePlaceholders），旧写法直调
	// s.db.Query 使本查询在生产（PostgreSQL）恒报语法错，而调用方把报错当「未命中」
	// 静默回退旧口径 ⇒「块不匹配」修复在 PG 上不显效、只在 SQLite/CI 上绿。
	// 现在 SQL 串仍以 SQLite 方言为唯一真源，方言差异交给包装器。
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TicketSegment
	for rows.Next() {
		var g TicketSegment
		if err := rows.Scan(&g.ID, &g.TenantID, &g.TicketID, &g.FilePath, &g.Lang, &g.SegIndex,
			&g.Source, &g.Target); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
