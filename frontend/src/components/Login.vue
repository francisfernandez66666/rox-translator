<!-- ============================================================================
   components/Login.vue — 登录 / 自助注册组件
   职责：mode=home 前台登录，mode=admin 后台登录（需租户管理员及以上角色）
   - 前台支持"自助注册试用"：填邀请码加入已有团队，留空则新建租户
   - 登录成功通过 emit('ok', user) 通知父组件
   ============================================================================ -->
<template>
  <!-- ===== 登录面板容器 ===== -->
  <div class="login-wrap">
    <!-- ===== 登录卡片：前台/后台标题随模式变化 ===== -->
    <div class="login-card">
      <div class="login-logo">{{ mode === 'admin' ? '🏢 翻译平台管理后台' : '🌐 翻译平台' }}</div>
      <div class="login-sub">{{ mode === 'admin' ? '仅限管理员账号登录' : '登录后进入翻译工作台' }}</div>
      <input v-model="username" placeholder="用户名" class="login-input" autocomplete="username" @keydown.enter="doLogin" />
      <input v-model="password" type="password" placeholder="密码" class="login-input" autocomplete="current-password" @keydown.enter="doLogin" />
      <div v-if="error" class="login-error">{{ error }}</div>
      <button class="login-btn" :disabled="loading" @click="doLogin">{{ loading ? '登录中…' : '登 录' }}</button>
      <button v-if="mode === 'home'" class="login-reg" @click="showReg = !showReg">{{ showReg ? '返回登录' : '没有账号？自助注册试用' }}</button>
    </div>

    <!-- ===== 自助注册面板（仅前台 home 模式显示） ===== -->
    <div v-if="showReg && mode === 'home'" class="login-card">
      <div class="login-logo">🌐 自助注册试用</div>
      <div class="login-sub">填写邀请码可加入已有团队；留空则创建新租户并获得试用额度</div>
      <input v-model="reg.username" placeholder="用户名" class="login-input" />
      <input v-model="reg.password" type="password" placeholder="密码（至少 6 位）" class="login-input" />
      <input v-model="reg.code" placeholder="租户编码（新建租户时必填）" class="login-input" />
      <input v-model="reg.name" placeholder="租户名称（新建租户时必填）" class="login-input" />
      <input v-model="reg.invite" placeholder="邀请码（可选）" class="login-input" />
      <div v-if="regMsg" class="login-error">{{ regMsg }}</div>
      <button class="login-btn" :disabled="loading" @click="doRegister">{{ loading ? '注册中…' : '注册并登录' }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
// 登录组件：mode=home 前台登录，mode=admin 后台登录（需租户管理员及以上角色）
// Vue 响应式
import { ref } from 'vue'
// API：登录 / 自助注册 / 写入 token
import { login, authRegister, setAuthToken } from '@/api'

// 组件入参：登录模式（home 前台 / admin 后台）
const props = defineProps<{ mode: 'home' | 'admin' }>()
// 组件事件：登录成功后向父组件返回用户对象
const emit = defineEmits<{ ok: [user: unknown] }>()

// ===== 登录表单状态 =====
const username = ref('')
const password = ref('')
// 登录错误提示
const error = ref('')
// 请求进行中（禁用按钮）
const loading = ref(false)
// 是否显示自助注册面板
const showReg = ref(false)
// 注册错误/提示信息
const regMsg = ref('')
// 自助注册表单
const reg = ref({ username: '', password: '', code: '', name: '', invite: '' })

// 注册成功后自动登录进入工作台
async function doRegister() {
  if (!reg.value.username || !reg.value.password) {
    regMsg.value = '请输入用户名和密码'
    return
  }
  if (reg.value.password.length < 6) {
    regMsg.value = '密码至少 6 位'
    return
  }
  loading.value = true
  regMsg.value = ''
  const resp = await authRegister({
    username: reg.value.username,
    password: reg.value.password,
    code: reg.value.code || undefined,
    name: reg.value.name || undefined,
    invite: reg.value.invite || undefined,
  })
  loading.value = false
  if (!resp.success) {
    regMsg.value = resp.message || '注册失败'
    return
  }
  username.value = reg.value.username
  password.value = reg.value.password
  showReg.value = false
  await doLogin()
}

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
/* ===== 登录页容器：渐变背景 + 垂直水平居中 ===== */
.login-wrap {
  min-height: 100vh; display: flex; align-items: center; justify-content: center;
  background: linear-gradient(135deg, #1a237e 0%, #3949ab 50%, #2e7d32 100%);
}
/* 登录卡片主体 */
.login-card {
  background: #fff; border-radius: 18px; padding: 40px 36px; width: 380px;
  box-shadow: 0 12px 40px rgba(0,0,0,0.25);
}
.login-logo { font-size: 22px; font-weight: 700; color: #1a237e; margin-bottom: 6px; }
.login-sub { font-size: 13px; color: #888; margin-bottom: 22px; }
/* 登录输入框 */
.login-input {
  width: 100%; padding: 12px 14px; border: 1px solid #e0e0e0; border-radius: 10px;
  font-size: 14px; margin-bottom: 12px; box-sizing: border-box;
}
.login-input:focus { outline: none; border-color: #1a237e; }
/* 登录/注册主按钮 */
.login-btn {
  width: 100%; padding: 12px; border: none; border-radius: 10px;
  background: #1a237e; color: #fff; font-size: 15px; cursor: pointer; margin-top: 6px;
}
.login-btn:disabled { opacity: 0.6; }
/* 切换注册面板按钮 */
.login-reg {
  width: 100%; padding: 10px; border: none; border-radius: 10px; background: #f5f5f5;
  color: #3949ab; font-size: 13px; cursor: pointer; margin-top: 10px;
}
.login-reg:hover { background: #e8eaf6; }
/* 错误提示文字 */
.login-error { color: #c62828; font-size: 13px; margin: 8px 0; }
</style>