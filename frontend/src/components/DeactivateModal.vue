<!-- ============================================================================
   DeactivateModal.vue — 自助注销确认弹窗（2026-08-26 需求）
   语义：当日仍可用、次日 00:00 起无法登录；名下 API Key 立即停用；数据保留。
   撤回：联系管理员在成员管理中重新启用。
   规范：应用内遮罩弹窗（fb-mask/fb-modal 样式组件内自带，scoped 不跨组件共享）；
        禁用浏览器 confirm 于关键交互。:teleport="false" 时就地渲染（前台扩展环境兼容）。
============================================================================ -->
<template>
  <teleport to="body" :disabled="!teleport">
    <div class="fb-mask" @click.self="dismissible && $emit('close')">
      <div class="fb-modal">
        <h3 class="fb-title">🗑 {{ t('deact.title') }}</h3>
        <p class="fb-text">{{ t('deact.line1') }}</p>
        <ul class="fb-list">
          <li>{{ t('deact.point1') }}</li>
          <li>{{ t('deact.point2') }}</li>
          <li>{{ t('deact.point3') }}</li>
        </ul>
        <label class="fb-confirm-row">
          <input type="checkbox" v-model="acknowledged" />
          <span>{{ t('deact.ack') }}</span>
        </label>
        <p v-if="done" class="fb-done">{{ t('deact.done') }}</p>
        <div class="fb-actions">
          <button class="fb-btn" :disabled="busy || done" @click="$emit('close')">{{ t('common.cancel') }}</button>
          <button class="fb-btn fb-btn-danger" :disabled="!acknowledged || busy || done" @click="submit">
            {{ busy ? t('deact.processing') : t('deact.confirm') }}
          </button>
        </div>
      </div>
    </div>
  </teleport>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { deactivateAccount } from '@/api'
import { t } from '@/i18n'

const props = defineProps<{ teleport?: boolean; dismissible?: boolean }>()
const emit = defineEmits<{ close: []; done: [] }>()

const acknowledged = ref(false)
const busy = ref(false)
const done = ref(false)

// submit 提交注销：成功后展示完成态并由父组件在关闭时登出
async function submit() {
  busy.value = true
  try {
    const r = await deactivateAccount()
    if (!r.success) return
    done.value = true
    emit('done')
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.fb-mask { position: fixed; inset: 0; background: rgba(0,0,0,.45); display: flex; align-items: center; justify-content: center; z-index: 3000; animation: fadeIn .15s ease; }
@keyframes fadeIn { from { opacity: 0 } to { opacity: 1 } }
.fb-modal { width: 400px; max-width: calc(100vw - 32px); background: #fff; border-radius: 14px; padding: 22px; box-shadow: 0 10px 36px rgba(0,0,0,.22); }
.fb-title { font-size: 16px; margin-bottom: 12px; color: #c62828; }
.fb-text { font-size: 13.5px; color: #333; line-height: 1.6; margin-bottom: 8px; }
.fb-list { margin: 0 0 10px 18px; font-size: 13px; color: #555; line-height: 1.8; }
.fb-confirm-row { display: flex; gap: 8px; align-items: flex-start; font-size: 13px; color: #333; margin: 6px 0 4px; }
.fb-done { margin-top: 8px; font-size: 13px; color: #2e7d32; }
.fb-actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 14px; }
.fb-btn { border: none; border-radius: 9px; padding: 9px 18px; font-size: 13.5px; cursor: pointer; background: #f0f2f5; color: #333; }
.fb-btn:hover { background: #e6e9ee; }
.fb-btn-danger { background: #c62828; color: #fff; }
.fb-btn-danger:hover { background: #ad1f1f; }
.fb-btn:disabled { opacity: .5; cursor: not-allowed; }
</style>
