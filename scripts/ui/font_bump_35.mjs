// ============================================================================
// scripts/ui/font_bump_35.mjs — 需求 #35 一次性字号/对比度上调脚本（可复核）
// ----------------------------------------------------------------------------
// 背景：2026-09-21 用户反馈「登录后前后台字体偏小、次级文字对比度偏低」。
// theme.css 的页面级覆写已覆盖组件库（.lc-*）与本仓自有类，但 tsx 里还有约 300 处
// **内联样式**（style={{ fontSize: 12 }}）——内联优先级高于任何类选择器，只能就地改。
//
// 规则（只动这两类，且都限定在登录后产品界面）：
//   1) 字号：数值 10 / 10.5 / 11 / 11.5 / 12 / 12.5 / 13 → 各 +1（14 及以上不动，
//      避免把标题/数字卡再放大破坏层级）；同时处理模板字符串里的 `font-size:NNpx`。
//   2) 写死的灰字：#9AA0AA / #878D95 / #7A828E / #8A9099 → 改走语义令牌
//      var(--lc-text-2/3/4)，让对比度以后只在一处可调（theme.css §十）。
//
// 安全边界：跳过 src/ui/langcross/**（交付包冻结）、*.test.tsx、落地页/定价页等
// 未登录页面；改完做「结构等价校验」——把新旧两行的数字与十六进制色值抹掉后必须
// 完全一致，任何其它字符变化（吞 ★、粘 token、缩进塌陷）都会让脚本非零退出。
// 用法：node scripts/ui/font_bump_35.mjs [--apply]（缺省 dry-run 只报统计）
// ============================================================================
import { readFileSync, writeFileSync } from 'node:fs'
import { execSync } from 'node:child_process'

const APPLY = process.argv.includes('--apply')
const FRONTEND = 'frontend-react/src'

// 组件库冻结 + 测试文件 + 未登录营销页（落地页/定价页/留言表单/页脚/首页演示卡）
const SKIP = [
  /^frontend-react\/src\/ui\/langcross\//,
  /\.test\.tsx?$/,
  /\/(Landing|PricingPage|LeadForm|SiteFooter|HeroDemo|Login)\.tsx$/,
]

// 用 find 而非 git ls-files：本批新建（尚未 add）的面板文件同样要跟上字号口径
const files = execSync(`find ${FRONTEND} \\( -name '*.tsx' -o -name '*.ts' \\) -print`, { encoding: 'utf8' })
  .split('\n').filter(Boolean).filter((f) => !SKIP.some((re) => re.test(f)))

// 灰字 → 语义令牌（对 #000 底：text-2 8.78:1 / text-3 7.79:1 / text-4 6.80:1）
// 只替换 color 上下文，避免把 background/border 上的同一个灰色错接成语义色令牌；
// 也不碰 `var(--lc-text-3, #9AA0AA)` 里的兜底值（前面不是紧邻 color:，天然不匹配）。
const GRAY2TOKEN = {
  '#9AA0AA': 'var(--lc-text-2)',
  '#878D95': 'var(--lc-text-3)',
  '#7A828E': 'var(--lc-text-4)',
  '#8A9099': 'var(--lc-text-3)',
}

// 抹掉所有数字、十六进制色值与语义色令牌，用于结构等价校验
const skeleton = (s) => s
  .replace(/var\(--lc-text-[234]\)/g, '#C')
  .replace(/#[0-9a-fA-F]{6}\b/g, '#C')
  .replace(/\d+(?:\.\d+)?/g, 'N')

let fontHits = 0, colorHits = 0, changedFiles = 0, mismatch = 0
for (const f of files) {
  const src = readFileSync(f, 'utf8')
  const out = []
  for (const line of src.split('\n')) {
    let l = line
    // 1) 内联 fontSize / 模板串里的 font-size，仅 10–13(.5) 档 +1
    l = l.replace(/(fontSize:\s*)(1[0-3](?:\.5)?)(?![\d.])/g, (_m, k, v) => k + (Number(v) + 1))
    l = l.replace(/(font-size:\s*)(1[0-3](?:\.5)?)(px)(?![\d.])/g, (_m, k, v, u) => k + (Number(v) + 1) + u)
    // 2) 写死灰字 → 令牌（保留原有引号形态：JSX 里是 'var(--lc-text-2)'，CSS 串里裸写）
    for (const [hex, tok] of Object.entries(GRAY2TOKEN)) {
      const re = new RegExp(`(color:\\s*)(['"]?)${hex}\\2`, 'g')
      l = l.replace(re, (_m, k, q) => { colorHits++; return k + q + tok + q })
    }
    if (l !== line) {
      fontHits += (line.match(/fontSize:\s*1[0-3](?:\.5)?(?![\d.])|font-size:\s*1[0-3](?:\.5)?px(?![\d.])/g) || []).length
      if (skeleton(l) !== skeleton(line)) {
        mismatch++
        console.error(`结构变化无法解释（拒绝落盘）: ${f}\n  old: ${line}\n  new: ${l}`)
        process.exitCode = 1
      }
    }
    out.push(l)
  }
  if (out.join('\n') !== src) {
    changedFiles++
    if (APPLY) writeFileSync(f, out.join('\n'))
  }
}
console.log(`${APPLY ? '已改写' : '试运行'}：文件 ${changedFiles} 个，字号 ${fontHits} 处，灰字转令牌 ${colorHits} 处，异常 ${mismatch} 处`)
