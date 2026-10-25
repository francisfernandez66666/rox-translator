// ============ systemconfig.go · 职责说明 ============
// store 包系统配置（system_config 表）数据访问层。
// KV 型配置的读写与流程步骤启停配置。
// 支持热更新：admin 可通过 API 保存 flow_steps（JSON）来控制流程引擎各步骤开关；
// GetFlowSteps 会把已保存配置与默认定义合并，StepEnabled 提供快捷判断。
// =============================================
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"translator/internal/db"
)

// SysConfig 系统配置项（DB 持久化 + 内存缓存）
type SysConfig struct {
	Key       string `json:"key"`        // 配置键名
	Value     string `json:"value"`      // 配置值
	UpdatedAt string `json:"updated_at"` // 最近更新时间（RFC3339 字符串）
}

// sysCfgCacheEntry GetConfig 的进程内缓存条目（★ 2026-10-11 慢 SQL 批 P2⑤）。
// 尺子只有 llmsource 那一把（AGENTS §一·3）：TTL 节流＋写侧"先落库、落成了才碰进程侧"＋
// 读失败沿用上一份快照＋换库即失效（挂在 Store 实例上构造性成立，见 store.go 字段注释）。
// value 不做"空串＝没有"的二义编码：**缺键也缓存空串**（负缓存），
// 因为 hasKey 字段记录的是"这一行到底存不存在"之外的信息并不需要——GetConfig 契约本来就是
// 缺键回 ("", nil)，缓存回 "" 与库语义一致。
type sysCfgCacheEntry struct {
	value     string
	expiresAt time.Time
}

// sysConfigCacheTTL 缓存窗口（SYSTEM_CONFIG_CACHE_TTL_SEC，默认 5s，与 LLM_CONFIG_RELOAD_TTL_SEC 同档）。
// 0 是显式"每次都现查"档（排障与单测用）；负数/非法按默认档，判据口径与 probeTTL 一致。
// 为什么 5 秒：档位数字/验证开关这类配置改完"下一位访客生效"就够（现网取证里它每请求被读 2–26 次，
// 全是同一份 KV 的重复往返）；再短就是拿库往返换那几秒的鲜度，方向反了。
func sysConfigCacheTTL() time.Duration {
	if v := os.Getenv("SYSTEM_CONFIG_CACHE_TTL_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 5 * time.Second
}

// GetConfig 读取配置项；不存在返回空字符串。
// 参数：key=配置键名；返回配置值（无记录时返回 "" 且无错误）。
// ★ TTL 缓存（P2⑤）三条口径：
//  1. 命中窗口内直接回缓存，**零往返**；窗口外现查，查到（含"缺键回空"）才写缓存；
//  2. 现查失败（网络/库坏）⇒ **沿用上一份快照**并回缓存值，不许把一次抖动打成"配置没了"
//     ——照 llmsource 那条「库读不到＝沿用快照，不许按 cfg 重发」的方向，只是这里连"重发"都没有；
//  3. 本方法是**读侧唯一动刀点**：ConfigsByKeys 是 llmsource 热加载探针自己的腿（它已带 TTL 节流），
//     给它再套一层缓存＝在同一把尺子下面垫第二层延迟，**禁止**；
//     绕开本方法直接摸 system_config 的写点（billing.go 启动默认项、points_rate_rebase 的事务内
//     upsert、支付配置/汇率/进度账的 DELETE）**落库成功后必须调 dropConfigCache 显式失效**——
//     本进程"读自己的裸写"不许等 TTL（既有契约测试钉的就是清空即时可见）；
//     只有**库外直改**（运维手改 SQL、另一台实例先写）才接受 ≤5 秒的鲜度代价，与 llmsource 同档。
func (s *Store) GetConfig(key string) (string, error) {
	ttl := sysConfigCacheTTL()
	if ttl > 0 {
		s.sysCfgMu.Lock()
		if e, ok := s.sysCfgCache[key]; ok && time.Now().Before(e.expiresAt) {
			s.sysCfgMu.Unlock()
			return e.value, nil
		}
		s.sysCfgMu.Unlock()
	}
	var v string
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT value FROM system_config WHERE key=?", key).Scan(&v)
	if err == sql.ErrNoRows {
		v = "" // 键不存在按空串处理
		err = nil
	}
	if err != nil {
		if ttl > 0 {
			s.sysCfgMu.Lock()
			e, ok := s.sysCfgCache[key]
			s.sysCfgMu.Unlock()
			if ok {
				return e.value, nil // 口径 2：读不到 ≠ 没有，沿用上一份快照
			}
		}
		return "", err
	}
	if ttl > 0 {
		s.sysCfgMu.Lock()
		if s.sysCfgCache == nil {
			s.sysCfgCache = map[string]sysCfgCacheEntry{}
		}
		s.sysCfgCache[key] = sysCfgCacheEntry{value: v, expiresAt: time.Now().Add(ttl)}
		s.sysCfgMu.Unlock()
	}
	return v, nil
}

// SetConfig 写入配置项（存在则更新，不存在则插入，upsert）。
// 参数：key=配置键名，value=配置值；返回错误。
// ★ 写侧纪律（§一·3 同一条尺子）：**先落库、落成了才碰进程侧**——
// 落库失败直接返回错误且缓存一字不动（反过来会把"保存失败"说成"这台已经生效了"）；
// 落库成功即把新值翻进缓存，本实例读自己的写**不等 TTL**。
func (s *Store) SetConfig(key, value string) error {
	// ON CONFLICT(key) 实现 upsert，保证键唯一
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO system_config (key, value, updated_at) VALUES (?,?,?) "+
			"ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at",
		key, value, time.Now().Format(time.RFC3339))
	if err != nil {
		return err
	}
	if sysConfigCacheTTL() > 0 {
		s.sysCfgMu.Lock()
		if s.sysCfgCache == nil {
			s.sysCfgCache = map[string]sysCfgCacheEntry{}
		}
		s.sysCfgCache[key] = sysCfgCacheEntry{value: value, expiresAt: time.Now().Add(sysConfigCacheTTL())}
		s.sysCfgMu.Unlock()
	}
	return nil
}

// dropConfigCache 让**绕开 SetConfig 的裸 SQL 写点**（DELETE、事务内 upsert、条件 INSERT）
// 在落库成功后显式失效缓存键（★ 2026-10-11 慢 SQL 批 P2⑤ 补腿）。
// 存在的理由：本批回归实锤——支付清空/汇率清除/进度账回收/F-78 补发四类写点都是裸 SQL，
// 缓存若不失效会把「已清除」读成「还在库上」（现网同形态＝运营清空支付私钥后 ≤5 秒渠道
// 仍按已配置走，方向危险）。纪律：**先落库、落成了才碰缓存**，与 SetConfig 写侧同一条尺子；
// 裸写失败的一律不许调本函数（失效了也读不回真值，反而掩盖写失败）。
// 多实例口径不变：别的进程最多再陈旧一个 TTL 窗口，与 llmsource 懒探同一档。
// 参数 keys: 已被裸写改动/删除的配置键；空参数无操作。
func (s *Store) dropConfigCache(keys ...string) {
	if len(keys) == 0 {
		return
	}
	s.sysCfgMu.Lock()
	for _, k := range keys {
		delete(s.sysCfgCache, k)
	}
	s.sysCfgMu.Unlock()
}

// ConfigsByKeys 一次查询读取多个配置项（★ 2026-10-04 〇-AR 第 5 波「每台热加载」的探测腿）。
// 参数 keys: 配置键名列表；返回 map[键]=值——**库里没有的键不出现在 map 里**（调用方按缺省处理，
// 不许把"缺键"和"值为空串"混为一谈，那是两种运维动作）。
// 为什么单独开这一个方法：热加载探测要在**每一次上游调用前**判断"库里那份模型配置变没变"，
// 用 N 次 GetConfig 就是 N 次往返；一条 `key IN (...)` 把它压成一次，
// 也让"指纹"建立在**同一次快照读**上（跨两次查询取值会读到半改状态：
// 管理台保存是多个键分次写入的，分开读能拼出一把谁都没配过的组合）。
// 方言口径：SQLite／PostgreSQL 都支持 `IN (?,?,…)`，无尾随 LIMIT，占位符按参数个数动态生成。
func (s *Store) ConfigsByKeys(keys ...string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	if len(keys) == 0 {
		return out, nil // 空清单直接回空 map，不生成 `IN ()` 这种两种方言都不认的句子
	}
	ph := strings.Repeat("?,", len(keys))
	ph = ph[:len(ph)-1] // 去掉最后一个逗号：?,?,? 而不是 ?,?,
	args := make([]interface{}, 0, len(keys))
	for _, k := range keys {
		args = append(args, k)
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT key, value FROM system_config WHERE key IN ("+ph+")", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue // 单行解析失败跳过（与 AllConfigs 同口径）
		}
		out[k] = v
	}
	return out, nil
}

// AllConfigs 返回全部配置项（按键名排序）。
// 返回：配置项切片。
func (s *Store) AllConfigs() ([]SysConfig, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT key, value, COALESCE(updated_at,'') FROM system_config ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SysConfig
	for rows.Next() {
		var c SysConfig
		if err := rows.Scan(&c.Key, &c.Value, &c.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, c)
	}
	return out, nil
}

// FlowStep 流程步骤配置（流程引擎步骤启停）
type FlowStep struct {
	Key    string `json:"key"`    // 步骤标识（如 kb_match / ai_initial / gate）
	Name   string `json:"name"`   // 步骤中文名
	Enable bool   `json:"enable"` // 是否启用
}

// DefaultFlowSteps 默认流程步骤定义（key → 中文名）
var DefaultFlowSteps = []FlowStep{
	{Key: "kb_match", Name: "知识库匹配", Enable: true},
	{Key: "ai_initial", Name: "AI 初翻", Enable: true},
	{Key: "evals_initial", Name: "初翻评估", Enable: true},
	{Key: "review", Name: "审校 Agent", Enable: true},
	{Key: "evals_review", Name: "审校评估", Enable: true},
	{Key: "gate", Name: "ConstraintGate 硬校验", Enable: true},
	{Key: "culture_gate", Name: "语言文化包输出闸门", Enable: true},
	{Key: "qa", Name: "QA 确定性检查", Enable: true},
	{Key: "approval", Name: "人工审批", Enable: true},
	{Key: "feedback", Name: "自迭代写库", Enable: true},
}

// GetFlowSteps 读取流程步骤启停配置（合并默认定义：仅覆盖已保存步骤的 Enable）。
// 返回：完整步骤列表（按默认定义顺序）。
func (s *Store) GetFlowSteps() ([]FlowStep, error) {
	raw, err := s.GetConfig("flow_steps")
	var saved []FlowStep
	if err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &saved) // 解析已保存的 JSON 配置
	}
	byKey := map[string]bool{}
	for _, st := range saved {
		byKey[st.Key] = st.Enable // 建索引：key → 是否启用
	}
	out := make([]FlowStep, 0, len(DefaultFlowSteps))
	for _, d := range DefaultFlowSteps {
		// 以默认定义为主干，命中已保存配置则覆盖 Enable
		if en, ok := byKey[d.Key]; ok {
			d.Enable = en
		}
		out = append(out, d)
	}
	return out, nil
}

// SaveFlowSteps 保存流程步骤启停配置（整体覆盖写入 flow_steps 配置项）。
// 参数：steps=完整步骤列表；返回错误。
func (s *Store) SaveFlowSteps(steps []FlowStep) error {
	data, _ := json.Marshal(steps)
	return s.SetConfig("flow_steps", string(data))
}

// StepEnabled 快捷判断某步骤是否启用（admin 可关）。
// 参数：key=步骤标识；返回是否启用（读取失败默认视为启用，保证容错）。
func (s *Store) StepEnabled(key string) bool {
	steps, err := s.GetFlowSteps()
	if err != nil {
		return true // 读取异常时默认启用，避免误停流程
	}
	for _, st := range steps {
		if st.Key == key {
			return st.Enable
		}
	}
	return true // 未知步骤默认启用
}

// ============ 租户 QPS/并发配额持久化（2026-08-26 全仓评审 C2） ============

// TenantQuotaCfg 租户限流配额（QPS / 并发上限）。
// 此前仅写进程内存（重启即回默认 10/3），现落 system_config 持久化。
type TenantQuotaCfg struct {
	QPS        int `json:"qps"`        // 每秒请求数上限
	Concurrent int `json:"concurrent"` // 并发请求数上限
}

// tenantQuotaKey 生成租户配额的配置键。
func tenantQuotaKey(tid int64) string { return fmt.Sprintf("tenant_quota_%d", tid) }

// SaveTenantQuotaCfg 持久化租户限流配额（upsert）。
func (s *Store) SaveTenantQuotaCfg(tid int64, qps, concurrent int) error {
	b, _ := json.Marshal(TenantQuotaCfg{QPS: qps, Concurrent: concurrent})
	return s.SetConfig(tenantQuotaKey(tid), string(b))
}

// LoadTenantQuotaConfigs 加载全部租户限流配额（服务启动时回放用）。
// 返回：map[租户ID]=配额；解析失败的行跳过。
func (s *Store) LoadTenantQuotaConfigs() map[int64]TenantQuotaCfg {
	out := map[int64]TenantQuotaCfg{}
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT key, value FROM system_config WHERE key LIKE 'tenant_quota_%'")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		tidStr := strings.TrimPrefix(k, "tenant_quota_")
		tid, perr := strconv.ParseInt(tidStr, 10, 64)
		if perr != nil || tid <= 0 {
			continue
		}
		var cfg TenantQuotaCfg
		if json.Unmarshal([]byte(v), &cfg) == nil && cfg.QPS > 0 {
			out[tid] = cfg
		}
	}
	return out
}

// ConfigIntAllowZero 读整型配置，**把 0 当成一个真实档位**（不是"没配"）。
// 参数：key=配置键；def=缺省档位（键不存在／值空／非整数／负数时回落）。
// 返回：库里那一个整数，或 def。
//
// 为什么要有这一枚，而不用既有的 Store.ConfigInt（在 kbscrape.go，语义是「≤0＝未配置」）：
// 《修改文档_UAT云端E2E核实与修法_20261024》档位表里 `ticket_repair_max=0＝不重发`、
// `text_ticket_per_line=0＝回退整篇` 这两条是**应急回退开关**——0 那一档本身就是想按下的按钮。
// 走 ConfigInt 时运营在管理台填 0，读回来却是默认 3／1，开关**结构性按不下去**
// （现网表现＝「已经配成 0 了怎么还在重发」，而库里那一行明明是 0，日志零线索）。
// 判别口径：只有「取不到整数」才回落默认；0 与正整数一律如实返回，负数按脏值回落
// （负的重发上限／负的每行数没有语义，放行只会把循环打歪）。
func (s *Store) ConfigIntAllowZero(key string, def int) int {
	v, err := s.GetConfig(key)
	if err != nil {
		return def
	}
	n, e := strconv.Atoi(strings.TrimSpace(v))
	if e != nil || n < 0 {
		return def
	}
	return n
}
