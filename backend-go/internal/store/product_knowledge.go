// ============ product_knowledge.go · 职责说明 ============
// 四期 W3-5 / KN11：主服务 store 产品域「委托知识服务」的桥接与转换层。
//
//   - 所有产品/事实/物料的读与写，在 KNOWLEDGE_SERVICE_READ=on（切读）时改走 knowledge.Client；
//     切读前（影子期）仍走主库 SQL（s.knowledgeClient==nil 或 ServiceRead=false），零风险过渡，
//     且构成回滚路径（翻回 off＋重建主库表即可）。
//   - 知识服务端 hRead 返回「列名键 map 数组」，本文件负责把 map 转回本仓 store 类型，
//     并正确还原库内非 JSON 标量：products.target_langs 存 CSV、product_assets.fact_refs 存 JSON。
//   - 写口由调用方把 store 类型摊平成「列名键 map」交给客户端（事实/物料的 id 空缺时由服务端自增/自然键兜底）。
//
// ★ 双路径是硬编码的 if/else：切读开关在 knowledge.Client.ServiceRead，不新增配置维度。
// ================================================
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"translator/internal/knowledge"
	"translator/internal/knowledge/contract"
)

// kcCutover 返回知识客户端（仅当已注入且处于切读态）；否则返回 nil，调用方回落主库 SQL。
func (s *Store) kcCutover() *knowledge.Client {
	if s.knowledgeClient != nil && s.knowledgeClient.ServiceRead {
		return s.knowledgeClient
	}
	return nil
}

// ---- 列名键 map → store 类型（还原 CSV / JSON 形态）----

func rawStr(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "1"
		}
		return "0"
	}
	return ""
}

func rawInt64(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	}
	return 0
}

func rawInt(m map[string]any, k string) int { return int(rawInt64(m, k)) }

func splitTargetLangs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinTargetLangs(langs []string) string {
	if len(langs) == 0 {
		return ""
	}
	return strings.Join(langs, ",")
}

func parseFactRefsFromAny(v any) []int64 {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return []int64{}
	}
	var ids []int64
	if err := json.Unmarshal([]byte(s), &ids); err != nil || ids == nil {
		return []int64{}
	}
	return ids
}

// rawToProduct 列名键 map → store.Product（target_langs 由 CSV 还原为数组）。
func rawToProduct(m map[string]any) Product {
	return Product{
		ID:             rawStr(m, "id"),
		TenantID:       rawInt64(m, "tenant_id"),
		UserID:         rawInt64(m, "user_id"),
		Code:           rawStr(m, "code"),
		Name:           rawStr(m, "name"),
		Tagline:        rawStr(m, "tagline"),
		IndustryCode:   rawStr(m, "industry_code"),
		Audience:       rawStr(m, "audience"),
		UseCase:        rawStr(m, "use_case"),
		StyleNotes:     rawStr(m, "style_notes"),
		BannedNotes:    rawStr(m, "banned_notes"),
		Status:         rawStr(m, "status"),
		Slug:           rawStr(m, "slug"),
		TenantCode:     rawStr(m, "tenant_code"),
		PageCode:       rawStr(m, "page_code"),
		SeoIndexable:   rawInt(m, "seo_indexable"),
		TargetLangs:    joinTargetLangs(nil), // 由 TargetLangsList 出栈，库内 CSV 单独处理
		TargetLangsList: splitTargetLangs(rawStr(m, "target_langs")),
		CreatedAt:      rawStr(m, "created_at"),
		UpdatedAt:      rawStr(m, "updated_at"),
	}
}

// rawToFact 列名键 map → store.ProductFact（纯标量）。
func rawToFact(m map[string]any) ProductFact {
	return ProductFact{
		ID:         rawInt64(m, "id"),
		TenantID:   rawInt64(m, "tenant_id"),
		ProductID:  rawStr(m, "product_id"),
		Kind:       rawStr(m, "kind"),
		Key:        rawStr(m, "key"),
		Value:      rawStr(m, "value"),
		Status:     rawStr(m, "status"),
		Visibility: rawStr(m, "visibility"),
		Source:     rawStr(m, "source"),
		Locked:     rawInt(m, "locked"),
		CreatedAt:  rawStr(m, "created_at"),
		UpdatedAt:  rawStr(m, "updated_at"),
	}
}

// rawToAsset 列名键 map → store.ProductAsset（fact_refs 由 JSON 还原）。
func rawToAsset(m map[string]any) ProductAsset {
	return ProductAsset{
		ID:           rawInt64(m, "id"),
		TenantID:     rawInt64(m, "tenant_id"),
		ProductID:    rawStr(m, "product_id"),
		AssetType:    rawStr(m, "asset_type"),
		Lang:         rawStr(m, "lang"),
		Body:         rawStr(m, "body"),
		ArtifactRef:  rawStr(m, "artifact_ref"),
		ReviewStatus: rawStr(m, "review_status"),
		Validity:     rawStr(m, "validity"),
		Visibility:   rawStr(m, "visibility"),
		FactRefs:     parseFactRefsFromAny(m["fact_refs"]),
		SourceKind:   rawStr(m, "source_kind"),
		Version:      rawInt(m, "version"),
		CreatedAt:    rawStr(m, "created_at"),
		UpdatedAt:    rawStr(m, "updated_at"),
	}
}

// productToRow store.Product → 写口列名键 map（target_langs 摊平成 CSV）。
func productToRow(p Product, targetLangs []string) map[string]any {
	if targetLangs == nil {
		targetLangs = p.TargetLangsList
	}
	tl := joinTargetLangs(targetLangs)
	if tl == "" {
		tl = joinTargetLangs(p.TargetLangsList)
	}
	return map[string]any{
		"id":             p.ID,
		"tenant_id":      p.TenantID,
		"user_id":        p.UserID,
		"code":           p.Code,
		"name":           p.Name,
		"tagline":        p.Tagline,
		"industry_code":  p.IndustryCode,
		"audience":       p.Audience,
		"use_case":       p.UseCase,
		"style_notes":    p.StyleNotes,
		"banned_notes":   p.BannedNotes,
		"status":         p.Status,
		"slug":           p.Slug,
		"tenant_code":    p.TenantCode,
		"page_code":      p.PageCode,
		"seo_indexable":  p.SeoIndexable,
		"target_langs":   tl,
		"created_at":     p.CreatedAt,
		"updated_at":     p.UpdatedAt,
	}
}

// factToRow store.ProductFact → 写口列名键 map。
func factToRow(f ProductFact) map[string]any {
	return map[string]any{
		"id":         f.ID,
		"tenant_id":  f.TenantID,
		"product_id": f.ProductID,
		"kind":       f.Kind,
		"key":        f.Key,
		"value":      f.Value,
		"status":     f.Status,
		"visibility": f.Visibility,
		"source":     f.Source,
		"locked":     f.Locked,
		"created_at": f.CreatedAt,
		"updated_at": f.UpdatedAt,
	}
}

// assetToRow store.ProductAsset → 写口列名键 map（fact_refs 摊平为 JSON 串）。
func assetToRow(a ProductAsset) map[string]any {
	refs := a.FactRefs
	if refs == nil {
		refs = []int64{}
	}
	refsJSON, _ := json.Marshal(refs)
	return map[string]any{
		"id":            a.ID,
		"tenant_id":     a.TenantID,
		"product_id":    a.ProductID,
		"asset_type":    a.AssetType,
		"lang":          a.Lang,
		"body":          a.Body,
		"artifact_ref":  a.ArtifactRef,
		"review_status": a.ReviewStatus,
		"validity":      a.Validity,
		"visibility":    a.Visibility,
		"fact_refs":     string(refsJSON),
		"source_kind":   a.SourceKind,
		"version":       a.Version,
		"created_at":    a.CreatedAt,
		"updated_at":    a.UpdatedAt,
	}
}

// gatherProduct 从读口返回的行集里取第一条产品（resolve/get 用）。
func gatherProduct(items []map[string]any) (Product, bool) {
	if len(items) == 0 {
		return Product{}, false
	}
	return rawToProduct(items[0]), true
}

// deadlineNow UTC RFC3339 时间戳（写口 updated_at / created_at 统一用）。
func deadlineNow() string { return time.Now().UTC().Format(time.RFC3339) }

// kcCtx 取客户端调用上下文；store 方法无 ctx 时用 Background（与既有非 ctx 调用一致）。
func kcCtx(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

// errKnowledgeUnavailable 知识服务不可达（切读后 fail-closed）：store 方法据此向 API 层返回 503。
var errKnowledgeUnavailable = errors.New("知识服务不可达")

// kcOutcomeErr 把四档 Outcome 翻译成 store 层错误：切读后不可达（503）⇒ 显式错误，
// 绝不让调用方把"服务挂了"误当成"业务空"；其余（200/业务空）⇒ nil。
func kcOutcomeErr(o knowledge.Outcome) error {
	if o.HTTPStatus == http.StatusServiceUnavailable {
		return errKnowledgeUnavailable
	}
	return nil
}

// kcRespID 从写口响应里取受影响行主键（事实/物料为自增整型，JSON 解码为 float64）。
func kcRespID(raw map[string]any) int64 { return rawInt64(raw, "id") }

// factRowToFact 契约 FactRow → store.ProductFact（库内 lang/priority 不在 store 结构里，忽略）。
func factRowToFact(f contract.FactRow) ProductFact {
	return ProductFact{
		ID:         f.ID,
		TenantID:   f.TenantID,
		ProductID:  f.ProductID,
		Kind:       f.Kind,
		Key:        f.Key,
		Value:      f.Value,
		Status:     f.Status,
		Visibility: f.Visibility,
		Source:     f.Source,
		Locked:     f.Locked,
		CreatedAt:  f.CreatedAt,
		UpdatedAt:  f.UpdatedAt,
	}
}

// kcProducts rawToProduct 批量。
func kcProducts(items []map[string]any) []Product {
	out := make([]Product, 0, len(items))
	for _, it := range items {
		out = append(out, rawToProduct(it))
	}
	return out
}

// kcAssets rawToAsset 批量。
func kcAssets(items []map[string]any) []ProductAsset {
	out := make([]ProductAsset, 0, len(items))
	for _, it := range items {
		out = append(out, rawToAsset(it))
	}
	return out
}

// kcFactByNaturalKey 从读口返回的事实行集里按 (product_id, kind, key) 取事实（upsert 前判既存）。
// 参数：rows=事实行集；productID/kind/key=自然键；返回事实与是否命中。
func kcFactByNaturalKey(rows []contract.FactRow, productID, kind, key string) (ProductFact, bool) {
	for _, f := range rows {
		if f.ProductID == productID && f.Kind == kind && f.Key == key {
			return factRowToFact(f), true
		}
	}
	return ProductFact{}, false
}

// inInt64Set 判 id 是否在集合内（FactsByIDSet 知识分支过滤用）。
func inInt64Set(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// upsertFactKnowledge KN11 切读实现：事实项幂等写（自然键 upsert；保"空值＝沿用现值"语义）。
// 参数：tid=租户 ID，in=事实内容，kind/key=规范化后的类型与键，vis/source=经校验与 cert 闸后的缺省值，now=时间戳。
// 返回落库后的事实行与错误。
func (s *Store) upsertFactKnowledge(tid int64, in ProductFactInput, kind, key, vis, source, now string) (ProductFact, error) {
	kc := s.kcCutover()
	if kc == nil {
		return ProductFact{}, errKnowledgeUnavailable
	}
	rows, o, err := kc.FactsList(kcCtx(nil), tid, in.ProductID)
	if err != nil {
		return ProductFact{}, err
	}
	if e := kcOutcomeErr(o); e != nil {
		return ProductFact{}, e
	}
	existing, found := kcFactByNaturalKey(rows, in.ProductID, kind, key)
	finalVis := vis
	finalSource := source
	status := FactStatusUnverified
	if found {
		if existing.Value != in.Value {
			status = FactStatusUnverified
		} else {
			status = existing.Status
		}
		// ★ 空值＝"没表态"：沿用库里现值（与直读同口径，防"改个数⇒该行被静默收起"）。
		if strings.TrimSpace(in.Visibility) == "" {
			finalVis = existing.Visibility
		}
		if strings.TrimSpace(in.Source) == "" {
			finalSource = existing.Source
		}
	}
	row := map[string]any{
		"product_id": in.ProductID,
		"kind":       kind,
		"key":        key,
		"value":      in.Value,
		"status":     status,
		"visibility": finalVis,
		"source":     finalSource,
		"updated_at": now,
	}
	locked := 0
	if found {
		row["id"] = existing.ID // 带 id ⇒ 服务端 ON CONFLICT(id) 改既存行（不碰 created_at）
		locked = existing.Locked
	} else {
		row["created_at"] = now // 新行 ⇒ 服务端自然键 insert
	}
	row["locked"] = locked
	resp, o2, err := kc.FactUpsert(kcCtx(nil), row)
	if err != nil {
		return ProductFact{}, err
	}
	if e := kcOutcomeErr(o2); e != nil {
		return ProductFact{}, e
	}
	newID := kcRespID(resp)
	if !found {
		return ProductFact{
			ID: newID, TenantID: tid, ProductID: in.ProductID, Kind: kind, Key: key,
			Value: in.Value, Status: status, Visibility: finalVis, Source: finalSource,
			Locked: 0, CreatedAt: now, UpdatedAt: now,
		}, nil
	}
	ef := existing
	ef.Value = in.Value
	ef.Status = status
	ef.Visibility = finalVis
	ef.Source = finalSource
	ef.Locked = locked
	ef.UpdatedAt = now
	ef.ID = newID
	if existing.Value != in.Value {
		if _, eerr := s.ExpireAssetsByFactChange(tid, existing.ID, in.ProductID); eerr != nil {
			return ProductFact{}, fmt.Errorf("事实变更作废物料失败: %w", eerr)
		}
	}
	return ef, nil
}
