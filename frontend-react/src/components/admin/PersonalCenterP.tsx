// ============================================================================
// components/admin/PersonalCenterP.tsx — 个人中心面板（功能③）
// 职责：把「邀请好友（ReferralP）」与「任务中心（TaskCenterP）」合并为
//       单一「个人中心」菜单，内部以 Tabs 分页承载。
// ★ 2026-09 权限收口：邀请裂变奖励仅个人用户（is_personal=1）参与；企业用户
//   （含平台超管平台上下文）不显示「邀请好友 · 多邀多得」子 tab，仅保留任务中心。
// 实现提示：tab 壳用 ui/langcross 的 Tabs（受控 activeKey，可为空串＝不选中），
//   子面板仍复用 ReferralP / TaskCenterP，本文件只做装配与权限过滤。
// ============================================================================

/**
 * PersonalCenterP.tsx · 职责说明
 * 个人中心面板：
 * - 邀请好友子 tab（仅个人用户可见）：我的邀请码/链接/二维码 + 邀请奖励记录
 * - 任务中心子 tab：启用任务列表 + 一键领取永久 token 奖励；超管可自定义每日/一次性任务
 */

import { useState } from 'react'
import { Tabs } from '@/ui/langcross/src'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { ReferralP } from './panels_c'
import TaskCenterP from './TaskCenterP'

/** 个人中心面板组件：邀请好友（个人用户）/ 任务中心 子 tab（默认不选中，由用户点选） */
export default function PersonalCenterP() {
  const [, t] = useT()
  const { isPersonal } = useAdmin()
  // ★ 默认不选中任何子 tab（2026-09-09 反馈）：点开「个人中心」先展示空内容区，
  //   由用户自行点选「邀请好友/任务中心」，避免默认打开一个子页。
  const [tab, setTab] = useState<'referral' | 'tasks' | ''>('')

  // 邀请好友 · 多邀多得：仅个人用户展示；企业用户/平台超管彻底隐藏
  const items = [
    ...(isPersonal ? [{ key: 'referral', label: t('referral.title') }] : []),
    { key: 'tasks', label: t('tasks.title') },
  ]
  return (
    <>
      <Tabs activeKey={tab} onChange={(k) => setTab(k as 'referral' | 'tasks' | '')} items={items} />
      {/* 渲染侧再做一次 isPersonal 兜底：即使 tab 状态被旧值/深链置为 referral，
          企业用户也不会拿到邀请裂变面板（权限收口，与上面 items 的过滤保持同一口径） */}
      {tab === 'referral' && isPersonal && <ReferralP />}
      {tab === 'tasks' && <TaskCenterP />}
    </>
  )
}
