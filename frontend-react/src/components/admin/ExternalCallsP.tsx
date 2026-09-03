// ============================================================================
// components/admin/ExternalCallsP.tsx — 外部调用面板（功能②）
// 职责：把「开放 API（ApiKeysP）」与「回调通知（WebhooksP）」两个面板合并为
//       单一「外部调用」菜单，内部以 Tabs 分页承载，减少后台侧边栏菜单层级。
// ============================================================================

/**
 * ExternalCallsP.tsx · 职责说明
 * 外部调用面板：
 * - 开放 API 子 tab：API Key 创建/启停/轮换/限额/删除 + OpenAPI 在线文档维护
 * - 回调通知子 tab：Webhook 列表、新增/编辑/启停/测试/删除
 */

import { useState } from 'react'
import { Tabs } from 'tdesign-react'
import { useT } from '@/i18n'
import { ApiKeysP, WebhooksP } from './panels_c'

/** 外部调用面板组件：开放 API / 回调通知 两子 tab */
export default function ExternalCallsP() {
  const [, t] = useT()
  const [tab, setTab] = useState<'apikeys' | 'webhooks'>('apikeys')

  return (
    <Tabs value={tab} onChange={(v) => setTab(v as 'apikeys' | 'webhooks')}>
      <Tabs.TabPanel value="apikeys" label={t('apikeys.title')}>
        <ApiKeysP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="webhooks" label={t('webhooks.title')}>
        <WebhooksP />
      </Tabs.TabPanel>
    </Tabs>
  )
}
