<!-- ============================================================================
   components/AdminDashboard.vue — 管理后台壳组件（按角色分工作台）
   职责：仅负责布局与导航编排，不做任何业务数据加载
   - 三种工作台按角色自动切换（扁平导航，无二级分类）：
     · 超管（L4）平台运营：租户/套餐/订单/组织架构/行业包/全局模型/告警
     · 租管（L3）企业管理：总览/用量/组织/成员/知识库/模型接入/订阅/开放能力
     · 部门管理员（L2）部门管理：部门总览/用量/成员/部门知识库/工单
   - 面板业务逻辑全部下沉到 components/admin/*.vue 子面板组件
   ============================================================================ -->
<template>
  <!-- ===== 后台整体布局：左侧栏 + 主内容区 ===== -->
  <div class="ad-shell">
    <!-- ★ 固定页眉（四期体验增强）：品牌 | 消息中心 | 账号 | 语言切换 | 退出 -->
    <header class="adm-topbar">
      <span class="adm-brand">🏢 {{ t('admin.console') }}</span>
      <div class="adm-top-right">
        <!-- 消息中心（复用前台铃铛） -->
        <Bell />
        <!-- 语言切换 -->
        <button class="adm-gear" @click="toggleLang()">{{ lang === 'zh' ? 'EN' : '中' }}</button>
        <!-- ☰ 账号下拉：修改密码（独立弹窗）/ 退出 -->
        <details class="account-menu">
          <summary title="账号菜单">👤 {{ user?.display_name || user?.username }}</summary>
          <div class="menu-drop">
            <button class="menu-item" @click="openPwd">🔒 {{ t('pwd.title') }}</button>
            <a class="menu-item" href="/">{{ t('admin.backWorkspace') }}</a>
            <button class="menu-item menu-logout" @click="logout">⎋ {{ t('common.logout') }}</button>
          </div>
        </details>
      </div>
    </header>

    <div class="ad-body">
    <!-- ===== 左侧侧边栏：工作台标识 / 租户切换 / 导航 ===== -->
    <aside class="ad-side">
      <div class="ad-ws-tag">{{ t(currentWorkspace?.label || '') }}</div>

      <!-- 租户切换器（仅超管）：0=平台根组织「翻译助手」 -->
      <div v-if="isSuper && tenantList.length" class="ad-tenant-switch">
        <label>{{ t('admin.currentOrg') }}</label>
        <select :value="activeTenantId" class="ad-input" @change="switchTenant(Number(($event.target as HTMLSelectElement).value))">
          <option :value="0">🏠 {{ t('admin.platformRoot') }}</option>
          <option v-for="t in tenantList" :key="t.id" :value="t.id">[{{ t.id }}] {{ t.name }} ({{ t.code }})</option>
        </select>
      </div>

      <!-- ===== 扁平导航（当前工作台的全部面板） ===== -->
      <nav class="ad-nav">
        <button
          v-for="p in visiblePanels"
          :key="p.key"
          :class="['ad-nav-item', activePanel === p.key ? 'on' : '']"
          @click="activePanel = p.key"
        >
          {{ t(p.label) }}
        </button>
      </nav>

      <div class="ad-side-foot">
        <a href="/" class="ad-back">← {{ t('admin.backWorkspace') }}</a>
      </div>
    </aside>

    <!-- ===== 主内容区：动态渲染当前面板 ===== -->
    <main class="ad-main">
      <div v-if="!isAdmin" class="ad-forbid">{{ t('admin.forbid') }}</div>
      <component :is="currentPanelComponent" v-else />
    </main>
    </div><!-- /ad-body -->
  </div>
</template>

<script setup lang="ts">
// Vue 核心组合式 API
import { ref, computed, watch, onMounted } from 'vue'
// API：token 读写 + 用户类型
import { setAuthToken, type AuthUser , meContext } from '@/api'
// 后台共享状态（用户/租户/角色等级）
import { user, activeTenantId, tenantList, myLevel, isAdmin, isSuper, roleName, switchTenant, loadTenants } from './admin/store'
// 子面板组件
import Overview from './admin/Overview.vue'
import Alerts from './admin/Alerts.vue'
import Usage from './admin/Usage.vue'
import Billing from './admin/Billing.vue'
import Packages from './admin/Packages.vue'
import Users from './admin/Users.vue'
import Org from './admin/Org.vue'
import Tenants from './admin/Tenants.vue'
import Invites from './admin/Invites.vue'
import Kb from './admin/Kb.vue'
import Models from './admin/Models.vue'
import Workflow from './admin/Workflow.vue'
import ApiKeys from './admin/ApiKeys.vue'
import Webhooks from './admin/Webhooks.vue'
import Tickets from './admin/Tickets.vue'
import Audit from './admin/Audit.vue'
// ★ 站内信铃铛：后台消息通知中心（工单完成/反馈/告警等站内信）
import Bell from '../components/Bell.vue'
import Feedback from './admin/Feedback.vue'
import PasswordModal from '../components/PasswordModal.vue'
// ★ 套餐详情合并面板（四期：商业包+订阅收银合一；非超管只读）
import PlansPanel from './admin/PlansPanel.vue'
// 共享样式（非 scoped，供全部面板使用）
import './admin/admin.css'
// 国际化：文案取词 + 语言切换
import { t, lang, toggleLang } from '@/i18n'

// 组件事件：退出登录
const emit = defineEmits<{ logout: [] }>()
// 组件入参：当前登录用户
const props = defineProps<{ user: AuthUser | null }>()

// 同步用户到共享 store（供各面板复用角色等级判断）
user.value = props.user

interface PanelDef { key: string; label: string; comp: any }
interface WorkspaceDef { label: string; panels: PanelDef[] }

// ---- 三种角色工作台（按角色等级自动匹配，扁平导航） ----
// L4 超管 = 全部权限：平台运营面板 + 企业管理全部面板（跨租户聚合视角）
// L3 租管 = 企业管理视角；L2 部门管理员 = 部门管理视角
const workspaces: Record<number, WorkspaceDef> = {
  // 平台运营（超级管理员，全量菜单）
  4: {
    label: 'admin.wsPlatform',
    panels: [
      { key: 'overview', label: 'admin.platformOverview', comp: Overview },
      { key: 'tenants', label: 'admin.tenants', comp: Tenants },

      { key: 'plans', label: 'admin.plansDetail', comp: PlansPanel },
      { key: 'org', label: 'admin.orgs', comp: Org },

      { key: 'usage', label: 'admin.usage', comp: Usage },
      { key: 'kb', label: 'admin.industryKb', comp: Kb },
      { key: 'models', label: 'admin.globalModels', comp: Models },

      { key: 'apikeys', label: 'admin.apikeys', comp: ApiKeys },
      { key: 'webhooks', label: 'admin.webhooks', comp: Webhooks },
      { key: 'workflow', label: 'admin.workflow', comp: Workflow },
      { key: 'tickets', label: 'admin.tickets', comp: Tickets },
      { key: 'audit', label: 'admin.auditPanel', comp: Audit },
      { key: 'alerts', label: 'admin.alerts', comp: Alerts },
      { key: 'feedbacks', label: 'admin.feedbackPanel', comp: Feedback }, // ★ 用户反馈（四期新增，仅超管工作台）
    ],
  },
  // 企业管理（租户管理员）
  // 权限边界：租户管理/商业包/全局模型/流程evals/审计日志/系统告警 仅超管可见（后端接口同步收紧）
  3: {
    label: 'admin.wsCompany',
    panels: [
      { key: 'overview', label: 'admin.companyOverview', comp: Overview },
      { key: 'usage', label: 'admin.usage', comp: Usage },
      { key: 'org', label: 'admin.orgs', comp: Org },

      { key: 'kb', label: 'admin.kbPack', comp: Kb },
      { key: 'plans', label: 'admin.plansDetail', comp: PlansPanel },

      { key: 'apikeys', label: 'admin.apikeys', comp: ApiKeys },
      { key: 'webhooks', label: 'admin.webhooks', comp: Webhooks },
      { key: 'tickets', label: 'admin.tickets', comp: Tickets },
    ],
  },
  // 部门管理（部门管理员）
  2: {
    label: 'admin.wsDept',
    panels: [
      { key: 'overview', label: 'admin.deptOverview', comp: Overview },
      { key: 'usage', label: 'admin.usage', comp: Usage },
      { key: 'users', label: 'admin.deptUsers', comp: Users },
      { key: 'kb', label: 'admin.deptKb', comp: Kb },
      { key: 'tickets', label: 'admin.tickets', comp: Tickets },
    ],
  },
}

// 当前角色对应的工作台（无管理权限则为 null）
const currentWorkspace = computed(() => workspaces[myLevel.value] || null)
// 当前工作台下可见面板
const visiblePanels = computed(() => currentWorkspace.value?.panels || [])
// 当前激活的面板 key
const activePanel = ref('')

// 角色加载/变化时：默认选中该工作台第一个面板；当前面板不可见时回退第一个
watch(visiblePanels, (list) => {
  if (!list.length) { activePanel.value = ''; return }
  if (!list.some(p => p.key === activePanel.value)) activePanel.value = list[0].key
}, { immediate: true })

// 当前渲染的面板组件
const currentPanelComponent = computed(() => visiblePanels.value.find(p => p.key === activePanel.value)?.comp)

// 退出登录：清空 token 并通知父组件
function logout() {
  setAuthToken('')
  emit('logout')
}


// ★ 自助修改密码弹窗状态
const pwdOpen = ref(false)
const pwdEmail = ref('')

// openPwd 打开改密弹窗并异步预填绑定邮箱
async function openPwd() {
  const d = document.querySelector('details.account-menu')
  if (d) d.removeAttribute('open')
  pwdOpen.value = true
  try {
    const r: any = await meContext()
    pwdEmail.value = r?.email || ''
  } catch { pwdEmail.value = '' }
}

// onPwdDone 改密成功提示
function onPwdDone() {
  alert(t('pwd.done'))
}

// 挂载：超管加载租户列表（供租户切换器使用）
onMounted(() => {
  if (isSuper.value) loadTenants()
})
</script>

<style scoped>
/* 工作台标识（侧边栏品牌下方，标明当前视角） */
.ad-ws-tag {
  margin: 4px 14px 10px;
  padding: 4px 10px;
  border-radius: 6px;
  background: rgba(64, 128, 255, 0.12);
  color: #4a7dff;
  font-size: 12px;
  font-weight: 600;
  text-align: center;
}

/* ★ 固定页眉（四期体验增强）：品牌 | 消息中心/账号/语言/退出 */
.adm-shell { height: 100vh; height: 100dvh; display: flex; flex-direction: column; overflow: hidden; }
.ad-shell .ad-wrap { flex: 1; min-height: 0; }
.adm-topbar { flex-shrink: 0; display: flex; align-items: center; justify-content: space-between;
  padding: 8px 16px; background: #1a73e8; color: #fff; box-shadow: 0 2px 6px rgba(0,0,0,.12); z-index: 40; }
.adm-brand { font-size: 15px; font-weight: 600; }
.adm-top-right { display: flex; align-items: center; gap: 10px; }
.adm-user { font-size: 13px; opacity: .95; }
.adm-gear { background: transparent; border: none; color: #fff; font-size: 14px; cursor: pointer; padding: 4px 6px; border-radius: 8px; }
.adm-gear:hover { background: rgba(255,255,255,.18); }
.adm-top-link { color: #fff; text-decoration: none; font-size: 13px; opacity: .95; }
.adm-top-link:hover { text-decoration: underline; }

/* ★ 壳层布局修正：抵消 admin.css 中 .ad-wrap/.ad-side 的 100vh/sticky */
.ad-shell > .ad-wrap { flex: 1; min-height: 0; height: auto !important; overflow: hidden; }
.ad-shell .ad-side { position: relative; top: auto; height: 100%; overflow-y: auto; }
.ad-shell .ad-main { overflow: auto; }

/* ★ 顶栏配色统一为侧栏色（用户反馈两种颜色） */
.adm-topbar { background: #1a237e; }
.adm-gear:hover { background: rgba(255,255,255,.18); }

/* ★ 账号下拉内修改密码项（打开独立弹窗） */
</style>

<style>
/* ★ 壳层布局（unscoped：需要覆盖 admin.css 全局规则） */
.ad-shell { display: flex; flex-direction: column; height: 100vh; height: 100dvh; overflow: hidden; }
.ad-shell > .adm-topbar { flex-shrink: 0; }
.ad-shell > .ad-body { display: flex; flex: 1; min-height: 0; overflow: hidden; }
.ad-shell .ad-side { width: 230px; background: #1a237e; color: #fff; display: flex; flex-direction: column;
  flex-shrink: 0; overflow-y: auto; }
.ad-shell .ad-main { flex: 1; padding: 28px 32px; overflow: auto; }
</style>
