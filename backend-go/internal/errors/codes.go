// ============ 本文件职责中文说明 ============
// 统一错误码体系（工作流 C）：定义全平台错误码枚举 ErrorCode、结构化错误 APIError
// （含 code/message/retry_after/details/trace_id），以及对应的 HTTP 状态码映射与 JSON 写出。
// 目标：前后端以稳定 error_code 通信，而非随实现漂移的中文字符串。
// ★ F-47（批 I-7 2026-09-26）：本文件是「状态码诚实」的唯一口径来源——
//
//	限流类（RATE_LIMITED）映射 429 并带 Retry-After 头＋retry_after 字段，
//	不再与「参数错」共用 400。
//
// ★ F-64①（同批）：开放 API 的 snake_case 对外码也归本文件管——
//
//	openAPIStatusByCode 一张表给出 error_code→HTTP 状态，StatusForCode() 供 api 包查表；
//	旧形态是「error_code 只在 200 响应体里出现」，状态码一路恒 200（任务类）或一律 500
//	（经 APIError 走统一出口时），客户与 SDK 只能解析文案才能知道为什么失败。
//
// =============================================
// Package errors 提供统一错误码体系：ErrorCode 枚举、APIError 结构化错误、
// HTTP 状态码映射与 JSON 写出，供各 API handler 统一返回标准错误。
package errors

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

// ErrorCode 统一错误码（前端据此做 i18n 与分支处理）。
type ErrorCode string

// 系统级错误码
const (
	ErrInternal     ErrorCode = "INTERNAL_ERROR"
	ErrValidation   ErrorCode = "VALIDATION_ERROR"
	ErrUnauthorized ErrorCode = "UNAUTHORIZED"
	ErrForbidden    ErrorCode = "FORBIDDEN"
	ErrNotFound     ErrorCode = "NOT_FOUND"
	ErrConflict     ErrorCode = "CONFLICT"
	ErrRateLimited  ErrorCode = "RATE_LIMITED"
	// ErrMethodNotAllowed 路径存在但方法不在允许集合内（405）。
	// 此前本包只有 4xx 的 400/401/403/404/409，代理类 handler 只能内联手搓 405，
	// 补进枚举是为了让「统一错误出口」真的覆盖到传输层状态（见 api 包错误写法棘轮）。
	ErrMethodNotAllowed ErrorCode = "METHOD_NOT_ALLOWED"
	// ErrUpstreamUnavailable 同源代理的上游服务不可达/响应中断（502）。
	// 与 ErrInternal(500) 区分：这不是本进程出错，而是被代理的服务没起来或断了，
	// 运维与前端兜底策略完全不同（502 该提示「检查服务是否启动」，500 该查 trace_id）。
	ErrUpstreamUnavailable ErrorCode = "UPSTREAM_UNAVAILABLE"
	// ErrServiceUnavailable 依赖未就绪、稍后可能成功（503）。
	// ★ F-64①（批 I-7 2026-09-26）：本仓此前没有 503 档，于是「平台存储未初始化」这类
	//
	//	**明确可重试**的状态只能二选一——要么挤进 500（客户以为服务崩了、放弃重试），
	//	要么写成 200＋success:false（通用重试器根本看不到失败）。503 才是这条路的真值：
	//	它承诺「现在不行、等一下再试有意义」，与 500 的「我们这边出错了」语义分开。
	//
	ErrServiceUnavailable ErrorCode = "SERVICE_UNAVAILABLE"
	// ErrPayChannelUnavailable 收款渠道未就绪（503，★ F-64① 批 I-7）。
	// 覆盖两类同族事实：①渠道资质/凭据未配置或渠道被管理台停用、下单失败；
	// ②静态收款码根本没上传图片。两者客户侧动作一致（换渠道或稍后再试），
	// 运维侧动作一致（照 message 去套餐中心补齐配置），故共用一码。
	// 为什么不是 500：钱没到账**不是**服务端出错，订单已按 pending 落库可重试，
	// 报 500 会让客户以为「我这次充值是不是失败了、要不要再按一次」——那是重单风险的来源。
	// 为什么不是 400：载荷没错，错在平台收款能力当前不可用。
	ErrPayChannelUnavailable ErrorCode = "PAY_CHANNEL_UNAVAILABLE"
)

// 业务级错误码
const (
	ErrInsufficientBalance ErrorCode = "INSUFFICIENT_BALANCE"
	ErrQuotaExceeded       ErrorCode = "QUOTA_EXCEEDED"
	ErrTicketNotFound      ErrorCode = "TICKET_NOT_FOUND"
	ErrKBNotFound          ErrorCode = "KB_NOT_FOUND"
	ErrTranslationFailed   ErrorCode = "TRANSLATION_FAILED"
	ErrFileTooLarge        ErrorCode = "FILE_TOO_LARGE"
	ErrModelUnreachable    ErrorCode = "MODEL_UNREACHABLE"
	ErrCircuitBreakerOpen  ErrorCode = "CIRCUIT_BREAKER_OPEN"
	ErrQuotaReserved       ErrorCode = "QUOTA_RESERVED"
	// ErrChatTextTooLong ★ F-29（2026-09-25 批 D）：/api/chat/stream 文本超运营上限
	// （chat_max_chars，默认 5,000 字符）在 SSE 头写出前被拒（400）。前端据该 code
	// 引导用户改走翻译工单，而非让 Cloudflare 把长跑掐成 524 HTML。
	ErrChatTextTooLong ErrorCode = "chat_text_too_long"
)

// 开放 API 对外错误码（snake_case，Python/TS/Java SDK 依赖，勿改值）。
// 与 OpenAPI 文档错误码表一致；HTTP 状态映射沿用 HTTPStatus()。
const (
	OpenAPIInvalidAPIKey    ErrorCode = "invalid_api_key"
	OpenAPIKeyQuotaExceeded ErrorCode = "key_quota_exceeded"
	OpenAPIForbidden        ErrorCode = "forbidden"
	OpenAPINotFound         ErrorCode = "not_found"
	OpenAPIInternal         ErrorCode = "internal"
	OpenAPIBadRequest       ErrorCode = "bad_request"
	OpenAPITextTooLong      ErrorCode = "text_too_long"
	OpenAPINoResult         ErrorCode = "no_result"
	OpenAPITaskFailed       ErrorCode = "task_failed"
	OpenAPIInsufficient     ErrorCode = "insufficient_balance"
	OpenAPIRateLimited      ErrorCode = "rate_limited"
	OpenAPIDailyQuota       ErrorCode = "daily_quota_exceeded"
	OpenAPIRejected         ErrorCode = "rejected"
	// OpenAPINotReady ★ F-64①（批 I-7 2026-09-26）补登记：/tasks/download 在任务尚未
	// completed（或鬼 completed：状态位已落但产物路径为空）时下发的事实码。
	// 它此前只是 handler 里的裸字面量，既不在本枚举、也不在对外文档的错误码表里，
	// 等于「契约里有、字典里没有」——SDK 侧无法穷举分支，状态码映射也无从登记。
	OpenAPINotReady ErrorCode = "not_ready"
)

// openAPIStatusByCode 开放 API 对外错误码（snake_case）→ HTTP 状态码。
// ★ F-64①（批 I-7 2026-09-26）：与 HTTPStatus() 共用一张表，是「状态码诚实」的落点——
// 本表建立之前，snake_case 对外码全部落进 HTTPStatus() 的 default 分支（500），
// 而 api 侧的任务类错误另有自己的写法：`writeJSON(w, 200, {success:false, error_code:…})`，
// 于是同一个对外契约里「参数没填错」和「服务端崩了」都表现为 200，
// 接入方与 SDK 只能解析响应体字符串才能知道失败原因（通用 HTTP 客户端/重试器全部失效：
// 它们在 200 看来就是成功）。状态码与错误码的分家在这里合流。
//
// 每一档的取法都有理由，不是随手挑的 4xx：
//   - bad_request / text_too_long → 400：请求本身可以改好再发，重试同一份载荷有意义；
//   - invalid_api_key → 401：凭证问题（换 Key 即解），不是权限问题；
//   - forbidden → 403：Key 有效但没有该项权限，重试同一 Key 无意义；
//   - not_found → 404：租户隔离下「跨租户查询一律 404 不泄露存在性」的既有安全口径，
//     状态码必须继续背这个语义（也因此不能顺手升成 403）；
//   - no_result → 404：请求指向的产物不存在（保留期已过/该文件处理失败），
//     与「任务不存在」同为不可重试的缺失，重试要先把产物做出来；
//   - insufficient_balance → 402：与内部 ErrInsufficientBalance 同档，充值后才可能成功；
//   - key_quota_exceeded / rate_limited / daily_quota_exceeded → 429：退避类，
//     与 F-47 同一判据（429 才让通用重试器做退避；写成 400 等于劝客户端放弃或直接猛撞）；
//   - task_failed / not_ready → 409：请求合法但与资源**当前状态**冲突——
//     任务还在 processing 就要下载产物。不用 429 是因为它承诺「等一会儿就成」，
//     而失败态的任务永远等不到产物；不用 400 是因为载荷没错，错在调用时机；
//   - rejected → 403：业务闸门泛化拒绝（没命中余额/频率/上限关键词的兜底档）。
//     这是**服务端策略**在挡，不是参数错也不是服务端故障，
//     历史缺陷（#40 评审）正是把这一档混进余额话术误导客户去充值。
var openAPIStatusByCode = map[ErrorCode]int{
	OpenAPIBadRequest:       http.StatusBadRequest,
	OpenAPITextTooLong:      http.StatusBadRequest,
	OpenAPIInvalidAPIKey:    http.StatusUnauthorized,
	OpenAPIForbidden:        http.StatusForbidden,
	OpenAPINotFound:         http.StatusNotFound,
	OpenAPINoResult:         http.StatusNotFound,
	OpenAPIInsufficient:     http.StatusPaymentRequired,
	OpenAPIKeyQuotaExceeded: http.StatusTooManyRequests,
	OpenAPIRateLimited:      http.StatusTooManyRequests,
	OpenAPIDailyQuota:       http.StatusTooManyRequests,
	OpenAPITaskFailed:       http.StatusConflict,
	OpenAPINotReady:         http.StatusConflict,
	OpenAPIRejected:         http.StatusForbidden,
	OpenAPIInternal:         http.StatusInternalServerError,
}

// StatusForCode 按**字符串形态**的对外错误码取 HTTP 状态（开放 API 的 error_code 出参
// 一直是字符串字面量，见 api 包 writeOpenAPIError），这里避免调用方为了查表再转一次类型）。
// 未知码回 500：宁可让客户看到「服务端错误」这条响亮的红，也不要退回 200 把失败藏进成功里
// （新增码忘了登记本表，会由 api 包 openapi_status_contract_test.go 的「规范 ↔ 表」双向穷举锁
// 先一步红灯——不是等客户撞上 500 才发现）。
func StatusForCode(code string) int {
	if st, ok := openAPIStatusByCode[ErrorCode(code)]; ok {
		return st
	}
	return http.StatusInternalServerError
}

// KnownOpenAPIStatusCodes 导出本表的**副本**，仅供闸门做双向穷举
// （对外文档 openapi.v1.json 的 Error.enum ↔ 本表：文档有码而表里没登记 ⇒ 红灯；
// 表里有码而文档没写 ⇒ 同样红灯）。调用方拿到的是拷贝，改不动真表——
// 基础包不反过来依赖 api 包，闸门只能以「读表 + 读文件」的方式做交叉锁。
func KnownOpenAPIStatusCodes() map[ErrorCode]int {
	out := make(map[ErrorCode]int, len(openAPIStatusByCode))
	for k, v := range openAPIStatusByCode {
		out[k] = v
	}
	return out
}

// APIError 结构化错误响应体。
// 说明：success 字段保持为 false 以兼容既有前端（前端以 res.success 判断成败），
// 新增 code 字段供前端做 i18n 与分支处理，trace_id 供全链路定位。
type APIError struct {
	Success bool      `json:"success"`
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	// RetryAfter ★ F-47（批 I-7 2026-09-26）：限流类错误还应等待的秒数。
	// 只作为**出参字段与 HTTP `Retry-After` 头**存在（omitempty：非限流错误完全不出现该键），
	// 不参与业务判定。由来：同仓两套限流响应口径——登录锁定走统一出口但回 400 且不带冷却时长，
	// 注册/找回密码与邮箱验证码走 `writeJSON(w, 429, …) + Retry-After`，
	// 前端/SDK 既不能按状态码区分「稍后再试」与「参数错」，也拿不到到底要等多久。
	// 现统一为：429 + `Retry-After` 头 + 同名的 JSON 字段（头给通用 HTTP 客户端/重试器，
	// 字段给浏览器端——fetch 读不到自定义头以外的信息时仍有兜底）。
	RetryAfter int         `json:"retry_after,omitempty"`
	Details    interface{} `json:"details,omitempty"`
	TraceID    string      `json:"trace_id,omitempty"`
}

// Error 实现 error 接口。
func (e *APIError) Error() string {
	return string(e.Code) + ": " + e.Message
}

// New 构造带消息的结构化错误。
func New(code ErrorCode, message string) *APIError {
	return &APIError{Code: code, Message: message}
}

// WithDetails 附加结构化细节（返回自身，便于链式）。
func (e *APIError) WithDetails(details interface{}) *APIError {
	e.Details = details
	return e
}

// WithTraceID 附加链路追踪 ID（返回自身，便于链式）。
func (e *APIError) WithTraceID(traceID string) *APIError {
	e.TraceID = traceID
	return e
}

// WithRetryAfter 附加「还需等待秒数」（★ F-47 批 I-7：仅限流类错误使用）。
// 参数 seconds: 冷却剩余秒数；0 或负数一律归零（零值＝omitempty 不出字段，
// 避免把「已经可以重试」的边界态写成 retry_after:0 让客户端再空转一轮）。
func (e *APIError) WithRetryAfter(seconds int) *APIError {
	if seconds > 0 {
		e.RetryAfter = seconds
	}
	return e
}

// HTTPStatus 将错误码映射为标准 HTTP 状态码。
func (e *APIError) HTTPStatus() int {
	switch e.Code {
	case ErrUnauthorized:
		return http.StatusUnauthorized
	case ErrForbidden:
		return http.StatusForbidden
	case ErrValidation, ErrQuotaExceeded, ErrFileTooLarge, ErrChatTextTooLong:
		return http.StatusBadRequest
	case ErrRateLimited:
		// ★ F-47（批 I-7）：限流从 400 组里拆出来单映射 429。
		// 旧口径把「你请求太快，等 T 秒再来」和「参数填错了」压成同一个 400，
		// 而通用重试器/SDK 只会对 429 做退避重试——400 在它眼里是「重试无意义」，
		// 于是客户端要么放弃要么立刻重试打得更凶。同仓 429 的另一半（注册/找回密码、
		// 邮箱验证码）此前用内联 writeJSON(429)，状态码是对的但绕过了统一错误体；
		// 两边在这里合流：统一出口 + 429 + Retry-After。
		return http.StatusTooManyRequests
	case ErrNotFound, ErrTicketNotFound, ErrKBNotFound:
		return http.StatusNotFound
	case ErrConflict:
		return http.StatusConflict
	case ErrMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case ErrUpstreamUnavailable:
		return http.StatusBadGateway
	case ErrServiceUnavailable, ErrPayChannelUnavailable:
		// ★ F-64①（批 I-7）：503 档（依赖未就绪 / 收款能力未就绪）——语义是「等一会儿或换条路再试」，
		// 与 500（本进程出错）和 400（你请求本身写错了）都不同。
		// 附带一层现实考量：Cloudflare 的 Error Pages 明确**不作用于 500/501/503/505**
		// （见 developers.cloudflare.com/rules/custom-errors），因此这两个码即使经 CDN 回源，
		// 响应体也不会被边缘节点换成 HTML 错误页——接入方仍能按 code 分支。
		return http.StatusServiceUnavailable
	case ErrInsufficientBalance:
		return http.StatusPaymentRequired
	case ErrInternal, ErrTranslationFailed, ErrModelUnreachable, ErrCircuitBreakerOpen, ErrQuotaReserved:
		return http.StatusInternalServerError
	default:
		// ★ F-64①（批 I-7）：snake_case 对外码不在上面 switch 的家族分支里（那是内部/上游错误码族），
		// 统一从 openAPIStatusByCode 取——此前它们一律落 500，等于「API Key 无效」这种
		// 客户自己一分钟就能改对的错也被报成服务端故障。两张表共用一处定义，不再分家。
		if st, ok := openAPIStatusByCode[e.Code]; ok {
			return st
		}
		return http.StatusInternalServerError
	}
}

// WriteError 将结构化错误以 JSON 写出（含正确 HTTP 状态码）。
// 若 err.TraceID 为空且 ctx 中存在 trace_id，则自动补全，便于前端联动排查。
// ★ F-47（批 I-7）：RetryAfter>0 时一并落 `Retry-After` 头——限流不给时长，客户端只能瞎猜
// 退避窗口；猜短了继续撞闸（并把失败计数窗口拉长），猜长了用户白等。
func WriteError(w http.ResponseWriter, ctx context.Context, e *APIError) {
	if e.TraceID == "" {
		if tid := TraceIDFromContext(ctx); tid != "" {
			e.TraceID = tid
		}
	}
	e.Success = false
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	w.WriteHeader(e.HTTPStatus())
	_ = json.NewEncoder(w).Encode(e)
}
