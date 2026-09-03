// ============================================================================
// components/admin/SystemSettingsP.tsx — 系统设置面板（超管 L4）
// 职责：合并「邮件模板 / 流程引擎 / 系统告警 / 协议签署」四个子面板为单一「系统设置」tab，
//       以 Tabs 分页承载，减少后台侧边栏菜单层级。
// ============================================================================
import { useState } from 'react'
import { Tabs } from 'tdesign-react'
import { useT } from '@/i18n'
import MailTplP from './MailTplP'
import { AlertsP, AgreementsP } from './panels_a'
import { WorkflowP } from './panels_d'

/** 系统设置面板组件（超管 L4）：邮件模板 / 流程引擎 / 系统告警 / 协议签署 四合一 */
export default function SystemSettingsP() {
  const [, t] = useT()
  const [tab, setTab] = useState<'mailTpl' | 'workflow' | 'alerts' | 'agreements'>('mailTpl')

  return (
    <Tabs value={tab} onChange={(v) => setTab(v as 'mailTpl' | 'workflow' | 'alerts' | 'agreements')}>
      <Tabs.TabPanel value="mailTpl" label={t('mailTpl.title')}>
        <MailTplP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="workflow" label={t('workflow.title')}>
        <WorkflowP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="alerts" label={t('alerts.title')}>
        <AlertsP />
      </Tabs.TabPanel>
      <Tabs.TabPanel value="agreements" label={t('agreements.title')}>
        <AgreementsP />
      </Tabs.TabPanel>
    </Tabs>
  )
}
