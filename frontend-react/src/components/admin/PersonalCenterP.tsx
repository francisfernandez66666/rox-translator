// ============================================================================
// components/admin/PersonalCenterP.tsx — 个人中心面板（功能③）
// 职责：把「邀请好友（ReferralP）」与「任务中心（TaskCenterP）」合并为
//       单一「个人中心」菜单，内部以 Tabs 分页承载。
// ★ 2026-09 权限收口：邀请裂变奖励仅个人用户（is_personal=1）参与；企业用户
//   （含平台超管平台上下文）不显示「邀请好友 · 多邀多得」子 tab，仅保留任务中心。
// ============================================================================

/**
 * PersonalCenterP.tsx · 职责说明
 * 个人中心面板：
 * - 邀请好友子 tab（仅个人用户可见）：我的邀请码/链接/二维码 + 邀请奖励记录
 * - 任务中心子 tab：启用任务列表 + 一键领取永久 token 奖励；超管可自定义每日/一次性任务
 */

import { useState } from 'react'
import { Tabs } from 'tdesign-react'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { ReferralP } from './panels_c'
import TaskCenterP from './TaskCenterP'

/** 个人中心面板组件：邀请好友（个人用户）/ 任务中心 子 tab */
export default function PersonalCenterP() {
  const [, t] = useT()
  const { isPersonal } = useAdmin()
  const [tab, setTab] = useState<'referral' | 'tasks'>('referral')

  return (
    <Tabs value={tab} onChange={(v) => setTab(v as 'referral' | 'tasks')}>
      {/* 邀请好友 · 多邀多得：仅个人用户展示；企业用户/平台超管彻底隐藏 */}
      {isPersonal && (
        <Tabs.TabPanel value="referral" label={t('referral.title')}>
          <ReferralP />
        </Tabs.TabPanel>
      )}
      <Tabs.TabPanel value="tasks" label={t('tasks.title')}>
        <TaskCenterP />
      </Tabs.TabPanel>
    </Tabs>
  )
}
