<!-- ============================================================================
   components/Login.vue — 登录 / 自助注册组件
   职责：mode=home 前台登录，mode=admin 后台登录（需租户管理员及以上角色）
   - 前台支持"自助注册试用"：填邀请码加入已有团队，留空则新建租户
   - 登录成功通过 emit('ok', user) 通知父组件
   ============================================================================ -->
<template>
  <!-- ===== 登录面板容器 ===== -->
  <div class="login-wrap">
    <!-- 语言切换 -->
    <div class="login-lang"><button @click="toggleLang()">{{ lang === 'zh' ? 'English' : '中文' }}</button></div>

    <!-- ===== 登录卡片：前台/后台标题随模式变化 ===== -->
    <div class="login-card">
      <div class="login-logo">{{ mode === 'admin' ? t('login.platformAdmin') : t('login.platform') }}</div>
      <div class="login-sub">{{ mode === 'admin' ? t('login.adminOnly') : t('login.enterWorkspace') }}</div>
      <input v-model="username" :placeholder="t('login.username')" class="login-input" autocomplete="username" @keydown.enter="doLogin" />
      <input v-model="password" type="password" :placeholder="t('login.password')" class="login-input" autocomplete="current-password" @keydown.enter="doLogin" />
      <div v-if="error" class="login-error">{{ error }}</div>
      <button class="login-btn" :disabled="loading" @click="doLogin">{{ loading ? t('login.signingIn') : t('login.signIn') }}</button>
      <button class="login-reg" @click="showForgot = true">{{ t('login.forgot') }}</button>
      <button v-if="mode === 'home'" class="login-reg" @click="showReg = !showReg">{{ showReg ? t('login.backToLogin') : t('login.selfRegister') }}</button>
    </div>

    <!-- ===== 自助注册面板（仅前台 home 模式显示） ===== -->
    <div v-if="showReg && mode === 'home'" class="login-card">
      <div class="login-logo">{{ t('login.selfRegisterTitle') }}</div>
      <div class="login-sub">{{ t('login.selfRegisterSub') }}</div>
      <input v-model="reg.username" :placeholder="t('login.username')" class="login-input" />
      <input v-model="reg.password" type="password" :placeholder="t('login.password')" class="login-input" />
      <input v-model="reg.email" :placeholder="t('login.emailPlaceholder')" class="login-input" />
      <input v-model="reg.code" :placeholder="t('login.orgCode')" class="login-input" />
      <input v-model="reg.name" :placeholder="t('login.orgName')" class="login-input" />
      <input v-model="reg.invite" :placeholder="t('login.invite')" class="login-input" />
      <div v-if="regMsg" class="login-error">{{ regMsg }}</div>
      <button class="login-btn" :disabled="loading" @click="doRegister">{{ loading ? t('login.registering') : t('login.registerAndLogin') }}</button>
    </div>

    <!-- ===== 忘记密码面板 ===== -->
    <div v-if="showForgot" class="login-card">
      <div class="login-logo">{{ t('login.forgotTitle') }}</div>
      <div class="login-sub">{{ t('login.forgotSub') }}</div>
      <input v-model="forgot.username" :placeholder="t('login.username')" class="login-input" />
      <input v-model="forgot.email" :placeholder="t('login.boundEmail')" class="login-input" />
      <div v-if="forgotMsg" class="login-error">{{ forgotMsg }}</div>
      <button v-if="!forgotSent" class="login-btn" :disabled="loading" @click="doForgot">{{ loading ? t('login.signingIn') : t('login.sendCode') }}</button>
      <template v-else>
        <input v-model="forgot.code" :placeholder="t('login.verificationCode')" class="login-input" />
        <input v-model="forgot.newPassword" type="password" :placeholder="t('login.newPassword')" class="login-input" />
        <button class="login-btn" :disabled="loading" @click="doReset">{{ loading ? t('login.resetting') : t('login.resetPassword') }}</button>
      </template>
      <button class="login-reg" @click="closeForgot">{{ t('login.backToLogin') }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
// 登录组件：mode=home 前台登录，mode=admin 后台登录（需租户管理员及以上角色）
// Vue 响应式
import { ref } from 'vue'
// API：登录 / 自助注册 / 写入 token
import { login, authRegister, forgotPassword, resetPassword, setAuthToken } from '@/api'
// 国际化：文案取词 + 语言切换
import { t, lang, toggleLang } from '@/i18n'

// 组件入参：登录模式（home 前台 / admin 后台）
const props = defineProps<{ mode: 'home' | 'admin' }>()
// 组件事件：登录成功后向父组件返回用户对象
const emit = defineEmits<{ ok: [user: unknown] }>()

// ===== 登录表单状态 =====
const username = ref('')
// 登录密码
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
const reg = ref({ username: '', password: '', code: '', name: '', invite: '', email: '' })

// ===== 忘记密码表单状态 =====
const showForgot = ref(false)
// 忘记密码流程的错误/提示信息
const forgotMsg = ref('')
// 验证码是否已发送（控制切换到输入验证码+新密码面板）
const forgotSent = ref(false)
// 忘记密码表单：用户名/绑定邮箱/验证码/新密码
const forgot = ref({ username: '', email: '', code: '', newPassword: '' })

// 发送验证码（忘记密码）
async function doForgot() {
  if (!forgot.value.username && !forgot.value.email) {
    forgotMsg.value = '请输入用户名或绑定邮箱'
    return
  }
  loading.value = true
  forgotMsg.value = ''
  const resp = await forgotPassword({
    username: forgot.value.username || undefined,
    email: forgot.value.email || undefined,
  })
  loading.value = false
  if (!resp.success) { forgotMsg.value = resp.message || '发送失败'; return }
  forgotSent.value = true
  forgotMsg.value = '验证码已发送到绑定邮箱（未配置邮件时请在服务端日志查看）'
}

// 重置密码（校验验证码）
async function doReset() {
  if (!forgot.value.code || forgot.value.newPassword.length < 6) {
    forgotMsg.value = '请输入验证码和新密码（至少 6 位）'
    return
  }
  loading.value = true
  forgotMsg.value = ''
  const resp = await resetPassword({
    username: forgot.value.username || '',
    code: forgot.value.code,
    new_password: forgot.value.newPassword,
  })
  loading.value = false
  if (!resp.success) { forgotMsg.value = resp.message || '重置失败'; return }
  // 重置成功：回填登录表单并回到登录面板
  username.value = forgot.value.username || ''
  password.value = forgot.value.newPassword
  closeForgot()
  error.value = '密码已重置，请使用新密码登录'
}

// 关闭忘记密码面板并重置状态
function closeForgot() {
  showForgot.value = false
  forgotSent.value = false
  forgot.value = { username: '', email: '', code: '', newPassword: '' }
}

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
    email: reg.value.email || undefined,
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

// doLogin 执行登录：校验输入 → 调用登录接口 → 校验角色权限 → 写入 token 并通知父组件。
// 后台模式（admin）要求角色不低于租户管理员；无返回，通过 emit('ok') 通知父组件登录成功。
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
/* 语言切换按钮 */
.login-lang { position: fixed; top: 18px; right: 24px; }
.login-lang button {
  border: 1px solid rgba(255,255,255,.5); background: rgba(255,255,255,.12); color: #fff;
  padding: 6px 14px; border-radius: 8px; font-size: 13px; cursor: pointer;
}
.login-lang button:hover { background: rgba(255,255,255,.25); }
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