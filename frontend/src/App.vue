<!-- ============================================================================
   App.vue — 应用根组件
   职责：负责全局路由分发与页面切换
   - 会话恢复中 → 显示恢复画面
   - 未登录 → 展示登录页（按路径区分前台 /admin 后台）
   - /admin 路径且已登录 → 渲染管理后台 AdminDashboard
   - 其余情况 → 渲染翻译工作台（顶部栏 + 聊天窗口 + 设置弹窗）
   ============================================================================ -->
<template>
  <!-- ===== 顶层路由分发：会话恢复中（避免刷新时闪现登录页再跳转） ===== -->
  <div v-if="restoring" class="restore-screen"><div class="restore-spinner"></div></div>

  <!-- ===== 未登录 → 统一登录页（按路径区分前台/后台） ===== -->
  <Login v-else-if="!authUser" :mode="isAdminRoute ? 'admin' : 'home'" @ok="onLogin" />

  <!-- ===== 管理后台 /admin（已登录且路径以 /admin 开头） ===== -->
  <AdminDashboard v-else-if="isAdminRoute" :user="authUser" @logout="onLogout" />

  <!-- ===== 翻译工作台（登录后） ===== -->
  <div v-else id="app-root" :class="isMobile ? 'device-mobile' : 'device-desktop'">
    <!-- ===== 顶部导航栏：品牌信息 + 在线状态 + 设置/退出按钮 ===== -->
    <header class="app-header">
      <div class="header-left">
        <span class="header-icon">🌐</span>
        <span class="header-title">{{ t('app.title') }}</span>
        <span class="header-user">{{ authUser.display_name || authUser.username }}</span>
      </div>
      <div class="header-right">
        <span class="status-indicator" :class="store.isBackendOnline ? 'online' : 'offline'">
          <span class="status-dot"></span>
          {{ store.isBackendOnline ? t('common.online') : t('common.offline') }}
        </span>
        <button class="gear-btn" @click="toggleLang()" :title="lang === 'zh' ? 'English' : '中文'">{{ lang === 'zh' ? 'EN' : '中' }}</button>
        <button class="gear-btn" @click="showSettings = true" :title="t('common.settings')">⚙️</button>
        <button class="gear-btn" @click="logout" :title="t('common.logout')">⎋</button>
      </div>
    </header>

    <!-- ===== 主内容区：翻译引擎启动加载屏 / 聊天窗口 ===== -->
    <main class="chat-main">
      <div v-if="store.isBackendLoading" class="loading-screen">
        <div class="loading-spinner"></div>
        <p class="loading-text">{{ t('app.starting') }}</p>
      </div>
      <ChatWindow v-else />
    </main>

    <!-- ===== 设置弹窗：翻译模型选择（Teleport 到 body） ===== -->
    <Teleport to="body">
      <div v-if="showSettings" class="modal-overlay" @click.self="showSettings = false">
        <div class="settings-panel">
          <div class="settings-title">{{ t('common.settings') }}</div>
          <div class="settings-item">
            <label>{{ t('app.modelLabel') }}</label>
            <select v-model="currentModel" @change="onModelChange">
              <optgroup label="SiliconFlow">
                <option value="tencent/Hunyuan-MT-7B">Hunyuan-MT-7B {{ t('app.modelRec33') }}</option>
                <option value="THUDM/GLM-Z1-9B-0414">GLM-Z1-9B-0414 {{ t('app.modelBackup') }}</option>
                <option value="Qwen/Qwen2.5-7B-Instruct">Qwen2.5-7B</option>
              </optgroup>
              <optgroup label="Zhipu">
                <option value="glm-4.7-flash">GLM-4.7-Flash {{ t('app.modelFast') }}</option>
                <option value="glm-4-flash">GLM-4-Flash {{ t('app.modelFallback') }}</option>
              </optgroup>
            </select>
          </div>
          <button class="settings-close" @click="showSettings = false">{{ t('common.close') }}</button>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
// Vue 核心组合式 API（响应式、生命周期）
import { ref, onMounted, onUnmounted, computed } from 'vue'
// 全局聊天状态 Store
import { useChatStore } from '@/stores/chat'
// 子组件：聊天窗口 / 登录页 / 管理后台
import ChatWindow from './components/ChatWindow.vue'
import Login from './components/Login.vue'
import AdminDashboard from './components/AdminDashboard.vue'
// API：token 读写与用户信息查询
import { getAuthToken, setAuthToken, authMe, type AuthUser } from '@/api'
// 国际化：文案取词 + 语言切换
import { t, lang, toggleLang } from '@/i18n'

// 全局聊天 Store 实例
const store = useChatStore()
// 设置弹窗显隐
const showSettings = ref(false)
// 当前选中的翻译模型（与 Store 保持同步）
const currentModel = ref(store.selectedModel)

// 是否处于管理后台路由（/admin）
const isAdminRoute = computed(() => window.location.pathname.startsWith('/admin'))
// 当前登录用户（null 表示未登录，显示登录页）
const authUser = ref<AuthUser | null>(null)
// 会话恢复中标记：避免刷新时闪现登录页
const restoring = ref(true)

// 角色等级：普通用户(1) < 租户管理员(2) < 超级管理员(3)，兼容旧值 approver/admin
function roleLevel(r?: string): number {
  if (r === 'super_admin' || r === 'admin') return 3
  if (r === 'tenant_admin' || r === 'approver') return 2
  return 1
}

// 恢复会话：本地有 token 则向后端校验，管理后台仅放行租户管理员及以上
async function restoreSession() {
  try {
    if (getAuthToken()) {
      const r = await authMe()
      if (r.success && r.user) {
        if (isAdminRoute.value && roleLevel(r.user.role) < 2) {
          authUser.value = null
          return
        }
        authUser.value = r.user
      } else {
        authUser.value = null
      }
    }
  } finally {
    restoring.value = false
  }
}

// 登录成功回调：写入当前用户（切换账号时重置会话，防止串号）
function onLogin(user: unknown) {
  authUser.value = user as AuthUser
  store.reset()
}

// 退出登录：清空用户、token 与会话缓存
function onLogout() {
  authUser.value = null
  setAuthToken('')
  store.reset()
}

// logout 退出登录：清除本地 token 与用户状态并跳转登录页。
// 无参数无返回，由顶栏退出按钮触发。
function logout() {
  onLogout()
}

// 切换翻译模型
function onModelChange() {
  store.setSelectedModel(currentModel.value)
}

// 响应式：检测移动端布局（宽度 ≤ 768px 判定为移动端）
const windowWidth = ref(window.innerWidth)
const isMobile = ref(windowWidth.value <= 768)

// 窗口尺寸变化回调：更新移动端标记
function onResize() {
  windowWidth.value = window.innerWidth
  isMobile.value = windowWidth.value <= 768
}

// 初始化：恢复登录态 + 检测后端在线状态 + 监听窗口尺寸
onMounted(async () => {
  window.addEventListener('resize', onResize)
  await restoreSession()
  await store.checkBackendHealth()
})

// 组件卸载：移除窗口尺寸监听
onUnmounted(() => {
  window.removeEventListener('resize', onResize)
})
</script>

<style>
/* 全局样式重置：清除默认边距并统一盒模型 */
* { margin: 0; padding: 0; box-sizing: border-box; }
html, body { height: 100%; overflow: hidden; }
/* 全局基础样式：字体栈 / 背景色 / 文字颜色 / 抗锯齿 */
body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'PingFang SC', 'Microsoft YaHei', sans-serif;
  background: #f5f5f5;
  color: #202124;
  -webkit-font-smoothing: antialiased;
}

/* 工作台根容器：撑满视口，纵向 Flex 布局 */
#app-root { height: 100vh; height: 100dvh; display: flex; flex-direction: column; overflow: hidden; }

/* 会话恢复中加载画面 */
.restore-screen {
  height: 100vh; display: flex; align-items: center; justify-content: center; background: #f5f7fb;
}
.restore-spinner {
  width: 40px; height: 40px; border: 4px solid #e0e0e0; border-top-color: #1a237e; border-radius: 50%;
  animation: spin 0.8s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }

/* 顶部导航栏：品牌 + 状态 + 操作按钮 */
.app-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 12px 20px; background: #2e7d32; color: #fff; flex-shrink: 0;
}
.header-left { display: flex; align-items: center; gap: 10px; }
.header-icon { font-size: 24px; }
.header-title { font-size: 18px; font-weight: 600; }
.header-right { display: flex; align-items: center; gap: 12px; }

/* 设置/退出按钮 */
.gear-btn {
  border: none; background: rgba(255,255,255,0.15); color: #fff;
  font-size: 18px; cursor: pointer; padding: 6px 10px; border-radius: 8px;
  transition: background 0.2s;
}
.gear-btn:hover { background: rgba(255,255,255,0.25); }

/* 后端在线状态指示器 */
.status-indicator { display: flex; align-items: center; gap: 6px; font-size: 12px; }
.status-dot { width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0; }
.status-indicator.online .status-dot { background: #69f0ae; box-shadow: 0 0 6px rgba(105,240,174,0.6); }
.status-indicator.offline .status-dot { background: #ff5252; box-shadow: 0 0 6px rgba(255,82,82,0.5); }

/* 聊天主区域：占据剩余高度 */
.chat-main { flex: 1; display: flex; flex-direction: column; background: #fff; overflow: hidden; min-width: 0; }

/* ===== 设置弹窗遮罩层（半透明黑色背景） ===== */
.modal-overlay {
  position: fixed; top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(0,0,0,0.4);
  display: flex; align-items: center; justify-content: center;
  z-index: 1000; animation: fadeIn 0.15s ease;
}
@keyframes fadeIn { from { opacity: 0; } to { opacity: 1; } }

/* 设置面板卡片 */
.settings-panel {
  background: #fff; border-radius: 16px; padding: 28px;
  width: 380px; max-width: 90vw;
  box-shadow: 0 8px 32px rgba(0,0,0,0.15);
}
.settings-title { font-size: 18px; font-weight: 600; margin-bottom: 20px; color: #202124; }
.settings-item { margin-bottom: 20px; }
.settings-item label { display: block; font-size: 14px; color: #5f6368; margin-bottom: 8px; }
.settings-item select {
  width: 100%; padding: 10px 12px; border: 1px solid #e0e0e0; border-radius: 10px;
  font-size: 14px; background: #f8f9fa; cursor: pointer;
}
.settings-item select:focus { outline: none; border-color: #2e7d32; }
.settings-close {
  width: 100%; padding: 10px; border: none; border-radius: 10px;
  background: #2e7d32; color: #fff; font-size: 15px; cursor: pointer;
  transition: background 0.2s;
}
.settings-close:hover { background: #1b5e20; }

/* ===== 移动端适配 ===== */
@media (max-width: 768px) {
  .app-header { padding: 10px 16px; }
  .header-title { font-size: 16px; }
  .device-mobile .chat-main { background: #fff; }
}

/* ===== 翻译引擎加载屏（等待后端启动） ===== */
.loading-screen {
  flex: 1; display: flex; flex-direction: column;
  align-items: center; justify-content: center;
  gap: 20px;
}
.loading-spinner {
  width: 48px; height: 48px;
  border: 4px solid #e0e0e0;
  border-top-color: #2e7d32;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
.loading-text { font-size: 16px; color: #5f6368; }
</style>
