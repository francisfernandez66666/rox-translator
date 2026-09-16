// ============================================================================
// components/admin/AdminDashboard.tsx — 后台控制台整体布局与导航（TDesign Menu 版）
// 职责：提供左侧菜单、顶部工具栏，并按权限渲染对应子面板。
// 映射 Vue 版：frontend/src/components/admin/AdminDashboard.vue（或同目录下菜单壳组件）。
// 权限矩阵与 Vue 版一致：L4 全量；L3 无 Tenants/Models/Workflow/Audit/Alerts；
// L2 仅 Overview/Usage/Kb/Tickets。超管含租户切换器。
// ★ 移动端自适应（2026-09-07）：≤900px 侧边栏转抽屉（汉堡按钮唤起 + 遮罩点关），
//   桌面端仍为固定侧栏，行为互不影响。
// ============================================================================
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Menu, Select, Tag } from 'tdesign-react'
import { useAdmin } from '@/stores/admin'
import type { PanelKey } from '@/stores/admin'
import { t, toggleLang, useLang } from '@/i18n'
import Bell from '@/components/Bell'
import AccountMenu from '@/components/AccountMenu'
import SiteFooter from '@/components/SiteFooter'
import { useBranding } from '@/branding'

import Overview from './panels_a'
import MailTplP from './MailTplP'
import FooterP from './FooterP'
import DataSourcesP from './DataSourcesP'
import { UsersP, InvitesP, AlertsP, AuditP, UsageP } from './panels_a'
import { TenantsP, OrgP } from './panels_b'
import BrandP from './BrandP'
import SystemSettingsP from './SystemSettingsP'
import ExternalCallsP from './ExternalCallsP'
import PersonalCenterP from './PersonalCenterP'
import { PlansP, ReferralP, WebhooksP, ApiKeysP } from './panels_c'
import { KbP, ModelsP, WorkflowP, TicketsP } from './panels_d'
import { OpsP } from './panels_e'
import { ReconcileP } from './ReconcileP' // ★ F9 对账视图
import BillingHubP from './BillingHubP'
import AssistP from './AssistP' // ★ autosales：AI 助手管理

// 菜单项接口定义：key 对应 admin store 中的面板标识，minLevel 为可见最低角色等级
interface Item { key: PanelKey; label: string; minLevel: number }

// 菜单项配置：定义所有可展示的面板及其最低角色等级要求
// ★ 2026-09-03 重组：协议签署并入「系统设置」；开放 API+回调通知并入「外部调用」；
//   邀请好友+任务中心并入「个人中心」；流程引擎并入「系统设置」（修复白板）。
// ★ 2026-09-15 Tab 精简（任务3）：一级菜单 20 → 8——「计费与套餐」Hub 收纳
//   套餐/租户/对账；「系统与运维」Hub 收纳注册触达/邮件模板/流程/协议/审计/
//   运营策略/模型供应商/品牌页脚；用量明细并入总览、数据源并入知识库、
//   成员并入组织；旧 key 仍保留在 renderPanel 深链兼容分支中。
const ITEMS: Item[] = [
  // ★ Tab 合并精简（2026-09-15）：20 个一级项 → 9 个。原一级 usage/dataSources/invites/
  //   tenants/plans/reconcile/alerts/audit/mailTpl/footer/brand/models/ops/workflow 等
  //   并入对应 Hub 子 tab（总览/知识库/组织/计费/外部调用/系统与运维/系统设置）；
  //   renderPanel 保留旧 key 分支以兼容历史深链/书签（渲染独立面板）。
  { key: 'overview', label: 'admin.menuOverview', minLevel: 2 },
  { key: 'tickets', label: 'admin.menuTickets', minLevel: 2 },
  { key: 'personal', label: 'admin.menuPersonal', minLevel: 2 },
  { key: 'kb', label: 'admin.menuKb', minLevel: 2 },
  { key: 'org', label: 'admin.menuOrg', minLevel: 3 },
  { key: 'external', label: 'admin.menuExternal', minLevel: 3 },
  { key: 'billing', label: 'hub.menuBilling', minLevel: 3 },
  { key: 'system', label: 'admin.menuSystem', minLevel: 4 },
  { key: 'assist', label: 'admin.menuAssist', minLevel: 3 }, // ★ autosales：AI 助手管理（超管/租户管理员）
]

/** 根据当前选中的面板 key 返回对应组件（集中分发，避免在 JSX 中写长 switch） */
function renderPanel(p: PanelKey) {
  switch (p) {
    case 'overview': return <Overview />
    case 'tenants': return <TenantsP />
    case 'plans': return <PlansP />
    case 'referral': return <ReferralP />
    case 'personal': return <PersonalCenterP />
    case 'external': return <ExternalCallsP />
    case 'org': return <OrgP />
    case 'kb': return <KbP />
    case 'models': return <ModelsP />
    case 'ops': return <OpsP />
    case 'reconcile': return <ReconcileP />
    case 'workflow': return <WorkflowP />
    case 'apikeys': return <ApiKeysP />
    case 'webhooks': return <WebhooksP />
    case 'tickets': return <TicketsP />
    case 'users': return <UsersP />
    case 'agreements': return <SystemSettingsP />
    case 'brand': return <BrandP />
    case 'system': return <SystemSettingsP />
    // ★ E9：补全孤儿面板分发（旧 default→null 使菜单/跳转渲染空白）
    case 'invites': return <InvitesP />
    case 'usage': return <UsageP />
    case 'alerts': return <AlertsP />
    case 'audit': return <AuditP />
    case 'mailTpl': return <MailTplP />
    case 'footer': return <FooterP />
    case 'dataSources': return <DataSourcesP />
    case 'billing': return <BillingHubP /> // ★ Tab 精简：计费 Hub（套餐/租户/对账）
    case 'assist': return <AssistP /> // ★ autosales：AI 助手管理（内嵌 ai-assist 管理台）
    default: return null
  }
}

/** 后台控制台主组件：组合侧边菜单、顶部操作栏与动态面板路由 */
export default function AdminDashboard() {
  const navigate = useNavigate()
  const ad = useAdmin()
  // 订阅语言变化，使菜单与角色标签随 UI 语言切换刷新
  const lang = useLang()
  // 租户级品牌定制
  const branding = useBranding()

  // 按当前用户等级与租户类型过滤可见菜单项：
  //  - 企业管理（org）仅企业租户可见，个人用户不显示
  //  - 「个人中心」含邀请好友 + 任务中心，对 L2+ 管理员可见（ReferralP 内部按 is_personal 自适应提示）
  const visible = ITEMS.filter((i) => {
    if (ad.myLevel < i.minLevel) return false
    if (i.key === 'org' && ad.isPersonal) return false
    return true
  })

  // ★ 移动端抽屉导航状态（≤900px 时侧边栏转抽屉，点菜单/遮罩关闭）
  const [navOpen, setNavOpen] = useState(false)
  // 选择菜单项：切面板并关闭移动端抽屉
  const onMenuChange = (v: PanelKey) => { ad.gotoPanel(v); setNavOpen(false) }

  return (
    <div className="admin-shell">
      {/* 移动端汉堡按钮（桌面端由 CSS 隐藏） */}
      <button type="button" className="admin-nav-toggle" aria-label="nav" onClick={() => setNavOpen(true)}>☰</button>
      {/* 移动端抽屉遮罩 */}
      {navOpen && <div className="admin-side-mask" onClick={() => setNavOpen(false)} />}

      {/* 侧边栏：品牌 Logo、菜单列表、语言切换按钮（移动端为抽屉） */}
      <aside className={'admin-side' + (navOpen ? ' open' : '')}>
        <div style={{ fontWeight: 800, color: 'var(--td-brand-color-active, #1f33d6)', padding: '6px 10px 14px' }}>
          {branding.brandLogo
            ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 60 }} />
            : `🌐 ${branding.brandName || t('admin.title')}`}
        </div>
        {/* 侧边菜单：点击切换面板 */}
        <Menu value={ad.panel} onChange={(v) => onMenuChange(v as PanelKey)} style={{ border: 'none' }}>
          {visible.map((i) => (
            <Menu.MenuItem key={i.key} value={i.key}>{t(i.label)}</Menu.MenuItem>
          ))}
        </Menu>
        {/* 语言切换按钮置于侧边栏底部，常驻可见 */}
        <div style={{ marginTop: 'auto', padding: '10px 10px 0' }}>
          <Button size="small" variant="outline" block onClick={toggleLang}>{lang === 'zh' ? 'EN' : '中文'}</Button>
        </div>
      </aside>

      {/* 主内容区：顶部工具栏 + 当前面板 */}
      <main className="admin-main">
        {/* 顶部工具栏：通知铃铛、租户切换器（超管）、管理范围标签、账号菜单 */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 16 }}>
          {/* 右侧占位，把管理范围标签与租户切换器推到 admin 下拉菜单左侧 */}
          <div style={{ flex: 1 }} />
          <Bell />
          {/* 超管专属：平台标识与租户切换器（置于 admin 下拉左侧） */}
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
          {/* 部门管理员：组织级标签 */}
          {ad.myLevel === 2 && <Tag variant="light">{t('admin.tagDept')}</Tag>}
          {/* ★ F1：非超管顶栏显示其管理范围租户名 */}
          {!ad.isSuper && !!ad.tenantName && <Tag theme="primary" variant="light">{ad.tenantName}</Tag>}
          <AccountMenu showWorkbench onGotoWorkbench={() => {
            // ★ E8：走 router navigate（pushState+合成 popstate 与 react-router 脱节）
            navigate('/')
          }} />
        </div>

        {/* 根据当前选中的面板 key 渲染对应子面板 */}
        {renderPanel(ad.panel)}
        <SiteFooter />
      </main>
    </div>
  )
}
