// ============================================================================
// components/AiRegisterFlow.tsx — AI 接管注册引导（交付包 §5 / demo-register-ai-motion.html）
// 点「免费注册」后传统表单闪烁三次并整页 120ms 退出，由本面板接管问答：
//   ① 账号类型 ② 企业→企业身份 ③ 管理员→行业 4 选 1 →（各分支）职业角色问答（2026-09-19）
//     ④ 按分支裁剪的账号信息表单
//     （用户名自动带入 + OTP 6 格 + 管理员组织中英名 / 员工组织编码）
//   ⑤ 管理员提交后追加品牌固定译名预配 chips ⑥ 检查点三连 + 完成摘要卡
// 支持「← 上一步」（首步隐藏、完成后禁用）；尊重 prefers-reduced-motion；generation 计数器防竞态。
// 业务逻辑复用 @/api 的 authRegister / login / sendEmailCode，仅重写呈现与交互。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { authRegister, login, sendEmailCode, setAuthToken, setActiveTenantId, type AuthUser } from '@/api'
import { registerPersonas } from '@/api/persona'
import { PERSONA_FALLBACK } from '@/lib/personas'
import { useT } from '@/i18n'
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

/**
 * AiRegisterFlow 入参：
 * prefillUsername 登录页已输入的用户名（带入但不锁）；dedicatedRegister 品牌专属域名入口
 * （跳过问答直落企业员工分支）；onDone 注册并自动登录成功后进工作台；onClose 退回传统表单。
 */
interface Props {
  prefillUsername: string
  dedicatedRegister: boolean
  onDone: (u: AuthUser) => void
  onClose: () => void
}

/**
 * AI 接管注册引导面板（390 宽竖版卡，不是全屏接管）：
 * 用问答 + 一个必要表单替用户跑完注册，最后调 authRegister/login 两个真实接口。
 * 步骤是命令式「渲染函数」组成的栈（stepStack），上一步靠截断消息重放，而不是路由或状态机库。
 */
export default function AiRegisterFlow({ prefillUsername, dedicatedRegister, onDone, onClose }: Props) {
  const [, t] = useT()
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
  // AI 流程内的账号信息表单（与传统注册表单字段有意不共用：这里只有 4~6 项）
  const [aiForm, setAiForm] = useState({
    username: prefillUsername, password: '', email: '', emailCode: '', orgCode: '', orgCn: '', orgEn: '',
  })
  // 登录页带没带用户名：只决定提示文案（「已带入，可修改」），不锁字段
  const usernameBrought = prefillUsername.trim().length > 0
  const codeSentRef = useRef(false) // 已发码标记：用 ref 是因为它只影响按钮文案，不该触发整条聊天流重渲染
  const loggedUserRef = useRef<AuthUser | null>(null) // 注册后自动登录拿到的 user，留到「进入工作台」按钮再用

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
    for (let i = 0; i < text.length; i++) {
      if (!alive(my)) return false
      const slice = text.slice(0, i + 1)
      setMsgs((s) => s.map((m) => (m.id === id ? { ...m, text: slice, typing: true } : m)))
      scrollBottom()
      await sleep(24)
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
    setStepLabel(total ? `第 ${idx} / ${total} 步 · ${name}` : `第 ${idx} 步 · ${name}`)
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
    const m = addMsg({ side: 'ai', full: t('auth.aiIntro'), text: reduced ? t('auth.aiIntro') : '', opts: undefined })
    void typeText(m, t('auth.aiIntro'), my).then((ok) => {
      if (!ok || !alive(my)) return
      const m2 = addMsg({ side:'ai', full: t('auth.aiAskType'), text: reduced ? t('auth.aiAskType') :''})
      void typeText(m2, t('auth.aiAskType'), my).then((ok2) => {
        if (!ok2 || !alive(my)) return
        addMsg({
          side: 'ai', opts: [
            { t: t('auth.aiPersonal'), d: t('auth.aiPersonalDesc') },
            { t: t('auth.aiEnterprise'), d: t('auth.aiEnterpriseDesc') },
          ],
        })
        // 选项点击在渲染时绑定（见 renderMsg 的 opts 处理）
      })
    })
  }, [addMsg, typeText, alive, reduced, t])

  const askRole = useCallback((my: number) => {
    const m = addMsg({ side:'ai', full: t('auth.aiAskRole'), text: reduced ? t('auth.aiAskRole') :''})
    void typeText(m, t('auth.aiAskRole'), my).then((ok) => {
      if (!ok || !alive(my)) return
      addMsg({
        side: 'ai', opts: [
          { t: t('auth.aiEnterprise') +'·'+ t('auth.roleAdmin'), d: t('auth.aiRoleAdminDesc') },
          { t: t('auth.aiEnterprise') +'·'+ t('auth.roleStaff'), d: t('auth.aiRoleStaffDesc') },
        ],
      })
    })
  }, [addMsg, typeText, alive, reduced, t])

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
          ...personaList.map((x) => ({ t: x.name, d: '' })),
          { t: t('auth.aiSkipPersona'), d: t('auth.aiSkipPersonaDesc') },
        ],
      })
    })
  }, [addMsg, typeText, alive, reduced, t, personaList])

  const askAccount = useCallback((my: number, kind:'personal'|'staff'|'admin') => {
    // 带没带用户名用不同话术：带了说「已带入（可修改）」，没带就请用户在本步填写
    const kindKey = kind ==='personal'?'Personal': kind ==='admin'?'Admin':'Staff'
    const tailKey = usernameBrought ? `auth.aiTail${kindKey}` : `auth.aiTail${kindKey}Plain`
    const m = addMsg({ side:'ai', full: t(tailKey), text: reduced ? t(tailKey) :''})
    void typeText(m, t(tailKey), my).then((ok) => {
      if (!ok || !alive(my)) return
      addMsg({ side: 'ai', form: true })
    })
  }, [addMsg, typeText, alive, reduced, t, usernameBrought])

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
    setStepLabel(`第 ${total} / ${total} 步 · ${t('auth.aiRegisterDone')}`)
    setTrackPct(100)
    const finishKey = kind ==='personal'?'auth.aiFinishingPersonal': kind ==='admin'?'auth.aiFinishingAdmin':'auth.aiFinishingStaff'
    // 管理员话术里嵌了行业名：t() 不做参数插值，这里手动替换 {industry} 占位
    const finishText = kind ==='admin'&& industryName ? t('auth.aiFinishingAdmin').replace('{industry}', industryName) : t(finishKey)
    const m = addMsg({ side:'ai', full: finishText, text: reduced ? finishText :''})
    if (!await typeText(m, finishText, my)) return
    const ckNames = kind === 'personal'
      ? [t('auth.ckPersonal1'), t('auth.ckPersonal2'), t('auth.ckPersonal3')]
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
    const d2 = addMsg({ side:'ai', full: t('auth.aiDoneChat'), text: reduced ? t('auth.aiDoneChat') :''})
    if (!await typeText(d2, t('auth.aiDoneChat'), my)) return
    const identity = kind ==='personal'? t('auth.aiIdentityPersonal')
      : kind ==='admin'? t('auth.aiIdentityAdmin') + (aiForm.orgCn ?'·'+ aiForm.orgCn :'')
        : t('auth.aiIdentityStaff') + (aiForm.orgCode ?'·'+ aiForm.orgCode :'')
    addMsg({
      side: 'ai', done: {
        type: selRef.current.type ==='personal'? t('auth.aiPersonal') : t('auth.aiEnterprise'),
        identity,
        industry: kind ==='admin'? industryName : undefined,
        persona: selRef.current.personaName || undefined,
        username: aiForm.username,
        email: aiForm.email,
        inviteLeft: kind === 'personal',
      },
    })
    scrollBottom()
  }, [addMsg, typeText, alive, reduced, t, aiForm.username, aiForm.email, aiForm.orgCn, aiForm.orgCode])

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
    // 账号类型
    if (label === t('auth.aiPersonal')) {
      sel.type = 'personal'
      stepRender(2, null, t('auth.aiStepPersona'), (my) => askPersona(my))
      return
    }
    if (label === t('auth.aiEnterprise')) {
      sel.type = 'enterprise'
      stepRender(2, null, t('auth.aiEnterprise'), (my) => askRole(my))
      return
    }
    // 企业身份（label 形如 "企业注册 · 管理员（新建企业）"）
    if (label.startsWith(t('auth.aiEnterprise') +'·')) {
      const roleLabel = label.slice((t('auth.aiEnterprise') +'·').length)
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
    if (label === t('auth.aiSkipPersona')) {
      sel.personaCode = ''
      sel.personaName = ''
      advanceAfterPersona(my)
      return
    }
    const per = personaList.find((x) => x.name === label)
    if (per) {
      sel.personaCode = per.code // 真实角色 code：后端按 kb_packages.code（persona 包）校验
      sel.personaName = per.name
      advanceAfterPersona(my)
    }
  }, [alive, addMsg, t, stepRender, askAccount, askRole, askIndustry, askPersona, advanceAfterPersona, personaList, reduced])

  // 账号信息表单提交：调用真实注册接口
  // 前置校验命中只 return、不弹提示：字段前有 * 必填标记，AI 气泡不该把用户已填内容冲掉。
  // 校验里的 setBusy(false) 属防御性复位（此时还没置忙），真正需要复位的是后面的接口失败分支
  const submitAccount = useCallback(async (my: number) => {
    if (!alive(my)) return
    if (!aiForm.username.trim()) { setBusy(false); return }
    if (aiForm.password.length < 6) { setBusy(false); return }
    if (!aiForm.email.trim()) { setBusy(false); return }
    if (aiForm.emailCode.trim().length < 6) { setBusy(false); return }
    const sel = selRef.current
    const kind = sel.type ==='personal'?'personal': sel.role ==='admin'?'admin':'staff'
    setBusy(true)
    try {
      const r = await authRegister({
        username: aiForm.username,
        password: aiForm.password,
        type: sel.type ==='personal'?'personal':'enterprise',
        role_choice: sel.role ==='admin'?'admin': sel.role ==='staff'?'member': undefined,
        email: aiForm.email.trim(),
        email_code: aiForm.emailCode.trim(),
        // 组织英文名后端暂无独立字段，按交付约束不改动 @/api；此处仅取组织名/品牌用于提交
        name: kind ==='admin'? aiForm.orgCn.trim() : undefined,
        brand_name: kind ==='admin'? aiForm.orgCn.trim() : undefined,
        brand_name_en: kind ==='admin'? aiForm.orgEn.trim() : undefined,
        industry: kind ==='admin'? sel.industryCode : undefined,
        job_role: sel.personaCode || undefined, // 角色绑用户不绑企业：三条分支共用（跳过=不下发）
        invite: kind ==='staff'? aiForm.orgCode.trim() : undefined,
        agreed: true, // 问答流程里没有单独的协议勾选步骤（只有传统表单有），提交即视为已同意
      })
      if (!r.success) { setBusy(false); return } // 注册失败：表单留在原地、不追加失败气泡，用户改完可直接重提
      // 注册成功自动登录
      const lr = await login(aiForm.username, aiForm.password)
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
        setBusy(false) // 注册成功但自动登录失败：只解锁表单，让用户自己回登录屏登（不重复建号）
      }
    } catch {
      setBusy(false) // 接口异常一律转回可重试态：AI 面板不抛红字，避免把半成品流程演成报错页
    }
  }, [alive, aiForm, t, addMsg, typeText, reduced, finish, scrollBottom])

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
      stepRender(1, null, t('auth.aiAskType'), askType)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <div className="ar-root">
      <style>{CSS_AR}</style>
      <div className="ar-panel">
      {/* 顶部栏 */}
      <div className="ar-nav">
        <button className="ar-close"type="button"aria-label={t('auth.backToLogin')} onClick={onClose}>
          <svg width="16"height="16"viewBox="0 0 24 24"fill="none"stroke="currentColor"strokeWidth="1.9"strokeLinecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg>
        </button>
        <span className="ar-nav-t">{t('auth.aiTitle')}</span>
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
              <div className="ar-who"><span className="ar-badge">AI</span><span>{branding.brandName || t('auth.aiTitle')}</span></div>
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
                  form={aiForm} setForm={setAiForm}
                  brought={usernameBrought}
                  codeSent={codeSentRef.current}
                  onSendCode={async () => {
                    if (!aiForm.email.trim()) return
                    codeSentRef.current = true
                    const r = await sendEmailCode(aiForm.email.trim())
                    if (!r.success) codeSentRef.current = false // 发码失败回滚标记：按钮回到「发送验证码」，允许重试
                  }}
                  busy={busy}
                  onSubmit={() => submitAccount(genRef.current)}
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
                  <div className="ar-done-t">{t('auth.aiRegisterDone')}</div>
                  <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryType')}</span><span className="ar-kv-v">{m.done.type}</span></div>
                  <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryIdentity')}</span><span className="ar-kv-v">{m.done.identity}</span></div>
                  {m.done.industry && <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryIndustry')}</span><span className="ar-kv-v">{m.done.industry}</span></div>}
                  {m.done.persona && <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryRole')}</span><span className="ar-kv-v">{m.done.persona}</span></div>}
                  <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryUsername')}</span><span className="ar-kv-v">{m.done.username}</span></div>
                  <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryEmail')}</span><span className="ar-kv-v">{m.done.email}</span></div>
                  {m.done.inviteLeft && <div className="ar-kv"><span className="ar-kv-k">{t('auth.aiSummaryInvite')}</span><span className="ar-kv-v">{t('auth.aiInviteLeft')}</span></div>}
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
  kind, form, setForm, brought, codeSent, onSendCode, busy, onSubmit, t,
}: {
  kind:'personal'|'staff'|'admin'
  form: { username: string; password: string; email: string; emailCode: string; orgCode: string; orgCn: string; orgEn: string }
  setForm: (f: typeof form) => void
  /** 用户名是否由登录页带入（仅影响提示文案，字段本身始终可编辑） */
  brought: boolean
  codeSent: boolean
  onSendCode: () => void
  busy: boolean
  onSubmit: () => void
  t: (k: string) => string
}) {
  // 单字段改写：form 由上层持有（提交时还要读），这里只回写不本地复制一份，免得两份状态打架
  const set = (k: keyof typeof form, v: string) => setForm({ ...form, [k]: v })
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
        <input className="lc-input"type="password"value={form.password} placeholder={t('auth.pwdPlaceholder')}
               onChange={(e) => set('password', e.target.value)} />
      </div>
      <div className="ar-fgroup">
        <span className="ar-flabel">{t('auth.fieldEmail')} <i>*</i></span>
        <input className="lc-input"value={form.email} placeholder="name@company.com"onChange={(e) => set('email', e.target.value)} />
      </div>
      <div className="ar-fgroup">
        <span className="ar-flabel">{t('auth.fieldEmailCode')} <i>*</i></span>
        <div className="ar-otp-row">
          <div className="ar-otp-cells"onPaste={onOtpPaste}>
            {codeChars.map((c, i) => (
              <input key={i} ref={(el) => { otpRefs.current[i] = el }} className={'ar-otp'+ (c ?'ar-otp--filled':'')}
                     inputMode="numeric"maxLength={1}
                     value={c} onChange={(e) => onOtp(i, e.target.value)} />
            ))}
          </div>
          {/* 邮箱未填时禁用发送（没有邮箱无处投验证码）；已发码后借用状态词 aiOnline 显示，
              词典里暂无独立的「已发送」键，不在此硬编码中文 */}
          <button className="lc-btn lc-btn--secondary"type="button"disabled={!form.email.trim() || busy}
                  onClick={onSendCode}>{codeSent ? t('auth.aiOnline') : t('auth.sendCode')}</button>
        </div>
      </div>
      {kind ==='staff'&& (
        <div className="ar-fgroup">
          <span className="ar-flabel">{t('auth.aiOrgCode')} <i>*</i></span>
          <input className="lc-input"value={form.orgCode} placeholder={t('auth.orgCodeStaffPlaceholder')} onChange={(e) => set('orgCode', e.target.value)} />
        </div>
      )}
      {kind ==='admin'&& (
        <>
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
      {/* 提交期间禁用并转圈：注册是「有副作用的一次性动作」，重复点击会再发一次注册请求 */}
      <button className={'lc-btn lc-btn--primary lc-btn--block'+ (busy ?'ar-busy':'')} type="button"disabled={busy}
              onClick={onSubmit}>
        {busy && <span className="ar-spin"/>}{busy ? t('auth.aiSubmitting') : t('auth.registerAndLogin')}
      </button>
    </div>
  )
}

// ---------- 作用域样式（ar- 前缀，避免与组件库类名重名；§2.5） ----------
const CSS_AR = `
.ar-root{position:fixed;inset:0;z-index:60;display:grid;place-items:center;padding:16px;background:rgba(0,0,0,.72);color:var(--lc-text);font-family:var(--lc-font);animation:lc-mo-fade var(--lc-mo-enter) var(--lc-mo-out) both;}
/* ★ 比例按演示稿（demo-register-ai-motion.html 的 .phone）：390 宽竖版卡、
   高 min(780, 100dvh-32)、圆角 28、居中 —— 不是全屏接管（2026-09-18 用户裁定） */
.ar-panel{width:390px;max-width:100%;height:min(780px,calc(100dvh - 32px));min-height:520px;display:flex;flex-direction:column;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:28px;overflow:hidden;box-shadow:0 24px 64px rgba(0,0,0,.55);animation:lc-mo-pop var(--lc-mo-enter) var(--lc-mo-out) both;--lc-mo-origin:50% 92%;}
@media (max-width:480px){
  .ar-root{padding:0;background:var(--lc-bg);}
  .ar-panel{width:100%;height:100dvh;min-height:0;border:0;border-radius:0;box-shadow:none;}
}
.ar-nav{height:52px;flex:none;display:flex;align-items:center;gap:12px;padding:0 16px;border-bottom:1.2px solid var(--lc-border-faint);}
.ar-close{display:flex;align-items:center;justify-content:center;width:28px;height:28px;border-radius:8px;background:none;border:0;color:var(--lc-text);cursor:pointer;}
.ar-nav-t{font-size:16px;font-weight:600;color:var(--lc-text);flex:1;}
.ar-online{display:flex;align-items:center;gap:6px;font-size:12px;color:var(--lc-text-3);}
.ar-online i{width:7px;height:7px;border-radius:50%;background:var(--lc-text);box-shadow:0 0 0 3px rgba(255,255,255,.08);}
.ar-prog{flex:none;display:flex;flex-direction:column;gap:9px;padding:14px 16px 12px;border-bottom:1.2px solid var(--lc-border-faint);}
.ar-prog-top{display:flex;align-items:center;justify-content:space-between;gap:12px;}
.ar-prog-label{font-size:13px;color:var(--lc-text-3);}
.ar-prog-label b{color:var(--lc-text);font-weight:600;}
.ar-back{flex:none;font-size:12px;padding:4px 11px;border-radius:999px;background:var(--lc-raised);border:1.2px solid var(--lc-border-pill);color:var(--lc-text-2);cursor:pointer;font-family:var(--lc-font);}
.ar-back:active{transform:scale(.97);}
.ar-back[disabled]{opacity:.5;cursor:not-allowed;}
.ar-track{height:2px;border-radius:1px;background:var(--lc-border-faint);overflow:hidden;}
.ar-track i{display:block;height:100%;width:100%;background:var(--lc-text-2);transform:translateX(-101%);transition:transform .5s cubic-bezier(.32,.72,.28,1);}
.ar-chat{flex:1;min-height:0;overflow-y:auto;padding:20px 16px;display:flex;flex-direction:column;gap:20px;scrollbar-width:none;}
.ar-chat::-webkit-scrollbar{display:none;}
.ar-msg{display:flex;flex-direction:column;opacity:0;transform:translateY(8px);animation:ar-in .36s cubic-bezier(.32,.72,.28,1) forwards;}
.ar-msg--user{align-items:flex-end;}
.ar-who{display:flex;align-items:center;gap:8px;font-size:12px;color:var(--lc-text-4);margin-bottom:8px;}
.ar-badge{width:20px;height:20px;border-radius:7px;background:var(--lc-raised);border:1.2px solid var(--lc-border-card);display:flex;align-items:center;justify-content:center;font-family:var(--lc-font-mono);font-size:9px;font-weight:600;color:var(--lc-text);}
.ar-body{display:flex;flex-direction:column;gap:10px;}
.ar-msg--ai .ar-body{margin-left:28px;}
.ar-bubble{padding:12px 14px;border-radius:14px;font-size:14.5px;line-height:1.7;}
.ar-msg--ai .ar-bubble{background:var(--lc-inset);border:1.2px solid var(--lc-border-card);color:var(--lc-text-2);border-top-left-radius:6px;align-self:stretch;}
.ar-msg--user .ar-bubble{background:#FFFFFF;color:#000;font-weight:500;border-top-right-radius:6px;}
.ar-type{position:relative;display:block;}
.ar-type .ar-ghost{visibility:hidden;}
.ar-type .ar-real{position:absolute;inset:0;}
.ar-caret{display:inline-block;width:1px;height:1em;background:var(--lc-text-3);vertical-align:-.15em;margin-left:2px;animation:ar-caret 1s steps(1) infinite;}
@keyframes ar-caret{50%{opacity:0;}}
.ar-opts{display:flex;flex-direction:column;gap:8px;}
.ar-opt{display:flex;flex-direction:column;gap:4px;padding:12px 14px;border-radius:12px;text-align:left;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);cursor:pointer;font-family:var(--lc-font);
  transition:border-color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out),opacity var(--lc-mo-sink) var(--lc-mo-out);}
.ar-opt:hover:not(:disabled){border-color:var(--lc-border-pill);}
.ar-opt:disabled{opacity:.6;cursor:not-allowed;}
/* 选项确认：被点中的提亮并留在原位、其余压暗退场 —— 一次看得见的交代（原则 7） */
.ar-opt--picked{border-color:var(--lc-text);background:var(--lc-raised);}
.ar-opts--locked{pointer-events:none;}
.ar-opts--locked .ar-opt:not(.ar-opt--picked){opacity:.34;}
.ar-opt-t{font-size:14px;font-weight:500;color:var(--lc-text);}
.ar-opt-d{font-size:12px;color:var(--lc-text-4);line-height:1.5;}
.ar-chips{display:flex;flex-wrap:wrap;gap:8px;}
.ar-chip{display:flex;align-items:baseline;gap:6px;padding:7px 11px;border-radius:8px;background:var(--lc-raised);border:1.2px solid var(--lc-border-card);}
.ar-chip-k{font-size:11.5px;color:var(--lc-text-4);}
.ar-chip-v{font-size:13px;color:var(--lc-text);font-weight:500;}
.ar-form{display:flex;flex-direction:column;gap:14px;padding:20px 16px;border-radius:14px;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);box-shadow:var(--lc-panel-highlight);align-self:stretch;}
.ar-fgroup{display:flex;flex-direction:column;gap:8px;}
.ar-flabel{font-size:13px;color:var(--lc-text-3);}
.ar-flabel i{font-style:normal;color:var(--lc-text-4);font-size:12px;}
.ar-otp-row{display:flex;gap:8px;align-items:center;min-width:0;}
.ar-otp-cells{display:flex;gap:7px;flex:1;min-width:0;}
/* ★ min-width:0 必须给：<input> 的默认尺寸（size≈20 字符 ≈170px）来自内容，
   flex 子项默认 min-width:auto 会拒绝收缩 —— 六格在 390 宽上直接撑破表单，
   实测整行右缘溢出卡片 126px、页面出现横向滚动。 */
.ar-otp{flex:1;min-width:0;width:100%;height:46px;border-radius:10px;background:var(--lc-inset);border:1.2px solid var(--lc-border-input);color:var(--lc-text);text-align:center;font-family:var(--lc-font-latin);font-size:18px;font-weight:600;
  transition:border-color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out);}
/* 窄屏：发送按钮让出整行，六格拿满宽度（390 下格子能到 ~44px，否则只有 24px 太挤） */
@media (max-width: 480px){
  .ar-otp-row{flex-wrap:wrap;}
  .ar-otp-cells{flex:1 1 100%;}
  .ar-otp-row > .lc-btn{flex:1 1 100%;justify-content:center;}
}
.ar-otp:focus{outline:none;border-color:var(--lc-border-strong);}
/* 已填格：描边提到完成态、底提一档，六格填满时整排能一眼读出来 */
.ar-otp--filled{border-color:var(--lc-border-done);background:var(--lc-raised);}
.ar-ckpts{display:flex;flex-direction:column;gap:10px;padding:14px;border-radius:12px;background:var(--lc-inset);border:1.2px solid var(--lc-border-card);}
.ar-ckpt{display:flex;align-items:center;gap:10px;font-size:14px;color:var(--lc-text-2);}
.ar-ck{width:18px;height:18px;border-radius:50%;background:var(--lc-text);border:1.2px solid var(--lc-text);display:flex;align-items:center;justify-content:center;flex:none;}
.ar-done{display:flex;flex-direction:column;gap:12px;padding:20px 16px;border-radius:14px;background:var(--lc-panel);border:1.2px solid var(--lc-border-done);box-shadow:var(--lc-panel-highlight);align-self:stretch;}
.ar-done-t{font-size:14px;font-weight:600;color:var(--lc-text);}
.ar-kv{display:flex;gap:12px;font-size:13px;line-height:1.5;}
.ar-kv-k{width:72px;flex:none;color:var(--lc-text-4);}
.ar-kv-v{color:var(--lc-text-2);font-family:var(--lc-font-latin);}
.ar-busy{color:var(--lc-text-2)!important;pointer-events:none;}
.ar-spin{display:inline-block;width:12px;height:12px;border:1.5px solid var(--lc-border-faint);border-top-color:var(--lc-text-2);border-radius:50%;margin-right:8px;vertical-align:-2px;animation:ar-spin .8s linear infinite;}
@keyframes ar-spin{to{transform:rotate(360deg);}}
@keyframes ar-in{to{opacity:1;transform:none;}}
@media (prefers-reduced-motion: reduce){
  .ar-msg{animation:none!important;opacity:1!important;transform:none!important;}
  .ar-track i{transition:none!important;}
  .ar-caret,.ar-spin{animation:none!important;}
  .ar-root,.ar-panel{animation:none!important;}
}
`
