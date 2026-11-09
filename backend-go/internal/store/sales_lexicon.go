// ============ sales_lexicon.go · 职责说明 ============
// 二期 §5「中文硬匹配外提」的落库口：词表按 租户／场景／语种／类别 四档配置，
// 代码里只留读取函数，任何行业词都不许抄进引擎源码。
// tenant_id=0 是平台默认档；读序固定为「租户档 > 平台档 > 空」，
// 租户档存在（哪怕词表为空）即视为运营显式覆盖，不再悄悄回落平台档。
// ================================================
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"translator/internal/db"
)

// LexiconRow 词表一行（words 以 JSON 数组落 TEXT，读侧解析成字符串切片）。
type LexiconRow struct {
	ID        int64
	TenantID  int64
	Scene     string
	Lang      string
	Kind      string
	Words     []string
	UpdatedAt string
}

// lexiconCols 列表查询的统一列清单（防止各读口各抄一份列名）。
const lexiconCols = "id, tenant_id, scene, lang, kind, words, updated_at"

// validateLexiconKey 校验四档键；租户档可为 0（平台默认档），但不得为负。
// 参数：tid=租户；scene/lang/kind=档位；返回：校验错误。
func validateLexiconKey(tid int64, scene, lang, kind string) error {
	if tid < 0 {
		return errors.New("词表档位租户不得为负（0 表示平台默认档）")
	}
	if strings.TrimSpace(scene) == "" || strings.TrimSpace(lang) == "" || strings.TrimSpace(kind) == "" {
		return errors.New("词表档位缺少场景、语种或类别")
	}
	return nil
}

// normalizeLexiconWords 去空白、去重、丢空串，保持配置写入口径稳定。
// 参数：words=原始词表；返回：规范化后的词表。
func normalizeLexiconWords(words []string) []string {
	out := make([]string, 0, len(words))
	seen := map[string]bool{}
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// UpsertLexicon 整份覆盖某一档（管理台写口）；同键第二次写只更新词表与时间。
// 参数：tid=租户；scene/lang/kind=档位；words=新词表；返回：写错误。
func (s *Store) UpsertLexicon(tid int64, scene, lang, kind string, words []string) error {
	if err := validateLexiconKey(tid, scene, lang, kind); err != nil {
		return err
	}
	raw, err := json.Marshal(normalizeLexiconWords(words))
	if err != nil {
		return fmt.Errorf("词表序列化失败: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO sales_lexicon (tenant_id, scene, lang, kind, words, updated_at)
		 VALUES (?,?,?,?,?,?)
		 ON CONFLICT(tenant_id, scene, lang, kind) DO UPDATE SET
			words=EXCLUDED.words, updated_at=EXCLUDED.updated_at`,
		tid, scene, lang, kind, string(raw), now)
	if err != nil {
		return fmt.Errorf("词表覆盖失败: %w", err)
	}
	return nil
}

// SeedLexiconIfAbsent 幂等种入平台默认档：已存在（含运营改过的）一律不覆盖。
// 参数：tid=档位租户（种子固定传 0）；scene/lang/kind=档位；words=种子词表；返回：本次是否种入与错误。
func (s *Store) SeedLexiconIfAbsent(tid int64, scene, lang, kind string, words []string) (bool, error) {
	if err := validateLexiconKey(tid, scene, lang, kind); err != nil {
		return false, err
	}
	raw, err := json.Marshal(normalizeLexiconWords(words))
	if err != nil {
		return false, fmt.Errorf("词表种子序列化失败: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO sales_lexicon (tenant_id, scene, lang, kind, words, updated_at)
		 SELECT ?,?,?,?,?,? WHERE NOT EXISTS (
			SELECT 1 FROM sales_lexicon WHERE tenant_id=? AND scene=? AND lang=? AND kind=?)`,
		tid, scene, lang, kind, string(raw), now, tid, scene, lang, kind)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return false, nil
		}
		return false, fmt.Errorf("词表种子写入失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("词表种子读数失败: %w", err)
	}
	return n == 1, nil
}

// scanLexiconRow 把一行扫成 LexiconRow（words 为 JSON 数组）。
// 参数：row=单行结果；返回：词表行与错误。
func scanLexiconRow(row *sql.Row) (LexiconRow, error) {
	var r LexiconRow
	var raw string
	if err := row.Scan(&r.ID, &r.TenantID, &r.Scene, &r.Lang, &r.Kind, &raw, &r.UpdatedAt); err != nil {
		return LexiconRow{}, err
	}
	if err := json.Unmarshal([]byte(raw), &r.Words); err != nil {
		return LexiconRow{}, fmt.Errorf("词表 JSON 读数失败: %w", err)
	}
	return r, nil
}

// ListLexicon 管理台列表：租户档＋平台默认档合并视图（不做隐式覆盖，由读口判优先级）。
// 参数：tid=当前租户；返回：词表行与错误。
func (s *Store) ListLexicon(tid int64) ([]LexiconRow, error) {
	if tid <= 0 {
		return nil, errors.New("词表列表必须带有效租户")
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT `+lexiconCols+` FROM sales_lexicon WHERE tenant_id=? OR tenant_id=0
		 ORDER BY tenant_id, scene, lang, kind`, tid)
	if err != nil {
		return nil, fmt.Errorf("词表列表查询失败: %w", err)
	}
	defer rows.Close()
	out := []LexiconRow{}
	for rows.Next() {
		var r LexiconRow
		var raw string
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Scene, &r.Lang, &r.Kind, &raw, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("词表列表扫描失败: %w", err)
		}
		if err := json.Unmarshal([]byte(raw), &r.Words); err != nil {
			return nil, fmt.Errorf("词表列表 JSON 读数失败: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadLexiconWords 引擎读口：租户档优先，未配置则回落平台默认档，仍无则空表。
// 参数：tid=租户；scene/lang/kind=档位；返回：词表与错误。
func (s *Store) LoadLexiconWords(tid int64, scene, lang, kind string) ([]string, error) {
	if err := validateLexiconKey(tid, scene, lang, kind); err != nil {
		return nil, err
	}
	r, err := scanLexiconRow(db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT `+lexiconCols+` FROM sales_lexicon WHERE tenant_id=? AND scene=? AND lang=? AND kind=?`,
		tid, scene, lang, kind))
	if err == nil {
		return normalizeLexiconWords(r.Words), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("词表租户档读取失败: %w", err)
	}
	if tid == 0 {
		return []string{}, nil // 平台档自身未配置：没有更低一档可回落
	}
	r, err = scanLexiconRow(db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT `+lexiconCols+` FROM sales_lexicon WHERE tenant_id=0 AND scene=? AND lang=? AND kind=?`,
		scene, lang, kind))
	if errors.Is(err, sql.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("词表平台档读取失败: %w", err)
	}
	return normalizeLexiconWords(r.Words), nil
}

// DeleteLexicon 删除租户自有的一档；平台默认档（tid=0）不许由租户删，跨租户删不到。
// 参数：id=行号；tid=当前租户；返回：本次是否删到与错误。
func (s *Store) DeleteLexicon(id int64, tid int64) (bool, error) {
	if id <= 0 || tid <= 0 {
		return false, errors.New("词表删除必须带有效行号与租户，且平台默认档不可删")
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`DELETE FROM sales_lexicon WHERE id=? AND tenant_id=?`, id, tid)
	if err != nil {
		return false, fmt.Errorf("词表删除失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("词表删除读数失败: %w", err)
	}
	return n == 1, nil
}
