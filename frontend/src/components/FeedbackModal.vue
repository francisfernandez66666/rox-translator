<!-- ============================================================================
   components/FeedbackModal.vue — 用户反馈弹窗（翻译结果 → 超管）
   职责：反馈意见输入 + 勾选是否附带翻译上下文（源文/译文），提交到 /api/feedback
   入参 target：{ type:'text'|'ticket', ticket_id?, source_text?, translations?, mode? }
   ============================================================================ -->

<template>
  <div class="fb-mask" @click.self="$emit('close')">
    <div class="fb-modal">
      <h3>💬 {{ t('fb.title') }}</h3>
      <p class="fb-hint">{{ t('fb.hint') }}</p>
      <textarea
        v-model="content"
        rows="4"
        maxlength="1000"
        :placeholder="t('fb.placeholder')"
        class="fb-textarea"
      ></textarea>
      <!-- 上下文勾选（存在可附加上下文时显示） -->
      <label v-if="hasContext" class="fb-check">
        <input type="checkbox" v-model="withContext" />
        {{ t('fb.withContext') }}
        <span class="fb-ctx-preview" v-if="withContext">（{{ ctxPreview }}）</span>
      </label>
      <div class="fb-actions">
        <button class="ad-btn" @click="$emit('close')">{{ t('common.cancel') }}</button>
        <button class="ad-btn ad-btn-green" :disabled="!content.trim() || submitting" @click="submit">
          {{ submitting ? t('fb.submitting') : t('fb.submit') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { t } from '@/i18n'
import { createFeedback } from '@/api'

// 反馈目标：文本气泡或已完成工单
const props = defineProps<{
  target: {
    type: 'text' | 'ticket'
    ticket_id?: number
    source_text?: string
    translations?: Record<string, string>
    mode?: string
  }
}>()

const emit = defineEmits<{ close: []; submitted: [] }>()

// 表单状态
const content = ref('')
const withContext = ref(true)
const submitting = ref(false)

// 是否存在可附带的上下文（源文或译文任一非空）
const hasContext = computed(() => !!(props.target.source_text || (props.target.translations && Object.keys(props.target.translations).length)))
// 上下文预览（截断）
const ctxPreview = computed(() => {
  const src = props.target.source_text || ''
  return src.length > 40 ? src.slice(0, 40) + '…' : src
})

// submit 提交反馈；成功后通知父组件关闭
async function submit() {
  submitting.value = true
  try {
    await createFeedback({
      target_type: props.target.type,
      ticket_id: props.target.ticket_id,
      content: content.value.trim(),
      with_context: withContext.value,
      source_text: props.target.type === 'text' ? props.target.source_text : undefined,
      translations: props.target.type === 'text' ? props.target.translations : undefined,
      mode: props.target.mode,
    })
    emit('submitted')
    emit('close')
  } catch (e) {
    alert(e instanceof Error ? e.message : String(e))
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.fb-mask { position: fixed; inset: 0; background: rgba(0,0,0,.45); display: flex; align-items: center; justify-content: center; z-index: 999; }
.fb-modal { background: #fff; border-radius: 12px; padding: 18px 20px; width: min(480px, 92vw); box-shadow: 0 8px 32px rgba(0,0,0,.2); }
.fb-modal h3 { margin: 0 0 6px; }
.fb-hint { margin: 0 0 10px; font-size: 12px; color: #888; }
.fb-textarea { width: 100%; box-sizing: border-box; border: 1px solid #d0d7de; border-radius: 8px; padding: 8px; font-size: 13px; resize: vertical; }
.fb-check { display: block; margin-top: 10px; font-size: 13px; color: #444; cursor: pointer; }
.fb-ctx-preview { color: #999; font-size: 12px; }
.fb-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 14px; }
</style>
