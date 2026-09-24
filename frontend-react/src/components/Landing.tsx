// ============================================================================
// components/Landing.tsx — 官网营销首页（2026-09-18 按画布 721173146990823 屏 6:1 重建）
// 区块顺序：导航 → Hero(左文案+右留资引导卡) → 解决方案三步 → 核心功能 bento →
// 覆盖范围 → 质量验证 → 开发者集成 → 价格三档 → 活动奖励 → 更新日志 → FAQ → 关于我们 → 收尾 CTA → 页脚。
// 动效：Hero 文案 lc-mo-up 节拍入场；★ 2026-09-19 #22：原「三检查点翻译流演示卡」（HeroDemo）
// 连同两颗「预约演示」按钮一起退役，转化入口统一为「留言获取方案」（锚到 #cta 留资表单）；
// 其「划掉错词→亮起正词」动效已抽到 components/WordSwap + theme.css，供产品内加载态复用（#24）。
// 其余区块 useReveal 滚动现身，组内 60ms 等速 stagger（动效原则 8）。
// 视觉：纯黑底、卡片描边 var(--lc-border-card) 2px 纯白（★ 〇-O）、主按钮白底黑字、无蓝无绿。
// ============================================================================
/* 依赖口径（三条硬约束，改本文件前先确认）：
   1. useReveal 与 motion.css 的 .lc-reveal 配对使用——只挂类名不调 hook，元素会永远停在 opacity:0；
   2. 图标一律取自 langcross 线性图标集，禁止混入其它图标库（官网视觉的唯一来源）；
   3. 文案 100% 走 useT 的 land.* 键，词条在 src/i18n/panels/landing.ts，中英两份必须同步补
      （AGENTS.md 约定 5；本文件不出现任何裸中文文案；演示数据 DEMO_* 的文案也走 land.demo* 键，
      由 demoSrc/demoFinal/demoTerms 工厂按当前 UI 语种取，不在此写死）。 */
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
import { useT, translateIn, demoSrcLang, type Lang } from '@/i18n' // useT() → [语种, t, tpl]；translateIn 按指定语种取词；demoSrcLang 决策演示源语种（en→zh）；版权行用 tpl 插值品牌名
import { typingUnitOf, typingSpeedOf } from '@/i18n/script' // ★ 〇-Q：打字单元/速度按文字系统分档
import { useBranding } from '@/branding' // 租户品牌信息：brandName 为空即回落产品名
import { openAPIDocsUrl } from '@/api/core' // 公开 API 文档地址（同源 /openapi/docs）
import { LeadForm } from '@/components/LeadForm' // ★ P1-3 收尾留资表单（自带状态，唯一例外）
import { LangSelect } from '@/components/LangSelect' // ★ 2026-09-20 反馈④：落地页对非中文访客给出手动切换入口（12 语种，与顶栏同一组件）
import { INDUSTRY_META, industryName } from '@/lib/industries' // 覆盖范围区块：行业包与本页术语大卡同源的一份事实
import { PERSONA_FALLBACK } from '@/lib/personas' // 覆盖范围区块：八个角色 code 与后端 persona 包对齐

/* —— Hero 演示卡固定内容（★ 〇-Q：文案抽成 land.demo* 键，按演示源语种取，已补 10 语种译文；
   画布 6:55 / hero-stream.html 现役三处；★ #22 更正后演示卡归位，术语大卡对照与 curl 示例继续共用这份数据） —— */
// 演示源语种决策（英语 UI 取 en，演示 EN→ZH）见 i18n/index.ts 的 demoSrcLang；此前误折回 zh（ZH→EN）已翻正
// 演示文案工厂：按 srcLang（英语时为 'zh'）取源句/术语，定稿目标恒为英文；避免模块级写死中文
function demoSrc(srcLang: Lang): string {
  return translateIn(srcLang, 'land.demoSrc')
}
function demoFinal(lang: Lang): string {
  // 定稿译文：英语 UI 演示 EN→ZH，定稿取中文源句（land.demoSrc 的 zh 版）；
  //   其余语种定稿为英文（land.demoFinal 键）。与 demoTerms 的 r 字段逐一对应（kickoff/benchmark/lead）。
  return lang === 'en' ? translateIn('zh', 'land.demoSrc') : translateIn('en', 'land.demoFinal')
}
// 落地页术语高亮演示要用的词条与译文对
function demoTerms(srcLang: Lang) {
  // 三检查点唯一数据源：w=机翻初译（判错项），r=行业正译（纠正项），cn=源语种术语（量尺标签 + 行内主语）
  return [
    { w: 'start', r: 'kickoff', cn: translateIn(srcLang, 'land.demoTerm1') },
    { w: 'compare', r: 'benchmark', cn: translateIn(srcLang, 'land.demoTerm2') },
    { w: 'clue', r: 'lead', cn: translateIn(srcLang, 'land.demoTerm3') },
  ] as const // as const：长度与字面量类型都锁死，节拍时长直接取 .length 才不会漂
}

/* —— 覆盖范围 / 开发者区块的固定数据（FILE_FORMATS 为文件格式白名单；curl 示例 text 取自 land.demoSrc） —— */
// 文件工单实际可解析的格式（取自后端 internal/fileproc 白名单的对外主流档位；
// docm/xlsm/ppsm 等宏变体与 ttc 字体包同在，故全站口径写「30+」而不是这里数出来的 20）
const FILE_FORMATS = [
  'DOC', 'DOCX', 'PPT', 'PPTX', 'XLS', 'XLSX', 'PDF', 'TXT', 'MD', 'JSON',
  'XML', 'YAML', 'CSV', 'RTF', 'SRT', 'VTT', 'EPUB', 'ODT', 'ODS', 'ODP',
] as const
// 示例 curl 逐字段对齐后端 openapi.v1.json：X-API-Key 鉴权头、text 必填、
// target_langs 数组、mode ∈ fast|pro。域名与 Key 用环境变量占位，复制过去即可直接跑；
// text 取当前语种演示源句（land.demoSrc），与演示卡同一份事实
function devSampleCmd(src: string): string {
  return [
    'curl -X POST "$LANGCROSS_HOST/openapi/v1/translate" \\',
    '  -H "X-API-Key: $LANGCROSS_API_KEY" \\',
    '  -H "Content-Type: application/json" \\',
    `  -d '{"text":"${src}","target_langs":["en"],"mode":"pro"}'`,
  ].join('\n')
}

/* 时序参数（ms）：移植自 hero-stream.html，节奏只改这一处。
   ★ 〇-Q：原 cnSpeed/wordSpeed/rightSpeed/srcSpeed/statusSpeed/finalSpeed 六档绝对速度已删除，
   改由打字机按「文字系统基准速度（script.ts 的 typingSpeedOf）× 下方 MUL 倍率」实时算出——
   换语种后整段节奏同比例平移而不走形，且 CJK 逐字 / 拉丁逐词的单位差异也一并生效。 */
const T = {
  enter: 240, cnHold: 220, arrowIn: 180, arrowHold: 40, // 单行入场 / 中文停留 / 箭头进 / 箭头停
  wordHold: 200, strike: 340, // 初译停留 / 划掉动画
  lock: 460, glowHold: 300, hold: 840, exit: 320, gap: 200, // 正解定版 / 辉光余韵 / 读题停留 / 退场 / 行间空隙
  srcHold: 460, statusHold: 200, // 原文段读完停留；状态段同构
  armHold: 660, breath: 600, peak: 460, sweepAt: 240, settleAt: 440, // 量尺蓄力 / 爆发前吸气 / 峰值时长 / 峰值后扫光·落定偏移
  unmergeAt: 660, proof: 700, resultHold: 2400, // 亮度回收偏移 / 审校光 / 结果可读停留
  tailLead: 340, tailHold: 850, // 盖章前置留白 / 尾拍退场
}
/* 每段相对「脚本基准速度」的倍率：保留原 demo 的内部节奏（术语比原文慢一档、定稿比术语快一截），
   倍率无量纲，随语种基准一起缩放，所以换语种后整段节奏同比例平移而不走形。
   取值还原自原绝对速度：cn 62/42、status 34/42、w 42/38、r 46/38、final 16/38。 */
const MUL = {
  src: 1,
  cn: 62 / 42,
  status: 34 / 42,
  w: 42 / 38,
  r: 46 / 38,
  final: 16 / 38,
} as const
/* 打字机每字实际耗时 = speed + random()*speed*0.5，均值即 1.25×speed；
   顶部进度条总时长要靠它折算，否则条会跑在字前面干等 */
const TYPE_AVG = 1.25
const LEAD = 200 // 每轮 reset 之后先静一拍再下笔：避免循环接缝处上一帧还没落定就冒字

/** 三检查点翻译流演示卡（hero-stream.html 的 React 移植） */
function HeroDemo() {
  const [lang, t] = useT() // 语种用于驱动打字单元/速度分档（CJK 逐字 / 拉丁逐词，速度随文字系统变）
  const SRC_LANG = demoSrcLang(lang) // ★ 〇-Q 修正：英语 UI 固定以英文为源（展示真实 EN→ZH）
  const DEMO_SRC = demoSrc(SRC_LANG) // 源句按演示源语种取（land.demoSrc）
  const DEMO_FINAL = demoFinal(lang) // 定稿译文：英语 UI 取中文（EN→ZH），其余语种恒为英文
  const DEMO_TERMS = demoTerms(SRC_LANG) // 三术语的 cn 字段按演示源语种取
  const branding = useBranding()
  const demoRef = useRef<HTMLDivElement>(null) // 挂在最外层 .hd 上：所有 data-hd 查询都以它为根，不污染 document
  // 复制按钮：用户主动点击才写剪贴板（写操作不弹权限），1.6s 后标签回弹
  const [copied, setCopied] = useState(false)
  const copyFinal = () => {
    navigator.clipboard?.writeText(DEMO_FINAL).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    }).catch(() => { /* 非安全上下文剪贴板不可用：静默失败，演示不受影响 */ })
  }
  const statusStr = t('land.demoStatus') // 「正在比对汽车行业术语库 · 命中 3 处术语」——长度参与总时长折算

  useEffect(() => {
    const root = demoRef.current
    if (!root) return // 首帧 ref 未就位（理论上不会发生）时直接放弃，不抛错
    // 20+ 个动画目标全部用 data-hd 选择器一次性取齐：比 20 个 useRef 干净，
    // 且这些节点在 JSX 里是静态结构（不随状态增删），缓存引用不会失效
    const el = (n: string) => root.querySelector<HTMLElement>(`[data-hd="${n}"]`)
    // 下面统一用 ! 断言而不是逐个判空：节点改成条件渲染时，第一帧就会在控制台报
    // "classList of null"，比"动画静默不播"更容易被发现（这类演出坏一半最难查）
    const panel = el('panel')! // 卡外壳：整卡的明暗/边框/下沉状态都挂在它身上
    const barfill = el('barfill')! // 顶部 1px 进度条：跑满时长 = 一轮完整循环（--dur）
    const srcEl = el('src')! // 原文打字区（盖在 ghost 占位之上的"真身"）
    const statusEl = el('status')! // 比对状态文案本体：逐字打出来
    const statusLn = el('statusline')! // 状态行容器：只负责整行淡入，不参与打字
    const stepsEl = el('steps')! // 量尺区：armed=蓄力完成，hot=峰值高亮
    const rowEl = el('row')! // 术语行：一次只演一条，靠 in/out 上下场
    const idxEl = el('idx')! // 行首序号 01/02/03：红色即"此处判错"的信号色
    const cnEl = el('cn')! // 本行要讲的中文术语
    const arrowEl = el('arrow')! // 中→英推进箭头：中文打完才出现
    const wEl = el('w')! // 机翻初译词位：struck 类给它挂删除红线
    const wtEl = el('wt')! // 初译的文字节点：红线是它的 ::before，二者必须分开
    const rEl = el('r')! // 行业正译词：全卡字号最大的一行，也是"判对"的落点
    const resultEl = el('result')! // 回写结果框：in/sweep/settled/stamp 四段状态依次叠加
    const finalEl = el('final')! // 定稿译文打字区（同样是 ghost 之上的真身）
    const ruleEl = el('rule')! // 爆发前的下笔线：从左写出再隐去，给峰值一个起手
    const flashEl = el('flash')! // 爆发光斑：一次性 burst，只允许"一帧全爆"
    const markEl = el('mark')! // 完成对勾：draw 画线 + glow 辉光
    const ringEl = el('ring')! // 对勾外扩散环：与 draw 同时起，撑出"落章"体积
    const labelEl = el('rlabel')! // 「翻译完成」标签：stamp 时闪一次白
    const metaEl = el('meta')! // 「3 / 3 处术语已校准」副标：lit 之后才转亮
    const wrapEl = el('fwrap')! // 定稿文字外层：::after 就是那道审校扫光
    const gfill = el('gfill')! // 量尺已填充段：宽度按"第几条"百分比推进
    const bead = el('bead')! // 尺珠：与 gfill 同步走位，每判完一条 snap 脉冲一次
    const labels = Array.from(root.querySelectorAll<HTMLElement>('.hd-sl')) // 量尺下三个中文标签：cur=正在判、done=已过

    // 代际计数器：effect 重跑 / 卸载时 ++gen，各 await 之后比对 g !== gen 即 return，
    // 防止上一轮残留的异步链继续往新 DOM 上写字（语言切换时最容易撞到这个竞态）
    let gen = 0
    const sleep = (ms: number) => new Promise<void>((res) => window.setTimeout(res, ms))
    // 演示源语种见组件顶部 SRC_LANG（英语 UI 固定以中文为源，展示真实 ZH→EN）；译文与机翻初译恒为英文
    const TR_LANG: Lang = 'en'
    // 逐字/逐词打字：每拍都查一次代际，被打断时立刻弃疗（不查会让上一轮的字继续吐进新文本）。
    // ★ 〇-Q：单元（CJK 逐字 / 拉丁逐词）与速度（typingSpeedOf）都按文字系统走，动效随语种变。
    const type = async (node: HTMLElement, text: string, l: Lang, mul: number, g: number) => {
      const unit = typingUnitOf(l)
      const speed = typingSpeedOf(l) * mul
      node.textContent = '' // 起点必须是零：缺字/串字都从这一行防住
      node.classList.add('typing') // typing 只负责光标（::after 闪烁），打完立刻摘；多个节点共用同一动画名
      if (unit === 'word') {
        // 拉丁/西里尔/阿语：逐词推进——逐字母打会像乱码闪烁，且长词打到一半换行很难看
        const toks = text.split(/(\s+)/) // 保留空白 token，重建后文本与原文逐字符一致
        let acc = ''
        for (const tk of toks) {
          if (g !== gen) return
          acc += tk
          node.textContent = acc
          await sleep(speed + Math.random() * speed * 0.5) // 非匀速才像人敲（均值 1.25×speed，即 TYPE_AVG）
        }
      } else {
        for (let i = 0; i < text.length; i++) {
          if (g !== gen) return
          node.textContent += text.charAt(i) // 直写 textContent 而不是 setState：每秒几十次打字不该压给 React 渲染
          await sleep(speed + Math.random() * speed * 0.5)
        }
      }
      node.classList.remove('typing')
    }
    // 重播一次性动画：先摘类 → 读 offsetWidth 强制重排（否则同一帧内摘加同类，浏览器视为无变化）→ 再加回
    const replay = (n: HTMLElement, c: string) => {
      n.classList.remove(c)
      void n.offsetWidth
      n.classList.add(c)
    }
    // 单行（一条术语）复位：只清这一行的类与文本，量尺/进度条状态要跨行保留
    const clearRow = () => {
      rowEl.classList.remove('in', 'out', 'lit') // 三个都是"一次性上下场/脉冲"类，留着会让下一行直接跳到终态
      wEl.classList.remove('struck')
      wtEl.textContent = ''
      arrowEl.classList.remove('in')
      idxEl.classList.remove('pop')
      rEl.textContent = ''
      rEl.classList.remove('typing', 'glow', 'lock') // 正解行三种状态同源：漏摘任何一个，下一行就自带"已判完"的错觉
      cnEl.textContent = ''
      cnEl.classList.remove('in')
      void rowEl.offsetWidth // 落地一次重排：保证下面重新 add('in') 时入场动画真的从头播
    }
    // 整轮复位：把演示卡擦回空白（循环的每一轮都从这里起步，也保证重挂时无残留态）
    const reset = () => {
      srcEl.textContent = ''
      srcEl.classList.remove('typing')
      statusEl.textContent = ''
      statusLn.classList.remove('in')
      barfill.classList.remove('run') // 进度条靠动画跑，必须摘类回到 width:0
      clearRow()
      stepsEl.classList.remove('armed', 'hot')
      labels.forEach((s) => s.classList.remove('done', 'cur')) // 三个量尺标签回到"未处理"（opacity 由 armed 动画给）
      gfill.style.width = '0%'
      bead.style.insetInlineStart = '0%' // 尺珠与填充段用 style 直写而非类：它们带 1.05s 过渡，走类会和动画抢时序
      bead.classList.remove('snap')
      resultEl.classList.remove('in', 'sweep', 'settled', 'stamp') // 结果框四段状态一次清空，漏一段下一轮就会"提前完成"
      /* 下面六条清的都是"一次性动画"的挂载类：它们带 forwards/终帧，不摘下一轮就会直接跳到结束画面 */
      markEl.classList.remove('draw', 'glow')
      ringEl.classList.remove('go')
      ruleEl.classList.remove('write')
      flashEl.classList.remove('burst')
      labelEl.classList.remove('stamp')
      wrapEl.classList.remove('proof')
      panel.classList.remove('bright', 'blaze', 'punch', 'drop', 'dim') // 整卡明暗五态全清：峰值/落章/下沉/压暗都不该跨轮存活
      metaEl.classList.remove('lit')
      finalEl.textContent = ''
      finalEl.classList.remove('typing', 'done')
      void panel.offsetWidth // 同 clearRow：把「全部摘类」这一帧落定，新一轮的动画才有起跳点
    }
    /* MOT：蓄力（下笔线）→ 一帧全爆 → 余韵逐拍衰减 */
    const ceremony = (g: number) => {
      // 峰值不是一条长动画，而是"同时点亮一组元素"的编排：下面每个 set 都独立带代际校验，
      // 用户中途切语言/离开页面时，未触发的节拍会被静默丢弃，不会留下半截高亮
      const set = (ms: number, fn: () => void) => {
        window.setTimeout(() => { if (g === gen) fn() }, ms)
      }
      ruleEl.classList.add('write')
      set(T.peak, () => {
        panel.classList.remove('dim') // 与前一拍的压暗同帧交换：暗→亮的落差就是"峰值"本身
        // 下面七条同一帧挂：峰值的强度来自"多处一起亮"，拆成两帧就会被读成两个独立事件
        flashEl.classList.add('burst')
        resultEl.classList.add('in')
        markEl.classList.add('draw', 'glow')
        ringEl.classList.add('go')
        labelEl.classList.add('stamp')
        stepsEl.classList.add('hot') // 量尺进入热态：填充段与标签在 CSS 里改成 0.1s 硬切，跟住这一拍
        panel.classList.add('bright')
      })
      set(T.peak + T.sweepAt, () => resultEl.classList.add('sweep')) // 峰值后 240ms：审校光斜扫过结果框
      set(T.peak + T.settleAt, () => {
        resultEl.classList.add('settled') // 440ms：描边转"完成色"，副标转亮，视觉从爆发切回可读
        metaEl.classList.add('lit')
      })
      set(T.peak + T.unmergeAt, () => {
        panel.classList.remove('bright') // 660ms：亮度与热态一起回收，同时把最后一个标签判为已过
        stepsEl.classList.remove('hot')
        labels.forEach((s) => { s.classList.remove('cur'); s.classList.add('done') })
      })
    }
    /** 终态快照：供「偏好减少动效」使用，内容与演出结果完全一致 */
    const staticState = () => {
      // 偏好减少动效：直接呈现终态（量尺三格全过、回写区落定）
      // 写法：只挂"终帧"状态类，不排任何计时——动画由文件末尾的 reduced-motion 规则统一掐掉
      srcEl.textContent = DEMO_SRC
      statusEl.textContent = statusStr
      statusLn.classList.add('in')
      stepsEl.classList.add('armed')
      labels.forEach((s) => s.classList.add('done'))
      gfill.style.width = '100%'
      bead.style.insetInlineStart = '100%' // 终值直写：该分支下 CSS 已把过渡与动画全关掉，不会自己滑过去
      const last = DEMO_TERMS[DEMO_TERMS.length - 1] // 术语行是同一位置的绝对定位，终态只需摆出最后一条
      idxEl.textContent = '03' // 与 DEMO_TERMS 条数手工绑定：增减术语时这一行必须同步改
      cnEl.textContent = last.cn
      cnEl.classList.add('in')
      arrowEl.classList.add('in')
      wtEl.textContent = last.w
      wEl.classList.add('struck') // 保留红线：终态也要讲清"这里被纠正过"，只给正解等于丢掉卖点
      // 以下六条按演出的收尾顺序补齐画面：正解 → 行 → 结果框 → 对勾 → 统计 → 定稿，少挂一类就会露出"半成品"
      rEl.textContent = last.r
      rowEl.classList.add('in')
      resultEl.classList.add('in', 'settled')
      markEl.classList.add('draw')
      metaEl.classList.add('lit')
      finalEl.textContent = DEMO_FINAL
    }

    // 减少动效：整条计时链根本不启动（不是"播完再跳"），信息一字不少只是不动；
    // matchMedia?.() 的可选链会短路后面整条表达式，没有该 API 的环境（jsdom）安全地拿到 undefined
    if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
      staticState()
      return () => { gen++ } // 这条分支没有计时链要断，但清理函数形状与主分支保持一致，便于两处对照
    }

    /* 顶部进度条时长 = 整段循环时长。
       打字机每字耗时是 speed + random()*speed*0.5（均值 1.25×speed），
       必须乘 TYPE_AVG，否则进度条会提前跑完干等下一轮。 */
    // 一段打字的期望耗时：单元（字/词）取数方式与 type() 完全一致，否则进度条会与演出脱节
    const typed = (text: string, l: Lang, mul: number) => {
      const unit = typingUnitOf(l)
      const n = unit === 'word'
        ? Math.max(1, text.trim().split(/\s+/).filter(Boolean).length) // 逐词：按空格分词计数
        : text.length // 逐字：按字符计数（泰文无空格也按字符，等价于逐音节）
      return Math.round(n * typingSpeedOf(l) * mul * TYPE_AVG)
    }
    // 一条术语行的耗时 = 入 → 中文打字 → 停 → 箭头 → 初译打字 → 停 → 划掉 → 正解打字 → 定版 → 余韵 → 读题 → 退 → 间隙；
    // 必须和下面 play() 里的 await 顺序逐拍对齐，改动画顺序时这里同步改，否则进度条与演出脱节
    const rowMs = DEMO_TERMS.reduce((sum, tm) => sum
      + T.enter
      + typed(tm.cn, SRC_LANG, MUL.cn) + T.cnHold
      + T.arrowIn + T.arrowHold
      + typed(tm.w, TR_LANG, MUL.w) + T.wordHold
      + T.strike
      + typed(tm.r, TR_LANG, MUL.r) + T.lock + T.glowHold
      + T.hold + T.exit + T.gap, 0)
    // 一整轮：静一拍 → 原文 → 状态行 → 量尺蓄力 → 三条术语 → 吸气 → 峰值 → 定稿打字 → 审校光 → 结果停留 → 尾拍
    const totalMs = LEAD
      + typed(DEMO_SRC, SRC_LANG, MUL.src) + T.srcHold
      + typed(statusStr, SRC_LANG, MUL.status) + T.statusHold + T.armHold
      + rowMs
      + T.breath + T.peak
      + typed(DEMO_FINAL, TR_LANG, MUL.final) + T.proof + T.resultHold
      + T.tailLead + T.tailHold
    root.style.setProperty('--dur', `${totalMs}ms`) // 只交给 CSS 的 .hd-barfill 用（见文末 var(--dur)）

    // 一轮完整演出：原文打字 → 状态行 → 量尺蓄力 → 三条术语逐处判错/纠正 → 回写峰值 → 定稿 → 自循环
    async function play() {
      const g = ++gen // 每轮占一个新代际，上一轮若有悬空 await 会自然熄火
      reset() // 每轮都从空白起跳：也是首次挂载时唯一的"清场"入口
      await sleep(LEAD)
      if (g !== gen) return
      barfill.classList.add('run') // 进度条与演出同帧起跑，二者时长同源于 --dur，不会各跑各的

      await type(srcEl, DEMO_SRC, SRC_LANG, MUL.src, g) // 第一拍先给"题面"：观众得先读懂原文，后面才看得懂纠正
      if (g !== gen) return
      await sleep(T.srcHold)
      if (g !== gen) return

      statusLn.classList.add('in') // 状态行先淡入再打字：光标得先有承载体
      await type(statusEl, statusStr, SRC_LANG, MUL.status, g) // 词条里的「3 处」与 DEMO_TERMS 条数靠人工对齐：加术语必须同步改中英两份词条
      if (g !== gen) return
      await sleep(T.statusHold)
      if (g !== gen) return

      stepsEl.classList.add('armed') // 量尺轨道展开 + 三个标签逐拍入场（全由 .armed 驱动，无需 JS 排期）
      await sleep(T.armHold)
      if (g !== gen) return

      for (let i = 0; i < DEMO_TERMS.length; i++) {
        const term = DEMO_TERMS[i]
        if (g !== gen) return
        clearRow() // 三条术语复用同一行 DOM：只做复位+重放，不条件渲染（结构一动 data-hd 缓存就失效）
        idxEl.textContent = i < 9 ? `0${i + 1}` : String(i + 1) // 序号固定两位，配 tabular-nums 三行左缘才齐
        labels.forEach((s, k) => s.classList.toggle('cur', k === i)) // 量尺标签：当前这条高亮，其余回到未处理态
        // 进度先行（走到本条的 60% 处）：字还没判完，尺子先跟上，视觉才像"正在处理这一项"
        const to = `${((i + 0.6) / DEMO_TERMS.length) * 100}%`
        gfill.style.width = to
        bead.style.insetInlineStart = to
        rowEl.classList.add('in') // 行入场与序号 pop 同帧，读作"这一项开始"
        replay(idxEl, 'pop')
        await sleep(T.enter)
        if (g !== gen) return

        cnEl.classList.add('in') // 中文术语先位移到位、紧接着逐字打出：位移与打字分两拍才有层次
        await type(cnEl, term.cn, SRC_LANG, MUL.cn, g)
        if (g !== gen) return
        await sleep(T.cnHold)
        if (g !== gen) return

        arrowEl.classList.add('in') // 箭头只做一次横向推进，暗示"从这里开始换成外语"
        await sleep(T.arrowIn + T.arrowHold)
        if (g !== gen) return

        await type(wtEl, term.w, TR_LANG, MUL.w, g)
        if (g !== gen) return
        await sleep(T.wordHold) // 停顿给"这词看着没问题"的错觉，红线才有判错感
        if (g !== gen) return

        wEl.classList.add('struck') // 判错：红线 scaleX 展开 + 一次红色 text-shadow 脉冲
        await sleep(T.strike)
        if (g !== gen) return

        rEl.classList.add('glow') // 正解先带辉光再打字：把视线钉在字号最大的那一栏
        await type(rEl, term.r, TR_LANG, MUL.r, g)
        if (g !== gen) return

        // 判完一处的同步反馈：正解定版缩放 + 整行提亮 + 尺珠脉冲 + 标签转「已过」
        replay(rEl, 'lock')
        replay(rowEl, 'lit')
        replay(bead, 'snap')
        labels[i].classList.remove('cur') // 只动当前这一个标签：整组 forEach 会让"已过"提前蔓延
        labels[i].classList.add('done')
        replay(panel, 'drop') // 整卡轻微下沉一帧：给「这一条判定完成」一个可感的落点
        const landed = `${((i + 1) / DEMO_TERMS.length) * 100}%` // 本条落定：尺子补齐到整格
        gfill.style.width = landed
        bead.style.insetInlineStart = landed

        await sleep(T.lock)
        if (g !== gen) return
        rEl.classList.remove('lock') // lock 与 glow 分两拍退：同帧摘掉会让正解从最亮直接掉回基线，看着像闪烁
        await sleep(T.glowHold)
        if (g !== gen) return
        rEl.classList.remove('glow')

        await sleep(T.hold)
        if (g !== gen) return

        // 最后一条走完先整卡压暗：ceremony 的爆发需要前置落差，一路亮着就没有峰值（动效原则 3）
        if (i === DEMO_TERMS.length - 1) panel.classList.add('dim')
        rowEl.classList.remove('in')
        rowEl.classList.add('out') // out 的退场曲线自带 .26s 延迟：整行错峰上移淡出，比 in 更慢
        await sleep(T.exit + T.gap)
        if (g !== gen) return
      }

      await sleep(T.breath) // 爆发前的一拍吸气：dim 之后不立刻点亮，留出让眼睛归位的时间
      if (g !== gen) return
      ceremony(g) // 峰值编排交给独立函数：play() 只排期，不掺动画细节
      await sleep(T.peak)
      if (g !== gen) return

      await type(finalEl, DEMO_FINAL, TR_LANG, MUL.final, g) // 定稿比术语快一截（final 倍率 16/38）：过程快、结论读得清
      if (g !== gen) return

      // 定稿盖章：审校光扫过 → 结果框描边闪一下 → 文本点亮一次 → 面板爆发收势
      wrapEl.classList.add('proof')
      resultEl.classList.add('stamp')
      finalEl.classList.add('done')
      panel.classList.add('blaze', 'punch') // blaze 给最亮的一帧边框、punch 给闪白+冲击位移，缺一半就不像"落章"
      markEl.classList.remove('glow')
      void markEl.offsetWidth // 对勾的辉光是同一动画名，必须先摘后加重排才会二次触发
      markEl.classList.add('glow')
      await sleep(T.tailLead)
      if (g !== gen) return

      metaEl.classList.add('lit') // 统计副标放在盖章之后再亮：先给结论再给数字，顺序反了会抢峰值
      await sleep(T.proof + T.resultHold) // 结果至少停 2.4s：读完一整句英文译文的下限
      if (g !== gen) return
      await sleep(T.tailHold)
      if (g !== gen) return
      play() // 自循环：结果停留够久（观众读完）后回到空白重放，而不是挂一个 setInterval
    }
    play()
    return () => { gen++ } // 卸载只 ++ 代际：悬空 await 与 ceremony 里未触发的 setTimeout 会自行熄火
    // 依赖 statusStr：切语言后备案文本长度变了，整段节拍时长必须按新语种重排，只能重挂一次 effect
  }, [statusStr, lang])

  const brand = branding.brandName || t('land.brand') // 页眉品牌与整页同源：租户改名后演示卡不留旧产品名
  return (
    <div className="hd" ref={demoRef}>
      {/* 演示卡整棵树是静态结构：动画只切类名与 textContent，绝不条件渲染节点，
          否则 effect 开头缓存的 23 个 data-hd 句柄会集体失效 */}
      <div className="hd-panel" data-hd="panel">
        <i className="hd-barfill" data-hd="barfill" /> {/* 循环进度条：时长取 JS 写入的 --dur */}
        <div className="hd-bar">
          {/* 仿编辑器标题栏：三点 + 品牌 + 语种标签，纯装饰，不带任何跳转 */}
          <span className="hd-dots"><i /><i /><i /></span>
          <span className="hd-name">
            <b>{brand}</b>
            <em>{t('land.demoSub')}</em> {/* 「· 术语择优引擎」：定位短语，≤620px 隐藏让位给语种标签 */}
          </span>
          <span className="hd-tag">{t('land.demoTag')}</span> {/* 方向随演示源语种：英语 UI 以英文为源故标「EN → ZH」，其余按 UI 语种带码；行业词与 DEMO_SRC 对齐 */}
        </div>
        <div className="hd-stream">
          <div className="hd-src">
            <span className="hd-srctag">{t('land.demoSrcTag')}</span>
            <div className="hd-srcwrap">
              {/* ghost 只用来撑高度，真身绝对定位覆盖其上：打字过程中行框不塌不跳（文末另有说明） */}
              <div className="hd-srctext ghost" aria-hidden="true">{DEMO_SRC}</div>
              <div className="hd-srctext" data-hd="src" />
            </div>
          </div>
          <div className="hd-status" data-hd="statusline">
            {/* 状态行：脉冲点常驻 + 文案逐字打出，两者合成"系统正在干活"的观感 */}
            <i className="hd-pulse" />
            <span data-hd="status" />
          </div>
          <div className="hd-steps" data-hd="steps">
            <div className="hd-slabels">
              {/* 三个量尺标签直接由 DEMO_TERMS 生成：与术语行同源，增删一条时标签/行/节拍时长三处一起跟随 */}
              {DEMO_TERMS.map((tm, i) => (
                <span className="hd-sl" key={tm.w}>
                  <i>{i < 9 ? `0${i + 1}` : i + 1}</i>
                  {tm.cn}
                </span>
              ))}
            </div>
            <div className="hd-gauge">
              {/* 量尺三段：轨道（.armed 时从左往右展开）+ 已填充段 + 尺珠，后两者的百分比由 JS 直写 style */}
              <i className="hd-gtrack" />
              <i className="hd-gfill" data-hd="gfill" />
              <i className="hd-gbead" data-hd="bead" />
            </div>
          </div>
          <div className="hd-track" data-hd="track">
            {/* hd-track 至少 128px 高：术语行是绝对定位的覆盖层（不占流），结果框才不会被三条轮播顶得上跳下窜 */}
            <div className="hd-row" data-hd="row">
              <span className="hd-idx" data-hd="idx">01</span> {/* 写死的 01 只是首帧占位，play() 每条都会覆写 */}
              <span className="hd-cn" data-hd="cn" />
              <span className="hd-arrow" data-hd="arrow">
                {/* 箭头单独画 SVG（非图标集）：24×13 的细长双段线在图标集里没有对应尺寸 */}
                <svg width="24" height="13" viewBox="0 0 24 13" fill="none" aria-hidden="true">
                  <path d="M1 6.5H21M21 6.5L15.7 2M21 6.5L15.7 11" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              </span>
              <span className="hd-swap">
                {/* 初译与正译同基线并排：正译字号更大（30~50 vs 21~38），"判对"的分量靠字号压过"判错" */}
                <span className="hd-w" data-hd="w"><span className="hd-wt" data-hd="wt" /></span>
                <span className="hd-r" data-hd="r" />
              </span>
            </div>
            <div className="hd-flash" data-hd="flash" /> {/* 爆发光斑：贴在轨道中线，一次性 burst */}
            <div className="hd-rule" data-hd="rule" /> {/* 下笔线：ceremony 起手写出，给峰值一个"落笔"动作 */}
            <div className="hd-result" data-hd="result">
              <div className="hd-rhead">
                {/* 结果框头：完成图标 + 结论 + 统计；右侧"复制"写入定稿译文（真实按钮，带已复制反馈） */}
                <span className="hd-rleft">
                  <span className="hd-rcheck">
                    <span className="hd-ring" data-hd="ring" />
                    <svg className="hd-mark" data-hd="mark" width="15" height="15" viewBox="0 0 15 15" fill="none" aria-hidden="true">
                      {/* 这段 path 实际长约 14.6px，CSS 里 stroke-dasharray/offset 取 15（略放大保证起笔完全藏住），
                          draw 才是"一笔写出"而不是淡入 */}
                      <path d="M2.6 7.9L6 11.3L12.4 3.9" stroke="#FFFFFF" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
                    </svg>
                  </span>
                  <span className="hd-rlabel" data-hd="rlabel">{t('land.demoDone')}</span>
                  <span className="hd-rsub" data-hd="meta">{t('land.demoMeta')}</span> {/* 统计口径与 DEMO_TERMS 条数一致（3 / 3） */}
                </span>
                <button type="button" className="hd-dlbtn" onClick={copyFinal}>
                  {copied ? <CheckIcon size={16} /> : <ClipboardIcon size={16} />}
                  <span>{copied ? t('land.demoCopied') : t('land.demoCopy')}</span>
                </button>
              </div>
              <div className="hd-fwrap" data-hd="fwrap">
                {/* 定稿译文：同样是 ghost 占位 + 真身覆盖，外层 ::after 负责审校扫光 */}
                <div className="hd-final ghost" aria-hidden="true">{DEMO_FINAL}</div>
                <div className="hd-final" data-hd="final" />
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

/* 翻译文件直出循环动效（★ 2026-09-23）：上传 → 翻译 → 回写 → 下载，四阶段自循环。
   复用 HeroDemo 范式：fdRef 根 + data-fd 选择器取节点、gen 代际计数防竞态、顶部进度条 --dur、
   reduced-motion 静态终态、自循环 play() 不挂 setInterval。纯黑单色：文件名示例走
   fdSampleSrc/fdSampleOut，标签走 fdSrcLabel/fdOutLabel/fdStep1-4，全程无裸中文。 */
function FileDirectDemo() {
  const [lang, t] = useT()
  const SRC_NAME = t('land.fdSampleSrc') // 源文件名（用户语种）：报价单
  const OUT_NAME = t('land.fdSampleOut') // 译文文件名（canonical）：quotation
  const fdRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const root = fdRef.current
    if (!root) return
    const el = (n: string) => root.querySelector<HTMLElement>(`[data-fd="${n}"]`)
    const barfill = el('barfill')!
    const srcCard = el('src')!
    const outCard = el('out')!
    const arrow = el('arrow')!
    const stemEl = el('stem')! // 译文卡文件名词干：翻译阶段改写
    const progEl = el('prog')! // 译文卡内进度条
    const check = el('check')! // 译文卡对勾（回写落章）
    const dl = el('dl')! // 译文卡下载按钮（下载脉冲）
    const steps = Array.from(root.querySelectorAll<HTMLElement>('.fd-step'))

    let gen = 0
    const sleep = (ms: number) => new Promise<void>((res) => window.setTimeout(res, ms))
    const setStep = (i: number, g: number) => {
      if (g !== gen) return
      steps.forEach((s, k) => {
        s.classList.toggle('on', k === i)
        s.classList.toggle('done', k < i)
      })
    }
    const reset = () => {
      srcCard.classList.remove('upload')
      outCard.classList.remove('live')
      arrow.classList.remove('live')
      stemEl.textContent = ''
      stemEl.classList.remove('show')
      progEl.classList.remove('run')
      progEl.style.width = '0%'
      check.classList.remove('show')
      dl.classList.remove('show')
      barfill.classList.remove('run')
      steps.forEach((s) => s.classList.remove('on', 'done'))
      void srcCard.offsetWidth // 落定摘类这一帧，新一轮动画才有起跳点
    }
    // 终态快照：供「偏好减少动效」使用，与演出结果一致（源卡常驻、译文卡已译好可下载）
    const staticState = () => {
      outCard.classList.add('live')
      arrow.classList.add('live')
      stemEl.textContent = OUT_NAME
      stemEl.classList.add('show')
      progEl.style.width = '100%'
      check.classList.add('show')
      dl.classList.add('show')
      steps.forEach((s) => s.classList.add('done'))
    }
    // 减少动效：整条计时链不启动，直接呈现终态（trunc 类动画由文末 reduced-motion 规则统一掐掉）
    if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
      staticState()
      return () => { gen++ }
    }

    // 四阶段时长（ms）：与下方 play() 的 await 顺序逐拍对齐，改动画顺序须同步改
    const T = {
      lead: 240,
      upload: 980, // 源卡落入 + 上传扫描线
      gap1: 280,
      translate: 1500, // 箭头亮 + 进度条跑 + 文件名改写
      gap2: 260,
      writeback: 900, // 对勾落章
      gap3: 240,
      download: 1500, // 下载按钮脉冲
      tail: 1200, // 结果停留后重来
    }
    const totalMs =
      T.lead + T.upload + T.gap1 + T.translate + T.gap2 + T.writeback + T.gap3 + T.download + T.tail
    root.style.setProperty('--dur', `${totalMs}ms`) // 只交给 .lc-fd-barfill.run 用

    async function play() {
      const g = ++gen
      reset()
      await sleep(T.lead)
      if (g !== gen) return
      barfill.classList.add('run')

      // 阶段 1：上传
      setStep(0, g)
      srcCard.classList.add('upload')
      await sleep(T.upload)
      if (g !== gen) return

      // 阶段 2：翻译（箭头亮 + 进度条 + 文件名改写）
      setStep(1, g)
      arrow.classList.add('live')
      outCard.classList.add('live')
      progEl.classList.add('run')
      stemEl.classList.remove('show')
      void stemEl.offsetWidth // 摘加同类同帧无效，强制重排让旧名先淡出
      stemEl.textContent = OUT_NAME
      stemEl.classList.add('show')
      await sleep(T.translate)
      if (g !== gen) return

      // 阶段 3：回写（对勾落章）
      setStep(2, g)
      check.classList.add('show')
      await sleep(T.writeback)
      if (g !== gen) return

      // 阶段 4：下载（按钮脉冲）
      setStep(3, g)
      dl.classList.add('show')
      await sleep(T.download)
      if (g !== gen) return

      await sleep(T.tail)
      if (g !== gen) return
      play() // 自循环：结果停留够久后回到空白重放，不挂 setInterval
    }
    play()
    return () => { gen++ } // 卸载只 ++ 代际：悬空 await 自行熄火
  }, [lang])

  const steps = [t('land.fdStep1'), t('land.fdStep2'), t('land.fdStep3'), t('land.fdStep4')]
  return (
    <div className="lc-fd-visual" ref={fdRef} aria-hidden="true">
      <i className="lc-fd-barfill" data-fd="barfill" />
      <div className="lc-fd-row">
        <div className="lc-fd-card lc-fd-src" data-fd="src">
          <span className="lc-fd-tag">{t('land.fdSrcLabel')}</span>
          <svg className="lc-fd-ico" width="38" height="38" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M6 2.5h8L19 7v14.5a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1v-18a1 1 0 0 1 1-1Z" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
            <path d="M14 2.5V7h5" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
          </svg>
          <span className="lc-fd-name">{SRC_NAME}<span className="lc-fd-ext">.pdf</span></span>
          <span className="lc-fd-fmt">PDF</span>
          <i className="lc-fd-up" />
        </div>
        <span className="lc-fd-arrow" data-fd="arrow"><ArrowRightIcon size={22} /></span>
        <div className="lc-fd-card lc-fd-out" data-fd="out">
          <span className="lc-fd-tag">{t('land.fdOutLabel')}</span>
          <svg className="lc-fd-ico" width="38" height="38" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M6 2.5h8L19 7v14.5a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1v-18a1 1 0 0 1 1-1Z" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
            <path d="M14 2.5V7h5" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
          </svg>
          <span className="lc-fd-name"><span className="lc-fd-stem" data-fd="stem" /><span className="lc-fd-ext">.pdf</span></span>
          <span className="lc-fd-prog"><i data-fd="prog" /></span>
          <span className="lc-fd-fmt">PDF</span>
          <span className="lc-fd-check" data-fd="check"><CheckCircleIcon size={18} /></span>
          <button type="button" className="lc-fd-dl" data-fd="dl">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" aria-hidden="true">
              <path d="M12 3.5v11M7.5 10.5L12 15l4.5-4.5M5 20h14" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            <span>{t('land.fdStep4')}</span>
          </button>
        </div>
      </div>
      <div className="lc-fd-steps">
        {steps.map((s, i) => (
          <span className="fd-step" key={i}>
            <i className="fd-dot" />
            <em>{s}</em>
          </span>
        ))}
      </div>
    </div>
  )
}

/** 开发者示例代码块：复制按钮写真实 curl（与演示卡同一套剪贴板降级口径，1.6s 回弹） */
function DevSample() {
  const [lang, t] = useT()
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard?.writeText(devSampleCmd(demoSrc(demoSrcLang(lang)))).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    }).catch(() => { /* 非安全上下文剪贴板不可用：静默失败，代码块仍可读可手动选中复制 */ })
  }
  return (
    <div className="lc-code lc-reveal">
      <div className="lc-code-bar">
        {/* 端点标签写死在代码块上：它和 devSampleCmd 是同一份契约的两半，改路径必须两处同改 */}
        <span>POST /openapi/v1/translate</span>
        <button type="button" className="lc-code-copy" onClick={copy}>
          {copied ? <CheckIcon size={14} /> : <ClipboardIcon size={14} />}
          <span>{copied ? t('land.devCopied') : t('land.devCopy')}</span>
        </button>
      </div>
      {/* pre 保留反斜杠续行：white-space:pre + overflow-x，窄屏横向滚动而不打散命令行 */}
      <pre className="lc-code-body">{devSampleCmd(demoSrc(demoSrcLang(lang)))}</pre>
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
  const DEMO_TERMS = demoTerms(demoSrcLang(lang)) // 术语大卡对照行复用演示术语（与 HeroDemo 同源，英语 UI 取中文 cn）
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
            <rect width="30" height="30" rx="8" fill="#FFFFFF" />
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

      {/* 2. Hero：左文案（徽章/双行标题/副题/双 CTA/信任指标）+ 右三检查点翻译流演示卡（★ 原样恢复） */}
      {/* 左栏定宽 500px、右栏 flex:1（尺寸都在 CSS .lc-hero-copy/.lc-hero-demo）：
          文案要窄到一行十几字好读，演示卡要 720px 档才放得下整段翻译流；<1200px 折成上下两段 */}
      <section className="lc-hero" ref={heroRef}>
        <div className="lc-hero-in">
          <div className="lc-hero-copy">
            <span className="lc-hero-badge lc-mo-up"><i />{t('land.heroBadge')}</span> {/* <i> 是徽章前的小圆点，纯装饰 */}
            <h1 className="lc-hero-h1">
              {/* 一个 h1 拆两个 block span：只为画布那处两行断句，语义上仍是一条标题，朗读顺序不断 */}
              <span className="lc-mo-up lc-mo-d1">{t('land.heroTitle1')}</span>
              <span className="lc-mo-up lc-mo-d2">{t('land.heroTitle2')}</span>
            </h1>
            {/* ★ 2026-09-23：第二核心卖点带（与「跨行业精确翻译」并列）。主句强调「原文件直出+官方文件分钟级」，
                小字提醒「订单/报价/确认等」作为官方文件的举例，不进主标题 */}
            <div className="lc-hero-point2 lc-mo-up lc-mo-d3">
              <i className="lc-hp-dot" />
              <span className="lc-hp-main">{t('land.heroPoint2')}</span>
              <span className="lc-hp-note">{t('land.heroPoint2Note')}</span>
            </div>
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
            {/* 演示卡用 d3 与副题同拍：再晚就成"二次加载"，更早则抢标题的读序 */}
            <HeroDemo />
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

      {/* 翻译文件直出（★ 2026-09-23）：核心功能之后、覆盖范围之前。左四阶段循环动效文件卡（上传→翻译→回写→下载）· 右文字卖点。
          纯黑单色；文件名示例走 fdSampleSrc/fdSampleOut，标签走 fdSrcLabel/fdOutLabel/fdStep1-4，全程无裸中文 */}
      <section id="filedirect" className="lc-sec lc-fd lc-reveal">
        <div className="lc-fd-in">
          <FileDirectDemo />
          <div className="lc-fd-copy">
            <h2 className="lc-fd-title">{t('land.heroPoint2')}</h2>
            <p className="lc-fd-body">{t('land.fdBody')}</p>
            <p className="lc-fd-note">{t('land.heroPoint2Note')}</p>
          </div>
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
              <div className="lc-plan-price">{p.price}</div> {/* ★ #39：卡面只写档位口径（¥0 起步 / 积分套餐 / 定制），不抄具体金额——价目事实源唯一在 /api/plans，由 /pricing 渲染；本条口径由 Landing.dom.test ⑨ 钉死 */}
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
                <rect width="30" height="30" rx="8" fill="#FFFFFF" />
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
.lc-nav-links{display:flex;align-items:center;gap:36px;font-size:17px;color:var(--lc-text-3)}
/* 只过渡 color：导航是定位工具，hover 时不许位移，否则整条栏像在被推动 */
.lc-nav-links a{transition:color var(--lc-mo-release) var(--lc-mo-out)}
.lc-nav-links a:hover{color:var(--lc-text)}
/* scrollspy 当前区块链接升到最强字色：与 hover 同色但常驻，靠"哪颗最亮"回答"我读到哪了" */
.lc-nav-links a.on{color:var(--lc-text)}
/* 右侧两块用 margin-left:auto 顶到最右：中间锚点靠自身 gap 排，不参与挤压 */
.lc-nav-cta{margin-left:auto;display:flex;align-items:center;gap:20px}
.lc-nav-login{font-size:17px;font-weight:500;transition:color var(--lc-mo-release) var(--lc-mo-out)}
.lc-nav-login:hover{color:var(--lc-text-2)}

/* —— 胶囊按钮 —— */
/* 尺寸不在基类里定，一律由 --sm/--md/--lg 三档给：同一颗按钮在导航、卡内、收尾必须同形不同档 */
.lc-mkt-btn{display:inline-flex;align-items:center;justify-content:center;border-radius:999px;font-weight:600;white-space:nowrap;transition:transform .18s var(--lc-mo-out),opacity .18s var(--lc-mo-out),background .28s ease,border-color .28s ease;cursor:pointer}
/* 按下只缩 3%：操作要被回应（动效原则），但按钮不是活物，不许"跳一下" */
.lc-mkt-btn:active{transform:scale(.97)}
/* 变体选择器带 .lc-mkt 前缀：压过 .lc-mkt a{color:inherit} 的 (0,1,1)，
   否则白底按钮上的白字 / 黑底按钮上的黑字会被 inherit 覆盖成隐形（实测踩坑） */
/* 主投=纯白底黑字：交付真值 .lc-btn--primary{background:#FFFFFF}（不是 #E7E9EA——
   那档灰只用于文字与活跃指示，拿来做整块填充会在纯黑底上读出冷调、显脏）。
   整页唯一一处实心白，强度最高，同屏通常只让它出现一次 */
.lc-mkt .lc-mkt-btn--pri{background:var(--lc-fill-white);color:#000}
.lc-mkt .lc-mkt-btn--pri:hover{opacity:.88}
/* 描边次投走令牌 --lc-border-pill（〇-O 起为纯白 #FFFFFF；旧档 #424956 作废）：2026-09-22 还原批把自造的 #546470 归位到令牌，
   〇-N 起该令牌为 2px；〇-O 起描边全部纯白，"可点"与"分隔"不再靠明暗分档，靠实心/透明底与 hover 区分 */
.lc-mkt .lc-mkt-btn--ghost{border:1.2px solid var(--lc-border-pill);color:var(--lc-text-1);background:transparent}
.lc-mkt .lc-mkt-btn--ghost:hover{border-color:var(--lc-border-done)}
/* 卡内浮面底：给非高亮价格档用，强度低于主投但仍是实心，不会和卡片背景糊在一起 */
.lc-mkt .lc-mkt-btn--soft{background:var(--lc-raised);color:var(--lc-text-1)}
.lc-mkt .lc-mkt-btn--soft:hover{opacity:.88}
/* 反相专用：白底卡与收尾白块上不能再放白按钮，改用实心黑保持"白-黑"配对 */
.lc-mkt .lc-mkt-btn--dark{background:#000;color:#fff}
.lc-mkt .lc-mkt-btn--dark:hover{opacity:.86}
/* 三档定高 42/50/56：只允许这三档，按钮高度一致才能与相邻文本块基线对齐 */
.lc-mkt-btn--sm{height:42px;padding:0 22px;font-size:17px}
.lc-mkt-btn--md{height:50px;padding:0 30px;font-size:18px}
.lc-mkt-btn--lg{height:56px;padding:0 36px;font-size:18px;font-weight:600}

/* —— 2. Hero —— */
/* min-height 734px 抄画布首屏高度："不到一屏"就不算首屏；窄屏该约束由响应式清掉 */
.lc-hero{display:flex;align-items:center;padding:56px 80px;min-height:734px}
/* 两栏用 flex 而非 grid：右栏要能吃掉左栏定宽之后的全部余量并居中放卡 */
.lc-hero-in{display:flex;align-items:center;gap:60px;width:100%}
/* 左栏 flex:none + 定宽 500：文案行长要锁死，宽屏也不许把句子拉散（超宽靠右栏吸收） */
.lc-hero-copy{flex:none;width:500px;display:flex;flex-direction:column;gap:24px}
/* 徽章底色走 --lc-raised（#16181C 徽章面令牌值）；描边原为演示画布字面 #31363D，
   〇-O 起收口到 --lc-border-pill（全站框线纯白），比卡片底抬一档靠面色台阶而非边框明度 */
.lc-hero-badge{display:inline-flex;align-items:center;gap:8px;align-self:flex-start;padding:8px 14px;border-radius:20px;background:var(--lc-raised);border:1.2px solid var(--lc-border-pill);font-size:16px;font-weight:500;color:var(--lc-text-3)}
.lc-hero-badge i{width:8px;height:8px;border-radius:4px;background:var(--lc-text-1);flex:none}
/* 3.89vw = 56px / 1440px 设计宽：clamp 的上界与画布字号一致，下界保证手机两行不断句 */
.lc-hero-h1{margin:0;font-size:clamp(34px,3.89vw,56px);line-height:1.21;font-weight:700;letter-spacing:.2px;color:var(--lc-text-1)}
/* 两行断句靠 block 而不是 <br>：DOM 里不留可被复制带走的换行符，也让两行各自能挂节拍类 */
.lc-hero-h1 span{display:block}
.lc-hero-sub{margin:0;font-size:18px;line-height:28px;color:var(--lc-text-3)}
/* ★ 2026-09-23：第二核心卖点带（与「跨行业精确翻译」标题并列）。外形复用徽章的浮面圆角+描边，
   但文字用 --lc-text-1（更亮）以显「卖点」分量；小字提醒走 --lc-text-3，靠左边框与主语分隔 */
.lc-hero-point2{display:inline-flex;align-items:center;gap:10px;flex-wrap:wrap;margin:18px 0 0;padding:10px 18px;border-radius:999px;background:var(--lc-raised);border:1.2px solid var(--lc-border-pill);font-size:15px;line-height:1.45;color:var(--lc-text-1)}
.lc-hero-point2 .lc-hp-dot{width:8px;height:8px;border-radius:50%;background:var(--lc-text-1);flex:none}
.lc-hero-point2 .lc-hp-main{font-weight:600;color:var(--lc-text-1)}
.lc-hero-point2 .lc-hp-note{font-size:12.5px;font-weight:400;color:var(--lc-text-3);padding-left:10px;margin-left:2px;border-left:1px solid var(--lc-border-pill)}
/* —— 翻译文件直出（四阶段循环动效：上传→翻译→回写→下载）：左文件卡动效 · 右文字，两栏图左文右 —— */
.lc-fd-in{display:grid;grid-template-columns:1.05fr .95fr;gap:48px;align-items:center;max-width:1080px;margin:0 auto}
.lc-fd-visual{position:relative;display:flex;flex-direction:column;align-items:center;gap:22px;padding-top:10px}
/* 顶部循环进度条：时长取 JS 写入的 --dur，与四阶段演出同帧起跑 */
.lc-fd-barfill{position:absolute;top:0;left:0;height:2px;width:100%;border-radius:2px;background:linear-gradient(90deg,transparent,var(--lc-text-1));transform-origin:left;transform:scaleX(0)}
.lc-fd-barfill.run{animation:fd-bar var(--dur) linear forwards}
@keyframes fd-bar{to{transform:scaleX(1)}}
.lc-fd-row{display:flex;align-items:center;justify-content:center;gap:14px}
.lc-fd-card{position:relative;display:flex;flex-direction:column;align-items:center;gap:10px;width:200px;padding:30px 16px 24px;border-radius:16px;background:var(--lc-raised);border:1.2px solid var(--lc-border-pill);transition:border-color .4s,box-shadow .4s,transform .4s,opacity .4s}
.lc-fd-tag{position:absolute;top:-11px;left:50%;transform:translateX(-50%);font-size:12px;line-height:1;color:var(--lc-text-3);background:var(--lc-bg);padding:4px 12px;border-radius:999px;border:1px solid var(--lc-border-pill);white-space:nowrap}
.lc-fd-ico{color:var(--lc-text-1)}
.lc-fd-name{font-size:18px;font-weight:600;color:var(--lc-text-1);text-align:center;word-break:break-word;min-height:22px;display:flex;align-items:baseline;justify-content:center;gap:1px}
.lc-fd-stem{display:inline-block;opacity:0;transform:translateY(5px);transition:opacity .28s ease,transform .28s ease}
.lc-fd-stem.show{opacity:1;transform:none}
.lc-fd-ext{color:var(--lc-text-3);font-weight:400}
/* 译文卡扩展名在翻译前隐藏：输出尚未生成，只露出空白待填 */
.lc-fd-out .lc-fd-ext{opacity:0;transition:opacity .3s}
.lc-fd-out.live .lc-fd-ext{opacity:1}
.lc-fd-fmt{font-size:11px;letter-spacing:.5px;color:var(--lc-text-3);border:1px solid var(--lc-border-pill);border-radius:6px;padding:2px 8px}
.lc-fd-arrow{color:var(--lc-text-3);flex:none;transition:color .34s,transform .34s}
.lc-fd-arrow.live{color:var(--lc-text-1);transform:translateX(3px)}
/* 源卡：上传阶段落入 + 顶部扫描线 */
.lc-fd-src.upload{animation:fd-drop .5s ease}
@keyframes fd-drop{from{opacity:0;transform:translateY(-14px)}to{opacity:1;transform:none}}
.lc-fd-up{position:absolute;left:8px;right:8px;top:6px;height:2px;border-radius:2px;background:var(--lc-text-1);opacity:0}
.lc-fd-src.upload .lc-fd-up{animation:fd-scan .9s ease}
@keyframes fd-scan{0%{opacity:0;transform:translateY(0)}22%{opacity:.85}100%{opacity:0;transform:translateY(112px)}}
/* 译文卡：翻译阶段亮边 + 进度条 */
.lc-fd-out{padding-bottom:46px}
.lc-fd-out.live{border-color:var(--lc-text-1);box-shadow:0 0 0 1px var(--lc-text-1)}
.lc-fd-prog{width:120px;height:4px;border-radius:999px;background:var(--lc-inset);overflow:hidden;opacity:0;transition:opacity .3s}
.lc-fd-out.live .lc-fd-prog{opacity:1}
.lc-fd-prog>i{display:block;height:100%;width:0;background:var(--lc-text-1);border-radius:999px}
.lc-fd-prog>i.run{animation:fd-prog 1.5s ease forwards}
@keyframes fd-prog{to{width:100%}}
/* 回写：对勾落章 */
.lc-fd-check{position:absolute;top:12px;right:12px;color:var(--lc-text-1);opacity:0;transform:scale(.6);transition:opacity .3s,transform .3s}
.lc-fd-check.show{opacity:1;transform:scale(1);animation:fd-stamp .4s ease}
@keyframes fd-stamp{0%{transform:scale(.5)}60%{transform:scale(1.18)}100%{transform:scale(1)}}
/* 下载：按钮脉冲 */
.lc-fd-dl{position:absolute;bottom:10px;left:50%;transform:translate(-50%,8px);display:inline-flex;align-items:center;gap:6px;padding:7px 16px;border-radius:999px;background:var(--lc-text-1);color:var(--lc-bg);border:none;font-size:13px;font-weight:600;cursor:default;opacity:0;pointer-events:none;transition:opacity .3s,transform .3s}
.lc-fd-dl.show{opacity:1;transform:translate(-50%,0);animation:fd-pulse 1.4s ease-in-out infinite}
@keyframes fd-pulse{0%,100%{box-shadow:0 0 0 0 rgba(231,233,234,0)}50%{box-shadow:0 0 0 6px rgba(231,233,234,.12)}}
/* 阶段指示条：on=进行中（亮）/ done=已完成（实心点） */
.lc-fd-steps{display:flex;align-items:center;gap:10px;flex-wrap:wrap;justify-content:center}
.fd-step{display:inline-flex;align-items:center;gap:7px;font-size:13px;color:var(--lc-text-3);transition:color .3s}
.fd-step .fd-dot{width:8px;height:8px;border-radius:50%;border:1px solid var(--lc-border-pill);background:transparent;transition:background .3s,border-color .3s}
.fd-step.on{color:var(--lc-text-1)}
.fd-step.on .fd-dot{background:var(--lc-text-1);border-color:var(--lc-text-1)}
.fd-step.done{color:var(--lc-text-2)}
.fd-step.done .fd-dot{background:var(--lc-text-1);border-color:var(--lc-text-1)}
.lc-fd-copy{max-width:460px}
.lc-fd-title{margin:0 0 16px;font-size:clamp(26px,3vw,38px);line-height:1.25;font-weight:700;color:var(--lc-text-1)}
.lc-fd-body{margin:0;font-size:17px;line-height:28px;color:var(--lc-text-3)}
.lc-fd-note{margin:14px 0 0;font-size:13px;color:var(--lc-text-3);opacity:.85}
@media (max-width:980px){.lc-fd-in{grid-template-columns:1fr;gap:32px}.lc-fd-visual{order:-1}}
/* reduced-motion：掐掉所有一次性动画，静态终态由 JS staticState() 给出 */
@media (prefers-reduced-motion:reduce){
  .lc-fd-barfill.run,.lc-fd-src.upload,.lc-fd-prog>i.run,.lc-fd-check.show,.lc-fd-dl.show{animation:none!important}
  .lc-fd-stem{transition:none}
}
.lc-hero-ctas{display:flex;align-items:center;gap:16px}
/* 信任指标 26px 间距：三条要读成"并列事实"，间距小于卡内 gap 就会粘成一段 */
.lc-hero-trust{display:flex;align-items:center;gap:26px;font-size:16px;color:var(--lc-text-3)}
.lc-hero-trust span{display:inline-flex;align-items:center;gap:8px}
.lc-hero-trust svg{color:var(--lc-text-2)}
/* min-width:0 是 flex 老坑：不写它，演示卡里的长英文会把右栏顶到溢出、整页出现横向滚动 */
.lc-hero-demo{flex:1;display:flex;justify-content:center;min-width:0}

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
.lc-sec-label{margin:0;font-size:16px;font-weight:600;color:var(--lc-text-3)}
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
.lc-step-num{font-family:var(--lc-font-mono);font-size:16px;font-weight:500;color:var(--lc-text-4)}
.lc-step-t{margin:0;font-size:20px;font-weight:600;color:var(--lc-text-1)}
.lc-step-d{margin:0;font-size:17px;line-height:24px;color:var(--lc-text-3)}
/* 箭头单独走 --lc-text-3（★ 2026-09-22 还原：自造的 #6A717A 归位令牌）：
   它是流程记号，够看见但不与正文抢对比 */
.lc-step-arrow{flex:none;display:flex;align-items:center;color:var(--lc-text-3)}

/* —— 4. 核心功能 bento —— */
/* 776fr/480fr 直接抄画布两列宽度：写 fr 不写 px，才能在区块内边距收缩时按比例跟着缩 */
.lc-bento-top{display:grid;grid-template-columns:776fr 480fr;gap:24px}
/* 四张小卡与顶栏分属两条 grid：结构上等价于"两行"，但折行只需各改一行 columns */
.lc-bento-row{display:grid;grid-template-columns:repeat(4,1fr);gap:24px;margin-top:24px}
/* 卡底仍用 --lc-bg 而不是抬升色：营销页的高低由「2px 纯白框（〇-O）」表达，
   三级面色台阶只用于产品界面（工作台/后台）的区块分层，营销页不跟着堆灰底 */
.lc-fcard{display:flex;flex-direction:column;gap:14px;padding:28px;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:16px;min-width:0}
/* 大卡多 4px 内边距：它要装三条演示行，密排会读成表格而不是产品截图 */
.lc-fcard--big{padding:32px;gap:16px}
/* hover 只提描边亮度，不位移不投影：功能卡不是按钮，不该给"可点"的暗示 */
.lc-fcard:hover{border-color:var(--lc-border-pill)}
.lc-ficon{display:inline-flex;align-items:center;justify-content:center;width:40px;height:40px;border-radius:10px;background:var(--lc-raised);color:var(--lc-text-1);flex:none}
.lc-fcard-t{margin:0;font-size:18px;font-weight:600;color:var(--lc-text-1)}
.lc-fcard-d{margin:0;font-size:17px;line-height:24px;color:var(--lc-text-3)}
/* margin-top:auto 把演示区压到大卡底部：卡高由 grid 拉齐，演示贴底才对得上画布构图 */
.lc-termdemo{display:flex;flex-direction:column;gap:8px;margin-top:auto;padding-top:8px}
.lc-td-note{margin:0;font-size:14px;color:var(--lc-text-4)}
/* 演示行用最深底色 --lc-deep：整卡里唯一一处"嵌进去"的容器，读起来才像界面截图 */
.lc-td-row{display:flex;align-items:center;gap:12px;padding:12px 16px;background:var(--lc-deep);border:1.2px solid var(--lc-border-faint);border-radius:10px}
.lc-td-cn{flex:1;font-size:16px;font-weight:500;color:var(--lc-text-1);min-width:0}
/* bad/good 同用等宽字体、只差色阶：让"错"与"对"是同一个位置的两种状态，而不是两种东西 */
.lc-td-bad{font-family:var(--lc-font-mono);font-size:15px;color:var(--lc-text-4)}
.lc-td-arrow{color:var(--lc-text-3);flex:none}
.lc-td-good{font-family:var(--lc-font-mono);font-size:15px;font-weight:500;color:var(--lc-text-1)}

/* —— 4b. 覆盖范围 —— */
/* 统计带四等分：数字是这一区块的主角，等宽排一排才读成"面板读数"而不是四段散文 */
.lc-cov-stats{display:grid;grid-template-columns:repeat(4,1fr);gap:24px;margin-bottom:44px}
.lc-cov-stat{display:flex;flex-direction:column;gap:8px;padding:26px 28px;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:16px;min-width:0}
.lc-cov-stat b{font-family:var(--lc-font-mono);font-size:clamp(28px,2.4vw,34px);font-weight:700;line-height:1.1;color:var(--lc-text-1)}
.lc-cov-stat span{font-size:15px;line-height:20px;color:var(--lc-text-3)}
.lc-cov-groups{display:flex;flex-direction:column;gap:30px}
.lc-cov-group{display:flex;flex-direction:column;gap:10px}
.lc-cov-t{margin:0;font-size:18px;font-weight:600;color:var(--lc-text-1)}
.lc-cov-d{margin:0;font-size:16px;line-height:22px;color:var(--lc-text-3);max-width:760px}
/* chips 用描边胶囊不用色块：本页的"标签"语法只有一种（同 Hero 语种标签），不为一排格式名新造视觉元素 */
.lc-chips{display:flex;flex-wrap:wrap;gap:10px;margin-top:4px}
.lc-chip{padding:7px 14px;border:1.2px solid var(--lc-border-card);border-radius:999px;font-size:15px;color:var(--lc-text-2);background:var(--lc-bg);white-space:nowrap}
.lc-chip--mono{font-family:var(--lc-font-mono);letter-spacing:.02em;font-size:14px}

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
.lc-qa-d{margin:0;font-size:17px;line-height:24px;color:var(--lc-text-3)}
/* 收口句用强描边框：全区块唯一的"结论容器"，靠边框强度而非底色区分（本页无彩底） */
.lc-qa-final{margin:44px 0 0;padding:26px 30px;border:1.2px solid var(--lc-border-strong);border-radius:16px;font-size:17px;line-height:28px;font-weight:600;color:var(--lc-text-1)}

/* —— 4d. 开发者集成 —— */
/* 网格参数照抄 FAQ（360px + 1fr / gap 80）：又一个"左目录右正文"区块，不另造版式 */
.lc-dev{display:grid;grid-template-columns:360px 1fr;gap:80px;align-items:start}
.lc-dev-head{display:flex;flex-direction:column;gap:14px;align-items:flex-start}
.lc-dev-sub{margin:0;font-size:17px;line-height:24px;color:var(--lc-text-3)}
/* 文档出口是文字链+图标不是大按钮：这一区块的动作密度本来就低，别和收尾 CTA 抢主投 */
.lc-dev-doc{display:inline-flex;align-items:center;gap:8px;font-size:16px;font-weight:600;color:var(--lc-text-1);transition:opacity var(--lc-mo-release) var(--lc-mo-out)}
.lc-dev-doc:hover{opacity:.75}
.lc-dev-doc svg{flex:none}
.lc-dev-body{display:flex;flex-direction:column;gap:24px;min-width:0}
.lc-dev-feats{display:grid;grid-template-columns:repeat(3,1fr);gap:24px}
.lc-dev-feat{display:flex;flex-direction:column;gap:8px;padding:24px;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:16px;min-width:0}
.lc-dev-t{margin:0;font-size:18px;font-weight:600;color:var(--lc-text-1)}
.lc-dev-d{margin:0;font-size:15.5px;line-height:22px;color:var(--lc-text-3)}
/* 代码块底色用 --lc-deep（与大卡演示行同语法）：页内"嵌进去的界面片段"共用一种深度 */
.lc-code{border:1.2px solid var(--lc-border-card);border-radius:16px;overflow:hidden;background:var(--lc-deep)}
.lc-code-bar{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:12px 18px;border-bottom:1px solid var(--lc-border-faint);font-family:var(--lc-font-mono);font-size:14px;letter-spacing:.02em;color:var(--lc-text-3)}
/* 复制按钮与小号胶囊按钮同形（28 高/8 圆角）：按钮语汇总只有一档尺寸，不新开 */
.lc-code-copy{display:inline-flex;align-items:center;gap:6px;height:28px;padding:0 12px;border:1.2px solid var(--lc-border-pill);border-radius:8px;background:none;color:#C8CCD1;font:500 14px/1 var(--lc-font);cursor:pointer;transition:border-color var(--lc-mo-release) var(--lc-mo-out),color var(--lc-mo-release) var(--lc-mo-out)}
.lc-code-copy:hover{border-color:var(--lc-border-done);color:var(--lc-text-1)}
.lc-code-copy svg{display:block}
/* white-space:pre：curl 的反斜杠续行是内容的一部分，折行会把它变成一条读不懂的长句；窄屏靠横向滚动 */
.lc-code-body{margin:0;padding:18px 20px;overflow-x:auto;font-family:var(--lc-font-mono);font-size:15px;line-height:22px;color:var(--lc-text-2);white-space:pre}

/* —— 5. 价格方案 —— */
/* 三档等宽：价格要能横向对读，一旦不等宽就变成"各说各话"，比较关系直接消失 */
.lc-plans{display:grid;grid-template-columns:repeat(3,1fr);gap:24px}
/* align-items:flex-start：卡内元素顶对齐，价格数字长短不同也不会把下面的按钮错开 */
.lc-plan{display:flex;flex-direction:column;align-items:flex-start;gap:20px;padding:32px;background:var(--lc-bg);border:1.2px solid var(--lc-border-card);border-radius:16px}
.lc-plan:hover{border-color:var(--lc-border-pill)}
/* 反相档底色与描边同为纯白：白卡上再画一圈浅边只会显脏，高亮靠"整块变白"完成 */
.lc-plan--pro{background:var(--lc-fill-white);border-color:var(--lc-fill-white);color:#000}
/* 徽标在白色卡上用实心黑：与它所在的反相卡共用同一套黑白语言，不引入第三种强调色 */
.lc-plan-badge{padding:6px 14px;border-radius:999px;background:#000;color:#fff;font-size:15px;font-weight:600}
.lc-plan-name{margin:0;font-size:20px;font-weight:600}
/* 价位是卡内唯一的大字号（36px）：读价格的人只看这一行，其余都要给它让位 */
.lc-plan-price{font-size:36px;font-weight:700;line-height:1.15}
.lc-plan-desc{margin:0;font-size:17px;color:var(--lc-text-3)}
/* 反相卡的次级文字必须走 on-light 档：黑底体系里的浅灰放到白底上直接不可读（2026-09-19 对比度整改） */
.lc-plan--pro .lc-plan-desc{color:var(--lc-text-on-light)}
/* 列表上边距只留 8px：按钮与功能点是一组，间距拉到 gap 会被读成两段内容 */
.lc-plan-feats{list-style:none;margin:8px 0 0;padding:0;display:flex;flex-direction:column;gap:12px}
.lc-plan-feats li{display:flex;align-items:center;gap:10px;font-size:16px;color:var(--lc-text-3)}
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
.lc-rw-d{margin:0;font-size:17px;line-height:24px;color:var(--lc-text-3)}
/* baseline 对齐：数字与单位字号差一倍，用 center 会让单位像挂在数字腰上 */
.lc-rw-num{margin:0;display:flex;align-items:baseline;gap:8px}
/* 数字是全卡最大字号且用等宽：先看到量、再看到口径，中英切换时宽度还不抖 */
.lc-rw-num b{font-family:var(--lc-font-mono);font-size:32px;font-weight:700;color:var(--lc-text-1)}
.lc-rw-num span{font-size:16px;color:var(--lc-text-3)}

/* —— 7. FAQ —— */
/* 左栏定宽 360 而非 1fr：标题栏要像"目录"，右侧问答才是被读的主体；80px 间距与区块共用同口径 */
.lc-faq{display:grid;grid-template-columns:360px 1fr;gap:80px}
.lc-faq-head{display:flex;flex-direction:column;gap:14px}
.lc-faq-sub{margin:0;font-size:17px;line-height:24px;color:var(--lc-text-3)}
/* 分隔线由每条自己的 border-top 承担：不另放 <hr>，条距天然等宽且末条不会多出尾线 */
.lc-faq-item{border-top:1px solid var(--lc-border-faint);padding:20px 0;display:flex;flex-direction:column;gap:10px}
/* 问题用 h3 但字号只到 18：它比区块标题小，语义上又要在同一条朗读层级里 */
.lc-faq-q{margin:0;font-size:18px;font-weight:600;color:var(--lc-text-1)}
.lc-faq-a{margin:0;font-size:17px;line-height:24px;color:var(--lc-text-3)}

/* —— 6b. 更新日志 —— */
/* 单列限宽 820：这是"台账"不是正文流，行长失控会让日期列与内容读成两栏报纸 */
.lc-cl-list{display:flex;flex-direction:column;max-width:820px}
/* 分隔线口径与 FAQ 条目完全一致：同一页里"逐条可读"的内容用同一种线 */
.lc-cl-item{display:grid;grid-template-columns:112px 1fr;gap:24px;padding:20px 0;border-top:1px solid var(--lc-border-faint)}
/* 日期等宽字体：五条日期数位天然对齐，左侧收成一根竖线 */
.lc-cl-date{font-family:var(--lc-font-mono);font-size:15px;color:var(--lc-text-3);padding-top:4px}
.lc-cl-t{margin:0;font-size:18px;font-weight:600;color:var(--lc-text-1)}
.lc-cl-d{margin:6px 0 0;font-size:16px;line-height:22px;color:var(--lc-text-3)}

/* —— 7b. 关于我们 —— */
/* 网格参数照抄 FAQ（360px + 1fr / gap 80）：两个"左目录右正文"区块不该各造一套版式 */
.lc-about{display:grid;grid-template-columns:360px 1fr;gap:80px}
.lc-about-head{display:flex;flex-direction:column;gap:14px;align-items:flex-start}
.lc-about-p{margin:0;font-size:17px;line-height:24px;color:var(--lc-text-3)}
.lc-about-pt{border-top:1px solid var(--lc-border-faint);padding:20px 0;display:flex;flex-direction:column;gap:8px}
/* 标题 18px 与 FAQ 问题同档：三条是"陈述"不是"问答"，但阅读层级要一致 */
.lc-about-t{margin:0;font-size:18px;font-weight:600;color:var(--lc-text-1);display:flex;align-items:center;gap:10px}
.lc-about-t svg{color:var(--lc-text-3);flex:none}
.lc-about-d{margin:0;font-size:17px;line-height:24px;color:var(--lc-text-3)}

/* —— 8. 收尾 CTA —— */
/* 上边距给 0：它紧贴上一区块（关于我们），靠白块本身的反相与上文切开，不需要再留一段黑 */
.lc-cta-wrap{padding:0 80px 80px}
/* 整页唯一的大面积纯白（--lc-fill-white，与主按钮同档）+ 居中排版：读到这里只剩一个动作，因此取消所有左对齐的信息密度 */
.lc-cta{display:flex;flex-direction:column;align-items:center;gap:20px;padding:60px 80px;background:var(--lc-fill-white);border-radius:20px;text-align:center}
.lc-cta-t{margin:0;font-size:clamp(24px,2.22vw,32px);font-weight:700;color:#000}
/* 白块里的次级文字同样只能取最深那档灰：比 #000 弱一级，既读得清又不抢标题 */
.lc-cta-sub{margin:0;font-size:18px;color:var(--lc-text-4)}

/* —— 8b. 收尾留资表单（★ P1-3；结构见 components/LeadForm.tsx，本页带状态组件之一，另两处是 HeroDemo/DevSample） —— */
/* 分隔线两侧各一段 1px 短线：把"免费自助注册"与"留资等回电"两条路在视觉上并置成二选一 */
.lc-lead-sep{display:flex;align-items:center;gap:14px;width:100%;max-width:560px;font-size:15px;color:var(--lc-text-4)}
.lc-lead-sep::before,.lc-lead-sep::after{content:'';flex:1;height:1px;background:rgba(0,0,0,.12)}
/* 表单与分隔线同宽：白块居中排版下两条"路"共享同一根行长轴；relative 给蜜罐的 absolute 定位兜底 */
.lc-lead{position:relative;width:100%;max-width:560px;display:flex;flex-direction:column;gap:14px;text-align:left}
.lc-lead-row{display:flex;gap:14px}
.lc-lead-field{flex:1;display:flex;flex-direction:column;gap:6px;min-width:0}
.lc-lead-label{font-size:15px;font-weight:600;color:rgba(0,0,0,.68)}
/* 白块上的输入框必须显式给白底：.lc-input 基类是深色主题底，直接复用会黑成一团 */
.lc-lead-input{width:100%;background:#fff;border:1.2px solid rgba(0,0,0,.16);border-radius:10px;padding:10px 12px;font-size:16px;color:#000}
.lc-lead-input:focus{outline:none;border-color:#000}
.lc-lead-msg{resize:vertical;min-height:52px;font-family:inherit}
.lc-lead-langs{display:flex;flex-direction:column;gap:8px}
.lc-lead-chips{display:flex;flex-wrap:wrap;gap:8px}
/* 语言胶囊沿用全站 radius 999 口径；选中态=白块上的反相（黑底白字），与主按钮同语法 */
.lc-lead-chip{height:30px;padding:0 14px;border:1.2px solid rgba(0,0,0,.16);border-radius:999px;background:#fff;color:rgba(0,0,0,.72);font-size:15px;cursor:pointer;transition:border-color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out)}
.lc-lead-chip:hover{border-color:#000}
.lc-lead-chip.on{background:#000;border-color:#000;color:#fff}
/* 蜜罐：不用 display:none（部分 bot 会跳过隐藏域），用 1px 裁剪——人眼不可见、仍在 DOM */
.lc-lead-hp{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap}
.lc-lead-captcha{align-self:flex-start}
/* 报错只用判错红（与演示卡划线同色系），不引入第二套告警色 */
.lc-lead-err{margin:0;font-size:15px;color:var(--lc-danger)}
/* 提交按钮 = 白块上的主投反相（与收尾 Pill 同形）：同屏两颗黑胶囊只有一颗是"最终动作" */
.lc-lead-btn{align-self:flex-start;height:46px;padding:0 26px;border:0;border-radius:999px;background:#000;color:#fff;font-size:17px;font-weight:600;cursor:pointer}
.lc-lead-btn:disabled{opacity:.55;cursor:default}
/* 成功回执：中性描边框住一句 status 文案，不动用绿色（本页无绿口径） */
.lc-lead-ok{display:flex;align-items:center;gap:10px;width:100%;max-width:560px;padding:16px 18px;border:1.2px solid rgba(0,0,0,.14);border-radius:12px;background:rgba(0,0,0,.03);font-size:16px;color:#000;text-align:left}

/* —— 9. 页脚 —— */
.lc-foot{padding:60px 80px 40px}
/* 品牌区与链接列用 flex-wrap：窄屏整列换行，而不是把链接挤断成两行 */
.lc-foot-top{display:flex;gap:80px;flex-wrap:wrap}
.lc-foot-brand{width:320px;display:flex;flex-direction:column;gap:14px}
/* 页脚复用导航的品牌类名，只把字号调小：标识在整站只允许一套画法 */
.lc-foot-brand .lc-nav-brand{font-size:20px}
.lc-foot-tag{margin:0;font-size:16px;line-height:22px;color:var(--lc-text-3)}
.lc-foot-cols{display:flex;gap:80px;flex-wrap:wrap}
.lc-foot-col{display:flex;flex-direction:column;gap:12px;font-size:16px}
.lc-foot-col b{font-weight:600;color:var(--lc-text-1)}
.lc-foot-col a{color:var(--lc-text-3);transition:color var(--lc-mo-release) var(--lc-mo-out)}
.lc-foot-col a:hover{color:var(--lc-text)}
/* 分隔线用 1px 空 div：要的是"上 40 下 20"的不对称呼吸，线更贴近版权那一行 */
.lc-foot-div{height:1px;background:var(--lc-border-card);margin-top:40px}
.lc-foot-bot{padding-top:20px;font-size:15px;color:var(--lc-text-4)}

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
  .lc-step-arrow{transform:rotate(90deg);padding-inline-start:28px} /* 箭头旋转并左缩进到卡的内边距线上 */
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
  .lc-nav-cta .lc-mkt-btn--sm{height:36px;padding:0 14px;font-size:15px}
}

/* ================= Hero 演示卡（hero-stream.html 移植，类名 hd-*） ================= */
/* 这一段的类名一律 hd-* 前缀且只在本文件出现：演示卡是独立"器件"，不与上方营销布局互相覆写。
   状态类（in/out/done/cur/armed/hot/bright/blaze/dim…）全由 JS 切挂，CSS 只负责"长什么样"。 */
/* 卡片定宽 720：与画布演示区等宽，父级 flex 居中，max-width 让窄屏等比收缩而不换结构 */
.hd{width:720px;max-width:100%}
/* overflow:hidden 是给 .hd-barfill 与扫光用的：它们都靠"跑出去一截"实现动画，不能露出圆角外 */
/* 底色与页面同黑，所以"卡片感"只能靠最高一档描边（--lc-border-strong 6.0:1）+ 顶缘那 1px 内受光做出来 */
.hd-panel{
  background:var(--lc-bg);
  border:1.2px solid var(--lc-border-strong);
  border-radius:16px;overflow:hidden;position:relative;
  box-shadow:var(--lc-panel-highlight);
  transition:box-shadow .62s ease,filter .16s linear;
  animation:hdBoot .62s cubic-bezier(.16,1,.3,1) both;
}
/* 挂载时的起手动画：只做一次（both 锁终值），10px 上浮 + 微缩放，让卡片"落位"而不是淡入 */
@keyframes hdBoot{from{opacity:0;transform:translateY(10px) scale(.994)}to{opacity:1;transform:none}}
/* dim：术语判完最后一条时整卡压暗，为 ceremony 的爆发蓄落差（动效原则 3） */
.hd-panel.dim{filter:brightness(.84);transition:filter .5s cubic-bezier(.4,0,.2,1)}
/* bright / blaze 是峰值的"亮两级"。★ 〇-O 后基础框线已是纯白，border-color 再往上没有档，
   所以两级改用外扩白色光环表达（整卡仍不动几何，避免和 drop/punch 的 transform 抢） */
.hd-panel.bright{box-shadow:0 0 0 2px rgba(255,255,255,.30), var(--lc-panel-highlight)}
.hd-panel.blaze{box-shadow:0 0 0 3px rgba(255,255,255,.55), var(--lc-panel-highlight)}
/* drop：每判完一条术语让整卡下沉不到 1px——几乎看不见，但"落定了"的感觉靠它 */
.hd-panel.drop{animation:hdPanelDrop .24s cubic-bezier(.3,1.3,.5,1)}
@keyframes hdPanelDrop{0%{transform:translateY(0)}35%{transform:translateY(.9px)}100%{transform:translateY(0)}}
/* punch：落章时"闪白 + 冲击"两条动画并行——亮度走 filter、位移走 transform，各占一条才不会互盖 */
.hd-panel.punch{animation:hdFlash .5s cubic-bezier(.16,1,.3,1),hdImpact .52s cubic-bezier(.2,.9,.2,1)}
@keyframes hdFlash{0%{filter:brightness(1)}20%{filter:brightness(1.65)}100%{filter:brightness(1)}}
@keyframes hdImpact{0%{transform:translateY(0)}13%{transform:translateY(1.6px)}40%{transform:translateY(-.7px)}68%{transform:translateY(.3px)}100%{transform:translateY(0)}}

/* 顶部循环进度条：时长由 JS 写进 --dur（按当前语种文案长度折算），CSS 只管线性跑满 */
.hd-barfill{position:absolute;top:0;inset-inline-start:0;height:1px;width:0;background:var(--lc-text-1);opacity:.5}
/* var(--dur) 带 24000ms 兜底：单测/静态预览里没有 JS，也要能看到这条线在跑 */
.hd-barfill.run{animation:hdSweep var(--dur,24000ms) linear forwards}
@keyframes hdSweep{from{width:0}to{width:100%}}

/* 标题栏定高 46px：它只是"窗口感"，不参与内容高度计算 */
.hd-bar{height:46px;display:flex;align-items:center;justify-content:space-between;padding:0 32px;border-bottom:1px solid var(--lc-border-faint)}
/* 三点用第一/第三点拉开明暗：三颗同色会像"禁用态"，有一颗最亮才像窗口 */
.hd-dots{display:inline-flex;align-items:center;gap:6px;flex:none}
.hd-dots i{width:10px;height:10px;border-radius:999px;background:var(--lc-text-4)}
.hd-dots i:nth-child(1){background:var(--lc-text-1)}
.hd-dots i:nth-child(2){background:var(--lc-text-3)}
/* 品牌名与副标基线对齐：字号相同但字重与色阶不同，用 center 会让 em 看起来偏低 */
.hd-name{display:inline-flex;align-items:baseline;white-space:nowrap}
.hd-name b{font-size:15px;font-weight:600;color:var(--lc-text-1);letter-spacing:.02em}
.hd-name em{margin-left:5px;font-style:normal;font-size:15px;font-weight:600;color:var(--lc-text-2);letter-spacing:.02em}
/* 语种标签用拉丁字体族：里面是 "ZH → EN" 这类拉丁字形，用中文字体拿不到正确的箭头与字距 */
.hd-tag{font-size:14px;color:var(--lc-text-4);font-family:var(--lc-font-latin);letter-spacing:.03em;border:1.2px solid var(--lc-border-pill);border-radius:999px;padding:5px 12px;white-space:nowrap}

/* —— 原文区 —— */
.hd-stream{padding:30px 32px 20px}
.hd-src{border:1.2px solid var(--lc-border-input);border-radius:14px;padding:16px 18px}
.hd-srctag{display:block;font-size:14px;letter-spacing:.06em;color:var(--lc-text-4);margin-bottom:8px}
.hd-srcwrap{position:relative}
/* min-height 一格：ghost 与真身都在，这里再兜一层，避免首帧空白时输入框塌陷 */
.hd-srctext{font-size:17px;line-height:26px;color:#B4B9C0;min-height:26px}
/* ghost 用 visibility 不用 display：要它继续占位撑高，真身才能绝对定位盖上去（打字时不跳行高） */
.hd-srctext.ghost{visibility:hidden}
.hd-srcwrap .hd-srctext:not(.ghost){position:absolute;left:0;top:0;right:0}
/* 光标：::after 画 2px 竖条并 steps(1) 硬闪（0.9s 一轮），三处打字区共用同一套 hdBlink 节拍 */
.hd-srctext.typing::after{content:'';display:inline-block;width:2px;height:14px;margin-left:3px;vertical-align:-3px;background:currentColor;animation:hdBlink .9s steps(1) infinite}

/* 状态行：min-height 先占好一格，文案打出来前后都不许顶动下面的量尺 */
.hd-status{margin-top:14px;min-height:22px;display:flex;align-items:center;gap:10px;font-size:15px;line-height:22px;color:var(--lc-text-3);opacity:0;transition:opacity .45s ease}
.hd-status.in{opacity:1}
/* 脉冲点常驻无限循环：它表达"系统还活着"，一旦停了观众会以为演示结束 */
.hd-pulse{width:6px;height:6px;border-radius:999px;background:var(--lc-text-4);flex:none;animation:hdPulse 1.4s ease-in-out infinite}
@keyframes hdPulse{0%,100%{opacity:.3}50%{opacity:1}}

/* 量尺区（.hd-steps）= 上排三个步骤标签 + 下排一条刻度尺。标签由 DEMO_TERMS 直接生成，
   所以条数/宽度都是自适应的，加删术语不必回来改这里；.armed 由 play() 在状态行打完字后挂上
   （减少动效分支则由 staticState 直接挂），挂上之前整块保持透明。 */
.hd-steps{margin-top:16px;margin-bottom:16px}
.hd-slabels{display:flex;margin-bottom:8px}
/* flex:1 把整条尺按术语条数等分：标签落点与 JS 写进 style 的百分比共用同一套比例，天然对齐 */
.hd-sl{flex:1;min-width:0;display:flex;align-items:baseline;gap:6px;font-size:14px;line-height:16px;letter-spacing:.02em;color:#3F444B;white-space:nowrap;opacity:0;transition:color .5s ease}
/* 序号单独 10px + 等宽数字：01/02/03 的字宽必须一致，否则后面的中文标签会左右跳动 */
.hd-sl i{font-style:normal;font-family:var(--lc-font-latin);font-size:12px;font-weight:600;letter-spacing:0;font-variant-numeric:tabular-nums;color:#33383F;transition:color .5s ease}
/* done→cur 只换颜色、不换字重：字重一变行宽就抖，量尺只是背景信息，抖动比对比度低更难受 */
.hd-sl.done{color:#8A9099}.hd-sl.done i{color:#C8CCD1}
.hd-sl.cur{color:var(--lc-text-1)}.hd-sl.cur i{color:#fff}
/* 数字"上钩"只在 armed 之后允许：没点亮就播会和下面 .armed 的入场节拍（.20/.29/.38s）抢跑 */
.hd-steps.armed .hd-sl.cur i{animation:hdNumHook .4s cubic-bezier(.16,1,.3,1)}
@keyframes hdNumHook{0%{transform:none}30%{transform:translateY(-2.5px)}100%{transform:none}}

/* —— 刻度尺本体：12px 高的相对定位容器，三段全部绝对定位，共用同一条中线（top:50% + 负 margin）—— */
.hd-gauge{position:relative;height:12px}
/* 轨道：scaleX(0) 起步，.armed 时展开成 1——先"把尺子画出来"，再在上面量 */
.hd-gtrack{position:absolute;left:0;right:0;top:50%;height:2px;margin-top:-1px;border-radius:2px;background:var(--lc-border-faint);transform:scaleX(0);transform-origin:left}
/* 已判定段：width 由 JS 直写百分比（走到本条的 60% 处），1.05s 缓动比单条节拍长一点，像"慢慢追上去" */
.hd-gfill{position:absolute;inset-inline-start:0;top:50%;height:2px;margin-top:-1px;width:0;border-radius:2px;background:linear-gradient(90deg,rgba(255,255,255,.30) 0%,rgba(255,255,255,.75) 70%,#fff 100%);box-shadow:0 0 10px rgba(255,255,255,.40);transition:width 1.05s cubic-bezier(.22,1,.28,1)}
/* 尺珠：3×12 竖条带光晕，left 用与 gfill 完全相同的时长与曲线，两者走位永远同步不脱节 */
.hd-gbead{position:absolute;inset-inline-start:0;top:50%;width:3px;height:12px;margin-top:-6px;margin-inline-start:-1.5px;border-radius:2px;background:#fff;box-shadow:0 0 12px rgba(255,255,255,.95),0 0 3px #fff;opacity:0;transition:inset-inline-start 1.05s cubic-bezier(.22,1,.28,1)}
/* 外扩 36px 径向光用伪元素而不是 box-shadow：box-shadow 出不了珠子自身轮廓，做不出这团"热度" */
.hd-gbead::before{content:'';position:absolute;left:50%;top:50%;width:36px;height:36px;margin:-18px 0 0 -18px;border-radius:50%;background:radial-gradient(closest-side,rgba(255,255,255,.26) 0%,rgba(255,255,255,0) 100%)}
/* 内层 16px 圆环：平时 scale(.35)+透明，只有 .snap 才炸一次，用作"这一条判完了"的打点 */
.hd-gbead::after{content:'';position:absolute;left:50%;top:50%;width:16px;height:16px;margin:-8px 0 0 -8px;border-radius:50%;border:1.2px solid rgba(255,255,255,.9);opacity:0;transform:scale(.35)}
/* forwards 停在透明终态：摘掉 .snap 时不会看到圆环"倒放"回去 */
.hd-gbead.snap::after{animation:hdBeadPulse .62s cubic-bezier(.2,.8,.2,1) forwards}
@keyframes hdBeadPulse{0%{opacity:0;transform:scale(.35)}12%{opacity:.95;transform:scale(.6)}100%{opacity:0;transform:scale(2.6)}}
/* —— 以下四条 .armed 规则是"点亮"的一次性编排，全部交给 CSS 排期，JS 只负责加一个类 —— */
.hd-steps.armed .hd-gtrack{transform:none;transition:transform .46s cubic-bezier(.22,1,.28,1)}
.hd-steps.armed .hd-sl{animation:hdSlIn .44s cubic-bezier(.16,1,.3,1) both}
.hd-steps.armed .hd-sl:nth-child(1){animation-delay:.20s}
/* 相邻标签错 90ms：能数出"一条一条"，又不至于慢到打断演示整体节奏（一轮约 24s） */
.hd-steps.armed .hd-sl:nth-child(2){animation-delay:.29s}
.hd-steps.armed .hd-sl:nth-child(3){animation-delay:.38s}
@keyframes hdSlIn{from{opacity:0;transform:translateY(-4px)}to{opacity:1;transform:none}}
/* 珠子上场排在最后（.46s）：轨道铺完、标签点亮完，它才有"沿着尺子滑"的前置条件 */
.hd-steps.armed .hd-gbead{animation:hdBeadIn .34s ease .46s both}
@keyframes hdBeadIn{from{opacity:0}to{opacity:1}}
/* .hot = ceremony 峰值那 660ms 的高亮档：填充/珠子/标签统一转纯白，过渡一律压到 0.1s 线性硬切，
   才有"通电"的感觉；JS 摘掉 .hot 后自动回落到上面这些 .5s 缓动，收势不需要额外写代码 */
.hd-steps.hot .hd-gfill{background:#fff;box-shadow:0 0 18px rgba(255,255,255,.95);transition:background .1s linear,box-shadow .1s linear}
.hd-steps.hot .hd-gbead{box-shadow:0 0 22px #fff,0 0 6px #fff;transition:box-shadow .1s linear}
.hd-steps.hot .hd-sl{color:#fff;transition:color .1s linear}

/* —— 术语行轨道 —— */
/* 层级关系：.hd-result 是这里唯一占流的子节点（透明时也保留位置），.hd-row/.hd-flash/.hd-rule 都是覆盖层。
   min-height 先兜住一格，结果框内容短的时候轨道也不会缩。 */
.hd-track{position:relative;min-height:128px;display:flex;align-items:center}
/* 一行 = 序号 | 中文 | 箭头 | 初译 → 正译；绝对定位钉在同一处，三条轮播复用这一个 DOM（clearRow 只做复位） */
/* blur 与位移同时消解，观感是"对焦到这一行"而不是普通淡入；transform 曲线控制点 >1，入场带一点回弹 */
.hd-row{position:absolute;left:0;right:0;top:12px;height:64px;display:flex;align-items:center;gap:16px;opacity:0;transform:translateY(20px);filter:blur(3px);transition:opacity .30s cubic-bezier(.2,.85,.25,1),transform .46s cubic-bezier(.16,1.06,.3,1),filter .30s ease}
.hd-row.in{opacity:1;transform:none;filter:blur(0)}
/* 离场改的是另一套曲线（.4,0,.6,1，不回弹）+ 每条 transition 都带 .26s 延迟：
   先原地停一拍让上一行读完，再向上散掉，也避免与下一条入场叠成"两行同时在" */
.hd-row.out{opacity:0;transform:translateY(-26px);filter:blur(3px);transition:opacity .40s cubic-bezier(.4,0,.7,1) .26s,transform .46s cubic-bezier(.4,0,.6,1) .26s,filter .40s ease-in .26s}
/* 子元素再快一档（.16s）各自淡出，整行不会像一块板那样消失 */
.hd-row.out .hd-idx,.hd-row.out .hd-cn,.hd-row.out .hd-arrow,.hd-row.out .hd-w{opacity:0;transform:translateY(-5px);transition:opacity .16s ease-in,transform .16s ease-in}
.hd-row.lit{animation:hdRowLit .48s ease-out}
@keyframes hdRowLit{0%{filter:brightness(1)}16%{filter:brightness(1.6)}100%{filter:brightness(1)}}
/* 命中确认只动 filter:brightness，不碰 color/宽度：这两样已被 struck 与 glow 占用，同属性会互相覆写 */

/* 序号：默认就是危险色——它标的是"被判错的那条初译"；24px 定宽 + 等宽数字，三条轮播左缘才齐 */
.hd-idx{align-self:center;flex:none;width:24px;font-family:var(--lc-font-latin);font-size:16px;font-weight:600;color:var(--lc-danger);font-variant-numeric:tabular-nums}
.hd-idx.pop{animation:hdIdxPop .46s cubic-bezier(.2,.9,.3,1) both}
@keyframes hdIdxPop{0%{transform:scale(1.38);color:#fff}100%{transform:scale(1);color:var(--lc-danger)}}
/* 白起纯基线收：pop 与 .hd-row.in 同帧挂，读作"这一项开始了"，所以起帧要最亮 */
.hd-swap{display:flex;align-items:baseline;gap:19px;min-width:0}
/* 初译/正译字号差近一倍，只有 baseline 对齐才能让两栏像同一句话；align-items:center 会让小字看着下沉 */
.hd-w{position:relative;flex:none;display:inline-block;white-space:nowrap;width:clamp(74px,22.917vw,165px);font-family:var(--lc-font-latin);font-size:clamp(21px,5.278vw,38px);font-weight:600;line-height:clamp(32px,7.778vw,56px);color:#8E949C;letter-spacing:-.015em;transition:color .28s ease}
.hd-w .hd-wt{position:relative;display:inline-block}
/* 文本挂内层 .hd-wt：外层是定宽的排版单元，内层才等于"词的实际宽度"，删除线不能画到空白上去 */
.hd-w .hd-wt::before{content:'';position:absolute;left:-4px;right:-4px;top:56%;height:3px;margin-top:-1.5px;background:var(--lc-danger);border-radius:2px;transform:scaleX(0);transform-origin:left center}
.hd-w.struck{color:var(--lc-danger);animation:hdStrikeFlash .72s ease-out both}
.hd-w.struck .hd-wt::before{transform:scaleX(1);animation:hdStrikeIn .36s cubic-bezier(.34,0,.2,1) both}
/* 用 scaleX 而不是 width：展开不触发重排，箭头与正译的位置在判错瞬间纹丝不动 */
@keyframes hdStrikeIn{0%{transform:scaleX(0)}74%{transform:scaleX(1.035)}100%{transform:scaleX(1)}}
/* 过冲 3.5% 再收回：像笔锋多拖了一下，比匀速划到端点更像"人手画的线" */
@keyframes hdStrikeFlash{0%{text-shadow:0 0 0 rgba(229,72,77,0)}20%{text-shadow:0 0 18px rgba(229,72,77,.62)}100%{text-shadow:0 0 0 rgba(229,72,77,0)}}
/* 只亮 20% 那一拍：闪一下是"判错"，常亮会去抢正译栏（字号最大那栏）的注意力 */
.hd-wt.typing::after{content:'';display:inline-block;width:2px;height:clamp(19px,4.167vw,30px);margin-inline-start:3px;vertical-align:-5px;background:currentColor;animation:hdBlink .9s steps(1) infinite}
.hd-arrow{flex:none;display:flex;align-items:center;color:var(--lc-border-faint);opacity:0;transform:translateX(-5px);transition:opacity .22s ease,transform .22s ease,color .28s ease}
/* 箭头单独 .in 提亮（描边色→#A0A5AC）：出场排在中文之后、初译之前，用次序表达"从这里开始换外语" */
.hd-arrow.in{opacity:1;transform:none;color:#A0A5AC}
.hd-r{font-family:var(--lc-font-latin);font-size:clamp(30px,6.944vw,50px);font-weight:800;line-height:clamp(42px,8.333vw,60px);color:#fff;letter-spacing:-.028em;white-space:nowrap;min-width:1px;text-shadow:0 0 0 rgba(255,255,255,0);transition:text-shadow .62s ease}
/* min-width:1px 兜住空文本帧——glow 比打字先挂，那一刻栏里还没字；text-shadow .62s 长过渡让辉光是"渗出来"的 */
.hd-r.glow{text-shadow:0 0 16px rgba(255,255,255,.5)}
.hd-r.lock{animation:hdRLock .42s cubic-bezier(.2,.9,.3,1) both}
@keyframes hdRLock{0%{transform:scale(1.05);text-shadow:0 0 17px rgba(255,255,255,.72)}55%{transform:scale(.994)}100%{transform:scale(1);text-shadow:0 0 16px rgba(255,255,255,.5)}}
/* 终值停在 glow 的辉光上（both 锁住）：所以摘掉 .lock 后 glow 接手，两拍之间没有亮度断层 */
.hd-r.typing::after{content:'';display:inline-block;width:2px;height:clamp(26px,5.833vw,42px);margin-inline-start:5px;vertical-align:-9px;background:#fff;animation:hdBlink .9s steps(1) infinite}
@keyframes hdBlink{0%,49%{opacity:1}50%,100%{opacity:0}}
/* steps(1) = 硬切而不是渐隐：四处打字区（原文 / 初译 / 正译 / 定稿）都挂这一条 hdBlink，光标节拍全站一致 */
.hd-cn{flex:none;width:clamp(78px,12.778vw,92px);font-size:clamp(14px,2.361vw,17px);font-weight:500;color:#B4B9C0;letter-spacing:.04em;white-space:nowrap;opacity:0;transform:translateX(-7px);transition:opacity .30s cubic-bezier(.2,.85,.25,1),transform .38s cubic-bezier(.16,1.08,.3,1)}
/* 中文栏定宽：JS 是逐字往里写的，不定宽的话箭头与初译会跟着字数的增加一路右移 */
.hd-cn.in{opacity:1;transform:none}

/* —— 回写结果框：峰值一次给"框出现→斜扫光→描边转完成色→落章"四段，段段独立挂类（in/sweep/settled/stamp）—— */
/* 起手 transform:scale(.992) 而不是 scale(.9)：结果框里全是正文，位移过大会读不清；overflow:hidden 给 ::before/::after 的两道扫光裁边 */
.hd-result{position:relative;overflow:hidden;width:100%;padding:16px 18px;border:1.2px solid var(--lc-border-input);border-radius:14px;opacity:0;transform:scale(.992);transform-origin:50% 50%;transition:opacity .3s ease-out,transform .42s cubic-bezier(.16,1,.3,1),border-color .7s ease}
.hd-result.in{opacity:1;transform:none}
/* settled 只换描边、.7s 慢过渡：爆发收势后要把"这是最终结果"这件事安静地说完，不该再有动静 */
.hd-result.settled{border-color:var(--lc-border-done)}
/* 下笔线与光斑都是 .hd-track 的子节点（不是结果框的），top:44px 正好压在术语行中线（行 top:12 + 高 64 的一半） */
.hd-rule{position:absolute;left:0;right:0;top:44px;height:2px;margin-top:-1px;background:linear-gradient(90deg,rgba(255,255,255,0) 0%,#fff 6%,#fff 94%,rgba(255,255,255,0) 100%);box-shadow:0 0 10px rgba(255,255,255,.4);opacity:0;transform:scaleX(0);transform-origin:left center;pointer-events:none}
/* 两端透明渐变是"笔迹"的起收笔；pointer-events:none 保证覆盖层不吃掉卡片上的选中/悬停 */
.hd-rule.write{animation:hdWriteIn .46s cubic-bezier(.3,0,.2,1) forwards}
@keyframes hdWriteIn{0%{opacity:0;transform:scaleX(0)}14%{opacity:.95}70%{opacity:.95}100%{opacity:0;transform:scaleX(1)}}
/* 70%→100% 这段是"写完就淡掉"：峰值那帧线已经没用了，留着会和结果框抢注意力 */
.hd-flash{position:absolute;left:50%;top:44px;width:min(420px,90%);height:150px;transform:translate(-50%,-50%) scale(.55);border-radius:50%;pointer-events:none;background:radial-gradient(closest-side,rgba(255,255,255,.09) 0%,rgba(255,255,255,.05) 48%,rgba(255,255,255,0) 78%);opacity:0}
/* 光斑亮度只到 .09：整卡已经是纯黑底，再亮就成"白闪"，会刺眼且显得廉价 */
.hd-flash.burst{animation:hdBurst .6s cubic-bezier(.16,1,.3,1) forwards}
@keyframes hdBurst{0%{opacity:0;transform:translate(-50%,-50%) scale(.52)}10%{opacity:1;transform:translate(-50%,-50%) scale(.9)}100%{opacity:0;transform:translate(-50%,-50%) scale(1.28)}}
/* 10% 处就到最大亮度、之后只扩散不增亮：能量"释放在前"才像被击中，平均分布会像呼吸灯 */
/* ::before = 整框白闪（落章那一拍填充），::after = 斜向扫光，两条各占一个伪元素才能同框不同步 */
.hd-result::before{content:'';position:absolute;inset:0;border-radius:inherit;background:#fff;opacity:0;pointer-events:none}
.hd-result.stamp::before{animation:hdStampFill .56s cubic-bezier(.16,1,.3,1) forwards}
@keyframes hdStampFill{0%{opacity:0}18%{opacity:.13}100%{opacity:0}}
/* 峰值只到 .13：白闪是"衬"一下内容，不是把内容盖掉 */
.hd-result.stamp{animation:hdStampRing .8s cubic-bezier(.16,1,.3,1) forwards}
@keyframes hdStampRing{0%{box-shadow:0 0 0 1px rgba(255,255,255,0),0 0 0 rgba(255,255,255,0)}12.5%{box-shadow:0 0 0 1px rgba(255,255,255,.55),0 0 30px rgba(255,255,255,.20)}100%{box-shadow:0 0 0 1px rgba(255,255,255,0),0 0 0 rgba(255,255,255,0)}}
/* 两层 box-shadow（1px 硬描边 + 30px 外晕）分别承担"章的边缘"和"章的余温"，只用一层会要么太硬要么太散 */
.hd-result::after{content:'';position:absolute;inset:0;border-radius:inherit;pointer-events:none;background:linear-gradient(100deg,transparent 0%,rgba(255,255,255,.085) 48%,transparent 92%);opacity:0;transform:translateX(-100%)}
.hd-result.sweep::after{animation:hdSweepLight .84s cubic-bezier(.3,0,.2,1) forwards}
@keyframes hdSweepLight{0%{opacity:0;transform:translateX(-100%)}22%{opacity:1}100%{opacity:0;transform:translateX(100%)}}
/* 100deg 斜向而不是 90deg：横向平移像"进度条"，带角度才像"审校笔扫过" */

/* —— 结果框头部：完成图标 + 结论 + 统计 在左，示意按钮在右 —— */
.hd-rhead{display:flex;align-items:center;margin-bottom:10px}
/* 左半单独包一层并给 min-width:0：三段文案是 nowrap 的，不收缩就会把右边的按钮挤出框 */
.hd-rleft{display:flex;align-items:center;gap:8px;min-width:0}
/* 必须自己当定位父：对勾圆环 .hd-ring 是它的绝对定位子节点，两者中心要重合 */
.hd-rcheck{display:flex;align-items:center;position:relative}
/* dasharray/dashoffset 同为 15（略大于 path 实际长度）：起笔完全藏在起点之外，draw 才是"写出来" */
.hd-mark path{stroke-dasharray:15;stroke-dashoffset:15}
.hd-mark.draw path{animation:hdDraw .34s cubic-bezier(.2,.8,.2,1) forwards}
@keyframes hdDraw{to{stroke-dashoffset:0}}
/* draw 改 stroke-dashoffset、glow 改 filter：两条动画属性不相交，所以能在同一元素上并行播 */
.hd-mark.glow{animation:hdMarkGlow .72s ease-out forwards}
/* drop-shadow 而非 box-shadow：SVG 里只有那两笔折线，box-shadow 会照矩形边框发光 */
@keyframes hdMarkGlow{0%{filter:drop-shadow(0 0 0 rgba(255,255,255,0))}8%{filter:drop-shadow(0 0 8px rgba(255,255,255,.95))}100%{filter:drop-shadow(0 0 0 rgba(255,255,255,0))}}
.hd-ring{position:absolute;left:50%;top:50%;width:16px;height:16px;margin:-8px 0 0 -8px;border-radius:999px;border:1.2px solid rgba(255,255,255,.85);opacity:0;transform:scale(.5);pointer-events:none}
.hd-ring.go{animation:hdRing .92s cubic-bezier(.2,.8,.2,1) forwards}
@keyframes hdRing{0%{opacity:0;transform:scale(.5)}6%{opacity:.9}100%{opacity:0;transform:scale(3.2)}}
/* 扩散到 3.2 倍才收：这是"落章"的余波，收得比对勾快会让人只看到一圈闪光 */
.hd-rlabel{font-size:16px;font-weight:500;color:var(--lc-text-1);white-space:nowrap}
/* 结论标签的 .stamp 与对勾 .glow 在 JS 里同帧挂：两处亮点同时出现才像"盖章"，错开会被读成两个事件 */
.hd-rlabel.stamp{animation:hdLabelGlow .8s ease-out forwards}
/* 100% 帧显式写回 var(--lc-text-1)：forwards 锁的就是终帧，不写回来文案会永久停在纯白 */
@keyframes hdLabelGlow{0%{color:var(--lc-text-1);text-shadow:none}7%{color:#fff;text-shadow:0 0 12px rgba(255,255,255,.85)}100%{color:var(--lc-text-1);text-shadow:none}}
/* 统计副标平时压到最暗一档：先给结论再给数字；.lit 由 JS 在盖章之后再挂，顺序反了会抢峰值 */
.hd-rsub{font-size:14px;color:var(--lc-text-4);transition:color .55s ease;white-space:nowrap}
.hd-rsub.lit{color:var(--lc-text-2)}
/* 右侧"复制"是真实按钮：点击把定稿译文写入剪贴板并短暂显示"已复制"（font 继承自 .hd-rhead 语境） */
.hd-dlbtn{margin-inline-end:auto;flex:none;display:inline-flex;align-items:center;gap:6px;height:28px;padding:0 12px;border:1.2px solid var(--lc-border-pill);border-radius:8px;background:none;color:#C8CCD1;font:500 14px/1 var(--lc-font);cursor:pointer;transition:border-color var(--lc-mo-release) var(--lc-mo-out),color var(--lc-mo-release) var(--lc-mo-out)}
.hd-dlbtn:hover{border-color:var(--lc-border-done);color:var(--lc-text-1)}
.hd-dlbtn svg{display:block}
/* display:block 消掉行内 SVG 的基线下沉：图标与 12px 文案要在 28px 高的胶囊里精确居中 */

/* —— 定稿译文区：与原文区同一套"ghost 撑高 + 真身覆盖"，外层再叠一条审校窄光 —— */
.hd-fwrap{position:relative}
.hd-final{font-family:var(--lc-font-latin);font-size:18px;line-height:28px;color:var(--lc-text-1)}
/* 拉丁字体族：DEMO_FINAL 是整句英文，用中文栈会让字距与连字都变形（中英混排时才需要中文兜底） */
.hd-final.ghost{visibility:hidden}
.hd-fwrap .hd-final:not(.ghost){position:absolute;left:0;top:0;right:0}
/* 真身靠 :not(.ghost) 挑出来，不需要额外的 data-hd 类：ghost 与真身共用 .hd-final，样式只写一份 */
.hd-final.done{animation:hdProofLight 1s cubic-bezier(.16,1,.3,1) forwards}
@keyframes hdProofLight{0%{color:var(--lc-text-1);text-shadow:none}10%{color:#fff;text-shadow:0 0 15px rgba(255,255,255,.62)}100%{color:var(--lc-text-1);text-shadow:none}}
/* 1s 里只有前 10% 是亮的：定稿要"被点亮一次"，然后立刻退回可读基线，常亮会让正文失去对比度 */
.hd-final.typing::after{content:'';display:inline-block;width:2px;height:15px;margin-inline-start:3px;vertical-align:-3px;background:var(--lc-text-1);animation:hdBlink .9s steps(1) infinite}
/* 光带本身就是这张伪元素的背景（宽 16px、不重复），动画只挪 background-position：
   伪元素始终 inset:0 覆盖整块，尺寸不变所以不会引起重排 */
.hd-fwrap::after{content:'';position:absolute;inset:0;pointer-events:none;background:linear-gradient(90deg,rgba(255,255,255,0) 0%,rgba(255,255,255,.16) 35%,rgba(255,255,255,.95) 50%,rgba(255,255,255,.16) 65%,rgba(255,255,255,0) 100%);background-size:16px 100%;background-repeat:no-repeat;background-position:-16px 0;opacity:0}
.hd-fwrap.proof::after{animation:hdProof .7s cubic-bezier(.4,0,.2,1) forwards}
@keyframes hdProof{0%{background-position:-16px 0;opacity:0}12%{background-position:0 0;opacity:.8}88%{opacity:.8}100%{background-position:100% 0;opacity:0}}
/* 12%~88% 恒定 .8：中段不衰减，光带经过文字时亮度稳定，才像"一道审校灯"而不是反光 */

/* 演示卡窄屏：术语行折两行（第一行 序号+中文，第二行 箭头+初译 正解） */
@media (max-width:620px){
  /* 左右内边距 32→16：窄屏先把标题栏与内容区的留白让出来，卡片宽度全给文字 */
  .hd-bar{padding:0 16px}
  .hd-name em{display:none}
  /* 副标整块撤掉而不是缩小：这一栏放不下两段文字，语种标签（ZH → EN）才是演示的语境信息 */
  .hd-stream{padding:20px 16px 16px}
  /* 换 grid 具名区域重排：DOM 一个节点都不动，JS 缓存的 data-hd 句柄在窄屏继续有效 */
  .hd-row{top:50%;height:auto;display:grid;grid-template-columns:auto 1fr;grid-template-areas:"idx cn" "arrow swap";align-items:center;column-gap:clamp(8px,2.222vw,16px);row-gap:clamp(2px,.556vw,4px);transform:translateY(calc(-50% + 20px))}
  /* 绝对定位的行改由 translateY 自己居中：基态 = 居中再下沉 20px，.in = 居中，.out = 居中再上移 26px，三处必须成套 */
  .hd-row.in{transform:translateY(-50%)}
  .hd-row.out{transform:translateY(calc(-50% - 26px))}
  /* 下面四条只把已有节点指派进对应格子，读写次序仍由 DOM 决定（idx→cn→arrow→swap 逐拍点亮不变） */
  .hd-idx{grid-area:idx}
  .hd-cn{grid-area:cn}
  .hd-arrow{grid-area:arrow}
  .hd-swap{grid-area:swap;justify-content:flex-start;min-width:0}
  /* 下笔线与光斑跟到新的视觉中线：宽屏写死的 44px 是"单行时的行中线"，折两行后那个位置已经不是中线 */
  .hd-rule,.hd-flash{top:50%}
  /* clamp 上限（165px/38px、50px）在窄屏根本用不到，实际生效的是中间的 vw 项；下调 min 是为了不顶破 320px 视口 */
  .hd-w{font-size:clamp(17px,5.278vw,38px);line-height:clamp(26px,7.778vw,56px)}
  .hd-r{font-size:clamp(24px,6.944vw,50px);line-height:clamp(34px,8.333vw,60px)}
}

/* 无障碍：偏好减少动效时，演示卡直接呈现终态，所有爆发一律不播 */
@media (prefers-reduced-motion: reduce){
  /* 兜底策略：只关 animation / transition，不关任何颜色与布局——终态的"信息"要完整保留（staticState 已把文本与刻度写好） */
  .hd-panel{animation:none !important}
  /* 逐条给终值：opacity/transform 一律直接落到位，"不播动画"不等于"看不到内容" */
  .hd-steps .hd-sl,.hd-steps .hd-gbead{opacity:1 !important;animation:none !important}
  .hd-steps .hd-gtrack{transform:none !important;transition:none !important}
  /* 尺珠与填充段的 left/width 过渡也关掉：staticState 是直写 100% 的，留着过渡会让终态又"滑"一次 */
  .hd-gfill,.hd-gbead{transition:none !important}
  /* 下面这条是"一次性爆发"黑名单（峰值、落章、划词、定稿光全在内）：只关 animation，颜色与字色一概不动，
     所以"被判错的初译"仍然是红划线红字，信息量不减。持续型的位移淡入（如 .hd-row）没全列进来，
     要更严格的话可以再补——目前靠 JS 侧的 staticState 分支直接摆终态 */
  .hd-flash.burst,.hd-panel.punch,.hd-mark.glow,.hd-rlabel.stamp,.hd-result.stamp,.hd-result.stamp::before,.hd-final.done,.hd-fwrap.proof::after,.hd-idx.pop,.hd-w.struck,.hd-w.struck .hd-wt::before,.hd-r.lock,.hd-row.lit,.hd-gbead.snap::after,.hd-panel.drop,.hd-panel.bright,.hd-panel.blaze{animation:none !important}
}
`
