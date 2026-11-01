// ============ ratelimit_distinct_keys.go · 职责说明 ============
// store 包 rate_limits 的「同一分组前缀下有几个不同 key」只读统计。
// 唯一消费方＝留资免验证通道的升级判据 S1（DEF-P0-03，D-3 拍板：同 IP＋同设备合并分组）。
//
// 为什么单开一个文件而不往 ratelimit.go 追加：那套 API 的语义是「窗口预留/退回」，
// 本函数是「分组基数」——混在一个文件里，下一个读的人会以为它也受窗口约束（它不预留、不改计数）。
//
// ⚠️ 为什么在 Go 侧按前缀筛而不是 SQL `key LIKE 'prefix%'`：
// 设备号的合法字符集是 `^[A-Za-z0-9_-]{8,64}$`，**下划线在 LIKE 里是单字符通配**，
// 不转义就会把别的分组算进来（升级判据误报，现网症状是"偶发要验证码"，最难归因的一类）；
// 而 `ESCAPE '\'` 这一句在 SQLite/PG 两边的字符串字面量规则又不完全一致（AGENTS §一·4 要求同一条 SQL 两方言都跑）。
// ⇒ 用 `scope + window_start>=?` 把候选行取窄（这本账只有留资提交会写，量级是"每组一行"），
//
//	前缀匹配与去重在 Go 侧做——**判据与方言无关**，且这一条不写库、不需要 LIMIT。
//
// =============================================
package store

import (
	"strings"

	"translator/internal/db"
)

// CountDistinctRateKeys 统计 scope 下、window_start ≥ since（unix 秒）的行的 key 里，
// 以 keyPrefix 开头的**不同 key 数**。
// 参数 scope: 账本名；keyPrefix: 分组前缀（含分隔符，如 "<IP>:<设备号>:"）；since: 窗口起点。
// 读不到库（s.db==nil）回 0 且不报错——调用方按"不升级"处理（fail-open：账本故障不该把唯一客户入口挡死）。
func (s *Store) CountDistinctRateKeys(scope, keyPrefix string, since int64) (int, error) {
	if s.db == nil || keyPrefix == "" {
		return 0, nil
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT key FROM rate_limits WHERE scope=? AND window_start>=?", scope, since)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return 0, err
		}
		if strings.HasPrefix(k, keyPrefix) {
			seen[k] = true
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return len(seen), nil
}
