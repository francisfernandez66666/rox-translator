<!-- ============================================================================
   components/EmailBindModal.vue — 登录后强制绑定邮箱（不可关闭）
   触发：meContext 返回 email 为空时由壳组件挂载；保存成功后 emit('done')
   用途：验证码收件地址（注册验证/找回密码/通知），未绑邮箱则关键自助流程不可用
   ============================================================================ -->
<template>
  <Teleport to="body">
    <div class="eb-mask">
      <div class="eb-modal">
        <h3>
          {{ props.dismissible ? t('emailBind.changeTitle') : t('emailBind.title') }}
          <button v-if="props.dismissible" class="eb-close" @click="emit('close')">✕</button>
        </h3>
        <p class="eb-hint">{{ t('emailBind.reason') }}</p>
        <input v-model="email" :placeholder="t('login.emailPlaceholder')"
               class="fb-input" type="email" @keydown.enter="save" />
        <div class="eb-code-row">
          <input v-model="code" :placeholder="t('login.verificationCode')" class="fb-input eb-code-input" @keydown.enter="save" />
          <button class="ad-btn" :disabled="cooldown > 0 || !valid || sendingCode" @click="sendCode">
            {{ cooldown > 0 ? tpl('login.codeResend', { n: cooldown }) : t('login.sendCode') }}
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
import { ref, computed } from 'vue'
import { t } from '@/i18n'
import { updateEmail, meEmailCode } from '@/api'

const props = defineProps<{ dismissible?: boolean; currentEmail?: string }>()
import { onUnmounted } from 'vue'
import { t as _t, tpl as _tpl } from '@/i18n'
const t = _t; const tpl = _tpl
const emit = defineEmits<{ done: [email: string]; close: [] }>()
const email = ref(props.currentEmail || '')
const msg = ref('')
const ok = ref(false)
const code = ref('')
const sendingCode = ref(false)
const cooldown = ref(0)
let timer: ReturnType<typeof setInterval> | undefined
const saving = ref(false)

const valid = computed(() => /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email.value.trim()))

// sendCode 向新邮箱发送变更验证码（60s 冷却）
async function sendCode() {
  if (!valid.value || cooldown.value > 0 || sendingCode.value) return
  sendingCode.value = true
  try {
    const r = await meEmailCode(email.value.trim())
    if (!r.success) { msg.value = r.message || 'failed'; ok.value = false; return }
    cooldown.value = 60
    timer = setInterval(() => { cooldown.value--; if (cooldown.value <= 0 && timer) clearInterval(timer) }, 1000)
    msg.value = r.message || ''
  } finally { sendingCode.value = false }
}
onUnmounted(() => { if (timer) clearInterval(timer) })

// save 绑定邮箱：成功即视为已维护，交回壳组件刷新上下文
async function save() {
  if (!valid.value || saving.value) return
  saving.value = true
  try {
    const r = await updateEmail(email.value.trim(), code.value.trim())
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
.eb-mask { position: fixed; inset: 0; background: rgba(0,0,0,.55); display: flex; align-items: center; justify-content: center; z-index: 1200; }
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
