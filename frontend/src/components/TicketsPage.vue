<!-- ============================================================================
   components/TicketsPage.vue — 翻译工单（前台独立 Tab，全屏页）
   职责：批量翻译工单的全生命周期管理
   - 创建：文本粘贴（大文本域）或上传文件（docx/xlsx/pptx/pdf/txt/csv ≤10MB）
   - 列表：我的工单（隐私隔离，后端强制）、状态流转、步骤进度展开
   - 下载：docx/xlsx/pptx 原格式回写产物；纯文本/PDF 为 xlsx 对照表
   ============================================================================ -->
<template>
  <div class="tp-wrap">
    <h2 class="tp-title">📋 {{ t('tk.entry') }}</h2>
    <p class="ad-hint">{{ t('tk.createHint') }}</p>

    <!-- ===== 创建工单 ===== -->
    <div class="ad-chart-card">
      <h3>{{ t('tk.createTitle') }}</h3>
      <!-- 模式切换：文本 / 文件 -->
      <div class="tp-mode">
        <button :class="['tp-mode-btn', mode === 'text' ? 'on' : '']" @click="mode = 'text'">📝 {{ t('tk.modeText') }}</button>
        <button :class="['tp-mode-btn', mode === 'file' ? 'on' : '']" @click="mode = 'file'">📎 {{ t('tk.modeFile') }}</button>
      </div>

      <input v-model="form.title" :placeholder="t('tk.titlePlaceholder')" class="ad-input" style="width:100%;margin-bottom:8px" />

      <!-- 文本模式 -->
      <textarea
        v-if="mode === 'text'"
        v-model="form.text"
        :placeholder="t('tk.textPlaceholder')"
        rows="14"
        class="tp-textarea"
      ></textarea>

      <!-- 文件模式 -->
      <div v-else class="tp-filezone" @click="($refs.fileInput as HTMLInputElement).click()">
        <input ref="fileInput" type="file" hidden accept=".docx,.xlsx,.pptx,.pdf,.txt,.csv" @change="onFileSelect" />
        <div v-if="!file" class="tp-file-hint">📎 {{ t('tk.fileHint') }}</div>
        <div v-else class="tp-file-name">{{ file.name }}（{{ (file.size / 1024).toFixed(0) }} KB）</div>
      </div>

      <div class="tp-row">
        <label>{{ t('tk.langsLabel') }}</label>
        <LangMultiSelect v-model="selectedLangs" />
        <button class="ad-btn ad-btn-green tp-submit" :disabled="creating" @click="create">
          {{ creating ? t('tk.submitting') : t('tk.create') }}
        </button>
      </div>
    </div>

    <!-- ===== 我的工单 ===== -->
    <div class="ad-chart-card">
      <div class="tp-row" style="justify-content:space-between;margin-bottom:8px">
        <h3 style="margin:0">{{ t('tk.myTickets') }}</h3>
        <button class="ad-btn-sm" @click="load">🔄</button>
      </div>
      <table class="ad-table">
        <thead><tr>
          <th>{{ t('tk.colNo') }}</th><th>{{ t('users.colName') }}</th><th>{{ t('users.colStatus') }}</th>
          <th>{{ t('tk.colLangs') }}</th><th>{{ t('users.colLastLogin') }}</th><th>{{ t('org.colActions') }}</th>
        </tr></thead>
        <tbody>
          <tr v-for="tk in tickets" :key="tk.id">
            <td><code>{{ tk.ticket_no || tk.id }}</code></td>
            <td style="max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" :title="tk.title">{{ tk.title }}</td>
            <td><span :class="'tk-status-' + tk.status">{{ statusLabel(tk.status) }}</span></td>
            <td>{{ tk.target_langs }}</td>
            <td>{{ fmtTime(tk.created_at) }}</td>
            <td class="ad-td">
              <button v-if="tk.status === 'draft'" class="ad-btn-sm" @click="run(tk)">{{ t('tk.run') }}</button>
              <button v-if="tk.status === 'completed'" class="ad-btn-sm ad-btn-green" @click="download(tk)">⬇ {{ t('tk.download') }}</button>
              <button class="ad-btn-sm" @click="toggleDetail(tk)">{{ t('tk.detail') }}</button>
            </td>
          </tr>
          <tr v-if="!tickets.length"><td colspan="6" class="ad-empty">{{ t('tk.empty') }}</td></tr>
        </tbody>
      </table>

      <!-- 步骤进度展开区 -->
      <div v-if="detail" class="tp-detail">
        <h4>{{ detail.ticket?.title }} · {{ t('tk.progress') }}</h4>
        <ul class="tp-steps">
          <li v-for="st in detail.states || []" :key="st.id">
            <b>{{ st.step }}</b> — <span :class="'st-' + st.status">{{ st.status }}</span>
            <template v-if="st.error"> ⚠️ {{ st.error }}</template>
          </li>
        </ul>
        <p class="ad-hint" v-if="!(detail.states || []).length">{{ t('tk.noSteps') }}</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import LangMultiSelect from './LangMultiSelect.vue'
import { myTickets, ticketCreate, ticketCreateFile, ticketRun, ticketDetail, ticketDownload } from '@/api'
import { t } from '@/i18n'
import { fmtTime } from './admin/ui'

// 创建表单与模式
const mode = ref<'text' | 'file'>('text')
const form = ref({ title: '', text: '' })
// 目标语言（与工作台同款多选选择器；默认 en）
const selectedLangs = ref<string[]>(['en'])
const file = ref<File | null>(null)
// 提交用语言串（逗号分隔）
const langsJoined = computed(() => (selectedLangs.value.length ? selectedLangs.value.join(',') : 'en'))
const creating = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

// 列表与详情
const tickets = ref<any[]>([])
const detail = ref<any>(null)

// 选择文件
function onFileSelect(e: Event) {
  const f = (e.target as HTMLInputElement).files?.[0]
  if (f) file.value = f
}

// 创建工单：文本走 JSON，文件走 multipart；成功自动入队并刷新
async function create() {
  creating.value = true
  try {
    let r;
    if (mode.value === 'text') {
      if (!form.value.text.trim()) return
      r = await ticketCreate({
        title: form.value.title.trim() || t('tk.defaultTitle'),
        source_text: form.value.text,
        target_langs: langsJoined.value,
      })
    } else {
      if (!file.value) return
      r = await ticketCreateFile(file.value, {
        title: form.value.title.trim(),
        target_langs: langsJoined.value,
      })
    }
    if (!r.success) { alert(r.message); return }
    // 重置表单
    form.value = { title: '', text: '' }
    file.value = null
    if (fileInput.value) fileInput.value.value = ''
    await load()
  } finally {
    creating.value = false
  }
}


// 运行草稿工单
async function run(tk: any) {
  const r = await ticketRun(tk.id)
  if (!r.success) { alert(r.message); return }
  await load()
}

// 下载结果
async function download(tk: any) {
  try { await ticketDownload(tk.id) } catch (e: any) { alert(e?.message || 'download failed') }
}

// 展开/收起步骤进度
async function toggleDetail(tk: any) {
  if (detail.value && detail.value.ticket?.id === tk.id) { detail.value = null; return }
  const r = await ticketDetail(tk.id)
  if (r.success) detail.value = r
}

// 加载我的工单；进行中每 5s 自动刷新
async function load() {
  const r = await myTickets()
  if (r.success) tickets.value = (r as any).tickets || []
}
onMounted(load)
setInterval(() => { if (tickets.value.some(x => ['queued', 'in_progress'].includes(x.status))) load() }, 5000)

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
</script>

<style scoped>
.tp-wrap { max-width: 1100px; margin: 0 auto; padding: 20px 24px; }
.tp-title { margin: 0 0 4px; }
.tp-mode { display: flex; gap: 8px; margin-bottom: 10px; }
.tp-mode-btn {
  border: 1px solid #d8dee6; background: #f7f9fc; border-radius: 8px;
  padding: 6px 16px; cursor: pointer; font-size: 14px;
}
.tp-mode-btn.on { background: #e8f1ff; border-color: #4a7dff; color: #2c62d9; font-weight: 600; }
.tp-textarea { width: 100%; font-family: inherit; font-size: 14px; line-height: 1.6; border-radius: 8px; border: 1px solid #d8dee6; padding: 10px; resize: vertical; }
.tp-filezone {
  border: 2px dashed #c9d4e3; border-radius: 10px; padding: 34px;
  text-align: center; cursor: pointer; color: #667; background: #fafbfd;
}
.tp-filezone:hover { border-color: #4a7dff; background: #f4f8ff; }
.tp-file-name { font-size: 15px; color: #2c62d9; font-weight: 600; }
.tp-row { display: flex; align-items: center; gap: 10px; margin-top: 10px; }
.tp-row label { font-size: 13px; color: #555; white-space: nowrap; }
.tp-langs { width: 260px; }
.tp-submit { margin-left: auto; }
.tp-detail { border-top: 1px dashed #ddd; margin-top: 10px; padding-top: 8px; }
.tp-steps { list-style: none; padding-left: 10px; }
.tp-steps li { padding: 3px 0; font-size: 13px; }
.tk-status-queued { color: #b8860b; }
.tk-status-in_progress { color: #1e6fd9; }
.tk-status-completed { color: #1a7f37; }
.tk-status-rejected { color: #c0392b; }
.st-success { color: #1a7f37; }
.st-failed { color: #c0392b; }
.st-running { color: #1e6fd9; }
</style>