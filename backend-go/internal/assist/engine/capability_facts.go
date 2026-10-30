// ============ capability_facts.go · 职责说明 ============
// 「能力与档位」注入（★ 2026-10-30 三事合一批 组7，I-2／I-3／G20／G26）。
//
// 为什么不能把它写进 persona/promise_rules：那两段是**运营会整段替换的文案**，
// 数字写进去就等于每调一次档都要改一次配置、发一次版，而漏改的那一次就是**对外错承诺**
// （知识库里还写着旧的"3 MB"而实际已经收紧）。所以这一段与【系统现值】同形：
// 每次建 prompt 前从主服务的**匿名公开口** `/api/instant/caps` 现取一次，60s 缓存。
//
// 三条硬口径（与本文件同等重要的是"不要做什么"）：
//
//	① **只放公开档位宽度**（图片/录音两档上限、免费档宽度、免费语种数、工单上限）。
//	   三个**暗额度**（free_daily_points / free_ip_daily / free_tenant_daily_points）
//	   一个字都不进提示词——模型会顺口把它们当"剩余额度"念给客户（I-3），
//	   而"剩余额度"恰恰是这条链上唯一不能对外报的数字（W-4）。
//	② **取不到就整段不出现**，绝不兜旧值（同 system_values.go 第 2 条）：
//	   兜底数字＝对外错报价／错承诺；宁可让模型说"具体档位看页面"。
//	③ **失败也占缓存位**：主服务抖动时不能让每条对话都先等一次超时。
//
// =============================================
package engine

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// instantCapsDoc /api/instant/caps 的响应结构（字段名与 api.handleInstantCaps 逐字对应）。
type instantCapsDoc struct {
	Success         bool  `json:"success"`
	FreeOn          bool  `json:"free_on"`
	FreeImageMaxMB  int64 `json:"free_image_max_mb"`
	FreeAudioMaxSec int64 `json:"free_audio_max_sec"`
	FreeLangLimit   int64 `json:"free_lang_limit"`
	ImageMaxMB      int64 `json:"image_max_mb"`
	AudioMaxMB      int64 `json:"audio_max_mb"`
	AudioMaxSec     int64 `json:"audio_max_sec"`
	TicketMaxMB     int64 `json:"ticket_max_mb"`
}

// capabilityFactsBlock 返回【即时翻译档位】整段；取不到返回空串（调用方整段不拼）。
func (e *Engine) capabilityFactsBlock(ctx context.Context) string {
	e.capsMu.Lock()
	defer e.capsMu.Unlock()
	if time.Since(e.capsAt) < systemValuesTTL {
		return e.capsBlock
	}
	block := ""
	var doc instantCapsDoc
	if getJSON(ctx, e.MainBaseURL()+"/api/instant/caps", &doc) && doc.Success && doc.ImageMaxMB > 0 {
		block = renderCapabilityFacts(doc)
	}
	e.capsBlock, e.capsAt = block, time.Now()
	return block
}

// CapabilityFactsSnapshot 无视缓存现取一次（供管理台/单测；对话链路走 capabilityFactsBlock）。
func (e *Engine) CapabilityFactsSnapshot(ctx context.Context) string {
	var doc instantCapsDoc
	if !getJSON(ctx, e.MainBaseURL()+"/api/instant/caps", &doc) || !doc.Success {
		return ""
	}
	return renderCapabilityFacts(doc)
}

// renderCapabilityFacts 把结构化档位渲染成一段事实素材。
// 口径：**只陈述能力与宽度**，不谈"还剩多少"；多语种是付费权益要说清（否则客户以为免费也能多语种同出）。
func renderCapabilityFacts(d instantCapsDoc) string {
	var sb strings.Builder
	sb.WriteString("【即时翻译档位（现读，运营改配置后自动跟随；不要把它当成剩余额度）】\n")
	if d.FreeOn {
		langs := "1 个目标语种"
		if d.FreeLangLimit > 1 {
			langs = fmt.Sprintf("%d 个目标语种", d.FreeLangLimit)
		} else if d.FreeLangLimit == 0 {
			langs = "不限目标语种数"
		}
		free := "免费档（登录后可用，不扣积分）：文本、图片、语音识别、朗读"
		if d.FreeImageMaxMB > 0 {
			free += fmt.Sprintf("；图片 ≤ %d MB", d.FreeImageMaxMB)
		}
		if d.FreeAudioMaxSec > 0 {
			free += fmt.Sprintf("；录音 ≤ %d 秒", d.FreeAudioMaxSec)
		}
		sb.WriteString("- " + free + "；每次 " + langs + "。\n")
	} else {
		sb.WriteString("- 免费档当前已关闭：即时翻译按积分计费。\n")
	}
	if d.ImageMaxMB > 0 || d.AudioMaxSec > 0 {
		paid := "付费即时档（按积分计费）"
		if d.ImageMaxMB > 0 {
			paid += fmt.Sprintf("：图片 ≤ %d MB", d.ImageMaxMB)
		}
		if d.AudioMaxSec > 0 {
			paid += fmt.Sprintf("；录音 ≤ %d 秒", d.AudioMaxSec)
		}
		if d.AudioMaxMB > 0 {
			paid += fmt.Sprintf(" 且 ≤ %d MB", d.AudioMaxMB)
		}
		sb.WriteString("- " + paid + "。多语种同出属于付费权益。\n")
	}
	if d.TicketMaxMB > 0 {
		sb.WriteString(fmt.Sprintf("- 超过即时上限的文件交给翻译工单：单次上限 %d MB，译完还原原格式。\n", d.TicketMaxMB))
	}
	sb.WriteString("- 免费剩余额度不对外报数字；被问到就说「按天重置，用完可以继续按积分使用」。")
	return sb.String()
}
