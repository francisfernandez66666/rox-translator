#!/usr/bin/env node
// ============================================================================
// scripts/gen_seo_meta.mjs — SEO 路由 meta 的**校验器**（DEF-06 收口批，2026-10-25）
//
// 事实源只有一份：frontend-react/src/seo/route_meta.json（12 语种）。
// 本脚本不生成文案、**绝不做机翻**——缺译文一律报错退出非 0。
// 理由写在方案里：机器译文当 SEO 文案发出去＝把未经审阅的句子送进搜索引擎索引，
// 而搜索引擎缓存下来的东西，比界面上一句错文案难撤得多。
//
// 它管四件事（方案 §A 点名的四条）：
//   V1 12 语种键齐全：每条路由的 title/description 都必须有全部 12 个语种，多一个也算错
//      （多余键＝有人手工加过没登记的语种，注入面与前端取词面会各认一半）；
//   V2 没有空串／纯空白（空 description 让搜索引擎自己抓正文，等于放弃控制）；
//   V3 同一语种内 title 不得重复（现网真踩过：/register 直接抄了 /login 的「Sign in」）；
//   V4 robots 分档正确：可收录三页 index,follow；登录/注册 noindex,follow；
//      并核 languages / x_default_lang / app 的形态。
//
// 用法：
//   node scripts/gen_seo_meta.mjs            # 校验，通过退 0
//   node scripts/gen_seo_meta.mjs --check    # 校验 ＋ 核后端副本逐字节同步（发版闸门跑这一条）
//   node scripts/gen_seo_meta.mjs --selftest # 判据自证：喂五种坏形态必须各判红（防"闸门失明"）
//
// 与另外两把尺子的分工（**不许互相顶替，也不许互相抄**）：
//   · scripts/sync_seo_meta.sh 只管"复制/比对 sha"（写侧）；
//   · backend-go/internal/api/seo_meta_test.go 管"服务端消费方看到的形状"（Go 侧回归）；
//   · 本脚本管"发版前把源文里的缺口拦住"（内容侧，且是唯一能报"缺哪个语种的哪条"的面）。
// ============================================================================
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SRC = join(ROOT, 'frontend-react/src/seo/route_meta.json')
const DST = join(ROOT, 'backend-go/internal/api/seo_meta.json')

// EXPECT_LANGS 12 语种码表（与前端 i18n 口径同源，顺序即 hreflang 输出顺序）。
const EXPECT_LANGS = ['zh', 'zh_hant', 'en', 'ru', 'fr', 'ar', 'es', 'pt', 'de', 'ja', 'ko', 'th']

// ROUTE_ROBOTS 路由收录分档（方案 §A 的表；新增公开路由必须在这里点名，否则判红）。
const ROUTE_ROBOTS = {
  '/': 'index,follow',
  '/pricing': 'index,follow',
  '/compare': 'index,follow',
  '/login': 'noindex,follow',
  '/register': 'noindex,follow',
}

// validate 纯函数：喂一份 route_meta 对象，返回问题清单（空数组＝通过）。
// 拆成纯函数是为了让 --selftest 能在**内存里**造坏形态，不必动真文件。
function validate(doc) {
  const errors = []
  if (!doc || typeof doc !== 'object') return ['根节点不是对象']
  if (!Array.isArray(doc.languages)) {
    errors.push('languages 不是数组')
  } else {
    const got = doc.languages.join(',')
    const want = EXPECT_LANGS.join(',')
    if (got !== want) errors.push(`languages 必须逐字等于 12 语种码表，实得 [${got}]（期望 [${want}]）`)
  }
  if (doc.x_default_lang !== 'zh') errors.push(`x_default_lang 应为 zh，实得 ${JSON.stringify(doc.x_default_lang)}`)
  if (!doc.app || doc.app.robots !== 'noindex,nofollow' || !String(doc.app.title_suffix || '').trim()) {
    errors.push('app 段缺 robots=noindex,nofollow 或 title_suffix 为空')
  }
  const routes = doc.routes
  if (!routes || typeof routes !== 'object') return errors.concat('routes 不是对象')

  for (const [path, wantRobots] of Object.entries(ROUTE_ROBOTS)) {
    const meta = routes[path]
    if (!meta) {
      errors.push(`路由 ${path} 缺条目（新路由必须显式登记，不许靠回落）`)
      continue
    }
    if (meta.robots !== wantRobots) {
      errors.push(`路由 ${path} robots=${JSON.stringify(meta.robots)}，应为 ${wantRobots}`)
    }
    for (const field of ['title', 'description']) {
      const map = meta[field]
      if (!map || typeof map !== 'object') {
        errors.push(`路由 ${path} 缺 ${field} 映射`)
        continue
      }
      const langs = Object.keys(map)
      for (const l of EXPECT_LANGS) {
        const v = map[l]
        if (typeof v !== 'string' || v.trim() === '') {
          errors.push(`路由 ${path} 的 ${field}[${l}] 缺失或为空串（脚本不做机翻：请人工补这一句）`)
        }
      }
      for (const l of langs) {
        if (!EXPECT_LANGS.includes(l)) errors.push(`路由 ${path} 的 ${field} 有多余语种键 ${l}`)
      }
    }
  }
  // V3 同语种 title 去重：跨路由重复＝两页在搜索结果里长得一样，等于自我 cannibalise。
  for (const lang of EXPECT_LANGS) {
    const seen = new Map()
    for (const path of Object.keys(routes)) {
      const t = String(routes[path]?.title?.[lang] ?? '').trim()
      if (!t) continue
      if (seen.has(t)) errors.push(`title[${lang}] 重复：${seen.get(t)} 与 ${path} 同为 ${JSON.stringify(t)}`)
      else seen.set(t, path)
    }
  }
  // 名单外的路由只提示不判红（/g/* 之类由后端直出面自己管）。
  const extra = Object.keys(routes).filter((p) => !(p in ROUTE_ROBOTS))
  if (extra.length) errors.push(`routes 出现未登记路由 ${extra.join(', ')}：先在 ROUTE_ROBOTS 点名再入库`)
  return errors
}

function sha256(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex')
}

// readDoc 读并解析 JSON，语法错直接以清单形式报出（不抛栈：运维看的是那一行结论）。
function readDoc(file) {
  try {
    return { doc: JSON.parse(readFileSync(file, 'utf8')), errors: null }
  } catch (e) {
    return { doc: null, errors: [`解析 ${file} 失败: ${e.message}`] }
  }
}

// selftest 判据自证：把合法文档逐项改坏，每一项都必须被 validate 抓到。
// 没有这一条，"闸门跑绿"与"闸门失明"长得一模一样（本仓的老账）。
function selftest() {
  const parsed = readDoc(SRC)
  if (parsed.errors) {
    console.error('SELFTEST_FAIL 源文件本身读不了：' + parsed.errors.join('; '))
    return 1
  }
  const base = JSON.parse(JSON.stringify(parsed.doc))
  if (validate(base).length !== 0) {
    console.error('SELFTEST_FAIL 合法源文档被判红，判据本身有缺陷：\n  ' + validate(base).join('\n  '))
    return 1
  }
  const mutations = [
    { name: '摘掉一个语种的 title', mk: (d) => { delete d.routes['/pricing'].title.th }, want: 'title[th]' },
    { name: 'description 写成空串', mk: (d) => { d.routes['/compare'].description.ja = '   ' }, want: '为空串' },
    { name: '/register 抄回 /login 的 title', mk: (d) => { d.routes['/register'].title = JSON.parse(JSON.stringify(d.routes['/login'].title)) }, want: 'title[zh] 重复' },
    { name: '可收录页写成 noindex', mk: (d) => { d.routes['/pricing'].robots = 'noindex,follow' }, want: 'robots=' },
    { name: '语种表少一档', mk: (d) => { d.languages = d.languages.filter((l) => l !== 'ko') }, want: 'languages 必须逐字等于' },
    { name: '塞进一个未登记路由', mk: (d) => { d.routes['/brand-new'] = JSON.parse(JSON.stringify(d.routes['/'])) }, want: '未登记路由' },
    { name: '多余语种键', mk: (d) => { d.routes['/'].title.it = 'Traduzione' }, want: '多余语种键' },
  ]
  let bad = 0
  for (const m of mutations) {
    const d = JSON.parse(JSON.stringify(base))
    m.mk(d)
    const errs = validate(d)
    const hit = errs.some((e) => e.includes(m.want))
    if (!hit) {
      console.error(`SELFTEST_FAIL 变异「${m.name}」没被抓到（判据射程不足）；实得清单：${JSON.stringify(errs)}`)
      bad++
    }
  }
  if (bad) return 1
  console.log(`SEO_META_SELFTEST_OK=1 变异 ${mutations.length} 项全部被抓，合法文档零误报`)
  return 0
}

function main() {
  const mode = process.argv[2] || ''
  if (mode === '--selftest') process.exit(selftest())

  const parsed = readDoc(SRC)
  if (parsed.errors) {
    console.error('SEO_META_ABORT=1 ' + parsed.errors.join('; '))
    return 2
  }
  const errors = validate(parsed.doc)
  if (errors.length) {
    for (const e of errors) console.error('SEO_META_FAIL ' + e)
    console.error(`SEO_META_ABORT=1 共 ${errors.length} 条（route_meta.json 是 12 语种唯一事实源，缺译文请人工补，脚本不做机翻）`)
    return 2
  }
  const routes = Object.keys(parsed.doc.routes).length
  if (mode === '--check') {
    let s
    try {
      s = sha256(DST)
    } catch {
      console.error(`SEO_META_ABORT=2 后端副本不存在：${DST}（跑 bash scripts/sync_seo_meta.sh）`)
      return 2
    }
    if (s !== sha256(SRC)) {
      console.error(`SEO_META_ABORT=3 SEO meta 不同步 src=${sha256(SRC).slice(0, 12)} dst=${s.slice(0, 12)}`)
      return 2
    }
    console.log(`SEO_META_CHECK_OK=1 routes=${routes} langs=${parsed.doc.languages.length} sha=${s.slice(0, 12)}`)
    return 0
  }
  console.log(`SEO_META_VALID_OK=1 routes=${routes} langs=${parsed.doc.languages.length}`)
  return 0
}

// 只在直接执行时跑（被 import 时只导出 validate，方便单测复用同一把尺子）。
if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  process.exit(main())
}

export { validate, EXPECT_LANGS, ROUTE_ROBOTS }
