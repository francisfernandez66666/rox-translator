// Package engine 接待引擎：知识/话术检索 → Prompt 组装 → LLM/规则兜底 → 流程推进 → 动作提取。
// 核心思想移植自 ai-scrm：策略定方向、大模型定表达、模板兜底保底。
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"translator/internal/assist/llm"
	"translator/internal/assist/store"
	"translator/internal/observability"
)

// Action 回复附带的动作按钮（前端渲染为可点击跳转）
type Action struct {
	Key   string `json:"key"`   // 动作唯一标识，用于关联 feature_links 表中的条目
	Name  string `json:"name"`  // 按钮展示名（前端渲染文案）
	URL   string `json:"url"`   // 点击后跳转地址（功能入口）
	FType string `json:"ftype"` // 入口类型，如 route（站内路由）/link（外链），前端据此决定跳转方式
	Icon  string `json:"icon"`  // 按钮图标标识（前端按约定加载对应图标）
}

// Reply 一次接待的产出
type Reply struct {
	Content string   `json:"content"`
	Actions []Action `json:"actions"`
	Flow    string   `json:"flow,omitempty"` // 命中的流程 key
	Model   string   `json:"model,omitempty"`
	Source  string   `json:"source"` // llm / rule / flow / fallback
}

// Engine 接待引擎
type Engine struct {
	db  *store.DB
	llm *llm.Client

	// ★ R0.4 LLM 热加载：记录构建 client 时的配置指纹，llmReply 前比对 configs
	// 中 LLM 四项（base_url/api_key/model/model_backup），变更则重建。
	llmFP    string
	llmMu    sync.Mutex
	llmBuilt bool
	// ★ R0.1 同义词归一表缓存（configs.synonyms 指纹失效重载）
	synFP     string
	synGroups [][]string

	// ★ 分级召回第 3 级（默认关闭，见 vector.go）：知识向量索引缓存。
	// vecMu 保护全部 vec* 字段；vecCoolUntil 为嵌入失败熔断窗口。
	vecMu        sync.Mutex
	vecModel     string
	vecFP        string
	vecKeys      []string
	vecMat       [][]float32
	vecCoolUntil time.Time
}

// New 构建引擎
func New(db *store.DB, client *llm.Client) *Engine {
	return &Engine{db: db, llm: client}
}

// ============================================================
// ★ R0.4 LLM 热加载：configs 表 LLM 配置 → 惰性重建 client
// 优先级：显式 env（main 构建的初始 client，providers 非空即视为 env 接管）
// > configs 表（管理台在线配置）。configs 变更经指纹比对在下次对话时生效。
// ============================================================

// llmFingerprint 计算 configs 中 LLM 四项的配置指纹
func (e *Engine) llmFingerprint() string {
	return strings.Join([]string{
		e.db.GetConfig("llm_base_url", ""),
		e.db.GetConfig("llm_api_key", ""),
		e.db.GetConfig("llm_model", ""),
		e.db.GetConfig("llm_model_backup", ""),
	}, "\x00")
}

// ensureLLM 返回当前应使用的 LLM client；configs LLM 配置变更时重建。
// env 显式接入（初始 providers 非空）时不被 configs 覆盖——生产 secrets.env 优先。
// ★ 改造 1A：签名加 ctx，热加载日志经主仓 observability 输出（slog JSON + trace_id）。
func (e *Engine) ensureLLM(ctx context.Context) *llm.Client {
	e.llmMu.Lock()
	defer e.llmMu.Unlock()
	// env 已显式接入：固定使用初始 client，不回读 configs（避免管理台误配导致生产断链）
	if e.llm != nil && e.llm.Enabled() {
		return e.llm
	}
	fp := e.llmFingerprint()
	if e.llmBuilt && fp == e.llmFP {
		return e.llm // 指纹未变，复用
	}
	// configs 表有完整 LLM 配置则重建
	baseURL := e.db.GetConfig("llm_base_url", "")
	apiKey := e.db.GetConfig("llm_api_key", "")
	model := e.db.GetConfig("llm_model", "")
	if baseURL != "" && apiKey != "" && model != "" {
		provs := []llm.Provider{{Name: "main", BaseURL: baseURL, APIKey: apiKey, Model: model}}
		if bk := e.db.GetConfig("llm_model_backup", ""); bk != "" {
			provs = append(provs, llm.Provider{Name: "backup", BaseURL: baseURL, APIKey: apiKey, Model: bk})
		}
		observability.Info(ctx, "assist.engine LLM 配置经管理台热加载生效",
			"model", model, "backup", bkName(provs))
		e.llm = llm.New(provs, 45)
	}
	e.llmFP = fp
	e.llmBuilt = true
	return e.llm
}

// bkName 降级链展示名（日志用）
func bkName(provs []llm.Provider) string {
	if len(provs) > 1 {
		return provs[1].Model
	}
	return "-"
}

// ============================================================
// 检索：关键词打分（关键词命中数 × 优先级）
// ============================================================

// hitScore 词条与输入的相关度（关键词命中数）
// ★ R0.1 增强语义：
//  1. 双向包含：关键词命中输入（原逻辑）或输入包含词根较短的词（≥2 字关键词被输入
//     包含也计命中，缓解「怎么充钱」vs 关键词「充值」的字面缺口）
//  2. 同义词归一：configs.synonyms 配置归一表（每行「词=同义词1|同义词2」，命中任一
//     同义词按词计分），管理台在线维护
func (e *Engine) hitScore(input string, keywords string) int {
	kws := strings.Split(keywords, ",")
	n := 0
	for _, k := range kws {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if strings.Contains(input, k) {
			n++
			continue
		}
		// 双向包含：短关键词（≥2 rune）被输入包含
		if len([]rune(k)) >= 2 && len([]rune(k)) < len([]rune(input)) && strings.Contains(k, input) {
			// input 是 k 的子串（如输入「充钱」是关键词「充钱指南」一部分）——极少用，跳过
			continue
		}
		// 同义词归一命中
		if e.synonymHit(input, k) {
			n++
		}
	}
	return n
}

// synOnce 同义词表进程内缓存（管理台改 synonyms 后经指纹失效）
var synMu sync.Mutex

// synonymHit 判断输入与关键词是否经同义词表等价
// 表格式（configs.synonyms，多行）：充值=充钱|交钱；付款=给钱
func (e *Engine) synonymHit(input, keyword string) bool {
	if e.synFP != e.db.GetConfig("synonyms", "") {
		e.reloadSynonyms()
	}
	synMu.Lock()
	defer synMu.Unlock()
	for _, group := range e.synGroups {
		// 组内任一成员与 keyword 相等，且输入包含组内任一其他成员
		kwIn := false
		for _, m := range group {
			if m == keyword {
				kwIn = true
				break
			}
		}
		if !kwIn {
			continue
		}
		for _, m := range group {
			if m != keyword && strings.Contains(input, m) {
				return true
			}
		}
	}
	return false
}

// reloadSynonyms 重新加载同义词表
func (e *Engine) reloadSynonyms() {
	synMu.Lock()
	defer synMu.Unlock()
	e.synFP = e.db.GetConfig("synonyms", "")
	e.synGroups = nil
	for _, line := range strings.Split(e.synFP, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		var group []string
		// 左侧标准词（可逗号分隔多个）+ 右侧同义词（| 分隔）
		for _, seg := range strings.Split(parts[0]+","+strings.ReplaceAll(parts[1], "|", ","), ",") {
			if p := strings.TrimSpace(seg); p != "" {
				group = append(group, p)
			}
		}
		if len(group) >= 2 {
			e.synGroups = append(e.synGroups, group)
		}
	}
}

// entry 打分后的候选
type entry struct {
	key     string
	title   string
	content string
	link    string
	score   int
	kw      string  // 该条目的原始关键词串（★ 复合意图让位判定用，不参与渲染）
	via     string  // 命中通道：exact（第 1 级精确/同义词）/ fuzzy（第 2 级相似度）/ vector（第 3 级嵌入）
	sim     float64 // fuzzy/vector 通道的原始相似度（exact 时为命中关键词数），供日志观测与同分排序
}

// 分级召回命中标识
const (
	viaExact  = "exact"
	viaFuzzy  = "fuzzy"
	viaVector = "vector"
)

// 分级打分口径（WHY）：score 决定素材排序与兜底判定，三级严格分层——
//   - exact：hits*10 + (10-prio)，单次命中恒 ≥10（seed 内 prio ≤10）；
//   - fuzzy：8 - prio*8/10 ∈ [1,8]，恒低于任何一次精确命中——相似度是「救零命中」
//     的弱信号，永远不能压过运营精心维护的关键词命中；
//   - vector：4 - prio*4/10 ∈ [1,4]，恒低于 fuzzy（语义召回误伤面最大，权重最低）。
//
// 若管理台把 priority 配到 >10 会破坏该不变式，属运营侧自伤，不做代码兜底。
func fuzzyTierScore(prio int) int {
	s := 8 - prio*8/10
	if s < 1 {
		s = 1
	}
	return s
}

func vectorTierScore(prio int) int {
	s := vecScoreBase - prio*vecScoreBase/10
	if s < 1 {
		s = 1
	}
	return s
}

// RetrieveKB 检索知识库 topN（enabled，分级召回：精确/同义词 → 相似度 → 向量）。
// 保留原签名（ctx 无关调用方/单测用），内部委托 retrieveKB。
func (e *Engine) RetrieveKB(input string, topN int) []entry {
	return e.retrieveKB(context.Background(), input, topN)
}

// retrieveKB 分级检索主实现。ctx 仅用于观测日志与第 3 级嵌入调用。
// topN>=99 特例维持原语义：全量注入系统 prompt（此时跳过第 2/3 级，零网络）。
func (e *Engine) retrieveKB(ctx context.Context, input string, topN int) []entry {
	rows, err := e.db.List("kb_entries", true)
	if err != nil {
		return nil
	}
	var cands []entry
	exactKeys := map[string]bool{}
	prioByKey := map[string]int{}
	for _, r := range rows {
		kw := store.Row(r)["keywords"].(string)
		prio := toInt(r["priority"], 5)
		prioByKey[asStr(r["key"])] = prio
		hits := e.hitScore(input, kw)
		if hits > 0 {
			exactKeys[asStr(r["key"])] = true
			cands = append(cands, entry{
				key:     asStr(r["key"]),
				title:   asStr(r["title"]),
				content: asStr(r["content"]),
				link:    asStr(r["link_keys"]),
				score:   hits*10 + 10 - prio,
				kw:      kw,
				via:     viaExact,
				sim:     float64(hits),
			})
			continue
		}
		if topN >= 99 {
			// 全量注入模式原本就带全部条目（score 仅按优先级），无需相似度
			cands = append(cands, entry{
				key:     asStr(r["key"]),
				title:   asStr(r["title"]),
				content: asStr(r["content"]),
				link:    asStr(r["link_keys"]),
				score:   10 - prio,
				kw:      kw,
				via:     viaExact,
			})
		}
	}
	if topN < 99 {
		cands = e.appendFuzzyHits(cands, exactKeys, prioByKey, input, rows)
		cands = e.appendVectorHits(ctx, cands, input, rows)
		sort.SliceStable(cands, func(i, j int) bool {
			if cands[i].score != cands[j].score {
				return cands[i].score > cands[j].score
			}
			return cands[i].sim > cands[j].sim // 同分按相似度降序，稳定可观测
		})
		if len(cands) > topN {
			cands = cands[:topN]
		}
		e.logRecall(ctx, input, cands)
	}
	return cands
}

// appendFuzzyHits 第 2 级：对第 1 级零命中的条目做相似度打分，达阈值的以弱分入池。
func (e *Engine) appendFuzzyHits(cands []entry, exactKeys map[string]bool, prioByKey map[string]int, input string, rows []store.Row) []entry {
	for _, r := range rows {
		key := asStr(r["key"])
		if exactKeys[key] {
			continue
		}
		kw := store.Row(r)["keywords"].(string)
		title := asStr(r["title"])
		sim, ok := fuzzyEntryScore(input, kw, title)
		if !ok {
			continue
		}
		cands = append(cands, entry{
			key:     key,
			title:   title,
			content: asStr(r["content"]),
			link:    asStr(r["link_keys"]),
			score:   fuzzyTierScore(prioByKey[key]),
			kw:      kw,
			via:     viaFuzzy,
			sim:     sim,
		})
	}
	return cands
}

// appendVectorHits 第 3 级（默认关闭）：向量召回补齐仍零命中的条目。
// 关闭/不可用/失败时原样返回 cands——对前端零感知（详见 vector.go 头注释）。
func (e *Engine) appendVectorHits(ctx context.Context, cands []entry, input string, rows []store.Row) []entry {
	if !e.vectorEnabled(ctx) {
		return cands
	}
	inCands := map[string]bool{}
	for _, c := range cands {
		inCands[c.key] = true
	}
	for _, vh := range e.vectorRecall(ctx, input) {
		if inCands[vh.key] {
			continue // 已被第 1/2 级收编的条目不重复注入
		}
		for _, r := range rows {
			if asStr(r["key"]) != vh.key {
				continue
			}
			kw := store.Row(r)["keywords"].(string)
			cands = append(cands, entry{
				key:     vh.key,
				title:   asStr(r["title"]),
				content: asStr(r["content"]),
				link:    asStr(r["link_keys"]),
				score:   vectorTierScore(toInt(r["priority"], 5)),
				kw:      kw,
				via:     viaVector,
				sim:     vh.sim,
			})
			break
		}
	}
	return cands
}

// logRecall 命中分数观测（分级召回改造的「可观测」交付物）：
// exact-only 走 Debug（默认日志级别下不刷屏）；有 fuzzy/vector 参与走 Info，
// 运营调阈值、排查「为什么答非所问」时按 trace_id 可直接看到通道与分数。
func (e *Engine) logRecall(ctx context.Context, input string, cands []entry) {
	if len(cands) == 0 {
		return
	}
	soft := false
	parts := make([]string, 0, len(cands))
	for _, c := range cands {
		if c.via != viaExact {
			soft = true
		}
		parts = append(parts, fmt.Sprintf("%s:%s:%d:%.2f", c.key, c.via, c.score, c.sim))
	}
	in := input
	if len([]rune(in)) > 40 {
		in = string([]rune(in)[:40])
	}
	if soft {
		observability.Info(ctx, "assist.engine 分级召回命中", "input", in, "hits", strings.Join(parts, "|"))
	} else {
		observability.Log(ctx, slog.LevelDebug, "assist.engine 召回命中(exact)", "input", in, "hits", strings.Join(parts, "|"))
	}
}

// MatchScript 命中单条话术（keyword 类，命中即返回优先级最高的一条）
func (e *Engine) MatchScript(input string) (store.Row, bool) {
	rows, err := e.db.List("scripts", true)
	if err != nil {
		return nil, false
	}
	var best store.Row
	bestScore := 0
	for i := range rows {
		r := rows[i]
		if asStr(r["stype"]) != "keyword" {
			continue
		}
		hits := e.hitScore(input, asStr(r["keywords"]))
		if hits > 0 {
			score := hits*10 + 10 - toInt(r["priority"], 5)
			if score > bestScore {
				bestScore = score
				best = r
			}
		}
	}
	return best, best != nil
}

// Greeting 欢迎词（greeting 类话术第一条 enabled）
func (e *Engine) Greeting() string {
	rows, err := e.db.List("scripts", true)
	if err != nil {
		return "你好，我是能言 AI 助手，有什么可以帮你？"
	}
	for _, r := range rows {
		if asStr(r["stype"]) == "greeting" {
			return asStr(r["content"])
		}
	}
	return "你好，我是能言 AI 助手，有什么可以帮你？"
}

// ============================================================
// 功能入口：按 key 批量取出（供动作按钮）
// ============================================================

// FeatureLinksByKey 按 key 集合取功能入口
func (e *Engine) FeatureLinksByKey(keys []string) []Action {
	rows, err := e.db.List("feature_links", true)
	if err != nil {
		return nil
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var out []Action
	for _, r := range rows {
		if want[asStr(r["key"])] {
			out = append(out, Action{
				Key:   asStr(r["key"]),
				Name:  asStr(r["name"]),
				URL:   asStr(r["url"]),
				FType: asStr(r["ftype"]),
				Icon:  asStr(r["icon"]),
			})
		}
	}
	return out
}

// ============================================================
// 流程：触发词命中 → 按步骤推进
// ============================================================

// FlowStep 流程步骤
type FlowStep struct {
	Ask     string   `json:"ask"`     // 本步向用户说什么/问什么
	Wait    bool     `json:"wait"`    // 是否等用户回答后再进下一步
	Actions []string `json:"actions"` // 本步展示的功能入口 keys
	Keys    []string `json:"keys"`    // 本步注入的知识库 key（给 LLM 当素材）
}

// MatchFlow 按触发词匹配流程（返回 key + 第一步）
func (e *Engine) MatchFlow(input string) (string, []FlowStep, bool) {
	rows, err := e.db.List("flows", true)
	if err != nil {
		return "", nil, false
	}
	for _, r := range rows {
		if e.hitScore(input, asStr(r["trigger_keywords"])) > 0 {
			steps, err := parseSteps(asStr(r["steps_json"]))
			if err == nil && len(steps) > 0 {
				return asStr(r["key"]), steps, true
			}
		}
	}
	return "", nil, false
}

// FlowByKey 按 key 取流程
func (e *Engine) FlowByKey(key string) (string, []FlowStep, bool) {
	rows, err := e.db.List("flows", true)
	if err != nil {
		return "", nil, false
	}
	for _, r := range rows {
		if asStr(r["key"]) == key {
			steps, err := parseSteps(asStr(r["steps_json"]))
			if err == nil && len(steps) > 0 {
				return asStr(r["name"]), steps, true
			}
		}
	}
	return "", nil, false
}

// parseSteps 解析流程步骤 JSON（steps_json 字段）
func parseSteps(js string) ([]FlowStep, error) {
	var steps []FlowStep
	if err := json.Unmarshal([]byte(js), &steps); err != nil {
		return nil, err
	}
	return steps, nil
}

// ============================================================
// 主入口：Respond
// 优先级：进行中的流程 > 话术直配 > 流程触发 > LLM+知识库
// ============================================================

// Respond 生成回复
// 优先级：进行中的流程（命中新意图则让位） > 话术直配 > 流程触发 > LLM+知识库
// ★ 改造 1A：签名加 ctx（HTTP 请求上下文），使 LLM 调用链日志继承 trace_id。
func (e *Engine) Respond(ctx context.Context, sessionID, input, pageURL string, history []store.Row) *Reply {
	// 1. 进行中的流程：输入命中其他意图（话术/其他流程）则退出流程让位，否则推进步骤
	if rep := e.advanceFlow(sessionID, input); rep != nil {
		return rep
	}
	// 2. 关键词话术直配（免 LLM，毫秒级）
	if sc, ok := e.MatchScript(input); ok {
		// ★ 2026-09-20 生产漏接修复：复合意图（如「印度语能翻译吗，一个字多少钱」=
		// 语言能力 + 价格）命中话术时，若知识库还检索到另一领域的条目，直配单话术
		// 会只答一半——把话术降为素材之一，让位给 LLM 融合应答
		// （LLM 未接入时 fallback 也会把两侧知识并排拼出）。
		if scEntry, comp := e.compoundIntent(ctx, input, sc); comp {
			hits := []entry{scEntry}
			for _, h := range e.retrieveKB(ctx, input, 3) {
				if h.key != scEntry.key && h.title != scEntry.title { // 同一内容既配话术又进知识库时不重复注入
					hits = append(hits, h)
				}
			}
			if len(hits) > 4 {
				hits = hits[:4]
			}
			return e.llmReplyWith(ctx, input, history, hits)
		}
		content := asStr(sc["content"])
		if content == "" {
			content = asStr(sc["title"])
		}
		actions := e.FeatureLinksByKey(splitKeys(asStr(sc["link_keys"])))
		return &Reply{Content: content, Actions: actions, Source: "rule"}
	}
	// 3. 流程触发
	if key, steps, ok := e.MatchFlow(input); ok {
		return e.enterFlow(sessionID, key, steps)
	}
	// 4. LLM + 知识库
	return e.llmReply(ctx, input, history)
}

// advanceFlow 推进进行中的流程；不在流程中或被新意图抢占（已退出）时返回 nil
func (e *Engine) advanceFlow(sessionID, input string) *Reply {
	sess, err := e.db.SessionRow(sessionID)
	if err != nil || sess == nil {
		return nil
	}
	flowKey := asStr(sess["in_flow"])
	if flowKey == "" {
		return nil
	}
	// 流程让位：输入命中关键词话术或另一条流程的触发词 → 退出当前流程，按新意图处理
	if _, ok := e.MatchScript(input); ok {
		_ = e.db.SetFlow(sessionID, "", 0)
		return nil
	}
	if other, _, ok := e.MatchFlow(input); ok && other != flowKey {
		_ = e.db.SetFlow(sessionID, "", 0)
		return nil
	}
	_, steps, ok := e.FlowByKey(flowKey)
	if !ok {
		_ = e.db.SetFlow(sessionID, "", 0)
		return nil
	}
	step := toInt(sess["flow_step"], 0)

	// 用户刚回答完当前步（wait 步），进下一步
	next := step + 1
	if next >= len(steps) {
		// 流程走完
		_ = e.db.SetFlow(sessionID, "", 0)
		return &Reply{
			Content: "好，以上就介绍完啦。你可以直接去试试，有问题随时问我～",
			Actions: e.FeatureLinksByKey(steps[len(steps)-1].Actions),
			Flow:    flowKey,
			Source:  "flow",
		}
	}
	return e.renderStep(sessionID, flowKey, steps, next)
}

// enterFlow 进入流程第一步
func (e *Engine) enterFlow(sessionID, key string, steps []FlowStep) *Reply {
	return e.renderStep(sessionID, key, steps, 0)
}

// renderStep 渲染流程某一步
func (e *Engine) renderStep(sessionID, flowKey string, steps []FlowStep, idx int) *Reply {
	st := steps[idx]
	_ = e.db.SetFlow(sessionID, flowKey, idx)
	// 注入本步 keys 的知识内容（如有），追加在 ask 后
	extra := ""
	for _, k := range st.Keys {
		if row, ok := e.kbByKey(k); ok {
			extra += "\n" + asStr(row["content"])
		}
	}
	content := st.Ask + extra
	return &Reply{
		Content: content,
		Actions: e.FeatureLinksByKey(st.Actions),
		Flow:    flowKey,
		Source:  "flow",
	}
}

// kbByKey 按 key 取单条知识
func (e *Engine) kbByKey(key string) (store.Row, bool) {
	rows, err := e.db.List("kb_entries", true)
	if err != nil {
		return nil, false
	}
	for i := range rows {
		if asStr(rows[i]["key"]) == key {
			return rows[i], true
		}
	}
	return nil, false
}

// compoundIntent 判定「话术直配是否该为新意图让位」并给出融合素材：
// 在知识库前几条命中里找与话术关键词零交集的跨领域条目；只要该条目真实命中
// （hitScore≥1）即判复合。刻意不比命中数或总分——计费域知识经多轮运营关键词
// 越滚越大（价格/多少钱/一个字…），数值对比会让同域大条目永远压过新领域小条目，
// 「问了两件事却只答一件」正是生产踩过的坑。复合时话术降为素材之一，连同跨领域
// 知识一起交给融合应答。纯单意图（如只问「多少钱」，命中全属计费域、无跨领域
// 竞争者）不让位，维持毫秒级直配快答。
// 返回 true 时素材即话术 entry（调用方拼在 RetrieveKB 结果前即可）。
// ★ 分级召回（2026-09-22）：让位判定只认第 1 级精确/同义词命中（via==exact），
// fuzzy/vector 弱信号不得触发让位——维持「单意图直配、双意图融合」的既有语义。
func (e *Engine) compoundIntent(ctx context.Context, input string, sc store.Row) (entry, bool) {
	hits := e.retrieveKB(ctx, input, 4)
	if len(hits) == 0 {
		return entry{}, false
	}
	scKws := kwTokenSet(asStr(sc["keywords"]))
	var cross *entry
	for i := range hits {
		h := &hits[i]
		if h.via != viaExact {
			continue // ★ 分级召回约束：让位判定只认第 1 级真实命中——fuzzy/vector 是弱信号，
		}
		// 若允许弱信号触发让位，「多少钱」这类单意图快答可能被近义知识带偏，
		// 破坏 CI2/UAT A2 的毫秒级直配语义
		sameDomain := false
		for tok := range kwTokenSet(h.kw) {
			if scKws[tok] {
				sameDomain = true // 与话术同领域（如价格话术 vs 积分计费知识），不构成复合
				break
			}
		}
		if !sameDomain {
			cross = h // RetrieveKB 已按分数排好序，取最高分的跨领域命中
			break
		}
	}
	if cross == nil {
		return entry{}, false
	}
	content := asStr(sc["content"])
	if content == "" {
		content = asStr(sc["title"])
	}
	return entry{
		key:     asStr(sc["key"]), // ★ 分级召回：素材带 key 供融合链路去重与观测归因
		title:   asStr(sc["title"]),
		content: content,
		link:    asStr(sc["link_keys"]),
		score:   cross.score, // 与让位对象同权重，作为素材并列进 prompt/兜底
		kw:      asStr(sc["keywords"]),
	}, true
}

// kwTokenSet 关键词串转小写 token 集合（比对两域关键词是否相交用）
func kwTokenSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, k := range strings.Split(s, ",") {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			set[k] = true
		}
	}
	return set
}

// ============================================================
// LLM 回复：系统 prompt + 检索知识 + 历史对话
// ============================================================

// llmReply 组装 prompt 调 LLM；无 LLM 或失败走规则兜底
// ★ 改造 1A：签名加 ctx，LLM 调用链日志带 trace_id。
func (e *Engine) llmReply(ctx context.Context, input string, history []store.Row) *Reply {
	return e.llmReplyWith(ctx, input, history, e.retrieveKB(ctx, input, 3))
}

// llmReplyWith 同 llmReply，但素材检索结果由调用方给定
// （★ 复合意图让位时传入「话术素材 + 检索知识」合并表，避免二次检索丢序）
func (e *Engine) llmReplyWith(ctx context.Context, input string, history []store.Row, hits []entry) *Reply {
	sys := e.buildSystemPrompt(hits)
	var msgs []llm.Message
	msgs = append(msgs, llm.Message{Role: "system", Content: sys})
	// 历史最近 6 条
	if len(history) > 6 {
		history = history[len(history)-6:]
	}
	for _, h := range history {
		role := asStr(h["role"])
		if role != "user" && role != "assistant" {
			continue
		}
		msgs = append(msgs, llm.Message{Role: role, Content: asStr(h["content"])})
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: input})

	// 全量知识 key 表（供 LLM 选择跳转动作）
	allKeys := e.allKBKeys()
	if allKeys != "" {
		msgs[len(msgs)-1].Content += "\n\n（回答末尾如需推荐功能，另起一行输出【go:key1,key2】，key 从：" + allKeys + " 中选。不需要就不输出。）"
	}

	client := e.ensureLLM(ctx) // ★ R0.4 惰性重建（管理台 LLM 配置热加载）
	if client.Enabled() {
		temp := 0.7
		if v := e.db.GetConfig("temperature", ""); v != "" {
			fmt.Sscanf(v, "%f", &temp)
		}
		maxTok := 400
		if v := e.db.GetConfig("max_tokens", ""); v != "" {
			fmt.Sscanf(v, "%d", &maxTok)
		}
		text, model, _, err := client.Chat(ctx, temp, maxTok, msgs)
		if err == nil && strings.TrimSpace(text) != "" {
			return e.postProcess(text, model)
		}
		observability.Warn(ctx, "assist.engine LLM 调用失败，走规则兜底", "err", err)
	}
	// 规则兜底：检索命中直接拼
	return e.fallbackReply(hits, input)
}

// buildSystemPrompt 系统提示词（人设 + 知识 + 铁律），风格借鉴 ai-scrm prompt_builder
func (e *Engine) buildSystemPrompt(hits []entry) string {
	var sb strings.Builder
	sb.WriteString(e.db.GetConfig("persona", "你是「能言」AI翻译平台的销售顾问兼使用指导助手，微信聊天风格，真诚接地气，帮用户选对功能、用顺产品。"))
	sb.WriteString("\n\n【语气铁律】\n1. 说「你」不说「您」，短句为主，一句不超过20个字\n2. 2-4句话，120字以内，别啰嗦\n3. 不用客服腔（「亲」「呢」「哦」「哈」），不用emoji堆砌\n4. 回答要基于下面给的知识，不确定的不要编造，说「这个我帮你确认下」\n5. 用户问怎么操作时，给出具体步骤；能跳转的主动推荐功能入口\n6. 用户要买/充值/价格，说清楚积分口径，引导到对应页面\n\n")
	if len(hits) > 0 {
		sb.WriteString("【相关知识】\n")
		for _, h := range hits {
			content := []rune(h.content)
			if len(content) > 500 {
				content = content[:500]
			}
			sb.WriteString("- [" + h.title + "] " + string(content) + "\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("直接回复用户：")
	return sb.String()
}

// postProcess 提取【go:...】动作标记，剥离正文
// 注意【与】均为 3 字节 rune：标记头 "【go:" 共 6 字节，尾部 "】" 3 字节
func (e *Engine) postProcess(text, model string) *Reply {
	content := text
	var actions []Action
	if i := strings.Index(text, "【go:"); i >= 0 {
		if end := strings.Index(text[i:], "】"); end > 0 {
			keys := splitKeys(text[i+6 : i+end])
			actions = e.FeatureLinksByKey(keys)
			content = strings.TrimSpace(text[:i] + text[i+end+3:])
		}
	}
	return &Reply{Content: content, Actions: actions, Model: model, Source: "llm"}
}

// fallbackReply 规则兜底：检索命中直接拼。
// ★ R0.2：零命中时不再空承诺「稍后确认」，改为引导提问 + 快捷入口，
// 并把该输入记入 configs:unanswered_questions（去重上限 200 条）供管理台运营补料。
func (e *Engine) fallbackReply(hits []entry, input string) *Reply {
	if len(hits) == 0 {
		e.recordUnanswered(input)
		return &Reply{
			Content: "这个问题我还没学到，先记下来，学完就能答你啦。\n你可以换个说法问，或先看看这几个入口：",
			Actions: e.FeatureLinksByKey([]string{"chat", "pricing", "register"}),
			Source:  "fallback",
		}
	}
	var sb strings.Builder
	var keys []string
	for i, h := range hits {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(h.content)
		keys = append(keys, splitKeys(h.link)...)
	}
	return &Reply{Content: sb.String(), Actions: e.FeatureLinksByKey(dedup(keys)), Source: "fallback"}
}

// recordUnanswered 未答问题登记（R0.2 运营闭环）：
// configs.unanswered_questions 追加去重，上限 200 条（FIFO 丢弃最旧）。
// 管理台「会话记录」页展示清单，运营据此补知识库。
func (e *Engine) recordUnanswered(input string) {
	const key = "unanswered_questions"
	const cap = 200
	cur := e.db.GetConfig(key, "")
	// 去重：同样的问题不重复登记
	for _, line := range strings.Split(cur, "\n") {
		if strings.TrimSpace(line) == strings.TrimSpace(input) {
			return
		}
	}
	lines := []string{}
	if cur != "" {
		lines = strings.Split(cur, "\n")
	}
	lines = append(lines, strings.ReplaceAll(strings.TrimSpace(input), "\n", " "))
	if len(lines) > cap {
		lines = lines[len(lines)-cap:]
	}
	_ = e.db.SetConfig(key, strings.Join(lines, "\n"))
}

// UnansweredQuestions 导出未答问题清单（管理台用）
func (e *Engine) UnansweredQuestions() []string {
	out := []string{}
	for _, l := range strings.Split(e.db.GetConfig("unanswered_questions", ""), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

// LLMMode 当前 LLM 接入来源（R0.3 徽标）："env" / "db" / ""（规则模式）
func (e *Engine) LLMMode(ctx context.Context) string {
	client := e.ensureLLM(ctx)
	if client == nil || !client.Enabled() {
		return ""
	}
	if e.llmBuilt && e.llmFP != "" {
		return "db"
	}
	return "env"
}

// LLMTest 测试连通（R0.4c）：用当前生效配置发 1-token 请求
func (e *Engine) LLMTest(ctx context.Context) (string, string, llm.Usage, error) {
	client := e.ensureLLM(ctx)
	if client == nil || !client.Enabled() {
		return "", "", llm.Usage{}, fmt.Errorf("LLM 未接入（规则模式）——请在下方填入 Base URL / API Key / 模型名")
	}
	msgs := []llm.Message{{Role: "user", Content: "回复「OK」两个字"}}
	return client.Chat(ctx, 0, 8, msgs)
}

// allKBKeys 全部知识 key（供 LLM 动作标记）
func (e *Engine) allKBKeys() string {
	rows, err := e.db.List("kb_entries", true)
	if err != nil {
		return ""
	}
	var ks []string
	for _, r := range rows {
		if k := asStr(r["key"]); k != "" {
			ks = append(ks, k)
		}
	}
	return strings.Join(ks, ",")
}

// ============================================================
// 工具
// ============================================================

// asStr 动态行取值转字符串（nil 安全）
func asStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// toInt 动态行取值转整数（int/int64/float64/数字串均兼容，失败回落默认）
func toInt(v any, def int) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		var x int
		if _, err := fmt.Sscanf(n, "%d", &x); err == nil {
			return x
		}
	}
	return def
}

// splitKeys 逗号分隔串转 key 切片（去空格、去空项）
func splitKeys(s string) []string {
	parts := strings.Split(s, ",")
	out := []string{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// dedup 保序去重（合并多条知识的 link_keys 用）
func dedup(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
