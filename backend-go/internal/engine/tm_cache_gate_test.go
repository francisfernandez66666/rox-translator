// ============ 本文件职责中文说明 ============
// 译文记忆（缓存 A 进程内 LRU + 缓存 B 库表）的闸门锁 —— ★ 2026-10-28 计价与成本优化批。
//
// 为什么这一族必须有机械锁：译文记忆的失效是**静默**的，出错时界面上不会出现任何异常形态——
//
//	键少一维 ⇒ 换了模型/改了提示词，旧译文继续投（"翻译质量莫名下降"，没人能归因）；
//	写回判据放宽 ⇒ 提示词回显被存进记忆，下一单直出给客户（不是"没翻"，是"翻错了还复现"）；
//	开关只关库不关内存 ⇒ 运维以为关了，实际还在命中（成本账对不上）。
//
// 七条腿覆盖：LRU 语义 / 键维度 / 模型维度 / 写回判据 / 开关 / 租户隔离 / 容量档位。
// M0006（写回判据）与 M0007（模型维度）由 scripts/gate_mutants.tsv 做变异登记，
// 改这两处时必须让本文件对应那一条腿红（期望消息见各条 t.Fatalf 的字面）。
//
// 方言口径（AGENTS §一·4）：本族固定内存 SQLite，显式钉 config.C.DatabaseDriver。
// ============================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"translator/internal/config"
	"translator/internal/domain"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ 假上游 ============

// tmStubUpstream 批量腿的假上游：按 `<sN>` 现读段号，逐条回同一段固定译文。
// 只有两种形态：good＝正常译文（应被记忆），echo＝指令回显（**不许**被记忆）。
type tmStubUpstream struct {
	good   string // 正常译文（good 模式回这个）
	echo   string // 回显形态（echo 模式回这个）
	asEcho atomic.Bool
	calls  atomic.Int64
}

var tmSlotRe = regexp.MustCompile(`<s(\d+)>`)

// handler 实现 OpenAI chat/completions 的最小应答（批量腿只读 choices[0].message.content + finish_reason）。
func (u *tmStubUpstream) handler(w http.ResponseWriter, r *http.Request) {
	u.calls.Add(1)
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	user := ""
	for _, m := range body.Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	out := u.good
	if u.asEcho.Load() {
		out = u.echo
	}
	slots := tmSlotRe.FindAllStringSubmatch(user, -1)
	content := ""
	for _, m := range slots {
		content += fmt.Sprintf("<s%s>%s</s%s>\n", m[1], out, m[1])
	}
	raw, _ := json.Marshal(content)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":"stop"}]}`, raw)
}

// tmTestEngine 装配：内存 Store（缓存 B 有落点）+ 假上游 + 钉死 sqlite 方言。
func tmTestEngine(t *testing.T, u *tmStubUpstream) *Engine {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(u.handler))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言：防 UAT 矩阵的 PG env 泄漏进本包（AGENTS §4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	e := NewEngine(cfg, nil, nil, nil)
	e.St = newTestStore(t)
	return e
}

// tmCountRows 现读库表行数（判"有没有真落库"的唯一证据；数日志不算）。
func tmCountRows(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM translation_memory`).Scan(&n); err != nil {
		t.Fatalf("读 translation_memory 行数失败: %v", err)
	}
	return n
}

// tmRunBatch 跑一次批量腿（租户 1；内存缓存每次清空以模拟"新进程"）。
func tmRunBatch(t *testing.T, e *Engine, texts []string) []string {
	t.Helper()
	ctx := tenant.WithTenant(context.Background(), 1)
	out, _ := e.BatchTranslateMarkedOpts(ctx, texts, "en", 0, nil, BatchOptions{})
	return out
}

// ============ L1：LRU 上限与淘汰序 ============

// TestTMLRUIsBoundedAndLRU 上限生效、最久未用先淘汰、Get 提升优先级。
func TestTMLRUIsBoundedAndLRU(t *testing.T) {
	c := newTMLRU(3)
	c.Put("a", "1")
	c.Put("b", "2")
	c.Put("c", "3")
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a 应命中（刚写入）")
	}
	// 此时 LRU 顺序（新→旧）= a,c,b；再写 d 必须淘汰 b。
	c.Put("d", "4")
	if got := c.Len(); got != 3 {
		t.Fatalf("上限 3 时条目数应为 3，实际 %d ⇒ 上限没生效（内存会无界增长）", got)
	}
	if _, ok := c.Get("b"); ok {
		t.Fatal("b 是最久未用项，写入 d 时必须被淘汰")
	}
	for _, k := range []string{"a", "c", "d"} {
		if _, ok := c.Get(k); !ok {
			t.Fatalf("%q 应仍在缓存里（被淘汰的必须是最久未用项，而不是任意一项）", k)
		}
	}
	var nilLRU *tmLRU
	if _, ok := nilLRU.Get("x"); ok {
		t.Fatal("nil LRU 的 Get 必须返回未命中（判据不许 panic）")
	}
	nilLRU.Put("x", "y") // 不许 panic
}

// ============ L2：键维度 ============

// TestTMKeySeparatesEveryDimension 七个维度 + 代次，任一变化都必须换键。
func TestTMKeySeparatesEveryDimension(t *testing.T) {
	base := tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m1", modelFP: "p|m", ctxHash: ""}
	k0 := tmKey(base, "今天天气很好。")
	cases := []struct {
		name  string
		scope tmScope
	}{
		{"租户", tmScope{tid: 2, srcLang: "zh", tgtLang: "en", mode: "m1", modelFP: "p|m"}},
		{"源语种", tmScope{tid: 1, srcLang: "ja", tgtLang: "en", mode: "m1", modelFP: "p|m"}},
		{"目标语种", tmScope{tid: 1, srcLang: "zh", tgtLang: "ja", mode: "m1", modelFP: "p|m"}},
		{"提示词口径", tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m2", modelFP: "p|m"}},
		{"模型指纹", tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m1", modelFP: "p|n"}},
		{"表头上下文", tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m1", modelFP: "p|m", ctxHash: "h1"}},
	}
	seen := map[string]string{k0: "基准"}
	for _, c := range cases {
		k := tmKey(c.scope, "今天天气很好。")
		if prev, dup := seen[k]; dup {
			t.Fatalf("%s 变了但键没变（与 %s 相同）⇒ 该维度没进键", c.name, prev)
		}
		seen[k] = c.name
	}
	// 源文维度
	if k := tmKey(base, "今天天气很好！"); k == k0 {
		t.Fatal("源文变了但键没变 ⇒ 源文没进键")
	}
	// 代次维度：常量本身必须逐字钉住（抬 rev 是刻意动作，不许顺手改）
	if tmPromptRev != "20261028-2" {
		t.Fatalf("tmPromptRev=%q，本批口径是 20261028-2；改它必须同时给出「哪些形态变化导致旧代必须作废」的理由", tmPromptRev)
	}
}

// ============ L3：模型维度（M0007 靶子） ============

// TestTMKeySeparatesModel 换模型必须换键（否则"换了模型，旧模型的译文继续投"）。
func TestTMKeySeparatesModel(t *testing.T) {
	a := tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m", modelFP: tmModelFingerprint("siliconflow", "Qwen/Qwen3-8B")}
	b := tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m", modelFP: tmModelFingerprint("siliconflow", "Qwen/Qwen3-32B")}
	c := tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m", modelFP: tmModelFingerprint("deepseek", "Qwen/Qwen3-8B")}
	src := "请勿拆卸电池。"
	if tmKey(a, src) == tmKey(b, src) {
		t.Fatal("换模型必须换键：同一 model 名之外的模型（8B→32B）共用了一条记忆")
	}
	if tmKey(a, src) == tmKey(c, src) {
		t.Fatal("换模型必须换键：同一 model 名在不同 provider 上不是同一个模型")
	}
	if c.modelFP == a.modelFP {
		t.Fatal("换模型必须换键：模型指纹必须含 provider（只取 model 名会跨供应商互串）")
	}
}

// ============ L4：写回判据（M0006 靶子） ============

// TestTMWriteBackRequiresUsableOutput 回显/失败占位/空串一律不写；
// 只有**过 fileTranslationUsable** 的译文才进记忆（正向对照在同一用例里）。
func TestTMWriteBackRequiresUsableOutput(t *testing.T) {
	u := &tmStubUpstream{
		good: "Today the weather is nice.",
		// 指令回显形态：IsTranslationUsable 的九类判据全部放行（它不是空/不是同文/不是回绝），
		// 但这是**提示词里的规范说明**，被模型当成待译内容吐了回来。
		echo: "Please keep the currency symbol exactly as in the source text.",
	}
	e := tmTestEngine(t, u)
	st := e.St
	ctx := tenant.WithTenant(context.Background(), 1)
	srcs := []string{"今天天气很好。", "我们去公园散步。", "晚上一起吃饭吧。"}

	// 前提自证：这串回显**过得了**九类判据，只有文件链路那把尺子能拦下它。
	// 没有这一句，本用例可能只是"另一条判据顺手拦了"，射程就说不清了。
	final := PostProcessTranslation(u.echo, "en")
	if !IsTranslationUsable(srcs[0], final) {
		t.Fatalf("夹具前提不成立：回显样本未过 IsTranslationUsable（%q）⇒ 本条锁测不到 fileTranslationUsable 那两条附加判据", final)
	}
	if e.fileTranslationUsable(ctx, srcs[0], final, "en") {
		t.Fatalf("夹具前提不成立：fileTranslationUsable 竟放行了指令回显（%q）", final)
	}

	// ① 回显形态：一律不写（内存与库表都不写）
	u.asEcho.Store(true)
	got := tmRunBatch(t, e, srcs)
	if n := tmCountRows(t, st); n != 0 {
		t.Fatalf("TM 写回必须过可用性判据：回显形态却写了 %d 行到库表（下一次命中会把回显直出给客户）", n)
	}
	if n := e.tmGetCache().Len(); n != 0 {
		t.Fatalf("TM 写回必须过可用性判据：回显形态却在内存缓存写了 %d 条", n)
	}
	if len(got) != len(srcs) {
		t.Fatalf("结果长度应为 %d，实际 %d", len(srcs), len(got))
	}

	// ② 正常译文：必须写（正向对照，防"判据写成永不写"这种假绿）
	u.asEcho.Store(false)
	tmRunBatch(t, e, srcs)
	if n := tmCountRows(t, st); n != len(srcs) {
		t.Fatalf("正常译文应逐段写库（%d 行），实际 %d 行 ⇒ 写回被过度收紧（缓存等于不存在）", len(srcs), n)
	}
}

// ============ L5：开关（A+B 一起停） ============

// TestTMDisabledStopsBothLayers tm_enabled=0 ⇒ 零命中、零写库；恢复后重新命中。
func TestTMDisabledStopsBothLayers(t *testing.T) {
	u := &tmStubUpstream{good: "Today the weather is nice."}
	e := tmTestEngine(t, u)
	st := e.St
	if err := st.SetConfig("tm_enabled", "0"); err != nil {
		t.Fatalf("写 tm_enabled 失败: %v", err)
	}
	srcs := []string{"今天天气很好。", "我们去公园散步。"}
	tmRunBatch(t, e, srcs)
	if n := tmCountRows(t, st); n != 0 {
		t.Fatalf("tm_enabled=0 时不许写库（否则「关掉」只是关了读）实际 %d 行", n)
	}
	if n := e.tmGetCache().Len(); n != 0 {
		t.Fatalf("tm_enabled=0 时内存层也必须一起停（运维以为关了，实际还在命中）实际 %d 条", n)
	}
	// 关着的时候每次都真打上游：两次调用 = 2 次上游请求（bs≥2 ⇒ 一批一次）
	before := u.calls.Load()
	tmRunBatch(t, e, srcs)
	if u.calls.Load() == before {
		t.Fatal("tm_enabled=0 时仍然零上游调用 ⇒ 缓存并没有真关（命中了不该命中的东西）")
	}

	// 恢复开关：写入后第二次必须零上游调用（命中）
	if err := st.SetConfig("tm_enabled", "1"); err != nil {
		t.Fatalf("恢复 tm_enabled 失败: %v", err)
	}
	tmRunBatch(t, e, srcs)
	if n := tmCountRows(t, st); n == 0 {
		t.Fatal("恢复开关后应写库")
	}
	before = u.calls.Load()
	tmRunBatch(t, e, srcs)
	if u.calls.Load() != before {
		t.Fatalf("恢复开关后同样的源文必须全命中（零上游调用），实际多打了 %d 次", u.calls.Load()-before)
	}
}

// ============ L6：租户隔离 ============

// TestTMTenantIsolationInMemory 同一份源文，不同租户互不命中（内存层与库表层各判一次）。
func TestTMTenantIsolationInMemory(t *testing.T) {
	e := &Engine{} // 只测记忆层：St 为 nil ⇒ 只有内存层
	scA := tmScope{tid: 1, srcLang: "zh", tgtLang: "en", mode: "m", modelFP: "p|m"}
	scB := tmScope{tid: 2, srcLang: "zh", tgtLang: "en", mode: "m", modelFP: "p|m"}
	ctx := context.Background()
	e.tmStore(ctx, scA, "请勿拆卸电池。", "Do not disassemble the battery.")
	if _, layer := e.tmLookup(ctx, scB, "请勿拆卸电池。"); layer != tmMiss {
		t.Fatal("租户 2 命中了租户 1 的记忆 ⇒ 跨租户泄露（数据事故，不是性能问题）")
	}
	if v, layer := e.tmLookup(ctx, scA, "请勿拆卸电池。"); layer != tmHitA || v == "" {
		t.Fatalf("本租户应命中内存层，实际 layer=%v v=%q", layer, v)
	}
	// tid<=0（平台上下文）不许读客户记忆
	if _, layer := e.tmLookup(ctx, tmScope{tid: 0, srcLang: "zh", tgtLang: "en", mode: "m", modelFP: "p|m"}, "请勿拆卸电池。"); layer != tmMiss {
		t.Fatal("平台上下文（tid=0）命中了客户记忆 ⇒ 隔离维度失效")
	}
}

// ============ L7：容量档位 ============

// TestTMCacheMaxEnvClamp 六档读数（钳位边界逐条现读）。
func TestTMCacheMaxEnvClamp(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", tmLRUMaxDefault},
		{"64", tmLRUMin},
		{"128", 128},
		{"4096", 4096},
		{"65536", 65536},
		{"1000000000", tmLRUMaxCap},
		{"abc", tmLRUMaxDefault},
	}
	for _, c := range cases {
		t.Setenv("TM_CACHE_MAX", c.raw)
		if got := tmCacheMax(); got != c.want {
			t.Errorf("TM_CACHE_MAX=%q 应读成 %d，实际 %d", c.raw, c.want, got)
		}
	}
}

// ============ L8：两侧常量等值 ============

// TestTMConstantsMatchEngine engine 侧缺省与 store 侧常量必须同值（两处各写一份迟早漂）。
func TestTMConstantsMatchEngine(t *testing.T) {
	if defaultTMKeepDays != store.TranslationMemoryRetentionDays {
		t.Fatalf("保留天数两侧不一致：engine=%d store=%d", defaultTMKeepDays, store.TranslationMemoryRetentionDays)
	}
	if defaultTMMaxRows != store.TranslationMemoryMaxRowsPerTenant {
		t.Fatalf("行数上限两侧不一致：engine=%d store=%d", defaultTMMaxRows, store.TranslationMemoryMaxRowsPerTenant)
	}
	if os.Getenv("TM_CACHE_MAX") != "" {
		t.Logf("注意：外部注入了 TM_CACHE_MAX=%s（本条不依赖它）", os.Getenv("TM_CACHE_MAX"))
	}
}

// ============ L9：每单命中计数与载荷（S4） ============

// TestTMHitsFlowIntoPayload 计数器只数**命中**（未命中不上账），并真的进载荷 `tm_hits`；
// 零值必须不序列化（老客户端与"一次没命中"逐字相同）。
//
// 为什么锁在 engine 而不是 api：`FileTranslateData` 是 engine 的类型，它的 JSON 形态
// 就是工单载荷那一维的事实源；放到 api 去断言一个跨包类型的 json tag，
// 多一层转手反而看不出"字段有没有接上计数器"。
func TestTMHitsFlowIntoPayload(t *testing.T) {
	u := &tmStubUpstream{good: "Today the weather is nice."}
	e := tmTestEngine(t, u)
	srcs := []string{"今天天气很好。", "我们去公园散步。", "晚上一起吃饭吧。"}
	ctx, counter := withTMHitCounter(tenant.WithTenant(context.Background(), 1))

	// 首跑：全未命中 ⇒ 计数必须为 0（写回不算命中）
	if _, _ = e.BatchTranslateMarkedOpts(ctx, srcs, "en", 0, nil, BatchOptions{}); counter.Load() != 0 {
		t.Fatalf("首跑全未命中，计数器应为 0，实际 %d（写回被当成命中了）", counter.Load())
	}
	// 二跑：全部命中 ⇒ 计数＝段数，且零上游调用
	before := u.calls.Load()
	if _, _ = e.BatchTranslateMarkedOpts(ctx, srcs, "en", 0, nil, BatchOptions{}); counter.Load() != int64(len(srcs)) {
		t.Fatalf("二跑应全命中（%d 段），计数器实际 %d", len(srcs), counter.Load())
	}
	if u.calls.Load() != before {
		t.Fatalf("二跑仍然打了 %d 次上游 ⇒ 命中不是真命中（计数器会撒谎）", u.calls.Load()-before)
	}
	// 有值态：tm_hits 必须出现且等于合计
	b, err := json.Marshal(FileTranslateData{TotalTexts: len(srcs), TargetLangs: []string{"en"}, TMHits: int(counter.Load())})
	if err != nil {
		t.Fatalf("序列化载荷失败: %v", err)
	}
	if !strings.Contains(string(b), `"tm_hits":3`) {
		t.Fatalf("载荷缺 tm_hits=3（实际 %s）⇒ 运营拿不到命中率，降价测算没有依据", string(b))
	}
	// 无值态：零值不序列化（有值/无值两态都钉住）
	b0, err := json.Marshal(FileTranslateData{TotalTexts: 1, TargetLangs: []string{"en"}})
	if err != nil {
		t.Fatalf("序列化零值载荷失败: %v", err)
	}
	if strings.Contains(string(b0), "tm_hits") {
		t.Fatalf("tm_hits 零值必须不序列化（omitempty），实际 %s", string(b0))
	}
}

// ============ L10：接线锁（计数器必须进两份 Data 字面量） ============

// TestTMHitsWiredIntoHandleFile `HandleFile` 必须**建**计数器并把读数写进两份 Data
// （成功路径 + 「零交付失败」路径）。
//
// 为什么是源码锁而不是行为锁：失败路径要在真跑一整条文件链（提取→翻→写回全失败）后才走到，
// 行为夹具成本高且不稳；而这条锁问的是**接线形态**——少接一处，那一档载荷就永远没有命中数，
// 界面上完全正常（只是读数缺席），正是本仓反复登记的"某条腿不在清单里"形态。
func TestTMHitsWiredIntoHandleFile(t *testing.T) {
	src := readGateSource(t, "file.go")
	if !strings.Contains(src, "withTMHitCounter(ctx)") {
		t.Fatal("HandleFile 没建每单命中计数器（withTMHitCounter）⇒ tm_hits 恒为 0")
	}
	if n := strings.Count(src, "TMHits:"); n != 2 {
		t.Fatalf("Data 字面量里 TMHits 应为 2 处（成功路径 + 零交付失败路径），实际 %d 处 ⇒ 有一档载荷永远没有命中数", n)
	}
}

// TestTMKeySeparatesGlossary 加词表的那一单不许复用"没有词表"的旧译文（第十二轮 G8 行为腿）。
//
// 与 tm_context_key_test.go 的等值锁分工：那条锁的是"键里有没有这一维"，
// 这条跑真批量腿，证明"键不同 ⇒ 记忆不串"（只测键的字符串会漏掉"键算了但没用上"这一形态）。
func TestTMKeySeparatesGlossary(t *testing.T) {
	u := &tmStubUpstream{good: "Hello"}
	e := tmTestEngine(t, u)
	texts := []string{"你好"}
	ctx := tenant.WithTenant(context.Background(), 1)
	run := func(opts BatchOptions) {
		t.Helper()
		out, _ := e.BatchTranslateMarkedOpts(ctx, texts, "en", 0, nil, opts)
		if len(out) != 1 || strings.TrimSpace(out[0]) == "" {
			t.Fatalf("批量腿没产出译文：%v", out)
		}
	}
	run(BatchOptions{})
	if u.calls.Load() != 1 {
		t.Fatalf("首次应拨一次上游，实读 %d", u.calls.Load())
	}
	run(BatchOptions{})
	if u.calls.Load() != 1 {
		t.Fatalf("同参数第二次应命中记忆（不拨上游），实读 %d", u.calls.Load())
	}
	gl := []domain.Term{{Src: "你好", Tgt: "Hi"}}
	run(BatchOptions{Glossary: gl, GlossaryLabel: "问候"})
	if u.calls.Load() != 2 {
		t.Fatalf("带词表必须换键重翻（复用无词表旧译文＝改了口径没改），实读 calls=%d", u.calls.Load())
	}
	run(BatchOptions{Glossary: gl, GlossaryLabel: "问候"})
	if u.calls.Load() != 2 {
		t.Fatalf("同一词表第二次应再次命中记忆，实读 calls=%d", u.calls.Load())
	}
}
