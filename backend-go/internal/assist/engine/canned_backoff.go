// ============ canned_backoff.go · 职责说明 ============
// 挂件 canned 首屏翻译链的**退避窗口 + 冷语种计数 + 独立快模型**（★ 0AR 第 4 波，台账 ㊷ 三腿）。
//
// ★ 现网实证（台账《修改文档_缺陷核实与修法断言_20261004》§七 ㊷，两次独立复现）：
// 泰文这一档冷语种走的是这样一个闭环——
//
//	同步腿必超时（GLM-Z1 是**推理型**模型，首 token 之前先把链子跑满，8 秒预算根本不够）
//	⇒ 后台补一枪 ⇒ 那一稿被出栈闸**正确**拒掉（夹带中文残渣与提示词回声）
//	⇒ 不写缓存、也不写负缓存 ⇒ 下一位访客从零再来一遍。
//
// 读数：`/opt/ai-assist/data/assist.log` 里 10-03 11:36:46 与 10-04 01:04:10 两批成对出现
// （`reason=canned_sync_timeout` ＋ `reason=canned_bg_gated`），而 `assist.db` 的 mtime 停在 10-02 08:09
// ⇒ 两天零成功写入。**每一个 th 访客都白等 8 秒拿到一屏中文**，全程只有 WARN、没有任何告警面读数。
//
// 本文件把这三件事分开治，因为它们是三张不同的处方：
//
//	① 退避窗口（㊷③）：同一 (kind,语种,指纹) 在窗口内**两条腿都不打上游**——
//	   既省下访客那 8 秒白等，也省下每天几千枪无效上游调用；窗口到了自然再试一次，成功即清窗。
//	   ⚠️ 台账里那句「抬 canned_translate_timeout_sec（8→25）」**不是**修法：
//	   它只把白等拉长到 25 秒，那一稿照样过不了闸，缓存照样写不上。
//	② 计数与健康读数（㊷④）：`canned_sync_timeout`／`canned_bg_gated` 这类分档从此**有地方被数**，
//	   /health 出一个状态词 ＋ 各档计数（同 ⑮ usdt_watch 的口径：**只有状态词与计数，绝不带上游地址/密钥**）。
//	③ 独立快模型（㊷①）：`canned_llm_model` 非空时，canned 这一路用它而不是对话正文那套推理模型——
//	   欢迎词/chips 是"全网就一份、值得缓存"的短文本，拿推理模型翻它是用首屏延迟去买不需要的思考。
//
// ★ 为什么退避状态是**内存态**而不是落库：
// 落库要再造一套 TTL 语义（什么时候清、谁清、多实例共享一把锁吗），而这条链的收益只在于
// "接下来几分钟别再白烧"。进程重启等于窗口清零＝最多多打一次上游，不构成坏状态；
// 反过来若落库，运营在管理台改了模型，库里那份退避还会继续押着新模型不发（同 §一·3 那条
// 「把读到的旧值当现值载回」的形态）。⚠️ 多实例各数各的账是**已知边界**，不假装它是全局配额。
//
// ★ 为什么计数用 map 而不是单个总数：运维排障问的是"**哪一语种**在冷"（th 与 ar 的处方完全不同：
// 前者是模型选型，后者是闸门判据），一个总数答不出这个问题；而 key 的基数有上界
// （kind 两类 × 12 语种 × 若干失败分档），不会长成需要回收的无界表。
// =============================================
package engine

import (
	"context"
	"strings"
	"time"

	"translator/internal/assist/llm"
	"translator/internal/observability"
)

// configs 表里这一族的新键（★ 0AR 第 4 波）。
// 手法照本包既有口径：读 configs 表 + 代码默认值，管理台可改，**不新建 env 解析层**
// （AGENTS §一·3：全局 LLM 配置的读点只有一份尺子，这里读的是挂件自己的档位，不是那四把全局键）。
const (
	// cfgCannedBackoffSec 退避窗口长度（秒）。
	cfgCannedBackoffSec = "canned_backoff_sec"
	// cfgCannedModel canned 这一路专用的模型名；空＝回落对话那套 llm_model（㊷①）。
	cfgCannedModel = "canned_llm_model"
)

// 退避窗口的默认值与硬夹区间（★ 0AR）。
// 下限 30 秒：比同步＋后台两条预算之和还短的窗口等于没有窗口（下一位访客紧接着就又打一遍）。
// 上限 6 小时：再长就成了"这一语种今天不会再试了"，而模型侧恢复是分钟级的，不该由我们代它请假。
const (
	cannedBackoffDefaultSec = 300
	cannedBackoffMinSec     = 30
	cannedBackoffMaxSec     = 21600
)

// canned 链的**运维可见**分档（★ 0AR 第 4 波）。
// 与 localize_async.go 那八档同族：**日志里看得见的名字就是对外排障契约**，逐字钉进单测。
const (
	// cannedBgBackoff 处于退避窗口内的**读数档**：它不是一种失败（上游压根没被拨），
	// 而是"上一枪失败换来的几分钟假释"。现网排障必须能分清「这一语种在退避」与
	// 「这一语种这一枪打失败了」——前者的动作是等，后者的动作是查。
	cannedBgBackoff = "canned_bg_backoff"
)

// /health 的 canned 状态词（★ 0AR，同 ⑮ 那四条 usdt_watch 词的口径：**只有状态词与计数**，
// 绝不出现上游地址、模型 Key、语种原文文本）。
const (
	cannedHealthOK      = "ok"      // 没有未自愈的冷语种记录
	cannedHealthCold    = "cold"    // 有失败记录，但此刻没有在途退避（下一位访客会真打一次）
	cannedHealthBackoff = "backoff" // 至少一个 (kind,语种) 正处于退避窗口＝此刻有语种在被节流
)

// cannedColdMaxKeys 计数字典的容量上限（防"每换一次原文就多一个指纹 key"长期无界增长）。
// 到顶后新 key 不再单列，只并入总数——这是观测面，不是记账面，宁可读数粗一点也别长成内存泄漏。
const cannedColdMaxKeys = 64

// noteCannedCold 记一次"canned 这一路没拿到可用产物"（★ 0AR）。
//
// 三个调用点刻意**只记不拦**：记的地方就是决策已经做完的地方（出中文、不补枪、开退避），
// 判据本身不在这里——在这里再加判据就是第五把尺子。
func (e *Engine) noteCannedCold(ctx context.Context, kind, uiLang, reason string) {
	if reason == "" {
		return
	}
	e.cannedColdMu.Lock()
	defer e.cannedColdMu.Unlock()
	if e.cannedCold == nil {
		e.cannedCold = map[string]int{}
	}
	key := canonicalLang(uiLang) + "\x00" + kind + "\x00" + reason
	if _, seen := e.cannedCold[key]; !seen && len(e.cannedCold) >= cannedColdMaxKeys {
		e.cannedColdOverflow = true // 只并进总数，字典不再长（读数见 CannedHealth 的 overflow 字段）
	} else {
		e.cannedCold[key]++
	}
	e.cannedColdTotal++
	// 第一次出现某一档时留一行 WARN：这一行带的是**运维动作**（该调档还是该查上游），
	// 而计数本身在日志里要靠 grep 数——没有这行，就得把 /health 的读数当唯一抓手。
	if e.cannedCold[key] == 1 {
		observability.Warn(ctx, "assist.engine canned 首屏翻译出现新的失败组合（该档第一次计数）",
			"kind", kind, "lang", uiLang, "reason", reason, "total", e.cannedColdTotal)
	}
}

// CannedBackoff 当前配置的退避窗口长度（管理台可改，硬夹到 [30s, 6h]）。
func (e *Engine) CannedBackoff() time.Duration {
	return e.cannedBudgetSec(cfgCannedBackoffSec, cannedBackoffDefaultSec, cannedBackoffMinSec, cannedBackoffMaxSec)
}

// markCannedBackoff 开（或续）一个窗口。key 用**在途声明那一个**（kind+语种+指纹）：
// 运营改了原文 ⇒ 指纹变 ⇒ 旧窗口对新键无感，新那一档立刻可以重打（不该替一次改动作请假 5 分钟）。
func (e *Engine) markCannedBackoff(key string) {
	e.cannedColdMu.Lock()
	defer e.cannedColdMu.Unlock()
	if e.cannedBackoff == nil {
		e.cannedBackoff = map[string]time.Time{}
	}
	e.cannedBackoff[key] = time.Now().Add(e.CannedBackoff())
}

// clearCannedBackoff 清窗（后台腿成功落库那一刻）。
func (e *Engine) clearCannedBackoff(key string) {
	e.cannedColdMu.Lock()
	defer e.cannedColdMu.Unlock()
	delete(e.cannedBackoff, key)
}

// cannedBgInBackoff 这一键是否正处于退避窗口内；**过期即就地删除**，
// 于是这张表只会在窗口活跃的几分钟里有条目，不会长成需要后台清扫的常驻表。
func (e *Engine) cannedBgInBackoff(key string) bool {
	e.cannedColdMu.Lock()
	defer e.cannedColdMu.Unlock()
	until, ok := e.cannedBackoff[key]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(e.cannedBackoff, key)
		return false
	}
	return true
}

// cannedBackoffRemaining 窗口还剩多久（只给日志读数用；读不到按 0）。
func (e *Engine) cannedBackoffRemaining(key string) time.Duration {
	e.cannedColdMu.Lock()
	defer e.cannedColdMu.Unlock()
	until, ok := e.cannedBackoff[key]
	if !ok {
		return 0
	}
	if d := time.Until(until); d > 0 {
		return d
	}
	return 0
}

// CannedHealth /health 的 canned 段读数（★ 0AR ㊷④）：状态词 ＋ 总数 ＋ 活跃窗口数 ＋ 按档明细。
//
// 出这一份读数的理由与 ⑮ 那条一模一样：**这条链的失败形态是"界面正常、只是慢/只是中文"**，
// 没有告警、没有 5xx，运维只能靠 grep 日志数行。有了状态词，`/api/health` 那一层就能一眼看出
// "此刻有语种在被节流"，而按档明细回答的是"该调模型还是该调闸门"。
// ⚠️ 只出计数与语种/分档名，**绝不出**上游地址、Key、原文或译文文本（同 §一·12 的状态词纪律）。
func (e *Engine) CannedHealth() map[string]any {
	e.cannedColdMu.Lock()
	defer e.cannedColdMu.Unlock()
	now := time.Now()
	active := 0
	for k, until := range e.cannedBackoff {
		if now.After(until) {
			delete(e.cannedBackoff, k) // 就地回收，见 cannedBgInBackoff 同一口径
			continue
		}
		active++
	}
	word := cannedHealthOK
	switch {
	case active > 0:
		word = cannedHealthBackoff
	case e.cannedColdTotal > 0:
		word = cannedHealthCold
	}
	byReason := map[string]int{}
	for k, n := range e.cannedCold {
		parts := strings.Split(k, "\x00")
		if len(parts) != 3 {
			continue
		}
		byReason[parts[2]] += n
	}
	return map[string]any{
		"status":         word,
		"prompt_rev":     cannedPromptRev, // 现跑的哪一代口径：排障先对这一档，再对缓存键头（见 localize.go）
		"fail_total":     e.cannedColdTotal,
		"backoff_active": active,
		"by_reason":      byReason,
		"backoff_sec":    int(e.CannedBackoff().Seconds()),
		"overflow":       e.cannedColdOverflow, // true＝明细字典到过容量上限，此后新组合只并进总数
	}
}

// ResetCannedColdForTest 清空计数与窗口（★ 0AR，只给单测用）。
// 为什么需要一个生产可见的导出方法而不是让测试各造各的 Engine：
// 计数与窗口是**进程级**状态，同一进程里跑多条用例会互相污染读数
// ——「第二条用例的 fail_total 应该只涨 1」这种断言只有先归零才成立，
// 而把它做成 `//go:test-only` 文件又会让判据看起来像"测试专用逻辑"（它其实是这条链的语义）。
func (e *Engine) ResetCannedColdForTest() {
	e.cannedColdMu.Lock()
	defer e.cannedColdMu.Unlock()
	e.cannedCold = map[string]int{}
	e.cannedBackoff = map[string]time.Time{}
	e.cannedColdTotal = 0
	e.cannedColdOverflow = false
}

// cannedClient canned 这一路该用的 LLM client（★ 0AR ㊷①：短文本配非推理型快模型）。
//
// 三档回落写清楚，因为这一条最容易被人"改成写死"：
//   - `canned_llm_model` 空 ⇒ **完全走 ensureLLM 那一条**（＝今天的行为，一字未变）；
//   - 非空但 base/key 在 configs 里取不到（env 接管的实例）⇒ 回落 ensureLLM 并留一行 WARN
//     （现网 env 那几台的 base/key 只存在于进程侧，挂件无权替运维猜一个地址出去）；
//   - 非空且 base/key 齐 ⇒ 用这把模型**优先**、原 llm_model／llm_model_backup 依次排在后面
//     （降级链照旧：快模型挂了仍然翻得动，只是慢；把备用摘掉就是拿可用性换速度）。
//
// 缓存按 (base,key,canned,主,备) 五元组指纹，改任一档即重建——同 ensureLLM 的手法，
// 不在此再造第二套优先级（AGENTS §一·3 那条尺子管的是全局 LLM 四键，这里是挂件自有档位）。
func (e *Engine) cannedClient(ctx context.Context) *llm.Client {
	base := e.ensureLLM(ctx) // 先让常规那条链按自己的指纹更新（副作用：管理台改配置照常热加载）
	canned := strings.TrimSpace(e.db.GetConfig(cfgCannedModel, ""))
	if canned == "" {
		e.cannedMu.Lock()
		e.cannedLLM, e.cannedLLMFP = nil, "" // 键被清空：丢掉旧缓存，回落常规 client
		e.cannedMu.Unlock()
		return base
	}
	url := strings.TrimSpace(e.db.GetConfig("llm_base_url", ""))
	key := strings.TrimSpace(e.db.GetConfig("llm_api_key", ""))
	mainModel := strings.TrimSpace(e.db.GetConfig("llm_model", ""))
	bkModel := strings.TrimSpace(e.db.GetConfig("llm_model_backup", ""))
	fp := strings.Join([]string{url, key, canned, mainModel, bkModel}, "\x00")
	e.cannedMu.Lock()
	if e.cannedLLM != nil && e.cannedLLMFP == fp {
		c := e.cannedLLM
		e.cannedMu.Unlock()
		return c
	}
	e.cannedMu.Unlock()
	if url == "" || key == "" {
		observability.Warn(ctx, "assist.engine canned 专用模型已配但 base/key 不在 configs 里，回落常规 LLM",
			"reason", "canned_model_override_no_base", "model", canned)
		return base
	}
	provs := []llm.Provider{{Name: "canned", BaseURL: url, APIKey: key, Model: canned}}
	if mainModel != "" {
		provs = append(provs, llm.Provider{Name: "main", BaseURL: url, APIKey: key, Model: mainModel})
	}
	if bkModel != "" {
		provs = append(provs, llm.Provider{Name: "backup", BaseURL: url, APIKey: key, Model: bkModel})
	}
	c := llm.New(provs, 45)
	e.cannedMu.Lock()
	e.cannedLLM, e.cannedLLMFP = c, fp
	e.cannedMu.Unlock()
	observability.Info(ctx, "assist.engine canned 首屏翻译改用专用模型",
		"model", canned, "fallbacks", len(provs)-1)
	return c
}

// cannedModelNameForTest 出当前 canned 实际用的第一档模型名（空＝回落）。给断言用，不给出栈。
func (e *Engine) cannedModelNameForTest() string {
	e.cannedMu.Lock()
	defer e.cannedMu.Unlock()
	return e.cannedLLMFP
}
