// ============================================================================
// components/admin/PersonalCenterP.tsx — 个人中心面板（功能③）
// 职责：把「邀请好友（ReferralP）」与「任务中心（TaskCenterP）」合并为
//       单一「个人中心」菜单，内部以 Tabs 分页承载。
// ============================================================================

/**
 * PersonalCenterP.tsx · 职责说明
 * 个人中心面板：
 * - 邀请好友子 tab：我的邀请码/链接/二维码 + 邀请奖励记录；超管可配运营参数
 * - 任务中心子 tab：启用任务列表 + 一键领取永久 token 奖励；超管可自定义每日/一次性任务
 */

import { useState } from 'react'
import { Tabs } from 'tdesign-react'
import { useT } from '@/i18n'
import { ReferralP } from './panels_c'
import TaskCenterP from './TaskCenterP'

/** 个人中心面板组件：邀请好友 / 任务中心 两子 tab */
export default function PersonalCenterP() {
  const [, t] = useT()
  const [tab, setTab] = useState<'referral' | 'tasks'>('referral')

  return (
    <Tabs value={tab} onChange={(v) => setTab(v as 'referral' | 'tasks')}>
      <Tabs.TabPanel value="referral" label={t('referral.title')}>
        <ReferralP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="tasks" label={t('tasks.title')}>
        <TaskCenterP />
      </Tabs.TabPanel>
    </Tabs>
  )
}
