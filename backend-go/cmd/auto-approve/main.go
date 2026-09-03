// ============ main.go · 职责说明 ============
// cmd/auto-approve 包一次性「采集待审数据自动清洗+审批+通过」回填工具。
//
// 背景（2026-09-02 流程改造）：待审批数据流程由「人工审核」改为「自动清洗/修正/审批并通过」，
// 人工只对已通过数据驳回/改正。本工具用于把历史已采集滞留待审池（pending）的数据一次性：
//   1. 按源文本脚本自动检测源语言（纠正英文源文本被误标 zh 等硬编码/误标问题，重算去重 hash）
//   2. 直接嵌入正式库（kb_entries/tm_segments + kb_safety_phrases）
//   3. 待审记录置 approved（留痕，供人工按「已通过」查看、驳回/改正）
//
// 幂等：已 approved/rejected 的跳过；SaveEntry 按唯一键判重（重复内容命中已有条目即更新不报错）。
// 操作前务必先 pg_dump 备份生产库。
//
// 用法：
//   auto-approve -dsn "postgres://user:pass@127.0.0.1:5432/langcross?sslmode=disable"
//   DB_DRIVER=postgres DB_DSN=postgres://... auto-approve
//   -dryrun 仅预览将纠正的源语言与将审批条数，不落库。
// =============================================
package main

import (
	"flag"
	"log"
	"os"

	_ "github.com/lib/pq"

	"translator/internal/config"
	"translator/internal/crawler"
	"translator/internal/db"
	"translator/internal/store"
)

// reconcileStagedHashes 规范化待审条目去重 hash（2026-09-03 数据修复）：
//   - stored hash 与【按当前字段重算】一致 → 跳过
//   - 不一致且存在其它行持有该正确 hash → stale 重复行，删除
//   - 不一致且无其它行持有正确 hash → 孤儿 hash，更正该行
//
// 背景：早期 AutoApproveEntry 沿用 stale in-memory hash，源语言更正行会插入重复 approved 行。
// 返回 (删除行数, 更正行数)。
func reconcileStagedHashes(st *store.Store) (int, int) {
	type entry = *store.KBStagedEntry
	// 第一遍：全量收集（id, storedHash, recomputedHash）
	rows := make([]entry, 0, 4096)
	for offset := 0; ; offset += 500 {
		items, err := st.ListStagedEntriesAll(500, offset)
		if err != nil {
			log.Printf("[reconcile] 读取失败: %v", err)
			break
		}
		if len(items) == 0 {
			break
		}
		rows = append(rows, items...)
		if len(items) < 500 {
			break
		}
	}
	// 收集各 recomputed hash 出现次数（判断是否有“持有正确 hash 的行”）
	hashCnt := map[string]int{}
	for _, e := range rows {
		hashCnt[e.SrcHash]++
	}
	del, upd := 0, 0
	for _, e := range rows {
		want := store.ComputeEntryHash(e.SrcLang, e.SrcText, e.TgtLang, e.TgtText)
		if e.SrcHash == want {
			continue
		}
		if hashCnt[want] > 0 {
			// stale 重复行：正确 hash 已有其它行持有 → 删
			if ok, _ := st.DeleteStagedEntry(e.ID); ok {
				del++
			}
		} else {
			// 孤儿 hash：无其它行持有正确 hash → 更正
			if ok, _ := st.UpdateStagedEntrySrcLang(e.ID, e.SrcLang); ok {
				upd++
				hashCnt[want] = 1
			}
		}
	}
	return del, upd
}

const pageSize = 500

func main() {
	dsn := flag.String("dsn", "", "目标 PostgreSQL DSN（缺省取环境变量 DB_DSN）")
	dry := flag.Bool("dryrun", false, "仅预览，不落库")
	flag.Parse()

	if *dsn == "" {
		*dsn = os.Getenv("DB_DSN")
	}
	if *dsn == "" {
		log.Fatal("缺少 PG DSN：请用 -dsn 或环境变量 DB_DSN")
	}

	prevC := config.C
	conn, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: *dsn})
	if err != nil {
		log.Fatalf("[auto-approve] 连接数据库失败: %v", err)
	}
	defer conn.Close()
	config.C = &config.Config{DatabaseDriver: "postgres"}
	defer func() { config.C = prevC }()

	st, err := store.New(conn)
	if err != nil {
		log.Fatalf("[auto-approve] 初始化 Store 失败: %v", err)
	}
	tid := int64(1) // 采集内容宿主租户1（平台共享行业/语言文化包）

	log.Printf("[auto-approve] 开始清洗+审批待审数据（dryrun=%v）", *dry)

	// ============ 0. hash 一致性修复（2026-09-03 加） ============
	// 早期版本 AutoApproveEntry 沿用 stale in-memory hash，会在源语言更正行上插入重复
	// approved 行（实测 15818→18841 +3023）。此 pass 把所有 stored hash 规范化：
	//   - stored 与【按当前字段重算】一致 → 跳过
	//   - 不一致且存在其它行持有正确 hash → stale 重复行，删除
	//   - 不一致且无其它行持有正确 hash → 孤儿 hash，直接更正该行
	if !*dry {
		if del, upd := reconcileStagedHashes(st); del > 0 || upd > 0 {
			log.Printf("[auto-approve] hash 修复：删除 stale 重复行 %d，更正孤儿 hash %d", del, upd)
		}
	}

	// ============ 待审条目 ============
	// 处理 pending（清洗+通过）与 approved 且源语言误标（回收→更正→重新嵌入）两类；
	// rejected 跳过（人工驳回不动）。
	var entryTotal, entryApplied, entryCleaned int
	for offset := 0; ; offset += pageSize {
		items, err := st.ListStagedEntriesAll(pageSize, offset)
		if err != nil {
			log.Fatalf("[auto-approve] 读取待审条目失败: %v", err)
		}
		if len(items) == 0 {
			break
		}
		for _, e := range items {
			if e.Status == "rejected" {
				continue
			}
			entryTotal++
			// 1. 源语言清洗：按源文本脚本纠正硬编码/误标的 zh
			det := crawler.DetectSourceLang(e.SrcText)
			needsReclaim := det != "" && det != e.SrcLang && e.Status == "approved"
			if det != "" && det != e.SrcLang {
				if *dry {
					entryCleaned++
					log.Printf("[dry] 条目 #%d 源语言 %s → %s（%q, status=%s）", e.ID, e.SrcLang, det, e.SrcText, e.Status)
					continue
				}
				// 已通过且误标：先回收正式库旧的（错误源语言）嵌入，再改 staged 后重新嵌入
				if needsReclaim && e.TargetPackID > 0 {
					if derr := st.DeleteAppliedEntry(tid, e.TargetPackID, e.SrcLang, e.SrcText, e.TgtLang); derr != nil {
						log.Printf("[auto-approve] 条目 #%d 旧嵌入回收失败: %v", e.ID, derr)
					}
				}
				if ok, uerr := st.UpdateStagedEntrySrcLang(e.ID, det); ok {
					e.SrcLang = det
					entryCleaned++
				} else if uerr != nil {
					log.Printf("[auto-approve] 条目 #%d 源语言更正失败: %v", e.ID, uerr)
				}
			}
			if *dry {
				continue
			}
			// 2. 嵌入正式库（重新嵌入已回收的、或首次嵌入 pending 的）
			if err := st.AutoApproveEntry(tid, e); err != nil {
				log.Printf("[auto-approve] 条目 #%d 审批落库失败: %v", e.ID, err)
				continue
			}
			entryApplied++
		}
		if len(items) < pageSize {
			break
		}
	}

	// ============ 待审安全句 ============
	var phTotal, phApplied int
	for offset := 0; ; offset += pageSize {
		items, err := st.ListStagedPhrasesAll(pageSize, offset)
		if err != nil {
			log.Fatalf("[auto-approve] 读取待审安全句失败: %v", err)
		}
		if len(items) == 0 {
			break
		}
		for _, p := range items {
			if p.Status != "pending" {
				continue
			}
			phTotal++
			if *dry {
				continue
			}
			if err := st.AutoApprovePhrase(tid, p); err != nil {
				log.Printf("[auto-approve] 安全句 #%d 审批落库失败: %v", p.ID, err)
				continue
			}
			phApplied++
		}
		if len(items) < pageSize {
			break
		}
	}

	if *dry {
		log.Printf("[auto-approve] 预览：待审条目 %d（将更正源语言 %d），待审安全句 %d。无写入。", entryTotal, entryCleaned, phTotal)
		return
	}
	log.Printf("[auto-approve] 完成：条目已审批 %d/%d（更正源语言 %d），安全句已审批 %d/%d",
		entryApplied, entryTotal, entryCleaned, phApplied, phTotal)
}