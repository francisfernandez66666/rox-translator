// ============================================================================
// components/Login.tsx — 登录 / 自助注册（LangCross 纯黑组件库实现）。
// ★ 2026-10-21 IAM 重做：公共找回/重置已下线（410 GONE），本文件不再有找回屏；
//   「忘记密码」不再是站内出路，改由 OTP/口令切换（usePassword/useCode）承担。
// 设计：认证卡用 AuthCard（400 宽、内边距 32、圆角 14、外层 .lc-auth-bg）。
// 几何口径来自画布 57:3（登录）/ 57:117（找回密码）实测；三屏标题字号见 components.css 注释。
// 注册交互：点「免费注册」→ 传统表单卡闪烁三次 → 整页 120ms 退出 → AI 助理面板接管
//          （AiRegisterFlow.tsx，交付包 §5 / demo-register-ai-motion.html）。
// 业务逻辑与 API 调用（登录/注册/验证码/找回密码/首登改密/Turnstile/UTM）原样保留。
// ★ 2026-09-27 F-71 实装：四屏的外层壳由裸 `<div className="lc-auth-bg">` 换成 `BrandLoginShell`
//   （见 branding.tsx），租户配了背景图/布局时按「全屏＝背景+遮罩+卡片定位」「分栏＝一侧图一侧容器」
//   实际渲染；没配（平台根域名恒为此态）时壳只输出原来那个 div，公开页与交付稿零变化。
//   锁：src/components/Login.brand_login.dom.test.tsx（默认形态零变化 + 两形态等值断言）。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import {
  AuthCard, Field, Input, Button, Checkbox, Dialog, useToast, EyeIcon, EyeOffIcon,
} from '@/ui/langcross/src'
import { readUtm, clearUtm } from '@/utils/utm'
import {
  login, loginWithEmail, profileSetup, authRegister, sendEmailCode, registerConfig,
  changePassword,
  setAuthToken, setActiveTenantId,
  ssoProviders, ssoLoginUrl, type AuthUser, type SSOProvider,
} from '@/api'
import { t, useT } from '@/i18n'
import { LangSelect } from '@/components/LangSelect' // ★ #23：12 语种语言下拉
// ★ 2026-10-22：密码可见切换的唯一实现挪到独立文件（原注释声称"四处共用"，
//   而强制改密窗 import 不到它 ⇒ 那个必过的弹窗三个密码框全是盲输）。
import PwdEye from '@/components/PwdEye'
import { useBranding, BrandLoginShell, DEFAULT_BRAND_NAME } from '@/branding'
import { roleLevel } from '@/stores/auth'
import { industryCodeOf, industryOptions } from '@/lib/industries'
import { PERSONA_FALLBACK, personaName } from '@/lib/personas'
import { useCountdown } from '@/lib/useCountdown' // ★ #42：验证码冷却收口（自带卸载清理 + 幂等重启）
// ★ 2026-10-21 IAM 重做 P0-6：邮箱形状与口令长度走这一把共用尺子（只判形状，不判"注册过没有"）
import { PWD_MIN_LEN, isEmailShape } from '@/lib/accountForm'
import { loadTurnstile, renderTurnstile as mountTurnstileWidget, reexecTurnstile } from '@/lib/turnstile'
import AiRegisterFlow from './AiRegisterFlow'

/** Login 入参：mode 区分前台(home)/后台(admin)登录；onLogin 登录成功后回调上层挂载工作台 */
interface Props {
  mode: 'home' | 'admin'
  onLogin: (u: AuthUser) => void
}

// 注册接管阶段：form 传统表单 → flashing 闪烁三次 → exiting 整页退出 → ai 由 AI 助理接管
type RegPhase = 'form' | 'flashing' | 'exiting' | 'ai'

/**
 * 登录 / 自助注册 两屏合一页（等价 Vue 版 Login.vue）。
 * 2026-09-18 改造：整层皮肤从 TDesign 迁到 LangCross 组件库（AuthCard/Field/Input/useToast），
 * 鉴权链路（登录、注册、邮箱码、首登强制改密、Turnstile、UTM 归因）原样保留。
 * ★ 2026-10-21 IAM 重做：找回/重置屏随公开找回下线删除（410 GONE），密碼降为备用登录方式，
 * 主入口是邮箱验证码（登录卡内 usePassword/useCode 切换）；「忘记密码」不再是站内出路。
 * 各屏按 view 提前 return 渲染各自的卡，不复用同一张：设计稿里两屏的卡宽、标题字号、
 * 底部链接行数都不同，共用一张只会把样式分支写成嵌套三元。
 */
export default function Login({ mode, onLogin }: Props) {
  const [lang, , tplF] = useT() // tplF：带 {n} 占位的模板取词（验证码倒计时「{n}s 后重发」用）
  const { toast } = useToast() // 迁移口径：原 TDesign MessagePlugin.warning/error 全部换成组件库 toast
  const branding = useBranding()
  // ★ 2026-10-22 统一登录批：「统一登录」落在主站（根域名）。
  //   unified=true 时：没有注册分支（自助注册钮隐退、/register 与 ?ref= 不再自动展开注册），
  //   邮箱验证通过即注册登录（未注册邮箱由后端自动建空白账号），字段补齐可见标签，
  //   首登资料提醒交给根级 ProfileSetupGate 的 AI 完善面板。
  //   门控信号与 ProfileSetupGate 同源：tenantId===0＝平台根域名＝主站；
  //   租户子域/演示站（tenantId>0）一切照旧——这一批改动的红线就是「只影响主站」。
  // ★ 2026-10-23（用户令「主站管理后台退出以后还保留着注册，应该删除，保留和主站的统一登录
  //   同源逻辑」）：`mode` 不再参与判定——主站的**后台登录也是主站登录**，同一张卡不该因为
  //   从 `/admin` 退出就换一副面孔（旧写法只认 `mode==='home'`，于是后台退出后
  //   「没有账号？自助注册试用」+ otpDesc 段落 + 「注册邮箱」placeholder 全部原样回来）。
  //   主站既然已经取消注册分支，后台登录更没有理由留一个注册入口——注册出来的空白账号
  //   在 applyLogin 里本来也会被 `roleLevel < 2` 弹回去（等于把人送进死路）。
  //   租户子域/演示站（tenantId>0）仍走旧形态，一字不动。
  const unified = branding.tenantId === 0
  // 邀请裂变个人码（?ref=），登录页与注册提交共用
  // 提到组件顶层而非在提交前现取：注册表单、传统注册提交、AI 接管流三处都要读；
  // 认证页存活期间地址栏 query 不变，故不必 useMemo
  const ref = new URLSearchParams(window.location.search).get('ref') || ''
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  // ★ 2026-10-21 IAM 重做：邮箱验证码登录是主流程；密码降为备用登录方式。
  const [loginMode, setLoginMode] = useState<'otp' | 'password'>('otp')
  const [loginEmail, setLoginEmail] = useState('')
  const [loginCode, setLoginCode] = useState('')
  const [loginAgreed, setLoginAgreed] = useState(false)
  const [loginCaptchaToken, setLoginCaptchaToken] = useState('')
  // 首登资料提醒（可跳过；完成/跳过后才 onLogin 进工作台）
  const [setupUser, setSetupUser] = useState<AuthUser | null>(null)
  const [setupName, setSetupName] = useState('')
  const [setupPwd, setSetupPwd] = useState('')
  const [setupBusy, setSetupBusy] = useState(false)
  const [setupMsg, setSetupMsg] = useState('')
  const [showPwd, setShowPwd] = useState(false) // 密码明文/密文切换（画布 57:8 的眼睛按钮）
  // ★ 2026-10-21 IAM 重做 P1-3：注册屏与找回屏的新密码各配一枚眼睛。
  //   登录屏早就有这枚按钮，而「注册时两次打错同一个字符」恰恰是最需要看得见的一次输入；
  //   找回屏同理——验证码邮件里抄出来的口令看不见，用户只能盲输。
  const [showRegPwd, setShowRegPwd] = useState(false)
  // ★ 2026-10-21 IAM 重做 P1-2：这一份读数**现在参与渲染**了。
  //   旧注释写的是「迁移后 AuthCard 的通栏提交按钮不暴露 loading 属性，loading 不再参与渲染」——
  //   那正是缺陷本体而不是设计：请求在飞的三秒里提交钮照常可点，
  //   注册那一腿连点两下就是两次请求，第二次必吃「该邮箱已被其他账号绑定」，
  //   用户看到的是"我点了注册，它报错"。现在 AuthCard 有 busy 档（复用交付档 .lc-btn:disabled，零新视觉）。
  //   setter 的名字与时序一字不改（各异步函数仍是「前置位 / finally 复位」）。
  const [loading, setLoading] = useState(false)
  // 登录 / 注册 / 找回 三类错误提示
  const [error, setError] = useState('')
  const [regMsg, setRegMsg] = useState('')
  // 首登强制改密弹窗
  const [forceOpen, setForceOpen] = useState(false)
  const [forcePending, setForcePending] = useState<AuthUser | null>(null)
  const [forceOld, setForceOld] = useState('')
  const [forceNew, setForceNew] = useState('')
  const [forceConfirm, setForceConfirm] = useState('')
  const [forceMsg, setForceMsg] = useState('')
  const [forceBusy, setForceBusy] = useState(false)
  // 视图与注册接管阶段
  const [view, setView] = useState<'signin' | 'register'>('signin')
  const [regPhase, setRegPhase] = useState<RegPhase>('form')
  // ★ 2026-10-21 IAM 重做 P1-8：是否处于「减少动效」偏好。
  //   旧形态在减少动效下直接 return，整段接管流程连入口一起消失——用户被永久钉在 9 项传统表单上，
  //   交付稿里那个"聊两句就注册完"的流程对这批用户**根本不存在**。现在只停动画、保留入口按钮。
  const [reducedMotion, setReducedMotion] = useState(false)
  const [typeChoice, setTypeChoice] = useState<'personal' | 'enterprise'>('personal')
  // 企业用户角色：admin=我是管理员（新建企业）；member=我是普通成员（须凭企业邀请码加入）
  const [roleChoice, setRoleChoice] = useState<'admin' | 'member'>('admin')
  // 注册表单聚合到一个对象：字段随「个人/企业 × 管理员/成员」分支增减，散成 9 个 useState 只会让重置更难维护
  // orgEn（组织英文名）为本次设计稿新增字段，后端暂无独立列，只在表单里占位不提交
  const [form, setForm] = useState({ code: '', name: '', invite: '', email: '', emailCode: '', industry: '', jobRole: '', brandName: '', brandNameEn: '', orgEn: '' })
  const [agreed, setAgreed] = useState(false)
  // 邮箱验证 / 人机验证两个开关由 registerConfig 下发（后台可关），不能前端写死
  const [emailVerifyOn, setEmailVerifyOn] = useState(false)
  const [captchaOn, setCaptchaOn] = useState(false)
  // 2026-09-10 行业字典动态化：超管在后台「行业管理」维护，注册下拉优先用后端返回值，空则回落本地词库
  const [industries, setIndustries] = useState<Array<{ code: string; name: string }>>([])
  // 2026-09-19 职业角色字典：与行业同口径由 registerConfig 下发（租户0 persona 包），
  // 角色只绑用户不绑企业——个人/企业所有分支都展示同一下拉
  const [personas, setPersonas] = useState<Array<{ code: string; name: string }>>(PERSONA_FALLBACK)
  // ★ #42：验证码冷却改走 useCountdown（旧写法 setInterval 的句柄只活在闭包里：
  //   离开登录页时定时器不会被清，且二次触发会并存两个递减源把 60s 冷却打成 30s）
  const codeCd = useCountdown(60)
  const captchaBoxRef = useRef<HTMLDivElement>(null) // Turnstile 挂载容器（脚本会往里塞 iframe）
  const captchaTokenRef = useRef('') // token 用 ref 而非 state：回调写入时不需要触发重渲染
  const captchaIdRef = useRef<string | number | null>(null) // widget id：token 消费后 reexec 领新码要用
  const loginCaptchaBoxRef = useRef<HTMLDivElement>(null)
  const loginCaptchaIdRef = useRef<string | number | null>(null)
  // ★ 2026-09-24：siteKey 存 state——AI 注册面板（接管注册的传统表单替代者）要拿同一份配置
  //   挂自己的验证组件，旧写法只活在注册期局部变量里，面板无从获得
  const [captchaSiteKey, setCaptchaSiteKey] = useState('')
  // 已启用的 IdP 列表；空数组=后端未接 SSO，登录卡不渲染第三方登录区块
  const [ssoList, setSsoList] = useState<SSOProvider[]>([])

  // 拉取 SSO 身份源：仅前台登录卡展示（回调固定落 SSO_FRONTEND_URL 根路径，后台卡跳过去会回不到 /admin）
  useEffect(() => {
    if (mode !== 'home') return
    // 查询失败（网络抖动 / 老后端无该端点）按「未启用 SSO」处理：登录主链路不能被辅助入口的可用性拖住
    void ssoProviders().then((r) => { if (r?.enabled) setSsoList(r.providers || []) }).catch(() => { /* 静默 */ })
  }, [mode])

  // 进入 /register 或带 ?ref= 自动展开注册并捕获邀请码
  // ★ 2026-10-22 统一登录批：主站没有注册分支了——/register 与 ?ref= 不再展开注册屏，
  //   统一登录下「新账号验证通过即注册」，邀请裂变码改由登录后的 AI 完善流承接。
  //   （租户/演示站与后台登录页不受影响：unified=false 时行为逐字不变。）
  useEffect(() => {
    try {
      if (mode === 'home' && !unified && (window.location.pathname === '/register' || ref)) setView('register')
      // ref（好友裂变个人码）刻意不写进 invite 输入框：invite 是「企业邀请码」，两者语义不同，
      // 混填会让用户以为已经带了好友码（实际由 doRegister 的 ref 字段单独上报）
      if (ref) setForm((f) => ({ ...f, invite: f.invite || '' }))
    } catch { /* ignore */ }
  }, [mode, ref, unified])

  // 仅前台加载注册配置（邮箱验证 / 人机验证 / 行业字典）
  useEffect(() => {
    if (mode !== 'home') return
    ;(async () => {
      try {
        const c = await registerConfig()
        if (c.success) {
          setEmailVerifyOn(!!c.email_verify_enabled)
          setCaptchaOn(!!(c as unknown as { captcha_enabled?: boolean }).captcha_enabled)
          const key = (c as unknown as { captcha_site_key?: string }).captcha_site_key || ''
          if ((c as unknown as { captcha_enabled?: boolean }).captcha_enabled) setCaptchaSiteKey(key)
          if ((c as unknown as { captcha_enabled?: boolean }).captcha_enabled && key) mountCaptcha(key)
          const inds = (c as unknown as { industries?: Array<{ code: string; name: string }> }).industries
          if (Array.isArray(inds) && inds.length > 0) setIndustries(inds)
          const ps = (c as unknown as { personas?: Array<{ code: string; name: string }> }).personas
          if (Array.isArray(ps) && ps.length > 0) setPersonas(ps)
        }
      } catch { /* ignore */ }
    })()
  }, [mode])

  // 注册屏：进入后传统表单闪烁三次 → 整页 120ms 退出 → AI 接管（尊重 prefers-reduced-motion）
  useEffect(() => {
    if (view !== 'register' || mode !== 'home') return
    // matchMedia 必须带守卫：jsdom / 老旧 WebView 没有 window.matchMedia，裸调会在 effect 阶段直接抛错
    const reduced = typeof window !== 'undefined' && !!window.matchMedia
      && window.matchMedia('(prefers-reduced-motion: reduce)').matches
    setRegPhase('form')
    setReducedMotion(reduced)
    // ★ P1-8：减少动效时**只停动画**，不再连入口一起砍——卡尾会多出一枚「AI 引导注册」按钮，
    //   点它同样进 AI 面板（面板自己的入场动画也由组件按同一偏好关掉）。旧形态 `return` 之后
    //   这批用户永远看不到 AI 流程，只能手填 9 项。
    if (reduced) return
    // 三段节拍对齐 demo-register-ai-motion.html：
    // 1300ms 让传统表单先站稳（观众得看清「原来要填 9 项」）→ 920ms 闪烁动画（= .auth-flash 时长）
    // → 130ms 覆盖 120ms 整页退出动画后再挂 AI 面板，中间不留「空白闪一下」的缝
    const id1 = window.setTimeout(() => setRegPhase('flashing'), 1300)
    const id2 = window.setTimeout(() => setRegPhase('exiting'), 1300 + 920)
    const id3 = window.setTimeout(() => setRegPhase('ai'), 1300 + 920 + 130)
    return () => { window.clearTimeout(id1); window.clearTimeout(id2); window.clearTimeout(id3) }
  }, [view, mode])

  // F-07（2026-09-25 发布前 UAT）：AI 接管会把传统表单整支卸载，Turnstile 挂件随 DOM 一起销毁；
  // 点 × 退回后容器重挂成空壳，但 mountCaptcha 只在启动配置回调里调过一次（133 行）——
  // 结果 captchaTokenRef 永远为空，「发送验证码」连请求都不发、注册静默拒绝，且页面没有任何可操作的验证框。
  // 兜底：注册屏回到表单阶段时，若人机验证开启且容器里还没有挂件 iframe，就重新挂载一次；
  // iframe 存在性判据同时防住首帧双挂（配置回调可能先/后于本 effect 到）。
  useEffect(() => {
    if (mode !== 'home' || view !== 'register' || regPhase !== 'form') return
    if (!captchaOn || !captchaSiteKey) return
    const el = captchaBoxRef.current
    if (!el || el.querySelector('iframe')) return
    mountCaptcha(captchaSiteKey)
  }, [regPhase, view, mode, captchaOn, captchaSiteKey])

  useEffect(() => {
    if (mode !== 'home' || view !== 'signin') return
    if (!captchaOn || !captchaSiteKey) return
    const el = loginCaptchaBoxRef.current
    if (!el || el.querySelector('iframe')) return
    mountLoginCaptcha(captchaSiteKey)
  }, [view, mode, captchaOn, captchaSiteKey])

  // ---- Cloudflare Turnstile 人机验证（装载收敛到 lib/turnstile：多消费方只注入一次脚本）----
  function mountCaptcha(siteKey: string) {
    loadTurnstile(() => {
      const el = captchaBoxRef.current
      if (!el) return
      // 容器里已有挂件 iframe 就不再二挂（配置回调与 F-07 退回重挂两条路可能先后到达同一容器）
      if (el.querySelector('iframe')) return
      const id = mountTurnstileWidget(el, siteKey, (tk) => { captchaTokenRef.current = tk })
      if (id !== null) captchaIdRef.current = id
    })
  }

  // 登录屏的人机验证挂件与注册屏**各挂各的容器**：共用一个容器会在切视图时把 token 串到另一条链路。
  function mountLoginCaptcha(siteKey: string) {
    loadTurnstile(() => {
      const el = loginCaptchaBoxRef.current
      if (!el || el.querySelector('iframe')) return
      const id = mountTurnstileWidget(el, siteKey, (tk) => { setLoginCaptchaToken(tk) })
      if (id !== null) loginCaptchaIdRef.current = id
    })
  }
  // token 一次一用：任一请求（发码/注册提交）把它送出去后即作废，清空并强制重新挑战领新码
  function refreshCaptcha() {
    captchaTokenRef.current = ''
    reexecTurnstile(captchaIdRef.current)
  }

  // ---- 会话落地的唯一出口（★ 2026-10-21 IAM 重做 P0-4：登录响应与注册响应共用这一条后处理）----
  // 旧形态注册成功后调 doLogin() 再补登一次，而 doLogin 的失败原因写进 `error` 槽，
  // `error` 槽**只在登录屏渲染**（本文件登录卡那一段）⇒ 人还停在注册卡，什么提示都看不见，
  // 观感就是"注册成功了，然后什么都没发生"（运营者报的第 1 条）。
  // 现在失败文案按**发起那一屏**落位：登录屏写 error，注册屏写 regMsg。
  async function settleSession(resp: Awaited<ReturnType<typeof login>>, fail: (msg: string) => void): Promise<void> {
    if (!resp.success || !resp.token) { fail(resp.message || t('login.fail')); return }
    // 后台入口二次把关：roleLevel <2（普通成员）即便拿到 token 也不放行，防止手输 /admin 越界
    if (mode === 'admin' && roleLevel(resp.user?.role) < 2) { fail(t('login.noAdmin')); return }
    // 品牌专属域名跳转：跨子域改带一次性 sso_code，落地后由 App 经 /api/auth/sso/exchange 兑换 JWT
    if (resp.brand_host && resp.sso_code && resp.brand_host !== window.location.host) {
      const target = window.location.protocol + '//' + resp.brand_host + '/?sso_code=' + encodeURIComponent(resp.sso_code)
      window.location.replace(target) // replace 而非 assign：不留历史记录，后退不会再回到跳走前的登录页
      return
    }
    setAuthToken(resp.token)
    setActiveTenantId(0) // 先落在平台默认租户；进后台后由 stores/admin 的 switchTenant 改写 X-Tenant-ID
    const u = resp.user!
    // 首登强制改密（2026-09-02）：管理员批量导入的账号，初始密码可能进过导出文件，
    // 必须先改掉才能进系统 —— 此处只弹窗、不回调 onLogin
    if ((u as unknown as { must_change_pwd?: number }).must_change_pwd) {
      setForcePending(u); setForceOld(''); setForceNew(''); setForceConfirm(''); setForceMsg('')
      setForceOpen(true)
      return
    }
    // ★ 首登资料提醒：只提醒、不阻断——用户可跳过，下次登录仍会提醒（profile_setup_required 不动）。
    //   ★ 2026-10-22 统一登录批：主站（unified）不再走本屏内联对话框——补全交给根级
    //   ProfileSetupGate 的 AI 完善面板（登录与会话恢复共用，刷新后同样提醒）。
    //   这里直接 onLogin，闸门随后依 user.profile_setup_required 自行弹出。
    if (!unified && (u as unknown as { profile_setup_required?: number }).profile_setup_required) {
      setSetupUser(u); setSetupName(''); setSetupPwd(''); setSetupMsg('')
      return
    }
    onLogin(u)
  }

  // ---- 登录：校验 → 调登录接口 → 交 settleSession（角色/跳转/写 token/首登改密/回调）----
  async function doLogin() {
    setError('')
    if (loginMode === 'otp') {
      if (!loginEmail.trim()) { setError(t('auth.emailRequired')); return }
      if (!isEmailShape(loginEmail)) { setError(t('auth.emailBad')); return }
      if (!loginCode.trim()) { setError(t('auth.codeRequired')); return }
      if (!loginAgreed) { setError(t('auth.needAgree')); return }
      if (captchaOn && !loginCaptchaToken) { setError(t('auth.captchaRequired')); return }
      setLoading(true)
      try {
        const resp = await loginWithEmail(loginEmail.trim(), loginCode.trim(), { agreed: loginAgreed, captchaToken: loginCaptchaToken })
        await settleSession(resp, setError)
      } catch (e: unknown) {
        setError((e as { message?: string }).message || t('login.fail'))
      } finally { setLoading(false) }
      return
    }
    if (!username || !password) { setError(t('auth.needInput')); return }
    setLoading(true)
    try {
      const resp = await login(username, password)
      await settleSession(resp, setError)
    } catch (e: unknown) {
      // 凭证错误等会以 401/404 抛出：必须把后端 message 透出，否则用户只看到「点了没反应」
      setError((e as { message?: string }).message || t('login.fail'))
    } finally { setLoading(false) }
  }

  // 登录验证码：用途必须与登录同档（purpose=login），否则会和注册码互相覆盖。
  async function doSendLoginCode() {
    if (!loginEmail.trim()) { setError(t('auth.emailRequired')); return }
    if (!isEmailShape(loginEmail)) { setError(t('auth.emailBad')); return }
    if (captchaOn && !loginCaptchaToken) { setError(t('auth.captchaRequired')); return }
    setLoading(true); setError('')
    try {
      const r = await sendEmailCode(loginEmail.trim(), loginCaptchaToken || undefined, 'login')
      if (r.success) {
        toast({ title: r.noop ? t('pwd.codeNoop') : t('pwd.codeSent'), tone: 'success' })
        codeCd.start()
      } else setError(r.message || t('pwd.sendFail'))
    } catch (e) {
      setError((e as Error).message || t('pwd.sendFail'))
    } finally {
      if (captchaOn) { setLoginCaptchaToken(''); reexecTurnstile(loginCaptchaIdRef.current) }
      setLoading(false)
    }
  }

  // 首登资料：完成或跳过都重签 token，然后才进工作台。
  async function finishSetup(skip: boolean) {
    if (!setupUser) return
    if (!skip) {
      if (!setupName.trim()) { setSetupMsg(t('auth.username')); return }
      if (setupPwd && setupPwd.length < PWD_MIN_LEN) { setSetupMsg(t('pwd.tooShort')); return }
    }
    setSetupBusy(true); setSetupMsg('')
    try {
      const r = await profileSetup({ skip, username: skip ? undefined : setupName.trim(), password: skip ? undefined : (setupPwd || undefined) })
      if (!r.success) { setSetupMsg(r.message || t('login.fail')); return }
      if (typeof r.token === 'string' && r.token) setAuthToken(r.token)
      const u = (r.user && typeof r.user === 'object') ? (r.user as AuthUser) : { ...setupUser, profile_setup_required: 0 }
      setSetupUser(null)
      onLogin(u)
    } catch (e) {
      setSetupMsg((e as Error).message || t('login.fail'))
    } finally { setSetupBusy(false) }
  }

  // ---- 首登强制改密：校验新密码长度与一致 → changePassword → 成功 toast 并 onLogin ----
  // ★ 2026-10-21 IAM 重做 P1-1：长度判据走 `PWD_MIN_LEN`（与注册/后端 ValidatePassword 同一把尺子）。
  //   旧形态在这里写死 `< 6`，后端一旦调档（比如抬到 8）就会变成"前端放行、后端 400"的假失败。
  async function submitForcePwd() {
    if (!forceOld || forceNew.length < PWD_MIN_LEN) { setForceMsg(t('pwd.tooShort')); return }
    if (forceNew !== forceConfirm) { setForceMsg(t('pwd.mismatch')); return }
    setForceBusy(true); setForceMsg('')
    try {
      const r = await changePassword(forceOld, forceNew)
      if (!r.success) { setForceMsg(r.message || t('auth.forcePwdFail')); return }
      toast({ title: t('pwd.done'), tone: 'success' })
      setForceOpen(false)
      // ★ 2026-10-21 IAM 重做 P0-9：改密成功会抬 token_version，服务端随响应重签一条新 token。
      //   旧形态丢掉它、拿手里那条旧 token 直接 onLogin ⇒ 下一个请求就被判「会话已撤销」踢回登录页
      //   （首登引导刚走完又要再登一次，用户报障第 3 条）。现在先落地新 token，再进工作台；
      //   响应没带 token（重签失败那一档）就如实回落——清掉本地会话让用户重新登录，语义仍是正确的。
      if (typeof r.token === 'string' && r.token) {
        setAuthToken(r.token)
        setActiveTenantId(0)
        const u = (r.user && typeof r.user === 'object') ? (r.user as AuthUser) : forcePending
        setForcePending(null)
        if (u) onLogin(u)
        return
      }
      setAuthToken('')
      setForcePending(null)
      setError(t('auth.sessionExpired'))
    } catch (e) { toast({ title: (e as Error).message || t('auth.forcePwdFail'), tone: 'error' }) }
    finally { setForceBusy(false) }
  }

  // ---- 发送邮箱验证码：校验邮箱与人机验证 → 调接口 → 60s 倒计时冷却 ----
  async function doSendCode() {
    if (!form.email.trim()) { toast({ title: t('auth.emailRequired'), tone: 'warn' }); return }
    // ★ P0-6：发码前先判形状——把码发到一串根本不是地址的东西上，等于替用户制造一次空等
    if (!isEmailShape(form.email)) { toast({ title: t('auth.emailBad'), tone: 'warn' }); return }
    if (captchaOn && !captchaTokenRef.current) { toast({ title: t('auth.captchaRequired'), tone: 'warn' }); return }
    let r!: Awaited<ReturnType<typeof sendEmailCode>>
    try {
      r = await sendEmailCode(form.email.trim(), captchaTokenRef.current || undefined)
    } catch (e) { toast({ title: (e as Error).message || t('auth.sendCodeFail'), tone: 'error' }); return } // E10：网络/超时必须显式提示，早先是 unhandled rejection（用户只见无响应）
    finally {
      // ★ 2026-09-24：本次请求已把 token 消费掉（成败都 consumed），旧码留着只会在
      //   下一步「提交注册」时被复用并吃一个人机验证 403——发码后立即清空并重新领码
      if (captchaOn) refreshCaptcha()
    }
    if (r.success) {
      toast({ title: r.noop ? t('pwd.codeNoop') : t('pwd.codeSent'), tone: 'success' })
      codeCd.start() // 冷却 60s（到 0 自动停；组件卸载即停，见 lib/useCountdown.ts）
    } else toast({ title: r.message || t('pwd.sendFail'), tone: 'error' })
  }

  // ---- 传统表单注册（AI 接管前的「前身」形态，仍可手动提交）----
  // 迁移附带修正：这批提示原先是硬编码中文（如「请填写邮箱并输入验证码」），现全量走 auth.* 键，
  // 英文环境不再露出中文；校验按表单自上而下顺序、命中即返回，一次只提示一条。
  async function doRegister() {
    if (!username || !password) { setRegMsg(t('auth.needInput')); return }
    if (password.length < PWD_MIN_LEN) { setRegMsg(t('auth.pwdShort')); return }
    if (!agreed) { setRegMsg(t('auth.needAgree')); return }
    if (!form.email.trim()) { setRegMsg(t('auth.emailRequired')); return }
    // ★ P0-6：形状不对当场出声（旧形态只判非空，写漏 @ 要等"验证码错误"那一步才发现）
    if (!isEmailShape(form.email)) { setRegMsg(t('auth.emailBad')); return }
    if (emailVerifyOn && (!form.emailCode.trim() || !form.email.trim())) { setRegMsg(t('auth.codeRequired')); return }
    if (!branding.dedicatedRegister && typeChoice === 'enterprise' && !form.code.trim()) { setRegMsg(t('auth.orgCodeRequired')); return }
    if (!branding.dedicatedRegister && typeChoice === 'enterprise' && !form.name.trim()) { setRegMsg(t('auth.orgNameRequired')); return }
    if (captchaOn && !captchaTokenRef.current) { setRegMsg(t('auth.captchaRequired')); return }
    const regType = (!branding.dedicatedRegister ? typeChoice : 'enterprise')
    // 个人用户：好友邀请码经 ref 走邀请裂变；企业用户：普通成员须凭企业邀请码加入
    // 专属域名（dedicatedRegister）下只允许企业注册，且邀请码无条件必填
    const entInvite = branding.dedicatedRegister
      ? (form.invite.trim() || undefined)
      : (regType === 'enterprise' && roleChoice === 'member' ? (form.invite.trim() || undefined) : undefined)
    if (branding.dedicatedRegister && !entInvite) { setRegMsg(t('auth.dedicatedInviteRequired')); return }
    if (regType === 'enterprise' && roleChoice === 'member' && !entInvite) { setRegMsg(t('auth.inviteRequired')); return }
    setLoading(true); setRegMsg('')
    try {
      const r = await authRegister({
        username, password,
        type: regType,
        code: (regType === 'enterprise' ? (form.code || undefined) : undefined),
        name: (regType === 'enterprise' ? (form.name || undefined) : undefined),
        invite: entInvite,
        role_choice: (regType === 'enterprise' ? roleChoice : undefined),
        email: form.email.trim() || undefined,
        email_code: form.emailCode || undefined,
        captcha_token: captchaTokenRef.current || undefined,
        industry: (regType === 'enterprise' ? (form.industry ? industryCodeOf(form.industry) : undefined) : undefined),
        // job_role 不带分支条件：角色绑用户不绑企业，个人/企业管理员/成员注册时都生效
        job_role: form.jobRole || undefined,
        brand_name: (regType === 'enterprise' && roleChoice === 'admin' ? (form.brandName.trim() || undefined) : undefined),
        brand_name_en: (regType === 'enterprise' && roleChoice === 'admin' ? (form.brandNameEn.trim() || undefined) : undefined),
        ref,
        agreed,
        landing_path: window.location.pathname, ...readUtm(), // S4 归因：落地页捕获的 UTM 随注册一次性上报
      })
      // 失败重试要用新 token：本次提交已把旧码消费掉（服务端 siteverify 一次一用），
      // 不重领的话，用户改完必填项再点提交必吃「人机验证失败」
      if (!r.success) { setRegMsg(r.message || t('register.fail')); if (captchaOn) refreshCaptcha(); return }
      clearUtm() // 归因只在首次注册消费，用完立即清掉，避免二次注册串号
      // ★ 2026-10-21 IAM 重做 P0-4（注册即登录）：服务端现在随注册响应直接下发会话，
      //   前端不再"拿明文账号密码回登一次"。旧补登有两处结构性失败：
      //   ① 当时的登录只认用户名，而注册表单收的是邮箱；② 注册把口令 trim 过、登录不 trim。
      //   兜档（服务端签发失败那一档，注册**仍算成功**）才回落到补登，
      //   且失败原因经 settleSession 的 fail 口落到**注册屏**的 regMsg，不再写进看不见的 error 槽。
      if (typeof r.token === 'string' && r.token) {
        await settleSession(r as unknown as Awaited<ReturnType<typeof login>>, setRegMsg)
        return
      }
      await doLogin()
    } catch (e) { setRegMsg(e instanceof Error ? e.message : String(t('register.fail'))); if (captchaOn) refreshCaptcha() } // E10：异常同样要落到卡内红字
    finally { setLoading(false) }
  }

  // ====================== 渲染 ======================
  // 登录卡标题渲染品牌名而不是「登录」（画布 57:4 实测；Login.auth.dom.test ① 守这条）
  const brandName = branding.brandName || DEFAULT_BRAND_NAME

  // 卡右上角语言入口（★ #23：原 zh/en 胶囊换成 12 语种 LangSelect，三个认证屏共用）
  const langPill = <LangSelect align="right" />

  // 登录屏（卡宽 400、标题=品牌名、底部两行居中链接、密码框右端眼睛、卡右上角 LangSelect）
  // ★ 2026-10-22 统一登录批：`|| unified` 是防御性兜底——主站的 view 只能停在 'signin'
  //   （两个置 'register' 的入口都已门控），这句保证即使将来新增入口也落回登录屏，
  //   注册表单在主站永不可达。
  if (view === 'signin' || unified) {
    return (
      <BrandLoginShell>
        <AuthCard
          title={brandName}
          desc={t('auth.loginDesc')}
          submitText={t('auth.signIn')}
          onSubmit={doLogin}
          busy={loading}
          corner={langPill}
          foot={(
            <>
              {/* ★ 2026-10-21 IAM 重做：公开找回已下线，密码降为备用；主流程是邮箱验证码。 */}
              <button type="button" className="auth-link" onClick={() => { setLoginMode(loginMode === 'otp' ? 'password' : 'otp'); setError('') }}>
                {loginMode === 'otp' ? t('auth.usePassword') : t('auth.useCode')}
              </button>
              {/* ★ 2026-10-22 统一登录批：主站撤掉「没有账号？自助注册试用」——
                  统一登录下没有注册分支，新邮箱验证通过即注册。租户/演示站/后台保留原按钮。 */}
              {!unified && (
                <button type="button" className="auth-link" onClick={() => setView('register')}>{t('login.selfRegister')}</button>
              )}
            </>
          )}
        >
          {loginMode === 'otp' ? (
            <>
              {/* ★ 2026-10-22 统一登录批：otpDesc 段落（"输入邮箱，获取一次性验证码即可登录；
                  未注册的邮箱会自动创建空白账号"）在主站撤下——同一句提示改放进邮箱框
                  placeholder（auth.autoCreateHint），少一段说明文、信息不丢。
                  租户/演示站保留原段落与「注册邮箱」placeholder，逐字不动。 */}
              {!unified && <p className="auth-note" style={{ margin: '0 0 10px' }}>{t('auth.otpDesc')}</p>}
              {/* 可见字段名标签：主站四个字段全部补上（用户令「显示字段就是：邮箱/密码等字段名」），
                  不再只靠 placeholder/aria-label；租户站保持原样（只影响主站）。 */}
              <Field label={unified ? t('auth.fieldEmail') : undefined}>
                <Input aria-label={t('auth.boundEmail')} type="email" inputMode="email" autoComplete="email"
                  value={loginEmail} onChange={(e) => setLoginEmail(e.target.value)}
                  placeholder={unified ? t('auth.autoCreateHint') : t('auth.boundEmail')} />
              </Field>
              <Field label={unified ? t('auth.fieldEmailCode') : undefined}>
                <div style={{ display: 'flex', gap: 8 }}>
                  <Input style={{ flex: 1 }} aria-label={t('auth.fieldEmailCode')} inputMode="numeric"
                    value={loginCode} onChange={(e) => setLoginCode(e.target.value)} placeholder={t('auth.fieldEmailCode')}
                    onKeyDown={(e) => { if (e.key === 'Enter') doLogin() }} />
                  <Button variant="secondary" disabled={codeCd.left > 0 || loading} onClick={doSendLoginCode}>
                    {codeCd.left > 0 ? tplF('auth.codeResend', { n: codeCd.left }) : t('auth.sendCode')}
                  </Button>
                </div>
              </Field>
              {captchaOn && <div ref={loginCaptchaBoxRef} id="__ts_login_widget__" />}
              <Checkbox checked={loginAgreed} onChange={(e) => setLoginAgreed(!!e.target.checked)}
                label={(
                  <>
                    {t('auth.agreeTerms')}{''}
                    <a href="/docs/terms" target="_blank" rel="noreferrer" className="auth-link">{t('auth.userAgreement')}</a>
                    {' & '}
                    <a href="/docs/privacy" target="_blank" rel="noreferrer" className="auth-link">{t('auth.privacyPolicy')}</a>
                  </>
                )} />
            </>
          ) : (
            <>
              {/* 备用密码登录：仅用于已设置密码的存量账号与导入账号。
                  ★ 2026-10-22：主站两枚字段同样补可见标签（fieldUsername/fieldPassword）。 */}
              <Field label={unified ? t('auth.fieldUsername') : undefined}>
                <Input aria-label={t('auth.username')} value={username} onChange={(e) => setUsername(e.target.value)} placeholder={t('auth.username')} autoComplete="username" />
              </Field>
              <Field label={unified ? t('auth.fieldPassword') : undefined}>
                <Input
                  aria-label={t('auth.password')}
                  type={showPwd ? 'text' : 'password'}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder={t('auth.password')}
                  autoComplete="current-password"
                  onKeyDown={(e) => { if (e.key === 'Enter') doLogin() }}
                  trailing={(
                    <button type="button" className="lc-input-affix auth-pwd-eye" aria-pressed={showPwd} onClick={() => setShowPwd((v) => !v)} aria-label={showPwd ? t('auth.hidePwd') : t('auth.showPwd')}>
                      {showPwd ? <EyeOffIcon /> : <EyeIcon />}
                    </button>
                  )}
                />
              </Field>
            </>
          )}
          {!!error && <div className="auth-err">{error}</div>}
          {/* ★ #38：第三方身份源登录入口。用 <a href> 而非按钮 + location.assign——
              /api/sso/login 回 302 到 IdP 且要下发 state cookie，fetch 会把整条重定向链吃掉；
              原生链接还顺带保住中键新开、复制链接这些浏览器默认能力。 */}
          {ssoList.length > 0 && (
            <div className="auth-sso">
              <div className="auth-sso-sep"><span>{t('auth.orSso')}</span></div>
              {ssoList.map((p) => (
                <a key={p.name} className="lc-btn lc-btn--secondary" href={ssoLoginUrl(p.name)}>
                  {tplF('auth.loginWith', { name: p.display_name || p.name })}
                </a>
              ))}
            </div>
          )}
        </AuthCard>
        <ForcePwdDialog open={forceOpen} msg={forceMsg} busy={forceBusy} old={forceOld} newP={forceNew} confirm={forceConfirm}
          onOld={setForceOld} onNew={setForceNew} onConfirmPwd={setForceConfirm} onSubmit={submitForcePwd}
          onCancel={() => { if (!forceBusy) setForceOpen(false) }} />
        {/* ★ 首登资料提醒：可跳过，不是强制闸。取消＝跳过（下次登录继续提醒）。 */}
        <Dialog open={!!setupUser} title={t('auth.profileTitle')} confirmText={t('auth.profileDone')}
          onConfirm={() => void finishSetup(false)} onCancel={() => void finishSetup(true)} dismissOnOverlay={!setupBusy}>
          <p style={{ fontSize: 15, color: 'var(--lc-text-3)', lineHeight: 1.7, margin: '0 0 14px' }}>{t('auth.profileDesc')}</p>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <Field label={t('auth.username')}>
              <Input value={setupName} onChange={(e) => setSetupName(e.target.value)} placeholder={t('auth.username')} autoComplete="username" />
            </Field>
            <Field label={t('auth.passwordOptional')}>
              <Input type="password" autoComplete="new-password" value={setupPwd} onChange={(e) => setSetupPwd(e.target.value)} placeholder={t('auth.passwordOptional')} />
            </Field>
            {!!setupMsg && <div className="auth-err">{setupMsg}</div>}
            <button type="button" className="auth-link" onClick={() => void finishSetup(true)}>{t('auth.skipForNow')}</button>
          </div>
        </Dialog>
        <style>{CSS_AUTH}</style>
      </BrandLoginShell>
    )
  }

  // 注册屏：regPhase==='ai' 由 AI 助理面板接管；否则显示传统表单卡（可手动提交）
  if (regPhase === 'ai') {
    return (
      <BrandLoginShell>
        {/* prefillUsername：已输入的用户名带进 AI 流程（对应话术「用户名我帮你带过来了」），
            是否可编辑由面板自己决定。onClose 一律退回传统表单 —— AI 接管不是单行道，
            用户随时有权自己把 9 项填完 */}
        <AiRegisterFlow
          prefillUsername={username}
          dedicatedRegister={branding.dedicatedRegister}
          captcha={{ enabled: captchaOn, siteKey: captchaSiteKey }}
          onDone={onLogin}
          onClose={() => setRegPhase('form')}
        />
        <style>{CSS_AUTH}</style>
      </BrandLoginShell>
    )
  }

  // 说明行随「个人 / 企业」切换整句文案（两张卡的必填项不同，不拼句只做二选一）
  const desc = typeChoice === 'personal' ? t('auth.personalDesc') : t('auth.enterpriseDesc')
  return (
    <BrandLoginShell>
      <AuthCard
        className={regPhase === 'flashing' ? 'auth-flash' : regPhase === 'exiting' ? 'auth-pg-out' : ''}
        title={t('auth.registerTitle')}
        desc={desc}
        submitText={t('auth.registerAndLogin')}
        onSubmit={doRegister}
        busy={loading}
        corner={langPill}
        foot={(
          <>
            {/* ★ P1-8：减少动效档的 AI 入口（正常档由下方的自动接管流程负责，这里不重复出按钮） */}
            {reducedMotion && (
              <button type="button" className="auth-link" onClick={() => setRegPhase('ai')}>{t('auth.aiEntry')}</button>
            )}
            {/* ★ 2026-10-21 IAM 重做 P1-4：注册屏同样补一条站内出路（旧形态只能靠浏览器后退）。
                点它回登录屏并把用户已经敲进去的用户名/密码原样留在表单里（不能顺手清空——
                想"去登录试试"的人往往就是刚发现自己的账号已经存在）。 */}
            <button type="button" className="auth-link" onClick={() => setView('signin')}>{t('auth.backToLogin')}</button>
          </>
        )}
      >
           {branding.dedicatedRegister ? (
          <Field label={t('auth.orgInvite')}>
            <Input value={form.invite} onChange={(e) => setForm({ ...form, invite: e.target.value })} placeholder={t('auth.orgInvite')} />
          </Field>
        ) : (
          <>
            {/* 个人 / 企业 分段 */}
            <div className="auth-seg-row">
              <button type="button" className={'auth-seg' + (typeChoice === 'personal' ? ' auth-seg--on' : '')} onClick={() => setTypeChoice('personal')}>{t('auth.tabPersonal')}</button>
              <button type="button" className={'auth-seg' + (typeChoice === 'enterprise' ? ' auth-seg--on' : '')} onClick={() => setTypeChoice('enterprise')}>{t('auth.tabEnterprise')}</button>
            </div>
            {/* 企业用户：管理员（建企业）/ 成员（凭邀请码加入）角色 */}
            {typeChoice === 'enterprise' && (
              <div className="auth-seg-row">
                <button type="button" className={'auth-seg' + (roleChoice === 'admin' ? ' auth-seg--on' : '')} onClick={() => setRoleChoice('admin')}>{t('auth.roleAdmin')}</button>
                <button type="button" className={'auth-seg' + (roleChoice === 'member' ? ' auth-seg--on' : '')} onClick={() => setRoleChoice('member')}>{t('auth.roleStaff')}</button>
              </div>
            )}
          </>
        )}

        {/* 企业成员 / 管理员 各自的字段（邀请码、组织名、行业、品牌译名）*/}
        {typeChoice === 'enterprise' && !branding.dedicatedRegister && (
          <>
                {roleChoice === 'member' && (
              <Field label={t('auth.orgCode')}>
                <Input value={form.invite} onChange={(e) => setForm({ ...form, invite: e.target.value })} placeholder={t('auth.orgCodeStaffPlaceholder')} />
              </Field>
            )}
                {roleChoice === 'admin' && (
              <>
                <Field label={t('auth.orgCode')}><Input value={form.code} onChange={(e) => setForm({ ...form, code: e.target.value })} placeholder={t('auth.orgCode')} /></Field>
                <Field label={t('auth.orgNameCn')}><Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder={t('auth.orgNameCn')} /></Field>
                <Field label={t('auth.orgNameEn')}><Input value={form.orgEn} onChange={(e) => setForm({ ...form, orgEn: e.target.value })} placeholder={t('auth.orgNameEn')} /></Field>
                <Field label={t('auth.industry')}>
                  <select className="lc-input" value={form.industry} onChange={(e) => setForm({ ...form, industry: e.target.value })}>
                    <option value="">{t('auth.selectIndustry')}</option>
                    {(industries.length > 0 ? industries.map((x) => ({ value: x.code, label: x.name })) : industryOptions(lang)).map((o) => (
                      <option key={o.value} value={o.value}>{o.label}</option>
                    ))}
                  </select>
                </Field>
              </>
            )}
          </>
        )}

        <Field label={t('auth.fieldUsername')}>
          <Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder={t('auth.fieldUsername')} />
        </Field>
        <Field label={t('auth.fieldPassword')}>
          <Input
            type={showRegPwd ? 'text' : 'password'}
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={t('auth.fieldPassword')}
            trailing={<PwdEye shown={showRegPwd} onToggle={() => setShowRegPwd((v) => !v)} />}
          />
        </Field>
        <Field label={t('auth.fieldEmail')}>
          <Input value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} placeholder={t('auth.fieldEmail')} />
        </Field>
        {/* 职业角色（2026-09-19）：个人/企业所有分支共用——角色只绑用户不绑企业，
            选填（后端对未知/空 code 静默忽略），注册后可在账号菜单随时改（转岗） */}
        <Field label={t('auth.jobRole')}>
          <select className="lc-input" value={form.jobRole} onChange={(e) => setForm({ ...form, jobRole: e.target.value })}>
            <option value="">{t('auth.selectJobRole')}</option>
            {personas.map((x) => (
              <option key={x.code} value={x.code}>{personaName(x.code, x.name, lang)}</option>
            ))}
          </select>
        </Field>
        {/* 邮箱验证码：只在后台开了邮箱验证（emailVerifyOn）时出现；发码按钮进 60s 冷却防刷 */}
            {emailVerifyOn && (
          <Field label={t('auth.fieldEmailCode')}>
            <div style={{ display: 'flex', gap: 8 }}>
              <Input style={{ flex: 1 }} value={form.emailCode} onChange={(e) => setForm({ ...form, emailCode: e.target.value })} placeholder={t('auth.fieldEmailCode')} />
              <Button variant="secondary" disabled={codeCd.left > 0} onClick={doSendCode}>
                {codeCd.left > 0 ? tplF('auth.codeResend', { n: codeCd.left }) : t('auth.sendCode')}
              </Button>
            </div>
          </Field>
        )}

        {/* 个人用户：好友邀请码（选填）*/}
        {typeChoice === 'personal' && !branding.dedicatedRegister && (
          <Field label={t('auth.friendInvite')}>
            <Input value={form.invite} onChange={(e) => setForm({ ...form, invite: e.target.value })} placeholder={t('auth.friendInvite')} />
          </Field>
        )}

        {/* 企业管理员：品牌固定译名（防翻译漂移）*/}
        {typeChoice === 'enterprise' && !branding.dedicatedRegister && roleChoice === 'admin' && (
          <>
            <Field label={t('auth.brandNameCn')}><Input value={form.brandName} onChange={(e) => setForm({ ...form, brandName: e.target.value })} placeholder={t('auth.brandNameCn')} /></Field>
            <Field label={t('auth.brandNameEn')}><Input value={form.brandNameEn} onChange={(e) => setForm({ ...form, brandNameEn: e.target.value })} placeholder={t('auth.brandNameEn')} /></Field>
          </>
        )}

        {/* Turnstile 挂载点：容器自己不留任何文案，开启人机验证时由 renderTurnstile 往里塞 widget */}
            <div ref={captchaBoxRef} id="__ts_widget__" />
        {/* ★ IAM P1-5：把「人机验证只覆盖注册＋发码」这条刻意取舍写在界面上，免得用户以为
            登录/找回漏了防护；文案走 12 语种词典，不硬编码中文。 */}
        {captchaOn && <p className="auth-note">{t('auth.captchaNote')}</p>}
        {/* 走 label 属性而不是 children：Checkbox 内部是 <input>（void 元素），children 若被摊进去会直接抛错 */}
        <Checkbox
          checked={agreed}
          onChange={(e) => setAgreed(!!e.target.checked)}
          label={(
            <>
              {t('auth.agreeTerms')}{''}
              <a href="/docs/terms" target="_blank" rel="noreferrer" className="auth-link">{t('auth.userAgreement')}</a>
              {' & '}
              <a href="/docs/privacy" target="_blank" rel="noreferrer" className="auth-link">{t('auth.privacyPolicy')}</a>
            </>
          )}
        />
        {!!regMsg && <div className="auth-err">{regMsg}</div>}
      </AuthCard>
      <ForcePwdDialog open={forceOpen} msg={forceMsg} busy={forceBusy} old={forceOld} newP={forceNew} confirm={forceConfirm}
        onOld={setForceOld} onNew={setForceNew} onConfirmPwd={setForceConfirm} onSubmit={submitForcePwd}
        onCancel={() => { if (!forceBusy) setForceOpen(false) }} />
      <style>{CSS_AUTH}</style>
    </BrandLoginShell>
  )
}

// ★ 2026-10-21 IAM 重做 P1-3：密码可见切换已抽到 `components/PwdEye.tsx`（登录/注册/找回/强制改密共用）。
//   本文件原先在这里定义了一份，注释写"四处共用"而强制改密窗取不到——2026-10-22 核查后挪走。

// 首登强制改密弹窗（保留原逻辑，langcross Dialog 实现）
// 三个认证屏共用同一个实例（每屏末尾各挂一处），props 全部由上层受控：
// 这里不持有任何状态，改密成功/失败的反馈由 submitForcePwd 写回 forceMsg + toast。
// busy 期间禁遮罩关闭（dismissOnOverlay）：请求进行中关掉弹窗会让人以为已经改完了。
function ForcePwdDialog(props: {
  open: boolean; msg: string; busy: boolean
  old: string; newP: string; confirm: string
  onOld: (v: string) => void; onNew: (v: string) => void; onConfirmPwd: (v: string) => void
  onSubmit: () => void; onCancel: () => void
}) {
  // 三格共用一个可见档：同一屏里逐格各切一次只会让人以为切错了格（与移动端改密弹窗同形态）
  const [show, setShow] = useState(false)
  return (
    <Dialog open={props.open} title={t('pwd.forceTitle')} confirmText={t('pwd.forceSubmit')}
            onConfirm={props.onSubmit} onCancel={props.onCancel} dismissOnOverlay={!props.busy}>
      <p style={{ fontSize: 15, color: 'var(--lc-text-3)', lineHeight: 1.7, margin: '0 0 14px' }}>{t('pwd.forceHint')}</p>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <Field label={t('pwd.oldPassword')}>
          <Input type={show ? 'text' : 'password'} value={props.old} onChange={(e) => props.onOld(e.target.value)}
            trailing={<PwdEye shown={show} onToggle={() => setShow((v) => !v)} />} />
        </Field>
        {/* 新密码/确认框标 new-password：否则浏览器密码管理器会把这次改密当成登录，
            自动回填初始密码，用户「改了」却没改 */}
        <Field label={t('auth.newPassword')}>
          <Input type={show ? 'text' : 'password'} autoComplete="new-password" value={props.newP} onChange={(e) => props.onNew(e.target.value)} />
        </Field>
        <Field label={t('pwd.confirmPlaceholder')}>
          <Input type={show ? 'text' : 'password'} autoComplete="new-password" value={props.confirm} onChange={(e) => props.onConfirmPwd(e.target.value)} />
        </Field>
        {!!props.msg && <div className="auth-err">{props.msg}</div>}
      </div>
    </Dialog>
  )
}

// ---------- 作用域样式（auth- 前缀，避免与组件库类名重名）----------
// ★ 2026-10-21 IAM 重做 P1-2/P1-5 两条几何口径：
//   ① 三张认证卡统一 400 宽（AuthCard 交付档）。旧形态给找回屏多挂了一个
//      `.lc-auth-card--compact{max-width:360px}`，于是同一套流程里三屏三个宽度，
//      从登录点进找回会看到卡片"缩了一下"——交付稿里并不存在这个窄档，已删。
//   ② 移动档（≤640）：卡片改满宽、外边距收到 16；背板从 `align-items:center` 换成 `safe center`
//      并允许纵向滚动。旧形态是固定 `center`＋不可滚动，屏幕比卡片矮时（小屏 + 键盘弹出 + 注册表单 9 项）
//      卡片顶部会被裁到视口外且**无法滚回去**，用户看不到第一个输入框。
const CSS_AUTH = `
.lc-auth-bg{position:fixed;inset:0;display:flex;align-items:center;align-items:safe center;justify-content:center;background:#000;font-family:var(--lc-font);padding:24px;box-sizing:border-box;overflow-y:auto;}
.auth-link{background:none;border:0;color:var(--lc-text-3);font-size:14px;cursor:pointer;font-family:var(--lc-font);padding:0;}
.auth-link:hover{color:var(--lc-text);text-decoration:underline;}
.auth-link:disabled{opacity:.55;cursor:default;text-decoration:none;}
.auth-note{margin:0;color:var(--lc-text-3);font-size:14px;line-height:1.5;}
.auth-resend{display:flex;justify-content:flex-end;margin-top:-4px;}
/* 卡右上角语言胶囊（31×19、面 #16181C、纯白、无描边、圆角全圆） */
.auth-lang{height:19px;padding:0 8px;border:0;border-radius:999px;background:var(--lc-raised);color:#FFFFFF;font-family:var(--lc-font-latin);font-size:13px;font-weight:500;line-height:1;cursor:pointer;}
.auth-lang:hover{box-shadow:inset 0 0 0 2px var(--lc-border-pill);}
.auth-lang:focus-visible{outline: 2px solid var(--lc-border-input);outline-offset:2px;}
/* 错误 / 中性提示各占一行：找回发码成功用 .auth-ok（次级文字色），
   纯黑体系里不引入成功绿，避免一处绿把整屏配色基调带偏 */
.auth-err{color:var(--lc-danger);font-size:15px;line-height:1.6;}
.auth-ok{color:var(--lc-text-2);font-size:15px;line-height:1.6;}
/* ★ #38 第三方登录区：分隔线用一条独立细条 + 居中文字（纯黑体系不引入品牌色块）。
   height 1px 的独立分隔条不算「框」，不随全站框线档变化；颜色取常规分隔线档 --lc-border-faint。
   按钮等宽纵排，避免两个 IdP 时长短不齐看着像残排 */
.auth-sso{display:flex;flex-direction:column;gap:8px;margin-top:4px;}
.auth-sso-sep{display:flex;align-items:center;gap:8px;color:var(--lc-text-3);font-size:14px;}
.auth-sso-sep::before,.auth-sso-sep::after{content:"";flex:1;height:1px;background:var(--lc-border-card,#FFFFFF);}
.auth-sso .lc-btn{width:100%;justify-content:center;text-decoration:none;}
/* 分段选择器（个人/企业、管理员/成员）：画布是两枚等宽胶囊，故用按钮组而不是 Radio */
.auth-seg-row{display:flex;gap:8px;}
.auth-seg{flex:1;height:36px;border-radius:var(--lc-r-ctl);border:1.2px solid var(--lc-border-pill);background:transparent;color:var(--lc-text-2);font-size:15px;font-weight:500;cursor:pointer;font-family:var(--lc-font);}
.auth-seg--on{background:var(--lc-raised);color:var(--lc-text);border-color:var(--lc-border-done);font-weight:600;}
.auth-pwd-eye{display:flex;align-items:center;justify-content:center;background:none;border:0;color:var(--lc-text-3);cursor:pointer;padding:4px;line-height:0;}
.auth-pwd-eye:hover{color:var(--lc-text);}
/* 接管前「传统表单闪三下」：三拍落在 8%/16%、38%/46%、72%/82%，
   steps(1,end) 让亮度是跳变而非被插值成糊的一团（时长 920ms 与 Login 里的节拍对齐） */
.auth-flash{animation:auth-flash .92s steps(1,end) forwards;}
@keyframes auth-flash{0%{filter:brightness(1)}8%{filter:brightness(1.8)}16%{filter:brightness(1)}38%{filter:brightness(1.62)}46%{filter:brightness(1)}72%{filter:brightness(1.45)}82%,100%{filter:brightness(1)}}
/* 整页退场：120ms 淡出 + 上移 6px，紧接 AI 面板挂上（130ms），中间不留空帧 */
.auth-pg-out{animation:auth-pg-out .12s ease forwards;pointer-events:none;}
@keyframes auth-pg-out{to{opacity:0;transform:translateY(-6px);}}
/* 偏好减少动效：动画一律不播；Login 的接管节拍同样会提前 return，用户停在传统表单自己填 */
@media (prefers-reduced-motion: reduce){
  .auth-flash{animation:none!important;}
  .auth-pg-out{animation:none!important;transition:none!important;}
}
/* ★ 移动档（≤640）：卡片满宽、内垫收到 20、背板外边距收到 16。
   桌面档几何一字不动（AuthCard 的 400 宽属于交付档，见 components.css）。 */
@media (max-width: 640px){
  .lc-auth-bg{padding:16px;}
  .lc-auth-bg .lc-auth-card{width:100%;max-width:100%;padding:20px;}
}
`
