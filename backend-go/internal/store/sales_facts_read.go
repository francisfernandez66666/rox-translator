// ============ sales_facts_read.go · 职责说明 ============
// 三期 W4：销售事实集**准入读口**（§0.4 第 1/2/5/6 条，断言 A13b）。
//
//   - ListSalesFactsForInjection(tid, productID, answerLang) 只返回「准入事实的已落库译文值」：
//     SQL 硬带 status='confirmed' AND visibility='public' AND locked=0 AND tenant_id=?，
//     按 priority DESC, id 排序（超限由 engine.LimitSalesFacts 保前丢后）。
//   - ★ 受控键（unverified/gated/locked 等非准入事实）只下发 kind+key，**绝不带 value**：
//     ControlledFactKey 结构物理上只有 Kind/Key 两个字段，与 engine.ControlledKey 类型层的
//     「无 Value 字段」结构性保证同源（A13b M0237：把 value 拼进清单必红）。
//   - ★ 缺译文 / source_hash 失配 ⇒ 该事实不进集（fail-closed），绝不跨语种放中文原文放行、
//     绝不热路径临时调 LLM（§0.4 第 5 条）。比对按 hash 做，不逐字比对译文措辞（第 6 条）。
//   - 现有 ListFacts/FactsByIDSet/ListPublicCorpus 都不过滤这三列，本读口是**唯一**准入出口，
//     不许复用它们拼一份「看起来一样」的过滤。
//
// ★ store 包不 import engine（成环）：返回值用 store 本地结构，由 api 层 buildSalesFactsSet
//
//	转成 engine.SalesFact / engine.ControlledKey 下发。
//
// 所有 SQL 一律走 db.Query ＋ db.CurrentDialect()（db/guard_test.go 裸 SQL 闸），双方言兼容。
// ================================================
package store

import (
	"fmt"
	"strings"

	"translator/internal/db"
)

// SalesFactForInjection 一条准入事实的已落库译文（store 本地形态，避免 import engine 成环）。
// Value 恒为 answerLang 的译文，不是中文原文。
type SalesFactForInjection struct {
	Kind  string
	Key   string
	Value string
	Lang  string
}

// ControlledFactKey 一条受控事实的主题键。★ 刻意只有 Kind/Key、没有 Value 字段：
// 受控值在类型层就进不了下发集，更进不了 prompt（与 §0.2 公开页 SEO 不变式同源）。
type ControlledFactKey struct {
	Kind string
	Key  string
}

// canonicalInjectionLang 归一语种码（小写＋下划线），与写时预生成同口径。
// 参数：lang=原始语种码；返回归一后的语种码。
func canonicalInjectionLang(lang string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(lang)), "-", "_")
}

// ListSalesFactsForInjection 取准入事实的已落库译文 + 受控键清单（事实集下发唯一读口）。
// 参数：tid=租户 ID；productID=产品主键（空串＝该租户全部产品，§0.4「按 tid+lang 取」）；
// answerLang=本轮作答语言码；返回准入译文列表、受控键清单、错误。
func (s *Store) ListSalesFactsForInjection(tid int64, productID, answerLang string) ([]SalesFactForInjection, []ControlledFactKey, error) {
	lang := canonicalInjectionLang(answerLang)
	if lang == "" {
		lang = "zh"
	}
	out := []SalesFactForInjection{}
	controlled := []ControlledFactKey{}
	d := db.CurrentDialect()

	// ① 准入事实（四条件硬带 tenant_id；空 productID 不加产品过滤）。
	//    ★ 按 priority 排序：超限时 engine.LimitSalesFacts 保前丢后，priority 高的先下发。
	factSQL := `SELECT id, kind, key, value FROM product_facts
		WHERE tenant_id=? AND status='confirmed' AND visibility='public' AND locked=0`
	factArgs := []interface{}{tid}
	if strings.TrimSpace(productID) != "" {
		factSQL += " AND product_id=?"
		factArgs = append(factArgs, strings.TrimSpace(productID))
	}
	factSQL += " ORDER BY priority DESC, id"
	frows, err := db.Query(s.db, d, factSQL, factArgs...)
	if err != nil {
		return nil, nil, fmt.Errorf("销售事实准入查询失败: %w", err)
	}
	type admitted struct {
		id               int64
		kind, key, value string
	}
	var facts []admitted
	for frows.Next() {
		var a admitted
		if e := frows.Scan(&a.id, &a.kind, &a.key, &a.value); e != nil {
			_ = frows.Close()
			return nil, nil, fmt.Errorf("销售事实准入行扫描失败: %w", e)
		}
		facts = append(facts, a)
	}
	_ = frows.Close()

	// ② 该语种全部已落库译文：map[fact_id] → (source_hash, value)。
	//    唯一键保证一 fact 一 lang 至多一行；hash 比对放 Go 侧（SQLite/PG 无内置 sha256 同函数）。
	trows, err := db.Query(s.db, d,
		"SELECT fact_id, source_hash, value FROM product_fact_i18n WHERE tenant_id=? AND lang=?",
		tid, lang)
	if err != nil {
		return nil, nil, fmt.Errorf("销售事实译文查询失败: %w", err)
	}
	type trans struct{ hash, value string }
	transByFact := map[int64]trans{}
	for trows.Next() {
		var fid int64
		var t trans
		if e := trows.Scan(&fid, &t.hash, &t.value); e != nil {
			_ = trows.Close()
			return nil, nil, fmt.Errorf("销售事实译文行扫描失败: %w", e)
		}
		transByFact[fid] = t
	}
	_ = trows.Close()

	// ③ 只对「译文存在且 source_hash 与当前原文一致」的事实带值进集；
	//    缺译文 / hash 失配一律跳过（该事实按「无判据」处理，守卫 fail-closed 回避）。
	for _, f := range facts {
		t, ok := transByFact[f.id]
		if !ok || t.hash != factSourceHash(f.value) || strings.TrimSpace(t.value) == "" {
			continue
		}
		out = append(out, SalesFactForInjection{
			Kind: f.kind, Key: f.key, Value: t.value, Lang: lang,
		})
	}

	// ④ 受控键：所有「非准入」事实（unverified / gated / locked / withdrawn 等）。
	//    ★ 只 SELECT kind, key —— value 一个字节都不查出来，绝无可能拼进清单。
	ctrlSQL := `SELECT kind, key FROM product_facts
		WHERE tenant_id=? AND (status<>'confirmed' OR visibility<>'public' OR locked<>0)`
	ctrlArgs := []interface{}{tid}
	if strings.TrimSpace(productID) != "" {
		ctrlSQL += " AND product_id=?"
		ctrlArgs = append(ctrlArgs, strings.TrimSpace(productID))
	}
	crows, err := db.Query(s.db, d, ctrlSQL, ctrlArgs...)
	if err != nil {
		return nil, nil, fmt.Errorf("销售受控键查询失败: %w", err)
	}
	defer crows.Close()
	for crows.Next() {
		var c ControlledFactKey
		if e := crows.Scan(&c.Kind, &c.Key); e != nil {
			return nil, nil, fmt.Errorf("销售受控键行扫描失败: %w", e)
		}
		controlled = append(controlled, c)
	}
	return out, controlled, crows.Err()
}
