// ============ handlers.go · 职责说明 ============
// 四期 W3（§4.3.4）：17 契约路径的真实现（真 SQL，无桩、无假装成功）。
//
//   - i18n/list / i18n/upsert：事实译文现状读取与回写（零模型调用，细节在 store 子包）。
//   - entries/query：W3 期未接管 ⇒ 503 + reason=not_owned_until_w5，**不给空结果**。
//   - 其余读口：QueryScoped 真查库，硬带 tenant_id=?（KN3 fail-closed）。
//   - 写口 / 删除：白名单列 + 参数化显式 id upsert / 带 tenant_id 的 DELETE（不带尾随 LIMIT）。
//   - answer：主服务给素材包，知识侧回同一结构，不新造字段、不重算 gated_count。
//
// ================================================
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/db"
	"translator/internal/knowledge/contract"
	knowledgeStore "translator/internal/knowledge/store"
)

// handleHealth 健康面：ok + version + contract_version（不鉴权，供主服务探针）。
// 三态（ok/degraded/unavailable）是主服务探针的读数，由 contract.ClassifyHealth 判定。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, contract.HealthResponse{
		Status:          contract.HealthOK,
		Version:         "knowledge-w3",
		ContractVersion: contract.ContractVersion,
	})
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

func decodeScope(r *http.Request) (map[string]any, int64, error) {
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		return nil, 0, err
	}
	return m, toInt64(m["tenant_id"]), nil
}

func strField(m map[string]any, k string) string {
	if s, ok := m[k].(string); ok {
		return s
	}
	return ""
}

// hI18nList 真 SQL：回 12 语种每语种现状（缺的 present=false）。
func (s *Server) hI18nList(w http.ResponseWriter, r *http.Request) {
	var req contract.I18nListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if req.TenantID <= 0 {
		writeErr(w, http.StatusBadRequest, "tenant_scope_required", "")
		return
	}
	items, err := knowledgeStore.ListFactI18n(s.ks.DB(), s.ks.Dialect, req.TenantID, req.FactID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "i18n_list_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, contract.I18nListResponse{Items: items})
}

// hI18nByTenantLang 真 SQL：回该租户该语种全部事实译文（带 fact_id），W1 热路径一次取齐。
func (s *Server) hI18nByTenantLang(w http.ResponseWriter, r *http.Request) {
	var req contract.I18nByTenantLangRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if req.TenantID <= 0 || strings.TrimSpace(req.Lang) == "" {
		writeErr(w, http.StatusBadRequest, "tenant_scope_required", "")
		return
	}
	items, err := knowledgeStore.ListFactI18nByTenantLang(s.ks.DB(), s.ks.Dialect, req.TenantID, req.Lang)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "i18n_by_tenant_lang_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, contract.I18nByTenantLangResponse{Items: items})
}

// hI18nUpsert 真 SQL：幂等回写，人工档优先（细节在 store 子包）。
func (s *Server) hI18nUpsert(w http.ResponseWriter, r *http.Request) {
	var req contract.I18nUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if req.TenantID <= 0 {
		writeErr(w, http.StatusBadRequest, "tenant_scope_required", "")
		return
	}
	if err := knowledgeStore.UpsertFactI18n(s.ks.DB(), s.ks.Dialect, req); err != nil {
		writeErr(w, http.StatusInternalServerError, "i18n_upsert_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, contract.I18nUpsertResponse{OK: true})
}

// hEntriesQuery W3 期未接管：503 + reason，绝不返回空结果（静默降级族 KN4 腿③）。
func (s *Server) hEntriesQuery(w http.ResponseWriter, r *http.Request) {
	_, tid, err := decodeScope(r)
	if err != nil || tid <= 0 {
		writeErr(w, http.StatusBadRequest, "tenant_scope_required", "")
		return
	}
	writeErr(w, http.StatusServiceUnavailable, "contract_not_owned", contract.ReasonNotOwnedUntilW5)
}

// hAnswer 零模型问答：主服务给素材包，知识侧回同一结构（不新造字段、不重算 gated_count）；
// 出栈纪律留主服务。真碰库一次（读该租户事实行数）以证端点不是空壳，读数不进回包。
func (s *Server) hAnswer(w http.ResponseWriter, r *http.Request) {
	var req contract.AnswerPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if req.TenantID <= 0 {
		writeErr(w, http.StatusBadRequest, "tenant_scope_required", "")
		return
	}
	if _, err := knowledgeStore.ScopedCount(s.ks.DB(), s.ks.Dialect, contract.TableProductFacts, req.TenantID); err != nil {
		writeErr(w, http.StatusInternalServerError, "answer_read_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// readPlan 把契约路径映到（表、可信常量过滤片段、参数）。片段是代码内常量，值一律参数化。
func readPlan(path string, m map[string]any) (string, string, []interface{}) {
	switch path {
	case contract.PathProductsList, contract.PathSitemapList:
		return contract.TableProducts, "", nil
	case contract.PathProductsGet, contract.PathProductResolve:
		if id := strField(m, "product_id"); id != "" {
			return contract.TableProducts, " AND id=?", []interface{}{id}
		}
		if tc, pc := strField(m, "tenant_code"), strField(m, "page_code"); tc != "" && pc != "" {
			return contract.TableProducts, " AND tenant_code=? AND page_code=?", []interface{}{tc, pc}
		}
		return contract.TableProducts, "", nil
	case contract.PathFactsList, contract.PathFactsAdmitted:
		extra, args := "", []interface{}{}
		if pid := strField(m, "product_id"); pid != "" {
			extra += " AND product_id=?"
			args = append(args, pid)
		}
		if path == contract.PathFactsAdmitted {
			extra += " AND status=? AND visibility=? AND locked=?"
			args = append(args, contract.FactStatusConfirmed, contract.VisibilityPublic, 0)
		}
		return contract.TableProductFacts, extra, args
	case contract.PathAssetsList:
		if pid := strField(m, "product_id"); pid != "" {
			return contract.TableProductAssets, " AND product_id=?", []interface{}{pid}
		}
		return contract.TableProductAssets, "", nil
	case contract.PathCorpusPublic:
		return contract.TableProductFacts, " AND visibility=?", []interface{}{contract.VisibilityPublic}
	case contract.PathReadiness:
		return contract.TableProductAssets, "", nil
	}
	return "", "", nil
}

// hRead 通用真 SQL 读口：所有查询硬带 tenant_id=?（KN3 fail-closed）。
func (s *Server) hRead(w http.ResponseWriter, r *http.Request, path string) {
	m, tid, err := decodeScope(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if tid <= 0 {
		writeErr(w, http.StatusBadRequest, "tenant_scope_required", "")
		return
	}
	table, extra, args := readPlan(path, m)
	if table == "" {
		writeErr(w, http.StatusNotFound, "unknown_path", "")
		return
	}
	rows, err := knowledgeStore.QueryScoped(s.ks.DB(), s.ks.Dialect, table, tid, extra, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "total": len(rows)})
}

// hWrite products/upsert、products/delete、facts/upsert 的真 SQL 写口（W3 期显式 id）。
func (s *Server) hWrite(w http.ResponseWriter, r *http.Request, path string) {
	m, tid, err := decodeScope(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if tid <= 0 {
		writeErr(w, http.StatusBadRequest, "tenant_scope_required", "")
		return
	}
	table := contract.TableProducts
	if path == contract.PathFactsUpsert {
		table = contract.TableProductFacts
	}
	if path == contract.PathProductsDelete {
		id := strField(m, "id")
		if id == "" {
			writeErr(w, http.StatusBadRequest, "explicit_id_required", "")
			return
		}
		res, err := db.Exec(s.ks.DB(), s.ks.Dialect,
			"DELETE FROM "+table+" WHERE tenant_id=? AND id=?", tid, id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "delete_failed", "")
			return
		}
		n, _ := res.RowsAffected()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": n})
		return
	}
	if err := s.upsertRow(table, tid, m); err != nil {
		writeErr(w, http.StatusInternalServerError, "upsert_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// upsertRow 白名单列 + 参数化的显式 id 幂等 upsert（表名/列名来自 contract 固定清单，值参数化 ⇒ 无注入）。
func (s *Server) upsertRow(table string, tid int64, m map[string]any) error {
	pk := contract.PrimaryKey(table)
	if strField(m, pk) == "" && toInt64(m[pk]) <= 0 {
		return fmt.Errorf("显式 %s 为必填（W3 期主服务签发产品 id，知识侧不代生成）", pk)
	}
	cols := make([]string, 0)
	vals := make([]interface{}, 0)
	placeholders := make([]string, 0)
	for _, c := range contract.ColumnsFor(table) {
		v, ok := m[c]
		if !ok {
			continue
		}
		if c == "tenant_id" {
			v = tid // 强制以作用域为准，不信任 body 里的租户号
		}
		cols = append(cols, c)
		vals = append(vals, v)
		placeholders = append(placeholders, "?")
	}
	setClauses := make([]string, 0, len(cols))
	for _, c := range cols {
		if c != pk {
			setClauses = append(setClauses, c+"=excluded."+c)
		}
	}
	if len(setClauses) == 0 {
		return fmt.Errorf("无可更新列")
	}
	q := "INSERT INTO " + table + " (" + strings.Join(cols, ",") + ") VALUES (" +
		strings.Join(placeholders, ",") + ") ON CONFLICT(" + pk + ") DO UPDATE SET " +
		strings.Join(setClauses, ",")
	_, err := db.Exec(s.ks.DB(), s.ks.Dialect, q, vals...)
	return err
}
