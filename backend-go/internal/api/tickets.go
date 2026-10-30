// ============ tickets.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 本文件实现工单流程与审批相关接口：
//   - 工单管理（tenant_admin+）：列表/创建/运行/详情（handleTickets / handleTicketCreate / handleTicketRun / handleTicketDetail）
//   - 审批（approver + admin）：待审批列表 / 审批操作（handleApproveList / handleApproveAction）
// 业务要点：
//   - handleTicketRun 执行前先过配额闸门（gateUsage），成功后按源文本字符数计量，失败计入指标
//   - 驳回重翻循环：重翻成功后清空驳回意见，避免下次运行重复重翻
//   - 批准审批后触发自迭代（feedback 步骤）；驳回时记录原因与建议
//   - 工单操作均写入审计；工单查询全部限定生效租户（租户隔离）
// ★ F-72（2026-09-27 〇-W，用户批准）：建单余额预检的预估改「固定项＋线性项」两段式
//   est = F(mode) + chars × langs × K(mode)；两档各自走 system_config
//   （est_tokens_fixed_pro/fast 缺省 3000/1200、est_tokens_per_char_pro/fast 缺省 160/60），
//   六键逐次现读、写入即生效（无需重启/发版）。★ 〇-X 第 5 项（2026-09-27）已补管理台表单：
//   GET/POST /api/admin/config/est-tokens（见 est_tokens_config.go，仅平台超管，六项必须一次交齐），
//   不再有「只能 psql 插行」的口径——两库当前仍**没有**这四行，未配置时走代码缺省 160/60 + 3000/1200。
//   判据与取数依据见 estimateTicketTokens 上方注释。
// ★ F-64②（批 I-10 2026-09-26）口径：本文件「HTTP 200 承载业务失败」的 21 处已全部改走
//   统一出口 s.writeError + apierrors，状态码按语义诚实——
//   404（本租户下取不到单：跨租户不泄露存在性）、409（状态或产物不满足该操作）、
//   500（存储读写故障）、503（工单服务未装配 / 并发锁冲突，稍后重试有意义）。
//   其余内联 writeJSON(w, 401/403/400/404/500, …) 的状态码本来就是诚实的，属
//   「错误写法棘轮」（TestErrorStyleRatchet）的另一档，本批按分期口径未动。

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"github.com/xuri/excelize/v2"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/config"
	"translator/internal/engine"
	apierrors "translator/internal/errors"
	"translator/internal/fileproc"
	"translator/internal/orchestrator"
	"translator/internal/qa"
	"translator/internal/store"
)

// ============ 工单 ============

// est_tokens_per_char_pro / est_tokens_per_char_fast = 线性项「每字符×每语种」系数 K 的配置键。
const (
	cfgEstKPro  = "est_tokens_per_char_pro"
	cfgEstKFast = "est_tokens_per_char_fast"
	// cfgEstFixedPro / cfgEstFixedFast = 固定项「每次建单一次性开销」的配置键（★ F-72）。
	// 与 K 分键的目的：调短单报价只动固定项，不必再把长单系数一起拽走（见 estimateTicketTokens 注释）。
	cfgEstFixedPro  = "est_tokens_fixed_pro"
	cfgEstFixedFast = "est_tokens_fixed_fast"
	// cfgEstFixedLangPro/Fast = 「每多一个目标语种」的固定开销 F1 的配置键（★ 2026-10-28 计价批）。
	// 与 F0 分键的原因：文件链是**每语种一轮调用**，固定指令开销随语种数线性增长；
	// 只用一个「每单固定」会把多语种短单结构性低估（旧形态 5 语种 × 100 字只算一次固定）。
	cfgEstFixedLangPro  = "est_tokens_fixed_lang_pro"
	cfgEstFixedLangFast = "est_tokens_fixed_lang_fast"
)

// 预估缺省参数（配置缺失/非法时的保守回落值，单一来源，禁止在调用点再写字面量）。
//
//	★ 2026-10-28 计价批（用户拍板 D1）：本批**只改结构、不改 K**。
//	  F 从「每单一次性 3000/1200」拆成「每单 F0 + 每语种 F1」：
//	  pro=600+2400×语种数、fast=300+900×语种数 —— 单语种合计与旧值逐字相同（3000/1200），
//	  多语种则按语种数线性补固定开销（旧形态 5 语种 × 100 字只算一次固定，结构性低估）。
//	  K 维持 pro=160 / fast=60：降价不靠调常数硬压，而靠本批同时上线的
//	  「译文缓存 A+B + 动态批 + 3 轮硬闸」把真实消耗降下来，7 天后按生产实测的有效 K
//	  再改配置降价（不改代码）。理由见方案《改造方案_计价与成本优化及遗留补齐_20261028.md》§2/§9-D1：
//	  直接写死 K=20 会让 88 号那类大单的预估从 107 万掉到 23 万（真实消耗没变），
//	  预估低于实烧＝建单闸门放行透支单，正是 F-41 的事故形态。
const (
	defEstKPro          = 160.0
	defEstKFast         = 60.0
	defEstFixedPro      = 600.0  // 每单固定 F0（pro）
	defEstFixedFast     = 300.0  // 每单固定 F0（fast）
	defEstFixedLangPro  = 2400.0 // 每语种固定 F1（pro）
	defEstFixedLangFast = 900.0  // 每语种固定 F1（fast）
)

// estParams 一次估算用到的两段式参数，全部由 system_config 现读后注入（纯函数便于字节级锁）。
// 取数入口见 estTokensPerChar（线性项）与 estTokensFixed（固定项）。
type estParams struct {
	kPro          float64 // pro 线性系数 K：每「源字符 × 目标语种」的 token 数
	kFast         float64 // fast 线性系数 K
	fixedPro      float64 // pro 每单固定项 F0（建单/解析/语言识别，与字数、语种数都无关）
	fixedFast     float64 // fast 每单固定项 F0
	fixedLangPro  float64 // pro 每语种固定项 F1（该系统提示词、术语/上下文、输出结构）
	fixedLangFast float64 // fast 每语种固定项 F1
}

// estimateTicketTokens 估算文件/文本工单翻译的 token 消耗（纯函数，便于单测与字节级锁）。
// ★ F-41（2026-09-25 批 D）公式重写：旧口径 `chars/1.3 × langs × markup` 与 pro 实测计费
// 差约 62 倍（工单 88：估 17.3k、实烧 1,075,400 token 后在 100% 进度处烧穿 rejected、零交付），
// 根因是旧公式只折算「源字符→单次译文 token」，跟不上 pro 的「初译+评审双趟 × 段分块 ×
// 逐次上下文开销」——注释里的「宁可多估」名不副实。新口径按**实测单位成本外推**：K(pro)=160
// （96 号单位成本→2 万字外推 155 的 P99 上包络）、K(fast)=60（单趟无评审约 2.5 折）。
//
// ★ F-72（2026-09-27 〇-W 用户批准）：在 K 之上再补**固定项**，最终形态
//
//	est = F(mode) + chars × langs × K(mode)        （固定项＋线性项 两段式）
//
// 为什么必须分段、而不是把 K 调大：纯线性无截距对短单**结构性低估**——19 字 ×1 语的 pro 单
// （工单 47）预估 3,040、实烧 18,304，差 6 倍；6～67 字短单实烧 p50=1,735、p90=3,289，
// 几乎全是与字数无关的一次性开销。拿短单去拟合 K 会把它抬到 382～963，于是 1 万字 ×5 语的
// 大单预估从 800 万变成 4,815 万 token——为修短单把长单吹上天，报价直接不可用。
// 分成两项后各管各的：短单由 F 兜住下限，长单仍由 K 决定量级（F 在长单里占比 <0.1%）。
//
// 保留的既有语义（勿改）：字符数/语种数任一 ≤0 一律返回 0（无归属租户或无可估消耗时
// 交 raw gateUsage 处理，预检不参与）；K 异常（0/负数）回退保守默认 160，绝不放大放行；
// F 允许显式配 0（＝运维主动退回纯线性），只有负数/非数字才回退默认。
//
// 参数：sourceChars=折算后的可译源字符数（文件单按 estimateFileSourceChars 的 /3·/6·/12 分档）；
// langCount=目标语种数；mode=fast 走 fast 档，其余（含空串）一律按 pro 保守档；p=现读的两段式参数。
func estimateTicketTokens(sourceChars int64, langCount int, mode string, p estParams) int64 {
	if sourceChars <= 0 || langCount <= 0 {
		return 0
	}
	k, f0, f1 := p.kPro, p.fixedPro, p.fixedLangPro
	if mode == "fast" {
		k, f0, f1 = p.kFast, p.fixedFast, p.fixedLangFast
	}
	// 脏配置的回退方向必须是「更保守」：一律回退到 pro 档，哪怕本单是 fast——
	// 回退到更低的 fast 档等于把闸门往放行方向挪，正是 F-41 的事故形态。
	if k <= 0 {
		k = defEstKPro
	}
	if f0 < 0 {
		f0 = defEstFixedPro // 负数属脏配置，回退 pro 档；0 是合法的「关闭固定项」
	}
	if f1 < 0 {
		f1 = defEstFixedLangPro
	}
	// 两段式（v3）：F0 + 语种数×(F1 + 源字符数×K)。单语种合计 = F0+F1（pro 仍是 3000），
	// 多语种按语种数线性增加固定开销；整式一次取整，避免逐项截断后与页面浮点式子差 1 分。
	return int64(f0 + float64(langCount)*(f1+float64(sourceChars)*k))
}

// estTokensPerChar 读取指定模式的每字符预估 token 系数 K（system_config 可调，缺省保守默认）。
// 键缺失/非法（非数字或 ≤0）一律回退默认（pro=160 / fast=60），查询失败同样回退——
// 预检是粗闸，配置坏了不能反过来把估算清零（清零＝全放行，正是 F-41 的事故形态）。
// 参数 mode：仅 "fast" 走 fast 档，其余（含空串/未知值）按 pro。
func (s *Server) estTokensPerChar(mode string) float64 {
	key, def := cfgEstKPro, defEstKPro
	if mode == "fast" {
		key, def = cfgEstKFast, defEstKFast
	}
	return s.estConfigFloat(key, def, false)
}

// estTokensFixed 读取指定模式的固定开销项 F（★ F-72，2026-09-27 〇-W）。
// 键：est_tokens_fixed_pro（缺省 3,000）／ est_tokens_fixed_fast（缺省 1,200）。
// 与 K 的判据差别：**0 是合法值**——那是「明知短单会低估、仍要退回纯线性」的显式选择，
// 不拦；只有缺失/非数字/负数才回退默认（负数会把预估往回扣，比清零更危险）。
// 参数 mode：仅 "fast" 走 fast 档，其余按 pro。
func (s *Server) estTokensFixed(mode string) float64 {
	key, def := cfgEstFixedPro, defEstFixedPro
	if mode == "fast" {
		key, def = cfgEstFixedFast, defEstFixedFast
	}
	return s.estConfigFloat(key, def, true)
}

// estTokensFixedLang 读取「每多一个目标语种」的固定开销 F1（★ 2026-10-28 计价批）。
// 键：est_tokens_fixed_lang_pro（缺省 2,400）／ est_tokens_fixed_lang_fast（缺省 900）。
// 0 合法：那是「每语种固定开销并进 K」的显式选择；只有缺失/非数字/负数才回退默认。
func (s *Server) estTokensFixedLang(mode string) float64 {
	key, def := cfgEstFixedLangPro, defEstFixedLangPro
	if mode == "fast" {
		key, def = cfgEstFixedLangFast, defEstFixedLangFast
	}
	return s.estConfigFloat(key, def, true)
}

// estConfigFloat 读取一个估算配置项并解析为浮点：查询失败/空值/非数字一律回退 def；
// allowZero=false 时 ≤0 也回退（K 档：清零＝全放行），true 时仅负数回退（F 档：0＝主动关闭固定项）。
// 参数 key=system_config 键名；def=保守缺省值；allowZero=是否允许显式 0。
func (s *Server) estConfigFloat(key string, def float64, allowZero bool) float64 {
	if s.Store != nil {
		if v, err := s.Store.GetConfig(key); err == nil && strings.TrimSpace(v) != "" {
			if f, cerr := strconv.ParseFloat(strings.TrimSpace(v), 64); cerr == nil && (f > 0 || (allowZero && f == 0)) {
				return f
			}
		}
	}
	return def
}

// estimateFileSourceChars 由文件字节数估算可译源字符数（按扩展名分档）。
// 目的仅为建单前余额粗预检（非计费依据），故取偏保守（低）系数避免误拦：
//   - 纯文本（.txt/.md/.csv/.srt 等）：字节≈字符，中文 UTF-8 约 3 字节/字 → /3；
//   - 压缩文本容器（.docx/.xlsx/.pptx/.epub/.odt 等，XML 压缩）→ /6；
//   - 图文混排二进制（.pdf/.doc/.ppt/.xls 等，图像/字体占大头）→ /12；
//   - 图片（P1a，.png/.jpg 等）：经 OCR 后的可译文本密度远低于字节数（一张截图几十 KB
//     可能只含数百字），取大分母 /30 避免把正常图片件按 /6 误估成数万字符而误拦；
//   - 音频（P2，.mp3/.m4a/.wav 等）：压缩率比图片还极端——64kbps 的 mp3 一分钟约 480KB，
//     而中文语音一分钟约 240 字（≈720 字节 UTF-8），即真实文本只有字节量的 1/660；
//     分母统一取**远大于实测比**：有损容器（mp3/m4a/aac/opus/ogg）/2000、
//     无损容器（wav/flac，未压缩 PCM 字节量再大 4 倍）/8000。
//     ★ 这里刻意**往少估**：预检的作用只是拦「余额明显不够」，估低最多跑完才发现不够，
//     估高则会在门口误伤正常件（图片 /30 那条是同一取舍，已实测纠错过一次）。
//   - 未知：按 /6 折中。
func estimateFileSourceChars(name string, size int64) int64 {
	if size <= 0 {
		return 0
	}
	denom := int64(6)
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "txt", "md", "markdown", "csv", "tsv", "srt", "vtt", "json", "xml", "html", "htm", "log", "yml", "yaml":
		denom = 3
	case "pdf", "doc", "ppt", "xls", "pptm", "xlsm", "key", "pages", "numbers", "epub", "azw3", "mobi":
		denom = 12
	case "png", "jpg", "jpeg", "bmp", "tif", "tiff", "webp", "heic", "heif", "gif":
		denom = 30
	case "mp3", "m4a", "aac", "opus", "ogg", "oga":
		denom = 2000
	case "wav", "flac", "aiff", "aif", "alac":
		denom = 8000
	default:
		denom = 6
	}
	return size / denom
}

// precheckTicketBalance 文件/文本工单建单前置余额预检（改进2，2026-09-10）：
// 强制计费开启时，估算本次翻译的 token 消耗，若超出剩余余额则直接拒绝建单，
// 避免「工单创建→队列→跑到中途余额耗尽→整单失败」（此前只查余额>0，不查是否够本次用量）。
// release 为 gateUsage 返回的并发名额释放闭包：预检失败时同样需归还。
// ★ F-41（2026-09-25 批 D）：估算改按模式系数 K（pro=160/fast=60，system_config 可配），
// mode 由调用点传入；拒绝文案继续走「预估积分」范式（S1 零 token 裸值口径不变）。
// ★ F-72（2026-09-27 〇-W）：估算补固定项，成 est = F(mode) + chars × langs × K(mode)。
// 只动**被拦的单**：固定项 3,000 token ≈ 8 积分（★ F-78 起按 points_tokens_rate=400 折算；
// 旧汇率 1:300 时代约 10 积分——同一笔真实消耗，改档后对外报价降 25%，这正是本次降价的落点），
// 余额只要够得上
// 一单，就不会因为这条常数被拦；真正被它改变结果的是「6～67 字短单 × 极小余额」的碰运气建单。
// 文案里「预估需约 N 积分」的**措辞与形态**继续不动（UAT T40 等值锁按公式现算，见下）。
func (s *Server) precheckTicketBalance(tid int64, srcChars int64, langCount int, mode string, release func()) error {
	if release != nil {
		defer release()
	}
	if tid <= 0 || srcChars <= 0 || langCount <= 0 {
		return nil // 无归属租户/无可估消耗：交 raw gateUsage 处理
	}
	if s.Bill == nil || !s.Bill.Enabled() {
		return nil // 未强制计费：不预检
	}
	// 两段式参数逐次现读（不缓存）：system_config 一改，下一单即生效（与批 H 回调 K 的口径一致）。
	estimated := estimateTicketTokens(srcChars, langCount, mode, estParams{
		kPro:          s.estTokensPerChar("pro"),
		kFast:         s.estTokensPerChar("fast"),
		fixedPro:      s.estTokensFixed("pro"),
		fixedFast:     s.estTokensFixed("fast"),
		fixedLangPro:  s.estTokensFixedLang("pro"),
		fixedLangFast: s.estTokensFixedLang("fast"),
	})
	grants, permanent, err := s.Store.TenantRemainTotal(tid)
	if err != nil {
		return nil // 余额查询失败不阻断建单，交由实时计费兜底
	}
	total := grants + permanent
	if total <= 0 {
		// 余额已耗尽：gateUsage.CheckBalance 已拦截，此处兜底给同样文案
		return &apiErr{"组织积分已耗尽，请联系管理员及时充值"}
	}
	if estimated > total {
		// ★ S1 积分口径（2026-09-15）：对外零 token 裸值，预估算量与余额一律折算积分展示
		return &apiErr{fmt.Sprintf("余额不足：本次翻译预估需约 %d 积分，当前余额约 %d 积分，请先充值后再发起",
			s.Store.PointsFromTokens(estimated), s.Store.PointsFromTokens(total))}
	}
	return nil
}

// writeGateError ★ F-21③（2026-09-25 UAT 修复批）：把 gateUsage / precheckTicketBalance
// 的拒绝统一出成 4xx + 结构化错误体（writeError），替换旧「HTTP 200 + success:false」口径——
// 客户端（含 SDK 与 run_uat 断言）终于可按状态码分支，而不是解析中文文案。
// 映射：billing quotaErr 稳定码 insufficient_balance → 402（ErrInsufficientBalance）；
// 其余闸门拒绝（日限额/QPS/并发/预算墙/余额预检）→ 400（ErrQuotaExceeded）。
// 文案原样保留（组织墙/充值引导话术前端已在展示），仅状态码与错误码收口。
func (s *Server) writeGateError(w http.ResponseWriter, r *http.Request, gerr error) {
	code := apierrors.ErrQuotaExceeded
	if billing.QuotaErrCode(gerr) == "insufficient_balance" {
		code = apierrors.ErrInsufficientBalance
	}
	s.writeError(w, r, apierrors.New(code, gerr.Error()))
}

// handleTickets 工单列表接口（tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（查询参数 mine=1 仅显示本人创建的工单）。
// 返回: success=true 时携带 tickets 数组。
func (s *Server) handleTickets(w http.ResponseWriter, r *http.Request) {
	// ★ 2026-10-21 UAT 收口 M5：方法闸（与 23N7 同一口径）。
	//   旧形态不判方法：POST／PUT／DELETE 打这个路径会一路走完鉴权与查询、回 200 的列表体，
	//   按状态码分支的客户端与监控会把它读成"动作成功"，而工单其实一件没动
	//   （UAT 实测：非 GET 与 GET 的响应体逐字节相同）。方法不允许是可自证的请求侧错误，
	//   必须在读任何状态之前就以 405 收口，并带上统一错误码与 trace_id。
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "该接口仅支持 GET"))
		return
	}
	// 鉴权：登录用户即可查看工单（隐私：非超管强制仅自己的工单）
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	// 隐私隔离：除超管外，一律只返回当前用户创建的工单（涉及用户隐私）
	onlyMine := !auth.IsSuperAdmin(u)
	// 租户隔离：仅查询生效租户下的工单
	tickets, err := s.Store.ListTickets(s.effTenant(r, u), u.ID, onlyMine)
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：工单列表读的是本进程存储层，查询失败属服务端故障。
		//   旧写法在 HTTP 层永远是 200：按状态码分支的客户端（SDK、监控、重试器）把它读成成功，
		//   而前端 TicketsPage 的 load() 只在 r.success 时 setTickets，失败被 catch 吞掉——
		//   首屏就是一张没有提示的空列表。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// ★ 运行中工单附实时进度百分比（后端唯一真源；仅运行中才查状态，避免全量 N+1）
	// 出参折积分口径（tokens_billed → points_billed，2026-09-19）
	out := make([]map[string]interface{}, 0, len(tickets))
	for _, t := range tickets {
		tv := s.ticketJSON(t)
		if t.Status != "queued" && t.Status != "in_progress" {
			p := 0
			if t.Status == "completed" || t.Status == "rejected" {
				p = 100
			}
			tv["progress"] = p
			out = append(out, tv)
			continue
		}
		states, _ := s.Store.TicketStates(t.ID)
		tfiles, _ := s.Store.TicketFiles(t.ID)
		pct := 10
		if t.Status == "in_progress" {
			pct = ticketProgressPct(t, states, tfiles)
		}
		tv["progress"] = pct
		out = append(out, tv)
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tickets": out})
}

// handleTicketCreate 创建工单接口（tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 title/source_text/target_langs）。
// 返回: success=true 时携带新工单对象。
func (s *Server) handleTicketCreate(w http.ResponseWriter, r *http.Request) {
	// 鉴权：登录用户即可创建工单（隐私隔离：仅创建者可见）
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		Title       string `json:"title"`        // 工单标题
		SourceText  string `json:"source_text"`  // 待翻译源文本（必填）
		TargetLangs string `json:"target_langs"` // 目标语言列表（逗号分隔，默认 en）
		Mode        string `json:"mode"`         // 翻译模式：fast | pro（默认 pro）
		MaxLength   int64  `json:"max_length"`   // ★ 缩翻最长字符限制（0=未启用）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SourceText == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请提供源文本"))
		return
	}
	// 默认目标语言 en
	if req.TargetLangs == "" {
		req.TargetLangs = "en"
	}
	// ★ 整改 C6：建单即自动执行，必须先过配额闸门（此前闸门只在手动 run 存在，
	//   自动入队路径使 QPS/日额/余额闸对建单入口形同虚设）
	var release func()
	var tid int64
	var gerr error
	if tid, release, gerr = s.gateUsage(r); gerr != nil {
		s.writeGateError(w, r, gerr) // ★ F-21③：400/402 + 稳定错误码（旧 200+success:false 语义保留在文案里）
		return
	}
	// ★ 改进2（2026-09-10）：余额预检——估算本次文本翻译消耗，超出剩余余额直接拒绝，
	//   避免工单入队后中途因余额耗尽失败（precheck 内部会归还并发名额）。
	langCount := len(strings.Split(req.TargetLangs, ","))
	if perr := s.precheckTicketBalance(tid, int64(len([]rune(req.SourceText))), langCount, normalizeTaskMode(req.Mode), release); perr != nil {
		s.writeGateError(w, r, perr) // ★ F-21③
		return
	}
	// 创建工单（归属生效租户）
	t, err := s.Store.CreateTicket(s.effTenant(r, u), u.ID, req.Title, req.SourceText, "", req.TargetLangs)
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：配额闸门与余额预检都已通过，此处失败是工单行
		//   写入故障（本进程存储层），不是用户改载荷就能改正的问题，故不是 400。
		//   200 壳下按状态码分支的客户端（SDK/监控/重试器）会读成「建单成功」并去取不存在的 ticket 对象。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// ★ 翻译模式随创建请求落库（fast=快速 / pro=专业校对；空值归一化为 pro）
	t.Mode = normalizeTaskMode(req.Mode)
	// ★ 缩翻（任务7）：最长字符限制随建单落库（0=未启用缩翻）
	t.MaxLength = req.MaxLength
	if t.MaxLength > 0 {
		_ = s.Store.UpdateTicket(t)
	}
	// 创建工单审计 + 自动入队执行（★ 整改 C6：入队用 Background——请求 ctx 在响应返回后
	// 即取消，异步任务不得绑定其生命周期）
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_create", "tickets", t.TicketNo)
	if s.TicketSvc != nil {
		t.Status = store.TicketQueued
		_ = s.Store.UpdateTicket(t)
		_, _ = s.TicketSvc.EnqueueTicketRun(context.Background(), t.ID)
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": s.ticketJSON(t)})
}

// handleTicketCreateFile 文件工单创建（multipart：file/title/target_langs）。
// 文件保存至上传目录 tickets/ 子目录；创建后自动入队，worker 走原格式回写流水线。
func (s *Server) handleTicketCreateFile(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	// 上传上限与 multipart 解析（与即时翻译对齐：translateUploadMax=40MB，多文件共享总上限）
	r.Body = http.MaxBytesReader(w, r.Body, translateUploadMax)
	if err := r.ParseMultipartForm(translateUploadMax); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, fmt.Sprintf("文件解析失败或超过大小上限（%dMB）", translateUploadMax/1024/1024)))
		return
	}
	// 多文件：优先取 "files" 字段（可重复）；兼容旧单文件字段 "file"
	var headers []*multipart.FileHeader
	if r.MultipartForm != nil {
		headers = append(headers, r.MultipartForm.File["files"]...)
		if len(headers) == 0 {
			headers = append(headers, r.MultipartForm.File["file"]...)
		}
	}
	if len(headers) == 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少文件"))
		return
	}
	// ★ 工单双模式（2026-09-13）：交付方式 delivery=restore 还原文件模式（默认）/ text 纯文案模式
	//   （anydoc 提取→交付译文 .md，不还原版式；额外准入 doc/xls/ppt/odt/ods/odp/rtf/epub 等）。
	delivery := normalizeTaskDelivery(r.FormValue("delivery"))
	// 扩展名白名单：★ 2026-10-14 #43 起判据在 `fileproc.IsTranslateFormat`（唯一事实源），
	// 本处不再自带一份字面量。旧写法是"本地 map ＋ 纯文案模式往里补 anydoc 键"，
	// 收成共享常量后那段补键代码会直接写进全局闸门，所以这里改判**函数**而不是拿共享 map。
	textMode := delivery == "text"
	for _, hdr := range headers {
		ext := strings.ToLower(filepath.Ext(hdr.Filename))
		if !fileproc.IsTranslateFormat(ext, textMode) {
			// 拒绝文案只有一个出口（★ 2026-10-30 三事合一批 C-10）：撤除的那五个纯文本容器
			// 必须给「请直接粘贴内容到文本翻译」这条出路——只回一句「不支持」等于把一个更省事的路藏起来。
			// 清单句由 FormatHintBase 现算，改档位这句话自己跟着变，不留第二份对外文案。
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation,
				fileproc.RejectHint(ext, textMode, "不支持的格式: "+hdr.Filename)))
			return
		}
		// 纯文案模式下 anydoc 独占格式需要依赖就绪（缺失时老 12 种仍可用，不受影响）
		if textMode && fileproc.AnydocFormats[ext] && !fileproc.AnydocAvailable() {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, hdr.Filename+" 需服务器安装 firecrawl-anydoc 依赖后方可纯文案翻译（请管理员执行 pip install firecrawl-anydoc，或改用还原模式支持格式）"))
			return
		}
	}
	title := r.FormValue("title")
	targetLangs := r.FormValue("target_langs")
	if targetLangs == "" {
		targetLangs = "en"
	}
	// 逐个保存到 上传目录/tickets/
	dir := filepath.Join(s.Cfg.UploadDir, "tickets")
	_ = os.MkdirAll(dir, 0o755)
	saved := make([]struct{ path, name string }, 0, len(headers))
	// 逐附件落盘为 纳秒时间戳_原名（防重名覆盖），任一步失败即整体 500 中止
	for _, hdr := range headers {
		saveName := fmt.Sprintf("%d_%s", time.Now().UnixNano(), filepath.Base(hdr.Filename))
		savePath := filepath.Join(dir, saveName)
		src, ferr := hdr.Open()
		if ferr != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "读取文件失败: "+hdr.Filename))
			return
		}
		out, cerr := os.Create(savePath)
		if cerr != nil {
			src.Close()
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "保存文件失败"))
			return
		}
		if _, cerr = io.Copy(out, src); cerr != nil {
			out.Close()
			src.Close()
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "写入文件失败"))
			return
		}
		out.Close()
		src.Close()
		saved = append(saved, struct{ path, name string }{savePath, hdr.Filename})
	}
	// ★ 性能优化 Phase A1：PDF 前置拦截（大小/页数），超限直接友好拒绝，避免后台转换卡死。
	//   纯文案模式跳过——不经 pdf2docx/LibreOffice 转换链，毫秒级提取无卡死风险。
	if delivery != "text" {
		for _, f := range saved {
			if perr := checkPdfLimits(r.Context(), f.path, f.name); perr != nil {
				for _, cleanup := range saved {
					os.Remove(cleanup.path)
				}
				s.writeError(w, r, apierrors.New(apierrors.ErrValidation, perr.Error()))
				return
			}
		}
	}
	if title == "" {
		title = saved[0].name + fmt.Sprintf(" 等 %d 个文件", len(saved))
		if len(saved) == 1 {
			title = saved[0].name
		}
	}
	// ★ 整改 C6：建文件工单同样先过配额闸门（自动入队路径此前完全绕闸）
	var tid int64
	var release func()
	var gerr error
	if tid, release, gerr = s.gateUsage(r); gerr != nil {
		s.writeGateError(w, r, gerr) // ★ F-21③：文件工单与文本工单同一出参口径
		return
	}
	// ★ 改进2（2026-09-10）：文件工单建单前余额预检——用上传文件大小估算源字符数。
	//   ★ 2026-09-15 修正（用户反馈 700KB PDF 预估 40 万 token 误拦）：旧口径一律 字节/3，
	//   把 PDF/Office 二进制容器整包当作可译文本（图像/字体/压缩流占了大头），高估 5~10 倍，
	//   造成「余额明明够却拒绝建单」。改按文件类型取字节→字符系数；预检只是防中途耗尽的
	//   粗闸（真实扣费按供应商回传 token 实时计），宁可低估放行，不可高估误拦。
	{
		var srcChars int64
		for _, f := range saved {
			if fi, serr := os.Stat(f.path); serr == nil {
				srcChars += estimateFileSourceChars(f.name, fi.Size())
			}
		}
		// ★ F-41：预检需要模式系数（pro K=160 / fast K=60），提前归一化本次建单模式
		//（与下方 t.Mode 落库同一 normalizeTaskMode 口径，两处不会分叉）。
		mode := normalizeTaskMode(r.FormValue("mode"))
		if perr := s.precheckTicketBalance(tid, srcChars, len(strings.Split(targetLangs, ",")), mode, release); perr != nil {
			// 预检失败：清理已上传文件，避免孤儿文件滞留磁盘
			for _, cleanup := range saved {
				os.Remove(cleanup.path)
			}
			s.writeGateError(w, r, perr) // ★ F-21③：与文本工单同一 400/402 出参口径
			return
		}
	}
	// 创建工单（file_path 记首个文件，兼容旧列表展示；全部文件入 ticket_files 表）
	t, err := s.Store.CreateTicket(s.effTenant(r, u), u.ID, title, "", saved[0].path, targetLangs)
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：文件已落盘、闸门与余额预检都已通过，
		//   这步失败是工单行写入故障（本进程存储层），客户端改载荷也救不回来；
		//   写成 200 时，按状态码分支的调用方读到的是「建单成功」，而响应体里根本没有 ticket 对象。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// ★ 文件任务模式落库（multipart mode 字段；空=pro）
	t.Mode = normalizeTaskMode(r.FormValue("mode"))
	// ★ 工单双模式（2026-09-13）：交付方式随建单落库（restore 还原文件 / text 纯文案）
	t.Delivery = delivery
	// ★ 缩翻（任务7）：文件工单最长字符限制（multipart max_length；空=0 未启用）
	if n, perr := strconv.ParseInt(r.FormValue("max_length"), 10, 64); perr == nil && n > 0 {
		t.MaxLength = n
	}
	tid = s.effTenant(r, u)
	for _, f := range saved {
		_, _ = s.Store.AddTicketFile(&store.TicketFile{
			TenantID: tid, TicketID: t.ID, FileName: f.name, FilePath: f.path,
		})
		s.Store.RegisterArtifact(f.path, tid, u.ID, t.ID) // ★ 归属登记（评审整改 C1）
	}
	s.Store.LogAudit(tid, u.ID, "ticket_create_file", "tickets", fmt.Sprintf("%s (%d files)", t.TicketNo, len(saved)))
	if s.TicketSvc != nil {
		t.Status = store.TicketQueued
		_ = s.Store.UpdateTicket(t)
		// ★ 整改 C6：入队用 Background（请求 ctx 响应后即取消）
		_, _ = s.TicketSvc.EnqueueTicketRun(context.Background(), t.ID)
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": s.ticketJSON(t)})
}

// handleTicketRun 运行工单流程接口（FlowDef 编排，tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。
// 返回: success=true 时携带运行后的工单对象（含流程结果）。
// 流程：取工单 → 配额闸门 → 置为进行中 → 执行编排 → 计量 → 清空驳回意见。
func (s *Server) handleTicketRun(w http.ResponseWriter, r *http.Request) {
	// 鉴权：登录用户；隐私：仅创建者或超管可运行
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		ID int64 `json:"id"` // 工单 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少工单 id"))
		return
	}
	// 租户隔离：仅可取生效租户下的工单
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 404：GetTicket 带生效租户条件，取不到就是
		//   「本租户下没有这张单」。跨租户一律按不存在处理（404 而非 403），是本仓既有的
		//   安全口径——403 会泄露「这个 id 在别家租户下确实存在」。
		s.writeError(w, r, apierrors.New(apierrors.ErrTicketNotFound, "工单不存在"))
		return
	}
	// 隐私校验：非创建者且非超管禁止运行他人工单
	if t.CreatedBy != u.ID && !auth.IsSuperAdmin(u) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "无权操作他人工单"))
		return
	}
	// ★ 整改 C5：状态机门槛——仅草稿/排队/被驳回可（重）跑。此前 completed 工单可被
	//   置回 queued 重复执行重复计费，cancelled/approved/pending_approval 重跑语义非法。
	switch t.Status {
	case store.TicketDraft, store.TicketQueued, store.TicketRejected:
		// 可执行状态
	default:
		// ★ F-64②（批 I-10）：原 200 承载失败 → 409：工单存在、载荷也没错，挡路的是资源**当前状态**
		//   （completed/cancelled/approved 重跑语义非法，见上方整改 C5 的状态机门槛）。
		//   不用 400 是因为改请求体救不了——要等状态回到可跑档；不用 403 是因为发起人有权跑自己的单。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "当前状态不可执行：仅待处理/排队中/被驳回的工单可以运行"))
		return
	}
	// 配额闸门：QPS/并发/每日上限/余额校验（不通过则拒绝运行）
	_, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		s.writeGateError(w, r, gateErr) // ★ F-21③：手动 run 与建单同一 400/402 出参口径
		return
	}
	// ★ 异步入队：立即返回 ticket_no，worker 后台执行五步编排（大文件不阻塞 HTTP）
	if s.TicketSvc == nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 503：工单服务与 store/engine 同批装配
		//   （见 server.go「if st != nil && eng != nil」），为 nil 就是执行依赖未就绪，
		//   语义上「等一会儿或运维补齐后再试有意义」，与 500「我们这边出错了」不同档——
		//   与 ①档 billing_api.go「平台存储未初始化」→ 503 同一口径。
		s.writeError(w, r, apierrors.New(apierrors.ErrServiceUnavailable, "工单服务未初始化"))
		return
	}
	t.Status = store.TicketQueued
	_ = s.Store.UpdateTicket(t)
	// ★ 整改 C6：手动 run 的入队同样用 Background（与建单/审批路径口径统一）
	if _, err := s.TicketSvc.EnqueueTicketRun(context.Background(), t.ID); err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：入队即往 jobs 表写一行（queue.Direct），
		//   失败是存储写入故障、本进程侧的问题；文案里的 err.Error() 原样透出便于排查。
		//   注意工单已被上面置成 queued 落库，这次运行确实没排上，故必须是失败状态码而不是 200。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "入队失败: "+err.Error()))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_enqueue", "tickets", t.TicketNo)
	resp := map[string]interface{}{"success": true, "ticket": s.ticketJSON(t), "queued": true}
	runExt := strings.ToLower(filepath.Ext(t.FilePath))
	if fileproc.IsImageFormat(runExt) || (runExt == ".pdf" && fileproc.PdfImageHeavy(t.FilePath)) {
		resp["image_heavy"] = true
	}
	writeJSON(w, 200, resp)
}

// ticketProgressPct 工单进度百分比（步骤锚点阶梯，唯一真源）：
// 排队10 → 提取20 → 初翻40 → 校对60 → 回写80 → 完成100。
func ticketProgressPct(t *store.Ticket, states []*store.TicketState, files []*store.TicketFile) int {
	if t == nil {
		return 0
	}
	switch t.Status {
	case "completed":
		return 100
	case "rejected":
		return 100
	case "", "draft", "queued", "pending_approval":
		if t.Status == "queued" {
			return 10
		}
		return 5
	}
	pct := 5
	// 步骤锚点表（多别名归一）：提取20 → 初翻40 → 校对60 → 回写80
	W := map[string]int{
		"upload": 20, "file_extract": 20, "extract": 20,
		"translate": 40, "file_translate": 40, "init_translation": 40,
		"proofread": 60, "qa": 60, "file_qa": 60, "quality_check": 60,
		"writeback": 80, "file_writeback": 80, "package": 80,
	}
	for _, x := range states {
		w, ok := W[x.Step]
		if !ok {
			continue
		}
		if x.Status == "success" || x.Status == "skipped" {
			if w > pct {
				pct = w
			}
		} else if x.Status == "running" {
			r := w - 10
			if r < w && r > pct {
				pct = r
			} else if r <= 10 && w > pct {
				pct = w
			}
		}
	}
	// 任一文件已出产物/报错即视为回写完成（进度保底 80%）
	for _, f := range files {
		if f.ResultPath != "" || f.Error != "" {
			if pct < 80 {
				pct = 80
			}
			break
		}
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

// handleTicketDetail 工单详情接口（含状态轨迹，tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（查询参数 id）。
// 返回: success=true 时携带 ticket（工单）与 states（状态轨迹数组）。
func (s *Server) handleTicketDetail(w http.ResponseWriter, r *http.Request) {
	// 鉴权：登录用户；隐私=创建者或超管
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	// 解析工单 ID（来自查询参数）
	idStr := r.URL.Query().Get("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少工单 id"))
		return
	}
	// 租户隔离：仅可取生效租户下的工单
	t, err := s.Store.GetTicket(id, s.effTenant(r, u))
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 404：详情是读接口，「本租户下查不到这张单」
		//   就是资源不存在；跨租户同样回 404 不泄露存在性（与上面 handleTicketRun 的 404 同一条安全口径）。
		s.writeError(w, r, apierrors.New(apierrors.ErrTicketNotFound, "工单不存在"))
		return
	}
	// 隐私校验：非创建者且非超管不可见他人工单详情
	if t.CreatedBy != u.ID && !auth.IsSuperAdmin(u) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "无权查看他人工单"))
		return
	}
	// 附带状态轨迹（流程步骤执行历史）
	states, _ := s.Store.TicketStates(id)
	// ★ 文件工单附带各文件处理状态（前端进度面板渲染用）
	tfiles, _ := s.Store.TicketFiles(id)
	// ★ G13（2026-10-19）：交付件清单回读——此前详情面只有 files（输入件＋状态），
	//   客户在页面上看不见自己已经拿到手的产物（四件都在 zip 里）。真值仍是磁盘上那一个 zip，
	//   这里只列条目名与大小，不新造第二份清单。
	// ★ 2026-10-30 组5（两段式）：新单的事实源换成 ticket_artifacts 表——`artifacts` 逐件出栈
	//   （id/file_id/lang/kind/name/size/primary/degraded，路径不外发），前端据此画两组按钮与预览；
	//   `deliverables` 保留但改为读表，**历史单（无 artifacts 行）仍回落旧 zip 清单**（方案 §5.6）。
	artifacts := s.ticketArtifacts(id)
	deliverables := ticketDeliverables(t, tfiles)
	if len(artifacts) > 0 {
		deliverables = ticketDeliverablesFromArtifacts(artifacts)
	}
	// ★ 改造 5（2026-09-17）用户侧 QA 报告透出：从 FinalResult payload 解析
	//   质检报告/评估分/不达标语言，详情接口一并返回（此前仅 xlsx 下载有 QA 列，界面零透出）。
	quality := parseTicketQuality(t.FinalResult)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": s.ticketJSON(t), "states": states, "files": tfiles, "deliverables": deliverables, "artifacts": ticketArtifactItems(artifacts), "progress": ticketProgressPct(t, states, tfiles), "quality": quality})
}

// ticketDeliverableItem 交付件清单的一条（★ G13）。
type ticketDeliverableItem struct {
	Name string `json:"name"` // 交付件文件名（多产物＝zip 内条目名；单产物＝文件名）
	Size int64  `json:"size"` // 字节数
	Kind string `json:"kind"` // restore=还原回写/主产物；text=纯文案 .md 旁路
}

// ticketDeliverables 回读本单的交付件清单（★ G13，2026-10-19）。
//
// 事实源就在磁盘上：多产物单在 service 层被 zipOutputs 打成一个 zip 存进 tickets.result_path
// （ticket_files.result_path 同值），按单号打开 zip 列条目即得「四件」；单产物单直接列一行；
// 纯文案旁路（text_result_path）另计为 text 档。**只读名字与大小**（zip.File 头部即带），
// 不读内容、不改库。与 zip 下载出口同一把尺子，避免详情面另造一份会漂的清单（同 §一·14）。
// 参数 t: 工单；tfiles: 该单源文件行。返回: 去重后的交付件列表（按读取顺序）。
func ticketDeliverables(t *store.Ticket, tfiles []*store.TicketFile) []ticketDeliverableItem {
	out := []ticketDeliverableItem{}
	seen := map[string]bool{}
	add := func(p, kind string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		if zr, err := zip.OpenReader(p); err == nil {
			for _, f := range zr.File {
				out = append(out, ticketDeliverableItem{Name: f.Name, Size: int64(f.UncompressedSize64), Kind: kind})
			}
			_ = zr.Close()
			return
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			out = append(out, ticketDeliverableItem{Name: filepath.Base(p), Size: info.Size(), Kind: kind})
		}
	}
	if t != nil {
		add(t.ResultPath, "restore")
		add(t.TextResultPath, "text")
	}
	for _, f := range tfiles {
		if f == nil {
			continue
		}
		add(f.ResultPath, "restore")
		add(f.TextResultPath, "text")
	}
	return out
}

// ticketQualityView 工单质量视图（改造 5）：从 FinalResult JSON 抽取用户侧可读的
// 质检报告、各语言评估分与评估不达标语言。解析失败（旧数据/非 JSON）返回零值结构不报错。
type ticketQualityView struct {
	QAReport         *qa.Report         `json:"qa_report,omitempty"`             // 确定性 QA 报告（空译文/同文/数字/占位符/漏翻/标点）
	EvalScores       map[string]float64 `json:"eval_scores,omitempty"`           // 语言 → 初翻评估总分（0-100）
	ReviewEvalScores map[string]float64 `json:"review_eval_scores,omitempty"`    // 语言 → 校对评估总分
	FlaggedLangs     []string           `json:"quality_flagged_langs,omitempty"` // 评估低于阈值语言（「质检存疑」徽标数据源）
	// ★ 2026-10-26 用户追加裁决九-1「上游发生的问题除了给运营/超管，也要给客户看」：
	// 上游那一族（整条不通／个别行拿不回东西）**过去只活在日志与来源列里**，
	// 客户在工单详情上看见的是"完成了"——那是把失败读数写成成功读数（AGENTS §一.3 那条 `from` 档位同族）。
	// 现在这一面直接出栈：哪一语种、哪一档、几行。三档分开各出一个 map，
	// 因为三档的**成因与补救动作不同**（上游断＝不可救、截断＝抬预算可救、坏形态＝已单独重拨过），
	// 混成一句"部分未译出"就是把归因藏起来（方案 §七 那条明令禁止的写法）。
	// 空值口径：三档都用 omitempty ⇒ 没回填时**键干脆不出**（不是 null、也不是 0），
	// 前端按"读不到＝这一单没有未译出行"渲染，避免常驻一个 0 让界面自己造一句"0 行未译出"的空话。
	Sources           map[string]string `json:"sources,omitempty"`             // 语言 → 来源档（名单见 orchestrator.Source*）
	UnresolvedLines   map[string]int    `json:"unresolved_lines,omitempty"`    // 语言 → 按原文回落的行数（总数）
	UpstreamDeadLines map[string]int    `json:"upstream_dead_lines,omitempty"` // 语言 → 其中判上游断那一部分
	TruncatedLines    map[string]int    `json:"truncated_lines,omitempty"`     // 语言 → 其中被 max_tokens 截断那一部分
}

// parseTicketQuality 从 final_result JSON 解析质量视图（改造 5）。
func parseTicketQuality(finalResult string) ticketQualityView {
	out := ticketQualityView{}
	if strings.TrimSpace(finalResult) == "" {
		return out
	}
	var p struct {
		QAReport            *qa.Report         `json:"qa_report"`
		EvalScores          map[string]float64 `json:"eval_scores"`
		ReviewEvalScores    map[string]float64 `json:"review_eval_scores"`
		QualityFlaggedLangs []string           `json:"quality_flagged_langs"`
		// 下面四件是九-1 新开的客户面读点（键名与 orchestrator.ticketPayload 逐字相同，
		// 抄错一个字母就是"接口说没回填而库里明明有"的那种假空态）。
		Sources           map[string]string `json:"sources"`
		UnresolvedLines   map[string]int    `json:"unresolved_lines"`
		UpstreamDeadLines map[string]int    `json:"upstream_dead_lines"`
		TruncatedLines    map[string]int    `json:"truncated_lines"`
	}
	if err := json.Unmarshal([]byte(finalResult), &p); err != nil {
		return out
	}
	out.QAReport = p.QAReport
	out.Sources = p.Sources
	out.UnresolvedLines = p.UnresolvedLines
	out.UpstreamDeadLines = p.UpstreamDeadLines
	out.TruncatedLines = p.TruncatedLines
	out.EvalScores = p.EvalScores
	out.ReviewEvalScores = p.ReviewEvalScores
	out.FlaggedLangs = p.QualityFlaggedLangs
	return out
}

// ticketDeliverable ★ F-42-d（2026-09-25 UAT 修复批）：工单是否真有可交付产物。
// 纯函数（files 由调用方备好，单测可直接注入），判据按工单形态分两支：
//   - 文件工单：任一子文件有原格式回写产物（result_path）或纯文案 .md（text_result_path），
//     或工单级产物列（tickets.result_path / text_result_path）非空；
//   - 文本工单：final_result 载荷的 translations 至少一种语言非空。
//
// 用途＝假 completed 的最后一道闸：状态机被判 completed 但产物为空时（F-42 链条的
// 空转重翻/欠费中止），对外不得宣称 completed、UI 不得露下载钮。两处调用点
// （api_openapi_tasks.go 任务详情状态映射、handleTicketDownload 下载校验）共用本函数，
// 避免「对外契约已修、租户界面还在发空文件」的第二轮事故。
// 参数：t=工单对象（nil 视为无产物），files=该单文件行（文本工单可传 nil）。
func ticketDeliverable(t *store.Ticket, files []*store.TicketFile) bool {
	if t == nil {
		return false
	}
	if t.FilePath != "" {
		for _, f := range files {
			if f != nil && (f.ResultPath != "" || f.TextResultPath != "") {
				return true
			}
		}
		return t.ResultPath != "" || t.TextResultPath != ""
	}
	var p struct {
		Translations map[string]string `json:"translations"`
	}
	if err := json.Unmarshal([]byte(t.FinalResult), &p); err != nil {
		return false
	}
	for _, tr := range p.Translations {
		if strings.TrimSpace(tr) != "" {
			return true
		}
	}
	return false
}

// ticketHasDeliverable 取数版包装：文件工单现读 ticket_files 行（与 ticket_artifacts 行）后判定。
// 读库失败按「无产物」处理（保守侧）——宁可少给一次下载，不可把空产物报成成功交付。
//
// ★ 2026-10-30 组5（B17）判据补全：两段式之后**多文件新单不写 tickets.result_path**
// （只写 ticket_files.result_path）且产物的事实源是 ticket_artifacts ⇒ 只看 result_path 会把
// 「产物都在、artifacts 也齐」的完成单判成无产物，下载端回 409。新判据：
// artifacts 有行 **或** 旧 result_path 非空（任一子文件、工单级、纯文案列都算）。
func (s *Server) ticketHasDeliverable(t *store.Ticket) bool {
	if t == nil {
		return false
	}
	var files []*store.TicketFile
	if t.FilePath != "" && s.Store != nil {
		files, _ = s.Store.TicketFiles(t.ID)
		if arts, err := s.Store.ListTicketArtifacts(t.ID); err == nil {
			for _, a := range arts {
				if a != nil && a.Path != "" {
					return true
				}
			}
		}
	}
	return ticketDeliverable(t, files)
}

// handleTicketDownload 下载工单翻译结果（创建者或超管）。
// 文件工单：流式返回原格式回写产物（docx/xlsx/pptx）；
// 纯文本工单：动态生成 xlsx 对照表（源文+各语言列）。
func (s *Server) handleTicketDownload(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	idStr := r.URL.Query().Get("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少工单 id"))
		return
	}
	t, err := s.Store.GetTicket(id, s.effTenant(r, u))
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 404：要下载的单在本租户下取不到就是资源不存在
		//   （跨租户不泄露存在性，同 handleTicketDetail 的 404 口径）。
		s.writeError(w, r, apierrors.New(apierrors.ErrTicketNotFound, "工单不存在"))
		return
	}
	// 校验链：仅创建者/超管可下载，且须已完成工单
	if t.CreatedBy != u.ID && !auth.IsSuperAdmin(u) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "无权下载他人工单结果"))
		return
	}
	if t.Status != store.TicketCompleted {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 409：请求合法、单也在，错在调用时机——
		//   工单还在排队/翻译（或已取消、被驳回），产物尚未生成。与开放接口
		//   /openapi/v1/tasks/download 的 not_ready→409 同一判据（F-64① 批 I-7 已订正那一侧）。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "工单尚未完成"))
		return
	}
	// ★ F-42-d（2026-09-25 UAT 修复批）：状态 completed 但无产物 ⇒ 不给下载。
	//   旧校验只看 status，假 completed（翻译中欠费中止后被空转重翻刷成完成）在租户界面
	//   照样露下载钮，点下去拿到空文件/空对照表，用户以为「翻译质量差」而非「本单没译成」。
	if !s.ticketHasDeliverable(t) {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 409：状态位写着 completed 但产物为空
		//   （鬼 completed），仍是「资源当前状态与请求冲突」，不是服务端这次出错；
		//   载荷里带不出文件，客户端需要的是「重新发起翻译」而不是重试本请求。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "该工单没有可用译文产物（可能因余额不足或流程中断），请重新发起翻译"))
		return
	}
	baseName := t.TicketNo
	if baseName == "" {
		baseName = fmt.Sprintf("ticket_%d", t.ID)
	}
	// ★ 工单双模式（2026-09-13）：fmt=text → 仅返回译文纯文案（.md），不取还原产物。
	// ★ 2026-10-30 组5：取件来源统一到 ticketArtifactFiles（用户面与开放 API 同一把尺子）——
	//   新单按 ticket_artifacts 表取，历史单回落 ticket_files/result_path；
	//   多件**按需流式打包**（不落盘），条目名用 artifact.name（与旧落盘 zip 逐字一致）。
	textOnly := strings.EqualFold(r.URL.Query().Get("fmt"), "text")
	fid, _ := strconv.ParseInt(r.URL.Query().Get("file_id"), 10, 64)
	tfiles, _ := s.Store.TicketFiles(t.ID)
	picks, _ := s.ticketArtifactFiles(t, tfiles, textOnly, fid, baseName)
	if len(picks) > 0 {
		zipName := baseName + ".zip"
		if textOnly {
			zipName = baseName + "_texts.zip"
		}
		s.serveTicketPicks(w, r, picks, zipName)
		return
	}
	if fid > 0 {
		// ★ 字面量必须写在**实参位**（词条覆盖棘轮只认 `New(code, "中文")`，变量传参会静默逃扫）
		if textOnly {
			s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "该文件暂无纯文案产物"))
		} else {
			s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "该文件的产物不存在"))
		}
		return
	}
	if textOnly {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 409：fmt=text 这条支路要的是纯文案产物，
		//   单子存在、下载权限也有，缺的是那份 .md 还没被生成出来（历史工单建单时没有双模式）。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "该工单暂无纯文案产物（历史工单建单时未生成），可重新发起工单获取"))
		return
	}
	// ① 旧单文件工单的产物（tickets.result_path）若在上面已被取件助手覆盖不会走到这里；
	//   走到这里说明文件产物确实都不可取（过期/丢失/多文件全部失败）——回明确状态而不是空体。
	if t.ResultPath != "" || len(tfiles) > 0 {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 409：整单有产物行，但可取的产物一条都没有
		//   （产物路径都指向已丢失/失败的行），仍是资源状态不满足下载条件。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "暂无已生成的产物（部分文件可能处理失败）"))
		return
	}
	// ② 纯文本工单：从 FinalResult 解析译文生成 xlsx 对照表
	var payload struct {
		Translations map[string]string `json:"translations"`
		QAReport     *qa.Report        `json:"qa_report"`
	}
	_ = json.Unmarshal([]byte(t.FinalResult), &payload)
	if len(payload.Translations) == 0 {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 409：文本工单的对照表要靠载荷里的译文列生成，
		//   载荷解析不出任何译文＝单子没有可交付内容（与上面「无产物」「未完成」同一族状态冲突），
		//   客户端要做的是重跑翻译，而不是重试同一个下载请求。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "暂无可下载的翻译结果"))
		return
	}
	f := excelize.NewFile()
	sheet := "Sheet1"
	langs := make([]string, 0, len(payload.Translations))
	for lc := range payload.Translations {
		langs = append(langs, lc)
	}
	sort.Strings(langs)
	_ = f.SetSheetName(sheet, sheet)
	headers := []string{"source_text"}
	for _, lc := range langs {
		// ★ 需求5：表头语言码 → 语言中文名（en→英文、fr→法语），无映射时回退语言码
		name := config.LangNames[lc]
		if name == "" {
			name = lc
		}
		headers = append(headers, name)
	}
	// QA 列：存在质检报告时追加（error 级前缀 ✖，warning 级 ⚠）
	hasQA := payload.QAReport != nil && len(payload.QAReport.Issues) > 0
	if hasQA {
		headers = append(headers, "QA")
	}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	// 源文可能多段（换行分隔），逐段成行；译文按行与源文对齐（alignTicketRow 复用
	// extractTextSegments 的按行映射口径，避免「源文两行、每行都填整段译文」的重复输出缺陷 2026-09-09）。
	srcLines := splitLines(t.SourceText)
	row := 2
	for i, line := range srcLines {
		_ = f.SetCellValue(sheet, cellName(1, row), line)
		for j, lc := range langs {
			_ = f.SetCellValue(sheet, cellName(j+2, row), alignTicketRow(t, srcLines, lc, i))
		}
		row++
	}
	if row == 2 {
		_ = f.SetCellValue(sheet, cellName(1, 2), t.SourceText)
		for i, lc := range langs {
			_ = f.SetCellValue(sheet, cellName(i+2, 2), payload.Translations[lc])
		}
	}
	// QA 汇总写入 QA 列首行数据行（整单一份报告，写在第 2 行末列）
	if hasQA {
		q := payload.QAReport
		var b strings.Builder
		fmt.Fprintf(&b, "pass=%v errors=%d warnings=%d", q.Pass, q.Errors, q.Warnings)
		for _, iss := range q.Issues {
			mark := "⚠"
			if iss.Level == "error" {
				mark = "✖"
			}
			fmt.Fprintf(&b, "\n%s [%s/%s] %s", mark, iss.Lang, iss.Rule, iss.Detail)
		}
		_ = f.SetCellValue(sheet, cellName(len(headers), 2), b.String())
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "生成 xlsx 失败"))
		return
	}
	xlsxName := baseName + ".xlsx"
	if ls := strings.Split(strings.TrimSpace(t.TargetLangs), ","); len(ls) == 1 && strings.TrimSpace(ls[0]) != "" {
		xlsxName = baseName + "-" + strings.TrimSpace(ls[0]) + ".xlsx"
	} else {
		xlsxName = baseName + "-multi.xlsx"
	}
	w.Header().Set("Content-Disposition", contentDisposition(xlsxName))
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	_, _ = w.Write(buf.Bytes())
}

// cellName 行列号转单元格名（excelize.CoordinatesToCellName 的本地简写）。
func cellName(col, row int) string {
	n, _ := excelize.CoordinatesToCellName(col, row)
	return n
}

// ============ 审批（approver + admin） ============

// handleApproveList 待审批列表接口（approver + admin 角色，权限等级 >=2）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回: success=true 时携带待审批 tickets 数组。
func (s *Server) handleApproveList(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	// 权限校验：需角色等级 >= 2（approver/admin/tenant_admin/super_admin）
	if err := auth.RequireRole(u, 2); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 租户隔离：仅列出生效租户下待审批工单
	tickets, err := s.Store.ListPendingApproval(s.effTenant(r, u))
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：待审批队列读的是本进程存储层，查询失败是服务端故障。
		//   审批台（admin/TicketsP loadApproval）只在 r.success 时 setApprovalTickets，200 壳下
		//   审批员看到的是「没有单要审」，而真实情况是数据库没答上来——漏审就这么发生。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tickets": s.ticketsViewJSON(tickets)})
}

// handleApproveAction 审批操作接口：approve（批准）/ reject（驳回，带意见）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/action/reason/suggestion/approved_text）。
// 返回: success=true 表示审批完成；批准会触发自迭代（feedback 步骤）。
func (s *Server) handleApproveAction(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	// 权限校验：需角色等级 >= 2
	if err := auth.RequireRole(u, 2); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID           int64  `json:"id"`            // 工单 ID
		Action       string `json:"action"`        // approve / reject
		Reason       string `json:"reason"`        // 驳回原因
		Suggestion   string `json:"suggestion"`    // 修改建议（附加到驳回原因）
		ApprovedText string `json:"approved_text"` // 审批员可编辑终稿
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	// 租户隔离：仅可取生效租户下的工单
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 404：待审批的单在本租户下取不到就是资源不存在
		//   （跨租户不泄露存在性，与 handleTicketDetail/handleTicketRun 同一口径）。
		s.writeError(w, r, apierrors.New(apierrors.ErrTicketNotFound, "工单不存在"))
		return
	}
	// 状态机约束：仅待审批状态的工单可审批
	if t.Status != store.TicketPendingAppr {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 409：单子在、审批员权限也有，冲突的是工单状态
		//   （已被别处批准/驳回，或还没提交审批）。审批操作幂等性差（重复批准会重复触发自迭代），
		//   状态码必须让调用方能一眼分支到「这单已经不在审批台上」，而不是重试同一个动作。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "工单不在待审批状态"))
		return
	}
	// 根据action 更新工单状态与审批字段
	switch req.Action {
	case "approve":
		t.Status = store.TicketApproved
		t.ApproverID = u.ID
		// ★ 整改 C4：终稿修订写回载荷 JSON（按语言定位合并覆盖），不再用裸文本整体替换
		//   FinalResult——旧实现撑破 JSON 结构，重跑流水线时被新建载荷静默丢弃。
		if req.ApprovedText != "" {
			if merged := applyApprovedTextOverride(t.FinalResult, req.ApprovedText); merged != "" {
				t.FinalResult = merged
				s.Store.LogAudit(s.effTenant(r, u), u.ID, "approve_edit", "tickets", t.TicketNo+" 审批终稿已并入载荷")
			} else {
				s.Store.LogAudit(s.effTenant(r, u), u.ID, "approve_edit_skip", "tickets", t.TicketNo+" 终稿无法定位语言，保留原载荷")
			}
		}
	case "reject":
		t.Status = store.TicketRejected
		t.ApproverID = u.ID
		t.RejectReason = req.Reason
		if req.Suggestion != "" {
			t.RejectReason = strings.TrimSpace(req.Reason + "；建议: " + req.Suggestion)
		}
		// ★ F-42-b（2026-09-25 UAT 修复批）：本处是「人工驳回意见」的唯一入口，
		//   来源必须落 'human'——重翻分支（workflow.runAIInitial）与自动认领
		//   （store.ClaimTicketForRun）都以该值作判据，区分「审批台写的意见」与
		//   「系统失败原因」，后者不得被当意见喂给模型。
		t.RejectSource = store.RejectSourceHuman
	default:
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "action 仅支持 approve/reject"))
		return
	}
	if err := s.Store.UpdateTicket(t); err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：状态与审批字段落库失败是存储写入故障，
		//   工单实际还停在 pending_approval（下方的审批审计与批准后的自迭代都没跑），这次审批没生效；
		//   按状态码分支的调用方在 200 壳下会读成「这次审批落定了」，转而把单子当已处理。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// 审批审计
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "approve_"+req.Action, "tickets", t.TicketNo)

	// 批准 → 异步触发自迭代（feedback 步骤）。
	// ★ 整改 C4：改为入队后台执行——此前在 HTTP 请求内同步重跑整条流水线，
	//   客户端断连会让工单永久卡在 approved；配合 flow 层「approved 态仅 QA+feedback」
	//   短路（见 orchestrator.applyModeOverride），二次质检失败也不会翻案人工结论。
	if req.Action == "approve" && s.TicketSvc != nil {
		if _, enqErr := s.TicketSvc.EnqueueTicketRun(context.Background(), t.ID); enqErr == nil {
			writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": s.ticketJSON(t), "queued": true})
			return
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": s.ticketJSON(t)})
}

// applyApprovedTextOverride 把审批员修订的终稿合并进工单载荷 JSON。
// 语言定位规则：① 与某语言现有译文完全一致 → 覆盖该语言；② 载荷仅一个语言 → 直接覆盖；
// 无法定位时返回空串（调用方保持原载荷不动并留痕审计）。返回合并后的 FinalResult。
func applyApprovedTextOverride(finalResult, approvedText string) string {
	var p struct {
		SourceText   string            `json:"source_text"`
		TargetLangs  []string          `json:"target_langs"`
		Translations map[string]string `json:"translations"`
		Sources      map[string]string `json:"sources"`
	}
	if json.Unmarshal([]byte(finalResult), &p) != nil || len(p.Translations) == 0 {
		return ""
	}
	// 定位语言：先按译文全文相等命中；命中不了且载荷仅一个语言时直接认定该语言
	hit := ""
	for lc, v := range p.Translations {
		if v == approvedText {
			hit = lc
			break
		}
	}
	if hit == "" && len(p.Translations) == 1 {
		for lc := range p.Translations {
			hit = lc
		}
	}
	if hit == "" {
		return ""
	}
	// 命中后覆盖对应语言译文并把 sources 标为 approved，整体重组 payload 返回
	p.Translations[hit] = approvedText
	if p.Sources == nil {
		p.Sources = map[string]string{}
	}
	p.Sources[hit] = orchestrator.SourceApproved
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// workflow 构建工作流（惰性，需 Store+Engine+Tenant 齐备）。
// 返回: 编排工作流实例；存储或引擎未初始化时返回 nil。
func (s *Server) workflow() *orchestrator.Workflow {
	if s.Store == nil || s.Engine == nil {
		return nil
	}
	return orchestrator.NewWorkflow(s.Store, s.Engine, s.Ten, s.DB)
}

// resultFileName 生成多文件工单产物在 zip 中的文件名（原文件名 + 语言后缀 + 原产物扩展名）。
// 参数 f: 工单文件行；返回形如 "报告_zh.docx"。
func resultFileName(f *store.TicketFile) string {
	base := f.FileName
	if base == "" {
		base = fmt.Sprintf("file_%d", f.ID)
	}
	ext := filepath.Ext(f.ResultPath)
	if ext == "" {
		ext = filepath.Ext(base)
	}
	return strings.TrimSuffix(base, filepath.Ext(base)) + ext
}

// （原 serveTicketTextDeliverable 已于 2026-10-30 组5 退役：取件/打包合并进
// ticketArtifactFiles + serveTicketPicks 一份助手，用户面与开放 API 共用同一把尺子。
// 保留这段说明是为了让后来者知道"fmt=text 的行为去哪了"——它没有消失，
// 只是取件来源换成了 ticket_artifacts 表、打包改成按需流式。）

// serveOneFile 流式返回单个磁盘文件（不存在/不可读时回 JSON 错误）。
func (s *Server) serveOneFile(w http.ResponseWriter, r *http.Request, path, dlName string) {
	fh, err := os.Open(path)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "产物文件不存在"))
		return
	}
	defer fh.Close()
	w.Header().Set("Content-Disposition", contentDisposition(dlName))
	http.ServeContent(w, r, dlName, time.Now(), fh)
}

// textDeliverableName 纯文案产物下载名：优先用产物真实 basename（base-<lang>-text.md），
// 旧产物才回落源文件主名-translated。<lang> 由 engine 命名层统一加（2026-10-24）。
func textDeliverableName(f *store.TicketFile) string {
	base := strings.TrimSuffix(f.FileName, filepath.Ext(f.FileName))
	if base == "" {
		base = fmt.Sprintf("file_%d", f.ID)
	}
	p := f.TextResultPath
	if p == "" {
		p = f.ResultPath
	}
	if n := engine.DeliverableBaseName(p, ""); n != "" {
		return n
	}
	return base + "-translated" + filepath.Ext(p)
}

// handleTicketDelete 删除已完成工单及其关联文件（创建者或超管；弹窗确认由前端负责）。
func (s *Server) handleTicketDelete(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	// 校验链：登录 → body id 合法 → 工单存在 → 仅创建者/超管 → 进行中/排队中禁删
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "工单不存在"))
		return
	}
	if t.CreatedBy != u.ID && !auth.IsSuperAdmin(u) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "无权删除他人工单"))
		return
	}
	// 删除状态闸口：排队/翻译中禁删（避免 worker 写回已删工单），完成/取消才可删
	if t.Status == store.TicketInProgress || t.Status == store.TicketQueued {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "工单正在翻译中，无法删除"))
		return
	}
	if err := s.Store.DeleteTicketWithFiles(req.ID, s.effTenant(r, u)); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_delete", "tickets", t.TicketNo)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleTicketCancel 用户取消工单：仅创建者或超管；排队中/翻译中可取消。
// 取消为标记式：worker 收尾前复查状态，已取消则放弃计费/完成态/通知。
func (s *Server) handleTicketCancel(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录"))
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少工单 id"))
		return
	}
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 404：要取消的单在本租户下不存在
		//   （跨租户不泄露存在性，与详情/运行/审批四处同一族 404）。
		s.writeError(w, r, apierrors.New(apierrors.ErrTicketNotFound, "工单不存在"))
		return
	}
	if t.CreatedBy != u.ID && !auth.IsSuperAdmin(u) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "无权取消他人工单"))
		return
	}
	// 取消闸口：仅创建者/超管，且状态必须是排队中/翻译中
	if t.Status != store.TicketQueued && t.Status != store.TicketInProgress {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 409：取消是对「工单」这个资源的状态迁移，
		//   单子已完成/已取消/已批准时这个迁移不被允许——冲突在资源状态，不在载荷（400）或权限（403）。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "当前状态不可取消（仅排队中/翻译中）"))
		return
	}
	if err := s.Store.CancelTicket(req.ID); err != nil {
		msg := err.Error()
		// ★ 用户友好映射：底层并发锁冲突对用户表现为「系统繁忙」
		if strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked") {
			// ★ F-64②（批 I-10）：原 200 承载失败 → 503：并发锁冲突是**瞬时**的存储忙，
			//   文案本身就承诺「稍候重试」，503 才让客户端的重试器按退避处理；
			//   写成 200 时前端把「繁忙」当业务失败弹个红条，用户只能手动反复点。
			// ★ 〇-Z（2026-09-28）：这句必须写成**实参位字面量**，不能先 `msg = "…"` 再传 ——
			//   词条覆盖棘轮 TestAPICnMessageLiteralsCovered 的正则只认 `New(code, "中文")`，
			//   变量传参会让这句静默逃过扫描（本条就是批 I-10 当时漏进 catalog 的那句，
			//   英文用户点「取消工单」撞到锁时会看到中文）。机制锁见 lang_middleware_test.go。
			s.writeError(w, r, apierrors.New(apierrors.ErrServiceUnavailable, "系统繁忙，工单取消未成功，请稍候重试"))
			return
		}
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：非锁冲突的取消失败就是存储写入故障
		//   （工单状态没落住），底层原文照旧逐字透出，便于按 trace_id 排查。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, err.Error()))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_cancel", "tickets", t.TicketNo)
	s.Store.SetTicketState(t.ID, "cancel", "success", "by user")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
