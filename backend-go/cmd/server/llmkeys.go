// ============ llmkeys.go · 职责说明 ============
// cmd/server 包内部实现文件：启动期「LLM 全局密钥水合」这一段的可测化抽取（★ R-1 修法 A/B）。
//
// 为什么单独成文件：这段逻辑原来整段内联在 main() 里，而 main() 不可单测——
// 于是「routes 存成 0 行 ⇒ 水合腿一次都不跑 ⇒ 全局 Key 停在随机占位符」这条
// 会让 Pro 翻译／知识库／匿名试用**三个客户面同时 100% 失败**的病灶（R-1），
// 在仓库里活了整整两周、跑过几十轮全绿闸门与两轮 UAT 都没被抓住。
// 抽成函数不是洁癖，是**让 A1/A2/A3 三条断言有地方站**。
//
// 三档优先序（与 AGENTS §一·3「环境变量 > 数据库配置」一致，能进到本函数说明 env 那档就是空的）：
//
//	① 环境变量（config.Default() 已读，非占位则本函数直接返回 env）；
//	② 后台库配置 system_config.online_api_key ＝ 运营在管理台显式保存过的意图；
//	③ 模型路由主路由 model_routes[0].api_key ＝ 历史上唯一在跑的兜腿。
//
// ★ 三条硬口径（都是本次核实里真踩出来的）：
//
//	· ② 必须**在 range 之外**：旧写法把五句 GetConfig 塞在 `for _, r := range cfg.ModelRoutes`
//	  循环体内，routes 为空时循环体不执行 ⇒ 库里明明有可用 Key，引擎却拿着占位符起跑。
//	· ③ 只认「带真实密钥的主路由」：一条 api_key 为空的路由**不能**当水合源——
//	  resolveStageModel 有「阶段未填密钥 ⇒ 继承全局密钥」的语义，拿空值水合等于把
//	  所有阶段腿一起打成空 Key，比不水合更糟。
//	· 两腿皆空 ⇒ **不许静默起跑**（修法 B）：ERROR 日志＋平台级告警＋/api/health 出状态词，
//	  三件事同时做。只出状态词，绝不出 Key、供应商域名或任何可定位坐标（同 §一·12 派发口径）。
//
// =============================================
package main

import (
	"context"
	"log"

	"translator/internal/config"
	"translator/internal/llmsource"
	"translator/internal/observability"
	"translator/internal/store"
)

// llmKeyAlertKind 全局 LLM 密钥缺失的告警类型（对外排障契约，与前端/看板按 kind 分支处理，
// 改名等于改契约；同 kind 的 open 告警由 store.CreateAlertEx 幂等去重，重启风暴不会刷屏）。
const llmKeyAlertKind = "llm_key_placeholder"

// 水合来源档位（/api/health 与日志里出现的就只有这三个词）
const (
	llmKeyFromEnv   = llmsource.FromEnv   // 环境变量给了可用 Key，本函数无事可做
	llmKeyFromDB    = llmsource.FromDB    // 后台库 system_config.online_api_key
	llmKeyFromRoute = llmsource.FromRoute // 模型路由主路由
	llmKeyFromNone  = llmsource.FromNone  // 两腿皆空 ⇒ 仍是随机占位符（调用必 401）
)

// hydrateLLMKeys 按「库配置 ＞ 主路由」两档水合全局 LLM 密钥与配套端点/模型名。
// 参数 cfg: 运行期配置（原地改写）；st: 平台存储，nil 时整段跳过（无库启动形态）。
// 返回: 实际生效的水合来源（llmKeyFrom*）。
// 副作用: 来源为 none 时落一条 ERROR 日志＋一条平台级 critical 告警（去重由 store 保证）。
func hydrateLLMKeys(cfg *config.Config, st *store.Store) string {
	if cfg == nil {
		return llmKeyFromNone
	}
	// ★ 单一解析腿（〇-AR 第 5 波）：优先序判断整体收进 internal/llmsource，
	//   启动期与运行期热加载共用同一把尺子。在这里再抄一份 if 就是"两把尺子"的起点，
	//   而这两把尺子迟早会分叉——本仓计费侧那三把尺子的账就是这么长出来的（AGENTS §一·11）。
	snap := llmsource.Resolve(cfg, st)

	// env 那档已经拿到可用 Key ⇒ 库里两族配置一条都不覆盖（AGENTS §一·3：环境变量优先）
	if snap.From == llmKeyFromEnv {
		snap.ApplyTo(cfg)
		llmsource.PublishFrom(st, snap)
		return llmKeyFromEnv
	}

	// Embedding 一族的库内腿（与翻译 Key 同源管理、独立生效）。
	// ⚠️ 这一族刻意留在启动期：llmsource 的快照只管"发起翻译调用"那一套三件套＋路由，
	//    向量重建另有每请求现读 stage_models.kb_embed 的请求级覆盖腿，不依赖本文件。
	if st != nil {
		if v, _ := st.GetConfig("embed_api_key"); v != "" {
			if dec := store.DecryptSecret(v); dec != "" {
				cfg.EmbedAPIKey = dec
				log.Println("[llmkey] 已从后台配置水合 Embedding Key")
			}
		}
		if v, _ := st.GetConfig("embed_api_base"); v != "" {
			cfg.EmbedAPIBase = v
		}
	}

	snap.ApplyTo(cfg)
	llmsource.PublishFrom(st, snap)
	switch snap.From {
	case llmKeyFromDB:
		log.Println("[llmkey] 已从后台配置水合 在线翻译 Key")
	case llmKeyFromRoute:
		// 只出档位与条数，不出 Key 片段（这一行会被 journal 与运维看板原样采集）
		log.Printf("[llmkey] 全局 API Key 已从主路由水合（路由 %d 条）", len(snap.Routes))
	default:
		return raisePlaceholder(cfg, st)
	}
	return snap.From
}

// raisePlaceholder 两腿皆空的显式暴露（★ R-1 修法 B「占位 Key 不许静默起跑」三条同时上）：
// ① ERROR 级结构化日志（旧形态是一条 INFO 警告埋在启动日志中段，R-1 就是这么藏了两周）；
// ② 平台级 critical 告警（alerts 表，同 kind open 去重）；
// ③ 配置上打标记，由 /api/health 出 `llm_global_key:"placeholder"` 状态词
//
//	（标记即 cfg.OnlineAPIKeyIsPlaceholder 本身，见 internal/api 的 health 侧读法）。
//
// 参数 st 可为 nil（无库启动形态：只出日志，落不了告警）。
func raisePlaceholder(cfg *config.Config, st *store.Store) string {
	msg := "LLM 全局密钥缺失：环境变量、后台配置与模型路由三档都没给出可用 Key，" +
		"Pro 翻译／知识库索引／匿名试用三条客户腿将全部失败（现用随机占位 Key，调用必 401）"
	observability.Error(context.Background(), "llm_global_key_placeholder", "detail", msg)
	if st != nil {
		// 告警写失败不能挡启动（alerts 表故障属另一条链），但必须留痕在日志里
		if err := st.CreateAlert(0, "critical", llmKeyAlertKind, msg); err != nil {
			observability.Error(context.Background(), "llm_global_key_alert_write_failed", "err", err.Error())
		}
	}
	return llmKeyFromNone
}
