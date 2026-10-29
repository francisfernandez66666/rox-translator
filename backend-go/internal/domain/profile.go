// ============ profile.go · 职责说明 ============
// 文档级领域词表（B 方案）的公共类型与门槛（2026-10-29 第十二轮）。
//
// 本包**不 import** llm/store：判定调用由 engine 以函数闭包传入（`LLMCall`），
// wiki 走纯 HTTP；这样单测不需要数据库与模型。
// ============================================
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Term 一条「源词 → 目标语译法」。
type Term struct {
	Src string
	Tgt string
}

// TermTargets 一个源词的多语种译法（wiki langlinks 一次取全，按语种各存一份）。
type TermTargets struct {
	Src     string            `json:"src"`
	Targets map[string]string `json:"targets"`
}

// Profile 一次文档级判域结果。
type Profile struct {
	Code       string        `json:"code"`
	Label      string        `json:"label"`
	Confidence float64       `json:"confidence"`
	Terms      []TermTargets `json:"terms"`
}

// TermsFor 取某语种的词表（按出现顺序截断到 max；max<=0 不截断）。
// nil 接收者返回 nil —— 调用方不必到处判空（判域失败是常态，不是异常）。
func (p *Profile) TermsFor(lang string, max int) []Term {
	if p == nil {
		return nil
	}
	out := make([]Term, 0, len(p.Terms))
	for _, t := range p.Terms {
		if v := strings.TrimSpace(t.Targets[lang]); v != "" {
			out = append(out, Term{Src: t.Src, Tgt: v})
		}
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

// DisplayLabel 词表的领域中文名；nil 安全（空串＝不注入）。
func (p *Profile) DisplayLabel() string {
	if p == nil {
		return ""
	}
	return p.Label
}

// Limits 每条链的门槛（用户令：低置信不做）。MinResolved/MaxTerms 都是**每语种**口径。
type Limits struct {
	MinSampleRunes int
	MinConfidence  float64
	MinTerms       int
	MinResolved    int
	MaxTerms       int
}

// Limits 的默认档（按链；运营可用 system_config 覆盖，见 engine.glossaryLimits）。
var (
	// FileLimits 文件/图片工单：异步、可多等几秒。
	FileLimits = Limits{MinSampleRunes: 200, MinConfidence: 0.70, MinTerms: 8, MinResolved: 4, MaxTerms: 12}
	// TextLimits 文本工单：消息更短，门槛略高。
	TextLimits = Limits{MinSampleRunes: 120, MinConfidence: 0.75, MinTerms: 6, MinResolved: 3, MaxTerms: 10}
	// InstantLimits 即时翻译：一句话判域本就不可靠 ⇒ 宁可不做；词数压到 3 是 600ms 预算反推的。
	InstantLimits = Limits{MinSampleRunes: 200, MinConfidence: 0.80, MinTerms: 4, MinResolved: 2, MaxTerms: 3}
)

// Normalize 空白归一（判据与缓存键同源）。
func Normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// Fingerprint 样本指纹（含租户：品牌过滤与术语层去重依赖租户，键不带它会串）。
func Fingerprint(tenantID int64, sample string) string {
	h := sha256.Sum256([]byte(strconv.FormatInt(tenantID, 10) + "|" + Normalize(sample)))
	return hex.EncodeToString(h[:])
}
