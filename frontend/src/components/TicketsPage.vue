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

      <!-- 文件模式（支持多选） -->
      <div v-else class="tp-filezone" @click="($refs.fileInput as HTMLInputElement).click()">
        <input ref="fileInput" type="file" multiple hidden accept=".docx,.xlsx,.pptx,.pdf,.txt,.csv,.srt,.vtt,.md,.json,.yaml,.yml" @change="onFileSelect" />
        <div v-if="!files.length" class="tp-file-hint">📎 {{ t('tk.fileHint') }}<br /><span style="font-size:12px">{{ t('tk.multiHint') }}</span></div>
        <div v-else class="tp-file-name">
          {{ files.length > 1 ? tpl('tk.filesCount', { n: files.length }) : files[0].name }}
          （{{ filesTotalKB }} KB）
          <button class="ad-btn-sm" style="margin-left:8px" @click.stop="clearFiles">{{ t('users.colActions')==='操作' ? '重选' : 'Reselect' }}</button>
        </div>
      </div>

      <div class="tp-row">
        <label>{{ t('tk.langsLabel') }}</label>
        <LangMultiSelect v-model="selectedLangs" />
        <!-- 报价预览：预计消耗与余额 -->
        <span v-if="estimate && estimate.sentence_enforced" class="tp-estimate" :class="{ warn: !estimate.activated }">
          <template v-if="!estimate.activated">⚠️ {{ estimate.hint || t('tk.estUnavailable') }}</template>
          <template v-else>{{ tpl('tk.estimateLine', { cost: estimate.cost, balance: estimate.balance }) }}</template>
        </span>
        <button class="ad-btn ad-btn-green tp-submit" :disabled="creating || estimateBlocked" @click="create">
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
        <!-- QA 质检摘要（存在报告时展示） -->
        <p class="tp-estimate" :class="{ warn: qaSummary && !qaSummary.pass }" v-if="qaSummary">
          🧪 QA — {{ tpl('tk.qaSummary', { errors: qaSummary.errors, warnings: qaSummary.warnings }) }}
          <template v-if="!qaSummary.pass">
            <span v-for="(iss, i) in (qaSummary.issues || []).slice(0, 5)" :key="i"><br />{{ iss.level === 'error' ? '✖' : '⚠' }} [{{ iss.lang }}/{{ iss.rule }}] {{ iss.detail }}</span>
          </template>
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import LangMultiSelect from './LangMultiSelect.vue'
import { myTickets, ticketCreate, ticketCreateFile, ticketRun, ticketDetail, ticketDownload, translationEstimate } from '@/api'
import { t, tpl } from '@/i18n'
import { fmtTime } from './admin/ui'

// 创建表单与模式
// 创建模式：text=粘贴文本 / file=上传文件（支持混合类型多选：pdf+word+ppt+excel 等任意组合）
const mode = ref<'text' | 'file'>('text')
const form = ref({ title: '', text: '' })
// 目标语言（与工作台同款多选选择器；默认 en）
const selectedLangs = ref<string[]>(['en'])
// 提交用语言串（逗号分隔）
const langsJoined = computed(() => (selectedLangs.value.length ? selectedLangs.value.join(',') : 'en'))
const creating = ref(false)
// 多文件选择：逐文件独立走各自提取/回写管道，互不影响
const files = ref<File[]>([])
const fileInput = ref<HTMLInputElement | null>(null)
// 已选文件总大小（KB 展示）
const filesTotalKB = computed(() => (files.value.reduce((a, f) => a + f.size, 0) / 1024).toFixed(0))

// ---- 报价预览（文本模式实时预估；强制计费且未开通时禁提交） ----
const estimate = ref<any>(null)
let estimateTimer: ReturnType<typeof setTimeout> | null = null
// 输入文本或目标语言变化后 500ms 防抖刷新报价预估
watch([() => form.value.text, selectedLangs], () => {
  if (estimateTimer) clearTimeout(estimateTimer)
  estimateTimer = setTimeout(refreshEstimate, 500)
})
// refreshEstimate 请求文本翻译预估报价（空文本清空预览，失败时静默置空）
async function refreshEstimate() {
  const text = form.value.text.trim()
  if (!text) { estimate.value = null; return }
  try {
    const r: any = await translationEstimate({ text, target_langs: [...selectedLangs.value] })
    if (r.success) estimate.value = r
  } catch { estimate.value = null }
}
// estimateBlocked 强制按句计费且账号未开通额度时禁止提交工单
const estimateBlocked = computed(() => {
  if (!estimate.value || !estimate.value.sentence_enforced) return false
  return !estimate.value.activated
})

// QA 质检摘要（从工单 final_result 解析 qa_report）
const qaSummary = computed<any | null>(() => {
  const raw = detail.value?.ticket?.final_result
  if (!raw) return null
  try {
    return (JSON.parse(raw) as any).qa_report || null
  } catch { return null }
})

// 列表与详情
const tickets = ref<any[]>([])
const detail = ref<any>(null)

// onFileSelect 记录用户选择的多文件（追加式：可多次选择累积；过滤重复文件名）
function onFileSelect(e: Event) {
  const list = Array.from((e.target as HTMLInputElement).files || [])
  if (!list.length) return
  const exist = new Set(files.value.map(f => f.name + f.size))
  for (const f of list) {
    const key = f.name + f.size
    if (!exist.has(key)) { files.value.push(f); exist.add(key) }
  }
}
// clearFiles 清空已选文件
function clearFiles() {
  files.value = []
  if (fileInput.value) fileInput.value.value = ''
}

// 创建工单：文本走 JSON，文件走 multipart；成功自动入队并刷新
// create 创建工单：文本走 JSON，文件走 multipart；成功自动入队并刷新列表
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
      if (!files.value.length) return
      // 多文件提交：混合类型（pdf/word/ppt/excel…）逐文件独立处理，产物打包下载
      r = await ticketCreateFile([...files.value], {
        title: form.value.title.trim(),
        target_langs: langsJoined.value,
      })
    }
    if (!r.success) { alert(r.message); return }
    // 重置表单
    form.value = { title: '', text: '' }
    clearFiles()
    await load()
  } finally {
    creating.value = false
  }
}


// 运行草稿工单
// run 运行草稿工单（入队）
async function run(tk: any) {
  const r = await ticketRun(tk.id)
  if (!r.success) { alert(r.message); return }
  await load()
}

// 下载结果
// download 下载结果文件（blob 触发保存）
async function download(tk: any) {
  try { await ticketDownload(tk.id) } catch (e: any) { alert(e?.message || 'download failed') }
}

// 展开/收起步骤进度
// toggleDetail 展开/收起步骤进度轨迹
async function toggleDetail(tk: any) {
  if (detail.value && detail.value.ticket?.id === tk.id) { detail.value = null; return }
  const r = await ticketDetail(tk.id)
  if (r.success) detail.value = r
}

// 加载我的工单；进行中每 5s 自动刷新
// load 加载我的工单；进行中的每 5s 自动刷新进度
async function load() {
  const r = await myTickets()
  if (r.success) tickets.value = (r as any).tickets || []
}
onMounted(load)
setInterval(() => { if (tickets.value.some(x => ['queued', 'in_progress'].includes(x.status))) load() }, 5000)

// 状态中文标签
// statusLabel 工单状态 → 中文标签
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
.tp-estimate { font-size: 12px; color: #5f6368; white-space: nowrap; }
.tp-estimate.warn { color: #e8710a; font-weight: 600; }
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