<!-- ============================================================================
   components/TicketsPanel.vue — 翻译工单面板（前台工作台抽屉）
   职责：创建文本翻译工单（入队异步执行）+ 我的工单列表（状态/步骤条/下载）
   隐私：非超管仅可见自己创建的工单（后端强制）
   ============================================================================ -->
<template>
  <div class="tk-wrap">
    <!-- 创建表单 -->
    <div class="tk-create">
      <input v-model="form.title" :placeholder="t('tk.titlePlaceholder')" class="ad-input" style="width:100%" />
      <textarea v-model="form.text" :placeholder="t('tk.textPlaceholder')" rows="5" style="width:100%;margin-top:6px"></textarea>
      <div class="ad-row" style="margin-top:6px">
        <input v-model="form.langs" :placeholder="t('tk.langsPlaceholder')" class="ad-input" style="flex:1" />
        <button class="ad-btn ad-btn-green" :disabled="creating || !form.text.trim()" @click="create">
          {{ creating ? t('tk.submitting') : t('tk.create') }}
        </button>
      </div>
      <div class="ad-hint">{{ t('tk.createHint') }}</div>
    </div>

    <!-- 我的工单 -->
    <h4 style="margin:14px 0 6px">{{ t('tk.myTickets') }}</h4>
    <table class="ad-table">
      <thead><tr><th>{{ t('tk.colNo') }}</th><th>{{ t('users.colName') }}</th><th>{{ t('users.colStatus') }}</th><th>{{ t('org.colActions') }}</th></tr></thead>
      <tbody>
        <tr v-for="tk in tickets" :key="tk.id">
          <td>{{ tk.ticket_no || tk.id }}</td>
          <td style="max-width:160px;overflow:hidden;text-overflow:ellipsis">{{ tk.title }}</td>
          <td><span :class="'tk-status-' + tk.status">{{ statusLabel(tk.status) }}</span></td>
          <td class="ad-td">
            <button v-if="tk.status === 'draft'" class="ad-btn-sm" @click="run(tk)">{{ t('tk.run') }}</button>
            <button v-if="tk.status === 'completed'" class="ad-btn-sm ad-btn-green" @click="download(tk)">{{ t('tk.download') }}</button>
            <button class="ad-btn-sm" @click="showDetail(tk)">{{ t('tk.detail') }}</button>
          </td>
        </tr>
        <tr v-if="!tickets.length"><td colspan="4" class="ad-empty">{{ t('tk.empty') }}</td></tr>
      </tbody>
    </table>

    <!-- 详情抽屉内容（选中时展示步骤轨迹） -->
    <div v-if="detail" class="tk-detail">
      <h4>{{ detail.ticket?.title }} · {{ t('tk.progress') }}</h4>
      <ul class="tk-steps">
        <li v-for="st in detail.states || []" :key="st.id">
          <b>{{ st.step }}</b> — {{ st.status }}<template v-if="st.error"> ⚠️ {{ st.error }}</template>
        </li>
      </ul>
      <p class="ad-hint" v-if="!(detail.states || []).length">{{ t('tk.noSteps') }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { myTickets, ticketCreate, ticketRun, ticketDetail, ticketDownload } from '@/api'
import { t, tpl } from '@/i18n'

// 表单与数据
const form = ref({ title: '', text: '', langs: 'en' })
const tickets = ref<any[]>([])
const creating = ref(false)
const detail = ref<any>(null)

// 创建工单（成功后刷新列表）
async function create() {
  if (!form.value.text.trim()) return
  creating.value = true
  try {
    const r = await ticketCreate({
      title: form.value.title.trim() || t('tk.defaultTitle'),
      source_text: form.value.text,
      target_langs: form.value.langs,
    })
    if (!r.success) { alert(r.message); return }
    form.value = { title: '', text: '', langs: form.value.langs }
    await load()
  } finally {
    creating.value = false
  }
}

// 运行草稿工单（入队）
async function run(tk: any) {
  const r = await ticketRun(tk.id)
  if (!r.success) { alert(r.message); return }
  await load()
}

// 下载结果（blob 触发保存；失败提示原因）
async function download(tk: any) {
  try {
    await ticketDownload(tk.id)
  } catch (e: any) {
    alert(e?.message || 'download failed')
  }
}

// 展开详情（拉取步骤轨迹）
async function showDetail(tk: any) {
  const r = await ticketDetail(tk.id)
  if (r.success) detail.value = r
}

// 状态中文标签
function statusLabel(s: string): string {
  switch (s) {
    case 'queued': return t('tk.stQueued')
    case 'in_progress': return t('tk.stRunning')
    case 'pending_approval': return t('tk.stPending')
    case 'approved': return t('tk.stApproved')
    case 'rejected': return t('tk.stRejected')
    case 'completed': return t('tk.stCompleted')
    default: return s || '—'
  }
}

// 加载我的工单；进行中的每 5s 自动刷新进度
async function load() {
  const r = await myTickets()
  if (r.success) tickets.value = (r as any).tickets || []
}
onMounted(load)
// 工单进行中每 5s 轮询刷新（★ 卸载时清理，防止多次进出页面叠加轮询器）
const pollTimer: ReturnType<typeof setInterval> = setInterval(pollActive, 5000)
function pollActive() {
  if (document.hidden) return // 页面不可见时跳过，省资源
  if (tickets.value.some(x => ['queued', 'in_progress'].includes(x.status))) load()
}
onUnmounted(() => clearInterval(pollTimer))
</script>

<style scoped>
.tk-wrap { max-height: 60vh; overflow: auto; }
.tk-create { border-bottom: 1px dashed #ddd; padding-bottom: 10px; margin-bottom: 8px; }
.tk-steps { list-style: none; padding-left: 12px; }
.tk-steps li { padding: 2px 0; font-size: 13px; }
.tk-status-queued { color: #b8860b; }
.tk-status-in_progress { color: #1e6fd9; }
.tk-status-completed { color: #1a7f37; }
.tk-status-rejected, .tk-status-dead { color: #c0392b; }
</style>