// ============================================================================
// components/admin/inviteLinkSecurity.lock.test.ts — §6.5 安全警示四面锁 ＋ §6.4 命名隔离锁
// 职责：把两条「只靠人记就会漂」的产品口径变成机械断言——
// ① §6.5：企业邀请凭据是 API Key 级凭证，警示语必须在**每一面**都出（创建后即时弹窗、
//    邀请管理页常驻条、列表每行风险标识，★ §6.5 第 5 条再加一面：管理台「邀请码列表」——
//    后端手输码与专属链接共用 invite_codes 那一台状态机，风险面完全相同，警示却只出在链接那侧），
//    且文案**只有一个出处**（InviteLinkSecurityNote）；
//    历史同族病＝页脚尾注四面各写一份、报价公式两处各抄一遍，改一处漏三处。
// ② §6.4：本产品有三层「邀请」语义（个人邀请码／手输注册码／企业专属邀请链接），
//    企业链接的十二语种文案里**不许出现该语种「邀请码」的整串用词**，
//    否则用户会以为手里那枚码和那条链接是同一件东西（发错对象的代价＝凭证外泄）。
// 运行：npx vitest run src/components/admin/inviteLinkSecurity.lock.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { zh as orgZh, en as orgEn } from '@/i18n/panels/org'
import { zh as linkZh, en as linkEn } from '@/i18n/panels/invitelink'
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
const read = (name: string) => readFileSync(DIR + name, 'utf8')

/**
 * stripComments 剥掉说明性文字，只留代码形态。
 * 这条不是洁癖：本仓的负向锁被自己新写的注释喂饱过多次（§一·9「注释里不许出现那串字面」)——
 * 上面那几个文件的文件头注释里**逐字写着** `${location.host}/?invite=` 与 `inviteLink.warnBody`
 * 的反面例子，不剥注释这条锁一开局就红，而红的内容指向一份完全正确的代码。
 * 做法：整段 `/* *\/` 块删掉 → 整行 `//` 注释丢掉 → 行尾 `// …` 只在「前面引号成对」时丢掉
 * （`https://` 这类串里的 `//` 前面必有一个未闭合引号，因此不会被当注释切掉）。
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

const CARD = stripComments(read('InviteLinkCard.tsx'))
const PANEL = stripComments(read('InviteLinksP.tsx'))
const ORG = stripComments(read('OrgP.tsx'))
const USERS = stripComments(read('panels_a.tsx'))

/**
 * securityFaceFailures §6.5 四面判据（抽成函数供反证复用）：
 * 喂进这几份源码文本，回「哪一面没把警示语出出来」。判据问的是**渲染形态**
 * （组件调用点／风险标识列），不是注释里有没有提——注释冒充实现是本仓反复踩过的老坑。
 */
function securityFaceFailures(
  face: { card?: string; panel?: string; orgp?: string; usersp?: string; codes?: string } = {},
): string[] {
  const card = face.card ?? CARD
  const panel = face.panel ?? PANEL
  const orgp = face.orgp ?? ORG
  const usersp = face.usersp ?? USERS
  // codes 面与 usersp 面同在 panels_a.tsx 这一份文件里，但**必须各判各的**：
  // 合并成一件事就会长成「建号回执卡有警示 ⇒ 邀请码列表也算有了」，而那是两个面、两套凭据。
  const codes = face.codes ?? USERS
  const bad: string[] = []
  // 面一：创建后即时回执卡里必须挂警示组件（组件本体与它的导出都在同一文件，两处都要在）
  if (!/export function InviteLinkSecurityNote\b/.test(card)) bad.push('InviteLinkSecurityNote 组件不见了（唯一出处被摘）')
  if (!/<InviteLinkSecurityNote\s*\/>/.test(card)) bad.push('创建回执卡片里没渲染警示组件')
  // 面二：邀请管理页必须有常驻警示条（不是 toast、不随滚动消失）
  if (!/<InviteLinkSecurityNote\s*\/>/.test(panel)) bad.push('邀请管理页没有常驻警示条')
  // 面三：列表每行的风险标识（只在 active 行出，用现读词典键而不是硬编码中文）
  if (!/t\('inviteLink\.riskTag'\)/.test(panel)) bad.push('邀请链接列表行里没有风险标识 inviteLink.riskTag')
  // 面四（★ §6.5 第 5 条）：邀请码列表同一风险面 ⇒ 同一件警示条＋同一枚行内风险标识，缺一即红
  if (!/<InviteLinkSecurityNote\s*\/>/.test(codes)) bad.push('邀请码列表面板没有常驻警示条')
  if (!/t\('inviteLink\.riskTag'\)/.test(codes)) bad.push('邀请码列表行里没有风险标识 inviteLink.riskTag')
  // 警示文案唯一出处：warnTitle／warnBody 这两个键只许在组件文件里出现
  for (const key of ['inviteLink.warnTitle', 'inviteLink.warnBody']) {
    for (const [name, src] of [['InviteLinksP', panel], ['OrgP', orgp], ['panels_a', usersp]] as const) {
      if (src.includes(`'${key}'`)) bad.push(`${name} 自己抄了一份 ${key}（警示语必须只有一个出处）`)
    }
  }
  // 两个建号入口的回执都复用同一张卡片，不许各拼一条链接
  if (!/<InviteLinkCard\s+link=/.test(orgp)) bad.push('OrgP 的建号回执没有渲染 InviteLinkCard')
  if (!/<InviteLinkCard\s+link=/.test(usersp)) bad.push('UsersP 的建号回执没有渲染 InviteLinkCard')
  // 链接字符串由服务端出栈：前端不许出现自己拼 ?invite= 的写法
  if (/location\.host\s*[,)}][\s\S]{0,80}invite=/.test(card + panel + orgp + usersp + codes)) {
    bad.push('前端在拼邀请链接 URL（host 必须由服务端决定）')
  }
  return bad
}

describe('§6.5 企业邀请凭据安全警示四面锁', () => {
  it('四面都在位、文案只有一个出处、URL 不在前端拼', () => {
    expect(securityFaceFailures()).toEqual([])
  })

  it('反证：任一面被摘掉，同一判据必须点名那一面（证明这条锁不是空转）', () => {
    // 每一枚变异体只动一面，判据必须**恰好**抓到那一面。
    // base/mutated 成对写：先证明这枚变异**真的改动了源码**（no-op 的变异体会把"锁抓住了破坏"
    // 伪装成绿灯，本仓反证首跑就栽过），再判它点名的那一面。
    const cases: Array<[string, 'card' | 'panel' | 'orgp' | 'usersp' | 'codes', (s: string) => string, RegExp]> = [
      ['卡片不渲染警示', 'card', (s) => s.replace('<InviteLinkSecurityNote />', '<div />'), /创建回执卡片/],
      ['管理页没有常驻条', 'panel', (s) => s.replace(/<InviteLinkSecurityNote \/>/g, ''), /邀请管理页没有常驻警示条/],
      ['列表行没有风险标识', 'panel', (s) => s.replace("t('inviteLink.riskTag')", "'tag'"), /邀请链接列表行里没有风险标识/],
      ['邀请码列表没有常驻条', 'codes', (s) => s.replace('<InviteLinkSecurityNote />', '<div />'), /邀请码列表面板没有常驻警示条/],
      ['邀请码列表行没有风险标识', 'codes', (s) => s.replace("t('inviteLink.riskTag')", "'tag'"), /邀请码列表行里没有风险标识/],
      ['面板自己抄了一份警示文案', 'orgp', (s) => s.replace('const isPlatformGrant', "t('inviteLink.warnBody');\n  const isPlatformGrant"), /inviteLink\.warnBody/],
      ['OrgP 回执不渲染卡片', 'orgp', (s) => s.replace(/<InviteLinkCard\s+link=/, '<Foo link='), /OrgP/],
      ['UsersP 回执不渲染卡片', 'usersp', (s) => s.replace(/<InviteLinkCard\s+link=/, '<Foo link='), /UsersP/],
      ['前端自己拼链接 URL', 'orgp', (s) => s.replace('const isPlatformGrant', 'const __probe = `${location.host}/?invite=`;\n  const isPlatformGrant'), /前端在拼/],
    ]
    const SRC: Record<'card' | 'panel' | 'orgp' | 'usersp' | 'codes', string> = { card: CARD, panel: PANEL, orgp: ORG, usersp: USERS, codes: USERS }
    for (const [label, key, mutate, expectHit] of cases) {
      const mutated = mutate(SRC[key])
      expect(mutated === SRC[key], `反证 ${label} 的变异体没改动任何字节（锚漂了，这枚反证是空转的）`).toBe(false)
      const failures = securityFaceFailures({ [key]: mutated })
      expect(failures.length, `反证失效：${label} 判据却回空（这条锁是空转的）`).toBeGreaterThan(0)
      expect(failures.join('\n'), `反证 ${label} 没点名预期那一面`).toMatch(expectHit)
    }
  })
})

// —— §6.4 命名隔离：十二语种 × 「企业邀请链接」不许说成「邀请码」 ——

/**
 * 十二份「合并词典」：zh／en 由两份面板词典并成一份（码类键与链接类键必须同时在场，
 * 判据才有射程——只喂链接那份会退化成「拿不到码类用词 ⇒ 恒绿」的空转锁），
 * 其余十语种直接取 locales/*.ts 的全量词典。
 * 判据**现读现算**：把「邀请码」的各语种用词写死在测试里，词典一改就变空转。
 */
const DICTS: Array<[string, Record<string, string>]> = [
  ['zh', { ...orgZh, ...linkZh } as Record<string, string>],
  ['en', { ...orgEn, ...linkEn } as Record<string, string>],
  ['zh_hant', zhHant], ['ja', ja], ['ko', ko], ['th', th],
  ['ru', ru], ['fr', fr], ['ar', ar], ['es', es], ['pt', pt], ['de', de],
]
// 「邀请码」三层语义里，手输注册码这一层的既有标题键（各语种都必须已有译文）
const CODE_TITLE_KEY = 'org.invitesTitle'

function linkKeysOf(dict: Record<string, string>): string[] {
  return Object.keys(dict).filter((k) => k.startsWith('inviteLink.'))
}

/** codeCollisions 判据函数（抽出来供反证复用）：喂一份词典，回「把链接说成码」的键 */
function codeCollisions(dict: Record<string, string>): string[] {
  const codeTerm = String(dict[CODE_TITLE_KEY] || '')
  if (!codeTerm) return []
  return linkKeysOf(dict).filter((k) => dict[k].includes(codeTerm))
}

// 英文面板的 inviteLink.* 键数：射程自证的基准，现读而不是写死——
// 写死数字会让「将来补译词条」变成一次假红，而「词典没跟上」又会被宽松判据放过。
const EN_LINK_KEY_COUNT = Object.keys(linkEn).length

describe('§6.4 三层邀请语义的命名隔离锁', () => {
  it('十二语种：邀请链接的每一条文案都不许含该语种「邀请码」的整串用词', () => {
    for (const [lang, dict] of DICTS) {
      // 射程自证（前置）：该语种必须**同时**有码类标题与链接类词条，缺任一件这条腿就是空转
      expect(dict[CODE_TITLE_KEY], `${lang} 读不到 ${CODE_TITLE_KEY}（锚漂了，本锁已失明）`).toBeTruthy()
      expect(linkKeysOf(dict).length, `${lang} 的 inviteLink.* 词条数与英文面板不等（词典没跟上）`)
        .toBe(EN_LINK_KEY_COUNT)
      const bad = codeCollisions(dict)
      expect(bad, `${lang} 把邀请链接说成了「${dict[CODE_TITLE_KEY]}」：${bad.slice(0, 4)}`).toEqual([])
    }
  })

  it('十二语种：管理台那两条标题必须一眼可分（同文＝用户分不出手里是哪一件）', () => {
    const same = DICTS.filter(([, d]) => d['inviteLink.title'] === d[CODE_TITLE_KEY]).map(([l]) => l)
    expect(same, '这些语种的「邀请链接」标题与「邀请码」标题逐字同文：').toEqual([])
  })

  it('反证：把某语种的邀请链接标题换成该语种「邀请码」用词，判据必须逐个点名它', () => {
    for (const [lang, dict] of DICTS) {
      const mutated = { ...dict, 'inviteLink.title': String(dict[CODE_TITLE_KEY]) }
      expect(codeCollisions(mutated), `反证失效（${lang}）：整串同文都没被判红，这条锁是空转的`)
        .toContain('inviteLink.title')
    }
  })

  it('十语种 locales 的 inviteLink.* 键集与英文面板逐键等集（补译不许只补一半）', () => {
    const enKeys = Object.keys(linkEn).sort()
    for (const [lang, dict] of DICTS.slice(2)) {
      expect(Object.keys(dict).filter((k) => k.startsWith('inviteLink.')).sort(), `${lang} 的 inviteLink.* 键集漂移`)
        .toEqual(enKeys)
    }
  })
})
