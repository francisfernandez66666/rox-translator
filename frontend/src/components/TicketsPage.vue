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

      <!-- 文件模式（支持多选）：已选文件平铺为独立 item，可单个移除 -->
      <div v-else class="tp-filezone" @click="($refs.fileInput as HTMLInputElement).click()">
        <input ref="fileInput" type="file" multiple hidden accept=".docx,.xlsx,.pptx,.pdf,.txt,.csv,.srt,.vtt,.md,.json,.yaml,.yml" @change="onFileSelect" />
        <div class="tp-file-hint">📎 {{ t('tk.fileHint') }}<br /><span style="font-size:12px">{{ t('tk.multiHint') }}</span></div>
      </div>
      <!-- 已选文件平铺列表：每个文件独立 item，✕ 单独移除 -->
      <div v-if="files.length" class="tp-chip-list">
        <div v-for="(f, idx) in files" :key="f.name + f.size" class="tp-file-chip" :title="f.name">
          <span class="tp-chip-name">📄 {{ f.name }}</span>
          <span class="tp-chip-size">{{ fmtKB(f.size) }}</span>
          <button class="tp-chip-remove" :title="t('org.delete')" @click.stop="removeFileAt(idx)">✕</button>
        </div>
        <div class="tp-chip-total">{{ tpl('tk.filesCount', { n: files.length }) }} · {{ filesTotalKB }} KB</div>
      </div>

      <div class="tp-row">
        <label>{{ t('tk.langsLabel') }}</label>
        <LangMultiSelect v-model="selectedLangs" />
        <!-- ★ 翻译模式：⚡快速（无知识库·初翻+校对）/ 🎓专业校对（全流水线） -->
        <select v-model="qualityMode" class="ad-input" style="width:auto" :title="t('tk.modeTip')">
          <option value="pro">🎓 {{ t('tk.modePro') }}</option>
          <option value="fast">⚡ {{ t('tk.modeFast') }}</option>
        </select>
        <!-- 报价预览：token 区间 + ≈句数 + 余额 -->
        <span v-if="estimate" class="tp-estimate" :class="{ warn: !estimate.sufficient }">
          <template v-if="!estimate.sufficient">⚠️ {{ estimate.hint || t('tk.estUnavailable') }}</template>
          <template v-else>{{ tpl('tk.estimateTokens', { min: fmtNum(estimate.tokens_min), max: fmtNum(estimate.tokens_max), s: fmtNum(estimate.cost_sentences_approx), bal: fmtNum(estimate.balance_tokens) }) }}</template>
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
              <button v-if="tk.status === 'completed'" class="ad-btn-sm ad-btn-green" :disabled="downloadingId === tk.id" @click="download(tk)">{{ downloadingId === tk.id ? '⏳' : '⬇' }} {{ downloadingId === tk.id ? '下载中…' : t('tk.download') }}</button>
              <button v-if="tk.status === 'completed'" class="ad-btn-sm" @click="openFeedback(tk)">💬 {{ t('fb.entry') }}</button>
              <button v-if="tk.status === 'completed'" class="ad-btn-sm ad-btn-red" @click="deleteTicket(tk)">🗑 {{ t('common.delete') }}</button>
              <button v-if="['queued','in_progress'].includes(tk.status)" class="ad-btn-sm ad-btn-red" @click.stop="cancelTicket(tk)">✕ {{ t('tk.cancel') }}</button>
              <button class="ad-btn-sm" @click.stop="toggleDetail(tk, $event)">{{ t('tk.detail') }}</button>
            </td>
          </tr>
          <tr v-if="!tickets.length"><td colspan="6" class="ad-empty">{{ t('tk.empty') }}</td></tr>
        </tbody>
      </table>

      <!-- 进度气泡：点击按钮后弹出，再次点击气泡外任意位置关闭 -->
      <Teleport to="body">
      <div v-if="detail" class="tp-backdrop" @click="detail = null"></div>
      <div v-if="detail" class="tp-popover" :style="popoverStyle">
        <div :class="['tp-popover-arrow', arrowDir === 'up' ? 'arr-up' : 'arr-down']"></div>
        <div class="tp-popover-inner">
          <div class="tp-popover-title">{{ detail.ticket?.title || t('tk.progress') }}</div>
          <div v-if="ticketProgress !== null" class="tp-progress-wrap">
            <div class="tp-progress-bar"><div class="tp-progress-fill" :style="{ width: ticketProgress + '%' }"></div></div>
            <span class="tp-progress-text">{{ ticketProgress }}%</span>
            <span v-if="currentStepLabel" class="tp-step-label">{{ currentStepLabel }}</span>
          </div>
          <div v-if="(detail.states || []).length" class="tp-steps-mini">
            <div v-for="st in detail.states" :key="st.id" class="tp-step-row" :class="'st-' + st.status">
              <span class="tp-step-name">{{ stepNames[st.step] || st.step }}</span>
              <span class="tp-step-status">{{ st.status }}</span>
              <span v-if="st.error" class="tp-step-err">⚠️ {{ st.error }}</span>
            </div>
          </div>
          <p class="ad-hint" v-if="!(detail.states || []).length">{{ t('tk.noSteps') }}</p>
        </div>
      </div>
      </Teleport>
    </div>

    <!-- ★ 用户反馈弹窗（已完成工单 → 超管） -->
    <FeedbackModal
      v-if="feedbackTarget"
      :target="feedbackTarget"
      @close="feedbackTarget = null"
      @submitted="onFeedbackSubmitted"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import LangMultiSelect from './LangMultiSelect.vue'
import FeedbackModal from './FeedbackModal.vue'
import { myTickets, ticketCreate, ticketCreateFile, ticketRun, ticketDetail, ticketDownload, ticketDelete, ticketCancel, translationEstimate } from '@/api'
import { t, tpl } from '@/i18n'
import { fmtTime } from './admin/ui'

// 创建表单与模式
// 创建模式：text=粘贴文本 / file=上传文件（支持混合类型多选：pdf+word+ppt+excel 等任意组合）
const mode = ref<'text' | 'file'>('text')
// ★ 翻译模式：fast 快速 / pro 专业校对（默认 pro；随创建请求透传）
const qualityMode = ref(localStorage.getItem('translate_mode') || 'pro')
// fmtNum 千分位格式化（报价展示用）
function fmtNum(n: number): string {
  return new Intl.NumberFormat().format(Math.max(0, Math.floor(n || 0)))
}
// ★ 反馈弹窗目标（已完成工单）
const feedbackTarget = ref<any>(null)
function openFeedback(tk: any) {
  feedbackTarget.value = { type: 'ticket', ticket_id: tk.id, mode: tk.mode || 'pro' }
}
function onFeedbackSubmitted() {
  alert(t('fb.done'))
}
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
    const r: any = await translationEstimate({ text, target_langs: [...selectedLangs.value], mode: qualityMode.value })
    if (r.success) estimate.value = r
  } catch { estimate.value = null }
}
// estimateBlocked 强制计费且额度不足/未开通时禁止提交工单
const estimateBlocked = computed(() => {
  if (!estimate.value) return false
  if (estimate.value.sufficient === false) return true
  return estimate.value.activated === false
})

// 步骤 key → 用户友好名称（中文）
const stepNames: Record<string,string> = {
  kb_match:'知识库匹配', ai_initial:'AI 初翻', evals_initial:'质量评估',
  review:'专业校对', evals_review:'校对评估', gate:'硬闸校验',
  culture_gate:'文化检查', qa:'校对', file_extract:'解析提取',
  file_translate:'初翻', approval:'审批', feedback:'反馈',
  file_qa:'校对', file_writeback:'回写文件', writeback:'回写文件',
}

// currentStepLabel 当前正在执行的步骤名（第一个 running 状态的步骤）
const currentStepLabel = computed<string>(() => {
  const st = detail.value?.states || []
  const running = st.find((x:any) => x.status==='running')
  if (running) return stepNames[running.step] || running.step
  // 文件工单：按产物就绪比例显示
  const fs = detail.value?.files || []
  if (fs.length) {
    const done = fs.filter((f:any)=>f.result_path||f.error).length
    return `已完成 ${done}/${fs.length} 个文件`
  }
  return ''
})

// ★ 工单进度百分比：文本按步骤完成数、文件按产物就绪数
const ticketProgress = computed<number | null>(() => {
  if (!detail.value) return null
  // ★ 后端唯一真源：detail.progress（列表行也带 tk.progress）
  if (typeof detail.value.progress === 'number') return detail.value.progress
  const st = detail.value.states || []
  const fs = detail.value.files || []
  const tk = detail.value.ticket || {}
  // ★ 步骤锚点阶梯：排队10 → 提取20 → 初翻40 → 校对60 → 回写80 → 完成100
  if ((tk.status || '') === 'completed') return 100
  let pct = (tk.status || '') === 'queued' ? 10 : 5
  const W: Record<string, number> = {
    upload: 20, file_extract: 20, extract: 20,
    translate: 40, file_translate: 40, init_translation: 40,
    proofread: 60, qa: 60, file_qa: 60, quality_check: 60,
    writeback: 80, file_writeback: 80, package: 80,
  }
  for (const x of st) {
    const w = W[x.step]
    if (!w) continue
    if (x.status === 'success' || x.status === 'skipped') pct = Math.max(pct, w)
    else if (x.status === 'running') pct = Math.max(pct, w - 10 > 10 ? w - 10 : w) // 运行中显示到达档位
  }
  // 任一文件已有产物/错误 → 至少进入回写档
  if (fs.some((f: any) => f.result_path || f.error)) pct = Math.max(pct, 80)
  return Math.min(100, Math.max(0, pct))
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
const detailPos = ref({ top: 0, left: 0 })
const arrowDir = ref('up')

const popoverStyle = computed(() => ({
  position: 'fixed' as const,
  top: detailPos.value.top + 'px',
  left: detailPos.value.left + 'px',
}))

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
// removeFileAt 移除单个已选文件（按索引）
function removeFileAt(i: number) {
  files.value.splice(i, 1)
}
// fmtKB 字节数转 KB 展示（保留 1 位小数，整数省略）
function fmtKB(bytes: number): string {
  const kb = bytes / 1024
  return kb >= 1024 ? (kb / 1024).toFixed(1) + 'MB' : kb.toFixed(kb % 1 ? 1 : 0) + 'KB'
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
        mode: qualityMode.value,
      })
    } else {
      if (!files.value.length) return
      // 多文件提交：混合类型（pdf/word/ppt/excel…）逐文件独立处理，产物打包下载
      r = await ticketCreateFile([...files.value], {
        title: form.value.title.trim(),
        target_langs: langsJoined.value,
        mode: qualityMode.value,
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

// cancelTicket 取消进行中的工单（确认后调用，刷新列表）
async function cancelTicket(tk: any) {
  if (!confirm(tpl('tk.cancelConfirm', { no: tk.ticket_no || tk.id }))) return
  const r = await ticketCancel(tk.id)
  if (!r.success) { alert(r.message); return }
  await load()
}

// deleteTicket 删除已完成工单及其关联文件（需确认）
async function deleteTicket(tk: any) {
  if (!confirm(tpl('tk.deleteConfirm', { no: tk.ticket_no }))) return
  const r = await ticketDelete(tk.id)
  if (!r.success) { alert(r.message); return }
  await load()
}

// 下载结果
// download 下载结果文件（blob 触发保存）
const downloadingId = ref<number | null>(null)
// download 流式下载产物（按钮带加载态：大文件耗时可见，防重复点击）
async function download(tk: any) {
  if (downloadingId.value !== null) return
  downloadingId.value = tk.id
  try { await ticketDownload(tk.id) } catch (e: any) { alert(e?.message || 'download failed') } finally { downloadingId.value = null }
}

// 展开/收起步骤进度
// 进度气泡轮询：打开期间对运行中工单每 3s 刷新详情（流式百分比）
let detailTimer: ReturnType<typeof setInterval> | undefined
function startDetailPoll() {
  stopDetailPoll()
  detailTimer = setInterval(async () => {
    if (!detail.value) { stopDetailPoll(); return }
    const id = detail.value.ticket?.id
    if (!id || document.hidden) return
    const r = await ticketDetail(id)
    if (r.success) detail.value = r
    const stt = detail.value?.ticket?.status
    if (stt && !['queued', 'in_progress'].includes(stt)) stopDetailPoll()
  }, 3000)
}
function stopDetailPoll() { if (detailTimer) { clearInterval(detailTimer); detailTimer = undefined } }
onUnmounted(stopDetailPoll)

// toggleDetail 展开/收起步骤进度气泡（优先按钮上方，空间不足则下方）
async function toggleDetail(tk: any, e?: MouseEvent) {
  if (detail.value && detail.value.ticket?.id === tk.id) { detail.value = null; stopDetailPoll(); return }
  const r = await ticketDetail(tk.id)
  if (r.success) {
    startDetailPoll()
    detail.value = r
    if (e?.target instanceof HTMLElement) {
      const rect = e.target.getBoundingClientRect()
      const popH = 200
      const gap = 4
      const left = Math.max(8, rect.left + rect.width / 2 - 130)
      if (rect.top > popH + gap) {
        detailPos.value = { top: rect.top - popH - gap, left }
        arrowDir.value = 'down'
      } else {
        detailPos.value = { top: rect.bottom + gap, left }
        arrowDir.value = 'up'
      }
    }
  }
}

// 加载我的工单；进行中每 5s 自动刷新
// load 加载我的工单；进行中的每 5s 自动刷新进度
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
    case 'cancelled': return t('tk.stCancelled')
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
.tp-backdrop { position: fixed; inset: 0; z-index: 199; background: transparent; }
.tp-popover { position: fixed; z-index: 200; min-width: 260px; max-width: 360px; }
.tp-popover-arrow { position: absolute; left: 80px; width: 12px; height: 12px; background: #fff; z-index: 201; }
.tp-popover-arrow.arr-up { top: -6px; border-left: 1px solid #d0d7de; border-top: 1px solid #d0d7de; transform: rotate(45deg); }
.tp-popover-arrow.arr-down { bottom: -6px; border-right: 1px solid #d0d7de; border-bottom: 1px solid #d0d7de; transform: rotate(45deg); }
.tp-popover-inner { background: #fff; border: 1px solid #d0d7de; border-radius: 10px; padding: 12px 14px; box-shadow: 0 4px 16px rgba(0,0,0,.12); }
.tp-popover-title { font-size: 13px; font-weight: 600; margin-bottom: 8px; color: #333; }
.tp-steps-mini { margin-top: 8px; max-height: 160px; overflow-y: auto; }
.tp-step-row { display: flex; gap: 6px; align-items: center; font-size: 12px; padding: 3px 0; }
.tp-step-name { flex: 1; color: #555; }
.tp-step-status { font-size: 11px; padding: 1px 6px; border-radius: 4px; }
.tp-step-row.st-completed .tp-step-status { background: #e6f4ea; color: #2e7d32; }
.tp-step-row.st-in_progress .tp-step-status { background: #e8f0fe; color: #1a73e8; }
.tp-step-row.st-error .tp-step-status { background: #fce8e6; color: #c5221f; }
.tp-step-err { color: #c5221f; font-size: 11px; }
.tp-steps { list-style: none; padding-left: 10px; }
.tp-steps li { padding: 3px 0; font-size: 13px; }
.tk-status-queued { color: #b8860b; }
.tk-status-in_progress { color: #1e6fd9; }
.tk-status-completed { color: #1a7f37; }
.tk-status-rejected { color: #c0392b; }
.st-success { color: #1a7f37; }
.st-failed { color: #c0392b; }
.st-running { color: #1e6fd9; }

/* ★ 已选文件平铺 chips */
.tp-chip-list { display: flex; flex-wrap: wrap; gap: 6px; margin: 8px 0 4px; align-items: center; }
.tp-file-chip { display: inline-flex; align-items: center; gap: 6px; background: #f5f7fb; border: 1px solid #d8dee6; border-radius: 14px; padding: 3px 10px; font-size: 12.5px; max-width: 320px; }
.tp-chip-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tp-chip-size { color: #999; font-size: 11.5px; }
.tp-chip-remove { border: none; background: transparent; cursor: pointer; color: #c62828; font-size: 12px; padding: 0 2px; }
.tp-chip-remove:hover { color: #e53935; }
.tp-chip-total { width: 100%; font-size: 12px; color: #888; }

/* ★ 文件清单行 */
.tp-files-detail { margin-top: 8px; }
.tp-file-row { font-size: 12.5px; padding: 3px 0; color: #444; }
.tp-file-row.err { color: #c62828; }
.tp-file-ok { color: #2e7d32; margin-left: 8px; }
.tp-file-err { margin-left: 8px; }

/* ★ 工单进度条 */
.tp-progress-bar-wrap { display: flex; align-items: center; gap: 10px; margin: 8px 0; }
.tp-progress-bar { flex: 1; height: 8px; background: #e0e0e0; border-radius: 4px; overflow: hidden; }
.tp-progress-fill { height: 100%; background: linear-gradient(90deg,#1a73e8,#4a90d9); border-radius: 4px; transition: width .6s ease; }
.tp-progress-text { font-size: 13px; font-weight: 600; color: #1a73e8; min-width: 36px; }

/* ★ 进度条 + 当前步骤名 */
.tp-progress-wrap { display: flex; align-items: center; gap: 10px; margin: 10px 0 6px; }
.tp-progress-bar { flex: 1; height: 8px; background: #e0e0e0; border-radius: 4px; overflow: hidden; }
.tp-progress-fill { height: 100%; background: linear-gradient(90deg,#1a73e8,#4a90d9); border-radius: 4px; transition: width .6s ease; }
.tp-progress-text { font-size: 14px; font-weight: 700; color: #1a73e8; min-width: 42px; }
.tp-step-label { font-size: 12.5px; color: #666; }
.tp-steps-detail summary { cursor: pointer; color: #999; font-size: 12px; margin-top: 6px; }
</style>
