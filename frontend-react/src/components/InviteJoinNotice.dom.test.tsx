// ============================================================================
// components/InviteJoinNotice.dom.test.tsx — §6.5 第 4 条「受邀落地页告知」这一面的行为锁
// （★ 2026-10-24 多身份 B4-d）
//
// 安全警示原本锁了三面（创建后弹窗／邀请管理页常驻条／列表每行风险标识，见
// admin/inviteLinkSecurity.lock.test.ts）。第四面是**受邀人自己看到的那一面**：
// 他点开一条管理员发来的链接，落地页必须在**他填任何东西之前**说清三件事——
//   ① 你在加入哪家企业（不是"注册一个自己的账号"）；
//   ② 加入后你的账号资料对企业管理员可见、企业知识库与企业预算随之打开；
//   ③ 这条链接如果已经用过／撤销／过期，为什么屏上还是登录框。
// 漏掉②＝让人盲填一份入职资料；漏掉③＝用户以为链接坏了但没人告诉他坏在哪。
//
// ★ 三条判据刻意分开，因为它们指向三个不同的下一步：
//   · 死链（服务端 valid=false）⇒ 文案点名"找企业管理员重新获取"；
//   · 取不到（请求本身失败）⇒ **不许**把人引去"重新获取"，只说"稍后再试"；
//   · 校验中 ⇒ 中性一句，不许预先渲染死链文案（否则用户会在合法链接上看到"已过期"）。
// 后端对前三档一律回 HTTP 200（见 invite_link.go handleInviteLinkPublic），
// 所以这一面拿得到 reason，而不是只看到一个报错页。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, render } from '@testing-library/react'
import { setLang } from '@/i18n'
import InviteJoinNotice, { type InvitePeek } from './InviteJoinNotice'

// 这一面不碰网络：Receipt 由每条用例直接喂进来
vi.mock('@/api', () => ({ API_BASE: '', getAuthToken: () => '', handleUnauthorized: vi.fn() }))

const KEY = {
  checking: 'auth.invite.checking',
  checkFail: 'auth.invite.checkFail',
  rInvalid: 'auth.invite.rInvalid',
  rUsed: 'auth.invite.rUsed',
  rRevoked: 'auth.invite.rRevoked',
  rExpired: 'auth.invite.rExpired',
  joinOrg: 'auth.invite.joinOrg',
  joinDept: 'auth.invite.joinDept',
  lockedEmail: 'auth.invite.lockedEmail',
  dataNote: 'auth.invite.dataNote',
  selfNote: 'auth.invite.selfNote',
} as const

beforeEach(() => setLang('zh'))
afterEach(() => cleanup())

const notice = (checking: boolean, peek: InvitePeek | null) => {
  const { container } = render(<InviteJoinNotice checking={checking} peek={peek} />)
  const el = container.querySelector('[data-testid="invite-notice"]') as HTMLElement
  return { el, text: el?.textContent || '' }
}

describe('受邀落地页告知块（§6.5 第 4 条第四面）', () => {
  it('校验中：出一句中性提示，且落在弱提示档（.auth-note）而不是红字档', () => {
    const { el, text } = notice(true, null)
    expect(el.className).toBe('auth-note')
    expect(el.dataset.state).toBeUndefined()
    expect(text).toContain('正在校验')
    // 反证护栏：合法与否还没定，不许提前把死链文案或"加入企业"主句渲染出来
    expect(text).not.toContain('已过期')
    expect(text).not.toContain('正通过邀请链接加入')
  })

  it('请求失败（peek=null）：出"稍后再试"，且绝不把人引去"请管理员重新生成"', () => {
    const { el, text } = notice(false, null)
    expect(el.className).toBe('auth-err')
    expect(el.dataset.state).toBe('checkfail')
    expect(text).toContain('暂时校验不了')
    // 死链四档的关键词一个都不许出现在这一档：
    // 「撤销／用过／过期／换一条新链接」都指向"去找管理员"，而这一次只是网络抖动
    for (const dead of ['已过期', '已被使用', '已被管理员撤销', '无效']) {
      expect(text).not.toContain(dead)
    }
  })

  it('死链四档各出一句、各自点名下一步，且统一落红字档 data-state=dead', () => {
    const cases: Array<[InvitePeek['reason'], string]> = [
      ['invalid', '无效'],
      ['used', '已被使用'],
      ['revoked', '已被管理员撤销'],
      ['expired', '已过期'],
    ]
    for (const [reason, expectText] of cases) {
      const { el, text } = notice(false, { valid: false, reason })
      expect(el.dataset.state, `reason=${reason}`).toBe('dead')
      expect(el.className, `reason=${reason}`).toBe('auth-err')
      expect(text, `reason=${reason}`).toContain(expectText)
      // 死链档一律不许出现"你将加入 X"主句：门是关的，别先给一个入职承诺
      expect(text).not.toContain('正通过邀请链接加入')
    }
  })

  it('reason 缺失或不在四档里：回落 invalid 那句，不许渲染成空白或裸 key', () => {
    for (const peek of [{ valid: false } as InvitePeek, { valid: false, reason: 'deleted' as unknown as InvitePeek['reason'] }]) {
      const { text } = notice(false, peek)
      expect(text).toContain('无效')
      expect(text).not.toContain('auth.invite.')
    }
  })

  it('合法链接：企业名主句 + 资料可见范围 + 一次性凭证提醒，三句缺一不可', () => {
    const { el, text } = notice(false, { valid: true, tenant_name: '某某跨境科技有限公司' })
    expect(el.dataset.state).toBe('valid')
    expect(text).toContain('某某跨境科技有限公司')
    // §6.5 第 4 条本体：可见范围必须在提交之前说，这一句被摘掉就是"盲填入职资料"
    expect(text).toContain('对企业管理员可见')
    expect(text).toContain('企业知识库')
    expect(text).toContain('企业预算')
    // 链接是凭证：不许转给别人的那一句也必须在同一屏
    expect(text).toContain('不要转发')
  })

  it('回执没给部门／没锁邮箱时：对应那句整句不出（不许留下未替换的占位符）', () => {
    const { text } = notice(false, { valid: true, tenant_name: 'A 公司' })
    expect(text).not.toContain('{dept}')
    expect(text).not.toContain('{email}')
    expect(text).not.toContain('部门')
    // 占位符检测：模板未替换时整串花括号会原样出现在客户屏幕上
    expect(text).not.toMatch(/\{[a-z]+\}/)
  })

  it('部门与锁定邮箱都在时：两句都出，且邮箱取的是回执里那一个（不是用户自己填的）', () => {
    const { text } = notice(false, {
      valid: true, tenant_name: 'A 公司', org_name: '翻译部',
      email_locked: true, bound_email: 'invited@example.com',
    })
    expect(text).toContain('翻译部')
    expect(text).toContain('invited@example.com')
  })

  it('只用登录卡既有两档视觉（.auth-note / .auth-err），不新增视觉档', () => {
    // 交付真值口径（AGENTS §一·5）：这一屏不许为一句提示再造一档颜色/字号。
    // 合法档的外层是一枚纯定位容器（无类名），真正带视觉档的是它里面每一句 <p>，
    // 所以这里收集的是**所有带类名的节点**，不是根节点那一个。
    const seen = new Set<string>()
    for (const p of [
      { checking: true, peek: null as InvitePeek | null },
      { checking: false, peek: null as InvitePeek | null },
      { checking: false, peek: { valid: false, reason: 'expired' as const } },
      { checking: false, peek: { valid: true, tenant_name: 'A' } },
    ]) {
      const { el } = notice(p.checking, p.peek)
      const add = (c: string) => { if (c.trim()) seen.add(c.trim()) }
      add(el.className)
      for (const n of Array.from(el.querySelectorAll('[class]'))) add(n.className)
    }
    expect([...seen].sort()).toEqual(['auth-err', 'auth-note'])
  })

  it('词典齐：本面 11 个键在 zh／en 双侧齐，且 12 语种全量词典逐键有值', async () => {
    const { zh, en } = await import('@/i18n/panels/auth')
    const keys = Object.values(KEY)
    for (const k of keys) {
      expect((zh as Record<string, string>)[k], `zh 缺 ${k}`).toBeTruthy()
      expect((en as Record<string, string>)[k], `en 缺 ${k}`).toBeTruthy()
    }
    // 十个非中文 locale 是全量词典（locales.core.test.ts 管键集，这里只补这一批的新键点名）
    const locales = ['zh-hant', 'ja', 'ko', 'th', 'ru', 'fr', 'ar', 'es', 'pt', 'de'] as const
    for (const code of locales) {
      const { dict } = await import(`@/i18n/locales/${code}`) as { dict: Record<string, string> }
      for (const k of keys) {
        expect(dict[k], `${code} 缺 ${k}`).toBeTruthy()
      }
    }
  })
})
