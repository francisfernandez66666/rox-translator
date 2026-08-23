<!-- ============================================================================
   components/admin/Feedback.vue — 用户反馈面板（仅超管工作台可见）
   职责：反馈列表（状态过滤/上下文展开）+ 标记已处理（附备注）
   数据源：GET /api/admin/feedbacks、POST /api/admin/feedbacks/resolve
   ============================================================================ -->

<template>
  <section class="ad-section">
    <h2>{{ t('fb.panelTitle') }}</h2>
    <div class="ad-row">
      <select v-model="statusFilter" class="ad-input" style="width:auto" @change="load">
        <option value="">{{ t('fb.all') }}</option>
        <option value="open">{{ t('fb.open') }}</option>
        <option value="resolved">{{ t('fb.resolved') }}</option>
      </select>
      <button class="ad-btn" @click="load">🔄 {{ t('workflow.refresh') }}</button>
    </div>

    <table class="ad-table">
      <thead><tr>
        <th>#</th><th>{{ t('audit.tenant') }}</th><th>{{ t('users.colUser') }}</th>
        <th>{{ t('fb.colTarget') }}</th><th>{{ t('fb.colContent') }}</th>
        <th>{{ t('fb.colMode') }}</th><th>{{ t('overview.colTime') }}</th><th>{{ t('fb.colStatus') }}</th><th>{{ t('org.colActions') }}</th>
      </tr></thead>
      <tbody>
        <template v-for="f in list" :key="f.id">
          <tr :class="{ 'fb-open-row': f.status === 'open' }">
            <td>{{ f.id }}</td>
            <td>{{ tenantName(f.tenant_id) }}</td>
            <td>#{{ f.user_id }}</td>
            <td>{{ f.target_type === 'ticket' ? '🎫 ' + t('fb.targetTicket') + ' #' + f.ticket_id : '📝 ' + t('fb.targetText') }}</td>
            <td class="ad-ellipsis" style="max-width:280px" :title="f.content">{{ f.content }}</td>
            <td>{{ f.mode === 'fast' ? '⚡' : '🎓' }}</td>
            <td>{{ fmtTime(f.created_at) }}</td>
            <td><span :class="f.status === 'open' ? 'st-failed' : 'st-success'">{{ f.status === 'open' ? t('fb.open') : t('fb.resolved') }}</span></td>
            <td class="ad-td">
              <button v-if="f.with_context" class="ad-btn-sm" @click="toggleCtx(f)">{{ ctxOpen === f.id ? '▲' : '▼' }} {{ t('fb.ctxBtn') }}</button>
              <button v-if="f.status === 'open'" class="ad-btn-sm ad-btn-green" @click="resolve(f)">✓ {{ t('fb.resolve') }}</button>
            </td>
          </tr>
          <!-- 上下文展开行：源文 + 各语言译文 -->
          <tr v-if="ctxOpen === f.id">
            <td colspan="9" class="fb-ctx-cell">
              <div v-if="f.source_text" class="fb-ctx-block"><b>原文：</b><pre>{{ f.source_text }}</pre></div>
              <div v-if="ctxTranslations(f).length" class="fb-ctx-block">
                <b>译文：</b>
                <pre v-for="(row, i) in ctxTranslations(f)" :key="i">{{ row }}</pre>
              </div>
            </td>
          </tr>
        </template>
        <tr v-if="!list.length"><td colspan="9" class="ad-empty">{{ t('fb.empty') }}</td></tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { adminFeedbacks, resolveFeedback, type FeedbackItem } from '@/api'
import { t, tpl } from '@/i18n'
import { fmtTime } from './ui'
import { tenantList } from './store'

// 列表与过滤状态
const list = ref<FeedbackItem[]>([])
const statusFilter = ref('')
// 上下文展开的反馈 ID
const ctxOpen = ref<number>(0)

// load 拉取反馈列表
async function load() {
  try {
    const r = await adminFeedbacks(statusFilter.value)
    if (r.success) list.value = (r as any).feedbacks || []
  } catch { list.value = [] }
}

// toggleCtx 展开/收起上下文
function toggleCtx(f: FeedbackItem) {
  ctxOpen.value = ctxOpen.value === f.id ? 0 : f.id
}

// ctxTranslations 解析译文 JSON 为「语言: 译文」行数组
function ctxTranslations(f: FeedbackItem): string[] {
  try {
    const m = JSON.parse(f.translations || '{}')
    return Object.entries(m).map(([k, v]) => `${k}: ${v}`)
  } catch { return [] }
}

// tenantName 租户名（复用共享 store 的租户列表；查不到回退 ID）
function tenantName(tid: number): string {
  const hit = tenantList.value.find(x => x.id === tid)
  return hit ? hit.name : '#' + tid
}

// resolve 标记已处理（备注经输入框确认）
async function resolve(f: FeedbackItem) {
  const note = prompt(tpl('fb.resolvePrompt', { id: f.id })) || ''
  if (note === null) return
  const r = await resolveFeedback((f as any).id, note)
  if (r.success) await load()
  else alert((r as any).message)
}

onMounted(load)
</script>

<style scoped>
.fb-open-row td { background: rgba(255, 152, 0, .05); }
.fb-ctx-cell { background: #fafbfd; }
.fb-ctx-block { margin: 6px 0; }
.fb-ctx-block pre { white-space: pre-wrap; word-break: break-all; background: #fff; border: 1px solid #e0e0e0; border-radius: 6px; padding: 6px 8px; margin: 4px 0; font-size: 12px; }
</style>
