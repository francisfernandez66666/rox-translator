// ============================================================================
// components/admin/SystemSettingsP.tsx — 「系统设置」聚合 Hub（超管 L4）
// 职责：把注册与触达（告警中心）、邮件模板、流程引擎、协议签署、审计日志、
//       品牌与页脚，连同运行时配置（运营策略/模型供应商）合并为「系统与运维」一级菜单（2026-09-15
//       Tab 精简：原一级菜单 alerts/mailTpl/audit/footer/brand 全部并入此处）。
// ============================================================================

/**
 * SystemSettingsP.tsx · 职责说明
 * Tabs：告警与注册触达（AlertsP）｜ 邮件模板 ｜ 流程引擎 ｜ 协议签署 ｜
 *       审计日志 ｜ 运营策略（OpsP）｜ 模型与供应商（ModelsP）｜ 品牌与页脚（BrandP）
 * 2026-09-18（UI 融合）：Tabs 采用 ui/langcross 的「items 声明表头 + activeKey
 *   受控 + 子面板在外部按 tab 条件挂载」写法（不同于旧组件库的 <Tabs.TabPanel>
 *   子面板内联写法）。本文件只做 tab 编排，不含任何数据请求，因此各子面板逻辑未受影响。
 */

import { useState } from 'react'
import { Tabs } from '@/ui/langcross/src'
import { useT } from '@/i18n'
import MailTplP from './MailTplP'
import { AlertsP, AgreementsP, AuditP } from './panels_a'
import { WorkflowP, ModelsP } from './panels_d'
import BrandP from './BrandP'
import { OpsP } from './panels_e'

/** HubTab 系统与运维 Hub 的 8 个子 Tab（2026-09-15 由 20 收敛） */
type HubTab = 'settings' | 'mailTpl' | 'workflow' | 'agreements' | 'audit' | 'ops' | 'models' | 'brand'

/** 系统设置 Hub：八类平台配置子 tab（仅超管可达，入口菜单 L4 门控） */
export default function SystemSettingsP() {
  const [, t] = useT()
  const [tab, setTab] = useState<HubTab>('settings')

  // tab 定义表：数组顺序即页签渲染顺序，key 必须取自 HubTab ——
  // 下方子面板按 key 逐一对应条件挂载，key 写错只会让该面板永远不显示（点进去一片空白）
  const items = [
    { key: 'settings', label: t('alerts.title') },
    { key: 'mailTpl', label: t('mailTpl.title') },
    { key: 'workflow', label: t('workflow.title') },
    { key: 'agreements', label: t('agreements.title') },
    { key: 'audit', label: t('audit.title') },
    { key: 'ops', label: t('ops.title') },
    { key: 'models', label: t('models.title') },
    { key: 'brand', label: t('hub.tabBrand') },
  ]
  return (
    <>
      {/* langcross Tabs 只渲染页签条、不渲染面板体，故面板改由下方 `tab === key &&` 逐条挂载：
          切走的子面板会卸载并丢弃其本地 state（切回时重新挂载、重新发首屏请求），
          与 TDesign TabPanel 的「常驻隐藏」行为不同，请勿再往子面板里放跨 tab 的状态 */}
      <Tabs activeKey={tab} onChange={(k) => setTab(k as HubTab)} items={items} />
      {tab === 'settings' && <AlertsP />}
      {tab === 'mailTpl' && <MailTplP />}
      {tab === 'workflow' && <WorkflowP />}
      {tab === 'agreements' && <AgreementsP />}
      {tab === 'audit' && <AuditP />}
      {tab === 'ops' && <OpsP />}
      {tab === 'models' && <ModelsP />}
      {tab === 'brand' && <BrandP />}
    </>
  )
}
