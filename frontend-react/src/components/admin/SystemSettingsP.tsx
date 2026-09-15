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
 */

import { useState } from 'react'
import { Tabs } from 'tdesign-react'
import { useT } from '@/i18n'
import MailTplP from './MailTplP'
import { AlertsP, AgreementsP, AuditP } from './panels_a'
import { WorkflowP, ModelsP } from './panels_d'
import BrandP from './BrandP'
import { OpsP } from './panels_e'

type HubTab = 'settings' | 'mailTpl' | 'workflow' | 'agreements' | 'audit' | 'ops' | 'models' | 'brand'

/** 系统设置 Hub：六类平台配置子 tab（仅超管可达，入口菜单 L4 门控） */
export default function SystemSettingsP() {
  const [, t] = useT()
  const [tab, setTab] = useState<HubTab>('settings')

  return (
    <Tabs value={tab} onChange={(v) => setTab(v as HubTab)}>
      <Tabs.TabPanel value="settings" label={t('alerts.title')}>
        <AlertsP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="mailTpl" label={t('mailTpl.title')}>
        <MailTplP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="workflow" label={t('workflow.title')}>
        <WorkflowP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="agreements" label={t('agreements.title')}>
        <AgreementsP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="audit" label={t('audit.title')}>
        <AuditP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="ops" label={t('ops.title')}>
        <OpsP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="models" label={t('models.title')}>
        <ModelsP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="brand" label={t('hub.tabBrand')}>
        <BrandP />
      </Tabs.TabPanel>
    </Tabs>
  )
}
