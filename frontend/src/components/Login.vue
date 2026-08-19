<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="login-logo">{{ mode === 'admin' ? '🏢 翻译平台管理后台' : '🌐 翻译平台' }}</div>
      <div class="login-sub">{{ mode === 'admin' ? '仅限管理员账号登录' : '登录后进入翻译工作台' }}</div>
      <input v-model="username" placeholder="用户名" class="login-input" autocomplete="username" @keydown.enter="doLogin" />
      <input v-model="password" type="password" placeholder="密码" class="login-input" autocomplete="current-password" @keydown.enter="doLogin" />
      <div v-if="error" class="login-error">{{ error }}</div>
      <button class="login-btn" :disabled="loading" @click="doLogin">{{ loading ? '登录中…' : '登 录' }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
// 登录组件：mode=home 前台登录，mode=admin 后台登录（需租户管理员及以上角色）
import { ref } from 'vue'
import { login, setAuthToken } from '@/api'

const props = defineProps<{ mode: 'home' | 'admin' }>()
const emit = defineEmits<{ ok: [user: unknown] }>()

// 表单状态
const username = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

// 角色等级：普通用户(1) < 租户管理员(2) < 超级管理员(3)，兼容旧值 approver/admin
function roleLevel(r?: string): number {
  if (r === 'super_admin' || r === 'admin') return 3
  if (r === 'tenant_admin' || r === 'approver') return 2
  return 1
}

async function doLogin() {
  if (!username.value || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  loading.value = true
  error.value = ''
  const resp = await login(username.value, password.value)
  loading.value = false
  if (!resp.success || !resp.token) {
    error.value = resp.message || '登录失败'
    return
  }
  // 后台入口：非租户管理员及以上角色拒绝登录
  if (props.mode === 'admin' && roleLevel(resp.user?.role) < 2) {
    error.value = '该账号无管理权限'
    return
  }
  setAuthToken(resp.token)
  emit('ok', resp.user)
}
</script>

<style scoped>
.login-wrap {
  min-height: 100vh; display: flex; align-items: center; justify-content: center;
  background: linear-gradient(135deg, #1a237e 0%, #3949ab 50%, #2e7d32 100%);
}
.login-card {
  background: #fff; border-radius: 18px; padding: 40px 36px; width: 380px;
  box-shadow: 0 12px 40px rgba(0,0,0,0.25);
}
.login-logo { font-size: 22px; font-weight: 700; color: #1a237e; margin-bottom: 6px; }
.login-sub { font-size: 13px; color: #888; margin-bottom: 22px; }
.login-input {
  width: 100%; padding: 12px 14px; border: 1px solid #e0e0e0; border-radius: 10px;
  font-size: 14px; margin-bottom: 12px; box-sizing: border-box;
}
.login-input:focus { outline: none; border-color: #1a237e; }
.login-btn {
  width: 100%; padding: 12px; border: none; border-radius: 10px;
  background: #1a237e; color: #fff; font-size: 15px; cursor: pointer; margin-top: 6px;
}
.login-btn:disabled { opacity: 0.6; }
.login-error { color: #c62828; font-size: 13px; margin: 8px 0; }
</style>