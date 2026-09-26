// ============================================================================
// components/admin/KbP.dom.test.tsx — 知识库面板发布前 UAT 批G 断言锁（2026-09-25）
// 锁定的三组行为（全部等值锁/负向锁，不动既有测试已锁数字）：
//   ① ★ F-26：安全句（语言文化规范）Panel 按角色收口——
//      isSuper=false：新键提示 kb.safetyPlatformManaged 在 DOM，且该 Panel 内
//      select/input/button **各恰好 0 枚**（原表单控件不得渲染，负向清零）；
//      isSuper=true：反向锁——提示文案不在 DOM（等值 0 枚），Panel 内表单控件
//      select 恰好 6 枚 / input 恰好 3 枚 / button 恰好 3 枚（原表单完整在）。
//   ② ★ F-24：双语语料 / TMX 导入成功回执整串等值——kb.bitextDone 新值
//      「已提交，待平台审核」+ 追加新键 kb.bitextPendingNote 补充说明；
//      并静态负向锁 zh 词典 kb.bitextDone 不含「已写入」、kb.bitextImport 不含「写TM」。
//   ③ ★ F-14（移交项）：包授权列表「时间」列不再 UTC 裸切片——钉 TZ=Asia/Shanghai
//      断单元格等值命中 fmtDateTime 本地口径，且旧 UTC 切片字面值 0 枚。
// 运行：npx vitest run src/components/admin/KbP.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { KbP } from './KbP'
// ★ F-24 静态负向锁直接读面板词典（不依赖运行时渲染）
import { zh as kbZh } from '@/i18n/panels/kb'
// ★ F-14 等值锁的口径函数（与被测组件同一实现，钉同一时区取词）
import { fmtDateTime } from '@/lib/format'

// 后台上下文可控桩：isSuper/myLevel 逐用例改写（F-26 两角色态）
/** 上传向导空壳收到的 props（F-57 锁读这里）；mock 工厂在模块初始化期就会写它 */
let kbUploadProps: Record<string, unknown> = {}

const m = vi.hoisted(() => ({
  admin: { myLevel: 4, isSuper: true, activeTenantId: 0, orgs: [] as unknown[] },
  api: {
    kbPackages: vi.fn(), kbPackageCreate: vi.fn(), kbPackageDelete: vi.fn(),
    kbEntries: vi.fn(), kbEntryAdd: vi.fn(), kbEntryDelete: vi.fn(), kbEntryUpdate: vi.fn(),
    kbEntriesImport: vi.fn(), bitextImport: vi.fn(), tmxImport: vi.fn(), tmxExport: vi.fn(),
    kbPackageStatus: vi.fn(), kbPackageShare: vi.fn(), kbIndexRebuild: vi.fn(),
    kbPackGrants: vi.fn(), kbPackGrantSet: vi.fn(), adminUsers: vi.fn(),
    safetyPhrases: vi.fn(), safetyPhraseAdd: vi.fn(), safetyPhraseDelete: vi.fn(),
    safetyPhraseStatus: vi.fn(), safetyBulkImport: vi.fn(),
  },
  orgList: vi.fn(),
}))
vi.mock('@/api', () => m.api)
vi.mock('@/api/org', () => ({ orgList: m.orgList }))
vi.mock('@/stores/admin', () => ({ useAdmin: () => m.admin }))
// 子面板与上传向导换成空壳：本测试只看 KbP 本体的渲染收口，不拉起兄弟面板的接口链
vi.mock('./DataSourcesP', () => ({ default: () => null }))
vi.mock('./BrandTermsP', () => ({ default: () => null }))
vi.mock('./IndustriesP', () => ({ default: () => null }))
vi.mock('./PersonasP', () => ({ default: () => null }))
// ★ F-57 锁需要拿到弹窗收到的 props（onSuccess 有没有接上），上传向导改成「记录 props 的空壳」
vi.mock('@/components/KbUploadDialog', () => ({
  default: (props: Record<string, unknown>) => { kbUploadProps = props; return null },
}))

// zh 字面值（与 panels/kb.ts 等值，改动词典即改这里——这是刻意的联动锁）
const SAFETY_TITLE = '语言文化规范（安全句 · Gate 闸门）'
const SAFETY_HINT = '规则注入 AI 翻译上下文；禁用词在译文出现时标记违规（可在系统设置开启拦截）'
const PLATFORM_MANAGED = '安全句（语言文化规范）由平台统一维护，无需企业配置。'
const PENDING_NOTE = '审核通过后会自动进入翻译记忆，无需再次导入。'

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  // 后台上下文默认回到超管态（F-26 反向锁用例用），逐用例自行改写
  m.admin.myLevel = 4
  m.admin.isSuper = true
  m.admin.activeTenantId = 0
  m.admin.orgs = []
  m.api.kbPackages.mockResolvedValue({ success: true, packages: [] })
  m.api.safetyPhrases.mockResolvedValue({ success: true, phrases: [], total: 0 })
  m.orgList.mockResolvedValue({ success: true, orgs: [] })
})

/** 找到「语言文化规范（安全句）」那张 Panel 卡片（Panel 容器 = .panel-card，标题在其 h2） */
function safetyPanel(): HTMLElement {
  const found = Array.from(document.querySelectorAll('.panel-card'))
    .find((el) => el.querySelector('h2')?.textContent === SAFETY_TITLE)
  if (!found) throw new Error('安全句 Panel 未渲染')
  return found as HTMLElement
}

describe('安全句 Panel · 角色收口（★ F-26）', () => {
  it('isSuper=false：提示文案在 DOM；Panel 内 select/input/button 各恰好 0 枚（原表单控件不渲染）', async () => {
    m.admin.isSuper = false
    m.admin.myLevel = 3 // 企业管理员（tenant_admin）视角
    render(<KbP />)
    await vi.waitFor(() => { expect(m.api.kbPackages).toHaveBeenCalled() })
    // 等值锁：新键提示整句上屏
    expect(screen.getByText(PLATFORM_MANAGED)).toBeTruthy()
    const panel = safetyPanel()
    // 负向清零锁：改不了的表单控件一枚都不许出现
    expect(panel.querySelectorAll('select').length).toBe(0)
    expect(panel.querySelectorAll('input').length).toBe(0)
    expect(panel.querySelectorAll('button').length).toBe(0)
    // 超管专属内容不得越界：旧提示行与规则搜索框在整份文档里都是 0 枚
    expect(screen.queryByText(SAFETY_HINT)).toBeNull()
    expect(document.querySelectorAll('input[placeholder="搜索规则内容 / 替换词…"]').length).toBe(0)
  })

  it('isSuper=true（反向锁）：提示文案 0 枚；Panel 内 select 恰好 6 / input 恰好 3 / button 恰好 3（原表单完整在）', async () => {
    render(<KbP />)
    await vi.waitFor(() => { expect(m.api.kbPackages).toHaveBeenCalled() })
    // 负向锁：新提示只对非 super 出现
    expect(screen.queryByText(PLATFORM_MANAGED)).toBeNull()
    const panel = safetyPanel()
    // 等值锁：4 枚过滤下拉 + 新增行的语种/类型 2 枚下拉 = 6
    expect(panel.querySelectorAll('select').length).toBe(6)
    // 规则搜索框 + 新增语句框 + 批量 JSON 框 = 3（kind=style 态无替换输入框）
    expect(panel.querySelectorAll('input').length).toBe(3)
    // 搜索 + 新增 + 批量导入 = 3
    expect(panel.querySelectorAll('button').length).toBe(3)
    // 超管仍看到原提示行
    expect(screen.getByText(SAFETY_HINT)).toBeTruthy()
  })
})

describe('双语/TMX 导入成功回执（★ F-24 文案）', () => {
  it('双语导入回执整串等值：「已提交，待平台审核」+ 追加 bitextPendingNote', async () => {
    m.api.bitextImport.mockResolvedValue({ success: true, added: 3, skipped: 1 })
    render(<KbP />)
    await vi.waitFor(() => { expect(m.api.kbPackages).toHaveBeenCalled() })
    // 第一枚 file input = 双语对照表（csv/xlsx）
    const file = new File(['a|b'], 'bitext.csv', { type: 'text/csv' })
    fireEvent.change(document.querySelectorAll('input[type="file"]')[0], { target: { files: [file] } })
    fireEvent.click(screen.getByRole('button', { name: '导入双语语料' }))
    await vi.waitFor(() => { expect(m.api.bitextImport).toHaveBeenCalledTimes(1) })
    // 等值锁：回执整串（done 新值 + 计数 + 追加的 pendingNote）；findBy* 等 React flush
    expect(await screen.findByText(`已提交，待平台审核 +3 / 跳过 1 · ${PENDING_NOTE}`)).toBeTruthy()
  })

  it('TMX 导入回执整串等值：单元数 + 新 done + 追加 bitextPendingNote', async () => {
    m.api.tmxImport.mockResolvedValue({ success: true, tus: 12, added: 30, skipped: 2 })
    render(<KbP />)
    await vi.waitFor(() => { expect(m.api.kbPackages).toHaveBeenCalled() })
    // 第二枚 file input = TMX
    const file = new File(['<tmx/>'], 'mem.tmx', { type: 'application/xml' })
    fireEvent.change(document.querySelectorAll('input[type="file"]')[1], { target: { files: [file] } })
    fireEvent.click(screen.getByRole('button', { name: '导入 TMX' }))
    await vi.waitFor(() => { expect(m.api.tmxImport).toHaveBeenCalledTimes(1) })
    // findByText：等接口 resolve 后的状态刷新落到 DOM 再断言
    expect(await screen.findByText(`12 个双语单元 · 已提交，待平台审核 +30 / 跳过 2 · ${PENDING_NOTE}`)).toBeTruthy()
  })

  it('静态负向锁（zh 词典）：kb.bitextDone 不含「已写入」、kb.bitextImport 不含「写TM」，新键值等值', () => {
    // 等值锁 + 负向锁双保险：硬承诺字样不得复活
    expect(kbZh['kb.bitextDone']).toBe('已提交，待平台审核')
    expect(kbZh['kb.bitextDone'].includes('已写入')).toBe(false)
    expect(kbZh['kb.bitextImport']).toBe('导入双语语料')
    expect(kbZh['kb.bitextImport'].includes('写TM')).toBe(false)
    expect(kbZh['kb.bitextPendingNote']).toBe(PENDING_NOTE)
    expect(kbZh['kb.safetyPlatformManaged']).toBe(PLATFORM_MANAGED)
  })
})

describe('包授权列表时间列（★ F-14 移交项 · 本地时区接线）', () => {
  // vitest 4 已删除 it 的 timezone 选项（类型不接受、运行时静默忽略＝假钉），
  // 故在测试体内直接改写 process.env.TZ（Node 20 实测对 Intl 即时生效），finally 恢复不污染同文件其他用例
  it('TZ=Asia/Shanghai：单元格等值命中 fmtDateTime 本地口径；旧 UTC 裸切片字面值 0 枚', async () => {
    const tzOld = process.env.TZ
    process.env.TZ = 'Asia/Shanghai'
    try {
    m.api.kbPackages.mockResolvedValue({
      success: true,
      packages: [{ id: 7, name: '包A', code: 'a', pack_type: 'tenant', enabled: 1, entry_count: 0 }],
    })
    // 23:30 UTC 在 UTC+8 已是次日 07:30 —— 裸切片会显示成「2026-09-25 23:30」（跨日错显示），
    // 正是本锁要抓的旧行为
    m.api.kbPackGrants.mockResolvedValue({
      success: true,
      grants: [{ id: 1, user_id: 9, username: 'alice', display_name: 'Alice', role: 'read', created_at: '2026-09-25T23:30:00Z' }],
    })
    m.api.adminUsers.mockResolvedValue({ success: true, users: [] })
    render(<KbP />)
    // 等包列表行渲染出来再点「授权」（行操作链接随列表 state flush 上屏）
    const grantLink = await screen.findByText('授权')
    fireEvent.click(grantLink)
    await vi.waitFor(() => { expect(m.api.kbPackGrants).toHaveBeenCalledTimes(1) })
    // 等值锁：UTC+8 下这一时刻本地是 09-26 07:30——整串等值命中恰好 1 枚；waitFor 等弹窗 state flush
    await vi.waitFor(() => { expect(screen.getAllByText(fmtDateTime('2026-09-25T23:30:00Z')).length).toBe(1) })
    // 负向锁：旧裸切片产物 `slice(0,16)`（保留 T）与「fmtTime 不带 local」的字面值都不得出现在 DOM
    expect(screen.queryAllByText('2026-09-25T23:30').length).toBe(0)
    expect(screen.queryAllByText('2026-09-25 23:30:00').length).toBe(0)
    // 等值锁的字面值本身钉死（防 fmtDateTime 口径漂移成非本地时区产物）
    expect(fmtDateTime('2026-09-25T23:30:00Z')).toContain('07:30')
    } finally {
      // 恢复宿主时区，避免泄漏给同文件/同 worker 后续用例
      if (tzOld === undefined) delete process.env.TZ
      else process.env.TZ = tzOld
    }
  })
})

// ============================================================================
// ★ 2026-09-26 〇-U 批 I-8 · F-57「写成功后同屏不重取」回归锁
// 缺陷形状：上传向导导入成功 → 只清了弹窗自己的临时态，KbP 的包列表与「查看条目（N）」
// 计数仍是导入前的值（本轮实测导入后计数纹丝不动），运营判断不出写没写进去。
// 锁的形状：**接线锁 + 效果锁**两层——① 弹窗确实收到了 onSuccess（属性没漏传），
// ② 触发 onSuccess 后包列表接口真的被重新调用（不是传了个空函数应付）。
// 反证：把 KbP 的 onSuccess 属性删掉 ⇒ ① 判红；换成空函数 ⇒ ② 判红。两层各管一段退化，
// 所以不做「自证式负向用例」（渲染 KbP 必然把 props 写回来，那种用例恒绿、属假绿）。
// ============================================================================
describe('上传向导写成功后重取包列表（★ F-57）', () => {
  it('KbP 给 KbUploadDialog 接了 onSuccess，且调用它确实重拉 kbPackages', async () => {
    m.api.kbPackages.mockResolvedValue({
      success: true,
      packages: [{ id: 7, name: '包A', code: 'a', pack_type: 'tenant', enabled: 1, entry_count: 34 }],
    })
    render(<KbP />)
    await vi.waitFor(() => { expect(m.api.kbPackages).toHaveBeenCalled() })
    const before = m.api.kbPackages.mock.calls.length

    // ① 接线锁：向导组件收到了 onSuccess 回调（没接 ⇒ 本缺陷原样存在）
    expect(typeof kbUploadProps.onSuccess, 'KbUploadDialog 必须接到 onSuccess 回调').toBe('function')

    // ② 效果锁：模拟「导入成功」时机（真实实现里由 r.success 分支触发）
    ;(kbUploadProps.onSuccess as () => void)()
    await vi.waitFor(() => { expect(m.api.kbPackages.mock.calls.length).toBeGreaterThan(before) })
  })

})
