// ============================================================================
// components/Landing.tsx — 官网营销首页（2026-09-18 按画布 721173146990823 屏 6:1 重建）
// 区块顺序：导航 → Hero(左文案+右留资引导卡) → 解决方案三步 → 核心功能 bento →
// 覆盖范围 → 质量验证 → 开发者集成 → 价格三档 → 活动奖励 → 更新日志 → FAQ → 关于我们 → 收尾 CTA → 页脚。
// 动效：Hero 文案 lc-mo-up 节拍入场；★ 2026-09-19 #22：原「三检查点翻译流演示卡」（HeroDemo）
// 连同两颗「预约演示」按钮一起退役，转化入口统一为「留言获取方案」（锚到 #cta 留资表单）；
// 其「划掉错词→亮起正词」动效已抽到 components/WordSwap + theme.css，供产品内加载态复用（#24）。
// 其余区块 useReveal 滚动现身，组内 60ms 等速 stagger（动效原则 8）。
// 视觉：纯黑底、卡片描边 var(--lc-border-card) 1.2px、主按钮白底黑字、无蓝无绿。
// ============================================================================
/* 依赖口径（三条硬约束，改本文件前先确认）：
   1. useReveal 与 motion.css 的 .lc-reveal 配对使用——只挂类名不调 hook，元素会永远停在 opacity:0；
   2. 图标一律取自 langcross 线性图标集，禁止混入其它图标库（官网视觉的唯一来源）；
   3. 文案 100% 走 useT 的 land.* 键，词条在 src/i18n/panels/landing.ts，中英两份必须同步补
      （AGENTS.md 约定 5；本文件不出现任何裸中文文案，例外只有两处演示数据：DEMO_* 与 DEV_SAMPLE）。 */
import { useEffect, useRef, useState } from 'react'
import { useReveal } from '@/ui/langcross/src'
import {
  ArrowRightIcon,
  BookIcon,
  CheckIcon,
  CheckCircleIcon,
  ClipboardIcon,
  GlobeIcon,
  LayersIcon,
  SendIcon,
  ShieldIcon,
  TerminalIcon,
  UploadIcon,
  UsersIcon,
} from '@/ui/langcross/src'
// 图标在这里只承担「装饰性图示」，不带独立点击语义：卡片图标位固定 24，行内箭头/勾选 14~18
import { useT } from '@/i18n' // useT() → [语种, t, tpl]；版权行用 tpl 插值品牌名
import { useBranding } from '@/branding' // 租户品牌信息：brandName 为空即回落产品名
import { openAPIDocsUrl } from '@/api/core' // 公开 API 文档地址（同源 /openapi/docs）
import { LeadForm } from '@/components/LeadForm' // ★ P1-3 收尾留资表单（自带状态，唯一例外）
import { LangSelect } from '@/components/LangSelect' // ★ 2026-09-20 反馈④：落地页对非中文访客给出手动切换入口（12 语种，与顶栏同一组件）
import { INDUSTRY_META, industryName } from '@/lib/industries' // 覆盖范围区块：行业包与本页术语大卡同源的一份事实
import { PERSONA_FALLBACK } from '@/lib/personas' // 覆盖范围区块：八个角色 code 与后端 persona 包对齐

/* —— 术语对照固定数据（★ #22 HeroDemo 退役后仍留两份用途：术语大卡静态对照 + 开发者 curl 示例） —— */
const DEMO_SRC = '新车发布启动会定在下周，需进行竞品对标，赋能经销商的销售线索转化。' // 汽车甲方口吻整句，故意埋 3 处机翻易错说法
const DEMO_TERMS = [
  // 三检查点唯一数据源：w=机翻初译（判错项），r=行业正译（纠正项），cn=中文术语（量尺标签 + 行内主语）
  { w: 'start', r: 'kickoff', cn: '发布启动会' },
  { w: 'compare', r: 'benchmark', cn: '竞品对标' },
  { w: 'clue', r: 'lead', cn: '销售线索' },
] as const // as const：长度与字面量类型都锁死，节拍时长直接取 .length 才不会漂

/* —— 覆盖范围 / 开发者区块的固定数据（与 DEMO_* 同属"演示数据"例外，不走 land.* 键） —— */
// 文件工单实际可解析的格式（取自后端 internal/fileproc 白名单的对外主流档位；
// docm/xlsm/ppsm 等宏变体与 ttc 字体包同在，故全站口径写「30+」而不是这里数出来的 20）
const FILE_FORMATS = [
  'DOC', 'DOCX', 'PPT', 'PPTX', 'XLS', 'XLSX', 'PDF', 'TXT', 'MD', 'JSON',
  'XML', 'YAML', 'CSV', 'RTF', 'SRT', 'VTT', 'EPUB', 'ODT', 'ODS', 'ODP',
] as const
// 示例 curl 逐字段对齐后端 openapi.v1.json：X-API-Key 鉴权头、text 必填、
// target_langs 数组、mode ∈ fast|pro。域名与 Key 用环境变量占位，复制过去即可直接跑
const DEV_SAMPLE = [
  'curl -X POST "$LANGCROSS_HOST/openapi/v1/translate" \\',
  '  -H "X-API-Key: $LANGCROSS_API_KEY" \\',
  '  -H "Content-Type: application/json" \\',
  `  -d '{"text":"${DEMO_SRC}","target_langs":["en"],"mode":"pro"}'`,
].join('\n')

/** 开发者示例代码块：复制按钮写真实 curl（与演示卡同一套剪贴板降级口径，1.6s 回弹） */
function DevSample() {
  const [, t] = useT()
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard?.writeText(DEV_SAMPLE).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    }).catch(() => { /* 非安全上下文剪贴板不可用：静默失败，代码块仍可读可手动选中复制 */ })
  }
  return (
    <div className="lc-code lc-reveal">
      <div className="lc-code-bar">
        {/* 端点标签写死在代码块上：它和 DEV_SAMPLE 是同一份契约的两半，改路径必须两处同改 */}
        <span>POST /openapi/v1/translate</span>
        <button type="button" className="lc-code-copy" onClick={copy}>
          {copied ? <CheckIcon size={14} /> : <ClipboardIcon size={14} />}
          <span>{copied ? t('land.devCopied') : t('land.devCopy')}</span>
        </button>
      </div>
      {/* pre 保留反斜杠续行：white-space:pre + overflow-x，窄屏横向滚动而不打散命令行 */}
      <pre className="lc-code-body">{DEV_SAMPLE}</pre>
    </div>
  )
}

/** 胶囊按钮（画布一律 radius 999：主=白底黑字 / 次=描边 / 卡内=浮面底） */
function Pill(props: {
  variant?: 'pri' | 'ghost' | 'soft' | 'dark' // pri 主投（白底黑字）/ ghost 描边次投 / soft 卡内浮面 / dark 反相卡上的实心黑
  size?: 'sm' | 'md' | 'lg' // 三档定高 42 / 50 / 56：导航用 sm，收尾用 lg，其余一律 md
  href: string // 只做链接跳转：导航与 CTA 链接用 <a> 而非 <button>（带状态的只有 LeadForm/DevSample 两处），右键新窗口打开也仍然可用
  children: React.ReactNode
}) {
  const v = props.variant ?? 'pri' // 默认主投：页面上出现次数最多的那颗就是它
  const s = props.size ?? 'md'
  return (
    // 挂 lc-mo-press 是为了沿用全站按压类名，真正的 scale(.97) 写在本文件 .lc-mkt-btn:active，
    // 这样即使 motion.css 的过渡类没生效，按钮仍有按下反馈
    <a className={`lc-mkt-btn lc-mkt-btn--${v} lc-mkt-btn--${s} lc-mo-press`} href={props.href}>
      {props.children}
    </a>
  )
}

/** 功能卡图标位：40×40 浮面圆角块 + 24 线性图标（画布 6:117） */
function FIcon(props: { children: React.ReactNode }) {
  // 这里只负责"底板"（尺寸/圆角/底色），图标尺寸一律由调用方传 24，保证整站线性图标视觉重量一致
  return <span className="lc-ficon">{props.children}</span>
}

// 导航六锚点表：id=页内区块元素 id，key=文案键。导航 JSX 与滚动高亮（scrollspy）共用这一份，
// 增删区块只改这张表，链接与高亮判定不会各自漂移
const NAV_SECTIONS = [
  { id: 'solution', key: 'land.navSolution' },
  { id: 'features', key: 'land.navFeatures' },
  { id: 'quality', key: 'land.navQuality' }, // ★ 2026-09-19 质量验证升为核心卖点，进导航
  { id: 'pricing', key: 'land.navPricing' },
  { id: 'rewards', key: 'land.navRewards' },
  { id: 'faq', key: 'land.navFaq' },
] as const

/** Landing 官网营销首页：未登录 `/` 落地页 */
export default function Landing() {
  const [lang, t, tpl] = useT() // lang 给覆盖范围区块：行业 chip 按当前语种取中/英文名
  const branding = useBranding()
  const brand = branding.brandName || t('land.brand') // 品牌租户化了就用租户名，否则回落产品名
  // deps 传 []：只在挂载时扫一次 .lc-reveal。首页 DOM 结构是静态的，没必要随文案变化重建观察器
  const revealRef = useReveal<HTMLDivElement>([])

  // 导航滚动高亮（scrollspy）：给"滚动位置和导航高亮一致"那句注释补上真实逻辑——
  // 滚过哪个区块的顶线（140px），对应导航链接就点亮；区块之间保持上一块常亮，不做闪烁
  const [activeSec, setActiveSec] = useState('')
  useEffect(() => {
    let raf = 0
    const onScroll = () => {
      if (raf) return // rAF 节流：滚动事件每秒可触发上百次，高亮判定一帧一次就够
      raf = requestAnimationFrame(() => {
        raf = 0
        let cur = ''
        for (const { id } of NAV_SECTIONS) {
          const el = document.getElementById(id)
          if (el && el.getBoundingClientRect().top <= 140) cur = id
        }
        setActiveSec(cur)
      })
    }
    onScroll() // 挂载即判一次：带 hash 深链进来（/#pricing）首屏就该有正确高亮
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => { window.removeEventListener('scroll', onScroll); if (raf) cancelAnimationFrame(raf) }
  }, [])

  // Hero 入场：先整块下沉压暗（.is-sink）再回弹释放（.is-release）——峰值前先压暗做落差
  const heroRef = useRef<HTMLElement>(null)
  useEffect(() => {
    const el = heroRef.current
    if (!el) return
    if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return // 减少动效：连压暗都不做，首屏直接是终态
    el.classList.add('is-sink') // 只加类：位移与亮度都写在 CSS 的 .lc-hero.is-sink，JS 不碰数值，调参只去一处
    // 240ms 压暗 → 460ms 释放：两段用 setTimeout 串联而非纯 CSS animation，
    // 好处是卸载时能清干净，用户秒离页面不会把动画卡在半途的压暗态
    const t1 = window.setTimeout(() => {
      el.classList.remove('is-sink') // 两类同帧交接：留着 is-sink 会和 is-release 抢 transform
      el.classList.add('is-release')
    }, 240)
    const t2 = window.setTimeout(() => el.classList.remove('is-release'), 240 + 460) // 摘早了会看见回弹被截断
    return () => { window.clearTimeout(t1); window.clearTimeout(t2) } // 两个 timer 都要清，只清一个仍会补上一次类名切换
  }, [])

  // 首页价格三档：营销「意向卡」，文案与价位固定取 land.plan* 词典（画布 6:1 同口径）；
  // 完整、随后台变动的价目以 /pricing 为准（那张卡才渲染 /api/plans 的返回值）。
  // 三档 CTA 一律指向 /register，转化路径不做中间页。
  const plans = [
    {
      // 引流档：¥0 的作用只是抹平"试一下"的成本，说服交给中间那档，三档功能点数量保持一致
      key: 'free', name: t('land.planFree'), price: t('land.planFreePrice'),
      desc: t('land.planFreeDesc'), btn: t('land.planFreeBtn'), href: '/register',
      feats: [t('land.planFreeF1'), t('land.planFreeF2'), t('land.planFreeF3')],
    },
    {
      // pro: true 是全页唯一的视觉重心开关——反相白底、徽标、实心黑按钮三处都由它驱动
      key: 'pro', name: t('land.planPro'), price: t('land.planProPrice'),
      desc: t('land.planProDesc'), btn: t('land.planProBtn'), href: '/register', pro: true,
      feats: [t('land.planProF1'), t('land.planProF2'), t('land.planProF3')],
    },
    {
      // 企业档：价位给"定制"而非数字（口径与 /pricing 一致）；按钮写「联系销售」但 href 仍与其余两档同落 /register
      key: 'ent', name: t('land.planEnt'), price: t('land.planEntPrice'),
      desc: t('land.planEntDesc'), btn: t('land.planEntBtn'), href: '/register',
      feats: [t('land.planEntF1'), t('land.planEntF2'), t('land.planEntF3')],
    },
  ]
  // 首页 FAQ 八条（land.cfaqN.q / .a 两段式键名，N=1..8）：覆盖行业/质量/API/安全/计费/体验额度/文件格式/共建奖励；
  // /pricing 的 pFaqN* 只讲支付与到账，两套不重复，避免同一份答案抄两遍
  const faqs = [1, 2, 3, 4, 5, 6, 7, 8].map((i) => ({ q: t(`land.cfaq${i}.q`), a: t(`land.cfaq${i}.a`) })) // 键名按序号拼：缺词会把 "land.cfaq9.q" 直接渲染出来，漏词条一眼可见

  return (
    <div className="lc-mkt" ref={revealRef}>
      {/* 样式随组件注入：官网视觉只在落地页用，不并进全局样式包，登录页与控制台不为它买单；
          内容是模块级常量，重渲染时字符串不变，不会重复生成 style 节点 */}
      <style>{LANDING_CSS}</style>

      {/* 1. 顶部导航：白块 Logo + 品牌名 | 6 个锚点 | 登录 + 免费试用 */}
      {/* 锚点只 6 个且全部指向本页 id：不引二级页与外链，先让人在同一屏读完整套论证再决定去注册 */}
      <header className="lc-mkt-nav lc-mo-left">
        <a className="lc-nav-brand" href="/">
          {/* Logo：白色 8px 圆角块 + 两笔一实一虚的笔画，虚笔（opacity .45）示意"双语对照"；页脚复用同一段 */}
          <svg width="30" height="30" viewBox="0 0 30 30" aria-hidden="true">
            <rect width="30" height="30" rx="8" fill="#E7E9EA" />
            <path d="M9.5 10.5v5.2a4.3 4.3 0 0 0 8.6 0V12" stroke="#000" strokeWidth="2.4" fill="none" strokeLinecap="round" />
            <path d="M20.5 19.5v-5.2a4.3 4.3 0 0 0-8.6 0V18" stroke="#000" strokeWidth="2.4" fill="none" strokeLinecap="round" opacity=".45" />
          </svg>
          <span>{brand}</span> {/* 品牌名：租户改名后导航、页脚、演示卡页眉三处同时跟着变 */}
        </a>
        <nav className="lc-nav-links">
          {/* 6 个锚点顺序 = 页面区块顺序：滚动到哪块，对应链接由 scrollspy 点亮（.on） */}
          {NAV_SECTIONS.map(({ id, key }) => (
            <a key={id} href={`#${id}`} className={activeSec === id ? 'on' : undefined}>{t(key)}</a>
          ))}
        </nav>
        <div className="lc-nav-cta">
          {/* ★ 反馈④：语言切换放在登录旁——外国访客第一眼能找到的位置；纯黑描边按钮与营销导航同色系 */}
          <LangSelect />
          {/* 登录用文字链（低权重），试用用实心按钮（本页唯一主投）：两种强度不并列，避免选择困难 */}
          <a className="lc-nav-login" href="/login">{t('land.navLogin')}</a>
          <Pill size="sm" href="/register">{t('land.ctaFree')}</Pill>
        </div>
      </header>

      {/* 2. Hero：左文案（徽章/双行标题/副题/双 CTA/信任指标）+ 右留资引导卡（★ #22：原演示卡退役） */}
      {/* 左栏定宽 500px、右栏 flex:1（尺寸都在 CSS .lc-hero-copy/.lc-hero-demo）：
          文案要窄到一行十几字好读，引导卡要尽量宽才放得下三条承诺；<1200px 折成上下两段 */}
      <section className="lc-hero" ref={heroRef}>
        <div className="lc-hero-in">
          <div className="lc-hero-copy">
            <span className="lc-hero-badge lc-mo-up"><i />{t('land.heroBadge')}</span> {/* <i> 是徽章前的小圆点，纯装饰 */}
            <h1 className="lc-hero-h1">
              {/* 一个 h1 拆两个 block span：只为画布那处两行断句，语义上仍是一条标题，朗读顺序不断 */}
              <span className="lc-mo-up lc-mo-d1">{t('land.heroTitle1')}</span>
              <span className="lc-mo-up lc-mo-d2">{t('land.heroTitle2')}</span>
            </h1>
            <p className="lc-hero-sub lc-mo-up lc-mo-d3">{t('land.heroSub')}</p>
            {/* d1~d5 每档差 60ms（motion.css）：徽章→标题→副题→按钮→信任指标逐拍落位，读作"页面在呼吸"而非整体闪现 */}
            <div className="lc-hero-ctas lc-mo-up lc-mo-d4">
              {/* 主 CTA 去 /register；副 CTA「留言获取方案」锚到本页收尾留资表单（#cta）：页内已有真表单，不再空转去注册页 */}
              <Pill href="/register">{t('land.ctaFree')}</Pill>
              <Pill variant="ghost" href="#cta">{t('land.ctaLead')}</Pill>
            </div>
            <div className="lc-hero-trust lc-mo-up lc-mo-d5">
              {/* 信任指标用可核对的事实句（行业数/语言数/是否人工审校），不用形容词：与 FAQ 的口径互相兜住 */}
              <span><CheckCircleIcon size={16} />{t('land.trust1')}</span>
              <span><CheckCircleIcon size={16} />{t('land.trust2')}</span>
              <span><CheckCircleIcon size={16} />{t('land.trust3')}</span>
            </div>
          </div>
          <div className="lc-hero-demo lc-mo-up lc-mo-d3">
            {/* 留资引导卡用 d3 与副题同拍：再晚就成"二次加载"，更早则抢标题的读序。
                卡上只有锚点按钮没有表单——表单全页唯一一份留在 #cta 收尾区，避免双提交口 */}
            <div className="lc-hero-lead">
              <span className="lh-tag">{t('land.heroLeadTag')}</span>
              <h2 className="lh-t">{t('land.heroLeadTitle')}</h2>
              <p className="lh-d">{t('land.heroLeadSub')}</p>
              <ul className="lh-pts">
                {[1, 2, 3].map((i) => (
                  <li key={i}><CheckCircleIcon size={16} />{t(`land.heroLeadP${i}`)}</li>
                ))}
              </ul>
              <Pill variant="ghost" href="#cta">{t('land.heroLeadBtn')}</Pill>
            </div>
          </div>
        </div>
      </section>

      {/* 3. 解决方案：三步流程（卡 → 箭头 → 卡 → 箭头 → 卡） */}
      {/* 三步文案全取 land.stepN.t/.d（N=1..3）：这是"流程"而不是功能列表，所以只给序号与短句，不放图标 */}
      <section id="solution" className="lc-sec">
        <div className="lc-sec-head lc-reveal">
          <p className="lc-sec-label">{t('land.secSolution')}</p>
          <h2 className="lc-sec-title">{t('land.solutionTitle')}</h2>
          <p className="lc-sec-sub">{t('land.solutionSub')}</p>
        </div>
        <div className="lc-steps">
          {[1, 2, 3].map((i, idx) => (
            <div className="lc-steps-slot" key={i}>
              {/* slot 包一层是为了把箭头和卡绑成一个整体：窄屏折成竖列时箭头随卡一起换行 */}
              {idx > 0 && (
                /* 箭头标 aria-hidden：朗读不需要"箭头"这个词，它只服务视线推进；≤980px 由 CSS 转 90° 变向下 */
                <span className="lc-step-arrow" aria-hidden="true">
                  <ArrowRightIcon size={18} />
                </span>
              )}
              {/* 组内 60ms 等速 stagger（idx*60），与文件头原则 8 一致：三张卡并排出现等于没有先后 */}
              <article className="lc-step lc-reveal lc-mo-lift" data-reveal-delay={String(idx * 60)}>
                <span className="lc-step-num">{`0${i}`}</span> {/* 补零写死：只有三步，不值得引 padStart */}
                <h3 className="lc-step-t">{t(`land.step${i}.t`)}</h3>
                <p className="lc-step-d">{t(`land.step${i}.d`)}</p>
              </article>
            </div>
          ))}
        </div>
      </section>

      {/* 4. 核心功能 bento：术语库大卡（含机翻错误演示）+ 480 中卡 + 四张小卡 */}
      {/* bento 用两块 grid（.lc-bento-top 776fr:480fr + .lc-bento-row 四等分）拼出画布的不等高布局，
          而不是一个 grid-template-areas：折行时只要各改一条 grid-template-columns，不必重排结构 */}
      <section id="features" className="lc-sec">
        <div className="lc-sec-head lc-reveal">
          <p className="lc-sec-label">{t('land.secFeatures')}</p>
          <h2 className="lc-sec-title">{t('land.featuresTitle')}</h2>
          <p className="lc-sec-sub">{t('land.featuresSub')}</p>
        </div>
        <div className="lc-bento-top">
          <article id="terms" className="lc-fcard lc-fcard--big lc-reveal lc-mo-lift">
            {/* 大卡是整段的主角：只它有演示内容，因此独享 32px 内边距与更宽的列；
                id="terms" 给页脚「行业术语库」链接当落点——滚到位直接停在术语对照卡上 */}
            <FIcon><BookIcon size={24} /></FIcon>
            <h3 className="lc-fcard-t">{t('land.fc.terms.t')}</h3>
            <p className="lc-fcard-d">{t('land.fc.terms.d')}</p>
            <div className="lc-termdemo">
              <p className="lc-td-note">{t('land.fc.demoNote')}</p>
              {/* 对照行直接复用 DEMO_TERMS：与演示卡同一份事实，改术语/换行业两张卡一起跟随 */}
              {DEMO_TERMS.map((tm) => (
                <div className="lc-td-row" key={tm.w}>
                  <span className="lc-td-cn">{tm.cn}</span>
                  <span className="lc-td-bad">{tm.w}</span> {/* 错误项：等宽字体 + 最弱文字色，"错"要退到背景里 */}
                  <ArrowRightIcon size={14} className="lc-td-arrow" />
                  <span className="lc-td-good">{tm.r}</span> {/* 正解：同样等宽但用最强文字色，与上一行只靠明暗对照 */}
                </div>
              ))}
            </div>
          </article>
          <article className="lc-fcard lc-reveal lc-mo-lift" data-reveal-delay="60">
            {/* 中卡与大卡同排但晚 60ms 现身：视线先落在大卡（动效原则 7：仪器不早于被测对象） */}
            <FIcon><GlobeIcon size={24} /></FIcon>
            <h3 className="lc-fcard-t">{t('land.fc.langs.t')}</h3>
            <p className="lc-fcard-d">{t('land.fc.langs.d')}</p>
          </article>
        </div>
        <div className="lc-bento-row">
          {/* 四张小卡表驱动：这里只配「词典键 + 图标」，文案取 land.fc.<k>.t/.d，加一张卡只动两处 */}
          {[
            { k: 'team', Icon: UsersIcon },
            { k: 'memory', Icon: LayersIcon },
            { k: 'quality', Icon: ShieldIcon },
            { k: 'api', Icon: TerminalIcon },
          ].map(({ k, Icon }, i) => (
            <article key={k} className="lc-fcard lc-reveal lc-mo-lift" data-reveal-delay={String(i * 60)}>
              <FIcon><Icon size={24} /></FIcon>
              <h3 className="lc-fcard-t">{t(`land.fc.${k}.t`)}</h3>
              <p className="lc-fcard-d">{t(`land.fc.${k}.d`)}</p>
            </article>
          ))}
        </div>
      </section>

      {/* 4b. 覆盖范围：统计带 + 行业/角色/文件格式三组 chips。
          三组数据全部取自系统真实运行清单（行业 INDUSTRY_META、角色 PERSONA_FALLBACK、
          格式 fileproc 白名单档位），不是排版装饰——这一区块的说服力就来自"名字都可核对" */}
      <section id="coverage" className="lc-sec">
        <div className="lc-sec-head lc-reveal">
          <p className="lc-sec-label">{t('land.secCoverage')}</p>
          <h2 className="lc-sec-title">{t('land.coverageTitle')}</h2>
          <p className="lc-sec-sub">{t('land.coverageSub')}</p>
        </div>
        <div className="lc-cov-stats">
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className="lc-cov-stat lc-reveal lc-mo-lift" data-reveal-delay={String((i - 1) * 60)}>
              {/* 数字走等宽字体：四个统计位并排，字宽不齐会毁掉这一行的"仪表盘感" */}
              <b>{t(`land.st${i}.n`)}</b>
              <span>{t(`land.st${i}.l`)}</span>
            </div>
          ))}
        </div>
        <div className="lc-cov-groups">
          <div className="lc-cov-group lc-reveal">
            <h3 className="lc-cov-t">{t('land.covIndustries')}</h3>
            <p className="lc-cov-d">{t('land.covIndustriesNote')}</p>
            <div className="lc-chips">
              {/* 与术语大卡/注册表单同一份 INDUSTRY_META：后端加行业包时这里自动多一枚 chip */}
              {Object.values(INDUSTRY_META).map((m) => (
                <span key={m.code} className="lc-chip">{industryName(m.code, lang)}</span>
              ))}
            </div>
          </div>
          <div className="lc-cov-group lc-reveal">
            <h3 className="lc-cov-t">{t('land.covRoles')}</h3>
            <p className="lc-cov-d">{t('land.covRolesNote')}</p>
            <div className="lc-chips">
              {/* 展示名走 land.role.* 词条（code 与后端 persona 包对齐），en 语种下不露中文兜底名 */}
              {PERSONA_FALLBACK.map((p) => (
                <span key={p.code} className="lc-chip">{t(`land.role.${p.code}`)}</span>
              ))}
            </div>
          </div>
          <div className="lc-cov-group lc-reveal">
            <h3 className="lc-cov-t">{t('land.covFiles')}</h3>
            <p className="lc-cov-d">{t('land.covFilesNote')}</p>
            <div className="lc-chips">
              {FILE_FORMATS.map((f) => (
                <span key={f} className="lc-chip lc-chip--mono">{f}</span>
              ))}
            </div>
          </div>
        </div>
      </section>

      {/* 4c. 质量验证（★ 2026-09-19 核心卖点）：统计带 + 两种校验方法卡 + 四条对比结论 + 一句话收口。
          数字全部来自对外交叉盲评结论（GPT/Gemini 双模型独立评估、专业大模型交叉校验），不写无法核实的口径；
          视觉复用覆盖范围区块的统计带与功能卡语法，不引入新视觉语言 */}
      <section id="quality" className="lc-sec">
        <div className="lc-sec-head lc-reveal">
          <p className="lc-sec-label">{t('land.secQuality')}</p>
          <h2 className="lc-sec-title">{t('land.qualityTitle')}</h2>
          <p className="lc-sec-sub">{t('land.qualitySub')}</p>
        </div>
        <div className="lc-cov-stats lc-qa-stats">
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className="lc-cov-stat lc-reveal lc-mo-lift" data-reveal-delay={String((i - 1) * 60)}>
              <b>{t(`land.qs${i}.n`)}</b>
              <span>{t(`land.qs${i}.l`)}</span>
            </div>
          ))}
        </div>
        <div className="lc-qa-methods">
          {[
            { k: 'qaExt', Icon: CheckCircleIcon },
            { k: 'qaInt', Icon: ShieldIcon },
          ].map(({ k, Icon }, i) => (
            <article key={k} className="lc-fcard lc-reveal lc-mo-lift" data-reveal-delay={String(i * 60)}>
              <FIcon><Icon size={24} /></FIcon>
              <h3 className="lc-fcard-t">{t(`land.${k}.t`)}</h3>
              <p className="lc-fcard-d">{t(`land.${k}.d`)}</p>
            </article>
          ))}
        </div>
        <div className="lc-qa-points lc-reveal">
          {/* 四条结论行：border-top 分隔的清单式排版，与「关于我们」区块同一语法——结论要读，不要看 */}
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className="lc-qa-pt">
              <h3 className="lc-qa-t"><CheckIcon size={16} />{t(`land.qp${i}.t`)}</h3>
              <p className="lc-qa-d">{t(`land.qp${i}.d`)}</p>
            </div>
          ))}
        </div>
        <p className="lc-qa-final lc-reveal">{t('land.qaFinal')}</p>
      </section>

      {/* 4d. 开发者集成：左栏定位 + 右栏三张能力卡与可复制的真实 curl 示例。
          端点/鉴权头/字段逐一对齐后端 openapi.v1.json，示例跑不通就是缺陷 */}
      <section id="developers" className="lc-sec">
        <div className="lc-dev">
          <div className="lc-dev-head lc-reveal">
            <p className="lc-sec-label">{t('land.secDev')}</p>
            <h2 className="lc-sec-title">{t('land.devTitle')}</h2>
            <p className="lc-dev-sub">{t('land.devSub')}</p>
            {/* 文档链走同源 /openapi/docs 真页（新标签打开）：这里是本区块唯一出口，不再摆注册按钮抢动作 */}
            <a className="lc-dev-doc" href={openAPIDocsUrl()} target="_blank" rel="noopener">
              <TerminalIcon size={16} />{t('land.devDocBtn')}<ArrowRightIcon size={14} />
            </a>
          </div>
          <div className="lc-dev-body">
            <div className="lc-dev-feats">
              {[1, 2, 3].map((i) => (
                <div key={i} className="lc-dev-feat lc-reveal lc-mo-lift" data-reveal-delay={String((i - 1) * 60)}>
                  <h3 className="lc-dev-t">{t(`land.dev${i}.t`)}</h3>
                  <p className="lc-dev-d">{t(`land.dev${i}.d`)}</p>
                </div>
              ))}
            </div>
            <DevSample />
          </div>
        </div>
      </section>

      {/* 5. 价格方案：三档（专业版白底高亮居中一档） */}
      {/* 三档等宽并排（≤980px 折成一列），只有中间档反相：一屏之内视觉重心只允许有一个 */}
      <section id="pricing" className="lc-sec">
        <div className="lc-sec-head lc-reveal">
          <p className="lc-sec-label">{t('land.secPricing')}</p>
          <h2 className="lc-sec-title">{t('land.pricingTitle')}</h2>
          <p className="lc-sec-sub">{t('land.pricingSub')}</p>
        </div>
        <div className="lc-plans">
          {plans.map((p, i) => (
            /* pro 档只多挂一个类名：徽标、反相底色、按钮配色三处全由同一个 p.pro 决定，
               不会出现"底色反了但按钮还是浅色"的半反相第四态 */
            <article key={p.key} className={`lc-plan lc-reveal lc-mo-lift${p.pro ? ' lc-plan--pro' : ''}`} data-reveal-delay={String(i * 60)}>
              {p.pro && <span className="lc-plan-badge">{t('land.planBadge')}</span>} {/* 徽标只此一处：两侧靠"没有徽标"表达非推荐 */}
              <h3 className="lc-plan-name">{p.name}</h3>
              <div className="lc-plan-price">{p.price}</div> {/* 价位取词典不取 /api/plans：落地页不该因后台调价而变样，那是 /pricing 的活 */}
              <p className="lc-plan-desc">{p.desc}</p>
              {/* 反相卡上不能再放白底按钮（会糊成一片），所以 pro 用 dark、其余两档用 soft */}
              <Pill variant={p.pro ? 'dark' : 'soft'} href={p.href}>{p.btn}</Pill>
              <ul className="lc-plan-feats">
                {p.feats.map((f) => (
                  <li key={f}><CheckIcon size={16} />{f}</li> // 每档固定三条：三档条数一致才能横向比较，多一条就变成"信息更丰富"的误导
                ))}
              </ul>
            </article>
          ))}
        </div>
      </section>

      {/* 6. 活动奖励：邀请好友 + 上传知识库 */}
      {/* 两卡严格等宽：拉新与内容贡献是两条独立的增长路径，做成大小卡会被读成主次 */}
      <section id="rewards" className="lc-sec">
        <div className="lc-sec-head lc-reveal">
          <p className="lc-sec-label">{t('land.secRewards')}</p>
          <h2 className="lc-sec-title">{t('land.rewardsTitle')}</h2>
          <p className="lc-sec-sub">{t('land.rewardsSub')}</p>
        </div>
        <div className="lc-rewards">
          <article className="lc-rw-card lc-reveal lc-mo-lift">
            <FIcon><SendIcon size={24} /></FIcon>
            <h3 className="lc-rw-t">{t('land.rewardInvite.t')}</h3>
            <p className="lc-rw-d">{t('land.rewardInvite.d')}</p>
            {/* 数字与单位都来自词典（land.rewardInvite.num / .unit）：这里只是营销口径的展示位，
                真实发放额度与单位以运营后台配置和 /invites 页为准，调额度不需要动本文件 */}
            <p className="lc-rw-num">
              <b>{t('land.rewardInvite.num')}</b> {/* 等宽字体：中英切换时 "10,000" 的宽度不抖，单位不会跟着跳位 */}
              <span>{t('land.rewardInvite.unit')}</span>
            </p>
            <Pill href="/register">{t('land.rewardInvite.btn')}</Pill>
          </article>
          <article className="lc-rw-card lc-reveal lc-mo-lift" data-reveal-delay="60">
            {/* 第二张卡晚 60ms 现身，与前面各区块的 stagger 同一口径；它不带数字，因为知识库奖励随内容与量变动 */}
            <FIcon><UploadIcon size={24} /></FIcon>
            <h3 className="lc-rw-t">{t('land.rewardKb.t')}</h3>
            <p className="lc-rw-d">{t('land.rewardKb.d')}</p>
            <Pill href="/register">{t('land.rewardKb.btn')}</Pill>
          </article>
        </div>
      </section>

      {/* 6b. 更新日志：条目取 land.clN.date/.t/.d（N=1..5），全部对应真实发布记录。
          这里就是"更新日志"链接的落点——页脚不再造指向不存在页面的死链，新增发版只加词条不改结构 */}
      <section id="changelog" className="lc-sec">
        <div className="lc-sec-head lc-reveal">
          <p className="lc-sec-label">{t('land.secChangelog')}</p>
          <h2 className="lc-sec-title">{t('land.changelogTitle')}</h2>
          <p className="lc-sec-sub">{t('land.changelogSub')}</p>
        </div>
        <div className="lc-cl-list">
          {[1, 2, 3, 4, 5].map((i) => (
            <div key={i} className="lc-cl-item lc-reveal" data-reveal-delay={String((i - 1) * 60)}>
              {/* 日期独立成列：等宽字体保证五条日期左右对齐成一条竖线，读作台账而非散文 */}
              <span className="lc-cl-date">{t(`land.cl${i}.date`)}</span>
              <div>
                <h3 className="lc-cl-t">{t(`land.cl${i}.t`)}</h3>
                <p className="lc-cl-d">{t(`land.cl${i}.d`)}</p>
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* 7. FAQ：左标题栏 360 + 右问答列表（1px 分隔线） */}
      {/* 问答一律展开、不做折叠：只有四条，折叠会把"给答案"变成"要多点一次"，也让问题文字不可选中 */}
      <section id="faq" className="lc-sec">
        <div className="lc-faq">
          <div className="lc-faq-head lc-reveal">
            <p className="lc-sec-label">{t('land.secFaq')}</p>
            <h2 className="lc-sec-title">{t('land.faqTitle')}</h2>
            <p className="lc-faq-sub">{t('land.faqSub')}</p>
          </div>
          <div className="lc-faq-list">
            {faqs.map((f, i) => (
              <div key={f.q} className="lc-faq-item lc-reveal" data-reveal-delay={String(i * 60)}> {/* key 用问题文本：条目静态不重排，省掉一份 id 表 */}
                <h3 className="lc-faq-q">{f.q}</h3> {/* 分隔线用每条自己的 border-top，不放 <hr>：条距天然等宽且末条不会多出一条尾线 */}
                <p className="lc-faq-a">{f.a}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* 7b. 关于我们：左定位右三条"工作方式"（与 FAQ 同网格参数，不另造一套版式）。
          只写页内可验证的事实：术语包/共建奖励在上方区块都在，OpenAPI 文档在页脚可直达 */}
      <section id="about" className="lc-sec">
        <div className="lc-about">
          <div className="lc-about-head lc-reveal">
            <p className="lc-sec-label">{t('land.secAbout')}</p>
            <h2 className="lc-sec-title">{t('land.aboutTitle')}</h2>
            <p className="lc-about-p">{t('land.aboutP')}</p>
            {/* 这里只放次级幽灵按钮：最终转化动作仍归收尾 CTA 白块，一屏不抢两颗主投 */}
            <Pill variant="ghost" href="#cta">{t('land.aboutBtn')}</Pill>
          </div>
          <div className="lc-about-pts">
            {[1, 2, 3].map((i) => (
              <div key={i} className="lc-about-pt lc-reveal" data-reveal-delay={String((i - 1) * 60)}>
                <h3 className="lc-about-t"><CheckIcon size={16} />{t(`land.about${i}.t`)}</h3>
                <p className="lc-about-d">{t(`land.about${i}.d`)}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* 8. 收尾 CTA：白底圆角块 */}
      {/* 整页唯一一次大面积反相：把最后的动作与上面的黑底论证在视觉上切断，读作"到这里可以决定了" */}
      <section id="cta" className="lc-cta-wrap">
        <div className="lc-cta lc-reveal">
          <h2 className="lc-cta-t">{t('land.ctaTitle')}</h2>
          <p className="lc-cta-sub">{t('land.ctaSub')}</p> {/* 副标只写可兑现的降风险承诺（无需卡/注册送体验额度） */}
          {/* 收尾只留一颗按钮且用最大档 lg：这里再放个次按钮，等于替用户留了"再想想"的门 */}
          <Pill variant="dark" size="lg" href="/register">{t('land.ctaFree')}</Pill>
          {/* ★ P1-3 留资表单：不注册也能让销售跟上进来——匿名 POST /api/lead（限流+蜜罐+可选验证码） */}
          <div className="lc-lead-sep"><span>{t('land.leadOr')}</span></div>
          <LeadForm source="landing" />
        </div>
      </section>

      {/* 9. 页脚：品牌区 + 三列链接 + 分隔线 + 版权 */}
      {/* 页脚只指本页锚点与同域法务页，绝不写死外部/生产域名（AGENTS.md 约定 6：e2e 红线） */}
      <footer className="lc-foot">
        <div className="lc-foot-top">
          <div className="lc-foot-brand">
            <a className="lc-nav-brand" href="/">
              {/* Logo 与导航那枚是同一份内联 SVG：官网只此一个标识，不引图片资源，免得首屏多一次请求 */}
              <svg width="30" height="30" viewBox="0 0 30 30" aria-hidden="true">
                <rect width="30" height="30" rx="8" fill="#E7E9EA" />
                <path d="M9.5 10.5v5.2a4.3 4.3 0 0 0 8.6 0V12" stroke="#000" strokeWidth="2.4" fill="none" strokeLinecap="round" />
            <path d="M20.5 19.5v-5.2a4.3 4.3 0 0 0-8.6 0V18" stroke="#000" strokeWidth="2.4" fill="none" strokeLinecap="round" opacity=".45" />
              </svg>
              <span>{brand}</span>
            </a>
            <p className="lc-foot-tag">{t('land.footTag')}</p>
          </div>
          <div className="lc-foot-cols">
            <div className="lc-foot-col">
              {/* 产品列全部锚到页内真区块：术语库落点是大卡 #terms，不是一律甩给 #features */}
              <b>{t('land.footProduct')}</b>
              <a href="#features">{t('land.footFeatures')}</a>
              <a href="#terms">{t('land.footTermsLib')}</a>
              <a href="#pricing">{t('land.footPricingLink')}</a>
              <a href="#cta">{t('land.footLead')}</a>
            </div>
            <div className="lc-foot-col">
              {/* 公司列四条同样有真实落点：关于我们/更新日志是页内新区块，
                  共建奖励=活动区（邀请+知识库两条合作路径都在线上生效），联系我们=留资表单 */}
              <b>{t('land.footCompany')}</b>
              <a href="#about">{t('land.footAbout')}</a>
              <a href="#changelog">{t('land.footChangelog')}</a>
              <a href="#rewards">{t('land.footCoCreate')}</a>
              <a href="#cta">{t('land.footContact')}</a>
            </div>
            <div className="lc-foot-col">
              <b>{t('land.footRes')}</b>
              {/* API 文档指向后端真实公开页 /openapi/docs；帮助中心锚到页内 FAQ；
                  法务三条走同域 /docs/* 静态页，与 Login、/pricing 页脚共用同一批路径，改路由要三处一起改 */}
              <a href={openAPIDocsUrl()} target="_blank" rel="noopener">{t('land.footApiDoc')}</a>
              <a href="#faq">{t('land.footHelp')}</a>
              <a href="/docs/terms">{t('land.fTerms')}</a>
              <a href="/docs/privacy">{t('land.fPrivacy')}</a>
              <a href="/docs/sla">{t('land.footSla')}</a>
            </div>
          </div>
        </div>
        {/* 分隔线用 1px 空 div 而不是 border：这里要的是"上 40 下 20"的不对称呼吸，border 只能贴一边 */}
        <div className="lc-foot-div" />
        {/* 版权行走 {brand} 模板插值：租户改名后与导航/页脚品牌区同源联动（2026-09-19 修硬编码品牌名破口） */}
        <div className="lc-foot-bot">{tpl('land.copyright', { brand })}</div>
      </footer>
    </div>
  )
}

// —— 样式：段落结构、配色与字号全部对齐画布 6:1；描边走 --lc-* 令牌 ——
const LANDING_CSS = `
/* 根容器：min-height 撑满一屏，内容不足一屏时也不露出浏览器底色；文字边缘按 macOS 口径调校 */
.lc-mkt{background:var(--lc-bg);color:var(--lc-text);font-family:var(--lc-font);min-height:100vh;-webkit-font-smoothing:antialiased}
/* 链接统一继承文字色：官网不出现蓝色链接，可点感一律靠按钮与描边表达 */
.lc-mkt a{color:inherit;text-decoration:none}

/* —— 1. 导航 —— */
/* sticky + 半透黑底 + blur：滚动时导航始终在位，但内容从它底下"透出"而不是被硬切断 */
.lc-mkt-nav{position:sticky;top:0;z-index:20;display:flex;align-items:center;gap:36px;height:76px;padding:0 80px;background:rgba(0,0,0,.86);backdrop-filter:blur(10px);border-bottom:1px solid var(--lc-border-card)}
/* 品牌名不许折行：窄屏先牺牲锚点链接，也不把 Logo 压成两行 */
.lc-nav-brand{display:flex;align-items:center;gap:10px;font-size:20px;font-weight:700;white-space:nowrap}
/* 锚点用三级文字色：它是"路径提示"不是"内容"，不该和正文争对比度 */
.lc-nav-links{display:flex;align-items:center;gap:36px;font-size:15px;color:var(--lc-text-3)}
/* 只过渡 color：导航是定位工具，hover 时不许位移，否则整条栏像在被推动 */
.lc-nav-links a{transition:color var(--lc-mo-release) var(--lc-mo-out)}
.lc-nav-links a:hover{color:var(--lc-text)}
/* scrollspy 当前区块链接升到最强字色：与 hover 同色但常驻，靠"哪颗最亮"回答"我读到哪了" */
.lc-nav-links a.on{color:var(--lc-text)}
/* 右侧两块用 margin-left:auto 顶到最右：中间锚点靠自身 gap 排，不参与挤压 */
.lc-nav-cta{margin-left:auto;display:flex;align-items:center;gap:20px}
.lc-nav-login{font-size:15px;font-weight:500;transition:color var(--lc-mo-release) var(--lc-mo-out)}
.lc-nav-login:hover{color:var(--lc-text-2)}

/* —— 胶囊按钮 —— */
/* 尺寸不在基类里定，一律由 --sm/--md/--lg 三档给：同一颗按钮在导航、卡内、收尾必须同形不同档 */
.lc-mkt-btn{display:inline-flex;align-items:center;justify-content:center;border-radius:999px;font-weight:600;white-space:nowrap;transition:transform .18s var(--lc-mo-out),opacity .18s var(--lc-mo-out),background .28s ease,border-color .28s ease;cursor:pointer}
/* 按下只缩 3%：操作要被回应（动效原则），但按钮不是活物，不许"跳一下" */
.lc-mkt-btn:active{transform:scale(.97)}
/* 变体选择器带 .lc-mkt 前缀：压过 .lc-mkt a{color:inherit} 的 (0,1,1)，
   否则白底按钮上的白字 / 黑底按钮上的黑字会被 inherit 覆盖成隐形（实测踩坑） */
/* 主投=白底黑字：整页唯一一处实心白，强度最高，同屏通常只让它出现一次 */
.lc-mkt .lc-mkt-btn--pri{background:var(--lc-text-1);color:#000}
.lc-mkt .lc-mkt-btn--pri:hover{opacity:.88}
/* 描边次投：#546470 比 --lc-border-card 亮一档，边框才读得出"可点"而不是"分隔线" */
.lc-mkt .lc-mkt-btn--ghost{border:1.2px solid #546470;color:var(--lc-text-1);background:transparent}
.lc-mkt .lc-mkt-btn--ghost:hover{border-color:var(--lc-border-done)}
/* 卡内浮面底：给非高亮价格档用，强度低于主投但仍是实心，不会和卡片背景糊在一起 */
.lc-mkt .lc-mkt-btn--soft{background:var(--lc-raised);color:var(--lc-text-1)}
.lc-mkt .lc-mkt-btn--soft:hover{opacity:.88}
/* 反相专用：白底卡与收尾白块上不能再放白按钮，改用实心黑保持"白-黑"配对 */
.lc-mkt .lc-mkt-btn--dark{background:#000;color:#fff}
.lc-mkt .lc-mkt-btn--dark:hover{opacity:.86}
/* 三档定高 42/50/56：只允许这三档，按钮高度一致才能与相邻文本块基线对齐 */
.lc-mkt-btn--sm{height:42px;padding:0 22px;font-size:15px}
.lc-mkt-btn--md{height:50px;padding:0 30px;font-size:16px}
.lc-mkt-btn--lg{height:56px;padding:0 36px;font-size:18px;font-weight:600}

/* —— 2. Hero —— */
/* min-height 734px 抄画布首屏高度："不到一屏"就不算首屏；窄屏该约束由响应式清掉 */
.lc-hero{display:flex;align-items:center;padding:56px 80px;min-height:734px}
/* 两栏用 flex 而非 grid：右栏要能吃掉左栏定宽之后的全部余量并居中放卡 */
.lc-hero-in{display:flex;align-items:center;gap:60px;width:100%}
/* 左栏 flex:none + 定宽 500：文案行长要锁死，宽屏也不许把句子拉散（超宽靠右栏吸收） */
.lc-hero-copy{flex:none;width:500px;display:flex;flex-direction:column;gap:24px}
/* 徽章底色 #17171C、描边 #31363D 是画布原值：它比卡片底略抬，又不到 raised 的强度 */
.lc-hero-badge{display:inline-flex;align-items:center;gap:8px;align-self:flex-start;padding:8px 14px;border-radius:20px;background:#17171C;border:1.2px solid #31363D;font-size:14px;font-weight:500;color:var(--lc-text-3)}
.lc-hero-badge i{width:8px;height:8px;border-radius:4px;background:var(--lc-text-1);flex:none}
/* 3.89vw = 56px / 1440px 设计宽：clamp 的上界与画布字号一致，下界保证手机两行不断句 */
.lc-hero-h1{margin:0;font-size:clamp(34px,3.89vw,56px);line-height:1.21;font-weight:700;letter-spacing:.2px;color:var(--lc-text-1)}
/* 两行断句靠 block 而不是 <br>：DOM 里不留可被复制带走的换行符，也让两行各自能挂节拍类 */
.lc-hero-h1 span{display:block}
.lc-hero-sub{margin:0;font-size:16px;line-height:28px;color:var(--lc-text-3)}
.lc-hero-ctas{display:flex;align-items:center;gap:16px}
/* 信任指标 26px 间距：三条要读成"并列事实"，间距小于卡内 gap 就会粘成一段 */
.lc-hero-trust{display:flex;align-items:center;gap:26px;font-size:14px;color:var(--lc-text-3)}
.lc-hero-trust span{display:inline-flex;align-items:center;gap:8px}
.lc-hero-trust svg{color:var(--lc-text-2)}
/* min-width:0 是 flex 老坑：不写它，右栏卡里的长英文会把右栏顶到溢出、整页出现横向滚动 */
.lc-hero-demo{flex:1;display:flex;justify-content:center;min-width:0}
/* ★ #22 Hero 右栏「留言获取方案」引导卡（原演示卡退役）：与页内其他卡共用一套卡语
   （1.2px 描边 + radius 16 + --lc-bg），刻意弱于 #cta 白块——这里只种草，转化动作留给表单 */
.lc-hero-lead{width:100%;max-width:520px;display:flex;flex-direction:column;align-items:flex-start;gap:14px;padding:28px 30px;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:16px}
.lh-tag{font:500 11px/1 var(--lc-font-mono);letter-spacing:.08em;color:var(--lc-text-3);border:1px solid var(--lc-border-pill);border-radius:999px;padding:5px 10px}
.lh-t{margin:0;font-size:19px;font-weight:600;line-height:28px;color:var(--lc-text-1)}
.lh-d{margin:0;font-size:13.5px;line-height:22px;color:var(--lc-text-3)}
/* 三条承诺竖排小间距：读成清单而不是段落；图标取 --lc-text-2 与信任指标同档 */
.lh-pts{margin:0;padding:0;list-style:none;display:flex;flex-direction:column;gap:8px;width:100%;font-size:13.5px;line-height:21px;color:var(--lc-text-2)}
.lh-pts li{display:flex;align-items:flex-start;gap:8px}
.lh-pts svg{flex:none;margin-top:2px;color:var(--lc-text-3)}

/* Hero 入场：整块下沉压暗 → 回弹释放（峰值前先压暗做落差） */
.lc-hero.is-sink{filter:brightness(.84);transform:translateY(6px)}
/* 基态也声明 transition：摘掉 is-sink 回到无类时同样要有缓动，不能只有加类才动 */
.lc-hero{transition:filter .5s cubic-bezier(.4,0,.2,1),transform .5s cubic-bezier(.4,0,.2,1)}
/* 释放段亮度 0.16s、位移 0.46s 且回弹曲线：亮要快（峰值感），弹要慢（余震），两条时长故意错开 */
.lc-hero.is-release{filter:brightness(1.06);transform:none;transition:filter .16s linear,transform .46s cubic-bezier(.16,1,.3,1)}

/* —— 区块共用 —— */
/* 每个区块统一 80px 内边距：区块之间的"呼吸"全由它给，不再额外加 margin，避免两处调同一间距 */
.lc-sec{padding:80px}
/* 锚点避让：吸顶导航高 76px，缺这条时点导航/页脚锚点会把区块标题顶进导航底下（2026-09-19 修）；
   .lc-fcard--big 也要挂：页脚「行业术语库」跳 #terms，落点是大卡而不是区块头 */
.lc-sec,.lc-cta-wrap,.lc-fcard--big{scroll-margin-top:96px}
/* 标题组与内容固定 48px：所有区块同一条呼吸线，读者能预判"下面就是正文" */
.lc-sec-head{display:flex;flex-direction:column;gap:14px;margin-bottom:48px}
/* 小标签与标题只差一个字号档：靠字重与色阶分层级，不引入新颜色 */
.lc-sec-label{margin:0;font-size:14px;font-weight:600;color:var(--lc-text-3)}
/* 二级标题 36px 封顶，与 Hero 的 56px 差出一档，滚动时层级不会乱 */
.lc-sec-title{margin:0;font-size:clamp(26px,2.5vw,36px);font-weight:700;color:var(--lc-text-1)}
.lc-sec-sub{margin:0;font-size:18px;color:var(--lc-text-3)}

/* —— 3. 三步流程 —— */
/* align-items:stretch：三张卡等高，话少的卡也要撑满，否则底部留白看着像缺内容 */
.lc-steps{display:flex;align-items:stretch;gap:24px}
/* slot 与卡各自 flex:1、箭头 flex:none：窄屏折成竖列时箭头跟着卡走，不会掉在上一行末尾 */
.lc-steps-slot{flex:1;display:flex;align-items:center;gap:24px;min-width:0}
.lc-step{flex:1;display:flex;flex-direction:column;gap:16px;padding:28px;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:16px;min-width:0}
/* 序号走等宽字体：与演示卡左侧的红色序号同一套语汇，"01/02/03" 才像流程编号而非列表符号 */
.lc-step-num{font-family:var(--lc-font-mono);font-size:14px;font-weight:500;color:var(--lc-text-4)}
.lc-step-t{margin:0;font-size:20px;font-weight:600;color:var(--lc-text-1)}
.lc-step-d{margin:0;font-size:15px;line-height:24px;color:var(--lc-text-3)}
/* 箭头单独定色 #6A717A（对 #000 约 4.4:1）：它是流程记号，够看见但不与正文抢对比 */
.lc-step-arrow{flex:none;display:flex;align-items:center;color:#6A717A}

/* —— 4. 核心功能 bento —— */
/* 776fr/480fr 直接抄画布两列宽度：写 fr 不写 px，才能在区块内边距收缩时按比例跟着缩 */
.lc-bento-top{display:grid;grid-template-columns:776fr 480fr;gap:24px}
/* 四张小卡与顶栏分属两条 grid：结构上等价于"两行"，但折行只需各改一行 columns */
.lc-bento-row{display:grid;grid-template-columns:repeat(4,1fr);gap:24px;margin-top:24px}
/* 卡底用 --lc-bg 而不是抬升色：整页的分层只靠 1.2px 描边表达，不用灰底堆叠 */
.lc-fcard{display:flex;flex-direction:column;gap:14px;padding:28px;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:16px;min-width:0}
/* 大卡多 4px 内边距：它要装三条演示行，密排会读成表格而不是产品截图 */
.lc-fcard--big{padding:32px;gap:16px}
/* hover 只提描边亮度，不位移不投影：功能卡不是按钮，不该给"可点"的暗示 */
.lc-fcard:hover{border-color:var(--lc-border-pill)}
.lc-ficon{display:inline-flex;align-items:center;justify-content:center;width:40px;height:40px;border-radius:10px;background:var(--lc-raised);color:var(--lc-text-1);flex:none}
.lc-fcard-t{margin:0;font-size:18px;font-weight:600;color:var(--lc-text-1)}
.lc-fcard-d{margin:0;font-size:15px;line-height:24px;color:var(--lc-text-3)}
/* margin-top:auto 把演示区压到大卡底部：卡高由 grid 拉齐，演示贴底才对得上画布构图 */
.lc-termdemo{display:flex;flex-direction:column;gap:8px;margin-top:auto;padding-top:8px}
.lc-td-note{margin:0;font-size:12px;color:var(--lc-text-4)}
/* 演示行用最深底色 --lc-deep：整卡里唯一一处"嵌进去"的容器，读起来才像界面截图 */
.lc-td-row{display:flex;align-items:center;gap:12px;padding:12px 16px;background:var(--lc-deep);border:1.2px solid var(--lc-border-faint);border-radius:10px}
.lc-td-cn{flex:1;font-size:14px;font-weight:500;color:var(--lc-text-1);min-width:0}
/* bad/good 同用等宽字体、只差色阶：让"错"与"对"是同一个位置的两种状态，而不是两种东西 */
.lc-td-bad{font-family:var(--lc-font-mono);font-size:13px;color:var(--lc-text-4)}
.lc-td-arrow{color:#6A717A;flex:none}
.lc-td-good{font-family:var(--lc-font-mono);font-size:13px;font-weight:500;color:var(--lc-text-1)}

/* —— 4b. 覆盖范围 —— */
/* 统计带四等分：数字是这一区块的主角，等宽排一排才读成"面板读数"而不是四段散文 */
.lc-cov-stats{display:grid;grid-template-columns:repeat(4,1fr);gap:24px;margin-bottom:44px}
.lc-cov-stat{display:flex;flex-direction:column;gap:8px;padding:26px 28px;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:16px;min-width:0}
.lc-cov-stat b{font-family:var(--lc-font-mono);font-size:clamp(28px,2.4vw,34px);font-weight:700;line-height:1.1;color:var(--lc-text-1)}
.lc-cov-stat span{font-size:13px;line-height:20px;color:var(--lc-text-3)}
.lc-cov-groups{display:flex;flex-direction:column;gap:30px}
.lc-cov-group{display:flex;flex-direction:column;gap:10px}
.lc-cov-t{margin:0;font-size:18px;font-weight:600;color:var(--lc-text-1)}
.lc-cov-d{margin:0;font-size:14px;line-height:22px;color:var(--lc-text-3);max-width:760px}
/* chips 用描边胶囊不用色块：本页的"标签"语法只有一种（同 Hero 语种标签），不为一排格式名新造视觉元素 */
.lc-chips{display:flex;flex-wrap:wrap;gap:10px;margin-top:4px}
.lc-chip{padding:7px 14px;border:1.2px solid var(--lc-border-card);border-radius:999px;font-size:13px;color:var(--lc-text-2);background:var(--lc-bg);white-space:nowrap}
.lc-chip--mono{font-family:var(--lc-font-mono);letter-spacing:.02em;font-size:12px}

/* —— 4c. 质量验证（核心卖点） —— */
/* 统计带复用上区块 .lc-cov-stat；只把下边距收紧一档（32px），因为方法卡紧跟其后不需要大间隔 */
.lc-qa-stats{margin-bottom:32px}
/* 方法两卡：与功能 bento 的小卡同款 .lc-fcard，只改网格为对半 */
.lc-qa-methods{display:grid;grid-template-columns:1fr 1fr;gap:24px}
.lc-qa-points{display:flex;flex-direction:column;max-width:920px;margin-top:44px}
/* 结论行用 border-top 清单而非卡片：这里是"要读的文字"，卡片边框会把四条读成一个一个的孤立卖点 */
.lc-qa-pt{display:flex;flex-direction:column;gap:8px;padding:20px 0;border-top:1px solid var(--lc-border-faint)}
.lc-qa-t{display:flex;align-items:center;gap:10px;margin:0;font-size:18px;font-weight:600;color:var(--lc-text-1)}
.lc-qa-t svg{color:var(--lc-text-3);flex:none}
.lc-qa-d{margin:0;font-size:15px;line-height:24px;color:var(--lc-text-3)}
/* 收口句用强描边框：全区块唯一的"结论容器"，靠边框强度而非底色区分（本页无彩底） */
.lc-qa-final{margin:44px 0 0;padding:26px 30px;border:1.2px solid var(--lc-border-strong);border-radius:16px;font-size:17px;line-height:28px;font-weight:600;color:var(--lc-text-1)}

/* —— 4d. 开发者集成 —— */
/* 网格参数照抄 FAQ（360px + 1fr / gap 80）：又一个"左目录右正文"区块，不另造版式 */
.lc-dev{display:grid;grid-template-columns:360px 1fr;gap:80px;align-items:start}
.lc-dev-head{display:flex;flex-direction:column;gap:14px;align-items:flex-start}
.lc-dev-sub{margin:0;font-size:15px;line-height:24px;color:var(--lc-text-3)}
/* 文档出口是文字链+图标不是大按钮：这一区块的动作密度本来就低，别和收尾 CTA 抢主投 */
.lc-dev-doc{display:inline-flex;align-items:center;gap:8px;font-size:14px;font-weight:600;color:var(--lc-text-1);transition:opacity var(--lc-mo-release) var(--lc-mo-out)}
.lc-dev-doc:hover{opacity:.75}
.lc-dev-doc svg{flex:none}
.lc-dev-body{display:flex;flex-direction:column;gap:24px;min-width:0}
.lc-dev-feats{display:grid;grid-template-columns:repeat(3,1fr);gap:24px}
.lc-dev-feat{display:flex;flex-direction:column;gap:8px;padding:24px;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:16px;min-width:0}
.lc-dev-t{margin:0;font-size:16px;font-weight:600;color:var(--lc-text-1)}
.lc-dev-d{margin:0;font-size:13.5px;line-height:22px;color:var(--lc-text-3)}
/* 代码块底色用 --lc-deep（与大卡演示行同语法）：页内"嵌进去的界面片段"共用一种深度 */
.lc-code{border:1.2px solid var(--lc-border-card);border-radius:16px;overflow:hidden;background:var(--lc-deep)}
.lc-code-bar{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:12px 18px;border-bottom:1px solid var(--lc-border-faint);font-family:var(--lc-font-mono);font-size:12px;letter-spacing:.02em;color:var(--lc-text-3)}
/* 复制按钮与小号胶囊按钮同形（28 高/8 圆角）：按钮语汇总只有一档尺寸，不新开 */
.lc-code-copy{display:inline-flex;align-items:center;gap:6px;height:28px;padding:0 12px;border:1.2px solid var(--lc-border-pill);border-radius:8px;background:none;color:#C8CCD1;font:500 12px/1 var(--lc-font);cursor:pointer;transition:border-color var(--lc-mo-release) var(--lc-mo-out),color var(--lc-mo-release) var(--lc-mo-out)}
.lc-code-copy:hover{border-color:var(--lc-border-done);color:var(--lc-text-1)}
.lc-code-copy svg{display:block}
/* white-space:pre：curl 的反斜杠续行是内容的一部分，折行会把它变成一条读不懂的长句；窄屏靠横向滚动 */
.lc-code-body{margin:0;padding:18px 20px;overflow-x:auto;font-family:var(--lc-font-mono);font-size:13px;line-height:22px;color:var(--lc-text-2);white-space:pre}

/* —— 5. 价格方案 —— */
/* 三档等宽：价格要能横向对读，一旦不等宽就变成"各说各话"，比较关系直接消失 */
.lc-plans{display:grid;grid-template-columns:repeat(3,1fr);gap:24px}
/* align-items:flex-start：卡内元素顶对齐，价格数字长短不同也不会把下面的按钮错开 */
.lc-plan{display:flex;flex-direction:column;align-items:flex-start;gap:20px;padding:32px;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:16px}
.lc-plan:hover{border-color:var(--lc-border-pill)}
/* 反相档底色与描边同色：白卡上再画一圈浅边只会显脏，高亮靠"整块变白"完成 */
.lc-plan--pro{background:var(--lc-text-1);border-color:var(--lc-text-1);color:#000}
/* 徽标在白色卡上用实心黑：与它所在的反相卡共用同一套黑白语言，不引入第三种强调色 */
.lc-plan-badge{padding:6px 14px;border-radius:999px;background:#000;color:#fff;font-size:13px;font-weight:600}
.lc-plan-name{margin:0;font-size:20px;font-weight:600}
/* 价位是卡内唯一的大字号（36px）：读价格的人只看这一行，其余都要给它让位 */
.lc-plan-price{font-size:36px;font-weight:700;line-height:1.15}
.lc-plan-desc{margin:0;font-size:15px;color:var(--lc-text-3)}
/* 反相卡的次级文字必须走 on-light 档：黑底体系里的浅灰放到白底上直接不可读（2026-09-19 对比度整改） */
.lc-plan--pro .lc-plan-desc{color:var(--lc-text-on-light)}
/* 列表上边距只留 8px：按钮与功能点是一组，间距拉到 gap 会被读成两段内容 */
.lc-plan-feats{list-style:none;margin:8px 0 0;padding:0;display:flex;flex-direction:column;gap:12px}
.lc-plan-feats li{display:flex;align-items:center;gap:10px;font-size:14px;color:var(--lc-text-3)}
.lc-plan-feats svg{color:var(--lc-text-3);flex:none}
.lc-plan--pro .lc-plan-feats li{color:var(--lc-text-on-light)}
.lc-plan--pro .lc-plan-feats svg{color:var(--lc-text-on-light)}

/* —— 6. 活动奖励 —— */
/* 两卡等宽（1fr 1fr）：拉新与内容贡献是两条独立增长路径，做成大小卡会被读成主次 */
.lc-rewards{display:grid;grid-template-columns:1fr 1fr;gap:24px}
/* 奖励卡内边距 36px（比功能卡 28 大一档）：这两张是"给好处"的段落，容器的分量要更足 */
.lc-rw-card{display:flex;flex-direction:column;align-items:flex-start;gap:20px;padding:36px;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:16px}
.lc-rw-card:hover{border-color:var(--lc-border-pill)}
/* 只有奖励卡的图标底板升到 48：标题字号到了 22px，40 的底板会显得小气 */
.lc-rw-card .lc-ficon{width:48px;height:48px;border-radius:12px}
.lc-rw-t{margin:0;font-size:22px;font-weight:700;color:var(--lc-text-1)}
.lc-rw-d{margin:0;font-size:15px;line-height:24px;color:var(--lc-text-3)}
/* baseline 对齐：数字与单位字号差一倍，用 center 会让单位像挂在数字腰上 */
.lc-rw-num{margin:0;display:flex;align-items:baseline;gap:8px}
/* 数字是全卡最大字号且用等宽：先看到量、再看到口径，中英切换时宽度还不抖 */
.lc-rw-num b{font-family:var(--lc-font-mono);font-size:32px;font-weight:700;color:var(--lc-text-1)}
.lc-rw-num span{font-size:14px;color:var(--lc-text-3)}

/* —— 7. FAQ —— */
/* 左栏定宽 360 而非 1fr：标题栏要像"目录"，右侧问答才是被读的主体；80px 间距与区块共用同口径 */
.lc-faq{display:grid;grid-template-columns:360px 1fr;gap:80px}
.lc-faq-head{display:flex;flex-direction:column;gap:14px}
.lc-faq-sub{margin:0;font-size:15px;line-height:24px;color:var(--lc-text-3)}
/* 分隔线由每条自己的 border-top 承担：不另放 <hr>，条距天然等宽且末条不会多出尾线 */
.lc-faq-item{border-top:1px solid var(--lc-border-faint);padding:20px 0;display:flex;flex-direction:column;gap:10px}
/* 问题用 h3 但字号只到 18：它比区块标题小，语义上又要在同一条朗读层级里 */
.lc-faq-q{margin:0;font-size:18px;font-weight:600;color:var(--lc-text-1)}
.lc-faq-a{margin:0;font-size:15px;line-height:24px;color:var(--lc-text-3)}

/* —— 6b. 更新日志 —— */
/* 单列限宽 820：这是"台账"不是正文流，行长失控会让日期列与内容读成两栏报纸 */
.lc-cl-list{display:flex;flex-direction:column;max-width:820px}
/* 分隔线口径与 FAQ 条目完全一致：同一页里"逐条可读"的内容用同一种线 */
.lc-cl-item{display:grid;grid-template-columns:112px 1fr;gap:24px;padding:20px 0;border-top:1px solid var(--lc-border-faint)}
/* 日期等宽字体：五条日期数位天然对齐，左侧收成一根竖线 */
.lc-cl-date{font-family:var(--lc-font-mono);font-size:13px;color:var(--lc-text-3);padding-top:4px}
.lc-cl-t{margin:0;font-size:16px;font-weight:600;color:var(--lc-text-1)}
.lc-cl-d{margin:6px 0 0;font-size:14px;line-height:22px;color:var(--lc-text-3)}

/* —— 7b. 关于我们 —— */
/* 网格参数照抄 FAQ（360px + 1fr / gap 80）：两个"左目录右正文"区块不该各造一套版式 */
.lc-about{display:grid;grid-template-columns:360px 1fr;gap:80px}
.lc-about-head{display:flex;flex-direction:column;gap:14px;align-items:flex-start}
.lc-about-p{margin:0;font-size:15px;line-height:24px;color:var(--lc-text-3)}
.lc-about-pt{border-top:1px solid var(--lc-border-faint);padding:20px 0;display:flex;flex-direction:column;gap:8px}
/* 标题 18px 与 FAQ 问题同档：三条是"陈述"不是"问答"，但阅读层级要一致 */
.lc-about-t{margin:0;font-size:18px;font-weight:600;color:var(--lc-text-1);display:flex;align-items:center;gap:10px}
.lc-about-t svg{color:var(--lc-text-3);flex:none}
.lc-about-d{margin:0;font-size:15px;line-height:24px;color:var(--lc-text-3)}

/* —— 8. 收尾 CTA —— */
/* 上边距给 0：它紧贴上一区块（关于我们），靠白块本身的反相与上文切开，不需要再留一段黑 */
.lc-cta-wrap{padding:0 80px 80px}
/* 整页唯一的大面积白 + 居中排版：读到这里只剩一个动作，因此取消所有左对齐的信息密度 */
.lc-cta{display:flex;flex-direction:column;align-items:center;gap:20px;padding:60px 80px;background:var(--lc-text-1);border-radius:20px;text-align:center}
.lc-cta-t{margin:0;font-size:clamp(24px,2.22vw,32px);font-weight:700;color:#000}
/* 白块里的次级文字同样只能取最深那档灰：比 #000 弱一级，既读得清又不抢标题 */
.lc-cta-sub{margin:0;font-size:16px;color:var(--lc-text-4)}

/* —— 8b. 收尾留资表单（★ P1-3；结构见 components/LeadForm.tsx，本页带状态组件之一，另一处是 DevSample；★ #22 HeroDemo 已退役） —— */
/* 分隔线两侧各一段 1px 短线：把"免费自助注册"与"留资等回电"两条路在视觉上并置成二选一 */
.lc-lead-sep{display:flex;align-items:center;gap:14px;width:100%;max-width:560px;font-size:13px;color:var(--lc-text-4)}
.lc-lead-sep::before,.lc-lead-sep::after{content:'';flex:1;height:1px;background:rgba(0,0,0,.12)}
/* 表单与分隔线同宽：白块居中排版下两条"路"共享同一根行长轴；relative 给蜜罐的 absolute 定位兜底 */
.lc-lead{position:relative;width:100%;max-width:560px;display:flex;flex-direction:column;gap:14px;text-align:left}
.lc-lead-row{display:flex;gap:14px}
.lc-lead-field{flex:1;display:flex;flex-direction:column;gap:6px;min-width:0}
.lc-lead-label{font-size:13px;font-weight:600;color:rgba(0,0,0,.68)}
/* 白块上的输入框必须显式给白底：.lc-input 基类是深色主题底，直接复用会黑成一团 */
.lc-lead-input{width:100%;background:#fff;border:1px solid rgba(0,0,0,.16);border-radius:10px;padding:10px 12px;font-size:14px;color:#000}
.lc-lead-input:focus{outline:none;border-color:#000}
.lc-lead-msg{resize:vertical;min-height:52px;font-family:inherit}
.lc-lead-langs{display:flex;flex-direction:column;gap:8px}
.lc-lead-chips{display:flex;flex-wrap:wrap;gap:8px}
/* 语言胶囊沿用全站 radius 999 口径；选中态=白块上的反相（黑底白字），与主按钮同语法 */
.lc-lead-chip{height:30px;padding:0 14px;border:1px solid rgba(0,0,0,.16);border-radius:999px;background:#fff;color:rgba(0,0,0,.72);font-size:13px;cursor:pointer;transition:border-color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out)}
.lc-lead-chip:hover{border-color:#000}
.lc-lead-chip.on{background:#000;border-color:#000;color:#fff}
/* 蜜罐：不用 display:none（部分 bot 会跳过隐藏域），用 1px 裁剪——人眼不可见、仍在 DOM */
.lc-lead-hp{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}
.lc-lead-captcha{align-self:flex-start}
/* 报错只用判错红（与演示卡划线同色系），不引入第二套告警色 */
.lc-lead-err{margin:0;font-size:13px;color:#c0392b}
/* 提交按钮 = 白块上的主投反相（与收尾 Pill 同形）：同屏两颗黑胶囊只有一颗是"最终动作" */
.lc-lead-btn{align-self:flex-start;height:46px;padding:0 26px;border:0;border-radius:999px;background:#000;color:#fff;font-size:15px;font-weight:600;cursor:pointer}
.lc-lead-btn:disabled{opacity:.55;cursor:default}
/* 成功回执：中性描边框住一句 status 文案，不动用绿色（本页无绿口径） */
.lc-lead-ok{display:flex;align-items:center;gap:10px;width:100%;max-width:560px;padding:16px 18px;border:1px solid rgba(0,0,0,.14);border-radius:12px;background:rgba(0,0,0,.03);font-size:14px;color:#000;text-align:left}

/* —— 9. 页脚 —— */
.lc-foot{padding:60px 80px 40px}
/* 品牌区与链接列用 flex-wrap：窄屏整列换行，而不是把链接挤断成两行 */
.lc-foot-top{display:flex;gap:80px;flex-wrap:wrap}
.lc-foot-brand{width:320px;display:flex;flex-direction:column;gap:14px}
/* 页脚复用导航的品牌类名，只把字号调小：标识在整站只允许一套画法 */
.lc-foot-brand .lc-nav-brand{font-size:20px}
.lc-foot-tag{margin:0;font-size:14px;line-height:22px;color:var(--lc-text-3)}
.lc-foot-cols{display:flex;gap:80px;flex-wrap:wrap}
.lc-foot-col{display:flex;flex-direction:column;gap:12px;font-size:14px}
.lc-foot-col b{font-weight:600;color:var(--lc-text-1)}
.lc-foot-col a{color:var(--lc-text-3);transition:color var(--lc-mo-release) var(--lc-mo-out)}
.lc-foot-col a:hover{color:var(--lc-text)}
/* 分隔线用 1px 空 div：要的是"上 40 下 20"的不对称呼吸，线更贴近版权那一行 */
.lc-foot-div{height:1px;background:var(--lc-border-card);margin-top:40px}
.lc-foot-bot{padding-top:20px;font-size:13px;color:var(--lc-text-4)}

/* —— 响应式：断点只改布局与留白，不改视觉语言（颜色、描边、字号档一律复用上面的规则） —— */
/* 1200px：首屏从左右两栏折成上下两段，同时把 80px 外边距收到 40px，bento 顶栏并为单列 */
@media (max-width:1200px){
  .lc-hero{padding:48px 40px;min-height:0} /* 折段后首屏高度约束必须清掉，否则演示卡下方留大片黑 */
  .lc-hero-in{flex-direction:column;align-items:flex-start}
  .lc-hero-copy{width:100%;max-width:560px} /* 定宽改上限：窄屏要占满，但仍给行长留顶 */
  .lc-hero-demo{width:100%}
  .lc-bento-top{grid-template-columns:1fr}
  .lc-bento-row{grid-template-columns:repeat(2,1fr)} /* 小卡先折 2×2：四张并排到 620px 才收成单列 */
  .lc-sec{padding:56px 40px}
  .lc-mkt-nav{padding:0 40px}
  .lc-cta-wrap,.lc-foot{padding-left:40px;padding-right:40px}
}
/* 980px：所有多列 grid 收单列；三步流程转竖排并把箭头旋转 90°；导航锚点直接隐藏（六个锚点不是必读信息，
   宁可少一个入口也不在这里造汉堡菜单） */
@media (max-width:980px){
  .lc-plans{grid-template-columns:1fr} /* 价格竖排后靠 DOM 顺序读：免费→专业→企业，推荐档仍在中间 */
  .lc-rewards{grid-template-columns:1fr}
  .lc-qa-methods{grid-template-columns:1fr} /* 两张方法卡竖排：并排时盲评说明整段会被压成窄条 */
  .lc-faq{grid-template-columns:1fr;gap:32px} /* 标题栏与问答之间只需一小段距离，80px 会把答案推到首屏外 */
  .lc-about{grid-template-columns:1fr;gap:32px} /* 与 FAQ 同口径：单列后 80px 会把三条方式推出首屏 */
  .lc-dev{grid-template-columns:1fr;gap:32px} /* 同上：左栏定长在窄屏只会把右栏正文挤瘪 */
  .lc-dev-feats{grid-template-columns:1fr} /* 三张能力卡竖排：路径文案 /openapi/v1/… 较长，横排会挤到折行 */
  .lc-cov-stats{grid-template-columns:repeat(2,1fr)} /* 四联排两联：单列会让统计带比它要引出的内容还长 */
  .lc-cl-item{grid-template-columns:1fr;gap:4px} /* 日期升到标题正上方：112px 定长在窄屏只会挤瘪正文 */
  .lc-steps{flex-direction:column}
  .lc-steps-slot{align-items:flex-start}
  .lc-step-arrow{transform:rotate(90deg);padding-left:28px} /* 箭头旋转并左缩进到卡的内边距线上 */
  .lc-nav-links{display:none}
}
/* 620px：手机档，只收留白与换行，不再改结构 */
@media (max-width:620px){
  .lc-mkt-nav{padding:0 16px;gap:16px}
  .lc-hero{padding:36px 16px}
  .lc-hero-ctas{flex-wrap:wrap} /* 两颗按钮允许换行：宁可上下叠，也不把文案压到 12px */
  .lc-hero-trust{flex-wrap:wrap;gap:12px 18px} /* 三条指标换行后给"行距 12 / 列距 18"：宁可叠三行也不缩字号（字号一缩就成脚注） */
  .lc-bento-row{grid-template-columns:1fr} /* 2×2 在这一档也撑不住：单列后卡片仍有整行宽度放 24 图标 + 标题 */
  .lc-sec{padding:44px 16px} /* 段落上下留白同步下调：窄屏一屏只滚过一两个区块，56px 会让页面看着空 */
  .lc-cta-wrap{padding:0 16px 44px}
  .lc-cta{padding:40px 24px} /* 收尾白块只收左右：它是本页唯一"必须读完"的区域，行长优先于留白 */
  .lc-lead-row{flex-direction:column} /* 手机档公司/邮箱上下叠：并排时输入框会窄到放不下一个邮箱地址 */
  .lc-lead-btn{align-self:stretch;text-align:center} /* 提交按钮拉满整行：拇指落点越大越好 */
  .lc-foot{padding:44px 16px 28px}
  .lc-foot-cols{gap:36px} /* 列间距 80→36：80px 会在内容换行之前先把第三列顶到下一行 */
}
/* 400px 以下：导航那颗 sm 按钮再降一档，避免与"登录"挤成两行 */
@media (max-width:400px){
  .lc-nav-cta .lc-mkt-btn--sm{height:36px;padding:0 14px;font-size:13px}
}

`

