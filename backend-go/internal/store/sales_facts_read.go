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
// ★ 四期 W3（KN11）：本读口支持「切读」——当注入的知识服务客户端 ServiceRead=on 时，
//
//	改走独立知识服务（:8791）取事实+译文；其余时候走主库直读（影子期回落，零风险过渡）。
//	切读后知识服务不可达 ⇒ 503 fail-closed，绝不回落成空结果/假装无判据。
//
// 所有 SQL 一律走 db.Query ＋ db.CurrentDialect()（db/guard_test.go 裸 SQL 闸），双方言兼容。
// ================================================
package store

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"translator/internal/db"
	"translator/internal/knowledge"
	"translator/internal/knowledge/contract"
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

// SetKnowledgeClient 注入独立知识服务客户端（:8791）。
// 仅当客户端 ServiceRead=on（切读/KN11）时，本读口改走知识服务；其余时候走主库直读（影子期回落）。
func (s *Store) SetKnowledgeClient(kc *knowledge.Client) { s.knowledgeClient = kc }

// ListSalesFactsForInjection 销售事实集准入读口（唯一下发出口，见文件头）。
// ★ 切读开关（knowledgeClient.ServiceRead=on）才走知识服务；否则主库直读（影子期回落，零风险过渡）。
func (s *Store) ListSalesFactsForInjection(tid int64, productID, answerLang string) ([]SalesFactForInjection, []ControlledFactKey, error) {
	lang := canonicalInjectionLang(answerLang)
	if lang == "" {
		lang = "zh"
	}
	if s.knowledgeClient != nil && s.knowledgeClient.ServiceRead {
		return s.knowledgeSalesFactsForInjection(tid, productID, lang)
	}
	return s.directSalesFactsForInjection(tid, productID, answerLang)
}

// directSalesFactsForInjection 主库直读实现（影子期/切读前源真值，零风险）。
func (s *Store) directSalesFactsForInjection(tid int64, productID, answerLang string) ([]SalesFactForInjection, []ControlledFactKey, error) {
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

// knowledgeSalesFactsForInjection 切读实现：走独立知识服务（:8791）取事实+译文，按 fact_id 匹配。
// 不可达/非 200 ⇒ 直接报错（503 fail-closed，绝不回落成空结果/绝不假装无判据）。
func (s *Store) knowledgeSalesFactsForInjection(tid int64, productID, lang string) ([]SalesFactForInjection, []ControlledFactKey, error) {
	ctx := context.Background()
	facts, o, err := s.knowledgeClient.FactsList(ctx, tid, productID)
	if err != nil || o.HTTPStatus != http.StatusOK {
		return nil, nil, fmt.Errorf("销售事实知识服务读取失败(status=%d): %w", o.HTTPStatus, err)
	}
	i18nResp, o2, err2 := s.knowledgeClient.I18nByTenantLang(ctx, tid, lang)
	if err2 != nil || o2.HTTPStatus != http.StatusOK {
		return nil, nil, fmt.Errorf("销售事实译文知识服务读取失败(status=%d): %w", o2.HTTPStatus, err2)
	}
	// ② 译文按 fact_id 建索引（与直读同口径：source_hash 比对放 Go 侧）。
	transByFact := make(map[int64]struct{ hash, value string }, len(i18nResp.Items))
	for _, it := range i18nResp.Items {
		transByFact[it.FactID] = struct{ hash, value string }{it.SourceHash, it.Value}
	}
	out := make([]SalesFactForInjection, 0)
	controlled := make([]ControlledFactKey, 0)
	for _, f := range facts {
		// ①+④ 准入判定与受控键判定（与直读同口径，避免两套过滤漂移）。
		admitted := f.Status == contract.FactStatusConfirmed &&
			f.Visibility == contract.VisibilityPublic && f.Locked == 0
		if !admitted {
			controlled = append(controlled, ControlledFactKey{Kind: f.Kind, Key: f.Key})
			continue
		}
		// ③ 译文存在且 source_hash 与当前原文一致才带值进集；缺/失配一律跳过。
		t, ok := transByFact[f.ID]
		if !ok || t.hash != factSourceHash(f.Value) || strings.TrimSpace(t.value) == "" {
			continue
		}
		out = append(out, SalesFactForInjection{Kind: f.Kind, Key: f.Key, Value: t.value, Lang: lang})
	}
	return out, controlled, nil
}
