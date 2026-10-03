// ============================================================================
// TrialPanel.dom.test.tsx — 〇-Z 免登录试用面板（★ 2026-09-28）
//
// 射程（钉的是"分支走对没有"，不是译文质量）：
//   A 首屏装配：目标语种下拉＝zh ∪ /api/translation/langs 那份名单（去重、顺序稳定），
//     默认值按界面语种给（中文界面→en，其余→zh）；
//   B 前端预检不白跑：空文本就地提示，**一次请求都不该发**（发出去就是白吃一次限流计数）；
//   C 分支按稳定码判：TRIAL_EXHAUSTED→引导卡（三种 reason 三种话）、RATE_LIMITED→带秒数的
//     「稍后再试」且**不**切引导卡（那是手快，不是没额度，切卡等于把访客推去注册）；
//   D 剩 N 句只在成功响应后出现（计数纪律「成功才计」，首发时任何数字都是猜的）；
//   E 请求体三字段与后端 trialReq 同名，device_id 必须过服务端同款字符集正则——
//     这条是防"前端把设备号编码成了服务端不认的形态"，那样访客会永远拿到 400。
//
// 不在这里跑：真网络（@/api/trial 整体 mock）、HeroDemo 与热区的切换（见 Landing.trial.dom.test.tsx）。
// 运行：npx vitest run src/components/TrialPanel.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, render, screen, waitFor, cleanup } from '@testing-library/react'
import { setLang, t, tpl } from '@/i18n'

const mocks = vi.hoisted(() => ({
  // 参数类型必须显式声明成 Record<string, unknown>：vi.fn 若写成无参箭头函数，
  // mock.calls[i] 的元组类型就是 `[]`，C1 那条「取请求体核三字段」的断言会在 tsc 阶段
  // 报 TS2493「index 0 不存在」并连带把 body 推成 undefined（TS18048），
  // 于是这条能抓「前端把设备号编码成服务端不认的形态」的锁根本编译不过去。
  translate: vi.fn(async (_req: Record<string, unknown>): Promise<unknown> => ({ success: true })),
  langs: vi.fn(async (): Promise<unknown> => ({ ok: true, langs: [] })),
}))
vi.mock('@/api/trial', async () => {
  // TRIAL_MAX_CHARS 是真常量（与后端 trialMaxTextRunes 同值），mock 里必须原样带出，
  // 否则 textarea 的 maxLength 会变成"无上限"，超长拦截这条就测了个假东西。
  // 用 vi.importActual 而不是裸 import()：后者在自身 mock 工厂里拿到的就是被 mock 的壳，会自指。
  const real = await vi.importActual<typeof import('@/api/trial')>('@/api/trial')
  return { TRIAL_MAX_CHARS: real.TRIAL_MAX_CHARS, trialTranslate: mocks.translate, trialLangs: mocks.langs }
})

import TrialPanel from './TrialPanel'
import { TRIAL_MAX_CHARS } from '@/api/trial' // 经上方 mock 工厂原样带出的真常量（与后端 trialMaxTextRunes 同值）

const ok = (over: Record<string, unknown> = {}) => ({
  success: true, translation: 'The new model launch kickoff.', source_lang: 'zh', target_lang: 'en',
  mode: 'pro', left: 4, exhausted: false, ...over,
})

beforeEach(() => {
  cleanup()
  setLang('zh')
  localStorage.clear()
  mocks.translate.mockReset()
  mocks.langs.mockReset().mockResolvedValue({ ok: true, langs: [{ code: 'en', name: '英语', name_en: 'English' }] })
})
afterEach(() => { cleanup(); setLang('zh') })

const open = () => {
  render(<TrialPanel onBack={() => { /* 本文件不测退回演示，那一条在热区锁里 */ }} />)
}
const typeInto = (text: string) => fireEvent.change(screen.getByPlaceholderText(t('land.trial.placeholder')), { target: { value: text } })
const clickGo = () => fireEvent.click(screen.getByRole('button', { name: t('land.trial.go') }))

describe('TrialPanel · 首屏装配与分支', () => {
  it('A 语种下拉 = zh ∪ 名单，中文界面默认翻成英文', async () => {
    mocks.langs.mockResolvedValue({ ok: true, langs: [{ code: 'en', name: '英语', name_en: 'English' }, { code: 'de', name: '德语', name_en: 'German' }] })
    open()
    const sel = await screen.findByLabelText(t('land.trial.to'))
    // 顺序＝插入序：zh 恒在首位（显式补的），其后照名单走；语种名一律经 langLabel（zh 界面取中文名）
    expect([...sel.querySelectorAll('option')].map((o) => o.textContent)).toEqual(['中文', '英语', '德语'])
    expect((sel as HTMLSelectElement).value).toBe('en')
  })

  it('B 空文本就地提示，一次请求都不发', async () => {
    open()
    clickGo()
    expect(await screen.findByText(t('land.trial.errEmpty'))).toBeTruthy()
    expect(mocks.translate).not.toHaveBeenCalled()
  })

  it('B2 超过 300 字就地提示（服务端仍会同样判 400，这里只是不白跑一趟）', async () => {
    open()
    typeInto('汉'.repeat(TRIAL_MAX_CHARS + 1))
    clickGo()
    expect(await screen.findByText(t('land.trial.errTooLong'))).toBeTruthy()
    expect(mocks.translate).not.toHaveBeenCalled()
  })

  it('C1 成功：渲染译文、按响应更新剩余额度', async () => {
    mocks.translate.mockResolvedValue(ok({ translation: 'Kickoff is next week.' }))
    open()
    typeInto('新车发布启动会定在下周')
    clickGo()
    expect(await screen.findByText('Kickoff is next week.')).toBeTruthy()
    expect(screen.getByText(tpl('land.trial.left', { n: 4 }))).toBeTruthy()
    // 请求体三字段与后端 trialReq 同名；device_id 必须过服务端那道 ^[A-Za-z0-9_-]{8,64}$
    const body = mocks.translate.mock.calls[0][0]
    expect(Object.keys(body).sort()).toEqual(['device_id', 'target_lang', 'text'])
    expect(String(body.device_id)).toMatch(/^[A-Za-z0-9_-]{8,64}$/)
    expect(body.target_lang).toBe('en')
  })

  it('C2 TRIAL_EXHAUSTED(global)：出今日名额文案＋注册/登录两条出路', async () => {
    mocks.translate.mockResolvedValue({ success: false, code: 'TRIAL_EXHAUSTED', reason: 'global' })
    open()
    typeInto('新车发布启动会')
    clickGo()
    expect(await screen.findByText(t('land.trial.doneGlobal'))).toBeTruthy()
    expect(screen.getByText(t('land.trial.doneTitle'))).toBeTruthy()
    expect(document.querySelector('a[href="/register"]')).toBeTruthy()
    expect(document.querySelector('a[href="/login"]')).toBeTruthy() // 已有账号的人不该被"再注册一次"挡住
    expect(screen.queryByPlaceholderText(t('land.trial.placeholder'))).toBeNull() // 引导态不再收输入
  })

  it('C3 RATE_LIMITED：给秒数、留在输入态（手快≠没额度）', async () => {
    mocks.translate.mockResolvedValue({ success: false, code: 'RATE_LIMITED', retry_after: 3 })
    open()
    typeInto('新车发布启动会')
    clickGo()
    expect(await screen.findByText(tpl('land.trial.errFast', { n: 3 }))).toBeTruthy()
    expect(screen.getByPlaceholderText(t('land.trial.placeholder'))).toBeTruthy()
    expect(screen.queryByText(t('land.trial.doneTitle'))).toBeNull()
  })

  it('D 剩余额度只在成功响应后出现；服务端报 exhausted=true 即切引导卡', async () => {
    mocks.translate.mockResolvedValue(ok({ left: 0, exhausted: true }))
    open()
    expect(screen.queryByText(/剩 \d+ 句/)).toBeNull() // 首发前不许出现一个猜出来的数字
    typeInto('新车发布启动会')
    clickGo()
    await waitFor(() => expect(screen.getByText(t('land.trial.doneTitle'))).toBeTruthy())
  })

  it('E 名单接口挂了也有兜底常用语种，且给一句说明（默认 en 不能落进空选项）', async () => {
    mocks.langs.mockResolvedValue({ ok: false, langs: [] })
    open()
    const sel = await screen.findByLabelText(t('land.trial.to'))
    const codes = [...sel.querySelectorAll('option')].map((o) => (o as HTMLOptionElement).value)
    expect(codes).toContain('en') // 中文界面默认目标就是 en：兜底表里没有它，界面显示与实际提交就会分叉
    expect((sel as HTMLSelectElement).value).toBe('en')
    expect(screen.getByText(t('land.trial.langFail'))).toBeTruthy()
    typeInto('新车发布启动会')
    clickGo()
    await waitFor(() => expect(mocks.translate).toHaveBeenCalledTimes(1))
  })

  it('F 英文界面默认翻成中文（与演示卡的 EN→ZH 方向对齐）', async () => {
    setLang('en')
    open()
    const sel = await screen.findByLabelText(t('land.trial.to'))
    expect((sel as HTMLSelectElement).value).toBe('zh')
  })

  // ★ ㊵ 的等值锁补齐（2026-10-04 R-1 批）：默认档必须是**这一个事实**，不许成为可调项。
  // 为什么 A＋F＋本条要一起站着：R-1 现网抓到「中文界面点开首页试用＝默认 en＝100% 500」之后，
  // 最省事的"修复"是把默认档改成 zh（那条腿当时恰好活着）。那不是修缺陷，是把 P0 藏起来
  // ——访客再也看不到 500，但 Pro 翻译腿照旧死着，而且再没有人的屏幕上会出现它。
  // 所以这里把两档都钉成**等值**（zh 界面→en、非 zh 界面→zh），本条再补简/繁同一档：
  // 判据是 `lang.startsWith('zh')`，繁体若被单拎出来改成 zh，下面这条会红。
  // 反证（本轮真跑过，见 closeout 账）：把 defaultTarget 写成恒 'zh' ⇒ A 与本条红；
  //                          写成恒 'en' ⇒ F 红；写成 'zh_hant' 特判 ⇒ 本条红。
  it('F2 繁体中文界面与简体同档：默认仍是 en（简繁是一个书写体系）', async () => {
    setLang('zh_hant')
    open()
    const sel = await screen.findByLabelText(t('land.trial.to'))
    expect((sel as HTMLSelectElement).value).toBe('en')
  })
})
