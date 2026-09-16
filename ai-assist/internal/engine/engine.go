// Package engine 接待引擎：知识/话术检索 → Prompt 组装 → LLM/规则兜底 → 流程推进 → 动作提取。
// 核心思想移植自 ai-scrm：策略定方向、大模型定表达、模板兜底保底。
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"ai-assist/internal/llm"
	"ai-assist/internal/store"
)

// Action 回复附带的动作按钮（前端渲染为可点击跳转）
type Action struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	FType string `json:"ftype"`
	Icon  string `json:"icon"`
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
}

// New 构建引擎
func New(db *store.DB, client *llm.Client) *Engine {
	return &Engine{db: db, llm: client}
}

// ============================================================
// 检索：关键词打分（关键词命中数 × 优先级）
// ============================================================

// hitScore 词条与输入的相关度
func hitScore(input string, keywords string) int {
	kws := strings.Split(keywords, ",")
	n := 0
	for _, k := range kws {
		k = strings.TrimSpace(k)
		if k != "" && strings.Contains(input, k) {
			n++
		}
	}
	return n
}

// entry 打分后的候选
type entry struct {
	title   string
	content string
	link    string
	score   int
}

// RetrieveKB 检索知识库 topN（enabled，按命中关键词数+优先级排序）
func (e *Engine) RetrieveKB(input string, topN int) []entry {
	rows, err := e.db.List("kb_entries", true)
	if err != nil {
		return nil
	}
	var cands []entry
	for _, r := range rows {
		kw := store.Row(r)["keywords"].(string)
		hits := hitScore(input, kw)
		if hits == 0 && topN < 99 {
			continue // 无命中跳过（topN>=99 表示全量注入，用于系统 prompt）
		}
		prio := toInt(r["priority"], 5)
		cands = append(cands, entry{
			title:   asStr(r["title"]),
			content: asStr(r["content"]),
			link:    asStr(r["link_keys"]),
			score:   hits*10 + 10 - prio,
		})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	if len(cands) > topN {
		cands = cands[:topN]
	}
	return cands
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
		hits := hitScore(input, asStr(r["keywords"]))
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
		if hitScore(input, asStr(r["trigger_keywords"])) > 0 {
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
func (e *Engine) Respond(sessionID, input, pageURL string, history []store.Row) *Reply {
	// 1. 进行中的流程：输入命中其他意图（话术/其他流程）则退出流程让位，否则推进步骤
	if rep := e.advanceFlow(sessionID, input); rep != nil {
		return rep
	}
	// 2. 关键词话术直配（免 LLM，毫秒级）
	if sc, ok := e.MatchScript(input); ok {
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
	return e.llmReply(input, history)
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

// ============================================================
// LLM 回复：系统 prompt + 检索知识 + 历史对话
// ============================================================

// llmReply 组装 prompt 调 LLM；无 LLM 或失败走规则兜底
func (e *Engine) llmReply(input string, history []store.Row) *Reply {
	// 检索 top3 知识条目
	hits := e.RetrieveKB(input, 3)
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

	if e.llm.Enabled() {
		temp := 0.7
		if v := e.db.GetConfig("temperature", ""); v != "" {
			fmt.Sscanf(v, "%f", &temp)
		}
		maxTok := 400
		if v := e.db.GetConfig("max_tokens", ""); v != "" {
			fmt.Sscanf(v, "%d", &maxTok)
		}
		text, model, _, err := e.llm.Chat(context.Background(), temp, maxTok, msgs)
		if err == nil && strings.TrimSpace(text) != "" {
			return e.postProcess(text, model)
		}
		log.Printf("[engine] LLM 失败: %v，走规则兜底", err)
	}
	// 规则兜底：检索命中直接拼
	return e.fallbackReply(hits)
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

// fallbackReply 规则兜底：直接用检索命中的知识拼回复
func (e *Engine) fallbackReply(hits []entry) *Reply {
	if len(hits) == 0 {
		return &Reply{
			Content: "这个问题我记下了，稍后帮你确认。你可以先说说你想翻译什么内容、翻成什么语言，我帮你看看哪个功能最合适～",
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
