// ============================================================================
// scripts/i18n/patch_36_copy.mjs — #36 提示词去文件化的 12 语种同步补丁
// 职责：把即时翻译文案里「上传文件」的表述改写为纯文本口径，并删除只服务上传按钮的
//       四把键（chat.attachFile / uploadFile / sendFile / translatingFile）。
// 为什么需要脚本：i18n 是 12 语种全量词典闸门（locales.core.test.ts 逐键对齐），
//       改一个值要同步 10 份 locales/*.ts，删一个键要 12 处一起删，手工改必漏。
// 用法：node scripts/i18n/patch_36_copy.mjs   （在仓库根执行）
// 约定：只认「键在行首、双引号包裹」的 locales 格式与「单引号包裹」的 panels 格式，
//       其余行原样输出；改写后必须再跑 i18n 两个闸门验证。
// ============================================================================
import fs from "node:fs"
import path from "node:path"

const ROOT = path.resolve(process.cwd(), "frontend-react/src/i18n")
const LOCALES = ["ar", "de", "es", "fr", "ja", "ko", "pt", "ru", "th", "zh-hant"]

// 改写后的值（12 语种；这些键都没有 {placeholder}，占位符闸门天然通过）
const SET = JSON.parse(fs.readFileSync(process.argv[2], "utf8"))

// #36 删除的键（即时翻译不再有文件入口，这四把键此前只服务上传按钮/文件发送态）
const DEL = ["chat.attachFile", "chat.uploadFile", "chat.sendFile", "chat.translatingFile"]

const esc1 = (s) => s.replace(/\\/g, "\\\\").replace(/'/g, "\\'")
const esc2 = (s) => s.replace(/\\/g, "\\\\").replace(/"/g, '\\"')

function patchPanels() {
  const p = path.join(ROOT, "panels/chat.ts")
  const lines = fs.readFileSync(p, "utf8").split("\n")
  const zhEnd = lines.findIndex((l) => l.trim() === "export const en: Record<string, string> = {")
  if (zhEnd < 0) throw new Error("未找到 en 段起点，panels 结构已变化")
  let removed = 0
  let changed = 0
  const out = []
  lines.forEach((l, i) => {
    const lang = i < zhEnd ? "zh" : "en"
    const key = (l.match(/^\s*'([^']+)':/) || [])[1]
    if (key && DEL.includes(key)) { removed++; return }
    if (key && SET[key] && SET[key][lang]) {
      changed++
      out.push(`${l.match(/^\s*/)[0]}'${key}': '${esc1(SET[key][lang])}',`)
      return
    }
    out.push(l)
  })
  fs.writeFileSync(p, out.join("\n"))
  console.log(`panels/chat.ts: 改写 ${changed}、删除 ${removed}`)
}

function patchLocale(code) {
  const p = path.join(ROOT, `locales/${code}.ts`)
  const lines = fs.readFileSync(p, "utf8").split("\n")
  let removed = 0
  let changed = 0
  const out = []
  for (const l of lines) {
    const key = (l.match(/^\s*"([^"]+)":/) || [])[1]
    if (key && DEL.includes(key)) { removed++; continue }
    if (key && SET[key] && SET[key][code]) {
      changed++
      out.push(`  "${key}": "${esc2(SET[key][code])}",`)
      continue
    }
    out.push(l)
  }
  fs.writeFileSync(p, out.join("\n"))
  const total = out.filter((l) => /^\s*"[^"]+":/.test(l)).length
  console.log(`${code}: 改写 ${changed}、删除 ${removed} → ${total} 键`)
}

patchPanels()
LOCALES.forEach(patchLocale)
