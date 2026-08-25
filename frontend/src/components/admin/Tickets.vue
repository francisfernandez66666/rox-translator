<!-- ============================================================================
   components/admin/Tickets.vue — 沟通 · 反馈工作台（BBS 模式）+ 审批台
   职责：
   - 反馈工作台：非超管提交意见反馈并跟踪回复；超管查看全部反馈、逐条回复、完成反馈
     状态流转：反馈中(open) → （超管点击完成反馈）→ 已完成(resolved)
   - 审批台：pro 文本工单待审批列表的批准/驳回（原有能力保留）
   注：前台真实翻译工单展示已移除（用户在「翻译工单」页操作）
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('fb.workbench') }}</h2>
    <p class="ad-hint">{{ isSuper ? t('fb.superHint') : t('fb.userHint') }}</p>

    <!-- ===== 提交意见反馈（所有非超管角色可用） ===== -->
    <div v-if="!isSuper" class="ad-chart-card">
      <h3>{{ t('fb.submitTitle') }}</h3>
      <textarea v-model="newContent" class="ad-input ad-textarea"
                :placeholder="t('fb.contentPlaceholder')" maxlength="1000" />
      <div class="ad-row" style="margin-top:8px">
        <span class="ad-hint" style="flex:1">{{ newContent.length }}/1000</span>
        <button class="ad-btn ad-btn-green" :disabled="!newContent.trim() || submitting" @click="submitFeedback">
          {{ submitting ? t('fb.submitting') : t('fb.submit') }}
        </button>
      </div>
    </div>

    <!-- ===== 状态过滤 + 列表 ===== -->
    <div class="ad-row" style="margin-top:16px">
      <select v-model="statusFilter" class="ad-input ad-mini-w" @change="loadFeedbacks">
        <option value="">{{ t('fb.filterAll') }}</option>
        <option value="open">{{ t('fb.statusOpen') }}</option>
        <option value="resolved">{{ t('fb.statusResolved') }}</option>
      </select>
      <button class="ad-btn-sm" @click="loadFeedbacks">{{ t('tickets.refresh') }}</button>
      <span class="ad-hint">{{ tpl('fb.count', { n: feedbacks.length }) }}</span>
    </div>

    <div v-for="f in feedbacks" :key="f.id" class="ad-chart-card" style="margin-top:12px">
      <div class="ad-approval-head">
        <b>#{{ f.id }} · {{ f.user_name || ('#' + f.user_id) }}</b>
        <span class="fb-status" :class="f.status">
          {{ f.status === 'resolved' ? t('fb.statusResolved') : t('fb.statusOpen') }}
        </span>
      </div>
      <p style="white-space:pre-wrap;margin:8px 0">{{ f.content }}</p>
      <div v-if="f.with_context && f.source_text" class="ad-hint ad-ellipsis" :title="f.source_text">
        {{ t('fb.ctxAttached') }}
      </div>

      <!-- 回复线程（BBS） -->
      <div v-if="f.replies && f.replies.length" class="fb-thread">
        <div v-for="(r, i) in f.replies" :key="i" class="fb-reply" :class="{ mine: r.role === 'admin' }">
          <div class="fb-reply-meta">{{ r.name }} · {{ fmtAt(r.at) }}</div>
          <div style="white-space:pre-wrap">{{ r.content }}</div>
        </div>
      </div>

      <!-- 回复框：开放状态且（超管 或 提交者本人） -->
      <div v-if="f.status === 'open'" class="ad-row" style="margin-top:10px">
        <input v-model="f._reply" class="ad-input" style="flex:1"
               :placeholder="t('fb.replyPlaceholder')" maxlength="1000"
               @keydown.enter="doReply(f)" />
        <button class="ad-btn" :disabled="!(f._reply || '').trim()" @click="doReply(f)">↩ {{ t('fb.reply') }}</button>
        <button v-if="isSuper" class="ad-btn ad-btn-green" @click="doResolve(f)">✔ {{ t('fb.complete') }}</button>
      </div>
    </div>
    <div v-if="!feedbacks.length" class="ad-empty">{{ t('fb.empty') }}</div>

    <!-- ===== 审批台（pro 文本工单，保留） ===== -->
    <h2 style="margin-top:32px">{{ t('tickets.approvalTitle') }}</h2>
    <button class="ad-btn" @click="loadApproval">{{ t('tickets.refresh') }}</button>
    <div v-for="t in approvalTickets" :key="t.id" class="ad-approval">
      <div class="ad-approval-head">
        <b>{{ t.ticket_no }} — {{ t.title }}</b>
        <span class="ad-pkg-role">{{ t.status }}</span>
      </div>
      <p class="ad-approval-src">{{ t.source_text }}</p>
      <textarea :value="t.final_result" class="ad-input ad-textarea" readonly />
      <div class="ad-row">
        <button class="ad-btn ad-btn-green" @click="doApprove(t, 'approve')">{{ t('tickets.approve') }}</button>
        <input v-model="t._reason" :placeholder="t('tickets.reasonPlaceholder')" class="ad-input" />
        <input v-model="t._suggestion" :placeholder="t('tickets.suggestionPlaceholder')" class="ad-input" />
        <button class="ad-btn ad-btn-red" @click="doApprove(t, 'reject')">{{ t('tickets.reject') }}</button>
      </div>
    </div>
    <div v-if="!approvalTickets.length" class="ad-hint">{{ t('tickets.noApproval') }}</div>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { approveList, approveAction } from '@/api'
import { feedbackList, feedbackReply, resolveFeedback, createFeedback, type FeedbackRecord } from '@/api/feedback'
import { activeTenantId, isSuper } from './store'
import { t, tpl } from '@/i18n'

// ===== 反馈工作台 =====
const feedbacks = ref<(FeedbackRecord & { _reply?: string })[]>([])
const statusFilter = ref('')
const newContent = ref('')
const submitting = ref(false)

// loadFeedbacks 按当前过滤条件加载反馈列表
async function loadFeedbacks() {
  const r = await feedbackList(statusFilter.value)
  if (r.success) feedbacks.value = (r.feedbacks || []) as any
}
// submitFeedback 非超管提交意见反馈 → 状态=反馈中(open)
async function submitFeedback() {
  const content = newContent.value.trim()
  if (!content) return
  submitting.value = true
  try {
    const r = await createFeedback({ target_type: 'text', content })
    if (!r.success) { alert(r.message); return }
    newContent.value = ''
    await loadFeedbacks()
  } finally { submitting.value = false }
}
// doReply 追加 BBS 回复（超管或提交者本人）
async function doReply(f: any) {
  const content = (f._reply || '').trim()
  if (!content) return
  const r = await feedbackReply(f.id, content)
  if (!r.success) { alert(r.message); return }
  f._reply = ''
  await loadFeedbacks()
}
// doResolve 超管完成反馈 → 状态=已完成(resolved)
async function doResolve(f: any) {
  if (!confirm(t('fb.resolveConfirm'))) return
  const r = await resolveFeedback(f.id)
  if (!r.success) { alert(r.message); return }
  await loadFeedbacks()
}
// fmtAt 时间展示（本地化到分）
function fmtAt(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return isNaN(+d) ? iso : d.toLocaleString()
}

// ===== 审批台（保留） =====
const approvalTickets = ref<any[]>([])
async function loadApproval() {
  const r = await approveList()
  if (r.success) approvalTickets.value = (r as any).tickets || []
}
async function doApprove(t: any, action: 'approve' | 'reject') {
  const r = await approveAction(t.id, action, t._reason || '', t._suggestion || '', '')
  if (!r.success) { alert(r.message); return }
  t._reason = ''
  t._suggestion = ''
  await loadApproval()
}
onMounted(() => { loadFeedbacks(); loadApproval() })
watch(activeTenantId, () => { loadFeedbacks(); loadApproval() })
</script>

<style scoped>
.fb-status { font-size: 12px; padding: 2px 8px; border-radius: 10px; }
.fb-status.open { background: #e8f0fe; color: #1a73e8; }
.fb-status.resolved { background: #e6f4ea; color: #2e7d32; }
.fb-thread { margin-top: 8px; display: flex; flex-direction: column; gap: 6px; }
.fb-reply { background: #f5f6f8; border-radius: 8px; padding: 6px 10px; font-size: 13px; }
.fb-reply.mine { background: #e8f0fe; }
.fb-reply-meta { font-size: 11px; color: #888; margin-bottom: 2px; }
</style>
