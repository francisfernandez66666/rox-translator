// ============================================================================
// api/bizRespGate.test.ts — 静态锁：接口层失败必须经 bizResp 收敛
// （AGENTS §一·5 ★2026-09-27 〇-U 立规；★ D-3 批 2026-09-29 建锁并收敛 6 个缺口文件）
//
// 为什么要有这条锁：后端 F-64①②③ 状态码诚实化之后，失败不再回 200＋{success:false}，
//   而是回结构化 4xx；core.ts 的 bizResp() 负责把这种失败体还原成历史信封形态
//   （details 摊平、401/403 照抛以触发重登录）。api 层任何一处**绕过 bizResp 直返裸 request**，
//   对应的面板就会对着抛出的 ApiError 白屏——这类缺口靠人工评审抓不住（2026-09-29 审计
//   就是在「看起来已接线」的 22 个文件里揪出 8 个），必须做成源码级闸门。
//
// 判据三腿（缺一即空转）：
//   ① 正向：逐行扫 src/api/*.ts 的「裸返点位」（return request(...)／await request(...)），
//      文件级粗判据（有裸返且整文件没接 bizResp ⇒ 红）＋ 逐处台账精判据（裸返点位的宿主函数
//      必须被存量台账或 ②b 读取保持抛出表点名，否则 ⇒ 红）。台账只减不增：还清一处不删条目也判红（防台账过期失真）。
//   ② 白名单：lead.ts / translate.ts 两个**刻意不接** bizResp 的豁免文件，必须确实含
//      「豁免理由」注释里的标记字样，缺了判红——防止后人只往白名单塞文件名、不写理由。
//   ②b 读取保持抛出表（READ_THROW_EXEMPT，★ D-3 批 2026-09-29 复核加档）：纯读取接口且调用点
//      靠异常通道出文案的那些，**必须一直是裸返**——bizResp 会把结构化 4xx 还原成 truthy 的
//      {success:false} 壳，读取于是被伪装成「空数据」（批 #42 的静默失败形态，
//      payhonesty_gate_test.go 的 mustNotBizResp 同判）。本表反向咬：包上 bizResp／没写理由／
//      函数消失，一律判红。与 ① 的收敛判据是一对**双向等值锁**，不是「越多 bizResp 越好」。
//   ③ 反证：在内存里把已收敛文件（scim.ts）的 bizResp 摘掉，锁必须立刻判红；
//      再把 lead.ts 的豁免注释抹掉，白名单腿必须立刻判红；又把 mybilling.ts 的理由注释抹掉、
//      把 referralFunnel 包上 bizResp，读取保持抛出腿必须立刻判红。
//      没有反证的守卫视为空转（本仓硬要求）。反证只改字符串、**绝不写盘**。
//
// node 环境：与 bizResp.test.ts 同口径直接读源文件做静态扫描，不 import 业务模块——
//   锁的射程是「源码写法」，也避免 api 层测试被 window/fetch 依赖在解析期拖崩。
// 运行：npx vitest run src/api/bizRespGate.test.ts
// ============================================================================
import { describe, expect, it } from 'vitest'
import { readdirSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

/** 本目录（src/api）——锁只扫接口层自己的文件，别的目录各有归属闸门 */
const API_DIR = dirname(fileURLToPath(import.meta.url))

/**
 * 不在扫描射程的基础设施文件（各有一句「为什么」）：
 * - core.ts：bizResp／request 的本体，统一 client 只能裸用 fetch（§4.2-2 正当豁免 1/2），不能被自己的锁判红；
 * - index.ts：barrel 纯再导出，不产生调用点；
 * - types.ts：纯类型声明文件，不产生调用点。
 */
const INFRA = new Set(['core.ts', 'index.ts', 'types.ts'])

/**
 * 白名单豁免文件 → 该文件源码里必须存在的「豁免理由」标记字样（②腿）。
 * - lead.ts：蜜罐「假成功」设计，失败也必须静默、不能向提交方揭穿拦截，刻意不接 bizResp
 *   （D-3 批已在文件头登记豁免理由注释，标记＝'bizResp 豁免'）；
 * - translate.ts：预估自带 `j?.success ? j : null` ＋静默 catch（设计而非疏漏，见文件内注释），
 *   SSE／健康检查非信封；文件头已登记「正当豁免」（§4.2-2），标记沿用该既有字样、文件零改动。
 * 新增豁免必须同时：往这里加一行 ＋ 在目标文件写明理由注释，只塞文件名不写理由＝判红。
 */
const EXEMPT: Record<string, string> = {
  'lead.ts': 'bizResp 豁免',
  'translate.ts': '正当豁免',
}

/**
 * 「读取保持抛出」豁免表（★ D-3 批 2026-09-29 复核加档）：文件 → 函数名列表。
 * 为什么要有第二档：①的收敛判据默认「信封调用一律包 bizResp」，但 bizResp 的产物是
 *   **truthy 的 {success:false} 壳**（core.ts:350-364：非 401/403 的结构化失败被摊平返回）。
 *   纯读取接口若调用点走的是**异常通道**（runGuarded 的 catch、useAsync 的 err、
 *   try/catch → bizFail），包上 bizResp 就等于把「取数失败」伪装成「空数据」——
 *   正是批 #42 定性的静默失败形态，也是 backend-go/internal/api/payhonesty_gate_test.go
 *   的 mustNotBizResp 表在 mybilling/billing 两处已点名禁止的写法。
 *   所以本表里的函数**必须**保持裸返，判据三腿：
 *     a) 点位腿：函数确实还是裸返（若被包上 bizResp ⇒ 红，防止两把锁互相拉锯）；
 *     b) 理由腿：函数体上方必须有『★ 读取保持抛出』注释（塞函数名不写理由 ⇒ 红，同 EXEMPT 口径）；
 *     c) 保鲜腿：表里点名的函数若在源码里已不存在 ⇒ 红（改名/删除要同步本表）。
 * 收录判据（新增必须逐条满足，否则走 LEGACY_UNWRAPPED 或直接收敛）：
 *   调用点确实靠异常出文案 ＋ 接口本身是纯读取 ＋ 写了理由注释。
 */
const READ_THROW_EXEMPT: Record<string, string[]> = {
  'mybilling.ts': ['myOverview', 'myOrders', 'myLedger', 'myRewards', 'myInvoices'],
  'referral.ts': ['referralFunnel', 'referralMy'],
}

/** READ_THROW_EXEMPT 函数体内必须存在的理由标记（b 腿） */
const READ_THROW_MARKER = '★ 读取保持抛出'

/** 测试文件不参与扫描（本锁自身源码里就含 'return request' 字面量，不排除会自伤误报） */
const isTestFile = (name: string) => name.includes('.test.')

/**
 * 存量裸返台账（★ 只减不增，2026-09-29 D-3 批实测快照，写法仿 logratchet 基线口径）。
 * 为什么需要它：正向①的**逐处**精判据若直接开在「信封形态未包 bizResp ⇒ 红」，
 *   会把此前批次只做了「文件级接线」审计（本批 6 个文件就是那样被审计漏掉的形态）时
 *   残留在已接线文件里的存量点位一并判红——那不是本批批准范围（AGENTS §三：不做无收益大爆炸）。
 *   于是按仓内成熟的棘轮手法：存量点名冻结、新增一律判红、还清必须删条目（否则判红）。
 * 粒度＝「文件 → 宿主函数名」（点位行号会随日常编辑漂移，函数名稳定且 diff 可读）。
 * D-3 批收敛的 6 个文件（branding/mybilling/ops/referral/scim/assistAdmin）已零裸返，**不得**进台账。
 */
const LEGACY_UNWRAPPED: Record<string, string[]> = {
  'admin.ts': ['userBulkImport', 'mailTemplatesGet', 'mailTemplatesSave', 'meContext'],
  'apikeys.ts': ['getOpenAPIDocs', 'saveOpenAPIDocs', 'previewOpenAPIDocs'],
  'auth.ts': ['login', 'ssoProviders', 'authMe', 'authRegister', 'sendEmailCode', 'registerConfig', 'forgotPassword', 'registerIndustries', 'deactivateAccount'],
  'billing.ts': ['usageMe', 'usageOrg', 'usageCost', 'billingOrders', 'billingConfigSave', 'billingQuota', 'billingQuotaSave', 'billingInvoices', 'manualConfirmOrders', 'plans', 'myPackage', 'autoRenewGet', 'adminPackageSettings', 'adminPackageSettingsSave', 'adminPayChannels', 'adminPayChannelsSave', 'adminQuoteCurrency', 'adminQuoteCurrencySave'],
  'coupons.ts': ['adminCoupons', 'adminCouponRedemptions'],
  'feedback.ts': ['createFeedback'],
  'flow.ts': ['flowConfig'],
  'kb.ts': ['kbEntriesImport'],
  'models.ts': ['adminModels', 'adminModelsSave', 'stageModels', 'stageModelsSave', 'adminPolicy'],
  'org.ts': ['orgBudgetSummary', 'orgTokenLimit'],
  'scrape.ts': ['scrapeSummary'],
  'system.ts': ['systemHealth'],
  'tasks.ts': ['adminTasks', 'myTasks'],
  'tenant.ts': ['tenantList', 'tenantCreate', 'tenantUpdate', 'tenantInviteEnabledGet', 'tenantSetStatus', 'tenantDelete', 'tenantExport', 'tenantErase'],
  'tickets.ts': ['ticketDelete', 'getSegments', 'getSegmentsByKey', 'saveSegments'],
  'tmreview.ts': ['adoptFeedbackTranslation', 'listMyTmReview'],
}

// 行首「裸返」点位：return request(...) / return request<...>(...)（含 return await request(...)）。
// 收敛后的写法是 return bizResp(() => request(...))——行首字面量变成 `return bizResp`，
// 所以这条判据用**行首锚定**即可，不需要任何 lookbehind 体操（可读性优先，同 AGENTS §一·7 口径）。
const BARE_RETURN = /^return\s+(?:await\s+)?request[<(]/

/**
 * 扫描源码，返回「裸返点位」列表（行号 ＋ 宿主函数名）。
 * 两类点位（对应上面两条形态判据）：
 *   1) 行首 return request(...)：函数直接裸返响应体；
 *   2) 同一行出现 `await request(`：表达式位置裸用（收敛形态是 `await bizResp(() => request(`，
 *      「await」直接跟「bizResp」，字面量 `await request` 不再存在，故子串判据成立且不误伤收敛形态）。
 * 注释行跳过（锁射程是执行代码；`// return request(...)` 这类说明文字不算点位）。
 * 宿主函数名＝点位行向上最近的一条 `function xxx` 声明（api 层函数全部平铺声明，无嵌套歧义）。
 */
function bareSites(content: string): { line: number; fn: string }[] {
  const lines = content.split('\n')
  const out: { line: number; fn: string }[] = []
  lines.forEach((raw, i) => {
    const t = raw.trim()
    if (t.startsWith('//') || t.startsWith('/*') || t.startsWith('*')) return // 注释行不算点位
    if (!BARE_RETURN.test(t) && !t.includes('await request')) return
    let fn = '(未归属到函数)'
    for (let j = i; j >= 0; j--) {
      const m = lines[j].match(/function\s+(\w+)/)
      if (m) { fn = m[1]; break }
    }
    out.push({ line: i + 1, fn })
  })
  return out
}

/** 读入 src/api 全部非测试源文件（文件名 → 源码文本）。反证腿复用本函数产物，只在内存里改字符串 */
function loadApiSources(): Map<string, string> {
  const m = new Map<string, string>()
  for (const f of readdirSync(API_DIR)) {
    if (!f.endsWith('.ts') || isTestFile(f)) continue
    m.set(f, readFileSync(join(API_DIR, f), 'utf8'))
  }
  return m
}

/** 注释行判定（// 行、块注释首行 /* 与续行 *）——三处切段/扫描共用同一口径 */
const isCommentLine = (line: string) => {
  const t = line.trim()
  return t.startsWith('//') || t.startsWith('/*') || t.startsWith('*')
}

/**
 * 切出「某个导出函数：连同紧贴其上的注释块 → 到下一个顶格 export **之前**（不含下一个函数的注释块）」的片段。
 * 为什么两头都要按行处理（2026-09-29 首跑真踩，别改回字符串 indexOf 版本）：
 *   ① 上界必须含注释：「读取保持抛出」的理由注释按本仓风格写在声明行**上方**（mybilling.ts 实测形态），
 *      只从声明行起切会把理由注释排除在片段外，b 腿（理由腿）整表误红；
 *   ② 下界必须停在「下一个函数的注释块之前」：按「下一个 \nexport 」切会把**下一个**函数的说明注释
 *      划进本片段，于是「上一个函数」能借「下一个函数的标记」蒙过 b 腿，而文件里最后一个函数（后面没有
 *      export 可借）反而判红——两个方向都是失真，不是严格性。
 * 与 Go 侧 payShellFnSegment 的差别正在这里：Go 只判「片段内不得出现 bizResp(」，不关心注释归属；
 *   本锁还要判理由注释，所以必须把注释归属切准。
 * 找不到返回 null（供保鲜腿判「函数已消失」）。
 */
function fnSegment(content: string, fn: string): string | null {
  const lines = content.split('\n')
  let decl = -1
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].includes(`function ${fn}(`)) { decl = i; break }
  }
  if (decl < 0) return null
  let from = decl
  while (from - 1 >= 0 && isCommentLine(lines[from - 1])) from-- // 吞掉紧贴的说明注释
  let next = decl + 1
  while (next < lines.length && !/^export\s/.test(lines[next])) next++ // 下一个顶格 export（没有就到文件尾）
  let to = next
  while (to - 1 > decl && isCommentLine(lines[to - 1])) to-- // 把属于下一个函数的注释剔回给它
  return lines.slice(from, Math.min(to, lines.length)).join('\n')
}

/**
 * 只留执行代码行（剥掉注释行）——供「函数是否被包上 bizResp」这类**形态判据**使用。
 * 为什么必须剥：理由注释里就写着「不包 bizResp」这类字样，直接对整片段做子串判定会自我误伤；
 *   同 bareSites 跳过注释行的口径（锁射程是执行代码）。
 */
function codeLines(seg: string): string {
  return seg.split('\n').filter((l) => !isCommentLine(l)).join('\n')
}

/**
 * 跑全部判据（①正向＋②白名单＋读取保持抛出的三腿），返回违规说明列表（空数组＝绿灯）。
 * 纯函数：入参是「文件名→源码」映射，反证腿传入内存改过的映射即可复跑，绝不写盘。
 */
function runGate(sources: Map<string, string>): string[] {
  const violations: string[] = []
  for (const [file, content] of sources) {
    if (INFRA.has(file)) continue
    // ②白名单腿：豁免文件必须确实含「豁免理由」注释标记；豁免文件不再做裸返扫描
    const marker = EXEMPT[file]
    if (marker !== undefined) {
      if (!content.includes(marker)) {
        violations.push(`${file}: 在白名单里，但源码找不到豁免理由标记「${marker}」——豁免必须写明理由，塞文件名不算`)
      }
      continue
    }
    const sites = bareSites(content)
    const readThrow = READ_THROW_EXEMPT[file] ?? []
    const detectedFns = new Set(sites.map((s) => s.fn))
    // ①正向·文件级粗判据：有「既非存量、又非读取保持抛出」的裸返点位，整文件却完全没接 bizResp ⇒ 红
    //   （审计口径的原样复刻：本批 6 个缺口文件当初就是被这条抓住的）
    const needWrap = sites.filter((s) => !readThrow.includes(s.fn))
    if (needWrap.length > 0 && !content.includes('bizResp(')) {
      violations.push(`${file}: 存在 ${needWrap.length} 处裸返 request 且整文件未接 bizResp——接口层失败必须经 bizResp 收敛（AGENTS §一·5）`)
    }
    // ①正向·逐处精判据：每个裸返点位的宿主函数必须被存量台账或读取保持抛出表点名；新增裸返 ⇒ 红
    const allowed = LEGACY_UNWRAPPED[file] ?? []
    for (const s of sites) {
      if (!allowed.includes(s.fn) && !readThrow.includes(s.fn)) {
        violations.push(`${file}:${s.line} ${s.fn}() 裸返 request（未包 bizResp 且不在存量台账）——新增缺口，判红`)
      }
    }
    // ①反向·台账保鲜腿：台账点名的函数实测已无裸返点位 ⇒ 红（还清不删条目＝台账失真，闸门只会越来越钝）
    for (const fn of allowed) {
      if (!detectedFns.has(fn)) {
        violations.push(`${file}: 台账条目 ${fn}() 已不存在裸返点位——已收敛，请把该函数从 LEGACY_UNWRAPPED 删除（棘轮只减不增）`)
      }
    }
    // 读取保持抛出 a) 点位腿：表里点名的函数**必须**仍是裸返；一旦被包上 bizResp ⇒ 红
    //   （两把锁必须同向：payhonesty 侧禁止读取走 bizResp，本侧若只放行不反向咬，
    //     下一次「统一收敛」就会把它再包回去，把读取失败重新伪装成空数据）
    // b) 理由腿：函数片段里必须含 READ_THROW_MARKER；c) 保鲜腿：函数必须还存在
    for (const fn of readThrow) {
      const seg = fnSegment(content, fn)
      if (seg === null) {
        violations.push(`${file}: 读取保持抛出表点名了 ${fn}()，但源码里找不到该函数——改名/删除必须同步本表`)
        continue
      }
      if (codeLines(seg).includes('bizResp(')) {
        violations.push(`${file}: ${fn}() 已被包上 bizResp——它是纯读取接口且调用点走异常通道，包上会把取数失败伪装成空数据（批 #42 形态，payhonesty 侧同判）`)
      }
      if (!detectedFns.has(fn)) {
        violations.push(`${file}: ${fn}() 在读取保持抛出表里，却扫不到裸返点位——判据失配，请核对函数写法`)
      }
      if (!seg.includes(READ_THROW_MARKER)) {
        violations.push(`${file}: ${fn}() 裸返但函数体内没有理由标记「${READ_THROW_MARKER}」——豁免必须写明理由，塞函数名不算`)
      }
    }
  }
  // 台账引用的文件必须还在（重命名/拆分文件时台账要同步改，否则精判据永远对不上号＝静默失效）
  for (const file of Object.keys(LEGACY_UNWRAPPED)) {
    if (!sources.has(file)) violations.push(`存量台账引用了不存在的文件 ${file}——文件改名/删除时必须同步台账`)
  }
  for (const file of Object.keys(READ_THROW_EXEMPT)) {
    if (!sources.has(file)) violations.push(`读取保持抛出表引用了不存在的文件 ${file}——文件改名/删除时必须同步本表`)
  }
  return violations
}

describe('bizRespGate 静态锁（AGENTS §一·5：接口层失败必须经 bizResp 收敛）', () => {
  // —— 正向腿：现网源码必须全绿 ——
  it('① src/api 全量扫描：无「未接 bizResp 的裸返文件」、无「台账外新增裸返点位」、无「过期台账条目」', () => {
    const v = runGate(loadApiSources())
    expect(v, '违规清单：\n' + v.join('\n')).toEqual([])
  })

  // —— 白名单腿：两个豁免文件的理由注释必须健在（present-but-unreasoned 即红）——
  it('② 白名单豁免登记锁：lead.ts / translate.ts 必须含各自的豁免理由标记', () => {
    const sources = loadApiSources()
    for (const [file, marker] of Object.entries(EXEMPT)) {
      const content = sources.get(file)
      expect(content, `豁免文件 ${file} 必须存在于 src/api（被删/改名要同步改白名单）`).toBeDefined()
      expect(content!, `${file} 必须含豁免理由标记「${marker}」`).toContain(marker)
    }
  })

  // —— 反证腿①：把已收敛文件的 bizResp 摘掉，锁必须立刻判红 ——
  // 本仓硬要求（没有反证的守卫视为空转）：只在内存里改字符串，不写盘。
  // 载体选 scim.ts：D-3 批刚收敛、点位少、形态代表性强（return bizResp(() => request(...))）。
  it('③a 反证：内存里摘掉 scim.ts 的 bizResp，锁立刻判红（证明正向腿真的在咬）', () => {
    const sources = loadApiSources()
    const original = sources.get('scim.ts')
    expect(original, '前置：scim.ts 必须存在').toBeDefined()
    // 前置基线自证：收敛前科要成立（真含 bizResp、且基线扫描下 scim.ts 零违规），
    //   否则「摘掉后判红」可能红在别处，反证就废了（假绿反证）。
    expect(original!).toContain('bizResp(')
    expect(bareSites(original!), 'scim.ts 基线必须零裸返点位').toEqual([])
    // 摘除法：`return bizResp(() => request(` → `return request(`（还原收敛前形态），
    //   同时文件里 bizResp( 字样清零，粗判据与逐处判据应同时起火。
    //   （用 split/join 而非 replaceAll：tsconfig lib 低于 ES2021，replaceAll 不过编译）
    const stripped = original!.split('bizResp(() => ').join('')
    expect(stripped).not.toContain('bizResp(')
    sources.set('scim.ts', stripped)
    const v = runGate(sources)
    expect(v.some((x) => x.includes('scim.ts')), `判红必须落在 scim.ts 上，实际违规：\n${v.join('\n')}`).toBe(true)
    // 两条腿都要咬到：文件级粗判据（整文件没接 bizResp）＋ 逐处精判据（scim 不在存量台账）
    expect(v.some((x) => x.includes('scim.ts') && x.includes('未接 bizResp')), '文件级粗判据必须起火').toBe(true)
    expect(v.some((x) => x.includes('scim.ts') && x.includes('不在存量台账')), '逐处精判据必须起火').toBe(true)
  })

  // —— 反证腿②：把 lead.ts 的豁免理由注释抹掉，白名单腿必须立刻判红 ——
  it('③b 反证：内存里删掉 lead.ts 的豁免理由注释，白名单腿立刻判红（证明②腿不是摆设）', () => {
    const sources = loadApiSources()
    const original = sources.get('lead.ts')
    expect(original, '前置：lead.ts 必须存在').toBeDefined()
    expect(original!).toContain('bizResp 豁免') // 基线：理由注释健在
    // 抹除法：把标记字样改成无关文字（点位代码一字不动，模拟「只塞白名单不写理由」）
    const unmarked = original!.split('bizResp 豁免').join('（豁免理由注释被删）')
    expect(unmarked).not.toContain('bizResp 豁免')
    sources.set('lead.ts', unmarked)
    const v = runGate(sources)
    expect(v.some((x) => x.includes('lead.ts') && x.includes('豁免理由')), `白名单腿必须起火，实际违规：\n${v.join('\n')}`).toBe(true)
  })

  // —— 反证腿③：把「读取保持抛出」的理由注释抹掉，b 腿必须对表里每个函数立刻判红 ——
  // 载体选 mybilling.ts：5 个接口全是纯读取＋调用点清一色 runGuarded，是本表最典型的存量。
  it('③c 反证：内存里抹掉 mybilling.ts 的「读取保持抛出」理由注释，理由腿逐函数判红', () => {
    const sources = loadApiSources()
    const original = sources.get('mybilling.ts')
    expect(original, '前置：mybilling.ts 必须存在').toBeDefined()
    // 基线自证：标记确实存在且出现 5 次（一次/函数），否则「抹掉后判红」可能红在别处＝假绿反证
    const occurrences = original!.split(READ_THROW_MARKER).length - 1
    expect(occurrences, `${READ_THROW_MARKER} 应逐函数各出现一次`).toBe(READ_THROW_EXEMPT['mybilling.ts'].length)
    // 抹除法：只动注释文字，点位代码一字不动（模拟「塞函数名不写理由」）
    const unmarked = original!.split(READ_THROW_MARKER).join('（理由注释被删）')
    expect(unmarked).not.toContain(READ_THROW_MARKER)
    sources.set('mybilling.ts', unmarked)
    const v = runGate(sources)
    const fired = READ_THROW_EXEMPT['mybilling.ts'].filter((fn) =>
      v.some((x) => x.includes('mybilling.ts') && x.includes(fn) && x.includes('理由标记')))
    expect(fired, `5 个函数都要各自起火，实际判红：\n${v.join('\n')}`).toEqual(READ_THROW_EXEMPT['mybilling.ts'])
  })

  // —— 反证腿④：把表里的读取重新包上 bizResp，a 腿必须判红 ——
  // 这条是「两把锁同向」的证明：本表不只是放行名单，还会**反向咬**越权收敛的写法
  //   （历史上 D-3 批就把 mybilling 五个读取整文件包走过一次，靠 payhonesty 侧才发现）。
  it('③d 反证：内存里把 referralFunnel 包上 bizResp，读取保持抛出点位腿判红', () => {
    const sources = loadApiSources()
    const original = sources.get('referral.ts')
    expect(original, '前置：referral.ts 必须存在').toBeDefined()
    // 基线自证：该函数在表里、当前是裸返、且执行代码里没有 bizResp
    expect(READ_THROW_EXEMPT['referral.ts']).toContain('referralFunnel')
    expect(bareSites(original!).map((s) => s.fn)).toContain('referralFunnel')
    const seg0 = fnSegment(original!, 'referralFunnel')
    expect(seg0, '前置：片段切得到 referralFunnel').not.toBeNull()
    expect(codeLines(seg0!), '基线：执行代码不得含 bizResp').not.toContain('bizResp(')
    // 包上 bizResp（还原 D-3 批那种「统一收敛」的写法，括号配平、只改这一处）
    const wrapped = original!.replace(
      "return request('/api/referral/funnel', { headers: authHeaders() })",
      "return bizResp(() => request('/api/referral/funnel', { headers: authHeaders() }))",
    )
    expect(wrapped, '前置：替换必须真的命中（写法漂移要同步本反证）').not.toBe(original)
    sources.set('referral.ts', wrapped)
    const v = runGate(sources)
    expect(v.some((x) => x.includes('referral.ts') && x.includes('referralFunnel') && x.includes('已被包上 bizResp')),
      `a) 点位腿必须咬住「读取被包上 bizResp」，实际违规：\n${v.join('\n')}`).toBe(true)
    // 「扫不到裸返点位」这条同腿的另一形态也要咬（包上后裸返点位消失）
    expect(v.some((x) => x.includes('referral.ts') && x.includes('referralFunnel') && x.includes('扫不到裸返点位')),
      `裸返点位消失同样判红，实际违规：\n${v.join('\n')}`).toBe(true)
  })
})
