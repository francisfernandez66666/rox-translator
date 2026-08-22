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
      <!-- 邮箱验证码（服务端 email_verify_enabled=1 时显示） -->
      <div v-if="emailVerifyOn" class="login-code-row">
        <input v-model="reg.emailCode" :placeholder="t('login.emailCode')" class="login-input login-code-input" />
        <button class="login-reg login-code-btn" :disabled="codeCooldown > 0 || !reg.email" @click="doSendCode">
          {{ codeCooldown > 0 ? tpl('login.codeResend', { n: codeCooldown }) : t('login.sendCode') }}
        </button>
      </div>
      <div v-if="codeMsg" :class="codeMsgOk ? 'login-ok-hint' : 'login-error'">{{ codeMsg }}</div>
      <input v-model="reg.code" :placeholder="t('login.orgCode')" class="login-input" />
      <input v-model="reg.name" :placeholder="t('login.orgName')" class="login-input" />
      <select v-model="reg.industry" class="login-input">
        <option value="">{{ t('login.selectIndustry') }}</option>
        <option v-for="ind in industries" :key="ind.code" :value="ind.code">{{ ind.name }}</option>
      </select>
      <input v-model="reg.invite" :placeholder="t('login.invite')" class="login-input" />
      <!-- 人机验证组件（服务端 captcha_provider=turnstile 时显示） -->
      <div v-if="captchaOn" :id="tsWidgetId" class="ts-box"></div>
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
import { ref, onMounted } from 'vue'
// API：登录 / 自助注册 / 写入 token
import { login, authRegister, forgotPassword, resetPassword, setAuthToken, setActiveTenantId, registerIndustries, sendEmailCode, registerConfig } from '@/api'
// 国际化：文案取词 + 语言切换
import { t, tpl, lang, toggleLang } from '@/i18n'

// 组件入参：登录模式（home 前台 / admin 后台）
const props = defineProps<{ mode: 'home' | 'admin' }>()
// 组件事件：登录成功后向父组件返回用户对象
const emit = defineEmits<{ ok: [user: unknown] }>()

// 注册行业列表（来自超管维护的行业包）
const industries = ref<{ code: string; name: string }[]>([])
// 加载注册行业列表（前台注册面板展示）
async function loadIndustries() {
  try {
    const r = await registerIndustries()
    if (r.success) industries.value = (r as any).industries || []
  } catch { industries.value = [] }
}

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
const reg = ref({ username: '', password: '', code: '', name: '', invite: '', email: '', emailCode: '', industry: '' })

// ===== 注册邮箱验证码状态 =====
// 服务端是否开启邮箱验证（email_verify_enabled=1 时显示验证码输入行）
const emailVerifyOn = ref(false)
// 验证码发送冷却倒计时（秒）
const codeCooldown = ref(0)
// 发码提示与成功标记
const codeMsg = ref('')
const codeMsgOk = ref(false)
let cooldownTimer: ReturnType<typeof setInterval> | null = null

// 加载注册配置：显隐验证码输入 + 人机验证组件
const captchaOn = ref(false)
const captchaSiteKey = ref('')
const captchaToken = ref('')
const tsWidgetId = 'ts-widget-' + Math.random().toString(36).slice(2, 8)
// loadRegisterConfig 拉取注册配置：邮箱验证码显隐与人机验证开关/站点Key，开启人机验证时渲染 Turnstile
async function loadRegisterConfig() {
  try {
    const r: any = await registerConfig()
    if (r.success) {
      emailVerifyOn.value = !!r.email_verify_enabled
      captchaOn.value = !!(r as any).captcha_enabled
      captchaSiteKey.value = (r as any).captcha_site_key || ''
    }
  } catch { emailVerifyOn.value = false }
  if (captchaOn.value) renderTurnstile()
}

// renderTurnstile 动态加载 Cloudflare Turnstile 脚本并渲染组件（token 回调写入 captchaToken）
function renderTurnstile() {
  const mount = () => {
    const el = document.getElementById(tsWidgetId)
    const ts = (window as any).turnstile
    if (!el || !ts) return
    if (el.childElementCount > 0) return // 已渲染防重复
    ts.render(el, {
      sitekey: captchaSiteKey.value,
      callback: (tk: string) => { captchaToken.value = tk },
      'expired-callback': () => { captchaToken.value = '' },
    })
  }
  if ((window as any).turnstile) { mount(); return }
  const s = document.createElement('script')
  s.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'
  s.async = true
  s.onload = mount
  document.head.appendChild(s)
}

// doSendCode 发送注册验证码：校验邮箱 → 调接口 → 60s 冷却倒计时
async function doSendCode() {
  const email = reg.value.email.trim()
  if (!email) return
  if (captchaOn.value && !captchaToken.value) {
    codeMsg.value = '请先完成人机验证'; codeMsgOk.value = false
    return
  }
  codeMsg.value = ''
  loading.value = true
  const r: any = await sendEmailCode(email, captchaToken.value || undefined)
  loading.value = false
  codeMsgOk.value = !!r.success
  codeMsg.value = r.message || (r.success ? '已发送' : '发送失败')
  // 测试模式提示（服务端未配置 SMTP，验证码打印在日志）
  if (r.success && r.noop) codeMsg.value += '（测试模式：请查看服务端日志）'
  if (r.success) {
    codeCooldown.value = 60
    if (cooldownTimer) clearInterval(cooldownTimer)
    cooldownTimer = setInterval(() => {
      codeCooldown.value--
      if (codeCooldown.value <= 0 && cooldownTimer) { clearInterval(cooldownTimer); cooldownTimer = null }
    }, 1000)
  }
}

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
  // 邮箱验证开启时：校验验证码已填写
  if (emailVerifyOn.value && !reg.value.invite && (!reg.value.emailCode || !reg.value.email)) {
    regMsg.value = '请填写邮箱并输入验证码'
    return
  }
  // 人机验证开启时：必须先完成验证
  if (captchaOn.value && !captchaToken.value) {
    regMsg.value = '请先完成人机验证'
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
    email_code: reg.value.emailCode || undefined,
    captcha_token: captchaToken.value || undefined,
    industry: reg.value.industry || undefined,
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
  // 登录成功重置租户上下文：超管默认平台（0=翻译助手根组织），普通用户由后端按 JWT 归属
  setActiveTenantId(0)
  emit('ok', resp.user)
}

// 挂载：预加载注册行业列表与注册配置（前台）
onMounted(() => { loadIndustries(); loadRegisterConfig() })
</script>

<style scoped>
/* ===== 登录页容器：渐变背景 + 垂直水平居中 ===== */
.login-wrap {
  min-height: 100vh; display: flex; align-items: center; justify-content: center;
  background: linear-gradient(135deg, #1a237e 0%, #3949ab 50%, #1557b0 100%);
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
/* 成功提示文字（验证码已发送） */
.login-ok-hint { color: #2e7d32; font-size: 13px; margin: 8px 0; }
/* 验证码行：输入框 + 发码按钮横排 */
.login-code-row { display: flex; gap: 8px; align-items: stretch; margin-bottom: 12px; }
.login-code-input { flex: 1; margin-bottom: 0; min-width: 0; }
.login-code-btn {
  width: 110px; padding: 0 10px; border: none; border-radius: 10px;
  background: #e8eaf6; color: #3949ab; font-size: 13px; cursor: pointer; margin-top: 0;
}
.login-code-btn:disabled { opacity: 0.55; cursor: not-allowed; }
/* Turnstile 人机验证容器（居中） */
.ts-box { margin-bottom: 12px; display: flex; justify-content: center; }
</style>