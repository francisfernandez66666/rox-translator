// ============ lib/siteFlags.test.ts · 职责说明 ============
// `landingEnabledForGuest()` 的判据锁（★ 2026-10-23 加租户维度那一批）。
//
// 这一条判据有**两条腿**，缺任何一条都会出线上事故，且两种事故的症状完全不同：
//   ① 租户定制页（tenantId>0）没有关主页 ⇒ 客户品牌域名上给访客看**平台自己的**营销落地页
//      （平台定价、平台比价、平台 CTA）。这不是样式问题：访客是冲这个客户来的，
//      却被递到平台的报价单前，等于用客户的域名给平台拉客。
//   ② 主站（tenantId=0）误关主页 ⇒ 对外营业的门面直接消失，访客打开就是登录框。
// 所以本文件对两条腿各配正测与反证：**该关的必须关、不该关的一个都不许关**。
//
// 为什么单独建文件而不是并进 navShim：navShim 锁的是「两棵树有没有问过这条判据」
// （静态文本锚点），本文件锁的是「判据自己算得对不对」（行为）。两件事的射程不重叠。
// =============================================
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { landingEnabledForGuest, readSiteFlags } from './siteFlags'

/** 清掉全局标记，回到「后端什么都没注入」的形态 */
function clearFlags() {
  delete (window as unknown as { __SITE_FLAGS__?: unknown }).__SITE_FLAGS__
}

/** 等价复现后端直出的那一段 `<script id="__site_flags__">` */
function seedFlags(v: unknown) {
  ;(window as unknown as { __SITE_FLAGS__?: unknown }).__SITE_FLAGS__ = v
}

beforeEach(clearFlags)
afterEach(clearFlags)

describe('readSiteFlags · 只认朴素对象', () => {
  it('未注入 ⇒ 空对象（＝全默认档，不抛错）', () => {
    expect(readSiteFlags()).toEqual({})
  })

  it('非朴素对象（null／数组／字符串／数字）一律按未表态处理', () => {
    for (const bad of [null, [], 'x', 42, true]) {
      seedFlags(bad)
      expect(readSiteFlags(), `${JSON.stringify(bad)} 不该被当成表态`).toEqual({})
    }
  })

  it('正常对象原样读出', () => {
    seedFlags({ landing: false })
    expect(readSiteFlags()).toEqual({ landing: false })
  })
})

describe('landingEnabledForGuest · 腿① 租户定制页（tenantId>0）', () => {
  it('租户域名（tenantId=1）即便没有任何门面标记，也必须关闭主页', () => {
    // 这是本批的核心：演示站/客户品牌子域**不靠** LANDING_DISABLED 也要直落登录页
    expect(landingEnabledForGuest(1)).toBe(false)
  })

  it('租户 id 取任意正数都关闭（不是只认 1）', () => {
    for (const tid of [2, 7, 999, 100000]) {
      expect(landingEnabledForGuest(tid), `tenantId=${tid} 应关闭主页`).toBe(false)
    }
  })

  it('租户域名上即使后端明确说 landing:true 也仍然关闭（租户维度优先）', () => {
    // 口径：租户定制页不展示平台落地页，这是**结构决定**，不是可配置项。
    // 谁想给某个租户开主页，该做的是给那个租户一个自己的落地页，而不是把平台落地页塞过去。
    seedFlags({ landing: true })
    expect(landingEnabledForGuest(1)).toBe(false)
  })

  it('反证：主站（tenantId=0）在同样标记下必须仍然开放', () => {
    seedFlags({ landing: true })
    expect(landingEnabledForGuest(0)).toBe(true)
  })
})

describe('landingEnabledForGuest · 腿② 部署级门面标记', () => {
  it('标记缺省（未注入）⇒ 主站主页开放', () => {
    expect(landingEnabledForGuest(0)).toBe(true)
  })

  it('landing:false ⇒ 主站主页关闭（演示站旧形态／运维急救）', () => {
    seedFlags({ landing: false })
    expect(landingEnabledForGuest(0)).toBe(false)
  })

  it('标记里 landing 不是布尔（字符串 "false"）⇒ 按缺省走，不猜', () => {
    seedFlags({ landing: 'false' })
    expect(landingEnabledForGuest(0)).toBe(true)
  })

  it('标记里是别的键 ⇒ 不影响主页判定', () => {
    seedFlags({ something: false })
    expect(landingEnabledForGuest(0)).toBe(true)
  })
})

describe('landingEnabledForGuest · 参数缺省与非法值', () => {
  it('省略参数等价于 tenantId=0（老调用点不传参也不会被误判成租户）', () => {
    expect(landingEnabledForGuest()).toBe(true)
    seedFlags({ landing: false })
    expect(landingEnabledForGuest()).toBe(false)
  })

  it('非法 tenantId（NaN／Infinity／负数）不触发租户腿', () => {
    // 宁可当主站（展示主页）也不要因为一个脏数字把客户的站关掉——
    // 反过来（把主站误判成租户）代价是对外门面消失，见文件头。
    for (const bad of [NaN, Infinity, -Infinity, -1]) {
      expect(landingEnabledForGuest(bad), `tenantId=${String(bad)} 不该关主页`).toBe(true)
    }
  })
})
