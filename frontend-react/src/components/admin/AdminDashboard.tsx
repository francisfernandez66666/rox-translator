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
import { t, toggleLang, useLang } from '@/i18n'
import Bell from '@/components/Bell'
import AccountMenu from '@/components/AccountMenu'
import SiteFooter from '@/components/SiteFooter'
import { useBranding } from '@/branding'

import Overview from './panels_a'
import { UsersP, AlertsP, AuditP, UsageP, InvitesP, AgreementsP } from './panels_a'
import { TenantsP, OrgP } from './panels_b'
import BrandP from './BrandP'
import FooterP from './FooterP'
import { PlansP, ReferralP, WebhooksP, ApiKeysP } from './panels_c'
import { KbP, ModelsP, WorkflowP, TicketsP } from './panels_d'

// 菜单项：key 对应 admin store 中的面板标识，minLevel 为可见最低角色等级
interface Item { key: PanelKey; label: string; minLevel: number }

const ITEMS: Item[] = [
  { key: 'overview', label: 'admin.menuOverview', minLevel: 2 },
  { key: 'tenants', label: 'admin.menuTenants', minLevel: 4 },
  { key: 'plans', label: 'admin.menuPlans', minLevel: 3 },
  { key: 'referral', label: 'admin.menuReferral', minLevel: 3 },
  { key: 'org', label: 'admin.menuOrg', minLevel: 3 },
  { key: 'usage', label: 'admin.menuUsage', minLevel: 2 },
  { key: 'kb', label: 'admin.menuKb', minLevel: 2 },
  { key: 'models', label: 'admin.menuModels', minLevel: 4 },
  { key: 'workflow', label: 'admin.menuWorkflow', minLevel: 4 },
  { key: 'apikeys', label: 'admin.menuApikeys', minLevel: 3 },
  { key: 'webhooks', label: 'admin.menuWebhooks', minLevel: 3 },
  { key: 'tickets', label: 'admin.menuTickets', minLevel: 2 },
  { key: 'audit', label: 'admin.menuAudit', minLevel: 4 },
  { key: 'alerts', label: 'admin.menuAlerts', minLevel: 4 },
  { key: 'agreements', label: 'admin.menuAgreements', minLevel: 2 },
  { key: 'brand', label: 'admin.menuBrand', minLevel: 3 },
  { key: 'footer', label: 'admin.menuFooter', minLevel: 4 },
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
    case 'agreements': return <AgreementsP />
    case 'brand': return <BrandP />
    case 'footer': return <FooterP />
    default: return null
  }
}

// 后台控制台主组件：组合侧边菜单、顶部操作栏与动态面板
export default function AdminDashboard() {
  const ad = useAdmin()
  // 订阅语言变化，使菜单与角色标签随 UI 语言切换刷新
  const lang = useLang()
  // 租户级品牌定制
  const branding = useBranding()
  // 按当前用户等级过滤出可见菜单项
  const visible = ITEMS.filter((i) => ad.myLevel >= i.minLevel)

  return (
    <div className="admin-shell">
      <aside className="admin-side">
        <div style={{ fontWeight: 800, color: '#1a237e', padding: '6px 10px 14px' }}>
          {branding.brandLogo
            ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 60 }} />
            : `🌐 ${branding.brandName || t('admin.title')}`}
        </div>
        <Menu value={ad.panel} onChange={(v) => ad.gotoPanel(v as PanelKey)} style={{ border: 'none' }}>
          {visible.map((i) => (
            <Menu.MenuItem key={i.key} value={i.key}>{t(i.label)}</Menu.MenuItem>
          ))}
        </Menu>
      </aside>

      {/* 主内容区：顶部工具栏 + 当前面板 */}
      <main className="admin-main">
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 16 }}>
          {/* 超管专属：平台标识与租户切换器 */}
          {ad.isSuper && (
            <>
              <Tag theme="primary" variant="light">{t('admin.tagPlatformAdmin')}</Tag>
              <Select
                value={ad.activeTenantId}
                onChange={(v) => ad.switchTenant(Number(v))}
                style={{ width: 240 }}
                clearable={false}
                options={[
                  { label: t('admin.tenantRoot'), value: 0 },
                  ...ad.tenants.map((x) => ({ label: `#${x.id} ${x.name}`, value: x.id })),
                ]}
                placeholder={t('admin.tenantSwitchPlaceholder')}
              />
            </>
          )}
          {/* 非超管时显示当前管理范围标签 */}
          {!ad.isSuper && ad.myLevel >= 3 && <Tag theme="primary" variant="light">{t('admin.tagCompany')}</Tag>}
          {ad.myLevel === 2 && <Tag variant="light">{t('admin.tagDept')}</Tag>}
          {/* 右侧占位，把后续操作按钮推到最右 */}
          <div style={{ flex: 1 }} />
          <Bell />
          <Button size="small" variant="text" onClick={toggleLang}>{lang === 'zh' ? 'EN' : '中文'}</Button>
          <AccountMenu showWorkbench onGotoWorkbench={() => {
            window.history.pushState({}, '', '/')
            window.dispatchEvent(new PopStateEvent('popstate'))
          }} />
        </div>

        {renderPanel(ad.panel)}
        <SiteFooter />
      </main>
    </div>
  )
}
