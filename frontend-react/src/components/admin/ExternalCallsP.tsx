// ============================================================================
// components/admin/ExternalCallsP.tsx — 外部调用面板（功能②）
// 职责：把「开放 API（ApiKeysP）」「回调通知（WebhooksP）」「官方 SDK（SdkP）」面板合并为
//       单一「外部调用」菜单，内部以 Tabs 分页承载，减少后台侧边栏菜单层级。
// 2026-09-18（UI 融合）：tab 壳换成 ui/langcross Tabs（items 声明式），面板改为外部条件挂载。
// ============================================================================

/**
 * ExternalCallsP.tsx · 职责说明
 * 外部调用面板：
 * - 开放 API 子 tab：API Key 创建/启停/轮换/限额/删除 + OpenAPI 在线文档维护
 * - 回调通知子 tab：Webhook 列表、新增/编辑/启停/测试/删除
 * - SDK 子 tab：Python/TypeScript/Java 三端官方 SDK 安装与快速集成（2026-09-15）
 */

import { useState } from 'react'
import { Tabs } from '@/ui/langcross/src'
import { useT } from '@/i18n'
import { ApiKeysP, WebhooksP } from './panels_c'
import SdkP from './SdkP'

/** 外部调用面板组件：开放 API / 回调通知 / 官方 SDK 三子 tab */
export default function ExternalCallsP() {
  const [, t] = useT()
  const [tab, setTab] = useState<'apikeys' | 'webhooks' | 'sdk'>('apikeys')

  return (
    <>
      {/* langcross Tabs 只出 tab 头，不含面板容器：下面的条件渲染即“懒加载”，
          未点开的子面板不挂载、不发请求（三个面板各自拉列表，省掉两次无用请求） */}
      <Tabs activeKey={tab} onChange={(k) => setTab(k as 'apikeys' | 'webhooks' | 'sdk')} items={[
        { key: 'apikeys', label: t('apikeys.title') },
        { key: 'webhooks', label: t('webhooks.title') },
        { key: 'sdk', label: t('sdk.title') },
      ]} />
      {tab === 'apikeys' && <ApiKeysP />}
      {tab === 'webhooks' && <WebhooksP />}
      {tab === 'sdk' && <SdkP />}
    </>
  )
}
