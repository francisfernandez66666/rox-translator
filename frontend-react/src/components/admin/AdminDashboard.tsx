// ============================================================================
// components/admin/AdminDashboard.tsx — 后台控制台整体布局与导航（TDesign Menu 版）
// 职责：提供左侧菜单、顶部工具栏，并按权限渲染对应子面板。
// 映射 Vue 版：frontend/src/components/admin/AdminDashboard.vue（或同目录下菜单壳组件）。
// 权限矩阵与 Vue 版一致：L4 全量；L3 无 Tenants/Models/Workflow/Audit/Alerts；
// L2 仅 Overview/Usage/Kb/Tickets。超管含租户切换器。
// ============================================================================
import { Button, Menu, Select, Tag } from 'tdesign-react'
import { useAdmin } from '@/stores/admin'
import type { PanelKey } from '@/stores/admin'
import { t, toggleLang } from '@/i18n'
import Bell from '@/components/Bell'
import AccountMenu from '@/components/AccountMenu'

import Overview from './panels_a'
import { UsersP, AlertsP, AuditP, UsageP, InvitesP } from './panels_a'
import { TenantsP, OrgP } from './panels_b'
import { PlansP, ReferralP, WebhooksP, ApiKeysP } from './panels_c'
import { KbP, ModelsP, WorkflowP, TicketsP } from './panels_d'

// 菜单项：key 对应 admin store 中的面板标识，minLevel 为可见最低角色等级
interface Item { key: PanelKey; label: string; minLevel: number }

const ITEMS: Item[] = [
  { key: 'overview', label: '📊 总览', minLevel: 2 },
  { key: 'tenants', label: '🏢 租户管理', minLevel: 4 },
  { key: 'plans', label: '💎 套餐中心', minLevel: 3 },
  { key: 'referral', label: '🔗 邀请好友', minLevel: 3 },
  { key: 'org', label: '🌳 组织架构', minLevel: 3 },
  { key: 'usage', label: '📈 用量看板', minLevel: 2 },
  { key: 'kb', label: '📚 知识库', minLevel: 2 },
  { key: 'models', label: '🧠 模型配置', minLevel: 4 },
  { key: 'workflow', label: '⚙️ 流程引擎', minLevel: 4 },
  { key: 'apikeys', label: '🔑 开放 API', minLevel: 3 },
  { key: 'webhooks', label: '🔗 回调通知', minLevel: 3 },
  { key: 'tickets', label: '📨 反馈/审批', minLevel: 2 },
  { key: 'audit', label: '📋 审计日志', minLevel: 4 },
  { key: 'alerts', label: '🚨 系统告警', minLevel: 4 },
]

// 根据当前选中的面板 key 返回对应组件（集中分发，避免在 JSX 中写长 switch）
function renderPanel(p: PanelKey) {
  switch (p) {
    case 'overview': return <Overview />
    case 'tenants': return <TenantsP />
    case 'plans': return <PlansP />
    case 'referral': return <ReferralP />
    case 'org': return <OrgP />
    case 'usage': return <UsageP />
    case 'kb': return <KbP />
    case 'models': return <ModelsP />
    case 'workflow': return <WorkflowP />
    case 'apikeys': return <ApiKeysP />
    case 'webhooks': return <WebhooksP />
    case 'tickets': return <TicketsP />
    case 'audit': return <AuditP />
    case 'alerts': return <AlertsP />
    case 'users': return <UsersP />
    default: return null
  }
}

// 后台控制台主组件：组合侧边菜单、顶部操作栏与动态面板
export default function AdminDashboard() {
  const ad = useAdmin()
  // 按当前用户等级过滤出可见菜单项
  const visible = ITEMS.filter((i) => ad.myLevel >= i.minLevel)

  return (
    <div className="admin-shell">
      <aside className="admin-side">
        <div style={{ fontWeight: 800, color: '#1a237e', padding: '6px 10px 14px' }}>🌐 {t('admin.title')}</div>
        <Menu value={ad.panel} onChange={(v) => ad.gotoPanel(v as PanelKey)} style={{ border: 'none' }}>
          {visible.map((i) => (
            <Menu.MenuItem key={i.key} value={i.key}>{i.label}</Menu.MenuItem>
          ))}
        </Menu>
      </aside>

      {/* 主内容区：顶部工具栏 + 当前面板 */}
      <main className="admin-main">
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 16 }}>
          {/* 超管专属：平台标识与租户切换器 */}
          {ad.isSuper && (
            <>
              <Tag theme="primary" variant="light">平台管理员</Tag>
              <Select
                value={ad.activeTenantId}
                onChange={(v) => ad.switchTenant(Number(v))}
                style={{ width: 240 }}
                clearable={false}
                options={[
                  { label: '🌐 平台根（全局）', value: 0 },
                  ...ad.tenants.map((x) => ({ label: `#${x.id} ${x.name}`, value: x.id })),
                ]}
                placeholder="切换生效租户"
              />
            </>
          )}
          {/* 非超管时显示当前管理范围标签 */}
          {!ad.isSuper && ad.myLevel >= 3 && <Tag theme="primary" variant="light">企业管理</Tag>}
          {ad.myLevel === 2 && <Tag variant="light">部门管理</Tag>}
          {/* 右侧占位，把后续操作按钮推到最右 */}
          <div style={{ flex: 1 }} />
          <Bell />
          <Button size="small" variant="text" onClick={toggleLang}>{t('app.langBtn')}</Button>
          <Button size="small" variant="outline"
                  onClick={() => { window.history.pushState({}, '', '/'); window.dispatchEvent(new PopStateEvent('popstate')) }}>
            ← {t('admin.backWorkbench')}
          </Button>
          <AccountMenu />
        </div>

        {renderPanel(ad.panel)}
      </main>
    </div>
  )
}
