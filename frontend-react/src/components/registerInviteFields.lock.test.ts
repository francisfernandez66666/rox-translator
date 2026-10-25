// ============================================================================
// components/registerInviteFields.lock.test.ts — §6.4「三码不同文」注册表单锁（★ 2026-10-25 对账批 #28）
// 职责：把方案 §6.4 那条「两套邀请体系严格区分」在**注册表单这一面**钉成机械断言。
//
// 为什么要单独一条锁（不是洁癖）：
//   byd 案例的本体就是「组织编码 / 邀请码」语义混淆——改造前成员那一格（手输企业邀请码，落 invite_codes
//   凭证）与管理员那一格（新建企业代号，落 tenants.code）**挂的是同一个词典键 auth.orgCode**，
//   逐字同文「组织编码（新建组织时必填）」；AI 引导流那格写 auth.aiOrgCode「组织编码（加入企业时必填）」，
//   同样把凭证叫成代号。填错一格的人不会收到报错，只会静默加入错误的地方或撞一句 400。
//   同族第二次现形：个人档那格「好友邀请码」手输后**压根没随注册下发**（旧 doRegister 只读地址栏 ?ref=），
//   于是推荐奖励归属一声不响地没发生——输入框存在但不起作用，比少一个输入框更坏。
//
// 四条判据（一条一个病灶）：
// R1 射程：三枚新键在两形态（zh/en 面板词典 ＋ 十份 locales 全量词典）**全部存在且非空**——缺一份＝该语种看到裸 key。
// R2 同文：`好友邀请码` / `企业邀请码` / `组织编码` 三份用词在 12 语种里**两两不得逐字相同**（同文＝用户分不出手里是哪一件）。
// R3 语义：zh／en 侧「企业邀请码」必须带着该语的「邀请码」字样，「组织编码」必须**不带**它（代号那格不许自称凭证）。
// R4 接线：表单里三格各引哪一个键（判**渲染形态**，注释不算），外加个人档手输的好友码真被下发。
// R5 反证：五枚变异体各改一处，判据必须点名那一处（证明 R4 不是空转）。
// 运行：npx vitest run src/components/registerInviteFields.lock.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { zh as authZh, en as authEn } from '@/i18n/panels/auth'
import { dict as zhHant } from '@/i18n/locales/zh-hant'
import { dict as ja } from '@/i18n/locales/ja'
import { dict as ko } from '@/i18n/locales/ko'
import { dict as th } from '@/i18n/locales/th'
import { dict as ru } from '@/i18n/locales/ru'
import { dict as fr } from '@/i18n/locales/fr'
import { dict as ar } from '@/i18n/locales/ar'
import { dict as es } from '@/i18n/locales/es'
import { dict as pt } from '@/i18n/locales/pt'
import { dict as de } from '@/i18n/locales/de'

const DIR = fileURLToPath(new URL('.', import.meta.url))
// 本文件在 components/ 下，源件与词典都按相对路径取（同 inviteLinkSecurity.lock.test.ts 的读法）
const read = (rel: string) => readFileSync(DIR + rel, 'utf8')

/**
 * stripComments 剥掉说明性文字只留代码形态。
 * 这条锁的靶子里有「不许出现 t('auth.orgCode') 第二处」这类**负向腿**，
 * 而下面的文件头注释把每一枚反面例子都逐字写了一遍——不剥注释，锁一开局就红，
 * 且红的内容指向一份完全正确的代码（AGENTS §一·9 同族老坑）。
 */
function stripComments(src: string): string {
  const noBlocks = src.replace(/\/\*[\s\S]*?\*\//g, '')
  return noBlocks
    .split('\n')
    .filter((line) => !line.trimStart().startsWith('//'))
    .map((line) => {
      const i = line.indexOf('//')
      if (i < 0) return line
      const before = line.slice(0, i)
      const quotes = (before.match(/["']/g) || []).length
      return quotes % 2 === 0 ? before : line
    })
    .join('\n')
}

const FRIEND = 'auth.friendInvite'
const ENT_CODE = 'auth.orgInviteCode'
const ORG_CODE = 'auth.orgCode'
const ORG_CODE_PH = 'auth.orgCodeAdminPlaceholder'

// 十二份词典：zh／en 取 auth 面板原样，其余十语种取 locales/*.ts 全量词典
const DICTS: Array<[string, Record<string, string>]> = [
  ['zh', authZh as unknown as Record<string, string>],
  ['en', authEn as unknown as Record<string, string>],
  ['zh_hant', zhHant], ['ja', ja], ['ko', ko], ['th', th],
  ['ru', ru], ['fr', fr], ['ar', ar], ['es', es], ['pt', pt], ['de', de],
]

const LOGIN = stripComments(read('Login.tsx'))
const AIFLOW = stripComments(read('AiRegisterFlow.tsx'))

/** fieldKeyFailures R4 判据（抽成函数供反证复用）：喂两份表单源码，回接线缺陷清单 */
function fieldKeyFailures(login: string = LOGIN, aiflow: string = AIFLOW): string[] {
  const bad: string[] = []
  // 传统表单：成员格＝凭证、管理员格＝代号、个人格＝好友码，三格各引各的键
  if (!/label=\{t\('auth\.orgInviteCode'\)\}/.test(login)) bad.push('Login.tsx 成员那格没挂 auth.orgInviteCode（企业邀请码）')
  if (!/label=\{t\('auth\.orgCode'\)\}/.test(login)) bad.push('Login.tsx 管理员那格没挂 auth.orgCode（组织编码）')
  if (!/label=\{t\('auth\.friendInvite'\)\}/.test(login)) bad.push('Login.tsx 个人那格没挂 auth.friendInvite（好友邀请码）')
  // 代号格的占位不许再抄标签（同文＝等于没分区）
  if (!/placeholder=\{t\('auth\.orgCodeAdminPlaceholder'\)\}/.test(login)) bad.push('Login.tsx 组织编码格占位没走 auth.orgCodeAdminPlaceholder')
  if (!/placeholder=\{t\('auth\.orgCodeAdminPlaceholder'\)\}/.test(aiflow)) bad.push('AiRegisterFlow 组织编码格占位没走 auth.orgCodeAdminPlaceholder')
  // AI 流的成员格同样不许把凭证叫成代号
  if (!/\{t\('auth\.orgInviteCode'\)\}/.test(aiflow)) bad.push('AiRegisterFlow staff 格没挂 auth.orgInviteCode（企业邀请码）')
  if (/t\('auth\.aiOrgCode'\)/.test(aiflow)) bad.push("AiRegisterFlow 又把成员格叫成 auth.aiOrgCode（'组织编码（加入企业时必填）'＝代号冒充凭证）")
  // 同一个「组织编码」标签在一份表单里只许出现一次（第二次出现＝又有人把两格并成一格）
  const orgCodeRefs = (login.match(/t\('auth\.orgCode'\)/g) || []).length
  if (orgCodeRefs !== 1) bad.push(`Login.tsx 里 t('auth.orgCode') 调用点应恰为 1（管理员格标签），实测 ${orgCodeRefs}`)
  // 个人档手输的好友码必须真随注册下发（地址栏 ?ref= 只是兜底，不是唯一来源）
  if (!/ref:\s*friendRef/.test(login)) bad.push('Login.tsx 注册请求没下发 friendRef（手输好友码又被静默丢弃）')
  if (!/const friendRef =[\s\S]{0,160}form\.invite\.trim\(\)/.test(login)) bad.push('Login.tsx 的 friendRef 没读 form.invite（个人那格成了假输入框）')
  return bad
}

describe('§6.4 注册表单三码分区锁（好友邀请码／企业邀请码／组织编码）', () => {
  it('R1 十二语种：四枚键都在位且非空（缺一份＝该语种看到裸 key）', () => {
    for (const [lang, dict] of DICTS) {
      for (const key of [FRIEND, ENT_CODE, ORG_CODE, ORG_CODE_PH]) {
        expect(dict[key], `${lang} 缺 ${key}（十语种补译不许只补一半）`).toBeTruthy()
      }
    }
  })

  it('R2 十二语种：三份用词两两不得逐字同文（同文＝用户分不出手里是哪一件）', () => {
    const pairs: Array<[string, string]> = [[FRIEND, ENT_CODE], [ORG_CODE, ENT_CODE], [ORG_CODE_PH, ENT_CODE], [FRIEND, ORG_CODE]]
    for (const [lang, dict] of DICTS) {
      for (const [a, b] of pairs) {
        expect(dict[a], `${lang}：${a} 与 ${b} 逐字同文（「${dict[a]}」）`).not.toBe(dict[b])
      }
    }
  })

  it('R3 zh／en 语义：企业邀请码自称凭证、组织编码不许自称凭证', () => {
    // 判据两侧各钉一个该语的「码／凭证」词面与「代号」词面，词典改词时这条会红而不是静默失配
    const TERM: Record<'zh' | 'en', { code: string; cred: string }> = { zh: { code: '组织编码', cred: '邀请码' }, en: { code: 'Org code', cred: 'invite code' } }
    for (const [lang, dict] of DICTS.slice(0, 2) as Array<['zh' | 'en', Record<string, string>]>) {
      const { code, cred } = TERM[lang]
      expect(dict[ENT_CODE], `${lang}：企业邀请码那格没出现「${cred}」字样`).toContain(cred)
      expect(dict[ORG_CODE], `${lang}：组织编码那格没出现「${code}」字样`).toContain(code)
      expect(dict[ORG_CODE], `${lang}：组织编码那格自称「${cred}」＝代号冒充凭证（byd 混淆的源头形态）`).not.toContain(cred)
      // 占位那格必须把两件事说清：它是代号、不是凭证（zh/en 之外的语种只判非空，见 R1）
      expect(dict[ORG_CODE_PH], `${lang}：组织编码占位没点明「不是${cred}」`).toContain(cred)
    }
  })

  it('R4 接线：三格各引各的键、个人档手输好友码真被下发', () => {
    expect(fieldKeyFailures()).toEqual([])
  })

  it('R5 反证：五枚变异体各改一处，判据必须点名那一处（证明 R4 有射程）', () => {
    const cases: Array<[string, 'login' | 'aiflow', (s: string) => string, RegExp]> = [
      ['成员格退回组织编码标签', 'login', (s) => s.replace(/label=\{t\('auth\.orgInviteCode'\)\}/g, "label={t('auth.orgInvite')}"), /成员那格没挂/],
      ['组织编码格占位抄回标签', 'login', (s) => s.replace("placeholder={t('auth.orgCodeAdminPlaceholder')}", "placeholder={t('auth.orgCode')}"), /占位没走/],
      ['AI 流 staff 格叫回组织编码', 'aiflow', (s) => s.replace("{t('auth.orgInviteCode')}", "{t('auth.aiOrgCode')}"), /staff 格没挂/],
      ['好友码再次被静默丢弃', 'login', (s) => s.replace('ref: friendRef,', 'ref,'), /friendRef/],
      ['friendRef 不再读手输格', 'login', (s) => s.replace("&& form.invite.trim()) ? form.invite.trim() : ref", ") ? ref : ref"), /没读 form\.invite/],
    ]
    const SRC = { login: LOGIN, aiflow: AIFLOW } as const
    for (const [label, side, mutate, expectHit] of cases) {
      const mutated = mutate(SRC[side])
      expect(mutated === SRC[side], `反证 ${label} 的变异体没改动任何字节（锚漂了，这枚反证是空转的）`).toBe(false)
      const failures = side === 'login' ? fieldKeyFailures(mutated, AIFLOW) : fieldKeyFailures(LOGIN, mutated)
      expect(failures.length, `反证失效：${label} 判据却回空（这条锁是空转的）`).toBeGreaterThan(0)
      expect(failures.join('\n'), `反证 ${label} 没点名预期那一处`).toMatch(expectHit)
    }
  })
})
