// ============ llmsource.go · 职责说明 ============
// 上游 LLM 模型配置的**单一解析事实源**：把「环境变量 ＞ 后台库配置 ＞ 模型路由主路由」这条
// 优先序从 cmd/server 的启动期专用代码里抽出来，让**任何一台实例在任何时刻**都能按同一把尺子
// 重新解析一次当前生效的配置（★ 2026-10-04 〇-AR 第 5 波，用户口径「每台热加载」）。
//
// 为什么要抽这一层（缺陷本体，㊻）：
//
//	管理台保存模型配置的接口只刷新**发起写入的那一个进程**的配置快照，另一台实例带着
//	启动期的随机占位 Key 会一直 401。单机部署时没人看得见，多实例／灰度／演示单元与主单元
//	共库时就是"配了却永远用不上"的静默失效——和 R-1 那次同一族形态（配置在库里，读的人不在库里读）。
//
// ★ 三条硬口径（都是本仓真踩过的形态，改这个文件前必读）：
//
//	① **不可变快照＋原子发布**：Snapshot 一经 Publish 就永不修改，读侧拿到的是指针指向的
//	   只读副本。旧形态直接改共享 cfg 的 slice 字段，与并发 range 是数据竞争
//	   （slice 头不是原子写，读者可能拼出"新指针＋旧长度"）；AGENTS §三 那条
//	   「并发回写共享 map 必须先快照」在这里的对应物就是"整份换指针"。
//	② **env 档不吃库值**：环境变量给了可用 Key 的实例，库里的 online_api_key 一律不覆盖
//	   （AGENTS §一·3「环境变量 > 数据库配置」）。但 model_routes 仍然从库读——
//	   这与改造前的启动顺序完全一致，热加载不改变优先级，只改变"什么时候再算一遍"。
//	③ **探测失败不许把已生效的配置抹掉**：库读不到 ⇒ 保留上一份快照并记一条限频 WARN，
//	   绝不回落到占位 Key（把"运维读库抖一下"打成"全站翻译 401"是新的可用性事故）。
//
// 射程外（如实登记，别当成已覆盖）：Embedding 一族（embed_api_key／embed_api_base）与
// 启动期一次性判定（路由的 supports_constraints 能力位）仍是启动期快照；
// 知识库向量那腿本来就有「每请求现读 stage_models.kb_embed」的请求级覆盖，不依赖本文件。
// =============================================
package llmsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"translator/internal/config"
	"translator/internal/observability"
	"translator/internal/store"
)

// 水合来源档位（对外只出这四个词：/api/health.llm_global_key 与日志里出现的就是它们）
const (
	FromEnv   = "env"   // 环境变量给了可用 Key，库里的在线 Key 一律不覆盖
	FromDB    = "db"    // 后台库配置 system_config.online_api_key
	FromRoute = "route" // 模型路由主路由带的 Key
	FromNone  = "none"  // 三档皆空 ⇒ 仍是随机占位符（调用必 401）
)

// 参与解析的配置键（同时也是热加载探测的键集合；加一个键就得同时进指纹，否则改了它不翻）
const (
	KeyModelRoutes = "model_routes"
	KeyOnlineKey   = "online_api_key"
	KeyOnlineBase  = "online_api_base"
	KeyOnlineModel = "online_model"
)

// probeKeys 指纹与快照读覆盖的键清单（唯一一份，禁止在别处再抄一遍字面量）
var probeKeys = []string{KeyModelRoutes, KeyOnlineKey, KeyOnlineBase, KeyOnlineModel}

// Snapshot 一次解析得到的不可变上游配置快照。
// 字段全部在构造期定稿，Publish 之后**只读**——新增字段时同步补进 Resolve，
// 不许出现"某条腿运行时再往快照里写"的形态（那就是把数据竞争请回来）。
type Snapshot struct {
	OnlineAPIBase string                  // 生效的在线翻译端点
	OnlineAPIKey  string                  // 生效的在线翻译密钥（内存态明文，绝不落日志）
	OnlineModel   string                  // 生效的默认模型名
	Placeholder   bool                    // 生效密钥是不是启动期随机占位符
	Routes        []config.ProviderConfig // 全局模型路由（已从库内密文解密；空=单模型形态）
	From          string                  // 生效来源档位（From* 常量）
	Fingerprint   string                  // 本次解析所依据的库内原始值指纹（节流判据，见 Refresh）
	ResolvedAt    time.Time               // 解析时刻（运维排障看"这份配置是几点算的"）
}

// live 当前生效的快照（原子指针，读侧无锁；nil ⇒ 尚未发布，Live() 会按 cfg 兜一份）
var live atomic.Pointer[Snapshot]

// probeState 热加载探测的节流状态（独立互斥锁，不与 live 指针混用）
type probeState struct {
	mu       sync.Mutex
	at       time.Time // 上一次真正探库的时刻（TTL 判据）
	lastErr  string    // 上一次探测的错误文案（错误翻转才记日志，防刷屏）
	errAt    time.Time // 上一次因错误记日志的时刻（限频）
	lastFail bool      // 上一次探测是否失败（用于「恢复时补一条」）
	st       any       // 上一次探测所依据的 Store（按指针身份比）；换了库 ⇒ TTL 一律作废
}

// probe 全局的"上一次真去探库"留痕（节流时刻／指纹／所依据的 Store 指针）。
// 为什么这一份状态必须是包内唯一的：TTL 节流与指纹比对只有"全平台共用一个上次读数"才成立——
// 每个读点自己存一份的话，六个读点就等于六个节流钟，运营改一次配置要被打六次库
// （其中健康面是探针高频打的），而"谁先换、谁还旧"又回到本批要消灭的那种机群分叉。
// 并发口径：只由 probeMu 保护（含读写），任何持锁路径都不许再回调本包的公开函数，
// 否则同协程重入会把探测卡在临界区里（AGENTS §一·12 那条重入教训同形）。
var probe probeState

// envTTLKey 重探间隔的环境变量名（UAT 与本地排障会把它压到 1 秒，别写进生产默认）
const envTTLKey = "LLM_CONFIG_RELOAD_TTL_SEC"

// reloadTTL 返回两次探库之间的最小间隔。默认 5 秒，env 可覆盖为 0（每次调用都探，只用于测试）。
// 返回值: 间隔时长；env 非法值一律回落默认，不报错也不静默变成 0（配错档位不该改变行为）。
func reloadTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv(envTTLKey)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 5 * time.Second
}

// baseFromCfg 从运行期配置取"构造期就定死的那一份"在线三件套与占位标记。
// 参数 cfg: 运行期配置，nil 时回落全局 config.C。
// 为什么先问 cfg 而不是直接读库：env 档的可用性只有在启动快照里才作数
//
//	（config.Default() 读过 env 才知道是真实 Key 还是随机占位符），
//	用库里有没有值去反推"是不是 env 给的"就是把派生状态当来源，那是本仓立过的那条红线。
func baseFromCfg(cfg *config.Config) (base, key, model string, placeholder bool) {
	c := cfg
	if c == nil {
		c = config.C
	}
	if c == nil {
		return "", "", "", true
	}
	return c.OnlineAPIBase, c.OnlineAPIKey, c.OnlineModel, c.OnlineAPIKeyIsPlaceholder
}

// resolveRoutesFromRaw 把库内 model_routes 原文解成可用路由列表。
// 参数 raw: 库内 JSON 原文（键值为 enc:v1: 密文，历史明文由 DecryptSecret 兼容）。
// 返回: 解密后的路由切片；原文为空／解析失败一律回 nil（调用方按"没有路由"处理，不报错）。
// 副作用: 单条解密失败只停用那一条并记 WARN——旧形态整列表解析失败才出声，
//
//	一条坏 Key 会把同批其他可用供应商一起打死（密钥解密失败多半是 JWT_SECRET 没同步）。
func resolveRoutesFromRaw(raw string) []config.ProviderConfig {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var routes []config.ProviderConfig
	if err := json.Unmarshal([]byte(raw), &routes); err != nil {
		observability.Warn(context.Background(), "模型路由原文解析失败，本份快照按无路由处理", "err", err.Error())
		return nil
	}
	alive := make([]config.ProviderConfig, 0, len(routes))
	for _, rt := range routes {
		dec := store.DecryptSecret(rt.APIKey)
		if dec == "" && strings.HasPrefix(rt.APIKey, store.SecretEncPrefix) {
			observability.Warn(context.Background(), "路由密钥解密失败，该路由停用",
				"provider", rt.Provider, "model", rt.Model)
			continue
		}
		rt.APIKey = dec
		alive = append(alive, rt)
	}
	return alive
}

// fingerprintOf 对"这份快照所依据的全部输入"算一份顺序无关的指纹。
// 参数 vals: 库内原文（键→值，密文形态未解密）；env: 进程侧现值（环境变量那档的三件套与路由条数）。
// 返回: sha256 十六进制串（只截 16 位，且**永不落日志**——它是凭据的单向摘要，不是凭据本身）。
// ★ 两条口径都是真踩出来的：
//
//	① 必须问库内**原文**而不是解密后的值：解密会吞掉"密文换了但内容相同"这种无变化写入；
//	② 必须把**进程侧现值**一起算进来：快照＝库值 ∧ env 值两半，只比库值的话，
//	   "另一座内容相同的库＋不同的环境变量"会算出同一个指纹，于是读到别人家那份配置
//	   （单测里每个用例各起一座内存库，第一版就栽在这里——表现为"这条用例拨到了别人的端点"）。
func fingerprintOf(vals map[string]string, env cfgComponent) string {
	h := sha256.New()
	for _, k := range probeKeys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(vals[k]))
		h.Write([]byte{0})
	}
	h.Write([]byte(env.Base))
	h.Write([]byte{0})
	h.Write([]byte(env.Key))
	h.Write([]byte{0})
	h.Write([]byte(env.Model))
	h.Write([]byte{0})
	if env.Placeholder {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	h.Write([]byte("routes:" + strconv.Itoa(env.Routes)))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// cfgComponent 进程侧配置现值的摘要输入（喂给 fingerprintOf，不外传、不落日志）。
type cfgComponent struct {
	Base        string // 环境变量／配置文件给的端点
	Key         string // 环境变量给的密钥（只进指纹，不进任何输出面）
	Model       string // 环境变量给的模型名
	Placeholder bool   // 密钥是不是启动期随机占位符
	Routes      int    // 进程侧路由条数（库里没有 model_routes 那一行时它才作数）
}

// cfgRoutesFor 取进程侧那一份路由列表（库里没有 model_routes 那一行时的回落来源）。
// 参数 cfg: 运行期配置，nil 时回落全局 config.C。
func cfgRoutesFor(cfg *config.Config) []config.ProviderConfig {
	if cfg == nil {
		cfg = config.C
	}
	if cfg == nil {
		return nil
	}
	return cfg.ModelRoutes
}

// Resolve 按「环境变量 ＞ 后台库配置 ＞ 主路由」解析出当前应生效的快照（纯函数，不落日志告警）。
// 参数 cfg: 运行期配置（提供 env 档现值）；st: 平台存储，nil 时只按 cfg 构造（无库启动形态）。
// 返回: 不可变快照。副作用: 无（要产生"三档皆空"的告警请调用 Publish＋上层显式处理）。
func Resolve(cfg *config.Config, st *store.Store) *Snapshot {
	base, key, model, placeholder := baseFromCfg(cfg)
	snap := &Snapshot{
		OnlineAPIBase: base, OnlineAPIKey: key, OnlineModel: model,
		Placeholder: placeholder,
		ResolvedAt:  time.Now(),
	}
	// env 那档已给出可用 Key ⇒ 在线三件套一条都不覆盖（硬口径 ②）
	envUsable := !placeholder && key != ""
	if st == nil {
		// 无库启动形态：路由只能问进程内现值（配置文件／调用方直接构造的 cfg），
		// 漏了这一句会让"配了路由却没有平台库"的引擎回落到单模型那一套——
		// 单测里大量 Engine{Cfg: 带路由} 的构造正是这一形态。
		snap.Routes = cfgRoutesFor(cfg)
		if envUsable {
			snap.From = FromEnv
		} else {
			snap.From = FromNone
		}
		return snap
	}

	vals, err := st.ConfigsByKeys(probeKeys...)
	if err != nil {
		// 库读不到 ⇒ 按 cfg 现值出一份快照，Fingerprint 留空使下次 Refresh 必然重探
		if envUsable {
			snap.From = FromEnv
		} else {
			snap.From = FromNone
		}
		return snap
	}
	snap.Fingerprint = fingerprintOf(vals, cfgComponent{
		Base: base, Key: key, Model: model, Placeholder: placeholder,
		Routes: len(cfgRoutesFor(cfg)),
	})

	// —— 路由腿：无论 env 是否可用都从库读（与改造前的启动顺序一致）——
	// ★ 库里**有这一行**就以库为准，包括"[]"＝清空：旧形态只在解析出的条数 >0 时才覆盖 cfg，
	//   于是管理台清空路由后本进程仍是旧列表（要重启才生效），与"每台热加载"的口径直接冲突。
	snap.Routes = resolveRoutesFromRaw(vals[KeyModelRoutes])
	if _, ok := vals[KeyModelRoutes]; !ok && len(snap.Routes) == 0 {
		// 库里根本没这个键（纯配置文件／无库启动形态）⇒ 回落进程内现值，不把路由强行抹成空
		snap.Routes = cfgRoutesFor(cfg)
	}

	if envUsable {
		snap.From = FromEnv
		return snap
	}

	// —— ② 后台库配置：运营在管理台显式保存过的那一份 ——
	if v := strings.TrimSpace(vals[KeyOnlineKey]); v != "" {
		if dec := store.DecryptSecret(v); dec != "" {
			snap.OnlineAPIKey = dec
			snap.Placeholder = false
		}
	}
	// 端点与模型名**各自独立判断**（与改造前的启动水合同一口径）：
	// 库里只配了 Key 没配端点时，必须保留 cfg 的端点现值，不许把它洗成空串——
	// 空端点会让每一次上游调用在构造请求时就失败，比"没水合上 Key"更难归因。
	if b := strings.TrimSpace(vals[KeyOnlineBase]); b != "" {
		snap.OnlineAPIBase = b
	}
	if m := strings.TrimSpace(vals[KeyOnlineModel]); m != "" {
		snap.OnlineModel = m
	}
	if !snap.Placeholder && snap.OnlineAPIKey != "" {
		snap.From = FromDB
		return snap
	}

	// —— ③ 主路由兜腿：库里也没给出可用 Key 时才走 ——
	for _, r := range snap.Routes {
		// 掩码值（sk-****）不是密钥；空值更不是
		if r.APIKey != "" && !strings.HasPrefix(r.APIKey, "sk-****") {
			// 只接密钥这一件（与改造前完全一致）：端点/模型名跟着路由条本身走，
			// resolveModel 命中路由时用的就是那条路由自己的端点，不必再抄进全局三件套。
			snap.OnlineAPIKey = r.APIKey
			snap.Placeholder = false
			snap.From = FromRoute
			return snap
		}
	}
	snap.From = FromNone
	return snap
}

// ApplyTo 把解析结果写回运行期配置（启动期用；Publish 之外的唯一 cfg 写入方）。
// 参数 cfg: 原地改写的运行期配置，nil 时静默返回。
// 为什么还要写 cfg：Embedding、熔断、路由能力位等一批启动期一次性判定仍读 cfg，
//
//	一次性收敛到快照的改动面 >3000 行且无行为收益（AGENTS §三），故保留这一薄层委托。
func (s *Snapshot) ApplyTo(cfg *config.Config) {
	if s == nil || cfg == nil {
		return
	}
	cfg.OnlineAPIBase, cfg.OnlineAPIKey, cfg.OnlineModel = s.OnlineAPIBase, s.OnlineAPIKey, s.OnlineModel
	cfg.OnlineAPIKeyIsPlaceholder = s.Placeholder
	if s.Routes != nil {
		cfg.ModelRoutes = s.Routes
	}
}

// PublishFrom 发布快照并**同时钉住"这份内容来自哪座库、什么时候读的"**（启动水合与管理台保存都走这里）。
// 参数 st: 解析时所依据的 Store；s: 待发布快照。返回: 发布后的快照。
// 为什么要多这一个口子：TTL 节流的前提是"还是同一座库"，
//
//	而启动水合与管理台保存都是**刚读完库就发布**，不钉住库身份的话，
//	第一次 Refresh 会当成"从没探过"再读一遍（白一次往返），
//	反过来把节流时刻无条件共享又会让"另一座内容相同的库"读到别人家的快照。
func PublishFrom(st *store.Store, s *Snapshot) *Snapshot {
	Publish(s)
	if s != nil && s.Fingerprint != "" {
		probe.mu.Lock()
		probe.st = st
		probe.mu.Unlock()
	}
	return s
}

// Publish 发布一份快照为"当前生效"，返回入参以便调用方链式使用。
// 副作用: 原子换指针；读侧下一次 Live() 即拿到新值（无需重启、无需等 TTL）。
func Publish(s *Snapshot) *Snapshot {
	if s == nil {
		return nil
	}
	if s.ResolvedAt.IsZero() {
		s.ResolvedAt = time.Now()
	}
	live.Store(s)
	// 带指纹的发布说明"刚刚真读过库"，把探测节流时刻一起钉上：
	// 否则启动水合后的第一次 Refresh 会当成"从没探过"立刻再读一遍库（白一次往返）。
	if s.Fingerprint != "" {
		probe.mu.Lock()
		probe.at = s.ResolvedAt
		probe.mu.Unlock()
	}
	return s
}

// Live 返回当前生效的快照，**永不返回 nil**（调用方不做 nil 判断）。
// 兜底形态：从未发布（单测、无库启动、旧二进制热重载路径之外的读点）⇒
// 按运行期配置现场构造一份并发布，保证后续读到的始终是同一个对象。
func Live() *Snapshot {
	if s := live.Load(); s != nil {
		return s
	}
	base, key, model, placeholder := baseFromCfg(nil)
	fallback := &Snapshot{
		OnlineAPIBase: base, OnlineAPIKey: key, OnlineModel: model,
		Placeholder: placeholder, From: FromNone, ResolvedAt: time.Now(),
	}
	if !placeholder && key != "" {
		fallback.From = FromEnv
	}
	if c := config.C; c != nil && c.ModelRoutes != nil {
		fallback.Routes = c.ModelRoutes
	}
	// CompareAndSwap：并发首次调用只有一份成为生效对象，另一份丢弃（内容同源，无害）
	if cur := live.Load(); cur == nil {
		if !live.CompareAndSwap(nil, fallback) {
			return live.Load()
		}
		return fallback
	}
	return live.Load()
}

// Current 取"这台机器此刻真正会拿去调上游的那一份"（六个读点的唯一入口）。
// 参数 ctx: 请求／任务上下文（只给热加载那条 INFO 带 trace_id，探测本身与请求无关）；
//
//	st: 平台存储，nil＝无库形态；cfg: 运行期配置（提供 env 档现值）。
//
// 返回: 不可变快照，**永不 nil**（调用方不写判空——判空迟早漏一个变成 panic）。
// 两条分支的口径：
//   - 有库 ⇒ 先按 TTL＋指纹惰性重探（Refresh），再读全局快照：这才是"每台热加载"；
//   - 无库（纯配置文件启动／裸引擎单测）⇒ 只按本进程配置现值算一份，**不碰全局指针**。
//     漏了这一句，同进程里别的用例发布过快照时，这里会把别人家的配置当成自己的现值——
//     表现是"这条断言拨到了别人的端点"，比直接红灯更难归因（本批真踩）。
func Current(ctx context.Context, st *store.Store, cfg *config.Config) *Snapshot {
	if st == nil {
		return Resolve(cfg, nil)
	}
	Refresh(ctx, st, cfg)
	return Live()
}

// Refresh 惰性重探：距上次探库超过 TTL 时读一次库内原文，指纹变了就重解析并发布。
// 参数 ctx: 请求上下文（只为日志带 trace_id，探测本身与请求无关）；
//
//	st: 平台存储（nil 直接返回 false，无库启动形态）；cfg: 运行期配置（提供 env 档现值）。
//
// 返回: 本次是否换上了新快照。副作用: 换上新快照时记一条 INFO（不含任何 Key 片段）；
//
//	探测失败记一条限频 WARN（最多每 60 秒一次），并**保留上一份快照继续服役**。
//
// ★ 为什么用 TTL＋指纹而不是每条键一次 GetConfig：一次请求会扇出几十路并发翻译调用，
//
//	每路都现读库就是几十次往返；而"值真变了"才需要重算，TTL 内直接复用上一份。
func Refresh(ctx context.Context, st *store.Store, cfg *config.Config) bool {
	if st == nil {
		return false
	}
	now := time.Now()
	probe.mu.Lock()
	// ★ TTL 只在"还是那一个库"的前提下成立：换了 Store（单测里每个用例各起一座内存库、
	//   或多进程测试里另一座库）必须立刻真探一次，否则会读到上一座库发布的那份快照——
	//   表现是"这条用例拨到了别人家的端点"，比直接红灯更难归因（本批真踩）。
	if probe.st == any(st) && now.Sub(probe.at) < reloadTTL() {
		probe.mu.Unlock()
		return false
	}
	probe.at = now
	probe.st = st
	probe.mu.Unlock()

	cur := Live()
	next := Resolve(cfg, st)
	if next.Fingerprint == "" {
		// 库没读到 ⇒ 保持上一份（硬口径 ③），只把"从有到无"的翻转记出来
		warnProbeOnce(ctx, "上游模型配置探测未取到库内现值，沿用上一份快照", cur.Fingerprint == "")
		return false
	}
	clearProbeErr(ctx)
	if next.Fingerprint == cur.Fingerprint {
		return false // 没变：不换指针，读侧继续用同一份（零分配）
	}
	Publish(next)
	// 出状态词与条数，不出 Key、端点域名或任何可定位坐标（同 AGENTS §一·12 派发口径）
	observability.Info(ctx, "上游模型配置已热加载", "from", next.From, "routes", len(next.Routes))
	return true
}

// warnProbeOnce 探测失败的限频日志（同一条错误最多 60 秒一次，恢复时补一条 INFO）。
// 参数 msg: 日志文案；keepPrevious: 是否沿用上一份快照的表述后缀。
func warnProbeOnce(ctx context.Context, msg string, keepPrevious bool) {
	now := time.Now()
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if keepPrevious && probe.lastFail && now.Sub(probe.errAt) < 60*time.Second {
		return // 同一个失败状态在一分钟内不刷屏（日志的价值在于"第一次"，第 N 次是噪声）
	}
	probe.lastFail = true
	probe.errAt = now
	observability.Warn(ctx, msg, "ttl_sec", int(reloadTTL().Seconds()))
}

// clearProbeErr 探测恢复时清失败标记，并补一条 INFO 让运维看见"那段抖动过去了"。
func clearProbeErr(ctx context.Context) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if probe.lastFail {
		probe.lastFail = false
		observability.Info(ctx, "上游模型配置探测已恢复正常")
	}
}

// ResetForTest 复位进程级状态（只给单测用：TTL 节流与原子指针都是进程级的，
// 不复位会让第二个用例读到第一个用例留下的快照，得到"看运气翻红"的形态）。
// 副作用: 清空已发布快照与探测节流时刻。
func ResetForTest() {
	live.Store(nil)
	probe.mu.Lock()
	probe.at = time.Time{}
	probe.st = nil
	probe.lastFail = false
	probe.errAt = time.Time{}
	probe.mu.Unlock()
}
