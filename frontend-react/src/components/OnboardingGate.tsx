// ============================================================================
// components/OnboardingGate.tsx — 登录后「确实没有产品档案」就直接进引导流（★ 2026-11-06 用户令）
//
// 用户原话：「我让你去掉横幅，用引导流」。
// 前因：上一版把这条第一公里做成**底部横幅**（`OnboardingPrompt`：一条 fixed 全宽、无背景的条
//   ＋ 一颗「用三分钟备第一份」按钮）。它有两个问题，第二个是功能性的：
//  ① 观感——横幅压在左栏页脚上，用户看到的是一条没有脸的浮字；
//  ② **它横着盖住视口底部一整条**（`left:0;right:0;bottom:0`），而那条带子正是工作台的输入工具条；
//     透明不等于没有——那一段的点击会被它吃掉（AGENTS §一·5「按视口定位的浮层」那一族的反面教材）。
//  ⇒ 修法不是把横幅挪个位置，而是**取消"横幅＋按钮"这一步**：判据成立就直接把引导流开起来。
//     原来横幅上那句话（`onboard.prompt`）没有丢，它变成对话里的**第一句**——
//     用户不必先读一条横幅、再点一个按钮、再等一屏表单。
//
// 三条判据口径（与上一版同源，只是落点从横幅换成闸门）：
//  ① **只认"确实没有"**：`productList` 必须 `success:true` 且数组长度为 0 才开。
//     接口 500／超时／返回非数组／字段缺 ⇒ 一律不开——把一次网络抖动读成"这人没产品"，
//     会把老用户一次次推进向导（AGENTS §一·5「空态不许伪装成检查过」）。
//  ② **不与 ProfileSetupGate 抢屏**：那一闸门管"补邮箱/改密码"（`profile_setup_required===1`），
//     它开着时本闸门不出——两个全屏面板同屏就是用户报过的「弹两次、点了没反应」那一族观感。
//  ③ 关掉＝**本会话不再自动开**（sessionStorage 按用户记一次），但**完成判据不落库**：
//     "有没有产品"这件事本身就是判据，加一列"引导已看过"标记只会多一个漏挂钩的写点。
//
// 本件由 App.tsx 的登录态分支 React.lazy 加载（独立分片，不进入口包）；它静态 import Onboarding，
// 两者同处一个分片——引导那台对话外壳因此不会把注册面的字节带进首屏。
// ============================================================================
import { useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { useAuth } from '@/stores/auth'
import { productList } from '@/api/product'
import Onboarding from './Onboarding'

/** 同一会话内"这个人已经关过引导"的记账键前缀（按 user.id 记，不按全局记——多身份切换不该互相顶掉） */
const DISMISS_PREFIX = 'onboard_gate_dismissed_'

/**
 * OnboardingGate —— 根级常驻的登录态闸门。
 * 返回 null 的三种情况都必须是**安静**的：没登录、完善信息闸门在走、取不到"确实没有产品"。
 */
export default function OnboardingGate() {
  const [, t] = useT()
  const { user } = useAuth()
  const [open, setOpen] = useState(false)

  useEffect(() => {
    if (!user) { setOpen(false); return }
    // 口径②：完善信息闸门在弹（profile_setup_required===1）时这一轮不出
    if (user.profile_setup_required === 1) { setOpen(false); return }
    // 口径③：本会话关过就不再自动开（但 /onboarding 这个入口仍然在——AI 顾问的深链会跳它）
    if (sessionStorage.getItem(DISMISS_PREFIX + String(user.id)) === '1') { setOpen(false); return }
    let alive = true
    productList()
      .then((r) => {
        if (!alive) return
        // 口径①：只有"确实取到、确实为空"才开
        if (r && r.success === true && Array.isArray(r.products) && r.products.length === 0) setOpen(true)
      })
      .catch(() => { /* 取不到＝不知道，不许据此把人推进引导 */ })
    return () => { alive = false }
  }, [user?.id, user?.profile_setup_required])

  if (!open || !user) return null
  return (
    <Onboarding
      intro={t('onboard.prompt')}
      onDismiss={() => {
        sessionStorage.setItem(DISMISS_PREFIX + String(user.id), '1')
        setOpen(false)
      }}
    />
  )
}
