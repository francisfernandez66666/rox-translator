<!-- ============================================================================
   components/PasswordModal.vue — 自助修改密码（邮箱校验流程）
   职责：向绑定邮箱发送验证码 → 校验验证码并设置新密码（复用找回密码通道）
   入参：username（当前登录用户名）、email（已绑定邮箱，用于预填与发送）
   ============================================================================ -->

<template>
  <Teleport to="body">
  <div class="fb-mask" @click.self="$emit('close')">
    <div class="fb-modal">
      <h3>🔒 {{ t('pwd.title') }}</h3>
      <p class="fb-hint">{{ tpl('pwd.hint', { user: username }) }}</p>

      <input v-model="email" :placeholder="t('login.boundEmail')" class="fb-input" />
      <div class="pwd-code-row">
        <input v-model="code" :placeholder="t('login.verificationCode')" class="fb-input pwd-code-input" />
        <button class="ad-btn" :disabled="cooldown > 0 || !canSend" @click="sendCode">
          {{ cooldown > 0 ? tpl('login.codeResend', { n: cooldown }) : t('login.sendCode') }}
        </button>
      </div>
      <input v-model="newPwd" type="password" :placeholder="t('login.newPassword')" class="fb-input" />
      <input v-model="confirmPwd" type="password" :placeholder="t('pwd.confirmPlaceholder')" class="fb-input" />

      <div v-if="msg" :class="[msgOk ? 'login-ok-hint' : 'login-error', 'fb-msg']">{{ msg }}</div>
      <div class="fb-actions">
        <button class="ad-btn" @click="$emit('close')">{{ t('common.cancel') }}</button>
        <button class="ad-btn ad-btn-green" :disabled="submitting || !code || !newPwd" @click="submit">
          {{ submitting ? t('pwd.submitting') : t('pwd.submit') }}
        </button>
      </div>
    </div>
  </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, onUnmounted } from 'vue'
import { t, tpl } from '@/i18n'
import { sendPwdCode, submitNewPassword } from '@/api'

const props = defineProps<{ username: string; email?: string }>()
const emit = defineEmits<{ close: []; done: [] }>()

// 表单状态
const email = ref(props.email || '')
const code = ref('')
const newPwd = ref('')
const confirmPwd = ref('')
const msg = ref('')
const msgOk = ref(false)
const submitting = ref(false)
const cooldown = ref(0)
let timer: ReturnType<typeof setInterval> | undefined

// canSend 需要用户名或邮箱任一可定位账号
const canSend = computed(() => !!props.username || !!email.value.trim())

onUnmounted(() => { if (timer) clearInterval(timer) })

// startCooldown 发码冷却倒计时（60s）
function startCooldown() {
  cooldown.value = 60
  timer = setInterval(() => {
    cooldown.value--
    if (cooldown.value <= 0 && timer) clearInterval(timer)
  }, 1000)
}

// sendCode 发送改密验证码到绑定邮箱（防枚举：服务端统一成功文案）
async function sendCode() {
  try {
    const r = await sendPwdCode({ username: props.username, email: email.value.trim() })
    msgOk.value = true
    msg.value = r.message || t('pwd.codeSent')
    startCooldown()
  } catch (e) {
    msgOk.value = false
    msg.value = e instanceof Error ? e.message : String(e)
  }
}

// submit 提交验证码与新密码
async function submit() {
  if (newPwd.value.length < 6) {
    msgOk.value = false; msg.value = t('pwd.tooShort'); return
  }
  if (newPwd.value !== confirmPwd.value) {
    msgOk.value = false; msg.value = t('pwd.mismatch'); return
  }
  submitting.value = true
  try {
    const r = await submitNewPassword({ username: props.username, code: code.value.trim(), new_password: newPwd.value })
    if (!r.success) {
      msgOk.value = false; msg.value = r.message || t('pwd.codeBad'); return
    }
    msgOk.value = true
    alert(t('pwd.done'))
    emit('done'); emit('close')
  } catch (e) {
    msgOk.value = false
    msg.value = e instanceof Error ? e.message : String(e)
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.pwd-code-row { display: flex; gap: 8px; margin-bottom: 8px; }
.pwd-code-input { flex: 1; }
.fb-input { width: 100%; box-sizing: border-box; border: 1px solid #d0d7de; border-radius: 8px; padding: 8px; font-size: 13px; margin-bottom: 8px; }
.fb-msg { margin-top: 6px; font-size: 12.5px; }

/* ★ 弹窗骨架样式（scoped 隔离，需在本组件内完整定义） */
.fb-mask { position: fixed; inset: 0; background: rgba(0,0,0,.45); display: flex; align-items: center; justify-content: center; z-index: 999; }
.fb-modal { background: #fff; border-radius: 12px; padding: 18px 20px; width: min(440px, 92vw); box-shadow: 0 8px 32px rgba(0,0,0,.2); }
.fb-hint { margin: 0 0 10px; font-size: 12px; color: #888; }
.fb-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 12px; }
</style>
