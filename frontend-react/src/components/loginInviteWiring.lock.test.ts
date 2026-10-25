// ============================================================================
// components/loginInviteWiring.lock.test.ts — 「受邀入职」在登录屏这一整条接线的静态锁
// （★ 2026-10-24 多身份 B4-d；行为面在 InviteJoinNotice.dom.test.tsx，本文件锁的是**接线**）
//
// 为什么必须有静态锁而不是只靠 DOM 测试：
//   主站登录屏的统一登录门（`unified`）是一句"结构性挡住注册卡"的判断。B4-d 在它上面开了
//   **第一条也是唯一一条例外**——一枚服务端认过的企业邀请链接。这类"门 + 例外"最坏的退化形态
//   不是报错，而是**安静地退回旧行为**：把例外条件写错一个字符，受邀人点开合法链接后看到的
//   还是登录框（他既注册不了也没得到任何解释），而 tsc 与全部单测照样绿。
//   同族先例＝AGENTS §一·5「页脚只有一处」「顶栏右簇两枚入口」那批：按**源码形态**判，
//   因为要防的是"下一次有人顺手把它改回去"。
//
// 九条腿各自盯一处退化，另有五枚反证证明这些腿不是空转：
//   L1 门只能由服务端回执开门（不许退回无条件挡注册、也不许按"地址栏有没有 invite 字样"开门）
//   L2 inviteJoining 的定义必须同时握「有码」与「回执 valid=true」两只手
//   L3 匿名校验只拨一次、只在登录态门面（mode==='home'）拨、失败留 null 而不是伪造成死链
//   L4 告知块必须两张卡各一处（改两面漏一面是本仓这一族的固定死法）
//   L5 受邀人不许走 AI 接管流（接管流按"从零聊出企业资料"设计，会抹平预填、追问已定事项）
//   L6 预填必须带上管理员写下的用户名，且邮箱只在锁定时才覆盖
//   L7 链接与锁定邮箱两格在受邀态必须只读（凭证不该由受邀人重新拼）
//   L8 受邀态不许再让他选「个人/企业」与「管理员/成员」（这两格的存在等于问一个已经定了的事）
//   L9 doRegister 的企业字段前置校验必须跟渲染面走（旧形态对成员档也要求企业码/企业名 ⇒
//      受邀成员提交必吃一句指向**不可见字段**的报错）
// 运行：npx vitest run src/components/loginInviteWiring.lock.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const DIR = fileURLToPath(new URL('.', import.meta.url))

/**
 * 剥注释：本仓的负向锁被自己新写的注释喂饱过多次（AGENTS §一·5「注释里不许出现那串字面」）。
 * Login.tsx 的文件内注释逐字写着 `(unified && !inviteJoining)` 的**旧形态**与各种反面例子，
 * 不剥注释的话 L1 那条负向腿一开局就红，而红指向一份完全正确的代码。
 */
function stripComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .map((l) => {
      const t = l.trim()
      if (t.startsWith('//')) return ''
      // 行尾注释：只在引号成对时才切（https:// 里的 // 前面必有一个未闭合引号）
      const idx = l.indexOf('//')
      if (idx > 0) {
        const before = l.slice(0, idx)
        if ((before.match(/"/g) || []).length % 2 === 0 && (before.match(/'/g) || []).length % 2 === 0) {
          return before
        }
      }
      return l
    })
    .join('\n')
}

/** 判据函数：喂源码，返回违规清单。正向跑真文件，五枚反证喂变异体，跑的是同一个函数。 */
function checkLogin(src: string): string[] {
  const s = stripComments(src)
  const bad: string[] = []
  const count = (re: RegExp) => (s.match(re) || []).length

  // L1 门：统一登录下仍要挡住注册，但**服务端认过的受邀链接**例外
  if (!/view\s*===\s*'signin'\s*\|\|\s*\(unified\s*&&\s*!inviteJoining\)/.test(s)) {
    bad.push("L1 主站登录门没写成 `(unified && !inviteJoining)`——受邀入职的例外没接上")
  }
  if (/\|\|\s*unified\s*\)/.test(s)) {
    bad.push('L1b 门里出现了裸 `|| unified)`（无条件挡注册的旧形态）')
  }

  // L2 inviteJoining 的定义
  if (!/const inviteJoining\s*=\s*!!inviteCode\s*&&\s*invitePeek\?\.valid\s*===\s*true/.test(s)) {
    bad.push('L2 inviteJoining 不是「有码 && 回执 valid===true」的合取')
  }

  // L3 匿名校验：只此一处、只在 home 门脸、失败留 null（不许伪造成死链回执）
  if (count(/inviteLinkPublic\(/g) !== 1) {
    bad.push(`L3 inviteLinkPublic 调用点应为恰好 1 处，实测 ${count(/inviteLinkPublic\(/g)}`)
  }
  if (!/if\s*\(mode\s*!==\s*'home'\s*\|\|\s*!inviteCode\)\s*return/.test(s)) {
    bad.push("L3b 校验 effect 没有「非 home 门脸 / 无码 ⇒ 不拨」的前置")
  }
  if (!/catch\(\(\)\s*=>\s*\{\s*if\s*\(alive\)\s*setInvitePeek\(null\)/.test(s)) {
    bad.push('L3c 请求失败没有把 invitePeek 留成 null（取不到会被渲染成死链，指向错误的下一步）')
  }

  // L4 告知块两张卡各一处
  if (count(/<InviteJoinNotice\b/g) !== 2) {
    bad.push(`L4 告知块应出现 2 次（登录卡＋注册卡），实测 ${count(/<InviteJoinNotice\b/g)}`)
  }

  // L5 受邀人不走 AI 接管流
  if (!/if\s*\(inviteJoining\)\s*\{\s*setRegPhase\('form'\);\s*return\s*\}/.test(s)) {
    bad.push("L5 没有「受邀态直接落传统表单、不进 AI 接管流」的早退分支")
  }
  if (!/\}\s*,\s*\[view,\s*mode,\s*inviteJoining\]\s*\)/.test(s)) {
    bad.push('L5b 接管 effect 的依赖里没有 inviteJoining（早退不会被触发）')
  }

  // L6 预填：用户名带守卫、邮箱只在锁定时代覆盖
  if (!/if\s*\(invitePeek\.username\)\s*setUsername\(invitePeek\.username\)/.test(s)) {
    bad.push('L6 用户名预填没有「回执里确实有」的守卫（undefined 会把已填内容清空）')
  }
  if (!/invite:\s*inviteCode/.test(s)) {
    bad.push('L6b 预填没把链接码写进 form.invite')
  }
  if (!/email:\s*invitePeek\.email_locked\s*&&\s*invitePeek\.bound_email\s*\?\s*invitePeek\.bound_email\s*:\s*f\.email/.test(s)) {
    bad.push('L6c 邮箱覆盖没走 email_locked（未锁定时会盖掉受邀人自己填的邮箱）')
  }

  // L7 受邀态只读：两枚链接格 + 锁定邮箱格
  if (count(/readOnly=\{inviteJoining\}/g) < 2) {
    bad.push(`L7 邀请链接格 readOnly={inviteJoining} 应至少 2 处，实测 ${count(/readOnly=\{inviteJoining\}/g)}`)
  }
  if (!/readOnly=\{inviteJoining\s*&&\s*!!invitePeek\?\.email_locked\}/.test(s)) {
    bad.push('L7b 邮箱格没按 email_locked 才锁')
  }

  // L8 受邀态隐藏两枚分段控件
  if (count(/!inviteJoining\s*&&/g) < 2) {
    bad.push(`L8 受邀态隐藏分段控件应至少 2 处（个人/企业、管理员/成员），实测 ${count(/!inviteJoining\s*&&/g)}`)
  }

  // L9 企业字段前置校验跟渲染面走（成员档不渲染企业码/企业名两格）
  if (count(/!branding\.dedicatedRegister\s*&&\s*typeChoice\s*===\s*'enterprise'\s*&&\s*roleChoice\s*===\s*'admin'/g) < 2) {
    bad.push('L9 企业码/企业名的前置校验没双双收到 roleChoice===admin（受邀成员会吃到指向不可见字段的报错）')
  }

  return bad
}

describe('受邀入职接线锁（Login.tsx，§6.1/§6.5 落地页那一面）', () => {
  const src = readFileSync(DIR + 'Login.tsx', 'utf8')

  it('L1~L9 九条腿在真文件上全绿（射程自证：判据不是返回空清单）', () => {
    expect(checkLogin(src)).toEqual([])
    // 反证护栏：判据函数本身要能产出违规清单，否则"全绿"可能只是它没在跑
    expect(checkLogin('')).not.toEqual([])
  })

  const mutants: Array<[string, (s: string) => string]> = [
    ['M1 门退回无条件挡注册', (s: string) => s.replace(/\(unified && !inviteJoining\)/, 'unified)')],
    ['M2 门按地址栏字样开（不看服务端回执）', (s: string) => s.replace('const inviteJoining = !!inviteCode && invitePeek?.valid === true', 'const inviteJoining = !!inviteCode')],
    ['M3 请求失败伪造成死链回执', (s: string) => s.replace('catch(() => { if (alive) setInvitePeek(null) })', 'catch(() => { if (alive) setInvitePeek({ valid: false, reason: "invalid" }) })')],
    ['M5 受邀人也走 AI 接管流', (s: string) => s.replace("if (inviteJoining) { setRegPhase('form'); return }", '')],
    ['M9 企业码校验回到整档 enterprise', (s: string) => s.replace(/!branding\.dedicatedRegister && typeChoice === 'enterprise' && roleChoice === 'admin'/g, "!branding.dedicatedRegister && typeChoice === 'enterprise'")],
  ]
  for (const [name, mutate] of mutants) {
    it(`${name} ⇒ 必须判红（证明对应腿有射程）`, () => {
      const mutated = mutate(src)
      // 变异必须真的改动了字节，否则"判红"是空替换的假象
      expect(mutated).not.toBe(src)
      expect(checkLogin(mutated).length, name).toBeGreaterThan(0)
    })
  }
})
