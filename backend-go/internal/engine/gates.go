// ============ 本文件职责中文说明 ============
// 输出质量闸门：主翻译路径（文本 HandleText / 文件 HandleFile）在翻译完成后对译文
// 施加的约束与文化校验层（整改 R1）。
//   - gateCtx：主翻译路径的质量闸门上下文，自带调用方租户的安全句（避雷词/风格/替换对），
//     避免每条译文逐次查库；newGateCtx 负责加载调用方租户及其共享宿主（租户1）的安全句。
//   - check：对单条译文执行约束闸门 gate.Run（数字/金额/单位/专有名词一致性等硬校验）
//     与语言文化闸门 culture.Run（避雷词/格式/语气），retry=true 时约束项首轮不过带反馈重翻一次。
//   - applyOutputGates / applySegmentGates：分别面向文本主路径与文件主路径的批量闸门校验，
//     返回仅以警告形式透出的违规提示，绝不静默丢弃译文内容。
//
// =============================================
package engine

import (
	"context"
	"fmt"
	"strings"

	"translator/internal/config"
	"translator/internal/culture"
	"translator/internal/gate"
	"translator/internal/observability"
	"translator/internal/store"
	"translator/internal/tenant"
)

// gateCtx 主翻译路径（整改 R1）输出质量闸门上下文：
// 自带调用方租户的安全句（避雷词 / 风格 / 替换对），避免每次逐条查库。
type gateCtx struct {
	safety []*store.KBSafetyPhrase
	e      *Engine
	tid    int64
	orgID  int64
	// ★ H1 术语强制：本批译文共享的术语命中缓存（key=srcLang|source|lang）
	termCache map[string][]gate.TermRequirement
	// ★ 2026-10-29 第十一轮（T-B）：本请求的 KB 可见包白名单（每请求只装配一次）。
	//   用集合而不是 *kb.PackScope，免得 gates.go 为一个字段类型再 import internal/kb。
	allow map[int64]bool
}

// kbAllow 懒装配本请求的可见包白名单（企业/组织链/行业/角色/语言文化）。
//
// 为什么懒：纯模型请求（无 KB）不该为它多查几跳；第一次真要用术语时才装配，
// 之后同一个 gateCtx 复用（gateCtx 是每请求一份）。
func (g *gateCtx) kbAllow(ctx context.Context) map[int64]bool {
	if g.allow == nil {
		g.allow = packAllowSet(g.e.userScope(ctx, g.tid))
	}
	return g.allow
}

// termReqs 查询并缓存「源文命中 → 该语种规定译法」的 KB 术语要求（layer=1）。
//
// ★ 2026-10-29 第十一轮（T-B）：查询范围从「本租户」扩到**当前用户的 KB 可见域**
// （企业/组织链/行业/角色/语言文化；装配规则只由 BuildUserPackScope 一处负责）。
// 为什么不是「tenant 0 全量」：tenant 0 下挂着所有行业包和所有角色包，全量放进来
// ＝财务租户拿到销售术语、两个行业互相冲突（本仓「多把尺子」那一族）。
func (g *gateCtx) termReqs(ctx context.Context, source, lang string) []gate.TermRequirement {
	if g.e == nil || g.e.St == nil || g.tid <= 0 {
		return nil
	}
	srcLang := DetectSourceLang(source)
	key := srcLang + "|" + source + "|" + lang
	if v, ok := g.termCache[key]; ok {
		return v
	}
	ents, err := g.e.St.FindTermsBySubstringAllowed(srcLang, source, g.kbAllow(ctx))
	if err != nil {
		ents = nil
	}
	seen := map[string]bool{}
	var out []gate.TermRequirement
	for _, ent := range ents {
		if ent == nil || ent.Layer != 1 || ent.TargetLang != lang {
			continue
		}
		if strings.TrimSpace(ent.SourceText) == "" || strings.TrimSpace(ent.TargetText) == "" {
			continue
		}
		// ★ 2026-10-26 P2-03：目标文本书写系统与目标语言明显不符的脏术语（如 target_lang=en
		//   却把目标写成中文「英语」）不得进入硬闸——否则任何含源词的正文都会被判「未体现」。
		//   入库/导入侧也会拦（api/kb.go、api/admin_kb.go），这里是第二道防线，防存量脏行。
		//   品牌术语（module=brand）豁免：品牌名可能刻意保留汉字（如中文品牌在英文文案里
		//   仍写中文），不能按“书写系统不符”判脏。
		if ent.Module != "brand" && !gate.TargetTextMatchesLang(lang, ent.TargetText) {
			observability.Warn(context.Background(), "跳过目标书写系统不符的术语",
				"term_id", ent.ID, "target_lang", lang, "source_len", len([]rune(ent.SourceText)))
			continue
		}
		// ★ 2026-10-29 第十一轮：同一个 source 只留一条（按 SQL 的 pack_type 优先级取第一条）。
		//   RunWithTerms 要求**每条**要求的 target 都出现；两条不同译法会让这句话永远过不了闸
		//   （每段重翻 + 一条 KB术语遵循 警告），这正是 T-A-0 查重的根因。
		k := strings.ToLower(ent.SourceText) + "\x00" + ent.TargetLang
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, gate.TermRequirement{Source: ent.SourceText, Target: ent.TargetText})
	}
	g.termCache[key] = out
	return out
}

// newGateCtx 构造输出闸门上下文，加载调用方租户及共享宿主（租户1）的安全句。
func (e *Engine) newGateCtx(ctx context.Context) gateCtx {
	gc := gateCtx{e: e, termCache: map[string][]gate.TermRequirement{}}
	if e.St == nil {
		return gc
	}
	tid := tenant.FromContext(ctx)
	gc.tid = tid
	if uid := tenant.UserFromContext(ctx); uid > 0 {
		if u, uerr := e.St.GetUser(uid, tid); uerr == nil && u != nil {
			gc.orgID = u.OrgID
		}
	}
	if tid > 0 {
		if ph, err := e.St.ListSafetyPhrases(tid); err == nil {
			gc.safety = ph
		}
		// 共享宿主（租户1）投放的全局安全句对本租户同样生效
		if ph, err := e.St.ListSafetyPhrases(1); err == nil {
			gc.safety = append(gc.safety, ph...)
		}
	}
	return gc
}

// strictLabelTerm 固定标签的确定性译法：源段逐字等于某条术语源文、≤8 字、且唯一命中。
//
// 为什么需要这一档（2026-10-29 第十一轮 T-C）：现有硬闸只在「译文残留中文源词」时确定性覆写
// （gate.ForceTerms），全英文译文走的是「重翻一次 + 警告」——那一句到底对不对是概率性的。
// 固定 UI 标签（`卖出胜负` 这类）本来就是"一个词对应一个译法"，可以确定性收口。
//
// 边界三条，一条都不许放宽：逐字相等（不是包含）、≤8 字（长句仍走重翻）、唯一命中（两条不同译法不猜）。
func strictLabelTerm(source string, terms []gate.TermRequirement) (string, bool) {
	s := strings.TrimSpace(source)
	if s == "" || len([]rune(s)) > 8 {
		return "", false
	}
	hit, n := "", 0
	for _, t := range terms {
		if strings.TrimSpace(t.Source) == s && strings.TrimSpace(t.Target) != "" {
			hit, n = t.Target, n+1
		}
	}
	if n != 1 {
		return "", false
	}
	return hit, true
}

// check 对单条译文执行约束闸门（gate.Run 8 项硬校验）与语言文化闸门（culture.Run 避雷词/格式/语气）。
// retry=true 时若约束项首轮不过，则带反馈让模型重翻一次（仅文本主路径使用，文件主路径为成本考虑不重翻）。
// 返回（修正后译文，警告列表）；任何情况下均不静默丢弃译文内容（仅以警告形式透出）。
func (g *gateCtx) check(source, lang, translated string, retry bool, e *Engine, ctx context.Context) (string, []string) {
	var warnings []string
	tr := translated
	if strings.TrimSpace(tr) == "" {
		return tr, warnings
	}
	// 1) 约束闸门：数字/金额/单位/专有名词一致性等硬校验
	// ★ H1 术语强制 100%：命中术语且译文残留源术语字面 → 直接以规定译法覆写；
	//   剩余违规（第三种写法）带术语要求重翻一次，仍违规则记警告（不静默放行）。
	terms := g.termReqs(ctx, source, lang)
	if len(terms) > 0 {
		if forced, n := gate.ForceTerms(source, tr, terms); n > 0 {
			tr = forced
			warnings = append(warnings, fmt.Sprintf("%s 术语强制替换 %d 处（KB 规定译法覆写）", langLabel(lang), n))
		}
	}
	gr := gate.RunWithTerms(source, lang, tr, terms)
	if !gr.Pass {
		if retry {
			msg := "请修正以下质量问题后重新翻译：" + firstGateFail(gr.Checks)
			// ★ 重译结果的采纳门槛＝「过闸」∧「过可用性判定」两条同时成立（2026-10-19 现网图片单）。
			//   闸门查的是数字/格式/术语/脚本这几项**形态**，它对「一整句英文指令回显」是放行的；
			//   而重译腿的提示词本身就是一句指令（「请严格按照意见修正重译」），模型偶发把这句
			//   **译成目标语言回吐** ⇒ 过闸即被采纳，客户屏幕上出现
			//   `<target language is reasonable. Please strictly follow the revision suggestions and
			//   retranslate the text.`（现网实证：源段是符号 `<`，闸门打回后重译回吐该句，
			//   它曾覆盖掉 identity 预填的原文）。
			//   IsTranslationUsable 里的 nonLinguisticMustStayVerbatim / hasInstructionEcho 正是
			//   为这一类回显而设，此处漏接＝那道判定对「文件重译腿」完全无感（同 RC-2 的装配级教训：
			//   判定函数存在且有单测，装配漏一处照样复发）。
			if fixed := e.TranslateWithFeedback(ctx, source, lang, msg, config.StageAIInitial); strings.TrimSpace(fixed) != "" &&
				IsTranslationUsable(source, fixed) && gate.RunWithTerms(source, lang, fixed, terms).Pass {
				tr = fixed
			}
		}
		if gr = gate.RunWithTerms(source, lang, tr, terms); !gr.Pass {
			// ★ 2026-10-29 第十一轮（T-C）：固定标签（源段逐字等于术语源文、≤8 字、唯一命中）
			//   直接采用规定译法——否则全英文译文只能靠重翻一次（概率性）。长句/包含关系不走这一档。
			if fixed, ok := strictLabelTerm(source, terms); ok && fixed != tr {
				tr = fixed
				if gr = gate.RunWithTerms(source, lang, tr, terms); gr.Pass {
					warnings = append(warnings, fmt.Sprintf("%s 固定标签按术语表采用 %q", langLabel(lang), fixed))
				} else {
					warnings = append(warnings, fmt.Sprintf("%s 固定标签按术语表采用 %q；仍有其他校验未通过(%s)", langLabel(lang), fixed, firstGateFail(gr.Checks)))
				}
			} else {
				warnings = append(warnings, fmt.Sprintf("%s 译文未通过质量校验(%s)", langLabel(lang), firstGateFail(gr.Checks)))
			}
		}
	}
	// 2) 语言文化闸门：输出侧避雷词 / 格式 / 语气校验（仅警告透出，不在主路径静默丢弃）
	cr := culture.Run(lang, tr, g.safety)
	if !cr.Pass {
		warnings = append(warnings, fmt.Sprintf("%s 语言文化提示：%s", langLabel(lang), strings.Join(cr.Reasons, ";")))
	}
	return tr, warnings
}

// applyOutputGates 文本主路径（HandleText）：对全部目标语言译文做一次性质量闸门校验。
func (e *Engine) applyOutputGates(ctx context.Context, source string, translations map[string]string, retry bool) []string {
	if len(translations) == 0 {
		return nil
	}
	gc := e.newGateCtx(ctx)
	var warnings []string
	for lc, tr := range translations {
		fixed, ws := gc.check(source, lc, tr, retry, e, ctx)
		if fixed != tr {
			translations[lc] = fixed
		}
		warnings = append(warnings, ws...)
	}
	return capWarnings(warnings)
}

// applySegmentGates 文件主路径（HandleFile）：对流式分段译文按语言做质量闸门校验。
// retry=true 时约束闸门首轮不过则带反馈重翻一次（文件交付物如成本表必须保证数字/格式正确，
// 整改：原「文件不重翻」会导致硬闸失效、错误直接落入成品文件）。
func (e *Engine) applySegmentGates(ctx context.Context, langTranslations map[string]map[string]string, retry bool) []string {
	if len(langTranslations) == 0 {
		return nil
	}
	gc := e.newGateCtx(ctx)
	var warnings []string
	for lc, m := range langTranslations {
		for orig, tr := range m {
			if tr == SensitivePlaceholderText { // ★ S8：拦截占位段不进质量闸门
				continue
			}
			fixed, ws := gc.check(orig, lc, tr, retry, e, ctx)
			if fixed != tr {
				m[orig] = fixed
			}
			warnings = append(warnings, ws...)
		}
	}
	return capWarnings(warnings)
}

// firstGateFail 取首个未通过约束项的描述，便于在警告/重翻反馈中提示。
// Detail 存在时一并附带（如哈萨克语必须使用西里尔字母），让重翻反馈对模型可执行。
func firstGateFail(checks []gate.Check) string {
	for _, c := range checks {
		if !c.Pass {
			if c.Detail != "" {
				return c.Name + "（" + c.Detail + "）"
			}
			return c.Name
		}
	}
	return ""
}

// langLabel 语言代码转可读标签。
func langLabel(lc string) string {
	if n, ok := config.LangNames[lc]; ok && n != "" {
		return n
	}
	return lc
}

// capWarnings 限制警告条数，避免大文件产生海量提示。
func capWarnings(ws []string) []string {
	if len(ws) > 100 {
		return ws[:100]
	}
	return ws
}
