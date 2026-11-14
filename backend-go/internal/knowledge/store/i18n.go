// ============ i18n.go · 职责说明 ============
// 四期 W3（§4.3.3 第 3 条 / §4.3.4）：知识服务只提供 i18n/list 与 i18n/upsert 两个口。
//
//   - 零模型调用、零翻译腿接口、零跨域 import（KN13）：译文预生成留在主服务 translation 域。
//   - i18n/list：回 12 语种每语种 engine / source_hash / 有无，供主服务「只翻缺的 + 非 manual + 哈希变了」。
//   - i18n/upsert：幂等回写，且运营手工档（engine=manual）永不被非 manual 覆盖
//     （§4.3.3 第 3 条「人工终稿优先」）。
//   - 两端都硬带 tenant_id（KN3 fail-closed）。
//
// ================================================
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/knowledge/contract"
)

// ListFactI18n 返回 12 语种译文现状（缺的 present=false，engine/source_hash 为空）。
// tid<=0 或 factID<=0 ⇒ fail-closed 返回「全缺」，绝不回退成读全表。
func ListFactI18n(conn *sql.DB, d db.Dialect, tid, factID int64) ([]contract.I18nItem, error) {
	found := map[string]contract.I18nItem{}
	if tid > 0 && factID > 0 {
		q := `SELECT lang, engine, source_hash FROM product_fact_i18n WHERE tenant_id=? AND fact_id=?`
		rows, err := db.Query(conn, d, q, tid, factID)
		if err != nil {
			return nil, fmt.Errorf("knowledge: i18n/list 查询失败: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var it contract.I18nItem
			if err := rows.Scan(&it.Lang, &it.Engine, &it.SourceHash); err != nil {
				return nil, fmt.Errorf("knowledge: i18n/list 行扫描失败: %w", err)
			}
			it.Present = true
			found[it.Lang] = it
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("knowledge: i18n/list 行迭代失败: %w", err)
		}
	}
	items := make([]contract.I18nItem, 0, len(contract.I18nLangs))
	for _, lang := range contract.I18nLangs {
		it, ok := found[lang]
		if !ok {
			it = contract.I18nItem{Lang: lang}
		}
		it.Present = ok
		items = append(items, it)
	}
	return items, nil
}

// ListFactI18nByTenantLang 回该租户该语种全部事实的译文（带 fact_id），
// 供主服务 W1 热路径「一次取齐、按 fact_id 匹配」（避免逐 fact 调 i18n/list 的 N 次往返）。
// tid<=0 或 lang 为空 ⇒ fail-closed 返回空切片，绝不回退成「lang 空查全表」。
func ListFactI18nByTenantLang(conn *sql.DB, d db.Dialect, tid int64, lang string) ([]contract.I18nByTenantLangItem, error) {
	if tid <= 0 || strings.TrimSpace(lang) == "" {
		return []contract.I18nByTenantLangItem{}, nil
	}
	q := `SELECT fact_id, lang, value, source_hash, engine FROM product_fact_i18n WHERE tenant_id=? AND lang=?`
	rows, err := db.Query(conn, d, q, tid, lang)
	if err != nil {
		return nil, fmt.Errorf("knowledge: i18n/by_tenant_lang 查询失败: %w", err)
	}
	defer rows.Close()
	out := make([]contract.I18nByTenantLangItem, 0)
	for rows.Next() {
		var it contract.I18nByTenantLangItem
		if e := rows.Scan(&it.FactID, &it.Lang, &it.Value, &it.SourceHash, &it.Engine); e != nil {
			return nil, fmt.Errorf("knowledge: i18n/by_tenant_lang 行扫描失败: %w", e)
		}
		it.Present = true
		out = append(out, it)
	}
	if e := rows.Err(); e != nil {
		return nil, fmt.Errorf("knowledge: i18n/by_tenant_lang 行迭代失败: %w", e)
	}
	return out, nil
}

// UpsertFactI18n 幂等回写一行译文（唯一键 tenant_id+fact_id+lang）。
// ★ 人工终稿保护：已落 manual 的语种遇到非 manual 写入直接跳过（不覆盖、不报错，保持幂等写口）。
//
//	source_hash 由主服务给（绑定当前原文），知识侧不重算、不调 LLM。
func UpsertFactI18n(conn *sql.DB, d db.Dialect, req contract.I18nUpsertRequest) error {
	if req.TenantID <= 0 || req.FactID <= 0 || strings.TrimSpace(req.Lang) == "" {
		return fmt.Errorf("knowledge: i18n/upsert 租户/事实/语种非法（fail-closed KN3）")
	}
	if strings.TrimSpace(req.Engine) == "" {
		req.Engine = contract.I18nEngineSingleLang
	}
	// 先读旧档：manual 优先（§4.3.3 第 3 条）。
	var oldEngine string
	err := db.QueryRow(conn, d,
		`SELECT engine FROM product_fact_i18n WHERE tenant_id=? AND fact_id=? AND lang=?`,
		req.TenantID, req.FactID, req.Lang).Scan(&oldEngine)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("knowledge: i18n/upsert 读旧档失败: %w", err)
	}
	if err == nil && oldEngine == contract.I18nEngineManual && req.Engine != contract.I18nEngineManual {
		return nil // 人工终稿永不被模型产物反向覆盖
	}
	translatedAt := time.Now().UTC().Format(time.RFC3339)
	_, err = db.Exec(conn, d,
		`INSERT INTO product_fact_i18n(tenant_id, fact_id, lang, value, source_hash, translated_at, engine)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(tenant_id, fact_id, lang) DO UPDATE SET
		   value=excluded.value, source_hash=excluded.source_hash,
		   translated_at=excluded.translated_at, engine=excluded.engine`,
		req.TenantID, req.FactID, req.Lang, req.Value, req.SourceHash, translatedAt, req.Engine)
	if err != nil {
		return fmt.Errorf("knowledge: i18n/upsert 落库失败: %w", err)
	}
	return nil
}
