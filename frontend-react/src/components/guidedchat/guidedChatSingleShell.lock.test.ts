// @vitest-environment node
// ============================================================================
// components/guidedchat/guidedChatSingleShell.lock.test.ts — 「引导外壳只有一份」的机械闸（H1，2026-11-06）
//
// 为什么必须有这把闸（不是洁癖）：
//   H1 的改形把注册面那套「打字机＋步进栈＋代际＋卡片骨架＋样式」抽成 guidedchat 一份，
//   让引导建产品复用。这种"抽公共件"的修法**最典型的退化**就是：
//   下一个场景图省事，在组件里再抄一份打字机或再写一遍 `.ar-panel{...}`
//   ⇒ 两份迟早分叉（一份改了动效档位、另一份没改），
//   而屏幕上没有任何症状——这正是 AGENTS §一·5「三口径打架」与 §一·11「多把尺子」那一族。
//   所以这条闸问的不是"这页长得对不对"，而是**实现有几份**。
//
// 判据（每条各治一病，2026-11-06 复核轮补了两条）：
//   S1 打字机/步进栈的实现体只许存在于 guidedChatMachine.ts（其他文件出现＝第二把尺子）；
//       ★ 动效档探测**按调用形状判、射程收在两个场景文件里**：旧判据钉的是 `function usePrefersReducedMotion`
//         那个**名字**，而现实现把探测内联在 machine:120、全仓没有那个名字 ⇒ 那条腿结构性空转，
//         新场景再抄一份 `window.matchMedia('(prefers-reduced-motion…')` 一个都不红。
//         不许扩成"全仓禁 matchMedia"：Landing/MessageBubble/WordSwap/Login 那些读点服务各自的动效，
//         把合法代码判成违规的闸门，归宿是被摘掉。
//   S1b 场景必须真的**消费**外壳交出来的 `reduced`（正向对照：只有负锁时"摘掉 reduced 写死 false"两头都不红）；
//   S2 两个场景都必须**真的 import** 那一份（正锁；只测"没抄"测不出"根本没复用"）；
//   S3 `.ar-*` 样式表只许在 guidedChatStyles.ts 声明一次（组件里再出现同名规则即红）；
//   S4 反证：把"再抄一份打字机／再写一份样式／场景里再内联一次探测／场景把 reduced 摘掉"
//       四种坏形态喂给**同一个判据函数**，必须被抓到；另配两条**射程**反证
//       （探测写在 machine 里合法、写在别的组件里也不许判红）与一条"只在注释里提名字不算违规"。
//
// ⚠️ 判"有没有"之前必须先**剥注释**：本仓老坑——说明文字里出现那个名字，负向锁就把"记录了坑"读成"踩了坑"。
// ============================================================================
import * as fs from 'node:fs'
import * as path from 'node:path'
import { describe, expect, it } from 'vitest'

// 扫描根＝frontend-react/src；判据一律吃「相对 src 的路径」，与下面三张常量清单同一口径。
const SRC = path.resolve(__dirname, '../..')

/** 只扫可执行行：行注释与块注释都先剥掉（AGENTS §一·5 那条「注释不许冒充实现」） */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').split('\n')
    .filter((l) => !l.trim().startsWith('//'))
    .join('\n')
}

/** 递归收集 src 下的 ts/tsx 源文件（排除测试自己，否则本文件的说明会被自己判成违规） */
function sources(dir: string, acc: string[] = []): string[] {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) { sources(p, acc); continue }
    if (!/\.(ts|tsx)$/.test(e.name)) continue
    if (e.name.endsWith('.test.ts') || e.name.endsWith('.test.tsx')) continue
    acc.push(p)
  }
  return acc
}

const MACHINE = 'components/guidedchat/guidedChatMachine.ts'
const STYLES = 'components/guidedchat/guidedChatStyles.ts'
// 现役两个场景（AI 顾问注册流／引导建产品）：S1 的"自己又探一次 reduced-motion"与
// "必须真消费外壳交出来的 reduced"两条判据都**只在这两份文件里**判，外壳自己探是合法的。
const SCENES = ['components/AiRegisterFlow.tsx', 'components/Onboarding.tsx']

/** 判据本体：输入 (相对路径, 源码) 全集，输出违规清单。真源码与合成件喂**同一个函数**（S4） */
function findSecondShell(entries: Array<{ rel: string; body: string }>): string[] {
  const bad: string[] = []
  for (const { rel, body } of entries) {
    const code = stripComments(body)
    const inMachine = rel === MACHINE
    const inStyles = rel === STYLES
    const isScene = SCENES.includes(rel)
    // ① 打字机/步进栈的实现体只许在状态机那一份里
    if (!inMachine) {
      for (const pat of [
        /const\s+stepStack\s*=\s*useRef/,                 // 自己再起一个步进栈
        /function\s+usePrefersReducedMotion/,             // H1 之前注册面那份探测的旧名字（历史形态，留着别长回来）
        /typingUnitOf\(\s*\w+\s*\)\s*[.;)]/,              // 自己取打字单元
        /chatTypingMs\(\s*\w+\s*\)\s*[.;)]/,              // 自己取打字速度
      ]) {
        if (pat.test(code)) bad.push(`${rel}：另起了一份引导动效实现（${pat.source.slice(0, 28)}…）`)
      }
    }
    // ② ★ 动效档探测按**调用形状**判，而且射程收在"这一副外壳的场景文件"里。
    //    旧判据写的是 `function usePrefersReducedMotion` 那个**名字**，而现实现把探测**内联**在
    //    guidedChatMachine.ts:120（全仓没有那个函数名）⇒ 那条腿结构性空转：
    //    新场景里再抄一份 `window.matchMedia('(prefers-reduced-motion…')` 一个都不会红。
    //    反过来也不能把它判成全仓禁令——Landing／MessageBubble／WordSwap／Login 那些
    //    reduced-motion 读点服务的是**各自**的动效，与外壳无关（扩射程＝把合法代码判成违规，
    //    那种闸门的归宿是被摘掉）。所以：**场景文件里 matchMedia 零命中**。
    if (isScene && /matchMedia\s*(?:\?\.)?\s*\(/.test(code)) {
      bad.push(`${rel}：自己又探了一次 prefers-reduced-motion（外壳已经把 reduced 交出来了）`)
    }
    // ③ ★ 正向对照：场景必须真的**消费**那一份探测结果。只有负锁时，
    //    "把 reduced 从解构里摘掉、改成写死 false"这种坏法两头都不红。
    if (isScene) {
      const flowName = /const\s+(\w+)\s*=\s*useGuidedChat(?:<[^>]+>)?\s*\(/.exec(code)?.[1]
      const binding = flowName && new RegExp(`const\\s*\\{[^}]*\\breduced\\b[^}]*\\}\\s*=\\s*${flowName}\\b`)
      if (!binding || !binding.test(code)) {
        bad.push(`${rel}：没有用外壳交出来的 reduced（复用断了，不是抄了一份）`)
      }
    }
    // ④ `.ar-*` 的样式规则只许在样式那一份里
    if (!inStyles && /(^|[;\s}])\.ar-[a-z-]+\s*\{/.test(code)) {
      bad.push(`${rel}：又写了一遍 .ar-* 样式（交付真值只能有一个出处）`)
    }
  }
  return bad
}

describe('引导外壳只有一份实现（H1 机械闸）', () => {
  const files = sources(SRC)
  const entries = files.map((p) => ({ rel: path.relative(SRC, p).split(path.sep).join('/'), body: fs.readFileSync(p, 'utf8') }))

  it('S0 射程自证：扫描范围必须真的覆盖到状态机与两个场景（扫不够＝闸门失明）', () => {
    const rels = entries.map((e) => e.rel)
    for (const need of [MACHINE, STYLES, ...SCENES]) {
      expect(rels, `扫描没覆盖到 ${need}，这条闸现在是空转的`).toContain(need)
    }
    expect(entries.length).toBeGreaterThan(200)
  })

  it('S1 打字机／步进栈／动效档探测：实现体只许存在于 guidedChatMachine.ts', () => {
    const bad = findSecondShell(entries).filter((x) => !x.includes('.ar-'))
    expect(bad, `第二份外壳实现：\n${bad.join('\n')}`).toEqual([])
  })

  it('S2 两个场景都必须真的复用那一份外壳（只测"没抄"测不出"根本没复用"）', () => {
    for (const rel of SCENES) {
      const body = entries.find((e) => e.rel === rel)?.body || ''
      expect(body, `${rel} 没有 import guidedchat 的状态机`).toMatch(/from '\.\/?(\.\.)?\/?guidedchat\/guidedChatMachine'/)
      expect(body, `${rel} 没有用外壳的视图，而是自己画了一遍卡片骨架`).toMatch(/GuidedChatFrame/)
    }
    // 状态机与样式各只有一个定义点
    expect(entries.filter((e) => /export function useGuidedChat/.test(e.body)).map((e) => e.rel)).toEqual([MACHINE])
    expect(entries.filter((e) => /export const CSS_GUIDED/.test(e.body)).map((e) => e.rel)).toEqual([STYLES])
  })

  it('S3 `.ar-*` 样式只许在 guidedChatStyles.ts 声明一次', () => {
    const bad = findSecondShell(entries).filter((x) => x.includes('.ar-'))
    expect(bad, `第二份样式声明：\n${bad.join('\n')}`).toEqual([])
  })

  it('S4 反证：四种坏形态喂同一个判据函数，必须各被抓到一次', () => {
    const withSecondStack = [...entries, { rel: 'components/OtherScene.tsx', body: "const stepStack = useRef([])\nexport default function X(){return null}\n" }]
    expect(findSecondShell(withSecondStack).some((x) => x.includes('OtherScene'))).toBe(true)
    const withSecondCss = [...entries, { rel: 'components/AnotherScene.tsx', body: 'const CSS = `\n.ar-panel{width:400px}\n`\n' }]
    expect(findSecondShell(withSecondCss).some((x) => x.includes('AnotherScene'))).toBe(true)
    // ★ 真回归形态（2026-11-06 复核抓到旧判据测不到这一档）：**场景里**再内联一次动效探测
    const sceneWithOwnProbe = [...entries, { rel: SCENES[0], body: "const [reduced, setR] = useState(false)\nuseEffect(() => { setR(!!window.matchMedia('(prefers-reduced-motion: reduce)').matches) }, [])\n" }]
    expect(findSecondShell(sceneWithOwnProbe).some((x) => x.includes('prefers-reduced-motion') || x.includes('matchMedia'))).toBe(true)
    // 反向对照：同一个探测写在**外壳状态机**里是合法的（否则这条禁令会连自己一起杀掉）
    const machineProbe = entries.filter((e) => e.rel === MACHINE)
    expect(findSecondShell(machineProbe).some((x) => x.includes('matchMedia'))).toBe(false)
    // 射程还有一条**负向**：别的组件（首页/气泡/WordSwap）那些 reduced-motion 读点与外壳无关，不许被判成违规
    const outsiders = entries.filter((e) => !SCENES.includes(e.rel) && e.rel !== MACHINE)
    expect(findSecondShell(outsiders).some((x) => x.includes('matchMedia'))).toBe(false)
    // ★ 正向对照腿自己的射程：场景把 `reduced` 摘掉（＝不再用那一份探测结果）必须红
    const scene = entries.find((e) => e.rel === SCENES[1])!
    const withoutReduced = scene.body.replace('goBack, reduced, stepLabel', 'goBack, stepLabel') + '\nconst reduced = false\n'
    expect(withoutReduced).not.toBe(scene.body)
    const sceneWithoutReduced = entries.map((e) => e.rel === scene.rel ? { ...e, body: withoutReduced } : e)
    expect(findSecondShell(sceneWithoutReduced).some((x) => x.includes('复用断了'))).toBe(true)
    const optionalProbe = entries.map((e) => e.rel === scene.rel
      ? { ...e, body: e.body + "\nwindow.matchMedia?.('(prefers-reduced-motion: reduce)')\n" } : e)
    expect(findSecondShell(optionalProbe).some((x) => x.includes('自己又探'))).toBe(true)
    // H1 之前注册面自己带着这一份**具名**探测函数（旧版实测命中），抽公共件后它必须只剩一处
    const withSecondProbe = [...entries, { rel: 'components/ThirdScene.tsx', body: 'function usePrefersReducedMotion(){return false}\n' }]
    expect(findSecondShell(withSecondProbe).some((x) => x.includes('ThirdScene'))).toBe(true)
    // 而"只在注释里提到这些名字"不许被判成违规（否则说明文字会被读成实现）
    const onlyComment = [...entries, { rel: 'components/DocOnly.tsx', body: '// 这里不要再写 const stepStack = useRef 那份实现\nexport default function X(){return null}\n' }]
    expect(findSecondShell(onlyComment).some((x) => x.includes('DocOnly'))).toBe(false)
  })
})
