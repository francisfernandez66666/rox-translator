// ============ 本文件职责中文说明 ============
// 引擎「对外稳定错误码」的单一事实源，以及缺陷 ⑭ 的出口收敛（★ 修法 F，2026-10-04 〇-AR 第 2 波）。
//
// 为什么要单独一个文件：错误码是**对外契约**（对话 SSE 帧、/api/translate 的 res.error、
// OpenAPI 的 error_code 三处都在消费它），而码的判定散在各业务文件里迟早长出第二套口径
// ——F-53 那次就是「敏感词拒译把码当文案发出去」。故：码只在这里登记，消费方一律问 IsStableErrorCode。
//
// 为什么要出口收敛：HandleText 在模型/知识库全腿失败时仍返回 Error==""（空译文被 text.go
// 的「跳过空译文不拼接」静默吞掉），于是三个客户面同时说谎：对话 SSE 发 done 帧带空壳、
// OpenAPI 回 success:true＋空 translations、只有试用面靠 trial.go 自己补了 500。
// 本文件把判据收在 HandleText 这一条出口咽喉上：**码走 Error、人话走 Reply**，三面自然同口径。
// =============================================
package engine

import (
	"context"
	"strings"

	"translator/internal/observability"
)

// CodeUpstreamFailed 「请求确有目标语种，但一条可用译文都没有」的稳定错误码（★ 修法 F）。
// 命名与 CodeSensitiveBlocked 同族：小写下划线、语义稳定、供应商文案改动不影响它。
const CodeUpstreamFailed = "upstream_failed"

// CodeInsufficientBalance 积分/余额类中止对外用的稳定码。
// ⚠️ 字面量必须与 api／billing 侧既有对外码逐字相同（那边是 errors.OpenAPIInsufficient
// 与 billing.NewQuotaErr(..., "insufficient_balance")），前端 useChat 的充值引导分支按这一码命中。
// 跨包同步由 internal/api/upstream_failure_face_test.go 的
// TestEngineStableCodesMatchExternalContractList 机械锁住，改任何一侧当场红灯。
const CodeInsufficientBalance = "insufficient_balance"

// UpstreamFailureReply 上游全腿失败时给客户的那句人话（与 CodeUpstreamFailed 配对）。
// 口径：说「暂时不可用＋可重试」，不说「服务坏了」——多数这类失败是上游配额/网络窗口，
// 换一次或稍后重试就能过去；把客户引向「别用我们了」是错的归因。
const UpstreamFailureReply = "⚠️ 翻译服务暂时不可用（上游未返回可用译文），请稍后重试；长文本请改用翻译工单，系统会自动重跑。"

// stableErrorCodes 引擎对外稳定错误码登记表（单一事实源，派生式判据从这里取）。
// 说明：这里只登记「Error 字段装的是码、人话在 Reply 里」这一族失败；
// 其余 Error 取值本来就是给人看的中文句子（「文件不存在或无法读取」一类），不进表、也不该进。
var stableErrorCodes = map[string]bool{
	CodeSensitiveBlocked:    true, // 合规闸拒译（sensitive.go）
	CodeUpstreamFailed:      true, // 上游全腿无产出（本文件）
	CodeInsufficientBalance: true, // 实时计费余额不足中止（store/billing 侧同码）
}

// IsStableErrorCode 判定 errCode 是否为「码在 Error、话术在 Reply」这一族对外稳定码。
// 参数 errCode: HandleText 返回的 Error 字段原文（可能是一句人话，也可能是码）。
// 返回: true 表示消费方必须把 errCode 当**错误码**下发（error_code），文案改取 Reply。
func IsStableErrorCode(errCode string) bool {
	return stableErrorCodes[strings.TrimSpace(errCode)]
}

// targetsAllEmpty 判定「请求确有目标语种，但结果里没有任何一条非空译文」。
// 参数 res: HandleText 的核心结果（可能为 nil）。
// 返回: true 即「空壳成功」形态——需要被收敛成失败。
//
// 判据刻意用 TargetLangs 而不是「Transitions 为空」：
//   - 非翻译类早退分支（空输入、未指定语言的追问、账号不可用）TargetLangs 为空 ⇒ 不判失败，
//     它们回的是**有内容的话术**，不是「翻译失败」；
//   - 部分语言成功（余额中途中止、单语种上游失败）TargetLangs 非空但有非空译文 ⇒ 不判失败，
//     那是「结果不完整」，由 GateWarnings／漏译硬闸那条腿负责，不许整单作废。
func targetsAllEmpty(res *TextTranslateResult) bool {
	if res == nil {
		return false
	}
	langs := res.Data.TargetLangs
	if len(langs) == 0 {
		return false
	}
	for _, lc := range langs {
		if strings.TrimSpace(res.Data.Translations[lc]) != "" {
			return false
		}
	}
	return true
}

// convergeEmptyResult 在 HandleText 出口把「空壳成功」翻成诚实失败（★ 修法 F 的落点）。
// 参数 ctx: 请求上下文（用于读实时计费的中止原因）；res: 核心流程结果，就地改写。
// 返回: 是否发生了改写（true＝本次结果是「一条可用译文都没有」）。
//
// 只动 Error=="" 的结果：敏感词闸（拒译）与更上层已经置码的失败一律不覆盖，
// 免得把一个已经有因的失败改写成「上游不可用」这种错归因。
// 余额不足单独走 CodeInsufficientBalance：客户看到「暂时不可用，请稍后重试」会白等，
// 看到「余额不足，请充值」才知道要做什么——这句话前端 useChat 已有充值引导分支接。
func (e *Engine) convergeEmptyResult(ctx context.Context, res *TextTranslateResult) bool {
	if res == nil || res.Error != "" || !targetsAllEmpty(res) {
		return false
	}
	if reason := strings.TrimSpace(abortReasonFrom(ctx)); reason != "" {
		res.Error = CodeInsufficientBalance
		res.Reply = "⚠️ " + reason + "：本次翻译未产出任何译文，请充值或升级套餐后重新发送。"
		res.Data.GateWarnings = append(res.Data.GateWarnings, res.Reply)
		// WARN 而非 ERROR：这是客户侧余额状态，不是平台故障；但它同样要出现在日志里，
		// 否则「翻译请求失败」在监控上只剩上游一条腿，欠费中止会被误判成供应商问题。
		observability.Warn(ctx, "翻译出口收敛：余额中止导致零译文", "code", res.Error, "reason", reason, "target_langs", len(res.Data.TargetLangs))
		return true
	}
	res.Error = CodeUpstreamFailed
	res.Reply = UpstreamFailureReply
	res.Data.GateWarnings = append(res.Data.GateWarnings, res.Reply)
	// ERROR 级：一条可用译文都没有＝这一单对产品来说没发生过，需要被值班看见。
	observability.Error(ctx, "翻译出口收敛：目标语种零可用译文（旧形态会报成功空壳）",
		"code", res.Error, "target_langs", len(res.Data.TargetLangs))
	return true
}
