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
// 四条判据：
//   S1 打字机/步进栈的实现体只许存在于 guidedChatMachine.ts（其他文件出现＝第二把尺子）；
//   S2 两个场景都必须**真的 import** 那一份（正锁；只测"没抄"测不出"根本没复用"）；
//   S3 `.ar-*` 样式表只许在 guidedChatStyles.ts 声明一次（组件里再出现同名规则即红）；
//   S4 反证：把"再抄一份打字机"与"再写一份样式"两种坏形态喂给**同一个判据函数**，必须被抓到。
//
// ⚠️ 判"有没有"之前必须先**剥注释**：本仓老坑——说明文字里出现那个名字，负向锁就把"记录了坑"读成"踩了坑"。
// ============================================================================
import * as fs from 'node:fs'
import * as path from 'node:path'
import { describe, expect, it } from 'vitest'

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
const SCENES = ['components/AiRegisterFlow.tsx', 'components/Onboarding.tsx']

/** 判据本体：输入 (相对路径, 源码) 全集，输出违规清单。真源码与合成件喂**同一个函数**（S4） */
function findSecondShell(entries: Array<{ rel: string; body: string }>): string[] {
  const bad: string[] = []
  for (const { rel, body } of entries) {
    const code = stripComments(body)
    const inMachine = rel === MACHINE
    const inStyles = rel === STYLES
    // ① 打字机/步进栈的实现体只许在状态机那一份里
    if (!inMachine) {
      for (const pat of [
        /const\s+stepStack\s*=\s*useRef/,                 // 自己再起一个步进栈
        /function\s+usePrefersReducedMotion/,             // 自己再探一次动效档
        /typingUnitOf\(\s*\w+\s*\)\s*[.;)]/,              // 自己取打字单元
        /chatTypingMs\(\s*\w+\s*\)\s*[.;)]/,              // 自己取打字速度
      ]) {
        if (pat.test(code)) bad.push(`${rel}：另起了一份引导动效实现（${pat.source.slice(0, 28)}…）`)
      }
    }
    // ② `.ar-*` 的样式规则只许在样式那一份里
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

  it('S4 反证：三种坏形态喂同一个判据函数，必须各被抓到一次', () => {
    const withSecondStack = [...entries, { rel: 'components/OtherScene.tsx', body: "const stepStack = useRef([])\nexport default function X(){return null}\n" }]
    expect(findSecondShell(withSecondStack).some((x) => x.includes('OtherScene'))).toBe(true)
    const withSecondCss = [...entries, { rel: 'components/AnotherScene.tsx', body: 'const CSS = `\n.ar-panel{width:400px}\n`\n' }]
    expect(findSecondShell(withSecondCss).some((x) => x.includes('AnotherScene'))).toBe(true)
    // H1 之前注册面自己带着这一份探测函数（2026-11-06 实测旧版命中），抽公共件后它必须只剩一处
    const withSecondProbe = [...entries, { rel: 'components/ThirdScene.tsx', body: 'function usePrefersReducedMotion(){return false}\n' }]
    expect(findSecondShell(withSecondProbe).some((x) => x.includes('ThirdScene'))).toBe(true)
    // 而"只在注释里提到这些名字"不许被判成违规（否则说明文字会被读成实现）
    const onlyComment = [...entries, { rel: 'components/DocOnly.tsx', body: '// 这里不要再写 const stepStack = useRef 那份实现\nexport default function X(){return null}\n' }]
    expect(findSecondShell(onlyComment).some((x) => x.includes('DocOnly'))).toBe(false)
  })
})
