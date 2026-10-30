// ============================================================================
// api/translateFormats.lock.test.ts — ★ 建单准入「一把尺子」的跨语言等值锁（2026-10-14 用户令 #43）
//
// 为什么前端也要进这条锁：
//   后端那份格式/体积清单原先在 Go 里有四份副本（upload.go / tickets.go / api_openapi_tasks.go /
//   engine/file.go），本批已收成 `backend-go/internal/fileproc/formats.go` 一处。
//   前端这一份（TRANSLATE_FILE_EXTS / TEXT_DELIVERY_ONLY_EXTS / TRANSLATE_FILE_MAX_BYTES）是**第五份**，
//   跨语言、没法共享常量，所以它只能靠"锁住与那一个事实源等值"来保证不漂。
//   漂移的后果不是不好看，是**客户在文件选择框里选得中、后端一句"不支持的格式"拒掉**
//   （或反过来：前端拦得住、后端放过一个执行口跑不动的格式）。
//   历史上 F-12「三口径打架」、§一·11「三把尺子」都是同一族：同一件事多处各写一份。
//
// 三条判据（都是**等值**，不是"前端不得更宽"这种单向锁——单向锁会把设计一路推离真值，本仓已为此返工过全站 50 文件）：
//   P1 还原模式 12 项逐字等值；
//   P2 纯文案模式独占的 anydoc 格式逐字等值；
//   P3 体积上限等值（Go 那句 `40 << 20` 现读现算，不抄数字）。
//
// 两段防空转（本仓纪律：负向/等值锁必须证明射程真的在）：
//   ① 解析射程非空：Go 侧三个锚点任一解析不到（改名、挪文件、块结构变了）当场红，
//      绝不允许"解析出空集合 → 与前端比出'互差不为 0'"这种把闸门失效读成配置问题；
//      实际写法是解析为空即单独报「射程已失效」，与"内容不等值"分成两档读数。
//   ② 反证：把三种坏形态（前端少一项 / 前端多一项 / 体积上限差一档）喂给**同一个判据函数**，
//      必须各自被抓；再喂一次干净文本作对照，必须全绿。
//
// 运行：npx vitest run src/api/translateFormats.lock.test.ts
// ============================================================================
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { AUDIO_EXTS, IMAGE_EXTS, RETIRED_TEXT_EXTS, TEXT_DELIVERY_ONLY_EXTS, TRANSLATE_FILE_EXTS, TRANSLATE_FILE_MAX_BYTES, validateTranslateFile } from './translate'

// 跨出包根读后端源码：必须 resolve()，裸拼 `../backend-go/...` 不折叠 `..` 会直接 ENOENT
// （与 BrandTagline.lock.test.ts、readability.test.ts 同一口径）
// ⚠️ ROOT 是 **frontend-react 包根**（不是仓库根），所以下面每个后端路径都带 `../`；
// 少这一个 `../` 得到的是 ENOENT——而且是 describe 体里就抛、整文件一条用例都不跑的那种红。
const ROOT = fileURLToPath(new URL('../../', import.meta.url)) // frontend-react 包根
// 读**内容**而不是读路径：本用例三处都直接把返回值喂给 stripGoComments/正则，
// 写成 `resolve(...)` 会静默把路径当源码扫——锚点解析不到，于是每条负向腿都"恒干净"（假绿）。
// P0 那条射程非空断言就是专门用来抓这一族的，改这里时别把它摘掉。
const read = (p: string) => readFileSync(resolve(ROOT, p), 'utf-8')

// stripGoComments 丢掉 Go 源码里的注释，只留真会被编译的那一份文本。
// 为什么必须先剥：formats.go 的头注释里逐字列着四份旧副本的名字、也提到过格式清单，
// 不剥注释的话"锚点解析得到"这件事可能是注释给的（注释冒充实现＝假绿，本批前一轮真踩过）。
function stripGoComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .map((l) => l.replace(/\/\/.*$/, ''))
    .filter((l) => !/^\s*\/\//.test(l))
    .join('\n')
}

// goQuotedInBlock 从 `anchor` 之后第一个 `{` 到配对 `}` 的块里，按顺序取出所有双引号字符串。
// 只做字面切块不跑 AST：这条锁要的正是"字面写法"（与后端 upload_pagecount_gate_test.go 同一取舍）。
function goQuotedInBlock(src: string, anchor: string): string[] {
  const i = src.indexOf(anchor)
  if (i < 0) return []
  const open = src.indexOf('{', i)
  if (open < 0) return []
  const close = src.indexOf('}', open)
  if (close < 0) return []
  const block = src.slice(open, close)
  return [...block.matchAll(/"([^"]+)"/g)].map((m) => m[1])
}

// goFormatSetBody 取 `var <setName> … []string{"…", …}` 那一串。
// ★ 为什么不能用 goQuotedInBlock：那种写法形如 `var imageFormatSet = func() map[string]bool {`
//   ——"锚点后第一个 `{`" 命中的是返回类型那一对空花括号 `map[string]bool{}`，
//   切出来的块里一个字符串都没有 ⇒ 解析结果恒为空数组。
//   **空数组会让"左右相等"直接成立**，这正是本仓记过的假绿形态，所以这里改锚 `[]string{`。
function goFormatSetBody(src: string, setName: string): string[] {
  const i = src.indexOf('var ' + setName)
  if (i < 0) return []
  const m = src.slice(i, i + 600).match(/\[\]string\{([^}]*)\}/)
  if (!m) return []
  return [...m[1].matchAll(/"([^"]+)"/g)].map((x) => x[1])
}

// goShiftedBytes 读 `anchor = <n> << 20` 这一行并**算出字节数**（不写死 41943040）。
// 写死就是把别人的单位换算抄第二份：Go 侧改成 << 10 那天，这条锁要能当场红，而不是跟着过时。
function goShiftedBytes(src: string, anchor: string): number | null {
  const re = new RegExp(anchor + '\\s*(?:int64\\s+)?=\\s*(\\d+)\\s*<<\\s*(\\d+)')
  const m = src.match(re)
  if (!m) return null
  return Number(m[1]) * 2 ** Number(m[2])
}

/**
 * formatsViolation 是唯一的判据本体：正例、反例都走它（判据只有一份，两侧不可能各判各的）。
 * 返回第一条问题描述；全部等值回空串。
 */
function formatsViolation(input: {
  goRestore: string[]
  goAnydoc: string[]
  goMaxBytes: number | null
  tsRestore: readonly string[]
  tsAnydoc: readonly string[]
  tsMaxBytes: number
}): string {
  // 射程腿先行：解析结果为空 ⇒ 锚点漂了，闸门本身失效，不许继续做等值比较
  if (input.goRestore.length < 12) {
    return `Go 侧还原模式清单只解析到 ${input.goRestore.length} 项（<12）⇒ formats.go 的锚点/结构已变，本闸门射程已失效`
  }
  if (input.goAnydoc.length < 16) {
    return `Go 侧 anydoc 独占清单只解析到 ${input.goAnydoc.length} 项（<16）⇒ anydoc.go 的锚点/结构已变，本闸门射程已失效`
  }
  if (input.goMaxBytes === null) {
    return '没读到 Go 侧 MaxUploadBytes 的 `<n> << <bits>` 写法 ⇒ 体积那一维扫不到（改成语法糖就得同步改这条锚）'
  }
  const diff = (a: readonly string[], b: readonly string[]) => {
    const setB = new Set(b.map((s) => s.toLowerCase()))
    const setA = new Set(a.map((s) => s.toLowerCase()))
    return {
      onlyGo: a.filter((s) => !setB.has(s.toLowerCase())),
      onlyTs: b.filter((s) => !setA.has(s.toLowerCase())),
    }
  }
  const r = diff(input.goRestore, input.tsRestore)
  if (r.onlyGo.length || r.onlyTs.length) {
    return `还原模式格式清单不等值：后端独有 [${r.onlyGo.join(' ')}]、前端独有 [${r.onlyTs.join(' ')}]`
  }
  const t = diff(input.goAnydoc, input.tsAnydoc)
  if (t.onlyGo.length || t.onlyTs.length) {
    return `纯文案模式独占格式清单不等值：后端独有 [${t.onlyGo.join(' ')}]、前端独有 [${t.onlyTs.join(' ')}]`
  }
  if (input.tsMaxBytes !== input.goMaxBytes) {
    return `体积上限不等值：后端 ${input.goMaxBytes} 字节、前端 ${input.tsMaxBytes} 字节`
  }
  return ''
}

/**
 * inlineFormatListLine 找出源码里"内联准入清单"的两种形状，命中返回那一行（截 120 字便于读报错）：
 *   ① 同一行并列出现 ≥3 个 Office/PDF 那四件（旧②④那种一行多项的字面量）；
 *   ② `".docx": true` 这种 map 条目形状（旧①那种一行一键）——
 *      .docx 只在准入清单里出现，写回格式 switch 用的是 `case ".docx":` 不带 `: true`，不会误伤。
 *
 * 为什么要区分形状而不是"文件里出现过 .docx"：engine/file.go 的写回 switch（893~899 行）
 * 与交付形态分档（`.srt", ".vtt", ".json", ".yaml", ".yml"`）都是合法逐格式代码，
 * 按"出现过"判会把它们误伤成副本——判红一次，下一位就把这条锁摘掉了（闸门钝化比红灯更坏）。
 */
function inlineFormatListLine(src: string): string {
  const four = ['.docx', '.xlsx', '.pptx', '.pdf']
  for (const line of src.split('\n')) {
    const n = four.filter((e) => line.includes(`"${e}"`)).length
    if (n >= 3) return line.trim().slice(0, 120)
    if (/"\.docx"\s*:\s*true/.test(line)) return line.trim().slice(0, 120)
  }
  return ''
}

// stripTsComments 丢掉 TS 源码里的注释，只留真会被编译的那一份文本（与 stripGoComments 同的理由：
// 注释里提过格式清单的文件不止一处，不剥就会出现"注释冒充实现"的假绿）。
// ★ 为什么 `//` 前要排除 `:`／引号类字符：词典里 `'https://…'` 这种 URL 也是字面值，
//   一刀切会把 URL 拦腰砍断，把密钥/链接类的文案句子撕成两半（这会让判据读到的东西失真）。
function stripTsComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .map((l) => l.replace(/(^|[^:"'`])\/\/.*$/, '$1'))
    .join('\n')
}

/**
 * hintedFormats 从对外那句格式文案里抽出**格式名列表**（去点号、去体积尾巴）。
 * 只认冒号之后、圆括号之外的纯字母数字 token——多语种文案的导语各不相同，
 * 但「 <名字> / <名字> / … （≤N MB）」这一段形状是十二份词典统一的，锚它就够了。
 * 返回空数组表示"文案里根本没有格式段"（＝本腿失明，由调用方的射程正锁当场红）。
 */
function hintedFormats(text: string): string[] {
  const noSize = text.replace(/[（(][^）)]*[）)]/g, '')
  // 先按 ASCII/全角冒号切一刀拿掉导语；找不到冒号＝不是这句文案的形状 ⇒ 空数组。
  const m = noSize.match(/[：:]\s*([\s\S]*)$/)
  if (!m) return []
  return m[1]
    .split('/')
    .map((s) => s.trim())
    .filter((s) => /^[a-zA-Z0-9]+$/.test(s))
}

describe('建单准入一把尺子：前端清单必须与后端 formats.go 等值（#43）', () => {
  const formatsSrc = stripGoComments(read('../backend-go/internal/fileproc/formats.go'))
  const anydocSrc = stripGoComments(read('../backend-go/internal/fileproc/anydoc.go'))

  const goRestore = goQuotedInBlock(formatsSrc, 'var TranslateFormatOrder = []string')
  const goAnydoc = goQuotedInBlock(anydocSrc, 'var AnydocFormats = map[string]bool')
  const goMaxBytes = goShiftedBytes(formatsSrc, 'MaxUploadBytes')

  it('P0 解析射程非空（锚点漂了要当场点名，不许冒充"清单不等值"）', () => {
    expect(goRestore.length, 'Go 还原模式清单解析结果').toBeGreaterThanOrEqual(12)
    expect(goAnydoc.length, 'Go anydoc 清单解析结果').toBeGreaterThanOrEqual(16)
    expect(goMaxBytes, 'Go 体积上限读数').not.toBeNull()
  })

  it('P1/P2/P3 三份读数与前端逐字等值', () => {
    const v = formatsViolation({
      goRestore,
      goAnydoc,
      goMaxBytes,
      tsRestore: TRANSLATE_FILE_EXTS,
      tsAnydoc: TEXT_DELIVERY_ONLY_EXTS,
      tsMaxBytes: TRANSLATE_FILE_MAX_BYTES,
    })
    expect(v, '').toBe('')
  })

  it('P5 对客户的那句格式说明（tk.fileHint，十二份词典）必须与准入清单逐字等值', () => {
    // 为什么单独一条：accept 属性管的是"选不选得中"，这句文案管的是"客不觉得平台收不收"。
    // 本批把图片八档放进来时，文案侧一处没跟——那还是**只写英文格式名、十二语种同形**的低危场景；
    // 真正危险的是「文案少列一类，客户就不上传一类」和「文案多列一类，建单被后端拒」。
    // 这一族的坏形态与 §一·5「对外报价只有一个事实源」同形：对外句子抄了第二份清单，迟早打架。
    const files = [
      'src/i18n/dicts.zh.ts',
      'src/i18n/dicts.en.ts',
      ...['de', 'ar', 'es', 'fr', 'th', 'ru', 'zh-hant', 'pt', 'ko', 'ja'].map((c) => `src/i18n/locales/${c}.ts`),
    ]
    expect(files.length, '词典份数应为 12（zh/en + 十语种）').toBe(12)
    const want = TRANSLATE_FILE_EXTS.map((e) => e.replace(/^\./, ''))
    const maxMB = String(TRANSLATE_FILE_MAX_BYTES / 1024 / 1024)
    const bad: string[] = []
    for (const f of files) {
      const src = stripTsComments(read(f))
      const m = src.match(/['"]tk\.fileHint['"]\s*:\s*(['"])((?:\\.|(?!\1)[^\\])*)\1/)
      if (!m) {
        bad.push(`${f}：取不到 tk.fileHint（键改名/换行写法变了＝本腿失明）`)
        continue
      }
      const got = hintedFormats(m[2])
      // 射程正锁：解析不出格式段＝探测器坏了，不能当成"文案干净"
      expect(got.length, `${f} 只解析到 ${got.length} 个格式名，判据射程可疑：${JSON.stringify(m[2])}`).toBeGreaterThanOrEqual(12)
      const miss = want.filter((x) => !got.includes(x))
      const extra = got.filter((x) => !want.includes(x))
      if (miss.length || extra.length) {
        bad.push(`${f}：文案少列 ${JSON.stringify(miss)}、多列 ${JSON.stringify(extra)}（准入清单＝fileproc/formats.go）`)
      }
      // 体积上限同一条句子：后端抬档那天，文案里那个数字也必须跟着（写死 40＝下一档对外错报）
      if (!m[2].includes(maxMB)) bad.push(`${f}：文案里的体积上限不是当前那一档 ${maxMB}MB：${JSON.stringify(m[2])}`)
    }
    expect(bad, `对外格式文案与准入清单不等值：\n${bad.join('\n')}`).toEqual([])

    // 反证（同一判据函数 hintedFormats，两侧都要有射程）：
    // ① 少列一档（就是本批真实发生过的那一形态：后端加了图片、文案没跟上）
    // ⚠️ 变量名别写成 `const少了`：紧贴关键字的中文会被词法当成一个标识符 `const少了`
    //    （ JS 允许中文做变量名，但这里缺的那一个空格会让它整句解析失败）。
    const fewNow = hintedFormats('点击选择文件：docx / xlsx / pptx / pdf / txt / csv / srt / vtt / md / json / yaml / yml（≤40MB）')
    expect(want.filter((x) => !fewNow.includes(x)).length, '反证①：文案少列图片档时必须能数出缺了几档').toBeGreaterThan(0)
    // ② 多列一档（文案承诺一个后端收不进来的格式）
    const moreNow = hintedFormats('点击选择文件：docx / xlsx / pptx / pdf / txt / csv / md / png / jpg / jpeg / bmp / tif / tiff / webp / heic / mp3 / dwg（≤40MB）')
    expect(moreNow.filter((x) => !want.includes(x)), '反证②：多列的 dwg 必须被抓').toEqual(['dwg'])
    // ③ 解析落空（文案整个改了写法）＝返回空数组，上面那条射程正锁会红，不许静默合规
    expect(hintedFormats('点击选择文件'), '反证③：文案里没有格式段时必须解析为空').toEqual([])
  })

  it('P6 素材类划分（IMAGE_EXTS + AUDIO_EXTS + 文档类）必须恰好等于准入清单', () => {
    // 为什么单列：UI 靠这两份来选话术（图片给「译后图＋译文 .md」，
    // 音频给「同容器配音＋译文 .md」——★ 2026-10-30 改档后字幕 .srt 已不在产物清单里）。
    // 它们是**准入口径的派生**，不是第三份独立清单——一旦有人给某一类加了一项却没进
    // TRANSLATE_FILE_EXTS（或反过来），表现是「客户端提示与实际产物不符」，而 accept 那一侧
    // 完全发现不了（它只认并集）。用「并起来恰好相等」判，双向都堵。
    const img: readonly string[] = IMAGE_EXTS
    const aud: readonly string[] = AUDIO_EXTS
    const all: readonly string[] = TRANSLATE_FILE_EXTS
    // 三者两两不得相交（同一扩展名不能既是图片又是音频）
    const inter = img.filter((e) => aud.includes(e))
    expect(inter, `图片类与音频类不得相交：${inter.join(' ')}`).toEqual([])
    // 素材类必须是准入清单的**子集**（不许出现"UI 认、后端不收"的扩展名）
    const orphan = [...img, ...aud].filter((e) => !all.includes(e))
    expect(orphan, `素材类里出现了准入清单外的格式：${orphan.join(' ')}`).toEqual([])
    // 反向：准入清单里剩下的恰好是文档类（不是素材类），且数量为一个整数
    const docish = all.filter((e) => !img.includes(e) && !aud.includes(e))
    expect(docish.length + img.length + aud.length, '三类并起来必须恰好等于准入清单').toBe(all.length)
    expect(docish.length, '文档类不得为空（划分退化了）').toBeGreaterThan(0)
    expect(img.length, '图片类不得为空').toBeGreaterThan(0)
    expect(aud.length, '音频类不得为空').toBeGreaterThan(0)
    // 正锁：后端 formats.go 那两份 set 与前端同类一致（跨语言的第四层确认）
    expect(goFormatSetBody(formatsSrc, 'imageFormatSet'), 'formats.go 的 imageFormatSet 必须与前端同类一致').toEqual([...IMAGE_EXTS])
    expect(goFormatSetBody(formatsSrc, 'audioFormatSet'), 'formats.go 的 audioFormatSet 必须与前端同类一致').toEqual([...AUDIO_EXTS])
  })

  it('P3b 后端那道闸不是纸面数字：internal/api 的三处上限确实委托到同一常量', () => {
    // 收拢前这里是三份 `40 << 20` 字面量。本腿判"三份变一份委托"这件事真的落地了，
    // 而不是只把注释改成"与 fileproc 对齐"（注释不会红灯，正是这一族长出来的地方）。
    const uploadSrc = stripGoComments(read('../backend-go/internal/api/upload.go'))
    const openapiSrc = stripGoComments(read('../backend-go/internal/api/api_openapi_tasks.go'))
    for (const [name, src] of [['upload.go', uploadSrc], ['api_openapi_tasks.go', openapiSrc]] as const) {
      expect(src.includes('fileproc.MaxUploadBytes'), `${name} 必须委托 fileproc.MaxUploadBytes`).toBe(true)
      expect(/\b40\s*<<\s*20\b/.test(src), `${name} 里不许再留第二份 40<<20 字面量`).toBe(false)
    }
  })

  it('P4 Go 侧四份副本已收成一份：不许再有内联的准入清单字面量', () => {
    // 判据锚的是"同一行里并列出现 ≥3 个 Office/PDF 准入扩展名"这一**清单形状**。
    // 为什么不按"这个文件里出现过 .docx"判：engine/file.go 里那些逐格式 switch 分支
    //（`case ".docx":` 这种一个 case 一个格式的写法）与交付形态分档都是**合法的逐格式代码**，
    // 按"出现过"判会把它们误伤成副本——判红一次，下一位就把这条锁摘掉了（闸门钝化比红灯更坏）。
    const files = [
      '../backend-go/internal/api/upload.go',
      '../backend-go/internal/api/tickets.go',
      '../backend-go/internal/api/api_openapi_tasks.go',
      '../backend-go/internal/engine/file.go',
    ]
    for (const f of files) {
      const src = stripGoComments(read(f))
      const bad = inlineFormatListLine(src)
      expect(bad, `${f} 里还留着内联准入清单（唯一事实源是 fileproc/formats.go）：${bad}`).toBe('')
    }
    // 反证 ①：一行四项那种副本必须被抓
    const fake = '\tallowed := map[string]bool{\n\t\t".docx": true, ".xlsx": true, ".pptx": true, ".pdf": true,\n\t}\n'
    expect(inlineFormatListLine(fake), '反证①：一行多项副本必须被抓').not.toBe('')
    // 反证 ②：一行一键的 map 那种副本（旧 upload.go 的形态）也必须被抓
    expect(inlineFormatListLine('\t".docx": true,\n'), '反证②：逐行 map 副本必须被抓').not.toBe('')
    // 对照腿 A：逐格式 switch 的真实写法不许被误判
    const switchShape = 'case ".docx":\n\t\t\treturn inplace\n\t\tcase ".pptx":\n\t\t\treturn inplace'
    expect(inlineFormatListLine(switchShape), '对照A：逐格式 switch 不算清单副本').toBe('')
    // 对照腿 B：**唯一事实源自己**必须被同一个探测器认出来——
    // 否则 P4 那四条"都干净"可能只是因为探测器坏了（负向腿恒零命中必须配正锁，本仓 §一·5 反复强调）
    expect(inlineFormatListLine(formatsSrc), '对照B：formats.go 那份清单必须落在探测器射程内').not.toBe('')
  })

  it('反证：三种坏形态各被抓一次，干净文本作对照必须全绿', () => {
    const clean = {
      goRestore,
      goAnydoc,
      goMaxBytes,
      tsRestore: TRANSLATE_FILE_EXTS,
      tsAnydoc: TEXT_DELIVERY_ONLY_EXTS,
      tsMaxBytes: TRANSLATE_FILE_MAX_BYTES,
    }
    // 对照腿：先证明判据在真源码上是绿的（否则下面的三条"必红"可能只是判据恒红）
    expect(formatsViolation(clean), '干净对照').toBe('')

    // R1 前端少一项（后端加了格式、前端 accept 没跟上 → 客户选不到）
    expect(
      formatsViolation({ ...clean, tsRestore: TRANSLATE_FILE_EXTS.slice(0, 11) }),
      'R1 前端少一项必须红',
    ).not.toBe('')
    // R2 前端多一项（前端放行一个后端不支持的格式 → 建单被拒）
    expect(
      formatsViolation({ ...clean, tsAnydoc: [...TEXT_DELIVERY_ONLY_EXTS, '.dwg'] }),
      'R2 前端多一项必须红',
    ).not.toBe('')
    // R3 体积上限差一档（后端抬到 80MiB、前端还按 40MiB 拦 = 客户被自己的选择框挡住）
    expect(
      formatsViolation({ ...clean, tsMaxBytes: 20 * 1024 * 1024 }),
      'R3 体积上限不等值必须红',
    ).not.toBe('')
    // R4 锚点漂了（后端把清单改名 → 解析为空）：必须报"射程失效"而不是"等值通过"
    const drifted = formatsViolation({ ...clean, goRestore: [] })
    expect(drifted, 'R4 射程失效必须红').not.toBe('')
    expect(drifted).toContain('射程已失效')
  })

  it('P7 撤出的五种纯文本容器：清单、出路句与前端校验三件必须同源（★ 2026-10-30 C-10/C11）', () => {
    // 这条锁管的是"撤格式之后，拒绝还得给得出路"这一整族：
    // 客户手上 .srt/.json 本来就该走文本翻译，只回一句「不支持」等于把快路藏起来。
    // 三件（Go 名单 / 前端名单 / 那句文案）任何一件单独改，都会在这里红。
    const retiredFromGo = goQuotedInBlock(formatsSrc, 'var retiredTextContainers = map[string]bool')
    expect(retiredFromGo.length, 'Go 侧 retiredTextContainers 解析为空 ⇒ 锚点漂了，本腿失明').toBeGreaterThanOrEqual(5)
    expect([...RETIRED_TEXT_EXTS].sort(), '前端撤除清单必须与 Go 那份逐字等值')
      .toEqual([...retiredFromGo].sort())

    // 撤除清单必须是准入清单的**补集**（两边都留着＝accept 选得中、后端拒）
    const overlap = RETIRED_TEXT_EXTS.filter((e) => TRANSLATE_FILE_EXTS.includes(e as (typeof TRANSLATE_FILE_EXTS)[number]))
    expect(overlap, `撤除格式仍在准入清单里：${overlap.join(' ')}`).toEqual([])

    // 出路句跨语言逐字等值：前端常量 == Go 常量 == 中文词典里那句的后半
    const goHint = (formatsSrc.match(/const PlainTextContainerHint = "([^"]*)"/) || [])[1] || ''
    expect(goHint, 'Go 侧 PlainTextContainerHint 锚点取不到 ⇒ 文案已漂').not.toBe('')
    // 前端兜底句（apiMsg 的回落值）必须含 Go 那一句——按**代码行**扫，不导符号：
    // 导成一个导出的中文常量会被 i18n 的「api 层不许写死中文」闸门判红（〇-R），
    // 而它的豁免恰恰是「同一物理行里有 apiMsg(」，所以这一族文案**只能**以兜底句形态存在。
    const tsCode = stripGoComments(read('src/api/translate.ts'))
    const fallbackLine = tsCode.split('\n').find((l) => l.includes("tr.fileFmtPlainText") && l.includes('apiMsg(')) || ''
    expect(fallbackLine, 'translate.ts 里找不到 tr.fileFmtPlainText 的 apiMsg 兜底行 ⇒ 接线漂了').not.toBe('')
    expect(fallbackLine.includes(goHint), `前端兜底句与 Go 常量不等值：${fallbackLine}`).toBe(true)
    const zhDict = read('src/i18n/dicts.zh.ts')
    const zhLine = (zhDict.match(/'tr\.fileFmtPlainText': '([^']*)'/) || [])[1] || ''
    expect(zhLine, '中文词典缺 tr.fileFmtPlainText（12 语种全量口径）').not.toBe('')
    expect(zhLine.endsWith(goHint), `中文词典那句必须以 Go 常量收尾，实得 ${JSON.stringify(zhLine)}`).toBe(true)

    // 行为腿：前端校验对撤除格式给的必须是出路句，对 .exe 给的必须**不是**出路句
    const asFile = (name: string): File => ({ name, size: 1024 } as unknown as File)
    const retiredMsg = validateTranslateFile(asFile('sub.srt')) || ''
    expect(retiredMsg, '.srt 的校验消息没给出路').toContain(goHint)
    const exeMsg = validateTranslateFile(asFile('tool.exe')) || ''
    expect(exeMsg, '.exe 不该拿到「粘贴到文本翻译」那句（成因不同，属误答）').not.toContain(goHint)
    expect(exeMsg).toContain('仅支持')
    expect(validateTranslateFile(asFile('ok.docx')), '在役格式必须仍通过校验').toBeNull()
    // 反证：把 .docx 也塞进撤除清单 ⇒ 补集判据必须红（证明这两条不是只测"少了"）
    expect(
      [...RETIRED_TEXT_EXTS, '.docx'].filter((e) => TRANSLATE_FILE_EXTS.includes(e as (typeof TRANSLATE_FILE_EXTS)[number])).length,
      '反证落空：文档类被当成撤除档时补集判据应当非零',
    ).toBeGreaterThan(0)
  })
})
