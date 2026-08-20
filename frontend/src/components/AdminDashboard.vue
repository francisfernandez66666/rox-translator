<!-- ============================================================================
   components/AdminDashboard.vue — 管理后台壳组件
   职责：仅负责布局与导航编排，不做任何业务数据加载
   - 顶部一级分类（经营/组织/内容/引擎/开放）+ 组内左侧面板列表 + <component :is> 渲染
   - 租户切换器（超管）+ 用户信息 + 退出登录
   - 面板业务逻辑全部下沉到 components/admin/*.vue 子面板组件
   ============================================================================ -->
<template>
  <!-- ===== 后台整体布局：左侧栏 + 主内容区 ===== -->
  <div class="ad-wrap">
    <!-- ===== 左侧侧边栏：品牌 / 租户切换 / 一级分类 / 面板列表 / 用户信息 ===== -->
    <aside class="ad-side">
      <div class="ad-brand">🏢 {{ t('admin.console') }}</div>

      <!-- 租户切换器（仅超管，前端统称"组织"） -->
      <div v-if="isSuper && tenantList.length" class="ad-tenant-switch">
        <label>{{ t('admin.currentOrg') }}</label>
        <select :value="activeTenantId" class="ad-input" @change="switchTenant(Number(($event.target as HTMLSelectElement).value))">
          <option v-for="t in tenantList" :key="t.id" :value="t.id">[{{ t.id }}] {{ t.name }} ({{ t.code }})</option>
        </select>
      </div>

      <!-- 语言切换 -->
      <div class="ad-lang-switch">
        <button class="ad-lang-btn" @click="toggleLang()">{{ lang === 'zh' ? 'English' : '中文' }}</button>
      </div>

      <!-- ===== 顶部一级分类 ===== -->
      <div class="ad-cats">
        <button
          v-for="cat in visibleCategories"
          :key="cat.key"
          :class="['ad-cat-btn', activeCategory === cat.key ? 'on' : '']"
          @click="activeCategory = cat.key"
        >
          {{ t(cat.label) }}
        </button>
      </div>

      <!-- ===== 组内左侧面板列表 ===== -->
      <nav class="ad-nav">
        <div class="ad-group-title">{{ t(currentCategoryLabel) }}</div>
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
        <div class="ad-user">{{ user?.display_name || user?.username }} ({{ roleName(user?.role) }})</div>
        <a href="/" class="ad-back">← {{ t('admin.backWorkspace') }}</a>
        <button class="ad-logout" @click="logout">{{ t('common.logout') }}</button>
      </div>
    </aside>

    <!-- ===== 主内容区：动态渲染当前面板 ===== -->
    <main class="ad-main">
      <div v-if="!isAdmin" class="ad-forbid">{{ t('admin.forbid') }}</div>
      <component :is="currentPanelComponent" v-else />
    </main>
  </div>
</template>

<script setup lang="ts">
// Vue 核心组合式 API
import { ref, computed, watch, onMounted } from 'vue'
// API：token 读写 + 用户类型
import { setAuthToken, type AuthUser } from '@/api'
// 后台共享状态（用户/租户/角色等级）
import { user, activeTenantId, tenantList, myLevel, isAdmin, isSuper, roleName, switchTenant, loadTenants } from './admin/store'
// 子面板组件
import Overview from './admin/Overview.vue'
import Alerts from './admin/Alerts.vue'
import Usage from './admin/Usage.vue'
import Billing from './admin/Billing.vue'
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

// ---- 一级分类 + 面板编排（min 为所需角色等级：2=租户管理员，3=超级管理员） ----
// label 为 i18n key（经 t() 渲染），中文与英文自动切换
const categories = [
  {
    key: 'ops', label: 'admin.ops',
    panels: [
      { key: 'overview', label: 'admin.overview', comp: Overview, min: 2 },
      { key: 'alerts', label: 'admin.alerts', comp: Alerts, min: 2 },
      { key: 'usage', label: 'admin.usage', comp: Usage, min: 2 },
      { key: 'billing', label: 'admin.billing', comp: Billing, min: 2 },
    ],
  },
  {
    key: 'org', label: 'admin.org',
    panels: [
      { key: 'users', label: 'admin.users', comp: Users, min: 2 },
      { key: 'org', label: 'admin.orgs', comp: Org, min: 2 },
      { key: 'tenants', label: 'admin.tenants', comp: Tenants, min: 3 },
      { key: 'invites', label: 'admin.invites', comp: Invites, min: 3 },
    ],
  },
  {
    key: 'content', label: 'admin.content',
    panels: [
      { key: 'kb', label: 'admin.kb', comp: Kb, min: 2 },
    ],
  },
  {
    key: 'engine', label: 'admin.engine',
    panels: [
      { key: 'models', label: 'admin.models', comp: Models, min: 2 },
      { key: 'workflow', label: 'admin.workflow', comp: Workflow, min: 2 },
    ],
  },
  {
    key: 'open', label: 'admin.open',
    panels: [
      { key: 'apikeys', label: 'admin.apikeys', comp: ApiKeys, min: 2 },
      { key: 'webhooks', label: 'admin.webhooks', comp: Webhooks, min: 2 },
      { key: 'tickets', label: 'admin.tickets', comp: Tickets, min: 2 },
    ],
  },
]

// 当前激活的一级分类与面板
const activeCategory = ref(categories[0].key)
// 当前激活的面板 key（默认第一个分类下的第一个面板）
const activePanel = ref(categories[0].panels[0].key)

// 可见分类（该分类下至少有一个面板对当前角色开放）
const visibleCategories = computed(() => categories.filter(c => c.panels.some(p => myLevel.value >= p.min)))
// 当前分类下可见面板
const visiblePanels = computed(() => {
  const cat = categories.find(c => c.key === activeCategory.value)
  return (cat ? cat.panels : []).filter(p => myLevel.value >= p.min)
})
// 当前分类 i18n key
const currentCategoryLabel = computed(() => categories.find(c => c.key === activeCategory.value)?.label || '')
// 当前渲染的面板组件
const currentPanelComponent = computed(() => {
  const cat = categories.find(c => c.key === activeCategory.value)
  const p = cat?.panels.find(x => x.key === activePanel.value)
  return p?.comp
})

// 切换分类时自动落到该分类第一个可见面板
watch(activeCategory, () => {
  if (visiblePanels.value.length) activePanel.value = visiblePanels.value[0].key
})
// 角色变化（如被降权）时确保当前面板仍可见
watch(visiblePanels, (list) => {
  if (list.length && !list.some(p => p.key === activePanel.value)) {
    activePanel.value = list[0].key
  }
})

// 退出登录：清空 token 并通知父组件
function logout() {
  setAuthToken('')
  emit('logout')
}

// 挂载：超管加载租户列表（供租户切换器使用）
onMounted(() => {
  if (isSuper.value) loadTenants()
})
</script>