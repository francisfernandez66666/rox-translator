<template>
  <!-- 会话恢复中（避免闪现登录页再跳转） -->
  <div v-if="restoring" class="restore-screen"><div class="restore-spinner"></div></div>

  <!-- 未登录 → 统一登录页（按路径区分前台/后台） -->
  <Login v-else-if="!authUser" :mode="isAdminRoute ? 'admin' : 'home'" @ok="onLogin" />

  <!-- 管理后台 /admin -->
  <AdminDashboard v-else-if="isAdminRoute" :user="authUser" @logout="onLogout" />

  <!-- 翻译工作台（登录后） -->
  <div v-else id="app-root" :class="isMobile ? 'device-mobile' : 'device-desktop'">
    <header class="app-header">
      <div class="header-left">
        <span class="header-icon">🌐</span>
        <span class="header-title">翻译助手</span>
        <span class="header-user">{{ authUser.display_name || authUser.username }}</span>
      </div>
      <div class="header-right">
        <span class="status-indicator" :class="store.isBackendOnline ? 'online' : 'offline'">
          <span class="status-dot"></span>
          {{ store.isBackendOnline ? '在线' : '离线' }}
        </span>
        <button class="gear-btn" @click="showSettings = true" title="设置">⚙️</button>
        <button class="gear-btn" @click="logout" title="退出登录">⎋</button>
      </div>
    </header>

    <main class="chat-main">
      <div v-if="store.isBackendLoading" class="loading-screen">
        <div class="loading-spinner"></div>
        <p class="loading-text">翻译引擎启动中…</p>
      </div>
      <ChatWindow v-else />
    </main>

    <!-- 设置弹窗 -->
    <Teleport to="body">
      <div v-if="showSettings" class="modal-overlay" @click.self="showSettings = false">
        <div class="settings-panel">
          <div class="settings-title">设置</div>
          <div class="settings-item">
            <label>翻译模型</label>
            <select v-model="currentModel" @change="onModelChange">
              <optgroup label="硅基流动 SiliconFlow">
                <option value="tencent/Hunyuan-MT-7B">Hunyuan-MT-7B (推荐，33语专用)</option>
                <option value="THUDM/GLM-Z1-9B-0414">GLM-Z1-9B-0414 (备用)</option>
                <option value="Qwen/Qwen2.5-7B-Instruct">Qwen2.5-7B</option>
              </optgroup>
              <optgroup label="智谱 Zhipu">
                <option value="glm-4.7-flash">GLM-4.7-Flash (快+强)</option>
                <option value="glm-4-flash">GLM-4-Flash (降级用)</option>
              </optgroup>
            </select>
          </div>
          <button class="settings-close" @click="showSettings = false">关闭</button>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useChatStore } from '@/stores/chat'
import ChatWindow from './components/ChatWindow.vue'
import Login from './components/Login.vue'
import AdminDashboard from './components/AdminDashboard.vue'
import { getAuthToken, setAuthToken, authMe, type AuthUser } from '@/api'

const store = useChatStore()
const showSettings = ref(false)
const currentModel = ref(store.selectedModel)

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

// 登录成功回调：写入当前用户
function onLogin(user: unknown) {
  authUser.value = user as AuthUser
}

// 退出登录：清空用户与 token
function onLogout() {
  authUser.value = null
  setAuthToken('')
}

function logout() {
  onLogout()
}

// 切换翻译模型
function onModelChange() {
  store.setSelectedModel(currentModel.value)
}

// 响应式：检测移动端布局
const windowWidth = ref(window.innerWidth)
const isMobile = ref(windowWidth.value <= 768)

function onResize() {
  windowWidth.value = window.innerWidth
  isMobile.value = windowWidth.value <= 768
}

// 初始化：恢复登录态 + 检测后端在线状态
onMounted(async () => {
  window.addEventListener('resize', onResize)
  await restoreSession()
  await store.checkBackendHealth()
})

onUnmounted(() => {
  window.removeEventListener('resize', onResize)
})
</script>

<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
html, body { height: 100%; overflow: hidden; }
body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'PingFang SC', 'Microsoft YaHei', sans-serif;
  background: #f5f5f5;
  color: #202124;
  -webkit-font-smoothing: antialiased;
}

#app-root { height: 100vh; height: 100dvh; display: flex; flex-direction: column; overflow: hidden; }

/* 会话恢复中 */
.restore-screen {
  height: 100vh; display: flex; align-items: center; justify-content: center; background: #f5f7fb;
}
.restore-spinner {
  width: 40px; height: 40px; border: 4px solid #e0e0e0; border-top-color: #1a237e; border-radius: 50%;
  animation: spin 0.8s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }

.app-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 12px 20px; background: #2e7d32; color: #fff; flex-shrink: 0;
}
.header-left { display: flex; align-items: center; gap: 10px; }
.header-icon { font-size: 24px; }
.header-title { font-size: 18px; font-weight: 600; }
.header-right { display: flex; align-items: center; gap: 12px; }

.gear-btn {
  border: none; background: rgba(255,255,255,0.15); color: #fff;
  font-size: 18px; cursor: pointer; padding: 6px 10px; border-radius: 8px;
  transition: background 0.2s;
}
.gear-btn:hover { background: rgba(255,255,255,0.25); }

.status-indicator { display: flex; align-items: center; gap: 6px; font-size: 12px; }
.status-dot { width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0; }
.status-indicator.online .status-dot { background: #69f0ae; box-shadow: 0 0 6px rgba(105,240,174,0.6); }
.status-indicator.offline .status-dot { background: #ff5252; box-shadow: 0 0 6px rgba(255,82,82,0.5); }

.chat-main { flex: 1; display: flex; flex-direction: column; background: #fff; overflow: hidden; min-width: 0; }

/* 设置弹窗 */
.modal-overlay {
  position: fixed; top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(0,0,0,0.4);
  display: flex; align-items: center; justify-content: center;
  z-index: 1000; animation: fadeIn 0.15s ease;
}
@keyframes fadeIn { from { opacity: 0; } to { opacity: 1; } }

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

/* 移动端适配 */
@media (max-width: 768px) {
  .app-header { padding: 10px 16px; }
  .header-title { font-size: 16px; }
  .device-mobile .chat-main { background: #fff; }
}

/* ==================== 加载屏 ==================== */
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
