#!/usr/bin/env node
// ============ scripts/precompress_dist.mjs · 职责说明 ============
// 前端构建产物（`frontend-react/dist`）的**预压缩出包腿**：给文本类静态资产在旁边落一份
// `.br`（brotli）与 `.gz`（gzip）侧车文件，让 Caddy 的 `file_server { precompressed br gzip }`
// 在收到请求时**直接发那份现成的压缩字节**，而不是在请求线程里现压。
//
// 为什么必须做（2026-10-11 慢链路批第②项）：
//   现网瓶颈不是服务器 CPU，是**大陆 → Cloudflare 免费版 → 美西边缘**这条被实测塌到
//   KB/s 数量级的链路（《部署指南》慢 SQL 批那笔账）。这条链路上唯一的杠杆就是**字节数**：
//   入口包已经从 2,633 kB 懒加载砍到 605,065 B（第①项，本机同口径 build 实测），而 brotli 相对 gzip 在同体积文本上
//   再省 15–20%——省下来的每一 KB 都是用户少等的每一秒。
//   另一半理由是**首字节延迟**：`encode gzip` 边发边压，大 JS 文件会把压缩时间摊进 TTFB；
//   预压缩把它挪到发版那一刻，一次付出、全网复用。
//
// ★ 四条硬口径（改这个文件前必读）：
//   ① **只有发版机跑，生产机上不跑。** 侧车是构建产物的一部分，随 dist 一起 tar 上线；
//      在生产机上就地生成＝换源前后可能出现「有 .br 但对应的是上一版字节」的混合半态
//      （同 AGENTS §一·5「只改源码不重打包＝线上旧包」那一族的成因）。
//   ② **压缩必须「更小才落盘」。** brotli 对已经压缩过的字节会**变大**，而 Caddy 拿到
//      一个比原文还大的侧车会照发不误——`Content-Length` 就是那个更大的数，慢链路上净亏。
//      所以这里有两把锁：扩展名白名单（二进制件根本不进射程）＋逐文件体积比较。
//      ⚠️ 反过来也成立：**「侧车不存在」不一定是漏跑**，可能就该不存在（极小文本件压完更大）。
//      判据必须问「按当前原文重算，该不该有这一份」，不许问「有没有这一份」——
//      这是本脚本 `--check` 现算而非只查在位的理由，也是它比"查文件存在"慢几秒的理由。
//   ③ **换源前必须先过 `--check`。** 侧车没生成/已过期就上线＝Caddy 静默回落原文
//      （不报错、界面照常），属于"有自动降级的链路，产物能打开不算验收"那一类（AGENTS §一·12）。
//   ④ **发版机与校验机必须是同一台、同一个 node。** 现算比对靠的是 brotli 的确定性；
//      换 node 大版本可能让同一份字节的压缩结果差几个字节，`--check` 就会假红——
//      假红是安全的方向（重跑一次生成即自愈），所以仍选现算比对而不是「只比 mtime」。
//
// 用法：
//   node scripts/precompress_dist.mjs                 # 生成/刷新侧车（默认 dist = frontend-react/dist）
//   node scripts/precompress_dist.mjs --check         # 只校验：侧车缺失/过期/多余即退 1，不写盘
//   node scripts/precompress_dist.mjs --dist <目录>    # 指定产物目录（测试夹具用）
//   node scripts/precompress_dist.mjs --quiet         # 只出一行汇总（发版日志里用）
//   node scripts/precompress_dist.mjs --no-prune      # 不清理孤儿侧车（调试用）
//
// 退出码：0 正常 / 1 --check 判定不合（缺/大/过期/多余）/ 2 参数或目录错
//         / 3 产物目录里有 macOS 脏文件（.DS_Store、._*）
// 闸门：`frontend-react/src/precompressGate.test.ts`（真跑本脚本打临时夹具，含五条反证：
//       摘掉「更小才落盘」⇒ 小文本也被压出更大的侧车且 check 判红；摘掉脏文件闸 ⇒ .DS_Store 随包上线；
//       摘掉过期判据 ⇒ 重 build 后旧侧车继续投；把 --check 写成恒 0 ⇒ 整套发版前判据失效；
//       把仓库根解析退回 `new URL().pathname` ⇒ 中文目录名被百分号编码、默认 dist 路径恒不存在）。
import { brotliCompressSync, brotliDecompressSync, gzipSync, gunzipSync, constants as zlibConstants } from 'node:zlib'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import fs from 'node:fs'
import path from 'node:path'

// ── 文本资产白名单：只有这些扩展名进预压缩 ────────────────────────────────
// 口径＝「Caddy 会按静态文本直出的那类文件」。图片/音视频/zip/whl/tgz 本身已压缩，
// 再压一次只会变大（见上面第②条），所以刻意不收。
const TEXT_EXT = new Set([
  '.js', '.mjs', '.cjs', '.css', '.html', '.htm', '.svg', '.json',
  '.txt', '.map', '.xml', '.webmanifest',
])
// 注：`.wasm` 刻意不收——它已经是 deflate 级熵的容器，且浏览器对 wasm 走编译缓存，
// 给它做 brotli 收益极小（历史读数：一份 200 KB wasm 的 brotli 只省 3%），不值这趟往返。

// brotli 参数：质量 11（最高档，发版机一次性付出）。
// 这里不追求"码率最优"，追求**确定性与可复现**：同一份输入字节 + 同一组参数 ⇒ 同一组输出字节。
//
// ★★ 2026-10-14 现网事故：这里曾经带着一行 `BROTLI_PARAM_LARGE_WINDOW: 1`，它把整站打白屏了。
//   大窗口（WBITS>24）是 brotli 的**扩展档位**，而浏览器内置的解码器一律把窗口上限钉在 16 MiB——
//   Caddy 把这个流原样发给带 `Accept-Encoding: br` 的访客，Chrome/Edge/Firefox/Safari **全部判解码失败**，
//   于是入口 `<script type=module>` 直接 import 失败＝白屏（现网长相：哨兵报「有脚本没能加载 index-Cil7bhKZ.js」）。
//   A/B 实测（同一份 608,625 B 入口、同 node、只改这一个参数）：
//     不带 ⇒ 160,718 B，brotliDecompressSync 解回 608,625 B 逐字节相等；
//     带 ⇒   160,719 B，头四字节 1156c125，brotliDecompressSync 抛 "Decompression failed"。
//   现网那份 .br 的头四字节正是 1156c125 —— 缺陷坐实，与 CDN／Caddy／前端代码都无关。
//   ⚠️ 收益那一侧本来就是零：本项目最大的单个文本资产 605 KB，离 16 MiB 窗口差两个数量级，
//      大窗口对这种量级**一个字节都省不到**（实测 160,718 vs 160,719，反而大 1 B）。
//   ⇒ 这一档**永久禁止再写回来**。机械锁见 backend-go/internal/fileproc/precompress_brotli_gate_test.go，
//      且真正的防线不是"参数没写"，是下面 compressBuf 出口那道**解回来逐字节等值**的自检。
const BROTLI_BASE = {
  [zlibConstants.BROTLI_PARAM_QUALITY]: 11,
  [zlibConstants.BROTLI_PARAM_NPOSTFIX]: 0,
  [zlibConstants.BROTLI_PARAM_NDIRECT]: 20,
}

/** 把一份原文压成指定格式的字节（返回 Buffer）。
 *  ★ 出口必过 decodeOrThrow：产出的字节**必须能被同一个解码器解回原文**才允许落盘。
 *    侧车是"发出去给浏览器解"的东西，判据就不能只问"我压出来了吗"，要问"对面解得开吗"。
 *    这一句是本次事故的根治腿——上面那行参数即便被人再写回来，这里会当场抛，
 *    一个坏侧车都落不到 dist 里，更进不了 tar。 */
function compressBuf(buf, fmt) {
  if (fmt === 'br') {
    const out = brotliCompressSync(buf, {
      params: { ...BROTLI_BASE, [zlibConstants.BROTLI_PARAM_SIZE_HINT]: buf.length },
    })
    return decodeOrThrow(out, buf, fmt)
  }
  const out = gzipSync(buf, { level: 9 })
  return decodeOrThrow(out, buf, fmt)
}

/** 解码回原文并与源字节逐字节比对；解不开或不等即抛（抛＝这个侧车不许上线）。 */
function decodeOrThrow(compressed, original, fmt) {
  let back
  try {
    back = fmt === 'br' ? brotliDecompressSync(compressed) : gunzipSync(compressed)
  } catch (e) {
    throw new Error(`${fmt} 侧车解不开（浏览器会判解码失败）：${e && e.message}`)
  }
  if (Buffer.compare(back, original) !== 0) {
    throw new Error(`${fmt} 侧车解回后与原文不等（解出 ${back.length} B，原文 ${original.length} B）`)
  }
  return compressed
}

/** 递归收集目录下所有普通文件（返回相对 dist 根、正斜杠分隔的路径）。 */
function walkFiles(root) {
  const out = []
  const stack = [root]
  while (stack.length) {
    const dir = stack.pop()
    for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
      const abs = path.join(dir, ent.name)
      if (ent.isDirectory()) { stack.push(abs); continue }
      if (!ent.isFile()) continue // 符号链接/设备件不属于产物，一律跳过
      out.push(path.relative(root, abs).split(path.sep).join('/'))
    }
  }
  return out.sort()
}

/** 该相对路径是不是"该被预压缩的文本资产"（侧车自身不算）。 */
function isEligible(rel) {
  if (isSidecar(rel)) return false
  return TEXT_EXT.has(path.extname(rel).toLowerCase())
}
/**
 * 是不是侧车文件（`.br` / `.gz` 后缀**且剥掉后缀后仍是一个文本资产**）。
 *
 * ★★ 2026-10-14 真踩：只按后缀判会把**别人的真产物**当成侧车删掉。
 *   SDK 的 Python 源码包叫 `langcross_translator-1.0.2.tar.gz`——它以 `.gz` 结尾，
 *   于是"孤儿侧车清理"那一档去找那个**结构上根本不存在**的 `langcross_translator-1.0.2.tar`，
 *   找不到就当死字节 `rmSync` 了（三个 `.tar.gz` 全没，`.whl`/`.tgz` 因为后缀不是 `.gz` 而幸存）。
 *   最坏的是它的**症状分布**：`--check` 那一档不跑清理 ⇒ 我复跑核对时是绿的；
 *   `check_frontend_tar.sh` 的 T1 只问"文本资产该有侧车的那几对" ⇒ 看不见多删的文件；
 *   一路到 Playwright 打 `/sdk/*.tar.gz` 落进 SPA 兜底（200＋整页 HTML）才现形。
 *   ⇒ 侧车的定义必须问**语义**（"这是某个文本资产的压缩副本"）而不是问后缀，
 *     与 AGENTS §一·12「就绪判据不许吃零值」同族：判据的输入本身要能指回一个真存在的东西。
 */
function isSidecar(rel) {
  if (!rel.endsWith('.br') && !rel.endsWith('.gz')) return false
  // 剥掉后缀后的名字必须仍是 TEXT_EXT 里那一类文本件，才可能是本脚本写出来的侧车。
  return TEXT_EXT.has(path.extname(baseOfSidecar(rel)).toLowerCase())
}
/** 侧车对应的原文路径（只在 isSidecar 为真时有意义）。 */
function baseOfSidecar(rel) { return rel.slice(0, -3) }
/** 原文对应的侧车路径。 */
function sidecarOf(rel, fmt) { return rel + (fmt === 'br' ? '.br' : '.gz') }

/** macOS 脏文件（AppleDouble 与 .DS_Store）：跟着 tar 落进 web 根＝公网可下载件。 */
function isDirty(rel) {
  const base = path.basename(rel)
  return base === '.DS_Store' || base.startsWith('._')
}

// —— 参数与模式（三档：生成／--check 在位核对／--quiet 精简输出）——
/** 内容指纹口径：与 check_frontend_tar.sh、postdeploy 的公网 sha 判据同一把尺子（sha256 十六进制）。 */
const sha256 = (b) => createHash('sha256').update(b).digest('hex')

// ── 命令行解析 ────────────────────────────────────────────────────────────
const argv = process.argv.slice(2)
/** 布尔开关取值（只认精确长选项名，不做前缀匹配，避免 `--ch` 被当成 `--check`）。 */
const has = (n) => argv.includes(n)
/** 带值选项取值：缺参回默认，绝不回空串以外的第三种形态（调用方按 falsy 判"没传"）。 */
const optOf = (n, dflt) => { const i = argv.indexOf(n); return i >= 0 && argv[i + 1] ? argv[i + 1] : dflt }
/** --check＝在位核对模式：不写任何文件，只现算侧车内容并与盘上字节比对。 */
const CHECK = has('--check')
/** --quiet＝只出一行汇总（发版链里跑，屏幕不给 37 行噪声）。 */
const QUIET = has('--quiet')
/** --no-prune＝不清孤儿侧车（夹具调试用；正常发版不许带，原文换名后残留＝随 tar 上线的死字节）。 */
const NO_PRUNE = has('--no-prune')
// 默认产物目录相对**仓库根**（本脚本在 scripts/ 下），这样从任意 cwd 跑都对。
// ⚠️ 必须走 `fileURLToPath` 而不是 `new URL(...).pathname`：后者返回**百分号编码**的路径，
//    本仓目录名是中文（`翻译助手`），于是 pathname 得到 `/Desktop/%E7%BF%BB%E8%AF%91%E5%8A%A9%E6%89%8B/…`，
//    `fs.existsSync` 恒假 ⇒ 脚本一进默认模式就报「产物目录不存在」（真踩：首跑即在中文目录下 ABORT=2）。
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
/** 产物根：显式 `--dist` 优先（夹具测试用它），否则回落到仓库内 frontend-react/dist。 */
const DIST = optOf('--dist', '') ? path.resolve(optOf('--dist')) : path.resolve(repoRoot, 'frontend-react', 'dist')

// `--print-root`：把解析出的仓库根打到 stdout 后退 0。
// 它不是给人用的便利口子，是**闸门的观测腿**——上面那条百分号编码的坑正是「默认路径解析」这一腿
// 坏掉时的表现，而发版机上 dist 通常存在、跑不到 ABORT=2 那一行，闸门若只测 --dist 模式就看不见它。
// 判据要能在**没有 dist 的机器（CI 干净检出）**上跑，所以这一档必须在校验目录存在之前返回。
if (has('--print-root')) {
  console.log(repoRoot)
  process.exit(0)
}

if (!fs.existsSync(DIST) || !fs.statSync(DIST).isDirectory()) {
  console.error(`ABORT=2 产物目录不存在或不是目录：${DIST}（先跑 npx vite build，或用 --dist 指定）`)
  process.exit(2)
}

/** 产物目录全量文件清单（相对路径，已排除侧车本身）：脏文件判定与逐文件压缩共用这一次遍历。 */
const all = walkFiles(DIST)

// ── 第一道：macOS 脏文件。发现即中止，不给"顺手打包"留余地（AGENTS §三 换源第①条）──
const dirty = all.filter(isDirty)
if (dirty.length) {
  console.error(`ABORT=3 产物目录里有 ${dirty.length} 个 macOS 脏文件（跟着 tar 上线＝公网可下载）：`)
  for (const d of dirty.slice(0, 20)) console.error('  · ' + d)
  if (dirty.length > 20) console.error(`  …另 ${dirty.length - 20} 个`)
  console.error(`  清理：find ${DIST} \\( -name '.DS_Store' -o -name '._*' \\) -delete`)
  process.exit(3)
}

/** 可压资产全集（文本类型白名单＋已排除 .br/.gz 侧车）：整条链只对这一批出侧车。 */
const eligible = all.filter(isEligible)

// ── 第二道：孤儿侧车（原文已经不在了）─────────────────────────────────────
// 判据只问「原文在不在」。原文在但字节变了 ⇒ 由下面逐文件的**内容比对**覆盖，
// 这一道只管"文件都被 vite 换名了还留下的死字节"（会随 tar 上线，纯占磁盘与 inode）。
let pruned = 0
if (!CHECK && !NO_PRUNE) {
  for (const rel of all) {
    if (!isSidecar(rel)) continue
    if (!fs.existsSync(path.join(DIST, baseOfSidecar(rel)))) {
      fs.rmSync(path.join(DIST, rel))
      pruned++
    }
  }
}

// ── 主循环：逐个文本资产现算「该不该有侧车」，写盘或核对 ──────────────────
// 每份原文只读一次，br 与 gz 各压一次；check 态拿现算字节与盘上侧车做**内容比对**。
let brWritten = 0, gzWritten = 0, brSkipped = 0, gzSkipped = 0
// gzMissing：check 态专用读数——按当前原文**本该有** .gz 却盘上没有的份数（不判红，但必须看得见，
// 2026-10-12 复校加；旧代码把这一族记进 gzSkipped，汇总行会把「侧车压根没写」显示成「压完没省所以不写」）。
let gzMissing = 0
let srcTotal = 0, brTotal = 0, gzTotal = 0
// check 态的不合清单（缺／大／过期(内容不符)／多余）：非空即 ABORT=2，逐条打印后由发版链拦停
const problems = []

for (const rel of eligible) {
  const abs = path.join(DIST, rel)
  let buf
  try {
    buf = fs.readFileSync(abs)
  } catch (e) {
    problems.push(`${rel} ⇒ 原文读不到：${e.message}`)
    continue
  }
  srcTotal += buf.length

  for (const fmt of ['br', 'gz']) {
    let want
    try {
      want = compressBuf(buf, fmt)
    } catch (e) {
      problems.push(`${rel} ⇒ ${fmt} 压缩异常：${e.message}`)
      continue
    }
    // ★ 第②条那把尺子：不比原文小就**不该有**侧车（并且要把上一版留下的侧车删掉）。
    const shouldExist = want.length < buf.length
    const scPath = path.join(DIST, sidecarOf(rel, fmt))
    const exists = fs.existsSync(scPath)

    if (CHECK) {
      // gz 侧车缺失不判红：全局 `encode gzip` 仍在，老客户端由 Caddy 运行时压——
      // 那是"少省一点"，不是"发错字节"。br 才是这一批的目标，缺/不符都要红。
      if (fmt === 'gz') {
        if (!exists) {
          // ★ 2026-10-12 发版当日复校：`!exists` 里混着两种形态——"本来就不该有"与"该有却没生成"。
          //   旧代码把两者都记进 gzSkipped，屏上读数就成了「gzip 合理跳过 37／0 B（省 100.0%）」，
          //   看发版日志的人会把"侧车压根没写"读成"压完没省所以不写"。判据不动（gz 缺失仍不判红，
          //   全局 `encode gzip` 兜着），但**读数必须分得开**。
          if (shouldExist) gzMissing++; else gzSkipped++
          continue
        }
        if (fs.statSync(scPath).size >= buf.length) {
          problems.push(`${rel}.gz ⇒ 多余：侧车不比原文小（会被 Caddy 直接发出去，净亏）`)
        } else { gzWritten++; gzTotal += fs.statSync(scPath).size } // check 态累加**盘上**字节，汇总行才是磁盘真值
        continue
      }
      if (!shouldExist && !exists) { brSkipped++; continue } // 本来就不该有，也没有 ⇒ 合规
      if (!shouldExist && exists) {
        problems.push(`${rel}.br ⇒ 多余：侧车不比原文小（会被 Caddy 直接发出去，净亏）`)
        continue
      }
      if (!exists) {
        problems.push(`${rel}.br ⇒ 缺失：该有而盘上没有（没跑预压缩，或跑完之后又 build 了一次）`)
        continue
      }
      const onDisk = fs.readFileSync(scPath)
      if (sha256(onDisk) !== sha256(want)) {
        problems.push(`${rel}.br ⇒ 过期：现算字节与盘上侧车不符（原文重 build 过却没重压）`)
      } else { brWritten++; brTotal += onDisk.length }
      continue
    }

    if (!shouldExist) {
      if (exists) fs.rmSync(scPath)
      if (fmt === 'br') brSkipped++; else gzSkipped++
      continue
    }
    fs.writeFileSync(scPath, want, { mode: 0o644 })
    if (fmt === 'br') { brWritten++; brTotal += want.length } else { gzWritten++; gzTotal += want.length }
  }
}

// ── 汇总读数（发版台账要的是这一行，不是"感觉变快了"）──────────────────
const pct = (raw, comp) => (raw > 0 ? ((1 - comp / raw) * 100).toFixed(1) : '0.0')
console.log(`预压缩${CHECK ? '校验' : '生成'}：${DIST}`)
if (!QUIET) {
  console.log(`文本资产 ${eligible.length} 个／原始 ${srcTotal.toLocaleString()} B`)
  console.log(`  brotli：${CHECK ? '在位核对' : '写'} ${brWritten}／合理跳过 ${brSkipped} ⇒ ${brTotal.toLocaleString()} B（省 ${pct(srcTotal, brTotal)}%）`)
  console.log(`  gzip ：${CHECK ? '在位核对' : '写'} ${gzWritten}／合理跳过 ${gzSkipped} ⇒ ${gzTotal.toLocaleString()} B（省 ${pct(srcTotal, gzTotal)}%）`)
  if (pruned) console.log(`  清理孤儿侧车 ${pruned} 个`)
} else if (!CHECK) {
  console.log(`brotli ${srcTotal.toLocaleString()} B → ${brTotal.toLocaleString()} B（省 ${pct(srcTotal, brTotal)}%）`)
}
// ★ 2026-10-12 复校：这一行**不受 --quiet 约束**，把 check 态的「该有却没生成」从「合理跳过」里拆出来。
//   旧代码把 !exists 一律记进 gzSkipped，发版日志上「gzip 在位核对 0／合理跳过 37 ⇒ 0 B（省 100.0%）」
//   会把「侧车压根没生成」读成「压完没省所以不写」；而 --quiet 正是发版日志用的那一档，
//   放在 !QUIET 里等于把这条诚实读数又藏回看不见的地方。
if (CHECK && gzMissing) {
  console.log(`  ⚠️ gzip「该有却不在位」${gzMissing} 个（不判红：全局 encode gzip 会在请求线程现压，` +
    `但预压缩本该省下的那段字节没上线——要全量直出就重跑本脚本不带 --check）`)
}

if (CHECK) {
  if (problems.length) {
    console.error(`CHECK=1 ${problems.length} 处不合（缺／大／内容不符）：`)
    for (const p of problems.slice(0, 25)) console.error('  · ' + p)
    if (problems.length > 25) console.error(`  …另 ${problems.length - 25} 处`)
    console.error('  ⇒ 先跑 `node scripts/precompress_dist.mjs` 重压，再打包换源。')
    console.error('     Caddy 在没有侧车时**静默发原文**：界面照常、一行错误都没有，没人会发现省下的字节没上线。')
    process.exit(1)
  }
  console.log(`CHECK=0 ${eligible.length} 个文本资产的 brotli 侧车全部现算相符`)
  process.exit(0)
}

// ── 写盘态的自证：写完立刻用同一把尺子复量（"我以为写了" ⇒ "实测在位"）────
// 抓的是写盘失败（磁盘满、只读挂载、目录权限）——那些在上面的循环里静默通过，
// 而它们的对外表现正好是第③条描述的那个"没人会发现"的静默回落。
{
  const missingAfter = []
  for (const rel of eligible) {
    const abs = path.join(DIST, rel)
    let buf
    try { buf = fs.readFileSync(abs) } catch { continue }
    const want = compressBuf(buf, 'br')
    if (want.length < buf.length && !fs.existsSync(path.join(DIST, sidecarOf(rel, 'br')))) {
      missingAfter.push(rel)
    }
  }
  if (missingAfter.length) {
    console.error(`SELFTEST=1 写完复验：${missingAfter.length} 个资产本该有 .br 却在盘上找不到（前 10 个）：`)
    for (const r of missingAfter.slice(0, 10)) console.error('  · ' + r)
    console.error('  ⇒ 别把这一批打包上线：Caddy 会静默发原文。先查磁盘/权限，再重跑本脚本。')
    process.exit(1)
  }
}
process.exit(0)
