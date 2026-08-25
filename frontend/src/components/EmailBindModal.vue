<!-- ============================================================================
   components/EmailBindModal.vue — 登录后强制绑定邮箱（不可关闭）
   触发：meContext 返回 email 为空时由壳组件挂载；保存成功后 emit('done')
   用途：验证码收件地址（注册验证/找回密码/通知），未绑邮箱则关键自助流程不可用
   ============================================================================ -->
<template>
  <Teleport to="body" :disabled="!teleport">
    <div class="eb-mask" style="position:fixed;top:0;left:0;width:100vw;height:100vh;display:flex;align-items:center;justify-content:center;background:rgba(0,0,0,.55);z-index:2147483000">
      <div class="eb-modal">
        <h3>
          {{ props.dismissible ? t('emailBind.changeTitle') : t('emailBind.title') }}
          <button v-if="props.dismissible" class="eb-close" @click="emit('close')">✕</button>
        </h3>
        <p class="eb-hint">{{ t('emailBind.reason') }}</p>
        <!-- 原邮箱验证（已绑定过才显示）：防单点劫持 -->
        <template v-if="props.currentEmail">
          <div class="eb-old-row">
            <span class="eb-old-label">{{ t('emailBind.oldEmail') }}</span>
            <span class="eb-old-addr">{{ props.currentEmail }}</span>
          </div>
          <div class="eb-code-row">
            <input v-model="oldCode" :placeholder="t('emailBind.oldCodePlaceholder')" class="fb-input eb-code-input" />
            <button class="ad-btn" :disabled="oldCooldown > 0" @click="sendOldCode">
              {{ oldCooldown > 0 ? tpl('login.codeResend', { n: oldCooldown }) : t('login.sendCode') }}
            </button>
          </div>
        </template>
        <!-- 新邮箱 + 验证码 -->
        <input v-model="email" :placeholder="t('login.emailPlaceholder')"
               class="fb-input" type="email" @keydown.enter="save" />
        <div class="eb-code-row">
          <input v-model="code" :placeholder="t('login.verificationCode')" class="fb-input eb-code-input" @keydown.enter="save" />
          <button class="ad-btn" :disabled="newCooldown > 0 || !valid || sendingCode" @click="sendNewCode">
            {{ newCooldown > 0 ? tpl('login.codeResend', { n: newCooldown }) : t('login.sendCode') }}
          </button>
        </div>
        <p v-if="msg" :class="ok ? 'eb-ok' : 'eb-err'">{{ msg }}</p>
        <button class="ad-btn ad-btn-green eb-save" :disabled="!valid || saving" @click="save">
          {{ saving ? t('common.save') + '…' : t('emailBind.save') }}
        </button>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, onUnmounted } from 'vue'
import { t, tpl } from '@/i18n'
import { updateEmail, meEmailCode } from '@/api'

const props = defineProps<{ teleport?: boolean;  dismissible?: boolean; currentEmail?: string }>()
const emit = defineEmits<{ done: [email: string]; close: [] }>()
const email = ref(props.currentEmail || '')
const msg = ref('')
const ok = ref(false)
const code = ref('')
const oldCode = ref('')
const sendingOld = ref(false)
let oldTimer: ReturnType<typeof setInterval> | undefined
const oldCooldown = ref(0)
const newCooldown = ref(0)
const sendingCode = ref(false)
const cooldown = ref(0)
let timer: ReturnType<typeof setInterval> | undefined
const saving = ref(false)

const valid = computed(() => /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email.value.trim()))

// startCd 通用冷却启动
function startCd(refv: { value: number }, t: ReturnType<typeof setInterval> | undefined, setter: (t2: any) => void) {
  refv.value = 60
  const iv = setInterval(() => { refv.value--; if (refv.value <= 0) clearInterval(iv) }, 1000)
  setter(iv)
}
// sendNewCode 向新邮箱发码
async function sendNewCode() {
  if (!valid.value || newCooldown.value > 0 || sendingCode.value) return
  sendingCode.value = true
  try {
    const r = await meEmailCode(email.value.trim())
    if (!r.success) { msg.value = r.message || 'failed'; ok.value = false; return }
    startCd(newCooldown, timer, (t2) => { timer = t2 })
    msg.value = r.message || ''
  } finally { sendingCode.value = false }
}
// sendOldCode 向原绑定邮箱发码（证明旧邮箱支配权）
async function sendOldCode() {
  if (oldCooldown.value > 0 || sendingOld.value || !props.currentEmail) return
  sendingOld.value = true
  try {
    const r = await meEmailCode(props.currentEmail)
    if (!r.success) { msg.value = r.message || 'failed'; ok.value = false; return }
    startCd(oldCooldown, undefined as any, () => {})
    oldCooldown.value = 60
    msg.value = r.message || ''
  } finally { sendingOld.value = false }
}
onUnmounted(() => { if (timer) clearInterval(timer); if (typeof oldTimer !== 'undefined' && oldTimer) clearInterval(oldTimer) })
onUnmounted(() => { if (timer) clearInterval(timer) })

// save 绑定邮箱：成功即视为已维护，交回壳组件刷新上下文
async function save() {
  if (!valid.value || saving.value) return
  saving.value = true
  try {
    const r = await updateEmail(email.value.trim(), code.value.trim(), oldCode.value.trim())
    if (!r.success) { ok.value = false; msg.value = r.message || 'failed'; return }
    ok.value = true
    emit('done', email.value.trim())
  } catch (e) {
    ok.value = false
    msg.value = e instanceof Error ? e.message : String(e)
  } finally { saving.value = false }
}
</script>

<style scoped>
.eb-mask { position: fixed; inset: 0; background: rgba(0,0,0,.55); display: flex; align-items: center; justify-content: center; z-index: 3000; }
.eb-modal { background: #fff; border-radius: 12px; padding: 22px 24px; width: min(420px, 92vw); box-shadow: 0 10px 40px rgba(0,0,0,.25); }
.eb-hint { font-size: 12.5px; color: #888; margin: 8px 0 12px; }
.fb-input { width: 100%; box-sizing: border-box; border: 1px solid #d0d7de; border-radius: 8px; padding: 9px; font-size: 13px; }
.eb-ok { color: #2e7d32; font-size: 12.5px; margin-top: 6px; }
.eb-err { color: #c5221f; font-size: 12.5px; margin-top: 6px; }
.eb-code-row { display: flex; gap: 8px; margin-top: 8px; }
.eb-code-input { flex: 1; }
.eb-save { width: 100%; margin-top: 14px; }
.eb-close { float: right; background: none; border: none; cursor: pointer; font-size: 15px; color: #999; }
h3 { display:flex; align-items:center; justify-content:space-between; }
</style>
