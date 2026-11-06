// 前后端「参数名／入参键名」跨语言等值闸（★ 2026-11-05 一期收尾批新立，同日扩到 POST 体）
// 为什么需要它：产品面首版里，前端下载口传 `id` 而后端读 `asset_id`、confirm 口传 `id` 而后端读
// `fact_id`、SEO 口传 `indexable` 而后端读 `on`——三处同一类裂缝，而 vitest 全量 mock 掉了 api 层
// ⇒ 类型对、构建过、单测绿，只有真打 HTTP 才现形（且 PG 矩阵撞出来时已经晚了一整个批次）。
// 这道闸按**源码文本**比对两侧的名字（查询键＋JSON 体键），把这一类裂缝提到构建期。
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const ROOT = join(__dirname, '..', '..', '..')
const FE = readFileSync(join(ROOT, 'frontend-react/src/api/product.ts'), 'utf8')
// ★ 2026-11-06 改**派生**而不是手写清单：产品面新开一个文件（本轮 product_asset_file.go、
//   product_onboard_draft.go 两次都是这样）时，写死的三行清单会**静默少读一个口**
//   ⇒ 症状恰好是这条锁要防的那件事："前端调的是不存在的口"，而真凶只是清单没跟上。
//   口径同 AGENTS §一·17「同步脚本刻意不枚举清单（忘记更新清单才是根因）」。
const BE_FILES = readdirSync(join(ROOT, 'backend-go/internal/api'))
  .filter((n) => /^product.*\.go$/.test(n) && !n.endsWith('_test.go'))
  .sort()
  .map((n) => `backend-go/internal/api/${n}`)
const BE = BE_FILES.map((f) => readFileSync(join(ROOT, f), 'utf8')).join('\n')

// 抽取前端每个 GET 口实际带的查询键：withQuery('/path', { a, b: x })
function feQueryPairs(src: string): Array<{ path: string; keys: string[] }> {
  const out: Array<{ path: string; keys: string[] }> = []
  const re = /withQuery\('([^']+)',\s*\{([^}]*)\}\)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(src))) {
    const keys = m[2]
      .split(',')
      .map((s) => s.trim().split(':')[0].trim())
      .filter((s) => /^[a-z_][a-z0-9_]*$/.test(s))
    out.push({ path: m[1], keys })
  }
  return out
}

// 抽取后端注册的路径与其**真正读得到**的查询键与 JSON 体键（一次遍历，两面共用一把尺子）。
// ★ 必须跟着调用链走两层：本仓的短码解析收在 `productCodePair(r)`、入参解码收在
//   `productAnswerInput(r)` 这类助手里（§5.7 要求两面同尺），只扫 handler 体就会报
//   "后端没读 tenant_code"——那是判据自己少读了一层，不是契约裂了（AGENTS §三「判据级代替不了守卫级」同款）。
function beRouteKeys(src: string): { query: Map<string, Set<string>>; body: Map<string, Set<string>> } {
  const helpers = funcsTakingRequest(src)
  const query = new Map<string, Set<string>>()
  const body = new Map<string, Set<string>>()
  const scanQuery = (text: string, into: Set<string>) => {
    for (const k of text.matchAll(/URL\.Query\(\)\.Get\("([a-z_]+)"\)/g)) into.add(k[1])
  }
  for (const m of src.matchAll(/s\.mux\.HandleFunc\("([^"]+)",\s*s\.(handle[A-Za-z0-9_]+)\)/g)) {
    const [, path, fnName] = m
    const q = new Set<string>()
    const jk = new Set<string>()
    const seen = new Set<string>()
    const walk = (name: string, depth: number) => {
      if (depth > 2 || seen.has(name)) return
      seen.add(name)
      // handler 是方法、助手是包级裸函数 ⇒ 两种声明形态都要认（只认方法会让解码助手整体取空）
      const text =
        balancedBody(src, src.indexOf('func (s *Server) ' + name + '(')) || helpers.get(name) || ''
      if (!text) return
      scanQuery(text, q)
      collectJsonTags(text, jk)
      unionStructDefs(text, src, jk)
      for (const h of helpers.keys()) {
        if (h === name) continue
        if (!new RegExp('\\b' + h + '\\(').test(text)) continue
        walk(h, depth + 1)
      }
    }
    walk(fnName, 0)
    query.set(path, q)
    body.set(path, jk)
  }
  return { query, body }
}

const pairs = feQueryPairs(FE)
const beKeys = beRouteKeys(BE)
const routes = beKeys.query

// 前端 POST 体：request('/path', postBody({ k, k2: v })) ——只认**字面量对象**。
// 三个"整个 data 交进来"的口（productSave／factUpsert／assetPut）结构性取不到键名，
// 所以本闸的射程＝字面量那几处；缺的那一角由 Q3 的**下界自证**兜（扫得不够＝闸门失明，判红而不是判绿）。
function fePostPairs(src: string): Array<{ path: string; keys: string[] }> {
  const out: Array<{ path: string; keys: string[] }> = []
  const split = (inner: string) =>
    inner
      .split(',')
      .map((s) => s.trim().split(':')[0].trim())
      .filter((s) => /^[a-z_][a-z0-9_]*$/.test(s))
  // 形态一：request('/path', postBody({ ... }))
  for (const m of src.matchAll(/request\('([^']+)',\s*postBody\(\{([^}]*)\}\)\)/g)) {
    out.push({ path: m[1], keys: split(m[2]) })
  }
  // 形态二：request(withQuery('/path', { ... }), postBody({ ... })) ——父级在 query、本体在 body
  for (const m of src.matchAll(
    /request\(\s*withQuery\('([^']+)',\s*\{[^}]*\}\),\s*postBody\(\{([^}]*)\}\)\)/g,
  )) {
    out.push({ path: m[1], keys: split(m[2]) })
  }
  return out
}

// balancedBody 从 at 起按花括号配平取整段（函数体／struct 定义都靠它，行号相邻 ≠ 同一结构）。
function balancedBody(src: string, at: number): string {
  if (at < 0) return ''
  const start = src.indexOf('{', at)
  if (start < 0) return ''
  let depth = 0
  for (let i = start; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') {
      depth--
      if (depth === 0) return src.slice(at, i + 1)
    }
  }
  return src.slice(at)
}

// collectJsonTags 取一段文本里声明的全部 json 标签名。
// ★ 判据形状＝**反引号紧贴 json:**，即 Go 的 struct tag 本体；测试里那些用原始字符串写的
//   请求体（`{"id":1}`）不算声明——把"测试在造 body"读成"接口在解 body"，改名式破坏就能逃过射程。
function collectJsonTags(text: string, into: Set<string>): void {
  for (const m of text.matchAll(/`\s*json:"([a-z_0-9]+)(?:,.*)?"\s*`/g)) into.add(m[1])
}

// funcsTakingRequest 本包内所有"收 *http.Request 的函数"名→体（入参解码常收在助手里，只扫 handler 体会漏）。
function funcsTakingRequest(src: string): Map<string, string> {
  const out = new Map<string, string>()
  const re = /^func(?: \([^)]*\))? ([A-Za-z_][A-Za-z0-9_]*)\([^)]*\*http\.Request[^)]*\)/gm
  for (const m of src.matchAll(re)) out.set(m[1], balancedBody(src, m.index!))
  return out
}

// unionStructDefs 把一段代码里 `var x SomeType` 引用的那些 struct 定义的标签并进来。
// ★ 具名类型**不要求首字母大写**：本仓的请求体类型（productFactReq／productAssetReq／productAnswerReq）
//   全是包内私有＝小写开头，按 `[A-Z]` 筛会让这三处的标签整个取不到——那正是"判据读空、把自己判成闸门坏了"的形态。
//   真判据是"src 里确实有 `type <名> struct`"，名字形状不参与判定。
function unionStructDefs(code: string, src: string, into: Set<string>): void {
  for (const t of code.matchAll(/var\s+\w+\s+([A-Za-z_]\w*)\b/g)) {
    const at = src.indexOf('type ' + t[1] + ' struct')
    if (at >= 0) collectJsonTags(balancedBody(src, at), into)
  }
}

const postPairs = fePostPairs(FE)
const bodyRoutes = beKeys.body

describe('前后端入参名同一把尺（§5.1／§9.1：查询键＋JSON 体键）', () => {
  it('Q0 射程自证：前端 GET 口与后端注册口都必须扫得到（扫到 0 个＝闸门失明，不判绿）', () => {
    expect(pairs.length).toBeGreaterThanOrEqual(5)
    expect(routes.size).toBeGreaterThanOrEqual(10)
    // 后端侧扫描面：派生清单必须真的读到产品面那批文件（读空＝本锁整体空转）
    expect(BE_FILES.length, `只派生出 ${BE_FILES.length} 个 product*.go ⇒ 扫描面变了，不是"都过了"`).toBeGreaterThanOrEqual(4)
  })

  it('Q1 每个 GET 口的每个查询键，后端必须真的读它', () => {
    const bad: string[] = []
    for (const { path, keys } of pairs) {
      const read = routes.get(path)
      if (!read) { bad.push(`${path} 未在后端注册（前端调的是不存在的口）`); continue }
      for (const k of keys) if (!read.has(k)) bad.push(`${path} 前端传 ${k}，后端没读 ${k}`)
    }
    expect(bad, '参数名两侧不一致（单测 mock 看不见，只有真 HTTP 才炸）：\n' + bad.join('\n')).toEqual([])
  })

  it('Q2 反证：把前端一个键改名，Q1 的判据函数必须判红（证明这条锁有射程）', () => {
    const mutated = FE.replace("withQuery('/api/product/asset/download', { id }", "withQuery('/api/product/asset/download', { asset_id }")
    expect(mutated).not.toBe(FE) // 改名必须真落到靶子上，空替换＝假反证
    const bad: string[] = []
    for (const { path, keys } of feQueryPairs(mutated)) {
      const read = routes.get(path)
      if (!read) continue
      for (const k of keys) if (!read.has(k)) bad.push(`${path}:${k}`)
    }
    expect(bad).toContain('/api/product/asset/download:asset_id')
  })

  it('Q3 射程自证（体）：前端字面量 POST 口与后端注册口都必须扫得到（扫到 0 个＝闸门失明，不判绿）', () => {
    expect(postPairs.length).toBeGreaterThanOrEqual(5)
    expect(bodyRoutes.size).toBeGreaterThanOrEqual(10)
  })

  it('Q4 每个 POST 口的每个体键，后端必须真的解它（首版三处裂缝：fact_id／asset_id／on）', () => {
    const bad: string[] = []
    for (const { path, keys } of postPairs) {
      const read = bodyRoutes.get(path)
      if (!read) { bad.push(`${path} 未在后端注册（前端调的是不存在的口）`); continue }
      for (const k of keys) if (!read.has(k)) bad.push(`${path} 前端送 ${k}，后端不认 ${k}`)
    }
    expect(bad, '入参键名两侧不一致（vitest 全量 mock 掉 api 层，只有真 HTTP 才炸）：\n' + bad.join('\n')).toEqual([])
  })

  it('Q5 反证：把后端一个 json 标签改名，Q4 的判据函数必须判红（证明这条锁有射程）', () => {
    const mutated = BE.replace('FactID     int64  `json:"id"`', 'FactID     int64  `json:"fact_id"`')
    expect(mutated).not.toBe(BE) // 改名必须真落到靶子上，空替换＝假反证
    const routes2 = beRouteKeys(mutated).body
    const bad: string[] = []
    for (const { path, keys } of postPairs) {
      const read = routes2.get(path)
      if (!read) continue
      for (const k of keys) if (!read.has(k)) bad.push(`${path}:${k}`)
    }
    expect(bad).toContain('/api/product/fact/confirm:id')
  })
})
