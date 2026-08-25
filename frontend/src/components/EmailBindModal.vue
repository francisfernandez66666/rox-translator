<!-- ============================================================================
   components/EmailBindModal.vue — 登录后强制绑定邮箱（不可关闭）
   触发：meContext 返回 email 为空时由壳组件挂载；保存成功后 emit('done')
   用途：验证码收件地址（注册验证/找回密码/通知），未绑邮箱则关键自助流程不可用
   ============================================================================ -->
<template>
  <Teleport to="body">
    <div class="eb-mask">
      <div class="eb-modal">
        <h3>📧 {{ t('emailBind.title') }}</h3>
        <p class="eb-hint">{{ t('emailBind.reason') }}</p>
        <input v-model="email" :placeholder="t('login.emailPlaceholder')"
               class="fb-input" type="email" @keydown.enter="save" />
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
import { updateEmail } from '@/api'

const emit = defineEmits<{ done: [email: string] }>()
const email = ref('')
const msg = ref('')
const ok = ref(false)
const saving = ref(false)

const valid = computed(() => /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email.value.trim()))

// save 绑定邮箱：成功即视为已维护，交回壳组件刷新上下文
async function save() {
  if (!valid.value || saving.value) return
  saving.value = true
  try {
    const r = await updateEmail(email.value.trim())
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
.eb-save { width: 100%; margin-top: 14px; }
</style>
