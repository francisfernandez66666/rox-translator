// ============ stream.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 本文件实现 SSE（Server-Sent Events）流式翻译与文件翻译、非流式兼容接口、文件下载：
//   - SSE 工具：sseEvent（构造 data: 事件帧）/ sseHeaders（设置流式响应头）
//   - 流式文本翻译（handleChatStream /api/chat/stream）与流式文件翻译（handleTranslateFileStream /api/translate/stream）
//   - 非流式兼容接口（handleChat / handleTranslateFile）：失败一律诚实状态码
//     （闸门拒绝 400/402、模式停用 409、输入超限 400、PDF 超限 400、落盘失败 500），
//     不再用「HTTP 200 + success:false」壳承载业务失败
//   - 文件下载（handleDownload /api/download/），按扩展名推断 Content-Type
// 业务要点：
//   - 所有翻译入口先过配额闸门（gateUsage），成功后按用量计量（meterUsage + countTranslate 指标）
//   - 进度事件 progress 逐步推送（step/done/total/percent），结束推送 done/error
//   - ★ B3：文件流式翻译另推逐段事件 segment_done/segment_final/segments_sealed
//     （边翻边上屏；segments_sealed/error 分支补 billing.Flush，载荷全积分口径零 token）
//   - 上传文件保存到 UploadDir（uniqueName 保证文件名唯一），处理完成后删除

import (
	"sync"

	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/engine"
	apierrors "translator/internal/errors"
	"translator/internal/llm"
	"translator/internal/tenant"
)

// ============ SSE 工具 ============

// sseEvent 构造一个 SSE 事件帧（data: {type, ...payload}）。
// 参数 eventType: 事件类型（如 progress/done/error）；payload: 事件负载字段。
// 返回: 符合 SSE 协议的事件文本（以 "data: " 开头、空行结尾）。
// 说明：type 与负载并成同一个 JSON 对象（一帧只有一个对象，前端按 type 分发）；
// 负载全部是本进程当场构造的普通值，故序列化错误没有可用的上报通道（协议也无错误帧），直接丢弃。
func sseEvent(eventType string, payload map[string]interface{}) string {
	full := map[string]interface{}{"type": eventType}
	for k, v := range payload {
		full[k] = v
	}
	data, _ := json.Marshal(full)
	return "data: " + string(data) + "\n\n"
}

// engineErrorPayload 把引擎的业务失败翻成 SSE error 帧载荷（★ 2026-09-26 〇-U 批 I-8 · F-53）。
// 参数 errStr=引擎的 Error 字段，reply=引擎的 Reply 字段（引擎在拒译类失败时会把人类话术放这里）。
//
// ★ 为什么不能照旧只发 {"error": res.Error}：res.Error 有两种性质完全不同的取值——
//
//	① 本来就是给人看的中文句子（「文件不存在或无法读取」「不支持的格式…」）：直接发没问题；
//	② **稳定错误码**（敏感词拒译的 engine.CodeSensitiveBlocked）：人类话术其实在 Reply 里。
//	  旧写法把码当文案发出去、把文案丢掉，客户气泡里就是一串裸键名 `sensitive_blocked`
//	  （12 份 locale 里没有这个键，前端也无从模板化）——本轮 UAT 实测到的正是这一形态。
//
// 现在 ② 走「error_code 下发稳定码 + error 下发那句人话」，与日限额分支
// （上面 gateErr 走 billing.QuotaErrCode 的同一族写法）对齐；前端有码就按码取本语种词条，
// 没命中词条时至少是一句人话，不会再露键名。① 保持只发 error，**不硬造假码**。
func engineErrorPayload(errStr, reply string) map[string]interface{} {
	if strings.TrimSpace(errStr) == engine.CodeSensitiveBlocked {
		msg := strings.TrimSpace(reply)
		if msg == "" {
			msg = errStr // Reply 意外为空时至少与旧行为一致，绝不发空串（空 error 会让前端显示空白气泡）
		}
		return map[string]interface{}{"error": msg, "error_code": engine.CodeSensitiveBlocked}
	}
	return map[string]interface{}{"error": errStr}
}

// sseHeaders 设置 SSE 响应头（text/event-stream 及禁用缓冲/代理缓冲）。
// 参数 w: HTTP 响应写入器。无返回。
func sseHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	// 禁用 Nginx 等反向代理的缓冲，保证事件实时推送
	h.Set("X-Accel-Buffering", "no")
	h.Set("Connection", "keep-alive")
}

// sseHeartbeat 周期写 SSE 注释帧（`: ping`）作心跳（2026-09-16 P2 整改）：
// 前端据此做「空闲断连」判定（收帧即重置计时）——旧版长间隔（代理静默挂起）时
// 客户端 reader 永挂、UI 卡 loading。注释帧按 SSE 规范被客户端解析器忽略，零侵入。
// 与 D20 写锁共用序列化；返回 stop 必须在 handler 返回前调用（defer），
// 防止向已回收的 ResponseWriter 写入。
func sseHeartbeat(w http.ResponseWriter, flusher http.Flusher, mu *sync.Mutex, every time.Duration) (stop func()) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				mu.Lock()
				fmt.Fprint(w, ": ping\n\n")
				if flusher != nil {
					flusher.Flush()
				}
				mu.Unlock()
			}
		}
	}()
	// stop 同步等待心跳协程退出：保证 handler 返回后不再有对 w 的写入
	return func() { close(done); <-stopped }
}

// ============ 流式文本翻译 ============

// chatMaxChars ★ F-29 后端半（2026-09-25 批 D）：单次对话字符上限（运营策略键 chat_max_chars，
// 默认 5,000 字符，按 rune 计——中文一字一符，与缺陷实测口径一致）。
// 键缺失/非法（非数字或 ≤0）回退默认：宁可保守拒绝，也不让误配置把护栏拆成放行（524 复发）。
func (s *Server) chatMaxChars() int {
	if s.Store != nil {
		if v, err := s.Store.GetConfig("chat_max_chars"); err == nil {
			if n, cerr := strconv.Atoi(strings.TrimSpace(v)); cerr == nil && n > 0 {
				return n
			}
		}
	}
	return 5000
}

// chatTextOverLimit 体积护栏的纯判据（拆出来是为了边界等值锁不必拉起引擎全链）：
// 按 rune 计数、先 TrimSpace（尾部空白不算体积）；恰好等于上限放行、超 1 字符即拒。
func chatTextOverLimit(msg string, maxChars int) bool {
	return len([]rune(strings.TrimSpace(msg))) > maxChars
}

// handleChatStream 流式文本翻译接口（/api/chat/stream，SSE）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 ChatRequest）。
// 事件流：progress 进度事件 → done（携带 result）或 error 事件。
// 流程：解码请求 → 配额闸门 → 引擎流式处理 → 进度推送 → 计量 → 结果/错误事件。
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	// ★ 安全止血（2026-08-26 整改 A1）：翻译入口强制登录——此前匿名请求经 withTenant
	//   兜底注入租户 1 白嫖平台 LLM 配额。必须在 SSE 头写出前返回 JSON 401。
	if s.authUser(r) == nil {
		writeJSON(w, 401, map[string]string{"error": "未登录或登录已过期"})
		return
	}
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求格式错误"})
		return
	}
	// ★ F-29 后端半（2026-09-25 批 D）：chat 通道体积护栏。此前唯一体积闸是全局
	//   withBodyLimit 5MB JSON，28k 字（≈84KB）畅通无阻打到引擎，最后被 Cloudflare
	//   ~100s 掐成 524、前端把 HTML 错误页塞进气泡。上限走运营策略键 chat_max_chars
	//   （默认 5,000 字符，按 rune 计，中文一字一符），超限在 SSE 头写出**前**以
	//   writeError 400 拒绝（code=chat_text_too_long，前端据 code 引导改走翻译工单）。
	if chatTextOverLimit(req.Message, s.chatMaxChars()) {
		n := len([]rune(strings.TrimSpace(req.Message)))
		s.writeError(w, r, apierrors.New(apierrors.ErrChatTextTooLong,
			fmt.Sprintf("文本过长（%d 字符，单次对话上限 %d 字符），长文本请创建翻译工单处理", n, s.chatMaxChars())))
		return
	}
	// SSE 响应头写出后 HTTP 状态码即固定为 200，此后只能靠 error 事件帧表达失败，
	// 所以 401/400 一类拒绝必须全部发生在这一行之前（见上面的鉴权与解码分支）。
	sseHeaders(w)
	// Flusher 用于每帧立即下发；类型断言可能不成立（如测试用的 ResponseRecorder），
	// 因此本文件所有 Flush 调用前都判空，绝不因缺少流式能力而 panic。
	flusher, _ := w.(http.Flusher)

	// 空消息：返回系统问候语（不消耗配额）
	if strings.TrimSpace(req.Message) == "" {
		result := map[string]interface{}{"skill": "system", "reply": "你好！我是能言，把要翻译的文本发给我就行。"}
		fmt.Fprint(w, sseEvent("done", map[string]interface{}{"result": result}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	// 配额闸门：QPS/并发/每日上限/余额校验（不通过则拒绝本次翻译）
	tid, release, gateErr := s.gateUsage(r)
	// 无条件 defer：并发名额只在真正过闸时才被占用，未过闸时 release 是 no-op，
	// 因此即使随后立即 return 也不会误归还别人的名额。
	defer release()
	if gateErr != nil {
		// 限流/余额不足：推送 error 事件并结束
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": gateErr.Error(), "error_code": billing.QuotaErrCode(gateErr)}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	// ★ D20：SSE 写序列化锁（progress/delta 均来自引擎并发管线）
	var sseMu sync.Mutex
	// ★ 心跳：每 20s 一帧注释，防代理/客户端把长间隔误判断连（见 sseHeartbeat）
	defer sseHeartbeat(w, flusher, &sseMu, 20*time.Second)()
	// 进度回调：计算百分比（封顶 99%，完成时单独发 100%）并推送 progress 事件
	prog := func(step string, done, total int) {
		percent := 0
		if total > 0 {
			percent = done * 100 / total
			if percent > 99 {
				percent = 99
			}
		}
		sseMu.Lock()
		defer sseMu.Unlock()
		fmt.Fprint(w, sseEvent("progress", map[string]interface{}{"step": step, "done": done, "total": total, "percent": percent}))
		if flusher != nil {
			flusher.Flush()
		}
	}

	// 调用引擎处理文本翻译（流式回调进度）
	// ★ 注入用户组织（2026-08-26 KB继承链）+ 交互标记（评审整改 R6：可抢占 LLM 保留槽）
	//   + 模式（2026-09-05 计费策略引擎：计量侧按 fast/pro 区分免费/扣费）
	ctx := llm.WithInteractive(tenant.WithLang(tenant.WithMode(s.userOrgCtx(r), engine.ModeFromOptions(req.Options)), tenant.LangFromOptions(req.Options)))
	// ★ D20：token 级流式 sink——上游增量以 delta 事件透传（lang=目标语言；
	//   多目标并发交替输出，前端仅在单目标场景消费逐字渲染）
	ctx = engine.WithStreamSink(ctx, func(lang, delta string) {
		sseMu.Lock()
		defer sseMu.Unlock()
		fmt.Fprint(w, sseEvent("delta", map[string]interface{}{"lang": lang, "text": delta}))
		if flusher != nil {
			flusher.Flush()
		}
	})
	// ★ F-29 后端半（2026-09-25 批 D）：请求级 deadline 90s——SSE 通道此前无服务端超时，
	//   长跑请求由 Cloudflare ~100s 先掐（用户看到 524 HTML 错误页）。本侧在 90s 到点
	//   主动终止管线并出结构化 error 帧（留 10s 余量给帧写出与代理透传）；
	//   只包 runCtx，客户端断开（r.Context cancel）的既有取消语义不变。
	runCtx, runCancel := context.WithTimeout(ctx, 90*time.Second)
	defer runCancel()
	res := s.Engine.HandleText(runCtx, req.Message, req.Options, prog)
	// ★ P3 补齐（2026-09-22，E2E TF2 抓到）：即时翻译 SSE 收尾前同步冲刷计量缓冲。
	//   此前本路径全程不 Flush（非流式 /api/chat、文件流、账单接口都有），用量只进内存
	//   缓冲、等 2s ticker 才落库；而前端是在收到 done 帧后才刷新余额/今日已耗，
	//   于是稳定读到落库前的旧值——用户表现为「翻译完余额不动，刷新页面才变」。
	//   必须放在 done/error 终帧**之前**（而非 handler 返回前）：客户端的刷新与 handler
	//   尾部并发，只有先落库再告知完成，才能保证那一次刷新看到的是新值。
	billing.Flush()
	// ★ F-29（批 D）：90s 到点——引擎被 runCtx 掐停，终帧改出 error（文案与前端批 G 兜底口径对齐）。
	//   不能照常发 done：前端会把半截译文当完整结果渲染，正是 524 之外第二种坏体验。
	if stderrors.Is(runCtx.Err(), context.DeadlineExceeded) {
		sseMu.Lock()
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": "处理超时，长文本请改用翻译工单", "error_code": "chat_timeout"}))
		if flusher != nil {
			flusher.Flush()
		}
		sseMu.Unlock()
		return
	}
	// 推送完成进度（心跳在途：收尾帧同样走写锁序列化）
	sseMu.Lock()
	fmt.Fprint(w, sseEvent("progress", map[string]interface{}{"step": "完成", "done": 1, "total": 1, "percent": 100}))
	sseMu.Unlock()
	if flusher != nil {
		flusher.Flush()
	}
	if res.Error != "" {
		// 翻译失败：推送 error 事件并计入失败指标
		// ★ F-53（批 I-8）：载荷走 engineErrorPayload——敏感词一类「码在 Error、话术在 Reply」的
		//   失败不再把裸键名当文案发给客户。
		sseMu.Lock()
		fmt.Fprint(w, sseEvent("error", engineErrorPayload(res.Error, res.Reply)))
		sseMu.Unlock()
		s.metrics.countTranslate("text", false)
	} else {
		s.metrics.countTranslate("text", true)
		s.grantTranslateTask(r, tid) // ★ #33 任务系统：发起翻译奖励（日 ≤1、周 ≤5）
		sseMu.Lock()
		fmt.Fprint(w, sseEvent("done", map[string]interface{}{"result": res}))
		sseMu.Unlock()
		// Webhook：翻译完成事件回调租户配置的 URL（异步投递，不阻塞 SSE 返回）
		s.dispatchTranslateWebhook(tid, "text", req.Message, res)
	}
}

// dispatchTranslateWebhook 投递翻译完成 webhook 事件（text/file 通用）。
// 参数：tid=租户 ID，kind=任务类型（text/file），source=源文本或文件名，res=引擎结果。
// 说明：本函数在翻译已经完成之后调用，属于「附加通知」，任何情况下都不允许把主流程带崩，
// 故 Store 未初始化时静默返回（不报错、不 panic）；实际投递由 Store 侧异步队列负责。
func (s *Server) dispatchTranslateWebhook(tid int64, kind, source string, res interface{}) {
	if s.Store == nil {
		return
	}
	s.Store.DispatchWebhook(tid, "translation.completed", map[string]interface{}{
		"event":     "translation.completed",
		"tenant_id": tid,
		"type":      kind,
		"source":    source,
		"result":    res,
		"time":      nowRFC3339(),
	})
}

// ============ 流式文件翻译 ============

// handleTranslateFileStream 流式文件翻译接口（/api/translate/stream，SSE）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（multipart：file + target_langs + message）。
// 事件流：progress 进度事件 → ★B3 逐段事件 segment_done/segment_final/segments_sealed
// → done（携带 result）或 error 事件。
// 流程：保存上传文件 → 解析语言参数 → 配额闸门 → 引擎流式处理 → 计量 → 清理文件。
func (s *Server) handleTranslateFileStream(w http.ResponseWriter, r *http.Request) {
	// ★ 安全止血（整改 A1）：强制登录（在解析 multipart 前拒绝，匿名零成本）
	if s.authUser(r) == nil {
		writeJSON(w, 401, map[string]string{"error": "未登录或登录已过期"})
		return
	}
	// 解析 multipart 表单（上限 40MB，仅允许 docx/pptx/xlsx/pdf）
	if err := parseUpload(r, translateUploadMax, translateExtWhitelist); err != nil {
		writeJSON(w, 400, map[string]string{"error": publicErrMessage(r.Context(), err)})
		return
	}
	// 取上传文件
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "缺少文件"})
		return
	}
	defer file.Close()

	// 读取目标语言与提示语参数
	targetLangs := r.FormValue("target_langs")
	message := r.FormValue("message")

	// 解析目标语言列表（逗号分隔，去空白）
	langs := strings.Split(targetLangs, ",")
	clean := []string{}
	for _, l := range langs {
		l = strings.TrimSpace(l)
		if l != "" {
			clean = append(clean, l)
		}
	}
	// 不在此默认 en：由 HandleFile 内部解析 message 语言，再兜底 en
	options := map[string]interface{}{"target_langs": clean}
	// ★ #65：把原件展示名交给引擎做产物取名（落盘名带内部纳秒标记，不得进交付物文件名）
	options["source_name"] = header.Filename
	if message != "" {
		options["message"] = message
		options["_prompt"] = message
	}
	// ★ 缩翻（任务7）：文件翻译最长字符限制（multipart max_length 字段；空=未启用）
	if n, perr := strconv.Atoi(strings.TrimSpace(r.FormValue("max_length"))); perr == nil && n > 0 {
		options["max_length"] = n
	}

	sseHeaders(w)
	flusher, _ := w.(http.Flusher)

	// 配额闸门：QPS/并发/每日上限/余额校验（不通过则拒绝本次文件翻译）
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": gateErr.Error(), "error_code": billing.QuotaErrCode(gateErr)}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	// ★ 整改 A2：闸门通过后再落盘——被限流/超额拒绝的请求不再产生孤儿文件；
	//   创建成功即 defer 清理，任何提前返回路径都不会残留磁盘文件。
	os.MkdirAll(s.Cfg.UploadDir, 0o755)
	// 目录创建错误故意不单独判断：真缺目录时紧随其后的 os.Create 必然失败，
	// 由那一条统一回 error 事件即可，避免同一故障出两套话术。
	// savePath = UploadDir + uniqueName（原始名先被 filepath.Base 清洗，见 uniqueName 注释）
	savePath := filepath.Join(s.Cfg.UploadDir, uniqueName(header.Filename))
	f, err := os.Create(savePath)
	if err != nil {
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": "无法保存文件"}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	if _, err := io.Copy(f, file); err != nil {
		// 此处 defer os.Remove 尚未注册，必须手工清理半截文件，否则磁盘留下不可用的残件
		f.Close()
		os.Remove(savePath)
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": "写入失败"}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	f.Close()
	defer os.Remove(savePath)

	// ★ 性能优化 Phase A1：PDF 前置拦截（大小/页数），超限直接友好拒绝
	if perr := checkPdfLimits(savePath, header.Filename); perr != nil {
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": perr.Error()}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	// ★ 2026-09-16 P2：写锁 + 心跳（与文本流同口径）。心跳延后到此处启动，
	//   覆盖耗时的 HandleFile 阶段；上方所有提前返回的 SSE 写均无心跳并发，无需锁。
	var sseMu sync.Mutex
	defer sseHeartbeat(w, flusher, &sseMu, 20*time.Second)()

	// ★ B3（方案 A2）：逐段事件通道——segment_done/segment_final/segments_sealed
	//   与 progress/心跳共用 sseMu 序列化写。载荷字段全积分口径（lang/index/source/
	//   source_hash/draft/target/stage/placeholder），零 token 裸值（AGENTS.md 约定 5）。
	//   segments_sealed 分支顺手 billing.Flush：该语言计量已定盘，余额/台账即时可见
	//   （此前 SSE 文件路径全程不 Flush，长文件期间余额滞后 - 方案 A2 第 5 条）。
	emitSeg := func(kind string, payload map[string]interface{}) {
		sseMu.Lock()
		fmt.Fprint(w, sseEvent(kind, payload))
		sseMu.Unlock()
		if flusher != nil {
			flusher.Flush()
		}
		if kind == "segments_sealed" {
			billing.Flush()
		}
	}

	// 进度回调：推送 progress 事件（与文本翻译一致，封顶 99%）
	prog := func(step string, done, total int) {
		percent := 0
		if total > 0 {
			percent = done * 100 / total
			if percent > 99 {
				percent = 99
			}
		}
		sseMu.Lock()
		fmt.Fprint(w, sseEvent("progress", map[string]interface{}{"step": step, "done": done, "total": total, "percent": percent}))
		sseMu.Unlock()
		if flusher != nil {
			flusher.Flush()
		}
	}

	// 调用引擎处理文件翻译
	// ★ 注入用户组织（2026-08-26 KB继承链）+ 模式（2026-09-05 计费策略引擎）
	// ★ B3：末参传入逐段事件回调，翻译期间实时推 segment_done/segment_final/segments_sealed
	res := s.Engine.HandleFile(tenant.WithLang(tenant.WithMode(s.userOrgCtx(r), engine.ModeFromOptions(options)), tenant.LangFromOptions(options)), savePath, options, prog, emitSeg)

	// 推送完成进度
	sseMu.Lock()
	fmt.Fprint(w, sseEvent("progress", map[string]interface{}{"step": "完成", "done": 1, "total": 1, "percent": 100}))
	sseMu.Unlock()
	if flusher != nil {
		flusher.Flush()
	}
	if res.Error != "" {
		// 失败：推送 error 事件并计入失败指标
		// ★ F-53（批 I-8）：同文本通道——码/文案分流，不把 engine.CodeSensitiveBlocked 一类
		//   稳定码当用户可见文案发出去。
		sseMu.Lock()
		fmt.Fprint(w, sseEvent("error", engineErrorPayload(res.Error, res.Reply)))
		sseMu.Unlock()
		s.metrics.countTranslate("file", false)
	} else {
		s.metrics.countTranslate("file", true)
		s.grantTranslateTask(r, tid) // ★ #33 任务系统：发起翻译奖励（日 ≤1、周 ≤5）
		// ★ 归属登记（评审整改 C1）：产物可被 /api/download 按 tenant/user 校验
		//   ticketID 传 0 —— 本路径是即时翻译、没有工单行，登记只服务于下载鉴权。
		if u := s.authUser(r); u != nil && s.Store != nil {
			for _, fp := range res.Files {
				s.Store.RegisterArtifact(fp, tid, u.ID, 0)
			}
		}
		sseMu.Lock()
		fmt.Fprint(w, sseEvent("done", map[string]interface{}{"result": res}))
		sseMu.Unlock()
		// Webhook：翻译完成事件回调（异步投递）
		s.dispatchTranslateWebhook(tid, "file", header.Filename, res)
	}
	// ★ B3（方案 A2 第 5 条）：SSE 文件路径补齐计量冲刷——error 分支（含中途失败未及
	//   sealed 的场景）与 done 收尾各兜底一次，与非流式 handleTranslateFile 同口径。
	billing.Flush()
}

// ============ 非流式兼容接口 ============

// handleChat 非流式文本翻译接口（/api/chat，JSON 返回）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 ChatRequest）。
// 返回: 引擎处理结果对象（JSON）；成功时已计量。
// 失败口径（★ F-64② 批 I-10）：本接口非 SSE、响应头未提前写出，故闸门拒绝出 402/400、
// 模式停用出 409、输入超长出 400（统一错误体），只有引擎译出的业务失败仍随 200 的
// res.Error 返回（那是一条正常的「结果」响应，客户端按 res.error 判，不按状态码判）。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	// ★ 安全止血（整改 A1）：强制登录
	if s.authUser(r) == nil {
		writeJSON(w, 401, map[string]string{"error": "未登录或登录已过期"})
		return
	}
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求格式错误"})
		return
	}
	// 配额闸门：QPS/并发/每日上限/余额校验
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		// ★ F-64②（批 I-10）：闸门拒绝（余额/日限额/QPS/并发）是**请求本身当前不可执行**，旧写法回 200 让
		//   SDK 与 OpenAPI 消费方只能去解析中文文案分支；改走 writeGateError（tickets.go 的 F-21③
		//   专用映射）：insufficient_balance → 402，其余闸门拒绝 → 400 QUOTA_EXCEEDED。
		//   原 body 的 error_code 稳定码由统一错误体的 code 承接（前端 core.ts 已按 code/error_code 双别名取码）。
		s.writeGateError(w, r, gateErr)
		return
	}
	// ★ 运营策略引擎（2026-09-05）：模式因子闸门——enabled=false 拒绝；limit_chars 超限拒绝（不计费）
	mode := engine.ModeFromOptions(req.Options)
	eff := s.effectivePolicyCached(tid) // ★ C31
	rule, hasRule := eff.Mode(mode)
	if hasRule && !rule.Enabled {
		// ★ F-64②（批 I-10）：模式被运营停用属「资源当前状态与请求冲突」→ 409（旧 200 壳会让客户端以为可以重试同模式）；
		//   不用 403：403 在本仓是**身份/角色不足**的口径，会误导前端跳无权限页。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "该翻译模式已停用"))
		return
	}
	// 长度闸门按 rune（字符）计，不按 len() 字节：一个汉字 3 字节，用字节会把上限压成 1/3
	if hasRule && rule.LimitChars > 0 && int64(len([]rune(req.Message))) > rule.LimitChars {
		// ★ F-64②（批 I-10）：输入超长是载荷不合法 → 400（与 handleChatStream 的 chat_text_too_long 同族口径，
		//   两条通道同一判据，客户端才不会一边拿到 400、一边拿到 200 壳）。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, fmt.Sprintf("该模式单次输入上限 %d 字符", rule.LimitChars)))
		return
	}
	// 调用引擎处理文本翻译（非流式，无进度回调）
	// ★ 注入用户组织（2026-08-26 KB继承链）+ 交互标记（评审整改 R6）+ 模式（计费策略引擎）
	res := s.Engine.HandleText(llm.WithInteractive(tenant.WithLang(tenant.WithMode(s.userOrgCtx(r), mode), tenant.LangFromOptions(req.Options))), req.Message, req.Options, nil)
	if res.Error != "" {
		// 失败：填充错误回复并计入失败指标
		res.Skill = "translation"
		res.Reply = "❌ 处理出错: " + res.Error
		s.metrics.countTranslate("text", false)
	} else {
		s.metrics.countTranslate("text", true)
		s.grantTranslateTask(r, tid) // ★ #33 任务系统：发起翻译奖励（日 ≤1、周 ≤5）
		// Webhook：翻译完成事件回调（异步投递）
		s.dispatchTranslateWebhook(tid, "text", req.Message, res)
	}
	// ★ P3 修复：交互路径响应前同步冲刷计量缓冲，余额/台账即时可见
	billing.Flush()
	writeJSON(w, 200, res)
}

// handleTranslateFile 非流式文件翻译接口（/api/translate，JSON 返回）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（multipart：file + target_langs + message）。
// 返回: 引擎处理结果对象（JSON）；成功时已计量。
// 失败口径（★ F-64② 批 I-10）：闸门拒绝 402/400、模式停用 409、提示语超限 400、
// PDF 体积/页数超限 400（客户端换个文件即可），落盘/写入失败 500；
// 与 /api/translate/stream 的分工是「头未写出才谈状态码」——SSE 通道里同款失败一律走 error 事件帧。
func (s *Server) handleTranslateFile(w http.ResponseWriter, r *http.Request) {
	// ★ 安全止血（整改 A1）：强制登录（在解析 multipart 前拒绝，匿名零成本）
	if s.authUser(r) == nil {
		writeJSON(w, 401, map[string]string{"error": "未登录或登录已过期"})
		return
	}
	// 解析 multipart 表单（上限 40MB，仅允许 docx/pptx/xlsx/pdf）
	if err := parseUpload(r, translateUploadMax, translateExtWhitelist); err != nil {
		writeJSON(w, 400, map[string]string{"error": publicErrMessage(r.Context(), err)})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "缺少文件"})
		return
	}
	defer file.Close()

	// 读取目标语言与提示语参数
	targetLangs := r.FormValue("target_langs")
	message := r.FormValue("message")

	// 解析目标语言列表
	langs := strings.Split(targetLangs, ",")
	clean := []string{}
	for _, l := range langs {
		l = strings.TrimSpace(l)
		if l != "" {
			clean = append(clean, l)
		}
	}
	// 不在此默认 en：由 HandleFile 内部解析 message 语言，再兜底 en
	options := map[string]interface{}{"target_langs": clean}
	// ★ #65：原件展示名传进引擎做产物取名（与 SSE 文件通道同口径）
	options["source_name"] = header.Filename
	if message != "" {
		options["message"] = message
		options["_prompt"] = message
	}
	// ★ 缩翻（任务7）：文件翻译最长字符限制（multipart max_length 字段；空=未启用）
	if n, perr := strconv.Atoi(strings.TrimSpace(r.FormValue("max_length"))); perr == nil && n > 0 {
		options["max_length"] = n
	}
	// ★ 运营策略引擎（2026-09-05）：文件翻译模式因子闸门——enabled=false 拒绝；
	//   limit_chars 按「提示语 + 待翻源文本总字符」在闸门后由引擎侧校验（见 HandleFile）。
	mode := engine.ModeFromOptions(options)
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		// ★ F-64②（批 I-10）：闸门拒绝（余额/日限额/QPS/并发）此前回 200 壳，客户端要按中文文案猜「是没额度还是欠费」；
		//   统一走 writeGateError（F-21③ 既有映射）：insufficient_balance → 402，其余 → 400 QUOTA_EXCEEDED。
		s.writeGateError(w, r, gateErr)
		return
	}
	eff := s.effectivePolicyCached(tid) // ★ C31
	rule, hasRule := eff.Mode(mode)
	if hasRule && !rule.Enabled {
		// ★ F-64②（批 I-10）：模式被运营停用＝资源状态与请求冲突 → 409（与 handleChat 同判据）；
		//   不用 403，403 在本仓专指身份/角色不足，会让前端误跳无权限页。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "该翻译模式已停用"))
		return
	}
	if hasRule && rule.LimitChars > 0 && int64(len([]rune(message))) > rule.LimitChars {
		// ★ F-64②（批 I-10）：提示语超出该模式单次上限属载荷不合法 → 400（本接口无 SSE 头，状态码可达）。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, fmt.Sprintf("该模式单次输入上限 %d 字符", rule.LimitChars)))
		return
	}
	// ★ 整改 A2：闸门通过后再落盘 + defer 兜底清理（拒绝路径零残留）
	//   目录创建错误不单独判：缺目录时下面 os.Create 必然失败，统一由那一条回 500。
	os.MkdirAll(s.Cfg.UploadDir, 0o755)
	savePath := filepath.Join(s.Cfg.UploadDir, uniqueName(header.Filename))
	f, err := os.Create(savePath)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法保存文件"})
		return
	}
	if _, err := io.Copy(f, file); err != nil {
		// defer os.Remove 尚未注册，须手工删除半截文件
		f.Close()
		os.Remove(savePath)
		writeJSON(w, 500, map[string]string{"error": "写入失败"})
		return
	}
	f.Close()
	defer os.Remove(savePath)
	// ★ 性能优化 Phase A1：PDF 前置拦截（大小/页数），超限直接友好拒绝
	if perr := checkPdfLimits(savePath, header.Filename); perr != nil {
		// ★ F-64②（批 I-10）：文件体积/页数超限是**客户端上传件本身不合格**（换个小文件即可成功）→ 400；
		//   旧 200 壳让 OpenAPI/SDK 把它当提交成功。用 ErrFileTooLarge（映射 400，与
		//   parseUpload 的体积拒绝同族），不改写 perr 的原话术（含「先转存 docx」的可行动指引）。
		s.writeError(w, r, apierrors.New(apierrors.ErrFileTooLarge, perr.Error()))
		return
	}
	// 调用引擎处理文件翻译（非流式）
	// ★ 注入用户组织（2026-08-26 KB继承链）+ 模式（2026-09-05 计费策略引擎）
	res := s.Engine.HandleFile(tenant.WithLang(tenant.WithMode(s.userOrgCtx(r), mode), tenant.LangFromOptions(options)), savePath, options, nil, nil)
	if res.Error == "" {
		s.metrics.countTranslate("file", true)
		s.grantTranslateTask(r, tid) // ★ #33 任务系统：发起翻译奖励（日 ≤1、周 ≤5）
		// ★ 归属登记（评审整改 C1）
		if u := s.authUser(r); u != nil && s.Store != nil {
			for _, fp := range res.Files {
				s.Store.RegisterArtifact(fp, tid, u.ID, 0)
			}
		}
		// Webhook：翻译完成事件回调（异步投递）
		s.dispatchTranslateWebhook(tid, "file", header.Filename, res)
	} else {
		s.metrics.countTranslate("file", false)
	}
	// ★ P3 修复：文件翻译结束同步冲刷计量缓冲（大文件数千条计量合并为一次事务）
	billing.Flush()
	// ★ 真因文案（2026-09-12）：余额耗尽中止导致的「未能译出」不应报「模型调用异常」——
	//   冲刷后双桶已归零/清零，据此改写为用户可行动文案。
	if res.Error != "" && strings.Contains(res.Error, "未能译出") && s.Store != nil {
		if g, p, terr := s.Store.TenantRemainTotal(tid); terr == nil && g+p <= 0 {
			res.Error = "组织积分余额已耗尽，本次翻译已中止（部分段落已保留在工单中供人工补译），请充值后重新发起"
		}
	}
	writeJSON(w, 200, res)
}

// ============ 文件下载 ============

// handleDownload 文件下载接口（/api/download/，按扩展名推断 Content-Type）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（查询参数 path 或路径中 /api/download/ 之后的文件名）。
// 返回: 文件内容（attachment 下载），支持图片/Office/PDF 等类型。
//
// ★ 安全止血（2026-08-26 P0-1，最高危）：本接口此前无任何鉴权、无目录白名单，
//
//	`?path=/etc/passwd` 即可拖走任意文件乃至整库。现加固为：
//	① 必须携带有效 JWT（authUser 校验，401 拒绝）；
//	② 路径白名单：仅允许「上传目录 UploadDir」与「工单产物目录 _output」两处，
//	   filepath.Clean 规范化后做前缀匹配，越界一律 404（不泄露存在性）。
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	// ① 鉴权：匿名请求直接拒绝
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]string{"error": "未登录或登录已过期"})
		return
	}
	// 解析文件路径：优先取查询参数 path，否则从 URL 路径提取
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		filePath = strings.TrimPrefix(r.URL.Path, "/api/download/")
	}
	if filePath == "" {
		writeJSON(w, 404, map[string]string{"error": "文件不存在"})
		return
	}
	// ② 目录白名单校验：Clean 后必须落在允许的基础目录内
	safePath, ok := resolveSafePath([]string{
		s.Cfg.UploadDir, // 上传临时目录（上传件预览）
		filepath.Join(s.Cfg.UploadDir, "_output"), // 工单产物输出目录
	}, filePath)
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "文件不存在"})
		return
	}
	filePath = safePath
	// ③ 归属校验（评审整改 C1 Phase1）：登记行命中时按「同租户 + 本人（或租管以上）」判定；
	//    未登记的历史产物灰度放行并留告警日志——一个产物保留周期（默认14天）后收紧为 404。
	if s.Store != nil {
		if art, aerr := s.Store.GetArtifactByPath(filePath); aerr == nil && art != nil {
			// 超管不受归属限制：平台视角需要能取任意租户产物做排障与质检。
			// 判定口径 = 同租户 AND（本人产物 OR 租管以上）；跨租户一律 404（不返回 403，免泄露存在性）。
			if !auth.IsSuperAdmin(u) {
				allowed := art.TenantID == u.TenantID &&
					(art.UserID == u.ID || auth.IsTenantAdmin(u))
				if !allowed {
					writeJSON(w, 404, map[string]string{"error": "文件不存在"})
					return
				}
			}
		} else {
			log.Printf("[download] 未登记产物放行（Phase1 过渡） path=%s uid=%d", filePath, u.ID)
		}
	}
	// 打开文件并校验存在性
	f, err := os.Open(filePath)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "文件不存在"})
		return
	}
	defer f.Close()
	// 校验为普通文件（非目录）
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeJSON(w, 404, map[string]string{"error": "文件不存在"})
		return
	}
	// 按扩展名推断 Content-Type（Office/图片/PDF 等）
	// 用显式映射表而不是 mime.TypeByExtension：后者依赖宿主机的 mime.types，精简镜像里
	// 常缺 Office 类型，会把 docx 报成 octet-stream 让浏览器放弃预览。
	ext := strings.ToLower(filepath.Ext(filePath))
	contentTypes := map[string]string{
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".gif": "image/gif", ".webp": "image/webp",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".pdf":  "application/pdf",
	}
	ct, ok := contentTypes[ext]
	if !ok {
		// 未知类型回退二进制流
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	// 附件下载（保留原始文件名）
	// 只取 filepath.Base 一个成分写进响应头：路径分隔符/换行不可能进 Content-Disposition，
	// 既防头注入，也不把内部目录结构（tickets/、translated/<落盘名>/）泄露给客户端。
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(filePath)+"\"")
	// 用 ServeContent 而非 io.Copy：自带 Range（大文件断点续传）与 ModTime/304 处理
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// uniqueName 生成带时间戳的唯一文件名（避免上传同名冲突）。
// 参数 name: 原始文件名。返回: "<base>_<纳秒时间戳><ext>" 格式的唯一文件名。
//
// ★ 安全加固（2026-08-26 P1-d）：入口先 filepath.Base 剥离任何目录成分——
//
//	multipart filename 可被恶意构造为 "../../evil.csv"，旧实现会把 ../ 带进
//	filepath.Join 造成上传目录外的路径穿越写。Base 清洗后仅保留纯文件名。
func uniqueName(name string) string {
	// 反斜杠先统一成正斜杠再取 Base：Windows 客户端上传的 filename 形如 C:\a\b.docx，
	// 只 ReplaceAll 不 Base（或反之）都会留下能把 ../ 带进 filepath.Join 的成分。
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/")) // 统一斜杠后取纯文件名（兼容 Windows 风格路径）
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	// 纳秒时间戳同时是跨请求/跨租户的唯一性守卫：产物分目录（engine 的 artifactOutputDir）
	// 用落盘名做子目录名正是依赖它，交付名（artifactDisplayBase）才敢把这串数字剥掉。
	return fmt.Sprintf("%s_%d%s", base, timeNow(), ext)
}

// resolveSafePath 路径白名单解析：将请求路径规范化后校验是否落在任一基础目录内。
// 参数 baseDirs: 允许访问的基础目录列表；p: 请求传入的相对/绝对路径。
// 返回: 规范化后的安全绝对路径与是否放行（false 时调用方应返回 404）。
// 实现要点：
//   - filepath.Clean 消解 "../" 等穿越成分；
//   - 相对路径按各基础目录逐一尝试拼接（保持旧行为兼容：?path=xxx 相对 UploadDir）；
//   - 最终结果必须带分隔符前缀命中某一基础目录，杜绝 "base_evil" 这类前缀误匹配。
func resolveSafePath(baseDirs []string, p string) (string, bool) {
	cleaned := filepath.Clean(p)
	candidates := []string{cleaned}
	if !filepath.IsAbs(cleaned) {
		// 相对路径：分别以每个基础目录为根尝试
		for _, bd := range baseDirs {
			candidates = append(candidates, filepath.Join(bd, cleaned))
		}
	}
	for _, cand := range candidates {
		for _, bd := range baseDirs {
			absBase, err1 := filepath.Abs(bd)
			absCand, err2 := filepath.Abs(cand)
			// 绝对化失败（如 CWD 已不存在）按「不放行」处理：跳过该组合，宁可 404 也不放行未知路径
			if err1 != nil || err2 != nil {
				continue
			}
			// 带分隔符前缀匹配：既允许恰好等于目录内文件，也排除同前缀名目录的混淆
			// （absCand == absBase 这一支允许「目录本身」通过，由调用方的 IsDir 校验兜住）
			if absCand == absBase || strings.HasPrefix(absCand, absBase+string(filepath.Separator)) {
				return absCand, true
			}
		}
	}
	return "", false
}

// timeNow 返回当前纳秒时间戳（用于唯一文件名）。
// 返回: UnixNano 纳秒时间戳。
func timeNow() int64 {
	return time.Now().UnixNano()
}

// userOrgCtx 组装带用户组织/职业角色/用户的请求上下文（KB 部门包祖先链继承 + 实时计费归属依据，
// 2026-08-26；性能优化 B1 修正 user_id 归属；2026-09-19 角色功能补 job_role 注入）。
// 已登录用户取其 org_id、job_role 与 id；匿名/超管平台上下文返回原 ctx（org=0 → 仅企业/共享层；user=0）。
func (s *Server) userOrgCtx(r *http.Request) context.Context {
	ctx := r.Context()
	if u := s.authUser(r); u != nil {
		if u.OrgID > 0 {
			ctx = engine.WithUserOrg(ctx, u.OrgID)
		}
		if u.JobRole != "" {
			ctx = engine.WithUserJobRole(ctx, u.JobRole)
		}
		ctx = tenant.WithUser(ctx, u.ID)
	}
	return ctx
}
