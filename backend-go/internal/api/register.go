// ============ register.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 本文件实现自助注册与体验额度发放、邀请码管理：
//   - handleRegister：自助注册（无邀请码→新建独立试用租户 + tenant_admin 账号；有绑定租户的邀请码→加入已有租户 + user 账号）
//   - 体验额度：新租户自动发放 free_trial_tokens（system_config 可配置，出厂档 1,000 积分折 400000 token/14 天，★ F-78 汇率 1:400）并写入每日字符上限权限
//   - 注册开关：system_config registration_enabled=0 时关闭注册
//   - 邀请码管理（super_admin）：列表 / 创建 / 撤销（handleInviteCodes / handleInviteCodeCreate / handleInviteCodeRevoke）
// 安全要点：邀请码一次性使用（used=1 即失效）；受邀加入前校验邮箱未被占用（用户名同租户可重名）。
// ★ G9（2026-10-19）：撤销是状态位（revoked=1）而非删行，保留 used_by/used_at 追溯；撤销后注册口拒收且不烧码。

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"translator/internal/ops"

	"translator/internal/auth"
	"translator/internal/billing"
	apierrors "translator/internal/errors"
	"translator/internal/observability"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ 自助注册 + 试用额度 ============

// handleRegister 自助注册接口：
//   - 不携带邀请码：创建独立租户（试用额度）→ 创建该租户 tenant_admin 账号
//   - 携带邀请码且邀请码绑定租户：加入已有租户（创建 user 账号）
//   - 体验额度默认发放（system_config free_trial_tokens，出厂档 1,000 积分折 400000 token，★ F-78 汇率 1:400），并在 permissions 记录 max_daily_chars
//
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 username/password/code/name/invite）。
// 返回: success=true 时携带新用户、tenant_id 与提示信息。
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	// 平台未初始化（缺存储/租户存储）时拒绝注册
	if s.Store == nil || s.Ten == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "平台未初始化"))
		return
	}
	// 注册开关：默认开放（system_config registration_enabled=0 时关闭）
	if v, _ := s.Store.GetConfig("registration_enabled"); v == "0" {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "注册已关闭，请联系管理员开通"))
		return
	}
	// ⚠️ IP 频率护栏不在这里：它已挪到「升级通道判定可算之后」（见下方 upgradeBlankID 那一段），
	//    否则body 还没解码、拿不到邮箱，无法区分「新注册」与「本人空白账号升级」。
	// 专属域名自助注册：从访问 Host 解析目标租户（命中路径见 resolveDedicatedTenant 注释）。
	// 命中后注册强制归入该企业且仅为普通成员（禁止建企业/升管理员）。
	// ★ F-77（2026-09-27 注释订正，旧注释写的是"免邀请码"，与下方代码相反）：
	//   本分支**必须有该企业的邀请码**才放行——由企业超管/租管在邀请码页发放（见本文件
	//   handleInviteCodeCreate）；企业不想走自助注册时，另一条路是在后台用 Excel 批量导入成员
	//   （见 server.go 的用户批量导入路由，导入后首登强制改密）。两者都没有的企业成员只能走普通注册。
	dedicatedTid := resolveDedicatedTenant(s, r)
	var req struct {
		Username    string `json:"username"`      // 注册用户名
		Password    string `json:"password"`      // 密码（至少 6 位）
		Code        string `json:"code"`          // 租户编码（无邀请码时必填）
		Name        string `json:"name"`          // 租户名称
		Invite      string `json:"invite"`        // 邀请码（可选）
		Email       string `json:"email"`         // 联系邮箱（找回密码验证码接收）
		EmailCode   string `json:"email_code"`    // 邮箱验证码（email_verify_enabled=1 时必填）
		Captcha     string `json:"captcha_token"` // 人机验证 token（captcha_provider=turnstile 时必填）
		Industry    string `json:"industry"`      // 所属行业（新租户注册时必填，来自行业包 code）
		JobRole     string `json:"job_role"`      // ★ 角色功能（2026-09-19）：职业角色（角色包 persona code，可空；无效值静默忽略不阻断注册）
		AppLang     string `json:"app_lang"`      // ★ F-17（2026-09-25 批E）：注册界面语种（12 码白名单，前端随 getLang() 上报；无效/缺省回落 X-App-Lang 头，再落空=中文链路）
		RoleChoice  string `json:"role_choice"`   // 角色选择（兼容旧客户端）：admin=我是管理员(建企业) / user=我是普通用户(邀请码加入)
		Type        string `json:"type"`          // 注册类型：personal=个人用户 / enterprise=企业用户（默认）
		Ref         string `json:"ref"`           // 个人邀请码（可选，邀请裂变：?ref=<个人码> 链接携带）
		DeviceID    string `json:"device_id"`     // ★ F-81（2026-09-28）：浏览器设备号（与免登录试用 lib/trialDevice.ts 同一份事实，供设备档防薅记账；可空，见下方 regDevice 的取值口径）
		BrandName   string `json:"brand_name"`    // 品牌中文名（企业注册引导填写，种入企业知识库固定用法）
		BrandNameEn string `json:"brand_name_en"` // 品牌英文名（覆盖所有非 zh/zh_hant 目标语固定用法）
		BrandNames  string `json:"brand_names"`   // 品牌多语言名 JSON（{"zh":"极石","en":"ROX"}，可选；含其它语言名时优先）
		// ★ S4 归因（2026-09-14）：UTM 五参由落地页捕获、注册时随表单上报（可空）
		UTMSource   string `json:"utm_source"`
		UTMMedium   string `json:"utm_medium"`
		UTMCampaign string `json:"utm_campaign"`
		UTMTerm     string `json:"utm_term"`
		UTMContent  string `json:"utm_content"`
		LandingPath string `json:"landing_path"` // 落地页路径（前端 location.pathname）
		// ★ 协议签署（2026-08-27 需求）：注册即视为同意《用户协议》与《隐私协议》，
		//   前端注册表单须勾选后方可提交；勾选时 agreed=true 并随注册写入签署时间。
		Agreed bool `json:"agreed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	// 字段归一化（★ 2026-10-21 IAM 重做 P0-1：账号与邮箱只走 auth 那一把尺子）
	// ⚠️ **口令刻意不再 trim**：旧形态这里把密码 TrimSpace 后入库，而登录原样比对
	//   （auth.go:79 的解码不改内容）⇒ "带首尾空格的密码注册进得去、登录登不进"，
	//   正是运营者报的第 1 条的一半根因。空格是口令的一部分，两个口子不许各削一刀。
	//   存量影响＝零：老账号存的是 trim 后的值，用户平时输入的也是 trim 后的值。
	req.Username = auth.NormalizeAccount(req.Username)
	// 邮箱归一化必须在"必填 / 一次性域名黑名单 / 全局唯一 / 验码"这四腿**之前**做完，
	// 否则四腿各读一份原值，`Foo@X.com` 与 `foo@x.com` 会在不同腿上给出不同答案。
	req.Email = auth.NormalizeEmail(req.Email)
	req.EmailCode = strings.TrimSpace(req.EmailCode)
	req.Code = strings.TrimSpace(req.Code)
	req.Name = strings.TrimSpace(req.Name)
	req.BrandName = strings.TrimSpace(req.BrandName)
	req.BrandNameEn = strings.TrimSpace(req.BrandNameEn)
	// 基础校验：用户名密码必填
	if req.Username == "" || req.Password == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "用户名和密码不能为空"))
		return
	}
	// 密码强度：走 iam 那把唯一尺子（★ P0-3；文案逐字沿用「密码至少 6 位」，catalog_en 词条已在位）
	if !auth.ValidatePassword(req.Password) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, auth.PasswordTooShortMsg))
		return
	}
	// 协议同意校验：注册必须勾选同意《用户协议》与《隐私协议》
	if !req.Agreed {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请阅读并同意《用户协议》与《隐私协议》"))
		return
	}

	// ★ 体验额度统一口径（任务2.2）：仅 free_trial_tokens / free_trial_days 为唯一配置键，
	//   旧键 trial_tokens / trial_sentences 已完全移除；句数镜像由 token÷折算率换算。
	defaultKey := "" // 新租户默认 API Key（明文，仅注册响应返回一次）
	// ★ C6（2026-09-12）：默认值单一来源（ops.DefaultEffective），读取点收敛到 trialConfig
	// ★ 2026-10-21 M3：发放天数由 autoGrantUserTrial 内部再取同一口径，这里只用于句数镜像。
	trialTokens, _ := s.trialConfig()
	// 体验额度句数镜像（展示用）：token ÷ 句↔token 折算率
	trialSentences := int64(0)
	if rate := s.Store.TokenSentenceRate(); rate > 0 {
		trialSentences = trialTokens / rate
	}

	// 1. 处理邀请码（可选）：校验有效且未使用，标记为已使用
	inviteTenantID := int64(0)
	// ★ 邮箱必填：所有自助注册路径（含受邀加入）都必须提供邮箱——验证码收件与找回密码依赖
	// ★ S3 防薅：一次性/临时邮箱域名黑名单（注册礼包=唯一钩子，先卡域名再验码）
	// ★ 变量名用 rejectMsg 而非 msg：本文件下方（注册成功的响应体）另有一个
	//   `msg := "注册成功，试用额度已发放"`，而 TestAPICnMessageEscapePattern 是**文本级**闸门
	//   （按标识符名判「这个变量被赋过中文字面量」），同名会让本行被误判成「中文经变量传给 New」。
	//   这里 msg 的来源是 disposableEmailRejected 的函数返回值，不是中文字面量，属合规形态。
	if rejectMsg := s.disposableEmailRejected(req.Email); rejectMsg != "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, rejectMsg))
		return
	}
	if strings.TrimSpace(req.Email) == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "邮箱为必填项"))
		return
	}
	// ★ 2026-10-22 统一登录批：「完善信息」升级注册通道（只有主站自动建号场景能触发）。
	//   主站邮箱验证码登录会为未注册邮箱自动创建空白个人账号（email_login.go
	//   createPersonalByEmail：无口令 + profile_setup_required=1）。登录后 AI 补全面板里
	//   改选「企业」（新建组织 / 凭邀请码加入）再提交时，下方邮箱唯一腿会 409 拒死——
	//   挡住注册的"其他账号"正是调用者本人几分钟前刚自动建的那个空白号。
	//   放行条件三条同时满足（缺一不可）：① 请求带有效 JWT；② 邮箱归属调用者本人；
	//   ③ 该账号是空白账号（无口令且待补全标记在身）。放行动作见下方唯一腿：先停用
	//   空白账号释放邮箱，再原样走新建账号+组织。游客注册与租户站注册不带这条边，
	//   upgradeBlankID 恒为 0，行为逐字节不变；防薅三档（IP/设备/平台名额）照旧占格。
	caller := s.authUser(r)
	upgradeBlankID := int64(0)
	if caller != nil && strings.EqualFold(caller.Email, req.Email) &&
		caller.PasswordHash == "" && caller.ProfileSetupRequired == 1 {
		upgradeBlankID = caller.ID
	}
	// ★ 2026-10-24 多身份 B3（§6.1 注册口汇流 + §6.3 删除静默降级）：
	//   企业凭据（管理台手输邀请码 / 专属邀请链接 / 专属域名码）在这里**一次性解析完**，
	//   下面整条链只用 `cred` 这一个事实，不再各读一遍 req.Invite——旧形态是「type/role 分派读一次、
	//   专属域名门禁读一次、烧码读一次、inviteOrgID 再读一次、wasInviteBind 还读一次」五把尺子，
	//   同一张码在一条链上可以得到五个不同答案（G9 加撤销位、链接加预绑邮箱那次都得跟着改五处，迟早漏一处）。
	//   位置必须在 IP 档**之前**：R5 定的是「持码加入企业豁免 IP 日限/最小间隔」，判据要拿到凭据才有。
	//   本调用只读库（不烧码、不建号）；无效/已用/非企业码一律显式 400，绝不再悄悄降级。
	cred, credErr := s.resolveEnterpriseCredential(r.Context(), req.Invite, req.Type, req.RoleChoice, req.Email, dedicatedTid)
	if credErr != nil {
		s.writeError(w, r, credErr)
		return
	}
	// 注册频率护栏：同 IP 24h 内注册次数上限 + 最小间隔（防脚本批量薅试用额度）
	// ★ F-81（2026-09-28）：这两档改走 configIntTier，与设备档／平台日预算档同口径
	//   （环境变量 > system_config 同名小写键 > 代码默认）。行为等价：env 未配时读的还是原来那两个
	//   system_config 键、默认值也还是 3 次／60 秒；差别只在于**现在可以不发版临时调档**。
	//   最小间隔那一档允许配 0（0＝不设间隔，run_uat 与本地快跑就是这么配的），故 allowZero 传 true。
	// ★ 2026-10-23（用户报障「注册后引导完善信息改选企业即报『注册过于频繁，请 N 秒后再试』」）：
	//   护栏从函数最前面（body 解码之前）挪到这里——拿不到邮箱就没法区分新注册与升级，
	//   而**升级通道不吃 IP 档**：那条路转化的是调用者本人几分钟前刚自动建、已经计过一次
	//   IP 的空白账号，不是新注册；拿 IP 档拦它等于把产品自己的主流程「注册→引导完善→
	//   改选企业」掐死（最小间隔 60 秒内必撞，日限 3 次当天试两轮就满）。
	//   防薅本尊没放：升级仍要过设备档/平台档（reserveFreeAccount——升级确实新建租户并
	//   发放体验额度）、人机验证、一次性邮箱黑名单，以及「本人空白账号」三条件。
	//   record 那一步同样跳过（见下方「登记注册成功」处），一次用户动作不扣两次 IP 账。
	// ★ 2026-10-24 B3（R5）：企业凭据门同样不吃 IP 档——员工入职由**企业自己发的一次性码**限量，
	//   而不是由平台名额决定一个客户能不能给自己公司加人（同一 NAT 出口 50 人同天入职，
	//   拿默认 3 次/24h 拦他们等于把企业客户的入职流程打死；这一档与旧 joinWithInvite/dedicatedTid
	//   豁免同一取向，只是判据从「读 req.Invite」换成「凭据已解析成功」）。
	ip := clientIP(r)
	if upgradeBlankID == 0 && cred == nil {
		dailyLimit := configIntTier(s.Store, "REGISTER_IP_DAILY", "register_ip_daily_limit", regIPDailyDefault, false)
		minInterval := configIntTier(s.Store, "REGISTER_IP_MIN_INTERVAL", "register_ip_min_interval_sec", regIPMinIntervalDef, true)
		if ok, wait := s.regGuard.allow(ip, dailyLimit, minInterval); !ok {
			// ★ F-47（批 I-7）：改走统一错误出口＝429 + code RATE_LIMITED + Retry-After 头 + retry_after 字段。
			// 文案保留「请 N 秒后再试」原样（i18n patternsEN 已有该变体，仍可翻）；秒数同时进字段，
			// 这样前端不必从中文里抠数字就能做倒计时（旧写法只有中文文案里夹一个数，非中文语种无法复用）。
			s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, fmt.Sprintf("注册过于频繁，请 %d 秒后再试", wait)).WithRetryAfter(wait))
			return
		}
	}
	// 邮箱全局唯一（他人已绑定则拒绝）——★ 2026-10-21 P0-9：这一腿**已挪到"验码＋人机"之后**，
	// 判据本体见下方 `该邮箱已被其他账号绑定` 那一段的注释（防枚举：先证明邮箱归你，才告诉你它被占用）。
	// 邮箱验证开关（email_verify_enabled=1）：自助注册必须先验证邮箱归属（受邀加入不受影响）
	verifyOn := false
	if v, _ := s.Store.GetConfig("email_verify_enabled"); v == "1" {
		verifyOn = true
	}
	// emailProven=本次注册已证明邮箱归你（验码通过，或本人 JWT——见 planEnterpriseJoin 的 proven 两半）。
	// 它只服务一个判据：§6.1 分流能不能说出"你已在该企业中"这句实话。
	emailProven := false
	// ★ 升级通道豁免 register-purpose 验码：邮箱归属已由本次登录的 login-purpose 验证码
	//   证明（同一邮箱、同一会话、间隔分钟级）；补全面板按用户令不再索取邮箱与验证码，
	//   此处若仍强校验，「完善信息改选企业」在开验部署下必吃 400。其余路径照旧。
	// ★ 2026-10-24 B3（§6.3）：**带企业凭据不再豁免验码**。旧豁免条件是 `req.Invite != ""`，
	//   于是任何人都能靠"塞一个码串"绕过邮箱归属证明，再从 §6.1 的占用文案里读出
	//   "这个邮箱已经是某家企业的成员"（P0-9 挪位那一批堵的就是这个枚举面，豁免腿留着等于没堵）。
	//   现在验码通过即记下 emailProven，分流判定只认这一个事实；未证明时占用分支一律回
	//   历史那一句 409「该邮箱已被其他账号绑定」，与改造前逐字等价。
	if verifyOn && upgradeBlankID == 0 {
		if strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.EmailCode) == "" {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请填写邮箱并输入验证码"))
			return
		}
		if !verifyEmailCode(req.Email, req.EmailCode) {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "邮箱验证码错误或已过期"))
			return
		}
		emailProven = true
	}
	// 人机验证（captcha_provider=turnstile 时校验；自助注册与受邀加入均拦截）
	if err := s.verifyCaptcha(r, req.Captcha); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 邮箱全局唯一（他人已绑定则拒绝）
	// ★ F-64② 收尾定夺（2026-09-26 批 I-10）：原回 400，与换绑邮箱那两处
	//   （handleSendEmailCode / handleUpdateEmail 回 409 CONFLICT）**同一句文案两个码**——
	//   前端/SDK 按 code 分支时，同一种失败要写两条判断，且日志聚合会把它们当成两类问题。
	//   语义上这是「与库里既有记录撞车」而不是「请求写错了」（载荷完全合法，换个邮箱就好），
	//   故统一成 409。状态码从 400 换 409 不改变前端行为：两个码都是非 2xx，
	//   request() 一律抛 ApiError 并把 body.message 落到红字（Login.tsx 的 catch 分支）。
	// ★ 2026-10-21 IAM 重做 P0-9（防枚举）：这一腿**从"必填邮箱"处挪到了"验码＋人机"之后**。
	//   旧顺序让任何人**不带任何凭证**打一次注册就能读出"这个邮箱是不是我们的客户"，
	//   而找回密码那条链为此刻意做了恒真文案——两条链一把防枚举一把裸奔，等于没有。
	//   现在要先证明邮箱归你（收件箱里那条码）并过人机，才知道它被占用；
	//   代价是"邮箱已被占用"的人白等一封验证码邮件（可接受：他本来就要去登录或换邮箱）。
	//   ⚠️ 残余风险要如实说：`email_verify_enabled=0` 且 `captcha_provider=none` 时这一腿仍然可被枚举
	//   ——那是**部署档位**的取舍（《部署指南》§生产档位明写生产应开邮箱验证），不在代码里替他兜。
	// ★ 2026-10-24 B3（§6.1）：**企业凭据门不走这一条全局唯一腿**。邮箱唯一性已从「全局唯一」改成
	//   「同租户内唯一」（D3），一个人可以在 A 公司做员工、同时在 B 公司做员工；跨租户命中不再是冲突，
	//   而是要按 §6.1 的六种情形分流（建新家行／补全空白家行／只挂成员行／已在该企业／注销宽限期／他人占用）。
	//   分流判定本身**只读**——放在烧码之前，任何一条拒绝都不许消耗客户自己发出去的邀请码（AGENTS §一·10）。
	var joinPlanRes *joinPlan
	if cred != nil {
		p, perr := s.planEnterpriseJoin(r.Context(), req.Email, cred.TenantID, caller, emailProven)
		if perr != nil {
			s.writeError(w, r, perr)
			return
		}
		joinPlanRes = p
	} else if other, oerr := s.Store.GetUserByEmail(req.Email); oerr == nil && other != nil {
		// ★ 升级通道：占用者是调用者本人的空白账号时，先停用它释放邮箱再放行。
		//   idx_users_email 是**部分唯一索引**（只约束 active/deactivating，见 iam/store.go
		//   SetUserEmail 注释），置 disabled 即释放；GetUserByEmail/CreateUserWithEmail
		//   两处查重都带 status 过滤，停用后新建账号不会撞索引。旧账号行保留（数据不删），
		//   与「注销账号邮箱可被占用」的既有语义同族。
		if upgradeBlankID == 0 || other.ID != upgradeBlankID {
			s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "该邮箱已被其他账号绑定"))
			return
		}
		if uerr := s.Store.UpdateUser(other.ID, other.TenantID, other.DisplayName, other.Role, store.UserDisabled, other.OrgID); uerr != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "账号升级失败，请稍后重试"))
			return
		}
		s.Store.LogAudit(other.TenantID, other.ID, "account_upgrade", "users",
			"完善信息升级注册：空白个人账号已停用，邮箱由新注册账号接管")
	}
	// ★ 注册类型（个人用户 / 企业用户）：
	//   personal = 个人用户：自动建个人租户，好友邀请码经 ref 走邀请裂变（个人用户默认可获奖励）；
	//   enterprise（默认，兼容旧客户端）= 合并原「管理员/普通用户」为单一企业注册，注册人成为企业管理员。
	// ★ 2026-10-24 B3（§6.3 删除静默降级）：这里只剩「个人用户要自动起一个个人租户编码」一件事。
	//   被删掉的两条静默路（都在 resolveEnterpriseCredential 里翻成显式 400）：
	//     ① role_choice=member + 无效/已用码 → 悄悄降级为个人用户（用户以为自己进了企业，实际建了个个人号）；
	//     ② 带了码但没写 role_choice → 悄悄清空 req.Invite、另建一家企业（客户自己的员工入职
	//        变成凭空多出一家公司，还顺手发放一份体验额度）。
	//   专属域名（dedicatedTid>0）与凭据门（cred!=nil）都不再新建租户，因此整段分派对它们直接跳过。
	//   ⚠️ req.Invite 从此**不再被本函数改写**：它是原始入参，凭据判定只有一个事实源 cred
	//      （旧形态一边改它一边在四处重读它，是五把尺子里最容易被下一批改错的一把）。
	creatingPersonal := false
	if dedicatedTid == 0 && cred == nil {
		switch strings.ToLower(strings.TrimSpace(req.Type)) {
		case "personal":
			creatingPersonal = true
			// 自动生成个人租户编码/名称（前端不填企业信息）
			req.Code = fmt.Sprintf("p_%d", time.Now().UnixNano())
			if req.Name == "" {
				req.Name = req.Username
			}
			if req.Name == "" {
				req.Name = "个人用户"
			}
		}
	}
	// ★ F-81（2026-09-28 用户令「注册免费账号也是和免费体验试用一样有防薅限制」）：
	//   本次要**新建免费账号（自带体验额度）**时，先过设备档与平台日预算档。
	// 位置收在这里（邀请码尚未被标记消耗之前），三条理由：
	//   ① 判据要落在「会不会新建租户」这个分支上——企业凭据门（cred!=nil，含专属域名注册与
	//      手输码/链接加入）都不新建租户、不发免费额度，用企业自己发的**一次性邀请码**做闸门，
	//      把它们算进平台名额会把客户自己的员工入职打成「今日注册名额已满」（50 人同 NAT 出口更是要排三天队）；
	//      ★ 2026-10-24 B3：旧判据是 `dedicatedTid == 0 && !joinWithInvite`，其中 joinWithInvite
	//      要靠「再读一次邀请码」才置位（第五把尺子）；现在统一成 `cred == nil`。
	//   ② 放在 MarkInviteCodeUsed 之前，拒绝时不会连带烧掉一个有效邀请码（烧码＝客户资产损失）；
	//   ③ 设备号取不到就当没有，**不返 400**：这是公开注册接口的对外契约，老缓存包／脚本客户端／
	//      管理台代客注册都可能不带这个字段（F-64、F-79 两批"契约改动引发现网故障"的同形教训）。
	//      不带设备号照样吃 IP 档（默认 3 次/24h，比设备档更紧）＋邮箱验证＋人机验证＋一次性邮箱黑名单。
	regDevice := ""
	if id := strings.TrimSpace(req.DeviceID); trialDeviceIDRe.MatchString(id) {
		regDevice = id // 字符集与免登录试用同一道白名单：这个串直接当限流表 key 片段，脏值进来只会污染账本
	}
	// regTicket 是本笔在设备档／平台档上占住的格子。声明放在 if 之外：
	// 后面「额度真发出去了」那一刻要调它 markSpent()，而 defer release() 在这里就挂上。
	var regTicket *freeAccountTicket
	if cred == nil {
		devLimit := configIntTier(s.Store, "REGISTER_DEVICE_DAILY", "register_device_daily_limit", regDeviceDailyDefault, false)
		globalLimit := configIntTier(s.Store, "REGISTER_GLOBAL_DAILY", "register_global_daily_limit", regGlobalDailyDefault, false)
		// ★ 占格子而不是「看一眼够不够」：判据收在带条件的单条 UPDATE 里，
		//   并发请求不能同时读到「还差一格」双双放行（C25 在邀请奖励上踩过的同一竞态形态）。
		tk, wait, reason := s.regGuard.reserveFreeAccount(regDevice, devLimit, globalLimit)
		if tk == nil {
			if reason == "global" {
				// 平台日名额被打满是市场费用异常信号（刷量或推广爆量），当天就该让运维看见，而不是月底对账（同 trial_budget 口径）
				s.registerBudgetAlert(wait)
				s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, "今日免费注册名额已用完，请明天再试").WithRetryAfter(wait))
				return
			}
			s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, "该设备今天注册的账号已达上限，请明天再试或登录已有账号").WithRetryAfter(wait).
				WithDetails(map[string]interface{}{"reason": reason}))
			return
		}
		regTicket = tk
		// 兜底退格：从这一行到 markSpent() 之间的任何一条 return（校验失败、企业编码撞码、建租户出错）
		// 都不该把用户当日额度扣掉——「系统没做成」的那几次算在用户头上，是防薅机制最容易被投诉的一种坏法。
		defer regTicket.release()
	}

	// ★ 2026-10-24 B3：专属域名门禁（码必填、须属于该企业）与五态判定都已上移到
	//   resolveEnterpriseCredential（品牌子域那一支），这里不再重读 req.Invite、不再自己比
	//   used/revoked/expires_at——那三处各判一次是 G9 加撤销位、§6.2 加过期与预绑邮箱时最容易漏改的形态。
	// 烧码单点：位置在「防薅占格成功 ＋ §6.1 分流判定通过」之后、任何写库之前。
	//   ★ 整改 A4 沿用：抢占失败＝被并发请求抢先，拒绝本次注册（一张码只放一个人进门）。
	if cred != nil {
		inviteTenantID = cred.TenantID
		if claimed, merr := s.Store.MarkInviteCodeUsed(cred.RowID, req.Username); merr != nil || !claimed {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, credentialRejectText("used", cred.FromLink)))
			return
		}
	}

	// 2. 创建租户（无绑定租户时新建独立试用租户）
	tenantInfo := map[string]interface{}{}
	if inviteTenantID == 0 {
		if req.Code == "" {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请提供租户编码"))
			return
		}
		if req.Name == "" {
			req.Name = req.Code
		}
		// Req8（2026-08-26 UAT 产品决策修订）：行业不再拒绝注册——
		//   缺选 → 通用行业兜底；错选(不存在) → 通用行业兜底；通用包也缺失(异常部署)
		//   → 跳过行业载入不阻塞注册。行业仅决定共享行业包的载入范围。
		industryCode := strings.TrimSpace(req.Industry)
		if industryCode == "" {
			industryCode = store.GeneralIndustryCode
		}
		industryPkg, ipErr := s.Store.FindIndustryByCode(industryCode)
		if ipErr != nil {
			industryPkg, ipErr = s.Store.FindIndustryByCode(store.GeneralIndustryCode)
		}
		if ipErr != nil {
			// 极端兜底：连通用包都缺失（部署异常）——置 nil，下方跳过 SetIndustry
			industryPkg = nil
			s.Store.LogAudit(0, 0, "register_industry_missing", "kb_packages",
				"通用行业包缺失，注册未载入行业(租户编码:"+req.Code+")")
		}
		// 权限：试用每日上限（D4 token 口径优先）；
		// ★ 任务2.2：注册一律自动发放体验额度（不再有审核模式不发分支）
		// ★ F-21①（2026-09-25 UAT 修复批）：默认墙不再硬编码 20000——改读运营策略
		//   Limits.DefaultMaxDailyChars/Tokens（ops 已有旋钮、平台级可配），注册链路与
		//   管理台同一把尺子；策略未配时 ops.DefaultEffective 兜底 20000 行为不变。
		lim := s.opsBaseEffective(0).Limits
		perms := &tenant.Perms{MaxDailyChars: lim.DefaultMaxDailyChars, MaxDailyTokens: lim.DefaultMaxDailyTokens}
		perms.SentenceBalance = trialSentences
		perms.PackageCode = "trial"
		pb, _ := json.Marshal(perms)
		t, err := s.Ten.Create(req.Code, req.Name, "", string(pb))
		if err != nil {
			// ★ 脱敏（2026-09-12）：驱动错误不透吐（PG/sqlite 文案不同且含约束名）
			if store.IsUniqueViolation(err) {
				s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "租户创建失败：该企业编码已存在"))
				return
			}
			log.Printf("[register] 租户创建失败 code=%s: %v", req.Code, err)
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "租户创建失败: "+store.DebriefDBError(err)))
			return
		}
		inviteTenantID = t.ID
		tenantInfo = map[string]interface{}{
			"id": t.ID, "code": t.Code, "name": t.Name, "status": t.Status,
			"expires_at": t.ExpiresAt, "permissions": t.Permissions,
		}
		// 初始化租户：默认 KB 包（组织包/部门包/语言文化包）+ 余额账户
		_ = s.Store.EnsureDefaultPackages(inviteTenantID)
		// ★ 品牌固定用法种入企业知识库（2026-09-04）：出厂把 品牌中文名/英文名（或其它语言名）
		//   初始化进企业包 L1 术语，防止后续翻译把品牌名音译为 jixi/jishi 等拼音变体。
		s.seedTenantBrandTerms(inviteTenantID, req.BrandName, req.BrandNameEn, req.BrandNames)
		// 行业包单轨制：仅记录注册所选行业编码（含通用兜底），内容从共享宿主（租户1）
		// 按行业载入，不再建空壳包；industryPkg=nil（异常部署）时跳过
		if industryPkg != nil {
			_ = s.Ten.SetIndustry(inviteTenantID, industryPkg.Code)
		}
		_ = s.Store.EnsureBalance(inviteTenantID)
	} else {
		// 受邀加入已有租户：返回被加入租户信息
		if t, err := s.Ten.GetByID(inviteTenantID); err == nil {
			tenantInfo = map[string]interface{}{
				"id": t.ID, "code": t.Code, "name": t.Name, "status": t.Status,
				"expires_at": t.ExpiresAt, "permissions": t.Permissions,
			}
		}
	}
	// 个人用户租户标记（企业用户默认 is_personal=0，无需额外处理）。
	// 个人用户默认可参与邀请好友奖励；企业用户默认不参与（见下方 ref 奖励门禁）。
	// ★ 2026-09-12：不得吞错——此前 `_ =` 把 PG 下 bool→INTEGER 的类型错误静默吞掉，
	//   导致个人标记恒失败、邀请奖励全量停发且无任何痕迹。失败必须留日志。
	if creatingPersonal && inviteTenantID > 0 {
		if perr := s.Ten.SetPersonal(inviteTenantID, true); perr != nil {
			log.Printf("[register] 个人租户标记失败 tid=%d: %v", inviteTenantID, perr)
		}
	}

	// 3. 创建账号
	//   无凭据 ⇒ 新建独立租户，注册人就是该租户的 tenant_admin（个人用户与企业管理员同形）；
	//   企业凭据 ⇒ 交给 createEnterpriseJoin 按 §6.1 的三种形态落位，角色与部门取自**凭据本身**
	//     （旧形态是"绑了租户的码一律降成 user"，管理员发的 tenant_admin 码也把入职者降成普通成员，
	//      与创建端写进 invite_codes.role 的那一列自相矛盾——读侧与写侧同一把尺子，AGENTS §一·8①）。
	//   ★ 2026-10-24 B3：这里不再调 wasInviteBind、也不再第二次 GetInviteCodeByCode 取 OrgID，
	//     凭据的一切读数只有 cred 一份（旧形态在同一条链上把同一张码读了五遍）。
	regAgreedAt := ""
	if req.Agreed {
		regAgreedAt = time.Now().Format(time.RFC3339)
	}
	var nu *store.User
	if cred != nil {
		joined, jerr := s.createEnterpriseJoin(r.Context(), cred, joinPlanRes, req.Username, req.Password, req.Email, regAgreedAt)
		if jerr != nil {
			s.writeError(w, r, jerr)
			return
		}
		nu = joined
	} else {
		// ★ 2026-10-21 IAM 重做：建号 + 绑邮箱 + 协议时间同一事务。
		// 旧形态先 CreateUser 再 `_ = SetUserEmail`，第二步失败被吞 ⇒ 回 success 但用户永远收不到验证码。
		fresh, cerr := s.Store.CreateUserWithEmail(inviteTenantID, req.Username, auth.PasswordHash(req.Password), req.Username,
			store.RoleTenantAdmin, 0, 0, strings.ToLower(strings.TrimSpace(req.Email)), regAgreedAt, 0)
		if cerr != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "用户创建失败: "+cerr.Error()))
			return
		}
		nu = fresh
	}
	// ★ F-81 坐实点：账号已经建成，这一格从此不再退（额度发放失败由登录路径补发）。
	//   企业凭据门不占格（cred!=nil ⇒ regTicket 恒 nil），这一句天然空转，保留是给读代码的人看坐实点。
	if regTicket != nil {
		regTicket.markSpent()
	}
	// ★ 2026-10-21 M3：账号级体验额度自动发放（一个账号仅一次）。
	// ★ 2026-10-24 B3（方案 R5）：**企业凭据门建出来的身份不发体验额度**。理由是钱落在哪一边：
	//   GrantUserTrialOnce 发放时 funded 的是**租户**，给一家公司每加一个员工身份就凭空多一份平台补贴额度，
	//   而企业侧用量本该走 store/quota_org.go 的部门/组织预算墙。这里给这一行落一笔 tokens=0 的**永久标记**
	//   （与 UserTrialGrantMigrate 的 'backfill' 占位同一形态），登录/邮箱登录那两条补发腿看到它就不要再发。
	if cred != nil {
		s.markTrialNotGranted(r.Context(), nu)
	} else {
		s.autoGrantUserTrial(r.Context(), nu)
	}
	// 记录协议签署时间（注册勾选即视为同意《用户协议》与《隐私协议》，给后台超管「协议签署」tab 留痕）
	// ★ B3：成员行（joinMemberRow/joinCompleteHome）建出来的那一行不带协议时间（CreateIdentityRow 不写这一列），
	//   出栈里硬填一个库里没有的值＝把"这个人同意过"写成猜测，只在真落库的形态上回显。
	if req.Agreed && (cred == nil || joinPlanRes.Action == joinNewHome) {
		nu.AgreedAt = regAgreedAt // 已在建号事务里落库
	}
	// ★ 角色功能（2026-09-19）：注册时选择的职业角色落库（所有注册分支统一在此写入——
	//   个人/企业管理员/受邀成员/专属域名共用同一账号创建点）。job_role 仅绑用户层级：
	//   校验「启用中角色包 code」，无效/停用值静默忽略（不阻断注册，与行业回落同口径）。
	if code := strings.ToLower(strings.TrimSpace(req.JobRole)); code != "" {
		if _, perr := s.Store.FindEnabledPersonaByCode(code); perr == nil {
			if serr := s.Store.SetJobRole(nu.ID, inviteTenantID, code); serr == nil {
				nu.JobRole = code
			}
		}
	}
	// ★ F-17（2026-09-25 批E）邮件语种跟随：注册界面语种落库（users.preferred_lang）。
	//   载荷 app_lang 优先、X-App-Lang 头兜底；12 码白名单外一律落空串（=中文链路，
	//   与 job_role「无效值静默忽略不阻断注册」同口径）。后续手册/验证码/企业欢迎邮件按该语种取稿。
	mailLang := requestMailLang(r, req.AppLang)
	if mailLang != "" {
		_ = s.Store.SetPreferredLang(nu.ID, inviteTenantID, mailLang)
	}
	// 绑定联系邮箱（用于找回密码）
	if req.Email != "" {
		// 邮箱已在建号事务里绑定（nu.Email 已由 CreateUserWithEmail 返回）。
		// ★ S4 归因快照：注册主链路成功后落库（失败不阻断）
		_ = s.Store.InsertRegAttribution(&store.RegAttribution{
			UserID: nu.ID, TenantID: nu.TenantID,
			UTMSource: strings.TrimSpace(req.UTMSource), UTMMedium: strings.TrimSpace(req.UTMMedium),
			UTMCampaign: strings.TrimSpace(req.UTMCampaign), UTMTerm: strings.TrimSpace(req.UTMTerm),
			UTMContent: strings.TrimSpace(req.UTMContent), RefCode: strings.TrimSpace(req.Ref),
			Host: r.Host, LandingPath: req.LandingPath, UserAgent: r.Header.Get("User-Agent"),
		})
	}
	// ★ 注册成功自动发送《产品手册》PDF 邮件（个人/企业用户均发送；用 info 专用邮箱，附件为手册 PDF）
	if req.Email != "" {
		// 后台异步发送《产品手册》PDF 邮件，避免阻塞注册响应（★ F-17：语种随注册界面语言）
		go func(to, uname, lang string) {
			_ = s.sendManualEmail(to, uname, lang)
		}(strings.TrimSpace(req.Email), req.Username, mailLang)
	}
	// ★ 企业注册提醒（需求 2026-08-27）：企业用户注册成功后，向注册人发送欢迎邮件
	//   并抄送运营邮箱（抄送地址由 OPS_NOTIFY_EMAIL 环境变量控制，空=不抄送）建联。
	//   仅「新建企业（非个人、非企业凭据门）」触发；个人用户不抄送。
	// ★ 2026-10-24 B3：判据从 `req.Invite == "" && dedicatedTid == 0` 换成 `cred == nil`——
	//   本函数已不再改写 req.Invite，而 role_choice=admin 带着码（我要新建企业）时 cred 为 nil、
	//   请求里那个串却非空；沿用旧判据会把这一档新企业的运营建联邮件漏掉。
	//   语义不变：只有「这次真的凭空建了一家新公司」才给运营抄送一份。
	if !creatingPersonal && cred == nil && req.Email != "" {
		// 后台异步发送企业注册欢迎邮件并抄送运营，不阻塞注册响应（★ F-17：语种随注册界面语言）
		go func(to, name, username, lang string) {
			_ = s.sendTemplatedMail(to, "enterprise_reg", lang, map[string]string{
				"name":     name,
				"username": username,
				"email":    to,
			})
		}(strings.TrimSpace(req.Email), req.Name, req.Username, mailLang)
	}
	// ★ 新租户默认 API Key（2026-08-26 冒烟修复）：移到建号之后签发并强绑定创建人——
	//   原「先发 Key 后建号」产生 user_id=0 的孤儿 Key，被 authenticateAPIKey
	//   「Key 必须归属用户」闸门拦截，新注册租户的 Key 要重启服务（backfill）后才生效。
	//   专属域名注册视为「加入已有企业」，不签发独立 API Key（与邀请码加入保持一致）。
	//   ★ B3 判据同上一条换成 cred==nil：Key 只发给「自己新建的那家公司」的创建人。
	if inviteTenantID > 0 && cred == nil {
		defaultKey = s.issueDefaultAPIKeyFor(inviteTenantID, nu.ID, "默认 Key")
	}
	// ★ 邀请裂变首绑（白皮书 §5）：携带个人邀请码注册→写入 referred_by（首绑闸门），
	//   绑定成功即给邀请人叠加体验奖励：+invite_reward_tokens、时长 +invite_extend_days（与既有体验到期取大后叠加，按对去重）
	//   ★ 总开关门禁（2026-08-26 U3）：referral_enabled=0 时跳过绑定与奖励发放
	//   ★ 运营策略引擎（2026-09-05）：invite.enabled 因子参与门禁（默认开启，零感知兼容）
	if strings.TrimSpace(req.Ref) != "" && s.Store.ReferralEnabled() && s.effectivePolicy(nu.TenantID).Invite.Enabled {
		if inviterUID, inviterTID, ok := s.Store.BindReferral(nu.ID, nu.TenantID, strings.TrimSpace(req.Ref)); ok {
			// ★ 需求 2026-08-27：企业用户默认不参与邀请好友奖励；仅个人用户邀请人可获奖励。
			inviterPersonal := false
			if it, ie := s.Ten.GetByID(inviterTID); ie == nil {
				inviterPersonal = it.IsPersonal
			}
			if !inviterPersonal {
				s.Store.LogAudit(inviteTenantID, nu.ID, "referral_bind", "user",
					fmt.Sprintf("受邀绑定邀请人 uid=%d（企业用户，不参与邀请奖励）", inviterUID))
			} else {
				// ★ 运营策略引擎（2026-09-05）：邀请奖励因子优先读最终策略 invite.*
				//   （策略显式值 > 存量 system_config 散键 > 代码内置默认）。
				inv := s.effectivePolicy(inviterTID).Invite
				// ★ F-78（2026-09-28 〇-X）：兜底档改取 store 常量（积分面值 × 出厂汇率），
				// 旧写法把 300000 钉死在代码里，汇率 1:300→1:400 后若配置缺键会静默少发 1/4 额度。
				refTokens := store.DefaultInviteRewardPoints * store.DefaultPointsTokensRate
				if v, _ := s.Store.GetConfig("invite_reward_tokens"); v != "" {
					if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
						refTokens = x
					}
				}
				if inv.RewardTokens > 0 {
					refTokens = inv.RewardTokens
				}
				refDays := 14
				if v, _ := s.Store.GetConfig("invite_extend_days"); v != "" {
					if x, e := strconv.Atoi(v); e == nil && x > 0 {
						refDays = x
					}
				}
				if inv.RewardDays > 0 {
					refDays = inv.RewardDays
				}
				// ★ 整改 A5：单邀请人日发放上限（referral_max_daily_rewards，默认 50 笔）——
				//   把「换 IP 自邀刷奖励」的损失上限钉死为可配置常数；触顶升 critical 告警拒发。
				//   配套 U3 总开关（referral_enabled=0 全量停发）构成两级防薅。
				maxDaily := ops.DefaultInviteMaxDailyRewards() // ★ C6 单一来源
				if v, _ := s.Store.GetConfig("referral_max_daily_rewards"); v != "" {
					if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
						maxDaily = x
					}
				}
				if inv.MaxDailyRewards > 0 {
					maxDaily = int64(inv.MaxDailyRewards)
				}
				// ★ C25：日上限守卫移入发放事务（条件计数器 UPDATE），
				//   消除旧「COUNT 判断→发放」竞态击穿；触顶以哨兵错误回报并告警。
				switch gerr := s.Store.GrantTrialStack(inviterUID, inviterTID, nu.ID, refTokens, refDays, maxDaily); {
				case gerr == nil:
					s.Store.LogAudit(inviteTenantID, nu.ID, "referral_bind", "user", fmt.Sprintf("受邀绑定邀请人 uid=%d", inviterUID))
					// ★ #33 任务系统：好友注册成功 → 邀请人 +500 临时积分（有效期 14 天、可叠加、按受邀人去重）。
					// 与体验叠加奖励同点触发：裂变日上限触顶（ErrReferralCap）时本笔不发，防绕开防刷闸门。
					s.grantTaskEvent(r, inviterUID, store.TaskKeyInviteReg, "invitee:"+strconv.FormatInt(nu.ID, 10))
				case errors.Is(gerr, store.ErrReferralCap):
					s.Store.CreateAlert(inviterTID, "critical", "referral_cap",
						fmt.Sprintf("邀请人 uid=%d 今日奖励已达上限 %d 笔，本笔(+%d token/%d天)已拒发，请人工核实",
							inviterUID, maxDaily, refTokens, refDays))
				default:
					log.Printf("[register] 邀请奖励发放失败（占用未落地，可重触发）inviter=%d invitee=%d: %v", inviterUID, nu.ID, gerr)
				}
			}
		}
	}
	// 清空密码哈希后返回
	nu.PasswordHash = ""
	// 注册审计
	// ★ 2026-10-24 B3：判据从「再读一次邀请码判断有没有绑租户」换成 cred——并如实记下这一行的**真实角色**
	//   （凭据门建出来的可能是 tenant_admin，写死 "user" 会让排障的人按错的角色找逻辑）。
	auditRole := "admin"
	if cred != nil {
		auditRole = nu.Role
	}
	s.Store.LogAudit(inviteTenantID, nu.ID, "register", "auth", "自助注册 role="+auditRole)
	// ★ 2026-10-24 B3（§6.1/§七）：凭据门另记一条 membership_create——它是「一个人多了一份企业身份」
	//   这件事的唯一留痕（register 那条只说"有人注册了"，说不出他是被哪家公司的哪张码放进来的）。
	//   动作名已在 12 语种词典补译（auditActionCoverage 基线 32 不动）。
	if cred != nil {
		s.Store.LogAudit(cred.TenantID, nu.ID, "membership_create", "user",
			fmt.Sprintf("凭企业邀请加入（来源=%s 形态=%s 编码=%s）", cred.Source, joinPlanRes.Action, tidLabel(cred.TenantID)))
	}
	// 登记注册成功（推进同 IP 频率窗口）
	// ★ 2026-10-23：升级通道不记 IP 账——这一次转化已经计过 IP（自动建号那次），再记一次
	//   等于把一次用户动作扣成两次；设备档/平台档的账照记（占格在 reserveFreeAccount）。
	// ★ 2026-10-24 B3（R5）：企业凭据门同样不记——上面已豁免 allow() 判定，这里再 record 一次
	//   等于"今天没放任何人进门却把这家公司的 IP 格子用完了"，会让同 IP 的下一个自然日注册者白等。
	if upgradeBlankID == 0 && cred == nil {
		s.regGuard.record(ip)
	}
	// 成功提示（任务2.2：注册即自动发放体验额度，不再有审核等待态）
	// ★ B3：凭据门不发体验额度（R5），文案不许再承诺"额度已发放"——客户按这句话去查余额会以为平台扣了钱。
	msg := "注册成功，试用额度已发放"
	if cred != nil {
		msg = "注册成功，已加入企业"
	}
	regResp := map[string]interface{}{
		"success":   true,
		"message":   msg,
		"user":      nu,
		"tenant_id": inviteTenantID,
		"tenant":    tenantInfo,
	}
	// ★ 2026-10-24 B3（§6.5 前端切换器）：凭据门建出来的身份天然带账户号，注册响应顺手回一份身份清单，
	//   前端不必再打一次 /api/auth/identities 才知道自己现在有几个人可切（少一次往返＝少一次弱链路白屏）。
	//   只在凭据门出栈：普通注册的账户号只有一行，回它是无信息量字段（也不给旧客户端添新键）。
	if cred != nil {
		regResp["identities"] = s.identityListOrEmpty(nu)
	}
	// ★ 新租户默认 API Key：明文仅此一次随注册响应返回（受邀加入为空串）
	if defaultKey != "" {
		regResp["api_key"] = defaultKey
	}
	// ★ 2026-10-21 IAM 重做 P0-4（注册即登录）：注册成功后**由服务端直接签发会话**，
	//   与登录走同一个 auth.Sign（同一把尺子：24 h、同一 claims 形状、同一 token_version 语义）。
	//   旧形态注册只回 success，前端再拿刚填的账号密码回 `/api/auth/login` 补登一次（Login.tsx:305），
	//   而那次补登当时**只认用户名**、注册填的却是邮箱 ⇒ 结构上必失败；失败原因又写在
	//   另一屏的报错位上（error 槽只在登录屏渲染）⇒ 用户只看到"注册成功，然后什么都没发生"。
	// ⚠️ 签发失败**不许把注册判成失败**：账号已建、额度已发，回一个 5xx 会把"注册成功"
	//   抹成"注册失败"，用户再点一次只会再建一个同邮箱账号而被邮箱唯一索引拦下。
	//   所以这里只出一行 WARN、少发一个字段，前端自然回落到"请你去登录"。
	if tok, serr := auth.Sign(nu, 24*time.Hour); serr == nil {
		regResp["token"] = tok
	} else {
		observability.Warn(r.Context(), "注册成功但会话签发失败（用户需自行登录）", "uid", nu.ID, "err", serr.Error())
	}
	writeJSON(w, 200, regResp)
}

// registerBudgetAlert 平台每日免费注册名额触顶告警（★ F-81，与 trial.go 的 trialBudgetAlert 同口径）。
// 触发即说明今天的免费额度发放异常（刷量或推广爆量），值得让运维当天看到并去核 rate_limits 的 reg_day 账。
// 去重靠 Store.CreateAlert 的「同租户+同类型已有 open 告警即跳过」语义（alerts 表内建），
// 故这里不自建哨兵窗口——自己造会同时面对「告警已解决却仍压在哨兵里不发」和
// 「哨兵过期但告警还开着」两种互相矛盾的状态，交给数据层那一份事实更省事。
// 参数 wait: 被拒请求拿到的建议等待秒数（≈窗口剩余），写进告警正文方便判断还要不要人工提额。
func (s *Server) registerBudgetAlert(wait int) {
	if s.Store == nil {
		return
	}
	cnt := int64(-1)
	if st, err := s.Store.RateLoad(regScopeGlobal, regGlobalKey); err == nil {
		cnt = st.Count
	}
	_ = s.Store.CreateAlert(0, "warning", "register_budget",
		"今日免费注册名额已打满（已累计 "+strconv.FormatInt(cnt, 10)+" 个，约 "+strconv.Itoa(wait)+" 秒后窗口重置），如非推广爆量请排查刷号")
}

// handleGrantTrial 超管向租户发放/重新发放体验额度（任务2.4：放开幂等，改为叠加发放）：//   - ★ 重新发放语义：每次调用都叠加发放一份全新体验额度（独立到期日），
//
//	  已开通/已订阅租户也可再领（适用于体验用完/过期的老租户再给一次体验）
//	- 发放内容：体验 token（free_trial_tokens）+ 句数镜像（token÷折算率），并置 package_code="trial"（仅当前为空时）
//
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 tenant_id，需 super_admin）。
// 返回: success=true 时携带发放后的句数余额。
func (s *Server) handleGrantTrial(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		TenantID int64 `json:"tenant_id"` // 待发放的租户 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TenantID < 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "无效的租户 ID"))
		return
	}
	// ★ C30（2026-09-12）：tid<=0 不再发放体验额度——该桶在 gateUsage 全量豁免计费，
	//   发放无意义且留雷（历史实现给租户 0 建 trial 台账，永不被消费也永不告警）。
	//   注册流程的租户都已在建租户环节拿到真实 ID，此处仅防御异常入参。
	if req.TenantID <= 0 {
		// F-64②：tid<=0 是「发放对象选错了」——上面 559 行已经按 JSON 解析失败/负数回过 400，
		// 这一条是业务规则层的同因拒绝（平台上下文桶在 gateUsage 全量豁免计费，发进去也永不消费），
		// 载荷本身不合法 → 400 VALIDATION_ERROR。不取 403：调用方已是超管（requireAdminUser 过了），
		// 权限没问题，是他传的对象不对；也不取 409：这里没有「与既有记录状态冲突」的语义。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "无效的发放对象（平台上下文不参与体验额度）"))
		return
	}
	t, err := s.Ten.GetByID(req.TenantID)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "租户不存在"))
		return
	}
	// ★ 任务2.4：不再幂等拒绝——每次都是「重新发放」叠加一份新体验
	perms := tenant.ParsePerms(t.Permissions)
	// 读取发放配置（与注册路径同一套配置键，唯一口径 free_trial_*）
	// ★ C6：与注册路径同一读取点
	trialTokens, trialDays := s.trialConfig()
	trialSentences := int64(0)
	if rate := s.Store.TokenSentenceRate(); rate > 0 {
		trialSentences = trialTokens / rate
	}
	// 发放：句数镜像叠加 + 包身份（仅当前为空时置 trial，避免覆盖付费订阅身份）
	perms.SentenceBalance += trialSentences
	if perms.PackageCode == "" {
		perms.PackageCode = "trial"
	}
	pb, _ := json.Marshal(perms)
	if err := s.Ten.Update(t.ID, t.Name, t.ExpiresAt, string(pb)); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// 充值 token 余额（叠加新台账行，独立到期日）
	_ = s.Store.EnsureBalance(t.ID)
	if trialTokens > 0 {
		if gerr := s.Store.CreateQuotaGrant(t.ID, "trial", trialTokens, time.Now().Add(time.Duration(trialDays)*24*time.Hour), "register", 0); gerr != nil {
			_ = s.Store.Charge(t.ID, trialTokens)
		}
		// ★ 影子余额失效重建：发放后立即让计费影子重新 seed，避免「已发放仍提示耗尽」
		billing.InvalidateShadow(t.ID)
	}
	// 审计 + 通知租户管理员
	s.Store.LogAuditDiff(s.effTenant(r, u), u.ID, "grant_trial", "tenant", strconv.FormatInt(t.ID, 10),
		`{"package_code":""}`, `{"package_code":"trial","sentences":`+strconv.FormatInt(trialSentences, 10)+`}`)
	s.notifyTenantAdmins(t.ID, "体验额度已重新发放", "您的企业工作台已重新发放体验额度，体验 token "+strconv.FormatInt(trialTokens, 10)+" 已到账（有效期 "+strconv.Itoa(trialDays)+" 天）。")
	writeJSON(w, 200, map[string]interface{}{"success": true, "sentence_balance": perms.SentenceBalance})
}

// notifyTenantAdmins 向指定租户的全部租户管理员发送站内通知；email_notify_enabled=1 时
// 同时向其绑定邮箱发送邮件触达（找回密码同链路，SMTP 未配置时 Noop 打印日志）。
// 参数 tid: 租户 ID；title/body: 通知标题与正文。
func (s *Server) notifyTenantAdmins(tid int64, title, body string) {
	users, err := s.Store.ListUsers(tid)
	if err != nil {
		return
	}
	emailOn, _ := s.Store.GetConfig("email_notify_enabled")
	for _, usr := range users {
		if usr.Role != store.RoleTenantAdmin {
			continue
		}
		_ = s.Store.CreateNotification(usr.ID, title, body, store.NotifyRefTenant, tid)
		if emailOn == "1" && usr.Email != "" {
			// 后台异步向成员发送租户通知邮件，避免阻塞循环
			// ★ F-17（批E）口径：通知标题/正文由触发方中文字面量传入，语种维度本批不启用（传空=中文链路）
			go func(to string) {
				_ = s.sendTemplatedMail(to, "tenant_notify", "", map[string]string{
					"title": title,
					"body":  body,
				})
			}(usr.Email)
		}
	}
}

// hasOpenAlert 判断租户是否存在指定类型的未处理告警（邮件触达去重依据）。
func (s *Server) hasOpenAlert(tid int64, kind string) bool {
	alerts, err := s.Store.ListAlerts(tid, "open", 50)
	if err != nil {
		return false
	}
	for _, a := range alerts {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

// mergeBrandNameMap 品牌名三列的唯一合并口径（★ 2026-10-23 F3-b）：
//   - 显式 brandNamesJSON（BrandP 现在总会回传）是**权威全集**：`{}` 表示清空，缺某语种即删除；
//   - 未显式提供时以库内现有 brand_names 为底、表单非空值覆盖——载荷不带 brand_names 不再等于清零；
//   - 空串一律剔除，避免把「没有这个语种」存成空值。
//
// 旧形态「只用表单三参数重建」在中文名框载不回真值时会整列覆盖掉 zh（BYD 事故的直接机制）。
func mergeBrandNameMap(baseJSON, explicitJSON, brandName, brandNameEn string) (map[string]string, error) {
	names := map[string]string{}
	if strings.TrimSpace(explicitJSON) != "" {
		if err := json.Unmarshal([]byte(explicitJSON), &names); err != nil {
			return nil, fmt.Errorf("brand_names 需为合法 JSON 对象")
		}
		if names == nil {
			names = map[string]string{}
		}
	} else if strings.TrimSpace(baseJSON) != "" {
		if err := json.Unmarshal([]byte(baseJSON), &names); err != nil || names == nil {
			names = map[string]string{}
		}
	}
	if v := strings.TrimSpace(brandName); v != "" {
		names["zh"] = v
	}
	if v := strings.TrimSpace(brandNameEn); v != "" {
		names["en"] = v
	}
	for k, v := range names {
		if strings.TrimSpace(v) == "" {
			delete(names, k)
		}
	}
	return names, nil
}

// currentBrandNames 读取租户库内现有品牌名 JSON（读不到按空底）。
func (s *Server) currentBrandNames(tid int64) string {
	if s.Ten == nil || tid <= 0 {
		return ""
	}
	if t, err := s.Ten.GetByID(tid); err == nil && t != nil {
		return t.BrandNames
	}
	return ""
}

// seedTenantBrandTerms 把注册/建租户/后台保存时的品牌固定用法种入租户企业知识库：
// 品牌中文名（zh）→ 品牌英文名（en，或其它语言名），防止后续翻译把品牌名音译/漂移。
// 规则：
//   - 合并口径见 mergeBrandNameMap（显式 brandNames 权威；否则库内为底、表单非空覆盖）；
//   - 「没有其它语言名称」时，英文名覆盖所有非 zh/zh_hant 目标语固定用法（SeedBrandTerms 兜底）；
//   - F3-a：三列 brand_name / brand_names / brand_name_en 同批写入（注册后中文名框不再是空值）。
func (s *Server) seedTenantBrandTerms(tid int64, brandName, brandNameEn, brandNamesJSON string) {
	if s.Store == nil || s.Ten == nil || tid <= 0 {
		return
	}
	brandName = strings.TrimSpace(brandName)
	brandNameEn = strings.TrimSpace(brandNameEn)
	explicit := strings.TrimSpace(brandNamesJSON) != ""
	names, err := mergeBrandNameMap(s.currentBrandNames(tid), brandNamesJSON, brandName, brandNameEn)
	if err != nil {
		// 非法 JSON 不回写库：退回「库内为底 + 表单覆盖」，避免把整列洗空。
		names, _ = mergeBrandNameMap(s.currentBrandNames(tid), "", brandName, brandNameEn)
		explicit = false
	}
	if len(names) == 0 && !explicit {
		return // 未填写任何品牌名，不种入
	}
	// 固定写法持久化三列（★ F3-a：brand_name 此前只在后台保存时写，注册/建租户恒空，
	// 品牌页中文名框因此灰显占位符「能言 LangCross」）；显式清空时这里同样会写空，语义一致。
	if b, e := json.Marshal(names); e == nil {
		_ = s.Ten.SetBrandNames(tid, string(b))
	}
	_ = s.Ten.SetBrandNameEn(tid, names["en"])
	_ = s.Ten.SetBrandName(tid, names["zh"])
	// 企业包 L1 术语种入（幂等）；空档不清术语（旧行为：没填品牌名就不动已有术语）
	if len(names) > 0 {
		_ = s.Store.SeedBrandTerms(tid, names)
	}
}

// handleInviteCodes 邀请码列表接口（tenant_admin 及以上；企业用户仅看本租户）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（需 tenant_admin 权限）。
// 返回: success=true 时携带 codes 数组；非超管仅返回当前生效租户（企业）的邀请码。
func (s *Server) handleInviteCodes(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	codes, err := s.Store.ListInviteCodes()
	if err != nil {
		// F-64②：邀请码列表读失败＝存储层故障 → 500。旧 200 壳会让「邀请码」面板
		// 渲染成空列表（`if (r.success)` 不成立时连错误提示都不弹），像「一个码都没有」。
		// 这里也不取 401：权限判定在上一行 requireTenantAdmin 已经做完（不过就是 403），
		// 走到这一步的管理员是实打实的登录态，401 会让前端 core.ts 清 token 把他踢回登录页。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// 企业用户（非超管）仅可管理本企业邀请码：按当前生效租户过滤
	if !auth.IsSuperAdmin(u) {
		kept := codes[:0]
		for _, c := range codes {
			if c.TenantID == u.TenantID {
				kept = append(kept, c)
			}
		}
		codes = kept
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "codes": codes})
}

// handleInviteCodeCreate 创建邀请码接口（tenant_admin 及以上；企业用户仅可绑定本企业）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 code/tenant_id）。
// 返回: success=true 时携带新建邀请码对象。
func (s *Server) handleInviteCodeCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		Code     string `json:"code"`      // 邀请码字符串（必填）
		TenantID int64  `json:"tenant_id"` // 0=新建租户；>0=绑定已有租户（受邀加入）；企业用户强制绑定本企业
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "邀请码不能为空"))
		return
	}
	// 企业用户（非超管）创建邀请码只能绑定本企业，忽略请求体中的 tenant_id
	tid := req.TenantID
	if !auth.IsSuperAdmin(u) {
		tid = u.TenantID
	}
	c, err := s.Store.CreateInviteCode(strings.TrimSpace(req.Code), tid)
	if err != nil {
		// F-64②：取码分两种，别一刀切——
		//   ① invite_codes.code 列带 UNIQUE 约束（store.go 建表语句），管理员重复录入同一个码
		//      才是这里的常态失败 → 409 CONFLICT（与既有记录状态冲突，前端可据此直接
		//      把光标弹回码输入框，而不是当成「系统坏了」放弃）；
		//   ② 非撞约束的失败（连接断、权限表写不进）＝真故障 → 500，不能伪装成业务冲突。
		// ★ 两种都不取 401：requireTenantAdmin 已在函数入口判过权限，此刻人是登录态，
		//   前端 core.ts 的 401 拦截（豁免名单只有 /api/auth/login 与 /api/auth/register）
		//   会清掉 token 把管理员从后台踢回登录页。
		// 文案照旧搬 err.Error()（本批不改文案，见配方边界 5；脱敏另案处理）。
		errCode := apierrors.ErrInternal
		if store.IsUniqueViolation(err) {
			errCode = apierrors.ErrConflict
		}
		s.writeError(w, r, apierrors.New(errCode, "创建失败: "+err.Error()))
		return
	}
	// 创建审计
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "invite_create", "invite", req.Code)
	writeJSON(w, 200, map[string]interface{}{"success": true, "code": c})
}

// handleInviteCodeRevoke 撤销邀请码接口（tenant_admin 及以上；企业用户仅可撤销本企业码）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id 或 code）。
// 返回: success=true 时携带 revoked=true 与新 status。已使用的码不可撤销（人已经进来了）。
//
// ★ G9（2026-10-19）：撤销是状态位，**绝不删行**——used_by/used_at 是「谁通过这枚码进来」的
// 唯一追溯资产，删行会让客户来问「我发的码被谁用了」时无从查起。撤销后注册口必须拒收该码
// 且**不得烧码**（拒绝发生在 MarkInviteCodeUsed 之前，见 handleRegister 的既有顺序）。
func (s *Server) handleInviteCodeRevoke(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		ID   int64  `json:"id"`   // 邀请码主键 ID（优先）
		Code string `json:"code"` // 邀请码字符串（id 缺失时按码定位）
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	// 允许 query 传参（老缓存包/手写脚本可能不带 JSON body，与公开接口软档口径一致）
	if req.ID <= 0 {
		if idStr := strings.TrimSpace(r.URL.Query().Get("id")); idStr != "" {
			req.ID, _ = strconv.ParseInt(idStr, 10, 64)
		}
	}
	if strings.TrimSpace(req.Code) == "" {
		req.Code = r.URL.Query().Get("code")
	}
	if req.ID <= 0 && strings.TrimSpace(req.Code) == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请提供邀请码 id 或 code"))
		return
	}
	// 定位目标码：id 优先，其次 code。
	var inv *store.InviteCode
	var gerr error
	if req.ID > 0 {
		inv, gerr = s.Store.GetInviteCode(req.ID)
	} else {
		inv, gerr = s.Store.GetInviteCodeByCode(strings.TrimSpace(req.Code))
	}
	if gerr != nil || inv == nil {
		// 不存在与跨租户都回 404（不泄露他租户码的存在性，与 handleTenantBrandingSet 同口径）
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "邀请码不存在"))
		return
	}
	// 企业用户（非超管）只能撤销本租户名下的码。
	if !auth.IsSuperAdmin(u) && inv.TenantID != u.TenantID {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "邀请码不存在"))
		return
	}
	ok, used, rerr := s.Store.RevokeInviteCode(inv.ID)
	if rerr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), rerr)))
		return
	}
	if !ok && used {
		// 已使用：撤销没有意义（用户已经加入），给可行动文案而不是含糊的「撤销失败」
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "该邀请码已被使用，无法撤销"))
		return
	}
	// ok=true 真撤销；ok=false 且未使用＝此前已撤销（幂等）。两者都按撤销态返回，并留审计。
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "invite_revoke", "invite", inv.Code)
	writeJSON(w, 200, map[string]interface{}{"success": true, "revoked": true, "status": "revoked"})
}
