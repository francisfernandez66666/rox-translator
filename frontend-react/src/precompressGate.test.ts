// ============ src/precompressGate.test.ts · 职责说明 ============
// `scripts/precompress_dist.mjs`（发版机预压缩出包腿）与 Caddy `precompressed` 接线
// 的**行为闸门**（2026-10-12 慢链路批②）；文件末尾 D 段同口径锁住
// `scripts/check_frontend_tar.sh`（换源前自检四档）与它在 `deploy/deploy_to_production.sh`
// 里的那一次**薄委托**（判据只留一个事实源，两条换源路径调同一条）。
//
// 为什么这一批必须有自动化锁（而不是"看一眼 curl 的 content-encoding"）：
//   这条链最坏的失败形态是**静默回落**——侧车没生成、生成了一半、或者生成的是上一版字节时，
//   Caddy 一声不吭地发原文，界面照常渲染、构建照常绿、发版验收脚本照常过，
//   而"省下的 15–20% 字节"根本没上线。这正是 AGENTS §一·12 定性的那一类
//   （「有自动降级的链路，产物能打开不算验收」），只能用机械判据堵住。
//   另一侧同样要堵：**「侧车不存在」不一定算漏**——极小文本件 brotli 压完反而更大，
//   那种文件本就不该有侧车。判据必须现算"该不该有"，不能只查"在不在"，
//   否则这条锁会长期红灯，然后把红灯的人训成"忽略它"。
//
// 本文件的反证（每条都真跑，见 R1–R5 注释）：
//   R1 摘掉「更小才落盘」⇒ 极小文件被压出更大的侧车 ⇒ 真 check 判红（抓"多余侧车"）；
//   R2 摘掉脏文件闸 ⇒ .DS_Store 随包上线（本文件正向断言 exit 3，摘了就红）；
//   R3 摘掉内容比对（只查在位）⇒ 重 build 后旧侧车继续投（本文件断言"过期"必红）；
//   R4 把 --check 写成恒 0 ⇒ T5/T6 当场红；
//   R5 Caddy 接线退回与 `{` 同行 ⇒ T9 语法锁当场红（那是 adapt 期硬错，线上会起不来）；
//   R6 换源前自检的四档各破坏一次 ⇒ 只有对应那一档红（D3–D5 真跑夹具，见文件末尾 D 段）。
// =============================================
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

// 脚本与仓库根：本文件在 frontend-react/src 下，向上两级即仓库根。
// ★ 这里刻意用 fileURLToPath 而不是 new URL().pathname —— 后者给百分号编码路径，
//   本仓目录名是中文（`翻译助手`），拿它去 existsSync 恒假（脚本首跑就是这么 ABORT=2 的，
//   现由 T8 的 --print-root 等值锁钉住）。
const SRC_DIR = fileURLToPath(new URL('.', import.meta.url))
/** 仓库根（夹具与临时目录都相对它算，避免依赖调用方的 cwd）。 */
const REPO_ROOT = path.resolve(SRC_DIR, '..', '..')
/** 现役预压缩脚本的绝对路径：所有判据都跑这一份真脚本，测试里不复制第二份实现。 */
const SCRIPT = path.join(REPO_ROOT, 'scripts', 'precompress_dist.mjs')

/** 建一个临时产物目录（夹具根）。 */
function mkFixture() {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'precompress-gate-'))
}
/** 跑脚本；返回退出码与两路输出（execFileSync 非零会抛，这里统一收口成读数）。
 *  entry 默认是现役脚本；反证那条传**坏副本的绝对路径**（首跑踩的就是"把坏副本当参数塞进
 *  默认入口"——`node 真脚本 坏脚本 --dist …` 里坏脚本只是个被忽略的位置参数，
 *  于是反证其实拿真判据跑了一遍，"坏版本确实产出大侧车"那条当场假红。入口必须显式分派。） */
function run(args: string[], entry: string = SCRIPT) {
  try {
    const out = execFileSync('node', [entry, ...args], { encoding: 'utf8' })
    return { code: 0, out, err: '' }
  } catch (e: any) {
    return { code: typeof e.status === 'number' ? e.status : -1, out: e.stdout || '', err: e.stderr || '' }
  }
}
/** 侧车在位性读数：本文件所有「该有/不该有」判据的唯一取数口径。 */
const has = (p: string) => fs.existsSync(p)
/** 侧车字节数：用于「不该比原文大」与「清理后不留死字节」两类等值/上限判据。 */
const size = (p: string) => fs.statSync(p).size

/** 一份"压得动"的文本件：重复结构 + 中英混排，brotli/gzip 都有明显收益。 */
function writeCompressible(abs: string) {
  fs.writeFileSync(abs, 'function render(){ return "翻译助手 慢链路优化 0123456789" }\n'.repeat(300))
}

describe('A · 生成腿：侧车只在"真的更小"时出现', () => {
  it('A1 文本件生成 .br/.gz 且都严格小于原文，随后 --check 判绿', () => {
    const d = mkFixture()
    writeCompressible(path.join(d, 'index-abc.js'))
    fs.writeFileSync(path.join(d, 'index.html'), '<!doctype html><html><head><title>t</title></head><body>' + 'x'.repeat(2000) + '</body></html>')
    const g = run(['--dist', d, '--quiet'])
    expect(g.code, `生成态退出码应为 0，实际 ${g.code}／stderr=${g.err}`).toBe(0)
    const jsBr = path.join(d, 'index-abc.js.br')
    const jsGz = path.join(d, 'index-abc.js.gz')
    expect(has(jsBr), '文本 .js 必须产出 .br 侧车').toBe(true)
    expect(has(jsGz), '文本 .js 必须产出 .gz 侧车（老客户端回落档）').toBe(true)
    expect(size(jsBr)).toBeLessThan(size(path.join(d, 'index-abc.js')))
    expect(size(jsGz)).toBeLessThan(size(path.join(d, 'index-abc.js')))
    const c = run(['--dist', d, '--check'])
    expect(c.code, `刚生成的夹具 --check 必须绿，实际 ${c.code}／${c.err}`).toBe(0)
  })

  it('A2 白名单外的二进制件一律不生成侧车（负向锁，配 A1 的正向对照）', () => {
    const d = mkFixture()
    fs.writeFileSync(path.join(d, 'logo.png'), Buffer.alloc(4096, 0x41))
    fs.writeFileSync(path.join(d, 'pkg.zip'), Buffer.alloc(4096, 0x42))
    writeCompressible(path.join(d, 'side.js')) // 正向对照：同目录文本件要有侧车
    run(['--dist', d, '--quiet'])
    expect(has(path.join(d, 'logo.png.br')), 'png 不该有 brotli 侧车（再压只会变大，Caddy 会照发）').toBe(false)
    expect(has(path.join(d, 'pkg.zip.br')), 'zip 不该有 brotli 侧车（已是压缩容器）').toBe(false)
    expect(has(path.join(d, 'side.js.br')), '同夹具里的文本件必须仍然生成侧车（否则上一条负向是空转）').toBe(true)
  })

  it('A3 极小文本件压完更大 ⇒ 该没有侧车，且 --check 不许因此判红', () => {
    // 这一条是"判据必须现算该不该有，而不是查在不在"的实体：
    // 7 字节的 JSON brotli 出来 13 字节，若判据写成「每个文本件都得有 .br」，
    // 这条锁会长期红灯，最后被人当噪音关掉——比没有锁更坏。
    const d = mkFixture()
    fs.writeFileSync(path.join(d, 'tiny.json'), '{"a":1}')
    const g = run(['--dist', d, '--quiet'])
    expect(g.code).toBe(0)
    expect(has(path.join(d, 'tiny.json.br')), '压完更大的文件不该留侧车').toBe(false)
    const c = run(['--dist', d, '--check'])
    expect(c.code, `合理跳过不该判红，实际 ${c.code}／${c.err}`).toBe(0)
  })

  it('A4〔反证 R1〕摘掉「更小才落盘」判据 ⇒ 真 check 抓到"多余侧车"判红', () => {
    // 手法：把脚本复制到临时目录、只改那一行（**绝不原地改现役脚本**），
    // 用坏版本生成产物，再用**真版本**的 --check 去量——必须判红。
    const d = mkFixture()
    fs.writeFileSync(path.join(d, 'tiny.json'), '{"a":1}')
    // 坏副本必须落在**夹具之外**：它本身是 .mjs 文本件，放进夹具目录会被真 check 当成
    // 待校验资产（多一条"缺失"噪声），把这条反证的靶子从"多余侧车"稀释成"反正都是红"。
    const bad = path.join(mkFixture(), 'broken.mjs')
    const src = fs.readFileSync(SCRIPT, 'utf8')
    const patched = src.replace('const shouldExist = want.length < buf.length', 'const shouldExist = true')
    expect(patched).not.toBe(src) // 锚点漂了要先红在这里，否则反证静默空转
    fs.writeFileSync(bad, patched)
    const g = run(['--dist', d, '--quiet'], bad)
    expect(g.code, '坏版本照样能跑（这正是反证要模拟的"没人发现"）').toBe(0)
    expect(has(path.join(d, 'tiny.json.br')), '坏版本确实产出了比原文大的侧车').toBe(true)
    const c = run(['--dist', d, '--check'])
    expect(c.code, '真判据必须把"侧车不比原文小"判成不合').toBe(1)
    expect(c.err).toContain('多余')
  })

  it('A5 孤儿侧车随原文消失而被清掉（vite 换名后不留死字节）', () => {
    const d = mkFixture()
    writeCompressible(path.join(d, 'old-hash.js'))
    run(['--dist', d, '--quiet'])
    expect(has(path.join(d, 'old-hash.js.br'))).toBe(true)
    fs.rmSync(path.join(d, 'old-hash.js')) // 模拟下一次 build 换了哈希名、旧件被清
    const g = run(['--dist', d, '--quiet'])
    expect(g.code).toBe(0)
    expect(has(path.join(d, 'old-hash.js.br')), '原文没了还留着侧车＝随 tar 上线的死字节').toBe(false)
    expect(has(path.join(d, 'old-hash.js.gz'))).toBe(false)
  })
})

describe('B · 发版前判据腿：--check 抓得住三类漏跑', () => {
  it('B1〔反证 R2〕产物目录里有 .DS_Store ⇒ 退 3 且点名文件', () => {
    const d = mkFixture()
    writeCompressible(path.join(d, 'a.js'))
    expect(run(['--dist', d, '--quiet']).code).toBe(0) // 干净态对照
    fs.writeFileSync(path.join(d, '.DS_Store'), 'dirty')
    const c = run(['--dist', d, '--check'])
    expect(c.code, '.DS_Store 跟着 tar 落进 web 根＝公网可下载（AGENTS §三 换源第①条）').toBe(3)
    expect(c.err).toContain('.DS_Store')
    fs.rmSync(path.join(d, '.DS_Store'))
    expect(run(['--dist', d, '--check']).code, '清掉脏文件后同一夹具必须回绿').toBe(0)
  })

  it('B2〔反证 R3〕改了原文不重压 ⇒ 判"过期"（只查在位会放过去）', () => {
    const d = mkFixture()
    writeCompressible(path.join(d, 'a.js'))
    run(['--dist', d, '--quiet'])
    fs.appendFileSync(path.join(d, 'a.js'), '\n// 重新 build 时多进来的一行\n')
    const c = run(['--dist', d, '--check'])
    expect(c.code, '侧车还在但对应的是上一版字节＝真判据必须红').toBe(1)
    expect(c.err).toContain('过期')
  })

  it('B3 侧车被删 ⇒ 判"缺失"，且错误文案里带着修法', () => {
    const d = mkFixture()
    writeCompressible(path.join(d, 'a.js'))
    run(['--dist', d, '--quiet'])
    fs.rmSync(path.join(d, 'a.js.br'))
    const c = run(['--dist', d, '--check'])
    expect(c.code).toBe(1)
    expect(c.err).toContain('缺失')
    expect(c.err, '文案必须给出下一步（发版是人在跑，别让后人去猜命令）').toContain('precompress_dist.mjs')
  })

  it('B4〔2026-10-12 复校〕gzip 侧车"该有却没有"⇒ 不判红，但读数不许冒充"合理跳过"', () => {
    // 为什么单独一条：判据上 gz 缺失**刻意不红**（全局 `encode gzip` 还在，那是"少省一点"不是"发错字节"），
    // 可正因为不红，它唯一的暴露面就是汇总那一行。旧代码把 `!exists` 一律记进 gzSkipped，
    // 于是发版日志上出现「gzip 在位核对 0／合理跳过 37 ⇒ 0 B（省 100.0%）」——
    // 读日志的人会把「侧车压根没生成」读成「压完没省所以不写」，这正是本批要防的那一族**读数撒谎**。
    const d = mkFixture()
    writeCompressible(path.join(d, 'a.js'))
    expect(run(['--dist', d, '--quiet']).code).toBe(0)
    const clean = run(['--dist', d, '--check'])
    expect(clean.code).toBe(0)
    expect(clean.out, `干净态不该出现"该有却不在位"：${clean.out}`).not.toContain('该有却不在位')
    expect(clean.out).toContain('在位核对 1')
    // gz 盘上字节要进汇总（旧代码 check 态不累加 ⇒ 显示 0 B／省 100.0%）
    expect(clean.out).toMatch(/gzip ：在位核对 1／合理跳过 0 ⇒ (?!0 B)[\d,]+ B/)

    fs.rmSync(path.join(d, 'a.js.gz'))
    const c = run(['--dist', d, '--check'])
    expect(c.code, 'gz 缺失仍不判红（设计如此，这条不许被抬成红灯）').toBe(0)
    expect(c.out, `必须点名「该有却不在位」：${c.out}`).toMatch(/该有却不在位」1 个/)
    expect(c.out).toContain('合理跳过 0')
    expect(c.out, 'br 那一条必须仍然在位核对通过（缺的是 gz，不是整批侧车）').toContain('brotli：在位核对 1')
  })

  it('B5〔B4 的反证〕把分档那一行退回旧形态 ⇒ 撒谎读数复现（证明 B4 抓得住）', () => {
    const d = mkFixture()
    writeCompressible(path.join(d, 'a.js'))
    expect(run(['--dist', d, '--quiet']).code).toBe(0)
    fs.rmSync(path.join(d, 'a.js.gz'))
    const bad = path.join(mkFixture(), 'broken_gz.mjs') // 坏副本落在夹具之外（同 A4 口径）
    const src = fs.readFileSync(SCRIPT, 'utf8')
    const patched = src.replace('if (shouldExist) gzMissing++; else gzSkipped++', 'gzSkipped++')
    expect(patched, '锚点漂了要先红在这里，否则这条反证静默空转').not.toBe(src)
    fs.writeFileSync(bad, patched)
    const c = run(['--dist', d, '--check'], bad)
    expect(c.code, '坏版本判据方向不变（仍不红）——红的是读数腿，不是退出码').toBe(0)
    expect(c.out, '旧形态必须把"压根没生成"显示成"合理跳过 1"（B4 那条断言正是为抓这个）').toContain('合理跳过 1')
    expect(c.out).not.toContain('该有却不在位')
  })
})

describe('C · 接线与路径解析', () => {
  it('C1〔反证 R5〕三处直出静态资源的 Caddy 接线都在，且必须是块内单独一行', () => {
    // `precompressed` 与 `{` 同行会在 Caddyfile adapt 期报
    // 「Unexpected next token after '{' on same line」——那是**线上起不来**级别的错，
    // 所以这一条不只是"有没有写"，还钉"写在哪"。
    // ⚠️ 判据只问**代码行**：这三份文件里都写着"不许与 { 同行"的说明注释，注释里那串反例
    //    `file_server { precompressed br gzip }` 会被负向锁命中（首跑真红在上面，属测试自伤不是接线错）
    //    ⇒ 先剥以 `#` 开头的整行，剩下的行才进等值/负向判定。
    const targets = ['deploy/caddy/translator.conf', 'deploy/caddy/translator-tenant.conf', 'scripts/bootstrap-demo.sh']
    const codeOnly = (s: string) => s.split('\n').filter((ln) => !ln.trim().startsWith('#')).join('\n')
    for (const rel of targets) {
      const code = codeOnly(fs.readFileSync(path.join(REPO_ROOT, rel), 'utf8'))
      expect(code, `${rel} 必须有 precompressed 接线`).toMatch(/^\s*precompressed\s+br\s+gzip\s*$/m)
      expect(code, `${rel} 不许把 precompressed 写在 { 同一行（adapt 必失败）`).not.toMatch(/\{[^\n]*precompressed/)
      // 正向对照之后补负向：静态资源段还得留着 `file_server`（被整段删掉也是"接线没了"）
      expect(code).toMatch(/file_server\s*\{/)
    }
  })

  it('C2 另一项目（quant-trading）的站点块不许被本批顺手改掉', () => {
    // 本批只动 translator 的两条静态直出腿（translator.conf / translator-tenant.conf）
    // 与 bootstrap-demo 模板。仓库 `deploy/caddy/Caddyfile` 里那段 `quant-trading.top { … }`
    // 属于**另一个项目**的 web 根（/var/www/quant-web），它的 dist 里没有本脚本生成的侧车，
    // 给它加 precompressed 只会让 Caddy 找不到文件（不报错，但白配一行）；
    // 更重要的是：translator 的接线是靠 `import /etc/caddy/translator.conf` 生效的，
    // 有人"顺手"在 Caddyfile 里再写一份 file_server 段，就会长出"仓库两套事实源"。
    const code = fs.readFileSync(path.join(REPO_ROOT, 'deploy', 'caddy', 'Caddyfile'), 'utf8')
    expect(code, 'Caddyfile 不该出现 precompressed（translator 的接线在 translator.conf 里，靠 import 生效）').not.toMatch(/precompressed/)
    expect(code).toMatch(/file_server/) // 正向对照：那一段还在，负向不是"整段被删"造成的恒真
    expect(code).toMatch(/import\s+\/etc\/caddy\/translator\.conf/) // 事实源链路没断
  })

  it('C3 默认产物路径解析：仓库根必须是没有百分号编码的真路径', () => {
    const r = run(['--print-root'])
    expect(r.code).toBe(0)
    const printed = r.out.trim()
    expect(printed, `--print-root 读数=${printed}／期望=${REPO_ROOT}`).toBe(REPO_ROOT)
    expect(printed.includes('%'), `路径里出现百分号说明走了 URL 编码解析（中文目录名会被编码成 %E7%BF%…）：${printed}`).toBe(false)
  })

  it('C4 目录不存在时报 ABORT=2 并给出可执行下一步（不是栈回溯）', () => {
    const r = run(['--dist', path.join(mkFixture(), 'nope')])
    expect(r.code).toBe(2)
    expect(r.err).toContain('ABORT=2')
    expect(r.err).toContain('vite build')
  })
})

// ────────────────────────────────────────────────────────────────────────────
// D · 换源前自检 `scripts/check_frontend_tar.sh`
// ────────────────────────────────────────────────────────────────────────────
// 这一族判据为什么不能只活在 deploy_to_production.sh 里（本文件这一段就是为此而加）：
//   历史上换源走过两条路径——`deploy/deploy_to_production.sh`（只覆盖主站）与《部署指南》§五
//   的手工链（tar→scp→解到 web.new→两步 mv，演示站与租户通配块**本来就不在前者射程里**）。
//   判据写在其中一条路径上，另一条路径就是"没有判据"，而这两批产物是同一份 dist。
//   所以本体收成一个可被两条路径共同调用的脚本，这里既验它的**四档判据有射程**（D2–D5 真跑夹具），
//   又验**调用方确实薄委托**（D6：不许在 deploy 脚本里再抄一份内联计数——那迟早长出一严一松）。
describe('D · 换源前自检 check_frontend_tar.sh（四档判据的射程）', () => {
  const CHECKER = path.join(REPO_ROOT, 'scripts', 'check_frontend_tar.sh')

  /** 跑 shell 自检脚本；非零退出码在 execFileSync 下是异常，统一收成读数。 */
  function runSh(args: string[]) {
    try {
      const out = execFileSync('bash', [CHECKER, ...args], { encoding: 'utf8' })
      return { code: 0, out, err: '' }
    } catch (e: any) {
      return { code: typeof e.status === 'number' ? e.status : -1, out: (e.stdout || '') + (e.stderr || ''), err: e.stderr || '' }
    }
  }

  /** 一份"本批形态"的最小交付目录：入口＋两份侧车＋10 份语种 chunk＋零脏文件。 */
  function mkDist(): string {
    const d = mkFixture()
    fs.mkdirSync(path.join(d, 'assets'), { recursive: true })
    const entry = path.join(d, 'assets', 'index-AAAA1111.js')
    fs.writeFileSync(entry, 'console.log("入口 chunk：本批实测 605,065 B，夹具只要求 ≤ 上限")\n')
    fs.writeFileSync(entry + '.br', 'br-bytes')
    fs.writeFileSync(entry + '.gz', 'gz-bytes')
    // 语种名必须与脚本里那份名单同源（少一个名字＝D5 的"抹掉全部"抹不干净，反证会假绿）
    for (const loc of ['zh-hant', 'ja', 'ko', 'th', 'ru', 'ar', 'fr', 'es', 'de', 'pt', 'id', 'vi', 'it', 'nl', 'pl', 'tr']) {
      const f = path.join(d, 'assets', `${loc}-BBBB2222.js`)
      fs.writeFileSync(f, 'export default {}')
      fs.writeFileSync(f + '.br', 'x')
      fs.writeFileSync(f + '.gz', 'x')
    }
    fs.writeFileSync(path.join(d, 'index.html'), '<html><script src="/assets/index-AAAA1111.js"></script>__BRANDING__</html>')
    return d
  }

  it('D1 没有 index.html 的目录＝ABORT=2（不是"四档全过"）', () => {
    const d = mkFixture() // 空目录
    const r = runSh(['--dist', d])
    expect(r.code, `空目录应退 2，读数=${r.out}`).toBe(2)
    expect(r.out + r.err).toContain('ABORT=2')
  })

  it('D2 干净夹具四档全过（正向对照：D3–D5 的负向不是"恒红"）', () => {
    const r = runSh(['--dist', mkDist()])
    expect(r.code, `读数=${r.out}${r.err}`).toBe(0)
    expect(r.out).toContain('FRONTEND_TAR_ALL_OK=1')
    for (const t of ['T1', 'T2', 'T3', 'T4']) {
      expect(r.out, `${t} 那一档必须在输出里被点名判过（缺档＝判据被整段删掉也照样绿）`).toContain(t)
    }
  })

  it('D3〔反证〕摘掉入口 .br 侧车 ⇒ 只有 T1 红', () => {
    const d = mkDist()
    fs.rmSync(path.join(d, 'assets', 'index-AAAA1111.js.br'))
    const r = runSh(['--dist', d])
    expect(r.code).toBe(1)
    expect(r.out).toMatch(/BAD\s+T1/)
    expect(r.out, '只许 T1 红：别的档也红＝判据互相牵连，归因会失效').not.toMatch(/BAD\s+T[234]/)
  })

  it('D4〔反证〕塞一个 .DS_Store ⇒ 只有 T2 红', () => {
    const d = mkDist()
    fs.writeFileSync(path.join(d, '.DS_Store'), 'dirty')
    const r = runSh(['--dist', d])
    expect(r.code).toBe(1)
    expect(r.out).toMatch(/BAD\s+T2/)
    expect(r.out).not.toMatch(/BAD\s+T[134]/)
  })

  it('D5〔反证〕抹掉全部独立语种 chunk ⇒ 只有 T4 红（①懒加载没生效的形态）', () => {
    const d = mkDist()
    for (const loc of ['zh-hant', 'ja', 'ko', 'th', 'ru', 'ar', 'fr', 'es', 'de', 'pt', 'id', 'vi', 'it', 'nl', 'pl', 'tr']) {
      for (const suffix of ['', '.br', '.gz']) {
        fs.rmSync(path.join(d, 'assets', `${loc}-BBBB2222.js${suffix}`), { force: true })
      }
    }
    const r = runSh(['--dist', d])
    expect(r.code).toBe(1)
    expect(r.out).toMatch(/BAD\s+T4/)
    expect(r.out).not.toMatch(/BAD\s+T[123]/)
  })

  it('D6 调用方是薄委托：deploy 脚本调这一个文件，且不再抄第二份内联计数', () => {
    const deploy = fs.readFileSync(path.join(REPO_ROOT, 'deploy', 'deploy_to_production.sh'), 'utf8')
    // 正向：真的委托给自检脚本
    expect(deploy, 'deploy_to_production.sh 必须调用 scripts/check_frontend_tar.sh').toContain('scripts/check_frontend_tar.sh')
    // 负向：不许在调用方再写一份"数 .br"的内联判据（两份尺子迟早一严一松）
    expect(deploy).not.toMatch(/grep -cE\s+'\\\.br\$'/)
    // 正向对照（负向锁必须配正向，否则"整段自检被删掉"也会让上面那条恒真）：
    //   自检这一行之后仍然拦着上传——scp 那两条腿还在，说明委托没把上传链一起带走。
    expect(deploy).toMatch(/scp -P "\$REMOTE_PORT" \/tmp\/frontend-dist\.tar\.gz/)
    expect(deploy).toMatch(/check_frontend_tar\.sh.*\|\|/)
  })
})
