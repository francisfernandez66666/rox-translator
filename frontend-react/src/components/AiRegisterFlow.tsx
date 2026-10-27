// ============================================================================
// components/AiRegisterFlow.tsx — AI 接管注册引导（交付包 §5 / demo-register-ai-motion.html）
// 点「免费注册」后传统表单闪烁三次并整页 120ms 退出，由本面板接管问答：
//   ① 账号类型 ② 企业→企业身份 ③ 管理员→行业 4 选 1 →（各分支）职业角色问答（2026-09-19）
//     ④ 按分支裁剪的账号信息表单
//     （用户名自动带入 + OTP 6 格 + 管理员组织中英名 / 员工组织编码）
//   ⑤ 管理员提交后追加品牌固定译名预配 chips ⑥ 检查点三连 + 完成摘要卡
// 支持「← 上一步」（首步隐藏、完成后禁用）；尊重 prefers-reduced-motion；generation 计数器防竞态。
// 业务逻辑复用 @/api 的 authRegister / login / sendEmailCode，仅重写呈现与交互。
// ★ 2026-10-22 统一登录批：本面板新增第二种模式 mode='complete'（完善信息）——
//   主站登录后由 ProfileSetupGate 挂载，邮箱/验证码两步已不需要（登录时已验证过邮箱），
//   个人分支改走 profileSetup 补全既有空白账号，企业分支走 authRegister 升级通道
//   （带登录邮箱，后端对「本人空白账号」豁免验码并停用旧号建新号）。
//   两种模式共用同一套问答动效，仅话术键与提交换算不同：注册话术键经 ACP_KEYS
//   映射换成「完善信息」话术（auth.acp*，2026-10-22 新增 12 语种），
//   共享键一个不改——那一改会顺着共享键串到租户站的注册流（「只影响主站」红线）。
// ★ 2026-09-24 发码 403 生产事故修复：本面板此前完全不接人机验证（Turnstile），
//   后台开启后「发送验证码/提交注册」必吃 403 且异常未捕获，表现为「点了没反应」——
//   现由 captcha 入参接管：表单块内挂验证组件、token 一请求一消费、失败显式 toast。
// ★ 2026-09-25 批G（发布前 UAT）：F-28 OTP filled 态类名拼接补空格；F-02 已发码标记
//   ref→useState（发码成功按钮文案才会真的重渲染）；F-03 发码失败 60s 冷却禁用重发
//   （复用 lib/useCountdown，与传统表单 Login.tsx 对齐）。
// ============================================================================
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { authRegister, login, sendEmailCode, profileSetup, setAuthToken, setActiveTenantId, type AuthUser } from '@/api'
import { registerPersonas } from '@/api/persona'
import { PERSONA_FALLBACK, personaName } from '@/lib/personas'
import { toastError } from '@/lib/toastBus'
// ★ 2026-10-21 IAM 重做 P1-1：口令长度与注册/登录共用同一把尺子
import { PWD_MIN_LEN } from '@/lib/accountForm'
import { useCountdown } from '@/lib/useCountdown' // ★ F-03（2026-09-25 批G）：发码失败 60s 冷却，复用 #42 收口的倒计时钩子
import { loadTurnstile, renderTurnstile, reexecTurnstile, pickTurnstileSize, retryTurnstile, turnstileLoadState, type TurnstileLoadState } from '@/lib/turnstile'
import { useT, getLang, tpl } from '@/i18n'
import { EyeIcon, EyeOffIcon, Checkbox } from '@/ui/langcross/src'
import { typingUnitOf, chatTypingMs } from '@/i18n/script' // ★ 〇-Q：聊天打字单元/速度按文字系统分档
import { useBranding } from '@/branding'

/** 屏幕是否偏好减少动效（驱动动效直出终态） */
function usePrefersReducedMotion() {
  const [r, setR] = useState(false)
  useEffect(() => {
    // matchMedia 必须带守卫：jsdom / 老旧 WebView 上不存在，裸调会在 effect 里直接抛
    if (typeof window ==='undefined'|| !window.matchMedia) return
    const m = window.matchMedia('(prefers-reduced-motion: reduce)')
    const f = () => setR(m.matches)
    f()
    m.addEventListener?.('change', f)
    return () => m.removeEventListener?.('change', f)
  }, [])
  return r
}

// 动效节拍之间的统一间隔单位：本文件所有「等一下」都走它，避免散落 setTimeout(数字) 难调
const sleep = (ms: number) => new Promise<void>((res) => setTimeout(res, ms))

// ---------- 聊天消息模型 ----------
// 一条消息 = 一个气泡，靠可选字段决定它渲染成什么（文本 / 选项组 / 表单 / chips / 检查点 / 摘要卡）。
// 刻意不用 union type 分派：同一条气泡可能先只有文本、打完字后再挂上选项，union 反而要拆消息 id。
interface Opt { t: string; d: string } // 选项：标题 + 一句说明
// 留资流程气泡：角色 + 文本（顾问话术与用户回答共用一种结构）
interface Msg {
  id: number
  side:'ai'|'user'
  full?: string // 打字机目标全文（同时作为 ghost 占位撑高，避免逐字打字时行高跳动）
  text?: string // 已打出文本
  typing?: boolean
  opts?: Opt[] // 可点选项组（账号类型 / 企业身份 / 行业）
  picked?: string
  form?: boolean // 渲染账号信息表单块
  chips?: { k: string; v: string }[] // 品牌固定译名预配结果（键值对小胶囊）
  ckpts?: string[] // 收尾检查点：数组长度即「已点亮几格」，逐格 push 出点亮节奏
  done?: { // 完成摘要卡（注册成功后落到聊天流末尾，等价于传统注册的结果确认页）
    type: string
    identity: string
    industry?: string // 仅管理员分支有（配了哪个行业词库）
    persona?: string // 职业角色显示名（2026-09-19，跳过则不展示）
    username: string
    email: string
    inviteLeft?: boolean // 个人分支：明示「好友邀请码已留空」，避免用户以为漏填
    // ★ 2026-10-24 多身份 B4-d（R8 拍板）：complete 那一支（既有个人账号 → 加入/新建企业）
    //   必须在摘要卡上看见"个人身份没被并进去、随时可切回"。
    //   **只在真的存在另一个身份时才出这一句**——纯新注册的人没有"切回"可言，写了就是空头承诺。
    switchBack?: boolean
  }
}

// 行业四选一：code 必须对齐后端内置行业字典（auto/ecommerce/education/b2b），
// 2026-09-19 修复：原先把词典键当 code 透传，后端 FindIndustryByCode 恒不命中、
// 所有 AI 注册的企业都落进「通用行业」兜底包
const INDUSTRIES = [
  { t:'auth.aiInd1', d:'auth.aiInd1Desc', code:'auto'},
  { t:'auth.aiInd2', d:'auth.aiInd2Desc', code:'ecommerce'},
  { t:'auth.aiInd3', d:'auth.aiInd3Desc', code:'education'},
  { t:'auth.aiInd4', d:'auth.aiInd4Desc', code:'b2b'},
]

// ★ 2026-10-22 统一登录批：complete（完善信息）模式的话术键映射。
// 键位是「注册话术 → 完善话术」的一对一替换；查不到的键原样返回（行业/角色/组织字段
// 这些两种模式同义的键不走映射）。刻意不就地改共享词：共享键一改，租户/演示站的
// 注册流话术会跟着变，违反「这一批改动只影响主站」。
const ACP_KEYS: Record<string, string> = {
  'auth.aiTitle': 'auth.acpTitle',
  'auth.aiIntro': 'auth.acpIntro',
  'auth.aiAskType': 'auth.acpAskType',
  'auth.aiPersonal': 'auth.acpPersonal',
  'auth.aiEnterprise': 'auth.acpEnterprise',
  'auth.aiPersonalDesc': 'auth.acpPersonalDesc',
  'auth.aiSkipPersona': 'auth.acpSkipPersona',
  'auth.aiTailPersonal': 'auth.acpTailPersonal',
  'auth.aiTailPersonalPlain': 'auth.acpTailPersonalPlain',
  'auth.aiTailAdmin': 'auth.acpTailAdmin',
  'auth.aiTailAdminPlain': 'auth.acpTailAdminPlain',
  'auth.aiTailStaff': 'auth.acpTailStaff',
  'auth.aiTailStaffPlain': 'auth.acpTailStaffPlain',
  'auth.aiFinishingPersonal': 'auth.acpFinishingPersonal',
  'auth.aiDoneChat': 'auth.acpDoneChat',
  'auth.aiRegisterDone': 'auth.acpDone',
}

/**
 * AiRegisterFlow 入参：
 * prefillUsername 登录页已输入的用户名（带入但不锁）；dedicatedRegister 品牌专属域名入口
 * （跳过问答直落企业员工分支）；captcha 人机验证配置（登录页 register-config 下发，
 * ★ 2026-09-24 起必传口径——缺省视为关闭，与后端 captcha_provider=none 同义）；
 * onDone 注册并自动登录成功后进工作台；onClose 退回传统表单。
 * ★ 2026-10-22 统一登录批：mode='complete'（完善信息，主站登录后）三件套——
 *   mode 决定话术键映射与提交换算；loginEmail 是登录邮箱（企业分支升级注册要原样带回，
 *   后端据此识别「本人空白账号」；摘要卡只读展示）；sessionUser 是当前登录用户，
 *   仅作接口未回 user 时的兜底（完成/进入工作台都要一个有 id 的 AuthUser）。
 */
interface Props {
  prefillUsername: string
  dedicatedRegister: boolean
  captcha?: { enabled: boolean; siteKey: string }
  mode?: 'register' | 'complete'
  loginEmail?: string
  sessionUser?: AuthUser | null
  onDone: (u: AuthUser) => void
  onClose: () => void
}

/**
 * AI 接管注册引导面板（390 宽竖版卡，不是全屏接管）：
 * 用问答 + 一个必要表单替用户跑完注册，最后调 authRegister/login 两个真实接口。
 * 步骤是命令式「渲染函数」组成的栈（stepStack），上一步靠截断消息重放，而不是路由或状态机库。
 */
export default function AiRegisterFlow({ prefillUsername, dedicatedRegister, captcha, mode = 'register', loginEmail = '', sessionUser = null, onDone, onClose }: Props) {
  const [lang, t] = useT()
  // ★ 2026-10-22：complete 模式的话术键映射器（mode 挂载后不变，故 tk 稳定）。
  // 用法：把原来写 t('auth.aiX') 的地方换成 t(tk('auth.aiX'))。
  const tk = useCallback((k: string) => (mode === 'complete' ? ACP_KEYS[k] ?? k : k), [mode])
  const branding = useBranding()
  const reduced = usePrefersReducedMotion()
  const chatRef = useRef<HTMLDivElement>(null) // 聊天滚动容器：每次追加消息都要手动贴底
  const genRef = useRef(0) // 代际：上一步 / 重挂时 ++，进行中的打字与定时器自行熄火
  const idRef = useRef(0) // 消息自增 id（同时也是「本步起点」的界标，见 stepRender.startId）
  // 已选答案放 ref 而不是 state：问答分支只在下一次渲染时读，不需要为它重渲染
  const selRef = useRef<{ type?:'personal'|'enterprise'; role?:'admin'|'staff'; industryCode?: string; industryName?: string; personaCode?: string; personaName?: string }>({})
  // 角色问答选项（2026-09-19）：动态取后端角色字典，失败/为空落本地兜底词库
  const [personaList, setPersonaList] = useState<Array<{ code: string; name: string }>>(PERSONA_FALLBACK)

  const [msgs, setMsgs] = useState<Msg[]>([]) // 整条聊天流（消息是不可变更新的数组）
  const [stepLabel, setStepLabel] = useState('') // 进度条左侧「第 x / y 步 · 名称」
  const [trackPct, setTrackPct] = useState(0)
  // 上一步按钮：首步隐藏、完成后禁用（两个态分开，别用一个 boolean 混表达）
  const [backHidden, setBackHidden] = useState(true)
  const [backDisabled, setBackDisabled] = useState(false)
  const [finished, setFinished] = useState(false) // 已进入收尾流程：此后不接受回退
  const [busy, setBusy] = useState(false) // 注册请求进行中：锁选项与提交，防重复建号
  // ★ 2026-10-22 一致性核查修复（P1-3「一种错误位」）：
  //   方案 §四·3 原文：「错误一律卡内红字 `.auth-err`（AI 流保留 toast 但**必须同时**在卡内落一条）」。
  //   此前本组件 8 处错误全走 toastError、卡内零落位——toast 一消失用户看到的就是
  //   "点了没反应"，而传统注册卡（Login.tsx）早就有 .auth-err，同一件事两种形态。
  //   现在两个出口同一个来源：fail() 既 toast 又落卡内。
  const [formErr, setFormErr] = useState('')
  const fail = useCallback((msg: string) => {
    setFormErr(msg)
    toastError(msg)
  }, [])
  // AI 流程内的账号信息表单（与传统注册表单字段有意不共用：这里只有 4~6 项）
  const [aiForm, setAiForm] = useState({
    username: prefillUsername, password: '', email: '', emailCode: '', orgCode: '', orgCn: '', orgEn: '',
    agreed: false,
  })
  // ★ 2026-10-21 IAM 重做 P1-4：AI 流不再硬编码 agreed:true——把协议勾选收进表单，
  //   与登录/传统注册两处同形（同款 Checkbox＋同款链接文案）。旧形态"问答流程没有勾选步骤
  //   提交即视为已同意"绕过了同意闸，且与传统表单形态不一致（三处三套口径）。
  // 登录页带没带用户名：只决定提示文案（「已带入，可修改」），不锁字段
  const usernameBrought = prefillUsername.trim().length > 0
  // F-02（2026-09-25 批G）：已发码标记由 ref 改 useState。旧实现用 ref 是想避开整条聊天流
  // 重渲染，但按钮文案恰好读这个值——ref 改值不触发渲染，发码成功后按钮一直停在「发送验证码」
  // 不变（即本缺陷本身）。按钮实际渲染在 AccountFormBlock 子组件里，重渲染代价可控，取舍改向状态。
  const [codeSent, setCodeSent] = useState(false)
  // F-03（2026-09-25 批G）：发码失败后 60s 冷却禁用重发，与传统表单 Login.tsx 的
  // useCountdown(60) 同口径（定时器互斥/卸载清理由 lib/useCountdown 自带，不在此重造）
  const codeCd = useCountdown(60)
  const loggedUserRef = useRef<AuthUser | null>(null) // 注册后自动登录拿到的 user，留到「进入工作台」按钮再用

  // ---- 人机验证接线（★ 2026-09-24 发码 403 修复）----
  // 旧实现本面板根本不挂 Turnstile：后台开启人机验证后，发码与提交注册的请求都以裸 token 出网，
  // 服务端 siteverify 必判失败回 403，再被 api/core 的 403 统一文案误写成「无管理权限」——即用户看到的「没反应」。
  const captchaOn = !!captcha?.enabled && !!captcha.siteKey
  const tsIdRef = useRef<string | number | null>(null) // 本面板自己的 widget id
  const tsTokenRef = useRef('') // 组件回调写入的最新一次性 token
  // token 一次一用：任一请求（发码/提交）把它带出后立刻作废 → 清空并强制重新挑战领新码，
  // 绝不允许「发码用掉的码又被提交注册复用」
  const takeCaptchaToken = useCallback(() => {
    const tk = tsTokenRef.current
    tsTokenRef.current = ''
    reexecTurnstile(tsIdRef.current)
    return tk
  }, [])
  // ★ D-02（2026-10-27）：装载状态。旧形态 12 秒到点就静默停表——容器永远空着，
  //   访客只会在提交时撞上一句「请先完成人机验证」，而那一句指着一个不会自己出现的框。
  const [tsState, setTsState] = useState<TurnstileLoadState>(turnstileLoadState())
  // 表单块渲染出容器后回调挂载组件（装载脚本收敛在 lib/turnstile：与传统表单共用、只注入一次）
  const attachTs = useCallback((el: HTMLDivElement | null) => {
    if (!el || tsIdRef.current !== null || !captchaOn) return
    loadTurnstile(() => {
      // size 按容器选档（normal 固有宽 300px，窄卡里会顶破右缘）；状态回写驱动下面那张脸
      const id = renderTurnstile(el, captcha?.siteKey || '', (tk) => { tsTokenRef.current = tk }, { size: pickTurnstileSize(el) })
      if (id !== null) tsIdRef.current = id
    }, (st) => setTsState(st))
  }, [captchaOn, captcha?.siteKey])

  // 守卫文案按装载状态分派：还在加载 ≠ 加载不出来 ≠ 用户没点，三种原因的出路不同
  const tsBlockMsg = useCallback(() => (
    tsState === 'unavailable' ? t('auth.captchaUnavailable')
      : tsState === 'loading' ? t('auth.captchaLoading')
        : t('auth.captchaRequired')
  ), [tsState, t])

  // 装载提示（loading 只报"在路上"；unavailable 必须给「重试」出路）
  const tsNotice: ReactNode = !captchaOn ? null : tsState === 'unavailable' ? (
    <p className="ar-ts-note" role="alert" data-testid="captcha-notice">
      {t('auth.captchaUnavailable')}{' '}
      <button type="button" className="lc-btn lc-btn--secondary lc-btn--sm" data-testid="captcha-retry" onClick={() => retryTurnstile()}>
        {t('auth.captchaRetry')}
      </button>
    </p>
  ) : tsState === 'loading' ? (
    <p className="ar-ts-note" role="status" data-testid="captcha-notice">{t('auth.captchaLoading')}</p>
  ) : null

  // 步骤栈：每步记录其起始消息 id，返回时截断并重放（对齐 demo 的 stepRender）
  const stepStack = useRef<{ idx: number; total: number | null; name: string; startId: number; render: (my: number) => void }[]>([])

  // 追加消息后必须手动贴底：容器是 overflow-y:auto 的定高卡，浏览器不会替聊天流滚到底
  const scrollBottom = useCallback(() => {
    requestAnimationFrame(() => { if (chatRef.current) chatRef.current.scrollTop = chatRef.current.scrollHeight })
  }, [])

  // 消息入流并返回 id（后续打字/点亮都要按 id 定点更新这一条）
  const addMsg = useCallback((m: Omit<Msg, 'id'>): number => {
    const id = ++idRef.current
    setMsgs((s) => [...s, { ...m, id }])
    scrollBottom()
    return id
  }, [scrollBottom])

  // 本异步链是否仍属当前代际（false=已被「上一步」或卸载作废，调用点必须立即 return）
  const alive = (my: number) => my === genRef.current

  // 打字机：ghost 占位撑高（visibility:hidden 同文本）+ 真身绝对定位，窄屏不跳高
  const typeText = useCallback(async (id: number, text: string, my: number) => {
    if (reduced) {
      // 减少动效：直接写满文本并返回 true，保持调用方 `.then(ok => 下一步)` 的推进语义不变
      setMsgs((s) => s.map((m) => (m.id === id ? { ...m, text, typing: false } : m)))
      return true
    }
    // ★ 〇-Q：打字单元（CJK 逐字 / 拉丁逐词）与速度（chatTypingMs）按界面语种走，动效随语种变
    const lang = getLang()
    const unit = typingUnitOf(lang)
    const step = chatTypingMs(lang)
    if (unit === 'word') {
      // 拉丁/西里尔/阿语：逐词推进——逐字母打会像乱码闪烁
      const toks = text.split(/(\s+)/) // 保留空白 token，重建后文本与原文逐字符一致
      let acc = ''
      for (const tk of toks) {
        if (!alive(my)) return false
        acc += tk
        setMsgs((s) => s.map((m) => (m.id === id ? { ...m, text: acc, typing: true } : m)))
        scrollBottom()
        await sleep(step)
      }
    } else {
      for (let i = 0; i < text.length; i++) {
        if (!alive(my)) return false
        const slice = text.slice(0, i + 1)
        setMsgs((s) => s.map((m) => (m.id === id ? { ...m, text: slice, typing: true } : m)))
        scrollBottom()
        await sleep(step)
      }
    }
    if (!alive(my)) return false
    setMsgs((s) => s.map((m) => (m.id === id ? { ...m, typing: false } : m)))
    return true
  }, [reduced, scrollBottom])

  // 上一步按钮的可见性：首步（栈深 <=1）没有可回的地方，收尾后也不允许回退
  const updateBack = useCallback(() => {
    setBackHidden(finished || stepStack.current.length <= 1)
  }, [finished])

  // 渲染某一步：先清掉本步之后的消息（保证重放幂等），再追加本步内容
  const stepRender = useCallback((idx: number, total: number | null, name: string, render: (my: number) => void) => {
    const startId = idRef.current + 1 // 本步第一条消息的 id：goBack 时按它截断
    stepStack.current.push({ idx, total, name, startId, render })
    setMsgs((s) => s.filter((m) => m.id < startId))
    setStepLabel(total ? tpl('auth.aiStepOf', { idx, total, name }) : tpl('auth.aiStepOnly', { idx, name }))
    // 总步数要等分支问完才确定（个人 3 步 / 员工 4 步 / 管理员 5 步）：
    // total 为 null 时按「已答步数 + 预估 2 步」给进度，别让它停在 0 看着像没动
    setTrackPct(total ? (idx / total) * 100 : (idx / (idx + 2)) * 100)
    render(genRef.current)
    updateBack()
  }, [updateBack])

  const goBack = useCallback(() => {
    if (finished || stepStack.current.length < 2) return
    const my = ++genRef.current // 中断进行中的打字/回调
    stepStack.current.pop() // 当前步作废
    const prev = stepStack.current[stepStack.current.length - 1]
    // 截断到上一步起点之前，再重放上一步
    setMsgs((s) => s.filter((m) => m.id < prev.startId))
    // 重放渲染函数而不是「缓存旧 DOM」：回退后的这一屏和首次进入时逐字一致（含打字动效）
    if (prev.render) prev.render(my)
  }, [finished])

  // ---------- 步骤体 ----------
  // 每个步骤体都是「(my) => 追加气泡 → 等打完 → .then 里追加下一条」的链式结构：
  // 串起来打而不并行，是为了同一时刻只有一个光标；my（代际）逐层透传，回退时整条链一起作废
  const askType = useCallback((my: number) => {
    const introKey = tk('auth.aiIntro'), askKey = tk('auth.aiAskType')
    const m = addMsg({ side: 'ai', full: t(introKey), text: reduced ? t(introKey) : '', opts: undefined })
    void typeText(m, t(introKey), my).then((ok) => {
      if (!ok || !alive(my)) return
      const m2 = addMsg({ side:'ai', full: t(askKey), text: reduced ? t(askKey) :''})
      void typeText(m2, t(askKey), my).then((ok2) => {
        if (!ok2 || !alive(my)) return
        addMsg({
          side: 'ai', opts: [
            { t: t(tk('auth.aiPersonal')), d: t('auth.aiPersonalDesc') },
            { t: t(tk('auth.aiEnterprise')), d: t('auth.aiEnterpriseDesc') },
          ],
        })
        // 选项点击在渲染时绑定（见 renderMsg 的 opts 处理）
      })
    })
  }, [addMsg, typeText, alive, reduced, t, tk])

  const askRole = useCallback((my: number) => {
    const m = addMsg({ side:'ai', full: t('auth.aiAskRole'), text: reduced ? t('auth.aiAskRole') :''})
    void typeText(m, t('auth.aiAskRole'), my).then((ok) => {
      if (!ok || !alive(my)) return
      addMsg({
        side: 'ai', opts: [
          { t: t(tk('auth.aiEnterprise')) +'·'+ t('auth.roleAdmin'), d: t('auth.aiRoleAdminDesc') },
          { t: t(tk('auth.aiEnterprise')) +'·'+ t('auth.roleStaff'), d: t('auth.aiRoleStaffDesc') },
        ],
      })
    })
  }, [addMsg, typeText, alive, reduced, t, tk])

  const askIndustry = useCallback((my: number) => {
    const m = addMsg({ side:'ai', full: t('auth.aiAskIndustry'), text: reduced ? t('auth.aiAskIndustry') :''})
    void typeText(m, t('auth.aiAskIndustry'), my).then((ok) => {
      if (!ok || !alive(my)) return
      addMsg({ side: 'ai', opts: INDUSTRIES.map((x) => ({ t: t(x.t), d: t(x.d) })) })
    })
  }, [addMsg, typeText, alive, reduced, t])

  // 角色问答（2026-09-19）：三条分支共用，选项动态取角色字典 + 「暂不选择」兜底；
  // 答完统一交给 advanceAfterPersona 按已选身份推进到账号信息步
  const askPersona = useCallback((my: number) => {
    const m = addMsg({ side:'ai', full: t('auth.aiAskPersona'), text: reduced ? t('auth.aiAskPersona') :''})
    void typeText(m, t('auth.aiAskPersona'), my).then((ok) => {
      if (!ok || !alive(my)) return
      addMsg({
        side: 'ai', opts: [
          ...personaList.map((x) => ({ t: personaName(x.code, x.name, lang), d: '' })),
          { t: t(tk('auth.aiSkipPersona')), d: t('auth.aiSkipPersonaDesc') },
        ],
      })
    })
  }, [addMsg, typeText, alive, reduced, t, tk, personaList, lang])

  const askAccount = useCallback((my: number, kind:'personal'|'staff'|'admin') => {
    // 带没带用户名用不同话术：带了说「已带入（可修改）」，没带就请用户在本步填写
    const kindKey = kind ==='personal'?'Personal': kind ==='admin'?'Admin':'Staff'
    // ★ 2026-10-22：尾款话术也要过 tk——complete 模式下六条尾款全部换成「完善信息」口径
    // （原话术里「邮箱和验证码」的提醒在登录后已不适用，字段也没了）
    const tailKey = tk(usernameBrought ? `auth.aiTail${kindKey}` : `auth.aiTail${kindKey}Plain`)
    const m = addMsg({ side:'ai', full: t(tailKey), text: reduced ? t(tailKey) :''})
    void typeText(m, t(tailKey), my).then((ok) => {
      if (!ok || !alive(my)) return
      addMsg({ side: 'ai', form: true })
    })
  }, [addMsg, typeText, alive, reduced, t, tk, usernameBrought])

  // 角色答完后的推进：账号信息步的序号/总步数按分支排布
  // （个人：类型1→角色2→账号3/4；员工：类型1→身份2→角色3→账号4/5；管理员：类型1→身份2→行业3→角色4→账号5/6）
  const advanceAfterPersona = useCallback((_my: number) => {
    const sel = selRef.current
    const kind: 'personal'|'staff'|'admin' = sel.type === 'personal' ? 'personal' : sel.role === 'admin' ? 'admin' : 'staff'
    const [idx, total] = kind === 'admin' ? [5, 6] : kind === 'staff' ? [4, 5] : [3, 4]
    stepRender(idx, total, t('auth.aiStepAccount'), (m) => askAccount(m, kind))
  }, [stepRender, t, askAccount])

  // 收尾：检查点三连 + 完成摘要
  const finish = useCallback(async (my: number, kind:'personal'|'staff'|'admin', industryName?: string) => {
    setFinished(true); setBackDisabled(true); setBackHidden(true)
    // 三条分支各自的总步数（2026-09-19 起含角色问答一步）：管理员 6、员工 5、个人 4
    const total = kind ==='admin'? 6 : kind ==='staff'? 5 : 4
    setStepLabel(tpl('auth.aiStepOf', { idx: total, total, name: t(tk('auth.aiRegisterDone')) }))
    setTrackPct(100)
    const finishKey = kind ==='personal'? tk('auth.aiFinishingPersonal'): kind ==='admin'?'auth.aiFinishingAdmin':'auth.aiFinishingStaff'
    // 管理员话术里嵌了行业名：t() 不做参数插值，这里手动替换 {industry} 占位
    const finishText = kind ==='admin'&& industryName ? t(finishKey).replace('{industry}', industryName) : t(finishKey)
    const m = addMsg({ side:'ai', full: finishText, text: reduced ? finishText :''})
    if (!await typeText(m, finishText, my)) return
    // ★ 2026-10-22：个人分支的检查点在 complete 模式换「完善信息」口径——
    // 旧三条（创建账号/初始化词库/发欢迎邮件）描述的是注册副作用，而完善模式只是
    // profileSetup 保存既有账号的资料（用户名/口令/职业角色 + 重签会话）。
    // 企业两条分支照旧：升级注册确实在建新账号与组织，共享词无需改。
    const ckNames = kind === 'personal'
      ? (mode === 'complete'
          ? [t('auth.acpCkPersonal1'), t('auth.acpCkPersonal2'), t('auth.acpCkPersonal3')]
          : [t('auth.ckPersonal1'), t('auth.ckPersonal2'), t('auth.ckPersonal3')])
      : kind === 'admin'
        ? [t('auth.ckAdmin1'), t('auth.ckAdmin2'), t('auth.ckAdmin3')]
        : [t('auth.ckStaff1'), t('auth.ckStaff2'), t('auth.ckStaff3')]
    addMsg({ side: 'ai', ckpts: ckNames })
    scrollBottom()
    // 峰值时序（原则 3/4）：三连点亮期间整块压暗到 .84 攒落差，
    // 最后一格亮起时释放并闪一下 —— 峰值是一个时刻，不是一段。
    const ckStart = idRef.current
    let ckEl: HTMLElement | null = null
    if (!reduced) {
      await sleep(90) // 让容器先落 DOM（仪器不能早于被测对象）
      if (!alive(my)) return
      ckEl = chatRef.current?.querySelector<HTMLElement>('.ar-ckpts') ?? null
      ckEl?.classList.add('is-sink')
    }
    // 逐格点亮
    for (let i = 0; i < ckNames.length; i++) {
      await sleep(reduced ? 40 : 460)
      if (!alive(my)) return
      setMsgs((s) => s.map((mm) => (mm.id === ckStart ? { ...mm, ckpts: ckNames.slice(0, i + 1) } : mm)))
      scrollBottom()
    }
    if (!reduced && ckEl) {
      ckEl.classList.remove('is-sink')
      ckEl.classList.add('is-release')
      await sleep(460) // 闪完停留，观众读完三条检查点
      if (!alive(my)) return
      ckEl.classList.remove('is-release')
    }
    await sleep(reduced ? 0 : 380)
    if (!alive(my)) return
    const doneChatKey = tk('auth.aiDoneChat')
    const d2 = addMsg({ side:'ai', full: t(doneChatKey), text: reduced ? t(doneChatKey) :''})
    if (!await typeText(d2, t(doneChatKey), my)) return
    const identity = kind ==='personal'? t('auth.aiIdentityPersonal')
      : kind ==='admin'? t('auth.aiIdentityAdmin') + (aiForm.orgCn ?'·'+ aiForm.orgCn :'')
        : t('auth.aiIdentityStaff') + (aiForm.orgCode ?'·'+ aiForm.orgCode :'')
    addMsg({
      side: 'ai', done: {
        type: selRef.current.type ==='personal'? t(tk('auth.aiPersonal')) : t(tk('auth.aiEnterprise')),
        identity,
        industry: kind ==='admin'? industryName : undefined,
        persona: selRef.current.personaName || undefined,
        username: aiForm.username,
        // ★ 2026-10-22：complete 模式没有邮箱字段——摘要卡只读展示登录邮箱
        // （企业分支随后就把它作为升级注册的账号邮箱原样发回后端）
        email: mode === 'complete' ? loginEmail : aiForm.email,
        inviteLeft: kind === 'personal' && mode === 'register',
        // ★ B4-d：只有 complete 那一支（个人账号升级/入职企业）才有一个"可以切回"的个人身份
        switchBack: mode === 'complete' && kind !== 'personal',
      },
    })
    scrollBottom()
  }, [addMsg, typeText, alive, reduced, t, tk, mode, loginEmail, aiForm.username, aiForm.email, aiForm.orgCn, aiForm.orgCode])

  // 选项点击：先在原位做一次确认（被点的提亮、其余压暗），再推进到下一步
  const onPick = useCallback(async (label: string, my: number, btn?: HTMLButtonElement | null) => {
    if (!alive(my)) return
    // 确认节拍 180ms：让「刚才点的是哪一项」先落一眼，再收走（原则 7：动作完成要有可见的交代）
    if (btn && !reduced) {
      btn.classList.add('ar-opt--picked')
      btn.parentElement?.classList.add('ar-opts--locked')
      await sleep(180)
      if (!alive(my)) return
    }
    addMsg({ side: 'user', text: label })
    const sel = selRef.current
    // 账号类型（★ 2026-10-22：四个判定全部经 tk——complete 模式选项标题已换成
    // 「个人资料/企业资料」，用注册语境的键去比会恒不中，点了没反应）
    const personalLabel = t(tk('auth.aiPersonal'))
    const enterpriseLabel = t(tk('auth.aiEnterprise'))
    if (label === personalLabel) {
      sel.type = 'personal'
      stepRender(2, null, t('auth.aiStepPersona'), (my) => askPersona(my))
      return
    }
    if (label === enterpriseLabel) {
      sel.type = 'enterprise'
      stepRender(2, null, enterpriseLabel, (my) => askRole(my))
      return
    }
    // 企业身份（label 形如 "企业注册 · 管理员（新建企业）"）
    if (label.startsWith(enterpriseLabel +'·')) {
      const roleLabel = label.slice((enterpriseLabel +'·').length)
      if (roleLabel.startsWith(t('auth.roleAdmin'))) {
        sel.role = 'admin'
        stepRender(3, 6, t('auth.industry'), (my) => askIndustry(my))
      } else {
        sel.role = 'staff'
        stepRender(3, 5, t('auth.aiStepPersona'), (my) => askPersona(my))
      }
      return
    }
    // 行业四选一：答完进角色问答
    const ind = INDUSTRIES.find((x) => t(x.t) === label)
    if (ind) {
      sel.industryName = t(ind.t)
      sel.industryCode = ind.code // 真实行业 code：后端按 kb_packages.code 精确命中
      stepRender(4, 6, t('auth.aiStepPersona'), (my) => askPersona(my))
      return
    }
    // 角色问答（2026-09-19）：选中记下 code+显示名，「暂不选择」落空值，答完统一推进
    if (label === t(tk('auth.aiSkipPersona'))) {
      sel.personaCode = ''
      sel.personaName = ''
      advanceAfterPersona(my)
      return
    }
    const per = personaList.find((x) => personaName(x.code, x.name, lang) === label)
    if (per) {
      sel.personaCode = per.code // 真实角色 code：后端按 kb_packages.code（persona 包）校验
      sel.personaName = per.name
      advanceAfterPersona(my)
    }
  }, [alive, addMsg, t, tk, stepRender, askAccount, askRole, askIndustry, askPersona, advanceAfterPersona, personaList, reduced, lang])

  // 账号信息表单提交：调用真实注册接口
  // 前置校验命中只 return、不弹提示：字段前有 * 必填标记，AI 气泡不该把用户已填内容冲掉。
  // 校验里的 setBusy(false) 属防御性复位（此时还没置忙），真正需要复位的是后面的接口失败分支
  const submitAccount = useCallback(async (my: number) => {
    if (!alive(my)) return
    if (!aiForm.username.trim()) { setBusy(false); return }
    // ★ 2026-10-21 IAM 重做 P1-1：长度走共享尺子（旧写法写死 6）
    if (aiForm.password.length < PWD_MIN_LEN) { setBusy(false); return }
    // ★ 2026-10-22 统一登录批：complete 模式没有邮箱/验证码两步——登录时邮箱已由
    //   login-purpose 验证码证明过（后端升级通道据此豁免 register-purpose 验码），
    //   字段也没渲染，校验必须跟着字段一起退场，否则永远卡在这两行。
    if (mode === 'register') {
      if (!aiForm.email.trim()) { setBusy(false); return }
      if (aiForm.emailCode.trim().length < 6) { setBusy(false); return }
    }
    // 协议勾选：注册模式全分支要；complete 模式只要企业分支要——
    // 企业分支走的是真注册（建新账号），个人补全只是给既有账号补资料。
    if (mode === 'register' || selRef.current.type === 'enterprise') {
      if (!aiForm.agreed) { setBusy(false); fail(t('auth.needAgree')); return }
    }
    // 人机验证前置：没 token 就别出网（出网必 403），提示一句并强制重新挑战领码
    if (captchaOn && !tsTokenRef.current) { fail(tsBlockMsg()); reexecTurnstile(tsIdRef.current); return }
    const sel = selRef.current
    const kind = sel.type ==='personal'?'personal': sel.role ==='admin'?'admin':'staff'
    // F-06 前置校验（对齐传统表单 Login.tsx 的 orgCodeRequired/inviteRequired）：
    // 管理员缺组织编码、员工缺企业邀请码都必吃后端 400，卡内字段前有 * 标记，故只拦不弹
    if (kind === 'admin' && !aiForm.orgCode.trim()) { setBusy(false); return }
    if (kind === 'staff' && !aiForm.orgCode.trim()) { setBusy(false); return }
    setFormErr('') // 每次提交先清上一轮红字：否则改完再提，旧错误会跟着新请求一起显示
    setBusy(true)
    const capTk = takeCaptchaToken()
    try {
      // ★ 2026-10-22 统一登录批：两种模式各自算出「登录结果」lr，下面共享收尾。
      //   变量名从 tk 改成 capTk：tk 已是 complete 模式的话术键映射器，重名会把它遮住。
      let lr: { success?: boolean; token?: string; user?: AuthUser } | null = null
      if (mode === 'complete') {
        if (kind === 'personal') {
          // 个人补全：profileSetup 给既有空白账号补资料（用户名/口令/职业角色），
          // 会话主体不变；接口按设计重签 token（设了口令会递增 token_version），
          // 必须消费新 token，否则下一个请求立刻 401。
          const r = await profileSetup({
            username: aiForm.username.trim(),
            password: aiForm.password,
            job_role: sel.personaCode || undefined, // 角色绑用户不绑企业（跳过=不下发）
          })
          if (!r.success) { setBusy(false); fail(r.message || t('auth.sendFail')); return }
          lr = {
            success: true,
            token: typeof r.token === 'string' ? r.token : '',
            user: (r.user && typeof r.user === 'object')
              ? r.user
              // 兜底：接口没回 user 时用当前登录用户改字段（完成/进入工作台都要有 id 的 AuthUser）
              : { ...(sessionUser as AuthUser), username: aiForm.username.trim(), profile_setup_required: 0 },
          }
        } else {
          // 企业补全＝升级注册：authRegister 带登录邮箱、**不带** email_code。
          // 后端 register.go 的升级通道三条件（JWT 调用者 + 邮箱是本人 + 本人是无口令空白账号）
          // 命中后豁免 register-purpose 验码、停用旧空白号、建新号并下发新会话；
          // 游客或他人同邮箱一律 409。authRegister 自带 authHeaders，JWT 随请求出网。
          const r = await authRegister({
            username: aiForm.username,
            password: aiForm.password,
            type: 'enterprise',
            role_choice: sel.role === 'admin' ? 'admin' : 'member',
            email: loginEmail.trim(),
            captcha_token: capTk || undefined, // ★ 2026-09-24：与发码各用一枚 token（升级通道只免验码，不免人机验证）
            // 组织英文名后端暂无独立字段，按交付约束不改动 @/api；此处仅取组织名/品牌用于提交
            name: kind ==='admin'? aiForm.orgCn.trim() : undefined,
            brand_name: kind ==='admin'? aiForm.orgCn.trim() : undefined,
            brand_name_en: kind ==='admin'? aiForm.orgEn.trim() : undefined,
            industry: kind ==='admin'? sel.industryCode : undefined,
            job_role: sel.personaCode || undefined, // 角色绑用户不绑企业：三条分支共用（跳过=不下发）
            invite: kind ==='staff'? aiForm.orgCode.trim() : undefined,
            code: kind ==='admin'? (aiForm.orgCode.trim() || undefined) : undefined, // F-06：管理员=新建租户，租户编码经组织编码字段下发（后端 register.go 无邀请绑定时必填）
            agreed: aiForm.agreed,
          })
          if (!r.success) { setBusy(false); fail(r.message || t('auth.sendFail')); return }
          // 升级注册必须换会话（旧空白号已停用）：没拿到 token/user 就不能进收尾
          if (!(typeof r.token === 'string' && r.token && r.user)) {
            setBusy(false); fail(t('auth.regOkLoginFail')); return
          }
          lr = { success: true, token: r.token, user: r.user }
        }
      } else {
        // 注册模式（租户/演示站，原逻辑）
        const r = await authRegister({
          username: aiForm.username,
          password: aiForm.password,
          type: sel.type ==='personal'?'personal':'enterprise',
          role_choice: sel.role ==='admin'?'admin': sel.role ==='staff'?'member': undefined,
          email: aiForm.email.trim(),
          email_code: aiForm.emailCode.trim(),
          captcha_token: capTk || undefined, // ★ 2026-09-24：与发码各用一枚 token（旧实现这里压根不下发，开验后 AI 注册整条断）
          // 组织英文名后端暂无独立字段，按交付约束不改动 @/api；此处仅取组织名/品牌用于提交
          name: kind ==='admin'? aiForm.orgCn.trim() : undefined,
          brand_name: kind ==='admin'? aiForm.orgCn.trim() : undefined,
          brand_name_en: kind ==='admin'? aiForm.orgEn.trim() : undefined,
          industry: kind ==='admin'? sel.industryCode : undefined,
          job_role: sel.personaCode || undefined, // 角色绑用户不绑企业：三条分支共用（跳过=不下发）
          invite: kind ==='staff'? aiForm.orgCode.trim() : undefined,
          code: kind ==='admin'? (aiForm.orgCode.trim() || undefined) : undefined, // F-06：管理员=新建租户，租户编码经组织编码字段下发（后端 register.go 无邀请绑定时必填）
          agreed: aiForm.agreed,
        })
        // 注册失败：表单留在原地可直接重提，但原因必须 toast——静默返回就是用户报的「点了没反应」
        // （人机验证类文案经 /api/auth/* 豁免链路透传后端原文，不再是误导性的「无管理权限」）
        if (!r.success) { setBusy(false); fail(r.message || t('auth.sendFail')); return }
        // 注册成功即登录（★ 2026-10-21 IAM 重做 P1-6）：
        // 服务端随注册响应直接下发会话，优先消费它——旧形态在这里**再调一次 login()**，
        // 而那正是 IAM 报障第 1 条的形态：注册用邮箱/口令落库，登录却只认用户名、
        // 且注册侧 trim 了口令而登录侧不 trim，只要用户前后敲的空格不一致就"注册成功但进不去"。
        // 只有服务端签发失败（响应里没有 token）那条兜档才回落到补登，且补登失败要**说清楚**，
        // 不能像旧形态那样静默 setBusy(false)（用户看到的是"填完了，什么也没发生"）。
        lr = (typeof r.token === 'string' && r.token && r.user)
          ? { success: true, token: r.token, user: r.user }
          : await login(aiForm.username, aiForm.password)
      }
      if (lr.success && lr.token && lr.user) {
        setAuthToken(lr.token)
        setActiveTenantId(0)
        // user 先存 ref 而不是立刻 onDone：用户要能读完检查点与摘要卡，再自己点「进入工作台」
        loggedUserRef.current = lr.user
        if (kind === 'admin') {
          // 管理员分支多一步：把按公司名预配的品牌固定译名摊出来（预配不能是黑箱）
          const mb = addMsg({ side:'ai', full: t('auth.aiBrandPreconfig'), text: reduced ? t('auth.aiBrandPreconfig') :''})
          if (await typeText(mb, t('auth.aiBrandPreconfig'), my)) {
            addMsg({ side: 'ai', chips: [
              { k: t('auth.aiBrandCnLabel'), v: aiForm.orgCn.trim() ||'—'},
              { k: t('auth.aiBrandEnLabel'), v: aiForm.orgEn.trim() ||'—'},
            ] })
            scrollBottom()
            await sleep(reduced ? 0 : 700) // 停一拍让 chips 被读完，再进收尾三连，否则峰值会被挤在一起
          }
        }
        await finish(my, kind, sel.industryName)
      } else {
        // 账号已经建成，只差进不去：这句必须显式出声（旧形态静默解锁表单，
        // 用户以为注册失败又提交一次，第二次必吃「用户名已存在」）
        setBusy(false)
        fail(t('auth.regOkLoginFail'))
      }
    } catch (e) {
      setBusy(false) // 接口异常一律转回可重试态
      // ★ 2026-09-24：旧写法静默吞异常，人机验证 403 时用户只看到「点了提交没反应」；
      //   现把失败原因显式 toast（后端文案经 publicErrMessage 脱敏，可直接面向用户）
      fail((e as Error).message || t('auth.sendFail'))
    }
  }, [alive, aiForm, t, mode, loginEmail, sessionUser, addMsg, typeText, reduced, finish, scrollBottom, captchaOn, takeCaptchaToken, fail])

  // 首个问题：账号类型（dedicatedRegister 直接走企业员工分支）
  // deps 只留挂载：面板每次挂上都是从第一步重来（不跨挂载续答），
  // 所以先 bump 代际熄灭上一条异步链，再清消息流与步骤栈
  useEffect(() => {
    genRef.current++
    setMsgs([])
    stepStack.current = []
    // 角色问答选项预取（公开接口，仅启用中角色）：失败保留本地兜底词库
    ;(async () => {
      try {
        const r = await registerPersonas()
        if (r.success && Array.isArray(r.personas) && r.personas.length > 0) setPersonaList(r.personas)
      } catch { /* ignore */ }
    })()
    if (dedicatedRegister) {
      // 品牌专属域名进来的用户身份已定（企业员工），类型/身份两问直接跳过，角色仍要一问（总步数 5）
      selRef.current = { type:'enterprise', role:'staff'}
      stepRender(1, 5, t('auth.aiStepPersona'), (my) => askPersona(my))
    } else {
      // 步骤名与首问同源过 tk：complete 模式下进度条要显示「完善信息」语境的步骤名
      stepRender(1, null, t(tk('auth.aiAskType')), askType)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <div className="ar-root">
      <style>{CSS_AR}</style>
      <div className="ar-panel">
      {/* 顶部栏 */}
      <div className="ar-nav">
        {/* ★ 2026-10-22：complete 模式的 × 不是「返回登录」而是「暂时跳过」
            （用户已登录，关掉面板只是本轮不补资料，账号照用） */}
        <button className="ar-close"type="button"aria-label={mode === 'complete' ? t('auth.skipForNow') : t('auth.backToLogin')} onClick={onClose}>
          <svg width="16"height="16"viewBox="0 0 24 24"fill="none"stroke="currentColor"strokeWidth="1.9"strokeLinecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
        </button>
        <span className="ar-nav-t">{t(tk('auth.aiTitle'))}</span>
        <span className="ar-online"><i />{t('auth.aiOnline')}</span>
      </div>
      {/* 进度（带名字） */}
      <div className="ar-prog">
        <div className="ar-prog-top">
          <span className="ar-prog-label">{stepLabel}</span>
          <button className="ar-back"type="button"hidden={backHidden} disabled={backDisabled} onClick={goBack}>{t('auth.aiBack')}</button>
        </div>
        {/* 进度条用整宽元素做负位移「填充」：transform 只走合成层，不像 width 那样每帧重排 */}
        <div className="ar-track"><i style={{ transform: `translateX(${-(100 - trackPct)}%)` }} /></div>
      </div>
      {/* 聊天流 */}
      <div className="ar-chat"ref={chatRef}>
        {msgs.map((m) => (
          <div key={m.id} className={`ar-msg ar-msg--${m.side}`}>
            {m.side ==='ai'&& (
              <div className="ar-who"><span className="ar-badge">AI</span><span>{branding.brandName || t(tk('auth.aiTitle'))}</span></div>
            )}
            <div className="ar-body">
              {m.text !== undefined && (
                <div className="ar-bubble">
                  {m.full && m.text !== m.full ? (
                    <span className="ar-type">
                      <span className="ar-ghost">{m.full}</span>
                      <span className="ar-real">{m.text}</span>
                      {m.typing && <span className="ar-caret"/>}
                    </span>
                  ) : (m.full ?? m.text)}
                </div>
              )}
              {/* 选项气泡：点击时现取 genRef.current 作为代际，而不是渲染时的快照——
                  中途发生过「上一步」的话，旧渲染闭包里的 my 已经作废，会把新点击误判成过期 */}
              {m.opts && (
                <div className="ar-opts">
                  {m.opts.map((o, i) => (
                    <button key={i} className="ar-opt lc-mo-up"type="button"disabled={busy}
                            style={{ animationDelay: `${i * 60}ms` }}
                            onClick={(e) => { void onPick(o.t, genRef.current, e.currentTarget) }}>
                      <span className="ar-opt-t">{o.t}</span>
                      <span className="ar-opt-d">{o.d}</span>
                    </button>
                  ))}
                </div>
              )}
              {/* 账号信息表单块：走到这一步答案已定，kind 直接从 ref 现取，不为它再加一个 state */}
              {m.form && (
                <AccountFormBlock
                  kind={selRef.current.type ==='personal'?'personal': selRef.current.role ==='admin'?'admin':'staff'}
                  mode={mode}
                  form={aiForm} setForm={setAiForm}
                  brought={usernameBrought}
                  errMsg={formErr}
                  codeSent={codeSent}
                  cooldown={codeCd.left > 0}
                  onSendCode={async () => {
                    if (!aiForm.email.trim()) return
                    // F-03：冷却期内双保险拦重发（按钮此时已 disabled，这里防住任何旁路触发）
                    if (codeCd.left > 0) return
                    // 开验但还没领到 token：明确告知要过人机验证（旧写法直接裸发 → 403 无提示）
                    if (captchaOn && !tsTokenRef.current) { fail(t('auth.captchaRequired')); reexecTurnstile(tsIdRef.current); return }
                    // F-02：乐观置位（与旧 ref 行为一致，请求在途即显示「在线」），失败分支回滚
                    setCodeSent(true)
                    const tk = takeCaptchaToken()
                    try {
                      const r = await sendEmailCode(aiForm.email.trim(), tk || undefined)
                      if (!r.success) {
                        setCodeSent(false) // 发码失败回滚标记：按钮回到「发送验证码」
                        codeCd.start()     // F-03：失败进 60s 冷却，禁止立刻连点刷验证码接口
                        fail(r.message || t('auth.sendCodeFail'))
                      }
                    } catch (e) {
                      setCodeSent(false)
                      codeCd.start() // F-03：异常同样落 60s 冷却（与业务失败同口径）
                      fail((e as Error).message || t('auth.sendCodeFail')) // E10：异常必须显式提示，未捕获拒绝就是「点了没反应」
                    }
                  }}
                  busy={busy}
                  onSubmit={() => submitAccount(genRef.current)}
                  tsOn={captchaOn} tsAttach={attachTs} tsNotice={tsNotice}
                  t={t}
                />
              )}
              {m.chips && (
                <div className="ar-chips">
                  {m.chips.map((c, i) => (
                    <span key={i} className="ar-chip lc-mo-up"style={{ animationDelay: `${i * 60}ms` }}>
                      <span className="ar-chip-k">{c.k}</span><span className="ar-chip-v">{c.v}</span>
                    </span>
                  ))}
                </div>
              )}
              {/* 检查点三连：亮几格由 finish() 逐格改写 ckpts 长度驱动，这里只按数组渲染 */}
              {m.ckpts && (
                <div className="ar-ckpts lc-mo-seq">
                  {m.ckpts.map((c, i) => (
                    <div key={i} className="ar-ckpt ar-ckpt--on lc-mo-up">
                      <span className="ar-ck lc-mo-pop"><svg width="10"height="10"viewBox="0 0 24 24"fill="none"stroke="#000"strokeWidth="3.2"strokeLinecap="round"strokeLinejoin="round"><path d="M4 12.5l5 5L20 6.5"/></svg></span>
                      <span>{c}</span>
                    </div>
                  ))}
                </div>
              )}
              {/* 完成摘要卡：行随分支裁剪（行业词库仅管理员有），「进入工作台」才真正交回上层 */}
              {m.done && (
                <div className="ar-done lc-mo-pop">
                  <div className="ar-done-t">{t(tk('auth.aiRegisterDone'))}</div>
                  <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryType')}</span><span className="ar-kv-v">{m.done.type}</span></div>
                  <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryIdentity')}</span><span className="ar-kv-v">{m.done.identity}</span></div>
                  {m.done.industry && <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryIndustry')}</span><span className="ar-kv-v">{m.done.industry}</span></div>}
                  {m.done.persona && <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryRole')}</span><span className="ar-kv-v">{m.done.persona}</span></div>}
                  <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryUsername')}</span><span className="ar-kv-v">{m.done.username}</span></div>
                  <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryEmail')}</span><span className="ar-kv-v">{m.done.email}</span></div>
                  {m.done.inviteLeft && <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryInvite')}</span><span className="ar-kv-v">{t('auth.aiInviteLeft')}</span></div>}
                  {/* ★ B4-d（R8）：企业身份的最后一行必须是"个人身份还在"——
                      这一族用户最怕的就是"进去以后我个人那份没了"。 */}
                  {m.done.switchBack && <div className="ar-done-note" data-testid="ai-switch-back">{t('auth.invite.switchBack')}</div>}
                  <button className="lc-btn lc-btn--secondary lc-btn--block"type="button"onClick={() => { if (loggedUserRef.current) onDone(loggedUserRef.current) }}>{t('auth.enterApp')}</button>
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
      </div>
    </div>
  )
}

// ---------- 账号信息表单块（真实可控输入，字段按分支裁剪） ----------
function AccountFormBlock({
  kind, mode = 'register', form, setForm, brought, codeSent, cooldown, onSendCode, busy, onSubmit, tsOn, tsAttach, tsNotice, t, errMsg,
}: {
  kind:'personal'|'staff'|'admin'
  /** ★ 2026-10-22 统一登录批：complete=登录后完善信息——邮箱/验证码两步隐退
   *  （登录时已验证过邮箱），个人分支连协议勾选也隐退（补资料不是注册） */
  mode?: 'register' | 'complete'
  form: { username: string; password: string; email: string; emailCode: string; orgCode: string; orgCn: string; orgEn: string; agreed: boolean }
  setForm: (f: typeof form) => void
  /** 用户名是否由登录页带入（仅影响提示文案，字段本身始终可编辑） */
  brought: boolean
  /** ★ 2026-10-22 P1-3：卡内错误位文案（与 toast 同源，由上层 fail() 一并写入） */
  errMsg?: string
  codeSent: boolean
  /** ★ F-03（2026-09-25 批G）：发码失败后的 60s 冷却期（true=冷却中，按钮禁用不可重发） */
  cooldown: boolean
  onSendCode: () => void
  busy: boolean
  onSubmit: () => void
  /** ★ 2026-09-24：人机验证开启时给 Turnstile 预留挂载容器（tsAttach 为回调 ref，widget 由上层持有与消费） */
  tsOn?: boolean
  tsAttach?: (el: HTMLDivElement | null) => void
  /** ★ D-02（2026-10-27）：装载提示由上层算好传下来（状态机在上层，这里只负责落位） */
  tsNotice?: ReactNode
  t: (k: string) => string
}) {
  // 单字段改写：form 由上层持有（提交时还要读），这里只回写不本地复制一份，免得两份状态打架
  const set = (k: keyof typeof form, v: string) => setForm({ ...form, [k]: v })
  // ★ 2026-10-21 IAM 重做 P1-3：口令可见切换（本块自持，不影响上层表单真值）
  const [showPwd, setShowPwd] = useState(false)
  // OTP 六格：真值只有 form.emailCode 一个字符串，六格是它的派生视图（不做 6 个 state）
  const codeChars = Array.from({ length: 6 }, (_, i) => form.emailCode[i] ?? '')
  const otpRefs = useRef<(HTMLInputElement | null)[]>([])
  const onOtp = (i: number, v: string) => {
    const dig = v.replace(/\D/g, '').slice(-1) // 只留最后一位数字：连打两次只认最新，不会出现「12」占一格
    const next = codeChars.slice()
    next[i] = dig
    set('emailCode', next.join(''))
    if (dig && i < 5) otpRefs.current[i + 1]?.focus() // 自动跳到下一格，免得一格格点
  }
  // 整串粘贴：拆掉非数字、截 6 位后直接落值，焦点移到粘贴位之后的那一格（不 preventDefault 会变成格内粘贴）
  const onOtpPaste = (e: React.ClipboardEvent) => {
    const txt = e.clipboardData.getData('text').replace(/\D/g, '').slice(0, 6)
    if (txt) { set('emailCode', txt); otpRefs.current[Math.min(txt.length, 5)]?.focus(); e.preventDefault() }
  }

  return (
    <div className="ar-form">
      <div className="ar-fgroup">
        {/* 用户名：登录页填过则带入预填，但始终可编辑（2026-09-18 用户裁定：
            带入=省一次输入，不是锁死；没填过也必须给填写机会） */}
        <span className="ar-flabel">{t('auth.fieldUsername')} <i>*</i>{brought && <i>{t('auth.aiUsernameBrought')}</i>}</span>
        <input className="lc-input"value={form.username} placeholder={t('auth.fieldUsername')}
               autoComplete="username" onChange={(e) => set('username', e.target.value)} />
      </div>
      <div className="ar-fgroup">
        <span className="ar-flabel">{t('auth.fieldPassword')} <i>*</i></span>
        {/* ★ 2026-10-21 IAM 重做 P1-3：AI 注册口令也配一枚可见切换
            （传统注册卡加了眼睛、AI 面板没有，等于同一条流程里有一步是盲输） */}
        <span className="ar-pwd">
          <input className="lc-input" type={showPwd ? 'text' : 'password'} autoComplete="new-password"
                 value={form.password} placeholder={t('auth.pwdPlaceholder')}
                 onChange={(e) => set('password', e.target.value)} />
          <button type="button" className="ar-pwd-eye" aria-pressed={showPwd}
                  aria-label={showPwd ? t('auth.hidePwd') : t('auth.showPwd')}
                  onClick={() => setShowPwd((v) => !v)}>
            {showPwd ? <EyeOffIcon /> : <EyeIcon />}
          </button>
        </span>
      </div>
      {/* ★ 2026-10-22：complete 模式没有邮箱字段——登录邮箱由上层经 loginEmail 直接带走，
          这里再要一次邮箱既是重复劳动，也会把「已验证过的邮箱」和「新邮箱」两种语义搅混 */}
      {mode !== 'complete' && (
        <div className="ar-fgroup">
          <span className="ar-flabel">{t('auth.fieldEmail')} <i>*</i></span>
          <input className="lc-input"value={form.email} placeholder="name@company.com"onChange={(e) => set('email', e.target.value)} />
        </div>
      )}
      {/* ★ 2026-09-24：人机验证容器。Turnstile widget 挂在邮箱与验证码之间——
          发码/提交都要吃这个 token，放在邮箱旁边语义最贴；未开启时整块不渲染，不留空档。
          ★ 2026-10-22：complete 模式邮箱/验证码隐退后容器前移到密码之后，
          企业分支的升级注册提交仍要吃这枚 token（升级通道只免验码，不免人机验证） */}
      {tsOn && <div className="ar-ts" ref={tsAttach} />}
      {tsNotice}
      {mode !== 'complete' && (
      <div className="ar-fgroup">
        <span className="ar-flabel">{t('auth.fieldEmailCode')} <i>*</i></span>
        <div className="ar-otp-row">
          <div className="ar-otp-cells"onPaste={onOtpPaste}>
            {codeChars.map((c, i) => (
              // ★ F-28（2026-09-25 批G）：旧拼接 `'ar-otp'+ (c ?'ar-otp--filled':'')` 缺空格，
              // filled 态实际类名是 "ar-otpar-otp--filled"（两个类都不命中、样式全丢）。
              // 改为三元直出两个完整类名字面量，filled 态 = 'ar-otp ar-otp--filled' 精确两词
              <input key={i} ref={(el) => { otpRefs.current[i] = el }} className={c ? 'ar-otp ar-otp--filled' : 'ar-otp'}
                     inputMode="numeric"maxLength={1}
                     value={c} onChange={(e) => onOtp(i, e.target.value)} />
            ))}
          </div>
          {/* 邮箱未填时禁用发送（没有邮箱无处投验证码）；已发码后借用状态词 aiOnline 显示，
              词典里暂无独立的「已发送」键，不在此硬编码中文。
              ★ F-03（2026-09-25 批G）：cooldown=发码失败后的 60s 冷却禁用，与传统表单同口径 */}
          <button className="lc-btn lc-btn--secondary"type="button"disabled={!form.email.trim() || busy || cooldown}
                  onClick={onSendCode}>{codeSent ? t('auth.aiOnline') : t('auth.sendCode')}</button>
        </div>
      </div>
      )}
      {kind ==='staff'&& (
        <div className="ar-fgroup">
          {/* ★ §6.4（2026-10-25 对账批）：成员这一格填的是**企业邀请码**（落 invite_codes 凭证），
              过去挂 auth.aiOrgCode「组织编码（加入企业时必填）」，与管理员那格逐字同文＝byd 案例同族混淆。 */}
          <span className="ar-flabel">{t('auth.orgInviteCode')} <i>*</i></span>
          <input className="lc-input"value={form.orgCode} placeholder={t('auth.orgCodeStaffPlaceholder')} onChange={(e) => set('orgCode', e.target.value)} />
        </div>
      )}
      {kind ==='admin'&& (
        <>
          {/* F-06（2026-09-25 发布前 UAT）：管理员=新建租户，后端 register.go 无邀请绑定时 code 缺失即 400
              「请提供租户编码」——传统表单一直有该字段与前置校验（Login.tsx），AI 流此前漏搬，企业注册主路径必死。
              标签复用既有键 auth.orgCode；★ §6.4（2026-10-25）占位改走 auth.orgCodeAdminPlaceholder，
              与成员那格「企业邀请码」明文区分（这一格填的是企业代号，落 tenants.code）。 */}
          <div className="ar-fgroup">
            <span className="ar-flabel">{t('auth.orgCode')} <i>*</i></span>
            <input className="lc-input"value={form.orgCode} placeholder={t('auth.orgCodeAdminPlaceholder')} onChange={(e) => set('orgCode', e.target.value)} />
          </div>
          <div className="ar-fgroup">
            <span className="ar-flabel">{t('auth.aiOrgCn')} <i>*</i></span>
            <input className="lc-input"value={form.orgCn} placeholder={t('auth.orgCnPlaceholder')} onChange={(e) => set('orgCn', e.target.value)} />
          </div>
          <div className="ar-fgroup">
            <span className="ar-flabel">{t('auth.aiOrgEn')} <i>*</i></span>
            <input className="lc-input"value={form.orgEn} placeholder="e.g. Huachuang Logistics"onChange={(e) => set('orgEn', e.target.value)} />
          </div>
        </>
      )}
      {/* ★ 2026-10-21 IAM 重做 P1-4：协议勾选与传统表单同形——同一款 Checkbox＋同一组
          条款链接文案（12 语种词典已有 agreeTerms/userAgreement/privacyPolicy，零新增键）。
          旧形态这里没有勾选、提交硬编码 agreed:true，等于把"我同意"替用户点了。
          ★ 2026-10-22：complete 模式的个人分支隐退勾选——给既有账号补资料不是注册，
          没有新协议要同意；企业分支保留（升级注册建的是新账号，同意闸照过）。 */}
      {!(mode === 'complete' && kind === 'personal') && (
        <Checkbox
          checked={form.agreed}
          onChange={(e) => setForm({ ...form, agreed: e.target.checked })}
          label={(
            <>
              {t('auth.agreeTerms')}{''}
              <a href="/docs/terms" target="_blank" rel="noreferrer" className="ar-link">{t('auth.userAgreement')}</a>
              {' & '}
              <a href="/docs/privacy" target="_blank" rel="noreferrer" className="ar-link">{t('auth.privacyPolicy')}</a>
            </>
          )}
        />
      )}
      {/* ★ 2026-10-22 一致性核查修复（P1-3）：卡内红字错误位，与 toast 同源。
          贴提交钮上方而不是表单顶部——用户点完按钮目光就落在这里，不必回滚去找提示。
          inline style 兜底同 ProfileSetupGate：CSS_AUTH 里的 .auth-err 只在登录面加载，
          本面板走 CSS_AR，不兜底就会"类名在、样式无"。 */}
      {!!errMsg && (
        <div className="auth-err" style={{ color: 'var(--lc-danger)', fontSize: 15, lineHeight: 1.6 }}>{errMsg}</div>
      )}
      {/* 提交期间禁用并转圈：注册是「有副作用的一次性动作」，重复点击会再发一次注册请求。
          ★ 2026-10-22：complete 模式提交钮换成「保存并进入」（auth.profileDone，
          与旧资料弹窗同一个键）——完善信息不是"注册并登录"，用户本来就登录着 */}
      <button className={'lc-btn lc-btn--primary lc-btn--block'+ (busy ?'ar-busy':'')} type="button"disabled={busy}
              onClick={onSubmit}>
        {busy && <span className="ar-spin"/>}{busy ? t('auth.aiSubmitting') : (mode === 'complete' ? t('auth.profileDone') : t('auth.registerAndLogin'))}
      </button>
    </div>
  )
}

// ---------- 作用域样式（ar- 前缀，避免与组件库类名重名；§2.5） ----------
const CSS_AR = `
.ar-root{position:fixed;inset:0;z-index:60;display:grid;place-items:center;padding:16px;background:rgba(0,0,0,.72);color:var(--lc-text);font-family:var(--lc-font);animation:lc-mo-fade var(--lc-mo-enter) var(--lc-mo-out) both;}
/* ★ 比例按演示稿（demo-register-ai-motion.html 的 .phone）：竖版卡、
   高 min(780, 100dvh-32)、圆角 28、居中 —— 不是全屏接管（2026-09-18 用户裁定）。
   ★ 2026-10-21 IAM 重做 P1-2：宽 390 → 400，与三张认证卡同档（AuthCard 交付档 400）。
   同一段注册流程里卡片宽度来回跳 10px，用户看到的是"面板换了一张"，
   而交付稿的认证面本来就只有一档宽度。 */
.ar-panel{width:400px;max-width:100%;height:min(780px,calc(100dvh - 32px));min-height:520px;display:flex;flex-direction:column;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:28px;overflow:hidden;box-shadow:0 24px 64px rgba(0,0,0,.55);animation:lc-mo-pop var(--lc-mo-enter) var(--lc-mo-out) both;--lc-mo-origin:50% 92%;}
@media (max-width:640px){ /* ★ 批3 断点归一：480 → 640（S 档上缘） */
  .ar-root{padding:0;background:var(--lc-bg);}
  .ar-panel{width:100%;height:100dvh;min-height:0;border:0;border-radius:0;box-shadow:none;}
}
.ar-nav{height:52px;flex:none;display:flex;align-items:center;gap:12px;padding:0 16px;border-bottom:1px solid var(--lc-border-faint);}
.ar-close{display:flex;align-items:center;justify-content:center;width:28px;height:28px;border-radius:8px;background:none;border:0;color:var(--lc-text);cursor:pointer;}
.ar-nav-t{font-size:18px;font-weight:600;color:var(--lc-text);flex:1;}
.ar-online{display:flex;align-items:center;gap:6px;font-size:13px;color:var(--lc-text-3);}
.ar-online i{width:7px;height:7px;border-radius:50%;background:var(--lc-text);box-shadow:0 0 0 3px rgba(255,255,255,.08);}
.ar-prog{flex:none;display:flex;flex-direction:column;gap:9px;padding:14px 16px 12px;border-bottom:1px solid var(--lc-border-faint);}
.ar-prog-top{display:flex;align-items:center;justify-content:space-between;gap:12px;}
.ar-prog-label{font-size:14px;color:var(--lc-text-3);}
.ar-prog-label b{color:var(--lc-text);font-weight:600;}
.ar-back{flex:none;font-size:13px;padding:4px 11px;border-radius:999px;background:var(--lc-raised);border:1.2px solid var(--lc-border-pill);color:var(--lc-text-2);cursor:pointer;font-family:var(--lc-font);}
.ar-back:active{transform:scale(.97);}
.ar-back[disabled]{opacity:.5;cursor:not-allowed;}
.ar-track{height:2px;border-radius:1px;background:var(--lc-border-faint);overflow:hidden;}
.ar-track i{display:block;height:100%;width:100%;background:var(--lc-text-2);transform:translateX(-101%);transition:transform .5s cubic-bezier(.32,.72,.28,1);}
.ar-chat{flex:1;min-height:0;overflow-y:auto;padding:20px 16px;display:flex;flex-direction:column;gap:20px;scrollbar-width:none;}
.ar-chat::-webkit-scrollbar{display:none;}
.ar-msg{display:flex;flex-direction:column;opacity:0;transform:translateY(8px);animation:ar-in .36s cubic-bezier(.32,.72,.28,1) forwards;}
.ar-msg--user{align-items:flex-end;}
.ar-who{display:flex;align-items:center;gap:8px;font-size:13px;color:var(--lc-text-4);margin-bottom:8px;}
.ar-badge{width:20px;height:20px;border-radius:7px;background:var(--lc-raised);border:1.2px solid var(--lc-border-card);display:flex;align-items:center;justify-content:center;font-family:var(--lc-font-mono);font-size:11px;font-weight:600;color:var(--lc-text);}
.ar-body{display:flex;flex-direction:column;gap:10px;}
.ar-msg--ai .ar-body{margin-inline-start:28px;}
.ar-bubble{padding:12px 14px;border-radius:14px;font-size:15.5px;line-height:1.7;}
.ar-msg--ai .ar-bubble{background:var(--lc-inset);border:1.2px solid var(--lc-border-card);color:var(--lc-text-2);border-top-left-radius:6px;align-self:stretch;}
.ar-msg--user .ar-bubble{background:#FFFFFF;color:#000;font-weight:500;border-top-right-radius:6px;}
.ar-type{position:relative;display:block;}
.ar-type .ar-ghost{visibility:hidden;}
.ar-type .ar-real{position:absolute;inset:0;}
.ar-caret{display:inline-block;width:1px;height:1em;background:var(--lc-text-3);vertical-align:-.15em;margin-inline-start:2px;animation:ar-caret 1s steps(1) infinite;}
@keyframes ar-caret{50%{opacity:0;}}
.ar-opts{display:flex;flex-direction:column;gap:8px;}
.ar-opt{display:flex;flex-direction:column;gap:4px;padding:12px 14px;border-radius:12px;text-align:start;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);cursor:pointer;font-family:var(--lc-font);
  transition:border-color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out),opacity var(--lc-mo-sink) var(--lc-mo-out);}
.ar-opt:hover:not(:disabled){border-color:var(--lc-border-pill);}
.ar-opt:disabled{opacity:.6;cursor:not-allowed;}
/* 选项确认：被点中的提亮并留在原位、其余压暗退场 —— 一次看得见的交代（原则 7） */
.ar-opt--picked{border-color:var(--lc-fill-white);background:var(--lc-raised);}
.ar-opts--locked{pointer-events:none;}
.ar-opts--locked .ar-opt:not(.ar-opt--picked){opacity:.34;}
.ar-opt-t{font-size:15px;font-weight:500;color:var(--lc-text);}
.ar-opt-d{font-size:13px;color:var(--lc-text-4);line-height:1.5;}
.ar-chips{display:flex;flex-wrap:wrap;gap:8px;}
.ar-chip{display:flex;align-items:baseline;gap:6px;padding:7px 11px;border-radius:8px;background:var(--lc-raised);border:1.2px solid var(--lc-border-card);}
.ar-chip-k{font-size:12.5px;color:var(--lc-text-4);}
.ar-chip-v{font-size:14px;color:var(--lc-text);font-weight:500;}
.ar-form{display:flex;flex-direction:column;gap:14px;padding:20px 16px;border-radius:14px;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);box-shadow:var(--lc-panel-highlight);align-self:stretch;}
.ar-fgroup{display:flex;flex-direction:column;gap:8px;}
/* ★ P1-3：口令框 + 右端眼睛（与认证卡 .auth-pwd-eye 同形态同尺寸，只换定位方式） */
.ar-pwd{position:relative;display:block;}
.ar-pwd .lc-input{width:100%;padding-inline-end:38px;box-sizing:border-box;}
.ar-pwd-eye{position:absolute;inset-inline-end:6px;top:50%;transform:translateY(-50%);display:flex;align-items:center;justify-content:center;background:none;border:0;color:var(--lc-text-3);cursor:pointer;padding:4px;line-height:0;}
.ar-pwd-eye:hover{color:var(--lc-text);}
/* Turnstile 挂载容器：widget 固定 300px 宽，居中摆放；预留 65px 高度，
   避免脚本晚到时表单内容突然下移（AI 面板是逐条追加的，跳动很显眼） */
/* ★ D-02：widget 固有宽 300px，容器必须能收缩（选档失效时也不许把面板顶出横向滚动）；
   装载提示占一行小字，用现成的次级文字档，不新造色。 */
.ar-ts{display:flex;justify-content:center;min-height:65px;max-width:100%;overflow-x:auto;}
.ar-ts-note{margin:0;color:var(--lc-text-3);font-size:14px;line-height:1.5;display:flex;align-items:center;gap:8px;flex-wrap:wrap;justify-content:center;}
.ar-flabel{font-size:14px;color:var(--lc-text-3);}
.ar-flabel i{font-style:normal;color:var(--lc-text-4);font-size:13px;}
/* ★ 2026-10-21 IAM 重做 P1-4：协议勾选链接与登录/传统注册卡的 .auth-link 同档（次级文字色、悬停下划线） */
.ar-link{background:none;border:0;color:var(--lc-text-3);font-size:14px;cursor:pointer;font-family:var(--lc-font);text-decoration:none;}
.ar-link:hover{color:var(--lc-text);text-decoration:underline;}
.ar-otp-row{display:flex;gap:8px;align-items:center;min-width:0;}
.ar-otp-cells{display:flex;gap:7px;flex:1;min-width:0;}
/* ★ min-width:0 必须给：<input> 的默认尺寸（size≈20 字符 ≈170px）来自内容，
   flex 子项默认 min-width:auto 会拒绝收缩 —— 六格在 390 宽上直接撑破表单，
   实测整行右缘溢出卡片 126px、页面出现横向滚动。 */
.ar-otp{flex:1;min-width:0;width:100%;height:46px;border-radius:10px;background:var(--lc-inset);border:1.2px solid var(--lc-border-input);color:var(--lc-text);text-align:center;font-family:var(--lc-font-latin);font-size:18px;font-weight:600;
  transition:border-color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out);}
/* 窄屏：发送按钮让出整行，六格拿满宽度（390 下格子能到 ~44px，否则只有 24px 太挤） */
@media (max-width: 640px){ /* ★ 批3 断点归一：480 → 640（S 档上缘） */
  .ar-otp-row{flex-wrap:wrap;}
  .ar-otp-cells{flex:1 1 100%;}
  .ar-otp-row > .lc-btn{flex:1 1 100%;justify-content:center;}
}
.ar-otp:focus{outline:none;border-color:var(--lc-border-strong);}
/* 已填格：描边提到完成态、底提一档，六格填满时整排能一眼读出来 */
.ar-otp--filled{border-color:var(--lc-border-done);background:var(--lc-raised);}
.ar-ckpts{display:flex;flex-direction:column;gap:10px;padding:14px;border-radius:12px;background:var(--lc-inset);border:1.2px solid var(--lc-border-card);}
.ar-ckpt{display:flex;align-items:center;gap:10px;font-size:15px;color:var(--lc-text-2);}
.ar-ck{width:18px;height:18px;border-radius:50%;background:var(--lc-text);border:1.2px solid var(--lc-text);display:flex;align-items:center;justify-content:center;flex:none;}
.ar-done{display:flex;flex-direction:column;gap:12px;padding:20px 16px;border-radius:14px;background:var(--lc-panel);border:1.2px solid var(--lc-border-done);box-shadow:var(--lc-panel-highlight);align-self:stretch;}
.ar-done-t{font-size:15px;font-weight:600;color:var(--lc-text);}
/* ★ B4-d（§6.5 第 4 条「受邀落地页告知」）：完成摘要卡里的「可切回个人身份」一句。
   与 .ar-done 卡同色阶、只做次级说明，抬字号会抢过上面的身份/邮箱读数。 */
.ar-done-note{font-size:14px;color:var(--lc-text-3);line-height:1.5;}
.ar-kv{display:flex;gap:12px;font-size:14px;line-height:1.5;}
.ar-kv-k{width:72px;flex:none;color:var(--lc-text-4);}
.ar-kv-v{color:var(--lc-text-2);font-family:var(--lc-font-latin);}
.ar-busy{color:var(--lc-text-2)!important;pointer-events:none;}
.ar-spin{display:inline-block;width:12px;height:12px;border:1.2px solid var(--lc-border-faint);border-top-color:var(--lc-text-2);border-radius:50%;margin-inline-end:8px;vertical-align:-2px;animation:ar-spin .8s linear infinite;}
@keyframes ar-spin{to{transform:rotate(360deg);}}
@keyframes ar-in{to{opacity:1;transform:none;}}
@media (prefers-reduced-motion: reduce){
  .ar-msg{animation:none!important;opacity:1!important;transform:none!important;}
  .ar-track i{transition:none!important;}
  .ar-caret,.ar-spin{animation:none!important;}
  .ar-root,.ar-panel{animation:none!important;}
}
`
