// ============ 本文件职责中文说明 ============
// 译文记忆两级缓存（★ 2026-10-28 计价与成本优化批）：
//
//	缓存 A ＝ 进程内 LRU：活在一次进程生命周期内，命中即零 I/O、零 DB；
//	缓存 B ＝ 库表 translation_memory：跨单、跨重启，按租户隔离（见 store/translation_memory.go）。
//
// 为什么两级而不是只要一级：
//   - 文件链的真实负载是「同一篇手册反复跑」与「同一批术语在多单里重复」。
//     只上库：每段一次 SELECT，一段几百行就是几百次往返，省了 token 却把 CPU/IO 花光；
//     只在内存：重启即清零、跨实例不共享，命中率上不去。两级＝热段走内存、冷段走库。
//
// 口径（改这块前必读）：
//  1. **键必须带「会改变译文的全部输入」**（六维 + 源文，见 tmKey）：
//     租户、源语种、目标语种、**提示词口径指纹**、**模型指纹**、**表头上下文**、提示词代次、源文。
//     少一维就是「换了模型/改了提示词，库里的旧译文继续投」——本仓最典型的静默坏
//     （同 cannedPromptRev 那条）。⚠️ 计费档（pro/fast）**刻意不进键**：它不是翻译输入，
//     进键会让同一段在两种计费档下各存一份、命中率腰斩；真正会改变译文的是"术语层 + 指令口径"。
//  2. **只存可用译文**：写回判据就是文件链路那唯一一把尺子 `e.fileTranslationUsable`
//     （`IsTranslationUsable` + 指令回显 + 长文爆炸）。只过九类判据会让"提示词回显"被存进记忆、
//     下一单直出给客户——那不是"没翻"，是"翻错了还复现"，比漏翻更难发现。
//  3. **Fail-open 方向只能朝「照常调模型」**：读失败＝未命中、写失败＝丢掉一条加速记录，
//     都不许让翻译主链失败（缓存是加速器，不是前置）。
//  4. **开关分级**：`tm_enabled.<租户>` > `tm_enabled` > 默认开；
//     显式 `0/false/off/no` 即关闭该租户（或全站）的译文记忆，**A 与 B 一起停**——
//     "只关库、不关内存"会让运维以为关了而实际还在命中。
//
// ==========================================
package engine

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"translator/internal/observability"
	"translator/internal/store"
)

// tmPromptRev 译文记忆的提示词代次：**任何会改变批量腿最终译文的改动都必须抬这一格**，
// 包括：批量指令（translateInstruction）、termMask 术语层、batchContextNote、
// PostProcessTranslation 的出栈处理、截断二分重试、以及模型档位解析口径。
// 不抬的后果：新二进制上线后，库里旧代译文继续被命中投给客户＝换件没修（cannedPromptRev 同族教训）。
//
// 20261028-2：本批三处形态变化（批量指令 + batchContextNote 口径、写回判据换 fileTranslationUsable、
// 截断二分改变了"一批里哪些段能拿到译文"）⇒ 旧代的每一行都必须当场失效（-1 作废）。
const tmPromptRev = "20261028-2"

// tmLRUMaxDefault 进程内条目上限缺省档。取 4096：按每条约 400B 估算 ≈1.6MB 常驻，
// 对 1G 机器的预算可忽略，同时覆盖「一篇大文档 + 若干热点句」的工作集。
const tmLRUMaxDefault = 4096

// tmLRUMin / tmLRUMaxCap `TM_CACHE_MAX` 的钳位区间。
// 下限 128：再小就等于关掉缓存（命中率会被一篇文档自己冲干净）。
// 上限 65536：≈26MB 常驻，是 1G 机器上还能接受的天花板（发版记录里要写明这是显式选择）。
const (
	tmLRUMin    = 128
	tmLRUMaxCap = 65536
)

// tmEntry LRU 节点值（key 与译文各一份）。
type tmEntry struct {
	key string
	val string
}

// tmLRU 极简并发安全 LRU（container/list + map，无第三方依赖）。
type tmLRU struct {
	mu    sync.Mutex
	max   int
	ll    *list.List
	items map[string]*list.Element
}

// newTMLRU 建一个上限为 max 的 LRU；max<=0 走 tmLRUMaxDefault。
func newTMLRU(max int) *tmLRU {
	if max <= 0 {
		max = tmLRUMaxDefault
	}
	return &tmLRU{max: max, ll: list.New(), items: make(map[string]*list.Element, max)}
}

// Get 取一条并把它提到队首；未命中返回 ("", false)。
func (c *tmLRU) Get(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		if e, ok := el.Value.(*tmEntry); ok {
			return e.val, true
		}
	}
	return "", false
}

// Put 写入/刷新一条；超出上限时淘汰队尾（最久未用）。
func (c *tmLRU) Put(key, val string) {
	if c == nil || key == "" || val == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		if e, ok := el.Value.(*tmEntry); ok {
			e.val = val
		}
		c.ll.MoveToFront(el)
		return
	}
	c.items[key] = c.ll.PushFront(&tmEntry{key: key, val: val})
	for c.ll.Len() > c.max {
		back := c.ll.Back()
		if back == nil {
			break
		}
		if e, ok := back.Value.(*tmEntry); ok {
			delete(c.items, e.key)
		}
		c.ll.Remove(back)
	}
}

// Len 当前条目数（仅测试与健康读数用）。
func (c *tmLRU) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// tmScope 一次批量腿的「记忆作用域」——键里除源文以外的全部维度。
//
// 把七个维度收成一个结构体（而不是七个参数）是有意的：新增一维（本批的 ctxHash 就是）
// 时只改这里与 tmKey，两处调用点（批量腿、逐段兜底腿）自动同口径；
// 七个裸参数分散在两个调用点上，改一处漏一处就是"两个腿各用一把尺子"。
type tmScope struct {
	tid     int64  // 租户（<0 一律不读不写；==0 平台单，落库 tenant_id=0 且只与 0 互查）
	srcLang string // 源语种（批量腿用 DetectSourceLang 的结果）
	tgtLang string // 目标语种
	mode    string // 提示词口径指纹（tmPromptMode(sysBatch)）
	modelFP string // 模型指纹（provider|model）
	ctxHash string // 表头上下文哈希（P5；未启用为 ""）
}

// tmHitLayer 命中层（只服务日志与计数分档）。
type tmHitLayer int

// tmHitLayerValues 命中层三档取值（未命中 / A 层 / B 层）。
// ★ 只许三档：日志与 `tm_hits` 计数按这三档分，加第四种会让「省了多少」对不上账。
const (
	tmMiss tmHitLayer = iota // 未命中
	tmHitA                   // 进程内 LRU 命中
	tmHitB                   // 库表命中
)

// tmGetCache 惰性初始化进程内 LRU（Engine 为进程级单例，首次使用时建一次即可）。
func (e *Engine) tmGetCache() *tmLRU {
	e.tmOnce.Do(func() { e.tm = newTMLRU(tmCacheMax()) })
	return e.tm
}

// tmCacheMax 读取 `TM_CACHE_MAX`（条数），钳 128..65536；空/非法 ⇒ 默认 4096。
// 只影响容量，不影响开关——容量配错最坏是多占/少占内存，不该变成"缓存没了"。
func tmCacheMax() int {
	raw := strings.TrimSpace(os.Getenv("TM_CACHE_MAX"))
	if raw == "" {
		return tmLRUMaxDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return tmLRUMaxDefault
	}
	if n < tmLRUMin {
		return tmLRUMin
	}
	if n > tmLRUMaxCap {
		return tmLRUMaxCap
	}
	return n
}

// tmEnabled 判断该租户是否启用译文记忆（含库表与内存两层）。
// 优先级：tm_enabled.<tid> > tm_enabled > 默认开。
// 值口径：0/false/off/no 一律视为关闭（大小写不敏感）；其余（含空串/未知）视为开。
func (e *Engine) tmEnabled(tid int64) bool {
	if e == nil {
		return false
	}
	if e.St != nil {
		if tid > 0 {
			if v, err := e.St.GetConfig("tm_enabled." + strconv.FormatInt(tid, 10)); err == nil && strings.TrimSpace(v) != "" {
				return !tmOffValue(v)
			}
		}
		if v, err := e.St.GetConfig("tm_enabled"); err == nil && strings.TrimSpace(v) != "" {
			return !tmOffValue(v)
		}
	}
	return true // 默认开（用户令：B 默认开）
}

// tmOffValue 判断配置值是否为「关闭」。
func tmOffValue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// tmPromptMode 批量腿「提示词口径指纹」：直接把**真发出去的那段 system 前缀**哈希掉。
//
// 为什么不是"手写一串版本号"：口径由 translateInstruction + 术语层 + batchContextNote
// 三者拼成，任何一处改了都会变译文；手写版本号要靠人记得抬（cannedPromptRev 已经踩过两次
// "换件没修"）。哈希现算则天然同步——漏抬的风险从"人"转移到"不存在"。
//
// 取 16 hex（8 字节）：它只做等值比较，碰撞概率对"几十种口径"可忽略。
func tmPromptMode(systemPrompt string) string {
	sum := sha256.Sum256([]byte(systemPrompt))
	return hex.EncodeToString(sum[:8])
}

// tmModelFingerprint 模型指纹：`provider|model`。
// 只取 model 名不够——同一 model 名在不同 provider 上是不同的模型（本仓多供应商路由的常态）。
func tmModelFingerprint(provider, model string) string {
	return provider + "|" + model
}

// tmKey 译文记忆键：sha256(租户|源语种|目标语种|提示词口径|模型指纹|表头上下文|代次|源文)。
// 用哈希而不是拼接后的明文：明文键长随段落增长，内存与索引都不经济；
// 哈希后固定 32 hex，且天然规避分隔符注入（源文里带 '|' 不会造成跨字段歧义）。
func tmKey(sc tmScope, text string) string {
	h := sha256.New()
	h.Write([]byte(strconv.FormatInt(sc.tid, 10)))
	h.Write([]byte{0})
	h.Write([]byte(sc.srcLang))
	h.Write([]byte{0})
	h.Write([]byte(sc.tgtLang))
	h.Write([]byte{0})
	h.Write([]byte(sc.mode))
	h.Write([]byte{0})
	h.Write([]byte(sc.modelFP))
	h.Write([]byte{0})
	h.Write([]byte(sc.ctxHash))
	h.Write([]byte{0})
	h.Write([]byte(tmPromptRev))
	h.Write([]byte{0})
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// tmLookup 单条查询：先内存，后库表（库里命中的回填内存，热起来）。
func (e *Engine) tmLookup(ctx context.Context, sc tmScope, text string) (string, tmHitLayer) {
	if e == nil || text == "" || sc.tgtLang == "" {
		return "", tmMiss
	}
	if v, ok := e.tmGetCache().Get(tmKey(sc, text)); ok {
		tmHitsA.Add(1)
		tmCountHit(ctx, 1)
		return v, tmHitA
	}
	// 库表在 tid>=0 时都查：平台单（tid==0）落 tenant_id=0，只与 0 互查、不与客户共享；
	// 只有 tid<0 才是"无归属"（v1 §4.1：不读不写）。
	if sc.tid < 0 || e.St == nil {
		return "", tmMiss
	}
	v, ok := e.St.TranslationMemoryGet(sc.tid, sc.srcLang, sc.tgtLang, sc.mode, sc.modelFP, sc.ctxHash, text)
	if !ok || v == "" {
		return "", tmMiss
	}
	e.tmGetCache().Put(tmKey(sc, text), v)
	tmHitsB.Add(1)
	tmCountHit(ctx, 1)
	return v, tmHitB
}

// tmLookupBatch 批量查询：内存逐条命中，未命中的**一次 IN** 打库。
//
// 返回 map[段下标]译文。为什么按下标而不是按源文返回：同一批里可能有重复段，
// 按源文返回会让调用方自己再做一次反查（那就是第二套映射，迟早错位）。
//
// ⚠️ 库表在 tid>=0 时都查；tid==0 落 tenant_id=0 且只与 0 互查，tid<0 不读不写。
func (e *Engine) tmLookupBatch(ctx context.Context, sc tmScope, texts []string) (map[int]string, int, int) {
	hits := make(map[int]string, len(texts))
	if e == nil || len(texts) == 0 || sc.tgtLang == "" {
		return hits, 0, 0
	}
	cache := e.tmGetCache()
	missSrc := make([]string, 0, len(texts))
	missIdx := make([]int, 0, len(texts))
	hitsA := 0
	for i, t := range texts {
		if t == "" {
			continue
		}
		if v, ok := cache.Get(tmKey(sc, t)); ok {
			hits[i] = v
			hitsA++
			continue
		}
		missSrc = append(missSrc, t)
		missIdx = append(missIdx, i)
	}
	tmHitsA.Add(int64(hitsA))
	// ⚠️ 每单计数必须**在早退之前**记 A 层：全命中（missSrc 为空）走的就是下面那个
	//   `return hits, hitsA, 0`，把计数写在函数末尾会让"第二次跑同一篇"——即命中率最高的
	//   那个形态——恰好一个字都不计（本批首跑真踩，L9 当场红）。
	tmCountHit(ctx, hitsA)
	if len(missSrc) == 0 || sc.tid < 0 || e.St == nil {
		return hits, hitsA, 0
	}
	got := e.St.TranslationMemoryGetBatch(sc.tid, sc.srcLang, sc.tgtLang, sc.mode, sc.modelFP, sc.ctxHash, missSrc)
	hitsB := 0
	for j, src := range missSrc {
		v, ok := got[src]
		if !ok || v == "" {
			continue
		}
		hits[missIdx[j]] = v
		cache.Put(tmKey(sc, src), v)
		hitsB++
	}
	tmHitsB.Add(int64(hitsB))
	// 每单计数（B 层）：调用方（file.go）据此把「本单全部语种命中数」写进载荷 tm_hits。
	tmCountHit(ctx, hitsB)
	return hits, hitsA, hitsB
}

// tmStore 写回两级缓存（内存必写；库表按租户归属写）。
// 只应在译文通过可用性判据后调用——本函数**不做质量判断**，那是调用方的事
// （判据只有一处：`e.fileTranslationUsable`，见 AGENTS §一·19）。
func (e *Engine) tmStore(ctx context.Context, sc tmScope, text, target string) {
	if e == nil || text == "" || target == "" || sc.tgtLang == "" {
		return
	}
	e.tmGetCache().Put(tmKey(sc, text), target)
	if sc.tid < 0 || e.St == nil {
		return
	}
	e.St.TranslationMemoryPut(store.TranslationMemoryRecord{
		TenantID:    sc.tid,
		SrcLang:     sc.srcLang,
		TgtLang:     sc.tgtLang,
		Mode:        sc.mode,
		ModelFP:     sc.modelFP,
		ContextHash: sc.ctxHash,
		Src:         text,
		Out:         target,
		Source:      store.TMSourceModel,
	})
	// 每写 tmPruneEvery 条触发一次清理（按 180 天 / 20 万行）。
	// 为什么不是每单清理：清理要 count + 两条 DELETE，跑在热路径上纯属浪费；
	// 为什么不能永不清理：库表会无界增长，且旧词会持续命中（术语换代后反而更坏）。
	if e.tmWrites.Add(1)%tmPruneEvery == 0 {
		e.St.TranslationMemoryPrune(sc.tid, e.tmKeepDays(), e.tmMaxRows())
	}
}

// tmPruneEvery 清理触发间隔（写入条数）。
const tmPruneEvery = 1000

// tmKeepDays 保留天数（system_config 可调，钳 1~3650；缺省 180）。
func (e *Engine) tmKeepDays() int {
	n := e.tmCfgInt("tm_keep_days", defaultTMKeepDays)
	if n < 1 {
		n = 1
	}
	if n > 3650 {
		n = 3650
	}
	return n
}

// tmMaxRows 单租户行数上限（system_config 可调，钳 1000~2000000；缺省 200000）。
func (e *Engine) tmMaxRows() int {
	n := e.tmCfgInt("tm_max_rows", defaultTMMaxRows)
	if n < 1000 {
		n = 1000
	}
	if n > 2000000 {
		n = 2000000
	}
	return n
}

// defaultTMKeepDays / defaultTMMaxRows 与 store 侧常量同值：给本包一处可引用的缺省，
// 避免 engine 里出现裸数字（两侧等值由 tm_cache_gate_test 的 L8 钉住）。
const (
	defaultTMKeepDays = 180
	defaultTMMaxRows  = 200000
)

// tmCfgInt 读一个整数配置；缺失/非数字/≤0 一律回缺省。
func (e *Engine) tmCfgInt(key string, def int) int {
	if e.St == nil {
		return def
	}
	v, err := e.St.GetConfig(key)
	if err != nil {
		return def
	}
	n, cerr := strconv.Atoi(strings.TrimSpace(v))
	if cerr != nil || n <= 0 {
		return def
	}
	return n
}

// tmHitsA / tmHitsB / tmMisses 观测计数（进程级；只用于日志与后续调档取证，
// **不做任何业务判据**——数日志只能用来抓"压根没走到"，判"生效"必须看返回值本身）。
var (
	tmHitsA  atomic.Int64
	tmHitsB  atomic.Int64
	tmMisses atomic.Int64
)

// tmLogBatch 每个批量腿结束后打一行命中读数（只记数，绝不记原文/译文——客户内容不进日志）。
func tmLogBatch(ctx context.Context, lang string, segs, hitsA, hitsB, misses int) {
	if hitsA+hitsB == 0 && misses == 0 {
		return
	}
	observability.Info(ctx, "译文记忆命中",
		"lang", lang, "segs", segs,
		"hits_a", hitsA, "hits_b", hitsB, "misses", misses,
		"proc_a", tmHitsA.Load(), "proc_b", tmHitsB.Load(), "proc_miss", tmMisses.Load())
}

// ============ 每单命中计数（S4：工单载荷 tm_hits） ============

// tmHitCounterKey ctx 键（私有类型，防与其他包冲突）。
type tmHitCounterKey struct{}

// withTMHitCounter 给一份 ctx 挂上「本单译文记忆命中数」计数器。
//
// 为什么用 ctx 而不是给批量腿加返回值：命中发生在**批量腿内部**（含逐段兜底腿），
// 而调用方（file.go 的三个语言协程）要的是「这一单全部语种合计」。
// 让每个调用点自己收一份 return value，就要在三个循环里各累加一次——
// 漏一处就是「某条腿的命中不计数」，而它不报错、只是读数偏小。
// 挂在 ctx 上则天然覆盖所有派生 ctx（协程、子调用），一个写点、一个读点。
//
// ⚠️ 计数器只在本单内有效：HandleFile 每次自己建一份，绝不放进包级变量
// （包级会被并发工单互相累加，读数直接错位）。
func withTMHitCounter(ctx context.Context) (context.Context, *atomic.Int64) {
	n := &atomic.Int64{}
	return context.WithValue(ctx, tmHitCounterKey{}, n), n
}

// tmCountHit 给 ctx 上的计数器加 n（没有计数器时静默跳过：非工单链不计数）。
func tmCountHit(ctx context.Context, n int) {
	if n <= 0 || ctx == nil {
		return
	}
	if v, ok := ctx.Value(tmHitCounterKey{}).(*atomic.Int64); ok && v != nil {
		v.Add(int64(n))
	}
}
