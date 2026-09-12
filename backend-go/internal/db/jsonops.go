// ============ jsonops.go · 职责说明 ============
// JSON 文档原子操作的方言助手：为「TEXT 列内 JSON 字段的原子自增/守卫自减/置布尔」
// 提供 SQLite(JSON1) 与 PostgreSQL(jsonb) 两套等价 SQL 片段。
//
// 背景（2026-09-12 PG 方言重研）：业务层曾直接内联 json_set/json_extract（SQLite JSON1 专属），
// PostgreSQL 下整条 UPDATE 报「function json_extract does not exist」，导致增量包支付结算、
// trial 提醒复位、卡死工单重排等链路失败。新增此类 JSON 原子操作一律经本文件取 SQL 片段，
// 禁止在业务代码内联方言 JSON 函数。
//
// 约定：所有片段以 SQLite 占位符 `?` 书写（PG 下由 query.go 的 RewritePlaceholders 统一改写
// 为 $n，参数顺序 = 片段中 ? 出现顺序）；列名/键名经白名单正则校验后拼接，防注入。
// =============================================
package db

import (
	"fmt"
	"regexp"
)

// jsonIdentRe 列名/JSON 键名白名单（仅字母数字下划线，杜绝拼接注入）。
var jsonIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// mustJSONIdent 校验并返回标识符；非法立即 panic（属编码错误，应在测试期暴露）。
// 参数：s=列名或 JSON 键名。
func mustJSONIdent(s string) string {
	if !jsonIdentRe.MatchString(s) {
		panic("db: 非法列名或 JSON 键名: " + s)
	}
	return s
}

// JSONNumAdd 生成「col 的 JSON 字段 $.k 数值 += ?（一个参数）」的 UPDATE SET 片段。
// 旧值缺失/空串按 0 处理；结果保持 JSON 数字类型。
// 参数：d=目标方言；col=JSON 文本列；k=数值键。
func JSONNumAdd(d Dialect, col, k string) string {
	c, key := mustJSONIdent(col), mustJSONIdent(k)
	if d == DialectPostgres {
		// permissions 为 TEXT 列：::jsonb 运算后整体转回 text；numeric+参数 归一为 bigint 再入 JSON
		return fmt.Sprintf("%s = jsonb_set(COALESCE(NULLIF(%s,'')::jsonb,'{}'::jsonb), '{%s}', "+
			"to_jsonb((COALESCE(NULLIF(%s,'')::jsonb->>'%s','0')::numeric + ?)::bigint))::text",
			c, c, key, c, key)
	}
	return fmt.Sprintf("%s = json_set(COALESCE(NULLIF(%s,''),'{}'), '$.%s', COALESCE(json_extract(NULLIF(%s,''),'$.%s'),0)+?)",
		c, c, key, c, key)
}

// JSONNumGE 生成「col 的 JSON 字段 $.k 数值 >= ?（一个参数）」的 WHERE 守卫片段。
// 与 JSONNumAdd（负增量）组合即「守卫式原子自减」：RowsAffected==0 表示余额不足。
// 参数：d=目标方言；col=JSON 文本列；k=数值键。
func JSONNumGE(d Dialect, col, k string) string {
	c, key := mustJSONIdent(col), mustJSONIdent(k)
	if d == DialectPostgres {
		return fmt.Sprintf("COALESCE(NULLIF(%s,'')::jsonb->>'%s','0')::numeric >= ?", c, key)
	}
	return fmt.Sprintf("COALESCE(json_extract(NULLIF(%s,''),'$.%s'),0)>=?", c, key)
}

// JSONSetFalse 生成「col 的 JSON 字段 $.k 置 false」的 UPDATE SET 片段（提醒标记复位用）。
// 参数：d=目标方言；col=JSON 文本列；k=布尔键。
func JSONSetFalse(d Dialect, col, k string) string {
	c, key := mustJSONIdent(col), mustJSONIdent(k)
	if d == DialectPostgres {
		return fmt.Sprintf("%s = jsonb_set(COALESCE(NULLIF(%s,'')::jsonb,'{}'::jsonb), '{%s}', 'false'::jsonb)::text",
			c, c, key)
	}
	return fmt.Sprintf("%s = json_set(COALESCE(NULLIF(%s,''),'{}'), '$.%s', json('false'))", c, c, key)
}

// JSONExtractNum 生成「取 col 的 JSON 字段 $.k 数值（缺失/空按 0）」的 SELECT 表达式。
// 参数：d=目标方言；col=JSON 文本列；k=数值键。
func JSONExtractNum(d Dialect, col, k string) string {
	c, key := mustJSONIdent(col), mustJSONIdent(k)
	if d == DialectPostgres {
		return fmt.Sprintf("COALESCE(NULLIF(%s,'')::jsonb->>'%s','0')::numeric", c, key)
	}
	return fmt.Sprintf("COALESCE(json_extract(NULLIF(%s,''),'$.%s'),0)", c, key)
}

// JSONTicketIDExpr 生成「从 payload 文本列的 JSON 中取 ticket_id 数值」的 SELECT/WHERE 表达式。
// 参数：d=目标方言；col=payload 列名，可带表别名限定（如 "j.payload"）。
func JSONTicketIDExpr(d Dialect, col string) string {
	c := mustJSONQualifiedIdent(col)
	if d == DialectPostgres {
		return fmt.Sprintf("(NULLIF(%s,'')::jsonb->>'ticket_id')::bigint", c)
	}
	return fmt.Sprintf("CAST(json_extract(%s,'$.ticket_id') AS INTEGER)", c)
}

// jsonQualifiedRe 允许「别名.列」形式的限定列名（两段，各自满足标识符白名单）。
var jsonQualifiedRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// mustJSONQualifiedIdent 校验限定列名（含可选表别名前缀）；非法 panic。
// 参数：s=列名或 别名.列。
func mustJSONQualifiedIdent(s string) string {
	if !jsonQualifiedRe.MatchString(s) {
		panic("db: 非法限定列名: " + s)
	}
	return s
}
