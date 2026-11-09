// ============ sales_config.go · 职责说明 ============
// 二期「前端可配」的租户级配置落库口：销售模型／流程引擎／销售策略三段配置合成一份 JSON。
// ★ 一租户一行，键就是 tenant_id（不用全局 system_config，避免把 A 租户的模型档位写到 B 租户）。
// 本层只管 JSON 原文存取与合法性，字段语义与校验由 API 层单一实现（不造第二把尺子）。
// ================================================
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"translator/internal/db"
)

// GetSalesConfig 读一租户的销售配置原文；未配置返回 ("", false, nil)。
// 参数：tid=显式租户；返回：JSON 原文、是否存在、错误。
func (s *Store) GetSalesConfig(tid int64) (string, bool, error) {
	if tid <= 0 {
		return "", false, errors.New("销售配置读取必须带有效租户")
	}
	var raw string
	err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT config_json FROM sales_config WHERE tenant_id=?`, tid).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("销售配置读取失败: %w", err)
	}
	return raw, true, nil
}

// SaveSalesConfig 整份覆盖一租户的销售配置（管理台保存口）。
// 参数：tid=显式租户；raw=经 API 层校验的 JSON；返回：写错误。
func (s *Store) SaveSalesConfig(tid int64, raw string) error {
	if tid <= 0 {
		return errors.New("销售配置保存必须带有效租户")
	}
	if !json.Valid([]byte(raw)) {
		return errors.New("销售配置必须是合法 JSON")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO sales_config (tenant_id, config_json, updated_at) VALUES (?,?,?)
		 ON CONFLICT(tenant_id) DO UPDATE SET config_json=EXCLUDED.config_json, updated_at=EXCLUDED.updated_at`,
		tid, raw, now)
	if err != nil {
		return fmt.Errorf("销售配置保存失败: %w", err)
	}
	return nil
}
