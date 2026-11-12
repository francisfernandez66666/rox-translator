// ============ product_fact_i18n_api.go · 职责说明 ============
// 三期 W4：产品事实 12 语种译文的**写时预生成口**（§0.4 第 4 条：热路径零 LLM）。
//
//   - 写时（事实确认写口／后台批任务）调 PreGenerateFactI18n：对 FactI18nLangs 逐语种
//     调 SingleLangTranslate(sourceLang="zh"、stage=config.StageAIInitial) 预生成译文落库，
//     翻译腿自己走 usage_ledger 记账（本口不重复记账）。中文档＝原文自身，不打无意义的一枪。
//   - 幂等（A13c M0240）：同 (fact, lang, source_hash) 已有译文 ⇒ 跳过，不重复打 LLM、
//     不重复计费、不覆盖运营手工档；原文变更（hash 变）⇒ 译文失效重生成（M0239）。
//   - ★ 绝不 import engine：engine 已 import store，反向 import 会成环。FactTranslator 接口的
//     方法签名逐字对齐 (*engine.Engine).SingleLangTranslate（internal/engine/engine.go:1750），
//     生产由主 agent 在 api 组合根把 s.Engine 传进来（它天然满足本接口），测试传 fake。
//     ★ 热路径（assist_open_proxy.go / assist_sales_facts.go）零本口调用（A13c 源码扫描 =0）。
//
// 只用 internal/observability 的 slog，禁 log.Printf；不往 billing.go/kbpackages.go/store.go 追加方法。
// ================================================
package store

import (
	"context"
	"fmt"
	"strings"

	"translator/internal/config"
	"translator/internal/kb"
	"translator/internal/observability"
)

// FactTranslator 翻译腿抽象：方法签名逐字对齐 (*engine.Engine).SingleLangTranslate。
// ★ 刻意用接口、不 import engine：engine 已 import store，反向 import 成环；
//
//	*engine.Engine 天然满足本接口，生产由 api 层直接传入，store 零 engine 依赖。
//
// 参数约束同 SingleLangTranslate：ctx=上下文；zhText=中文原文；targetLang=目标语种码；
// examples=术语示例（预生成传 nil）；sourceLang=原文语种；stage=记账档位。
type FactTranslator interface {
	SingleLangTranslate(ctx context.Context, zhText, targetLang string, examples []*kb.Row, sourceLang, stage string) (string, error)
}

// canonicalFactLang 把语种码归一成「小写＋下划线」（与 canonicalLang 同口径，不反向认识 engine 包）。
// 参数：lang=原始语种码；返回归一后的语种码。
func canonicalFactLang(lang string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(lang)), "-", "_")
}

// PreGenerateFactI18n 写时预生成 12 语种译文并幂等落库。
// 参数：ctx=上下文；tid=租户；factID=事实主键；zhText=中文原文；tr=翻译腿（nil 直接报错）；
// 返回：预生成错误（任一语种失败即中断，绝不落半套译文充当全量）。
func (s *Store) PreGenerateFactI18n(ctx context.Context, tid, factID int64, zhText string, tr FactTranslator) error {
	if tr == nil {
		return fmt.Errorf("产品事实译文预生成缺少翻译腿（FactTranslator 为 nil）")
	}
	if strings.TrimSpace(zhText) == "" {
		return fmt.Errorf("产品事实译文预生成的中文原文为空")
	}
	hash := factSourceHash(zhText)
	for _, raw := range FactI18nLangs {
		lang := canonicalFactLang(raw)
		// ① 已有行：运营手工档永不让位；同 hash 的合法译文幂等跳过（不重复计费）。
		if old, ok := s.factI18nRow(tid, factID, lang); ok {
			if old.Engine == factI18nEngineManual {
				continue
			}
			if old.SourceHash == hash {
				continue // 幂等：原文没变，译文没失效，绝不重复打 LLM
			}
		}
		// ② 中文档＝原文自身：不打 LLM（省一次无意义调用），仍落行保证 12 语种齐全。
		var value, engine string
		if lang == "zh" {
			value, engine = zhText, "identity"
		} else {
			got, err := tr.SingleLangTranslate(ctx, zhText, lang, nil, "zh", config.StageAIInitial)
			if err != nil {
				return fmt.Errorf("产品事实 %d 的 %s 译文预生成失败: %w", factID, lang, err)
			}
			value, engine = strings.TrimSpace(got), "single_lang"
			if value == "" {
				// 空译文绝不落行：读口按「缺译文」判无判据 ⇒ 守卫 fail-closed 回避。
				return fmt.Errorf("产品事实 %d 的 %s 译文为空，拒绝落行（fail-closed）", factID, lang)
			}
		}
		// ③ 幂等落行：hash 绑定当前原文，原文一改旧行自动失效。
		if err := s.UpsertFactI18n(ProductFactI18nRow{
			TenantID:   tid,
			FactID:     factID,
			Lang:       lang,
			Value:      value,
			SourceHash: hash,
			Engine:     engine,
		}); err != nil {
			return err
		}
	}
	observability.Info(ctx, "产品事实 12 语种译文预生成完成",
		"tenant_id", tid, "fact_id", factID, "langs", len(FactI18nLangs), "source_hash", hash)
	return nil
}
