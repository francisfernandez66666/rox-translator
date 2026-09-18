// ============================================================================
// components/admin/AdminDashboard.tsx — 后台控制台整体布局与导航
// （2026-09-18 起为 AdminShell 版；此前为 TDesign Menu 版）
// 职责：提供左侧菜单、顶部工具栏，并按权限渲染对应子面板。
// 映射 Vue 版：frontend/src/components/admin/AdminDashboard.vue（或同目录下菜单壳组件）。
// 权限矩阵与 Vue 版一致：L4 全量；L3 无 Tenants/Models/Workflow/Audit/Alerts；
// L2 仅 Overview/Usage/Kb/Tickets。超管含租户切换器。
// ★ 移动端自适应（2026-09-07）：≤900px 侧边栏转抽屉（汉堡按钮唤起 + 遮罩点关），
//   桌面端仍为固定侧栏，行为互不影响。
//   注：该抽屉逻辑已于 2026-09-18 上移到 ui/langcross AdminShell 内部
//   （自带 burger + scrim + drawer 状态），本文件不再持有 navOpen 本地状态。
// 2026-09-18 UI 融合：壳层（侧栏/顶栏/内容区）、按钮、状态胶囊、图标全部改用
//   ui/langcross 组件；菜单图标由 emoji 文案改为 16×16 自绘 SVG（见 Item.icon）。
// ============================================================================
import { useNavigate } from 'react-router-dom'
import { Button, StatusPill, AdminShell } from '@/ui/langcross/src'
import type { NavItem } from '@/ui/langcross/src'
import { useAdmin } from '@/stores/admin'
import type { PanelKey } from '@/stores/admin'
import { t, toggleLang, useLang } from '@/i18n'
import Bell from '@/components/Bell'
import AccountMenu from '@/components/AccountMenu'
import SiteFooter from '@/components/SiteFooter'
import { useBranding } from '@/branding'
import { Icon } from '@/ui/langcross/src'
import type { IconName } from '@/ui/langcross/src'

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
// icon 为 16×16 SVG 图标名（UI-ANNOTATIONS §3.1-13 侧栏九项；§0.1：emoji 只是图标占位）
interface Item { key: PanelKey; label: string; minLevel: number; icon: IconName }

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
  { key: 'overview', label: 'admin.menuOverview', minLevel: 2, icon: 'chart' },
  { key: 'tickets', label: 'admin.menuTickets', minLevel: 2, icon: 'chat' },
  { key: 'personal', label: 'admin.menuPersonal', minLevel: 2, icon: 'user' },
  { key: 'kb', label: 'admin.menuKb', minLevel: 2, icon: 'book' },
  { key: 'org', label: 'admin.menuOrg', minLevel: 3, icon: 'building' },
  { key: 'external', label: 'admin.menuExternal', minLevel: 3, icon: 'satellite' },
  { key: 'billing', label: 'hub.menuBilling', minLevel: 3, icon: 'gem' },
  { key: 'system', label: 'admin.menuSystem', minLevel: 4, icon: 'gear' },
  { key: 'assist', label: 'admin.menuAssist', minLevel: 3, icon: 'robot' }, // ★ autosales：AI 助手管理（超管/租户管理员）
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

  // ★ 菜单项映射为 AdminShell nav（TDesign Menu → AdminShell nav）
  //   label 在此处预先用 t() 求值：AdminShell 只渲染纯文本，无法自己解 i18n key；
  //   本组件订阅了 useLang()，语言切换会重渲染从而刷新这里的文案。
  //   icon 传 <Icon /> 节点而非名字：NavItem.icon 是 ReactNode 图标位。
  const nav: NavItem[] = visible.map((i) => ({
    key: i.key,
    label: t(i.label),
    icon: <Icon n={i.icon} />,
  }))

  // 顶栏右侧集群：语言切换、铃铛、租户切换器（超管）、角色/租户标签、账号菜单
  // marginLeft:auto —— AdminShell 顶栏左侧固定放汉堡按钮，右侧内容整体靠右对齐
  const topbar = (
    <div className="lc-topbar-cluster" style={{ display: 'flex', alignItems: 'center', gap: 12, marginLeft: 'auto' }}>
      <Button size="sm" variant="secondary" onClick={toggleLang}>{lang === 'zh' ? 'EN' : '中文'}</Button>
          <Bell />
          {ad.isSuper && (
        <>
          {/* 角色标签：Tag → StatusPill（tone=idle，与新版中性胶囊样式一致） */}
          <StatusPill tone="idle">{t('admin.tagPlatformAdmin')}</StatusPill>
          {/* 超管租户切换器：TDesign Select → 原生 select + lc-select 样式。
              值走字符串，故 onChange 必须 Number() 还原成租户 ID 再存 store。 */}
          <select
            className="lc-select"
                style={{ width: 240 }}
                value={ad.activeTenantId}
            onChange={(e) => ad.switchTenant(Number(e.target.value))}
          >
            <option value={0}>{t('admin.tenantRoot')}</option>
            {ad.tenants.map((x) => (
              <option key={x.id} value={x.id}>{`#${x.id} ${x.name}`}</option>
            ))}
          </select>
        </>
      )}
          {/* 部门管理员：组织级标签 */}
      {ad.myLevel === 2 && <StatusPill tone="idle">{t('admin.tagDept')}</StatusPill>}
          {/* ★ F1：非超管顶栏显示其管理范围租户名 */}
      {!ad.isSuper && !!ad.tenantName && <StatusPill tone="idle">{ad.tenantName}</StatusPill>}
      {/* ★ E8：走 router navigate（pushState+合成 popstate 与 react-router 脱节） */}
      <AccountMenu showWorkbench onGotoWorkbench={() => navigate('/')} />
    </div>
  )

  return (
    <>
      {/* AdminShell 自带侧栏语言按钮为静态占位，此处用顶栏的语言切换替代，故隐藏之 */}
      <style>{'.adm-shell .lc-side-lang{display:none}'}</style>
      <AdminShell
        className="adm-shell"
        nav={nav}
        activeKey={ad.panel}
        onNavigate={(k) => ad.gotoPanel(k as PanelKey)}
        // 有自定义 Logo 时不再重复渲染品牌名文字（Logo 图内已含品牌字），置空由 appIcon 表达
        appName={branding.brandLogo ? '' : (branding.brandName || t('admin.title'))}
        // Logo 按侧栏 24px 高度等比缩放，objectFit 交给浏览器按原始比例拉伸
        appIcon={branding.brandLogo ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 24 }} /> : undefined}
        topbar={topbar}
      >
        {/* 按当前面板 key 渲染子面板：这里不再做等级校验（菜单可见性已由 visible 过滤），
            并保留旧 key 分支以兼容历史深链/书签 */}
        {renderPanel(ad.panel)}
        <SiteFooter />
      </AdminShell>
    </>
  )
}
