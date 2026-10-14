// ============================================================================
// api/chatVoice.lock.test.ts — ★ 语音失败「档位」的跨语言等值锁（改造方案 §六 第 9 条，2026-10-15）
//
// 这一把锁守的缺陷不是文案难看，而是**一句中文投给俄语访客**：
//   后端出栈的是 `{error:<中文人话>, reason:<档位名>}`（`backend-go/internal/api/chat_audio.go`
//   与 `internal/fileproc/tts.go` 的 TtsReason*），界面上那句必须按 **reason** 从 12 语种词典取。
//   两侧都是字符串常量、跨语言没有共享枚举，所以三种漂移在编译期和运行时都看不见：
//   ① 后端改掉/删掉一档的名字；② 前端留了一支后端永不命中的死分支；③ 前端引用的词典键拼错
//   （拼错不会崩，只会在客户屏幕上**回显键名字面**——本仓 t() 的既定行为）。
//   ⇒ 只能靠这把跨语言锁。
//
// 判据（都是**等值**，不是"前端不许更少"这种单向锁）：
//   V1 射程腿：后端那组 TtsReason 常量解析非空且含两档关键名——解析不到时报的是
//      「锚点漂了、闸门已失效」，不是"前端多写了分支"（与 translateFormats.lock.test.ts 同一口径）。
//   V2 死分支：voiceFail.ts 里 `case '<档位>'` 的字面集合 ⊆ 后端档位集合。
//   V3 必须分开的两档：unsupported_lang（这一语种没有语音，客户无事可做）与
//      too_long（客户动作＝分段）各占一支——把两者混成一句是本条缺陷的另一半。
//   V4 键在位：voiceFail.ts 引用的每个词典键在 zh／en 两份面板词典里有非空值（十语种全量覆盖
//      由 locales.core.test.ts 逐键守，本条只管"这个模块引用的键不是我编的"）。
//   V5 负向：MessageBubble／ChatWindow 的语音失败分支不许再取异常对象的 message 当文案；
//      **先剥注释再判**——本批新写的注释里就有「message 只留给日志」这句，不剥会被自己的说明判红。
//
// 反证（末条）：把三种坏形态喂给**同一组判据函数**，必须各自被抓；干净文本作对照必须全绿。
//
// 运行：npx vitest run src/api/chatVoice.lock.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { en, zh } from '../i18n/panels/chat'
import { VOICE_FAIL_KEYS } from '@/lib/voiceFail'

// ROOT 是 **frontend-react 包根**（不是仓库根），所以后端路径一律带 `../`
// （与 translateFormats.lock.test.ts 同口径；少这一个 `../` 得到的是 ENOENT，整文件一条不跑）。
const ROOT = fileURLToPath(new URL('../../', import.meta.url))
const read = (rel: string): string => readFileSync(resolve(ROOT, rel), 'utf-8')

// stripComments 剥掉 // 行注释与 /* */ 块注释：两侧（Go／TS）判据都只吃代码行。
// 注释里允许出现档位名与 `e.message` 字样（那是说明与事故记录），留着就会把负向腿判红、把正向腿判绿。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

// backendTtsReasons 解析后端 fileproc/tts.go 里那一组 `TtsReasonXxx = "…"` 常量的**值**。
// 名字与值都留在正则里：改名式破坏（常量改名、值不动）与换值（值改了、前端没跟）都要当场红。
function backendTtsReasons(goSrc: string): string[] {
  const body = stripComments(goSrc)
  const out: string[] = []
  const re = /\bTtsReason\w+\s*=\s*"([^"]+)"/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body)) !== null) out.push(m[1])
  return Array.from(new Set(out))
}

// frontendCaseReasons 解析 voiceFail.ts 里 `case '<档位>':` 的字面集合。
// 只认单引号档位名（本仓 TS 的字面量风格）：写法漂移会让这一支从 V2 的读数里漏掉，
// 所以反证里专门喂了一条"改写形态"，证明判据真的在吃这一份源码。
function frontendCaseReasons(tsSrc: string): string[] {
  const body = stripComments(tsSrc)
  const out: string[] = []
  const re = /\bcase\s*'([a-z_]+)'/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body)) !== null) out.push(m[1])
  return Array.from(new Set(out))
}

// deadBranches 前端有、后端没有的档位（V2 的读数）。
function deadBranches(backend: string[], frontend: string[]): string[] {
  return frontend.filter((r) => !backend.includes(r))
}

// usesRawErrorMessage 界面上那句是否还直接取异常对象的 message（V5 的判据）。
function usesRawErrorMessage(jsxSrc: string): boolean {
  const body = stripComments(jsxSrc)
  return /\(\s*e\s+as\s+Error\s*\)\??\.message\b/.test(body) || /\be\.message\b/.test(body)
}

describe('语音失败档位跨语言等值锁（改造方案 §六 第 9 条）', () => {
  const goSrc = read('../backend-go/internal/fileproc/tts.go')
  const failSrc = read('src/lib/voiceFail.ts')
  const backend = backendTtsReasons(goSrc)
  const frontend = frontendCaseReasons(failSrc)

  it('V1 射程腿：后端档位常量解析非空（解析不到＝锚点漂了、闸门已失效，不许读成"没问题"）', () => {
    expect(backend.length, 'TtsReason* 常量解析为空 ⇒ 本闸门失明，去核 tts.go 的常量块形状').toBeGreaterThanOrEqual(5)
    expect(backend).toContain('unsupported_lang')
    expect(backend).toContain('too_long')
    expect(backend).toContain('capability_missing')
  })

  it('V2 前端不许有后端永不发出的死分支', () => {
    expect(deadBranches(backend, frontend)).toEqual([])
  })

  it('V3「这一语种没语音」与「译文过长」必须各占一支（混成一句＝本条缺陷的另一半）', () => {
    expect(frontend).toContain('unsupported_lang')
    expect(frontend).toContain('too_long')
  })

  it('V4 voiceFail.ts 引用的词典键在 zh／en 两份面板词典里都有值（拼错键＝屏幕上回显键名）', () => {
    expect(VOICE_FAIL_KEYS.length).toBeGreaterThanOrEqual(3)
    for (const k of VOICE_FAIL_KEYS) {
      expect(zh[k], `zh 缺键 ${k}`).toBeTruthy()
      expect(en[k], `en 缺键 ${k}`).toBeTruthy()
    }
    // 引用侧自证：判据读的键清单必须真的等于 voiceFail.ts 里 t('…') 那几支，
    // 否则清单被改成"随便写三个在位的键"，这条腿就只是对着词典空转。
    const referenced = Array.from(failSrc.matchAll(/\bt\('([^']+)'\)/g)).map((m) => m[1])
    expect(referenced.sort()).toEqual([...VOICE_FAIL_KEYS].sort())
  })

  it('V5 界面那句取自词典，不许直取异常 message（后端那句是给运维看的中文）', () => {
    expect(usesRawErrorMessage(read('src/components/MessageBubble.tsx'))).toBe(false)
    expect(usesRawErrorMessage(read('src/components/ChatWindow.tsx'))).toBe(false)
  })

  it('反证：四种坏形态喂给同一组判据，必须各自被抓；干净文本作对照必须全绿', () => {
    // ① 前端多一支后端根本不存在的档位 ⇒ V2 抓
    expect(deadBranches(backend, frontend)).toEqual([]) // 干净对照
    const mutated = frontendCaseReasons(failSrc.replace("case 'too_long':", "case 'quota_exceeded':"))
    expect(deadBranches(backend, mutated)).toEqual(['quota_exceeded'])
    // ② 摘掉 too_long 那一支（两档混成一句）⇒ V3 抓
    const merged = frontendCaseReasons(
      failSrc.replace(/case 'too_long':\s*\n\s*return t\('chat\.voiceTooLong'\)/, ''),
    )
    expect(merged).not.toContain('too_long')
    // ③ 界面退回 e.message ⇒ V5 抓（造"改造前形态"的靶子，不吃真文件）
    expect(usesRawErrorMessage('function f(){ toastWarn((e as Error)?.message) }')).toBe(true)
    expect(usesRawErrorMessage('function f(){ toastWarn(voiceFailText(e)) }')).toBe(false)
    // ④ 后端那组常量整体注释掉 ⇒ V1 必须先报「解析为空」，而不是让 V2 冒充成"前端多写了"
    const commentedGo = goSrc.replace(/\bTtsReason\w+\s*=\s*"[^"]+"/g, (s) => '// ' + s)
    expect(backendTtsReasons(commentedGo)).toEqual([])
  })
})
