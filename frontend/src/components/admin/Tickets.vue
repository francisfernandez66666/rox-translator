<!-- ============================================================================
   components/admin/Tickets.vue — ❓ 问题反馈（BBS 模式，合并原「用户反馈」）+ 审批台
   交互：
   - 超管：落地即用户反馈列表 → 点击「查看详情」进入细节页（上下文+回复线程+回复框+完成反馈）
   - 非超管：提交意见反馈表单 + 我的反馈列表 → 点击查看详情/继续回复
   - 状态流转：反馈中(open) → 超管「完成反馈」→ 已完成(resolved)，归档后禁止再回复
   - 提醒：新反馈→通知超管；超管回复→通知提交者；提交者补充→再通知超管（站内信铃铛）
   - 审批台：pro 文本工单批准/驳回（原有能力保留在页面底部）
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('fb.workbench') }}</h2>
    <p class="ad-hint">{{ isSuper ? t('fb.superHint') : t('fb.userHint') }}</p>
    <div v-if="isSuper" class="ad-row" style="margin-bottom:12px">
      <button class="ad-btn-sm" :class="{ 'ad-btn-green': fbTab === 'feedback' }" @click="fbTab = 'feedback'">💬 {{ t('fb.tabFeedback') }}</button>
      <button class="ad-btn-sm" :class="{ 'ad-btn-green': fbTab === 'review' }" @click="switchReview">📚 {{ t('fb.tabReview') }}</button>
    </div>

    <!-- ===== 记忆审核子视图（超管） ===== -->
    <template v-if="isSuper && fbTab === 'review'">
      <div class="ad-row">
        <select v-model="rvFilter" class="ad-input ad-mini-w" @change="loadReviews">
          <option value="pending">{{ t('tmr.pending') }}</option>
          <option value="approved">{{ t('tmr.approved') }}</option>
          <option value="rejected">{{ t('tmr.rejected') }}</option>
        </select>
        <button class="ad-btn-sm" @click="loadReviews">{{ t('tickets.refresh') }}</button>
      </div>
      <table class="ad-table">
        <thead><tr><th>#</th><th>{{ t('tmr.zh') }}</th><th>{{ t('tmr.trans') }}</th><th>{{ t('tk.colLangs') }}</th><th>{{ t('tmr.source') }}</th><th>{{ t('tmr.hits') }}</th><th>{{ t('users.colStatus') }}</th><th>{{ t('org.colActions') }}</th></tr></thead>
        <tbody>
          <tr v-for="c in reviews" :key="c.id">
            <td>{{ c.id }}</td>
            <td class="ad-ellipsis" style="max-width:220px" :title="c.zh">{{ c.zh }}</td>
            <td class="ad-ellipsis" style="max-width:220px" :title="c.trans">{{ c.trans }}</td>
            <td>{{ c.lang }}</td>
            <td>{{ srcLabel(c.source) }}<template v-if="c.ref_type === 'feedback'"> · <a href="#" @click.prevent="jumpFeedback(c.ref_id)">{{ t('tmr.linkFb') }}#{{ c.ref_id }}</a></template></td>
            <td>{{ c.hit_count }}</td>
            <td><span class="fb-status" :class="c.status === 'approved' ? 'resolved' : c.status === 'pending' ? 'open' : ''">
              {{ c.status === 'approved' ? t('tmr.approved') : c.status === 'rejected' ? t('tmr.rejected') : t('tmr.pending') }}</span></td>
            <td class="ad-td">
              <template v-if="c.status === 'pending'">
                <button class="ad-btn-sm ad-btn-green" @click="doApproveReview(c)">✔</button>
                <button class="ad-btn-sm ad-btn-red" @click="doRejectReview(c)">✘</button>
              </template>
            </td>
          </tr>
          <tr v-if="!reviews.length"><td colspan="8" class="ad-empty">{{ t('fb.empty') }}</td></tr>
        </tbody>
      </table>
    </template>

    <!-- ===== 用户反馈子视图 ===== -->
    <template v-else>

    <!-- ===== 详情视图 ===== -->
    <div v-if="selected" class="ad-chart-card">
      <button class="ad-btn-sm" style="float:right" @click="selected = null">← {{ t('fb.backToList') }}</button>
      <h3>#{{ selected.id }} · {{ selected.user_name || ('#' + selected.user_id) }}
        <span class="fb-status" :class="selected.status">
          {{ selected.status === 'resolved' ? t('fb.statusResolved') : t('fb.statusOpen') }}
        </span>
      </h3>
      <p style="white-space:pre-wrap">{{ selected.content }}</p>

      <!-- 翻译上下文（勾选同意才存；仅超管可见按钮已由后端数据控制） -->
      <div v-if="selected.with_context" class="fb-ctx-block">
        <b>{{ t('fb.ctxAttached') }}</b>
        <pre v-if="selected.source_text">{{ selected.source_text }}</pre>
        <div v-for="(v, k) in ctxTranslations(selected)" :key="k" class="fb-ctx-lang">
          <b>[{{ k }}]</b> {{ v }}
        </div>
      </div>

      <!-- 回复线程 -->
      <div v-if="selected.replies && selected.replies.length" class="fb-thread">
        <div v-for="(r, i) in selected.replies" :key="i" class="fb-reply" :class="{ mine: r.role === 'admin' }">
          <div class="fb-reply-meta">{{ r.name }} · {{ r.role === 'admin' ? '超管' : '用户' }} · {{ fmtAt(r.at) }}</div>
          <div style="white-space:pre-wrap">{{ r.content }}</div>
        </div>
      </div>
      <p v-else class="ad-hint">{{ t('fb.noReplies') }}</p>

      <!-- 回复框 -->
      <div v-if="selected.status === 'open'" class="ad-row" style="margin-top:10px">
        <input v-model="replyDraft" class="ad-input" style="flex:1"
               :placeholder="t('fb.replyPlaceholder')" maxlength="1000" @keydown.enter="doReply" />
        <button class="ad-btn" :disabled="!replyDraft.trim()" @click="doReply">↩ {{ t('fb.reply') }}</button>
        <button v-if="isSuper" class="ad-btn ad-btn-green" @click="doResolve">✔ {{ t('fb.complete') }}</button>
      </div>
      <p v-else class="ad-hint">✅ {{ t('fb.archivedHint') }}</p>
    </div>

    <!-- ===== 列表视图 ===== -->
    <template v-else>
      <!-- 非超管：提交表单 -->
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

      <!-- 过滤 + 表格列表 -->
      <div class="ad-row" style="margin-top:12px">
        <select v-model="statusFilter" class="ad-input ad-mini-w" @change="loadFeedbacks">
          <option value="">{{ t('fb.filterAll') }}</option>
          <option value="open">{{ t('fb.statusOpen') }}</option>
          <option value="resolved">{{ t('fb.statusResolved') }}</option>
        </select>
        <button class="ad-btn-sm" @click="loadFeedbacks">{{ t('tickets.refresh') }}</button>
        <span class="ad-hint">{{ tpl('fb.count', { n: feedbacks.length }) }}</span>
      </div>

      <table class="ad-table">
        <thead><tr>
          <th>#</th><th>{{ t('users.colUser') }}</th>
          <th>{{ t('fb.colTarget') }}</th><th>{{ t('fb.colContent') }}</th>
          <th>{{ t('fb.colMode') }}</th><th>{{ t('overview.colTime') }}</th>
          <th>{{ t('fb.colStatus') }}</th><th>{{ t('org.colActions') }}</th>
        </tr></thead>
        <tbody>
          <tr v-for="f in feedbacks" :key="f.id" :class="{ 'fb-open-row': f.status === 'open' }">
            <td>{{ f.id }}</td>
            <td>{{ f.user_name || ('#' + f.user_id) }}</td>
            <td>{{ f.target_type === 'ticket' ? '🎫 #' + f.ticket_id : '📝 ' + t('fb.targetText') }}</td>
            <td class="ad-ellipsis" style="max-width:260px" :title="f.content">{{ f.content }}</td>
            <td>{{ f.mode === 'fast' ? '⚡' : '🎓' }}</td>
            <td>{{ fmtAt(f.created_at) }}</td>
            <td><span class="fb-status" :class="f.status">
              {{ f.status === 'resolved' ? t('fb.statusResolved') : t('fb.statusOpen') }}</span></td>
            <td class="ad-td">
              <button class="ad-btn-sm" @click="openDetail(f)">👁 {{ t('fb.viewDetail') }}</button>
            </td>
          </tr>
          <tr v-if="!feedbacks.length"><td colspan="8" class="ad-empty">{{ t('fb.empty') }}</td></tr>
        </tbody>
      </table>
    </template>

    </template>

    <!-- ===== 审批台（pro 文本工单，保留） ===== -->
    <h2 style="margin-top:32px">{{ t('tickets.approvalTitle') }}</h2>
    <button class="ad-btn" @click="loadApproval">{{ t('tickets.refresh') }}</button>
    <div v-for="tk in approvalTickets" :key="tk.id" class="ad-approval">
      <div class="ad-approval-head">
        <b>{{ tk.ticket_no }} — {{ tk.title }}</b>
        <span class="ad-pkg-role">{{ tk.status }}</span>
      </div>
      <p class="ad-approval-src">{{ tk.source_text }}</p>
      <textarea :value="tk.final_result" class="ad-input ad-textarea" readonly />
      <div class="ad-row">
        <!-- ★ 修复（2026-08-26 全仓评审 D2）：循环变量原别名 t 遮蔽 i18n 函数 t，
             本区块内 {{ t('tickets.approve') }} 会把工单对象当函数调用 → 渲染即 TypeError；
             统一改名为 tk。 -->
        <button class="ad-btn ad-btn-green" @click="doApprove(tk, 'approve')">{{ t('tickets.approve') }}</button>
        <input v-model="tk._reason" :placeholder="t('tickets.reasonPlaceholder')" class="ad-input" />
        <input v-model="tk._suggestion" :placeholder="t('tickets.suggestionPlaceholder')" class="ad-input" />
        <button class="ad-btn ad-btn-red" @click="doApprove(tk, 'reject')">{{ t('tickets.reject') }}</button>
      </div>
    </div>
    <div v-if="!approvalTickets.length" class="ad-hint">{{ t('tickets.noApproval') }}</div>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { approveList, approveAction } from '@/api'
import { feedbackList, feedbackReply, resolveFeedback, createFeedback, type FeedbackRecord } from '@/api/feedback'
import { activeTenantId, isSuper, pendingFeedbackId, pendingPanel } from './store'
import { listTmReview, approveTmReview, rejectTmReview, type TmReviewItem } from '@/api/tmreview'
import { t, tpl } from '@/i18n'

// ===== 反馈工作台状态 =====
const feedbacks = ref<(FeedbackRecord & { _reply?: string })[]>([])
const statusFilter = ref('')
const selected = ref<any>(null)
const replyDraft = ref('')
const newContent = ref('')
const submitting = ref(false)
// ===== 记忆审核（超管子视图）=====
const fbTab = ref<'feedback' | 'review'>('feedback')
const reviews = ref<TmReviewItem[]>([])
const rvFilter = ref('pending')
// loadReviews 拉取 TM 待审候选（按状态过滤）
async function loadReviews() {
  const r = await listTmReview(rvFilter.value)
  if (r.success) reviews.value = (r.candidates || []) as any
}
// switchReview 切到「记忆审核」子视图并刷新候选
function switchReview() { fbTab.value = 'review'; loadReviews() }
// srcLabel 候选来源→中文名
function srcLabel(src: string) {
  return src === 'bitext' ? '双语文本' : src === 'tmx' ? 'TMX 导入' : src === 'feedback' ? '用户反馈修正' : '次数达标'
}
// 联动：铃铛通知 / 审核台「关联反馈」跳转
async function jumpFeedback(fid: number) {
  fbTab.value = 'feedback'
  statusFilter.value = ''
  await loadFeedbacks()
  const f = feedbacks.value.find(x => x.id === fid)
  if (f) openDetail(f)
}
watch(pendingFeedbackId, async fid => {
  if (!fid) return
  await jumpFeedback(fid)
  pendingFeedbackId.value = 0
})
// 审核台跳反馈详情：先切回面板信号归零避免循环
watch(pendingPanel, v => { if (v) pendingPanel.value = '' })
// doApproveReview 审核通过候选：正式落库为翻译记忆后刷新列表
async function doApproveReview(c: TmReviewItem) {
  const r = await approveTmReview(c.id)
  if (!r.success) { alert(r.message); return }
  await loadReviews()
}
// doRejectReview 驳回候选：废弃不落库后刷新列表
async function doRejectReview(c: TmReviewItem) {
  const r = await rejectTmReview(c.id)
  if (!r.success) { alert(r.message); return }
  await loadReviews()
}

// loadFeedbacks 按过滤条件加载列表（超管=全部，其他=本人）
async function loadFeedbacks() {
  const r = await feedbackList(statusFilter.value)
  if (r.success) feedbacks.value = (r.feedbacks || []) as any
}
// openDetail 进入详情视图
function openDetail(f: any) { selected.value = f; replyDraft.value = '' }
// submitFeedback 非超管提交意见反馈（状态=反馈中，触发超管站内提醒）
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
// doReply 追加回复（对方会收到站内提醒）
async function doReply() {
  if (!selected.value || !replyDraft.value.trim()) return
  const r = await feedbackReply(selected.value.id, replyDraft.value.trim())
  if (!r.success) { alert(r.message); return }
  selected.value.replies = (r as any).replies || []
  replyDraft.value = ''
}
// doResolve 超管完成反馈（归档，双方不可再回复）
async function doResolve() {
  if (!confirm(t('fb.resolveConfirm'))) return
  const r = await resolveFeedback(selected.value.id)
  if (!r.success) { alert(r.message); return }
  selected.value.status = 'resolved'
  selected.value.handled_at = new Date().toISOString()
  await loadFeedbacks()
}
// ctxTranslations 解析附带的译文 JSON 上下文
function ctxTranslations(f: any): Record<string, string> {
  try { return JSON.parse(f.translations_json || '{}') } catch { return {} }
}
// fmtAt 本地化时间
function fmtAt(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return isNaN(+d) ? iso : d.toLocaleString()
}

// ===== 审批台（保留） =====
const approvalTickets = ref<any[]>([])
// loadApproval 拉取待审批工单列表（审批台）
async function loadApproval() {
  const r = await approveList()
  if (r.success) approvalTickets.value = (r as any).tickets || []
}
// doApprove 执行审批动作：approve=通过 / reject=驳回（附驳回理由与修改建议），成功后刷新
async function doApprove(t: any, action: 'approve' | 'reject') {
  const r = await approveAction(t.id, action, t._reason || '', t._suggestion || '', '')
  if (!r.success) { alert(r.message); return }
  t._reason = ''
  t._suggestion = ''
  await loadApproval()
}
onMounted(() => { loadFeedbacks(); loadApproval() })
// ★ 铃铛通知点击 → 定位并打开对应反馈详情
watch(pendingFeedbackId, async fid => {
  if (!fid) return
  statusFilter.value = ''
  await loadFeedbacks()
  const f = feedbacks.value.find(x => x.id === fid)
  if (f) openDetail(f)
  pendingFeedbackId.value = 0
})
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
.fb-ctx-block { background: #fafafa; border: 1px dashed #ddd; border-radius: 8px; padding: 8px 10px; margin-top: 8px; font-size: 12.5px; }
.fb-ctx-block pre { white-space: pre-wrap; margin: 4px 0; }
.fb-ctx-lang { margin-top: 4px; }
.fb-open-row { background: #fdfdff; }
</style>
