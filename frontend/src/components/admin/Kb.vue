<!-- ============================================================================
   components/admin/Kb.vue — 内容 · 行业管理（KB 包）
   职责：行业包 CRUD + 包内条目（L1术语/L2 TM/L3安全句/L4碎片）+ 批量导入
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('kb.title') }}</h2>

    <!-- ===== KB 文件上传（识别 → 导入到所选包） ===== -->
    <div class="ad-chart-card">
      <h3>{{ t('kb.uploadTitle') }}</h3>
      <div class="ad-hint">{{ t('kb.uploadHint') }}</div>
      <div class="ad-row">
        <input ref="kbFileInputRef" type="file" accept=".csv,.xlsx,.xls" class="ad-input" style="flex:1" @change="handleKbFileSelect" />
        <button class="ad-btn" @click="startRecognize" :disabled="!kbFile || kbRecognizing">
          {{ kbRecognizing ? t('kb.recognizing') : t('kb.recognize') }}
        </button>
      </div>
      <div v-if="kbRecognized" class="kb-upload-result">
        <div class="kb-upload-stat">
          {{ tpl('kb.kbTotal', { total: kbRecognized.total, n: kbRecognized.lang_cols?.length || 0 }) }}
          <span v-if="kbRecognized.new_langs?.length" class="kb-new-langs">{{ t('kb.kbNewLangs') }}<span v-for="l in kbRecognized.new_langs" :key="l" class="kb-lang-tag">{{ l }}</span></span>
        </div>
        <div class="ad-row">
          <select v-model="kbImportPkg" class="ad-input">
            <option :value="0" disabled>{{ t('kb.selectPkg') }}</option>
            <option v-for="p in packages" :key="p.id" :value="p.id">[{{ p.pack_type }}] {{ p.name }}</option>
          </select>
          <button class="ad-btn ad-btn-green" @click="startImport" :disabled="!kbImportPkg || kbImporting">{{ t('kb.import') }}</button>
        </div>
        <div v-if="kbImportResult" class="kb-import-result" :class="kbImportResult.success ? 'ok' : 'err'">{{ kbImportResult.message }}</div>
      </div>
      <!-- 双语语料对齐导入：直接写入翻译记忆库（TM），冷启动提升命中率 -->
      <div class="ad-row" style="margin-top:8px;border-top:1px dashed #e0e0e0;padding-top:10px">
        <input ref="bitextInputRef" type="file" accept=".csv,.xlsx,.xls" class="ad-input" style="flex:1" @change="handleBitextSelect" />
        <button class="ad-btn" @click="startBitextImport" :disabled="!bitextFile || bitextImporting">
          {{ bitextImporting ? t('kb.bitextImporting') : t('kb.bitextImport') }}
        </button>
        <!-- TMX 标准格式导入 -->
        <input ref="tmxInputRef" type="file" accept=".tmx,.xml" class="ad-input" style="flex:1;margin-left:8px" @change="handleTmxSelect" />
        <button class="ad-btn" @click="startTmxImport" :disabled="!tmxFile || tmxImporting">
          {{ tmxImporting ? t('kb.tmxImporting') : t('kb.tmxImport') }}
        </button>
      </div>
      <div v-if="bitextMsg" class="kb-import-result" :class="bitextOk ? 'ok' : 'err'">{{ bitextMsg }}</div>
      <div v-if="tmxMsg" class="kb-import-result" :class="tmxOk ? 'ok' : 'err'">{{ tmxMsg }}</div>
    </div>

    <div class="ad-row">
      <input v-model="pForm.code" :placeholder="t('kb.codePlaceholder')" class="ad-input" />
      <input v-model="pForm.name" :placeholder="t('kb.namePlaceholder')" class="ad-input" />
      <select v-model="pForm.pack_type" class="ad-input">
        <option v-for="opt in packTypeOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
      </select>
      <button class="ad-btn" @click="createPackage">{{ t('kb.createPackage') }}</button>
    </div>
    <div class="ad-row" style="justify-content:space-between">
      <div class="ad-hint" style="margin:0">{{ t('kb.entriesHint') }}</div>
      <button v-if="isSuper" class="ad-btn-sm" :disabled="rebuilding" @click="rebuildIndex">{{ rebuilding ? t('kb.rebuilding') : t('kb.rebuildIndex') }}</button>
    </div>
    <div class="ad-hint">{{ t('kb.entriesHint') }}</div>
    <div v-for="p in packages" :key="p.id" class="ad-pkg">
      <div class="ad-pkg-head" :style="p.enabled === 0 ? 'opacity:.55' : ''">
        <b>[{{ p.pack_type }}] {{ p.name }}</b>
        <span class="ad-pkg-role">{{ p.role }}</span>
        <button class="ad-btn-sm" @click="togglePackage(p)">{{ p.enabled === 0 ? t('kb.enablePack') : t('kb.disablePack') }}</button>
        <button class="ad-btn-sm ad-btn-red" @click="removePackage(p)">{{ t('kb.deletePackage') }}</button>
        <button class="ad-btn-sm" @click="loadEntries(p)">{{ tpl('kb.viewEntries', { count: entryCount(p.id) }) }}</button>
      </div>
      <div v-if="selectedPkg === p.id" class="ad-pkg-entries">
        <div class="ad-row">
          <input v-model="eForm.source_text" :placeholder="t('kb.sourcePlaceholder')" class="ad-input" />
          <select v-model="eForm.layer" class="ad-input">
            <option :value="1">{{ t('kb.layer1') }}</option><option :value="2">{{ t('kb.layer2') }}</option><option :value="3">{{ t('kb.layer3') }}</option><option :value="4">{{ t('kb.layer4') }}</option>
          </select>
          <input v-model="eForm.target_lang" :placeholder="t('kb.targetLangPlaceholder')" class="ad-input ad-mini-w" />
          <input v-model="eForm.target_text" :placeholder="t('kb.translationPlaceholder')" class="ad-input" />
          <button class="ad-btn" @click="addEntry(p.id)">{{ t('kb.add') }}</button>
        </div>
        <details class="ad-bulk">
          <summary>{{ t('kb.bulkImportSummary') }}</summary>
          <textarea v-model="bulkText" class="ad-input ad-wide ad-textarea" :placeholder="t('kb.bulkPlaceholder')" />
          <button class="ad-btn" @click="bulkImport(p.id)">{{ t('kb.bulkImport') }}</button>
          <span class="ad-hint" style="margin-left: 10px">{{ bulkMsg }}</span>
        </details>
        <table class="ad-table">
          <thead><tr><th>{{ t('kb.colId') }}</th><th>{{ t('kb.colLayer') }}</th><th>{{ t('kb.colSource') }}</th><th>{{ t('kb.colLang') }}</th><th>{{ t('kb.colTranslation') }}</th><th></th></tr></thead>
          <tbody>
            <tr v-for="e in entries" :key="e.id">
              <td>{{ e.id }}</td><td>L{{ e.layer }}</td><td>{{ e.source_text }}</td>
              <td>{{ e.target_lang }}</td><td>{{ e.target_text }}</td>
              <td><button class="ad-btn-sm ad-btn-red" @click="removeEntry(e)">{{ t('kb.delete') }}</button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  <!-- ===== 语言文化规范（安全句 / Gate 闸门） ===== -->
    <div class="ad-chart-card" style="margin-top:16px">
      <h3>{{ t('kb.safetyTitle') }}</h3>
      <div class="ad-hint">{{ t('kb.safetyHint') }}</div>

      <!-- 语言文化包选择 + 状态过滤 -->
      <div class="ad-row" style="margin-bottom:8px">
        <select v-model="safetyPkgId" class="ad-input">
          <option v-for="p in localePackages" :key="p.id" :value="p.id">[{{ p.pack_type }}] {{ p.name }}</option>
        </select>
        <select v-model="safetyStatusFilter" class="ad-input ad-mini-w" @change="loadSafety">
          <option value="">{{ t('kb.allStatus') }}</option>
          <option value="pending">{{ t('kb.pending') }}</option>
          <option value="approved">{{ t('kb.approved') }}</option>
          <option value="rejected">{{ t('kb.rejected') }}</option>
        </select>
        <span class="ad-hint">{{ t('kb.safetyScopeHint') }}</span>
      </div>

      <!-- 结构化录入 -->
      <div class="ad-row" style="margin-bottom:8px">
        <select v-model="sf.lang" class="ad-input ad-mini-w">
          <option value="en">en</option><option value="ar">ar</option><option value="de">de</option>
          <option value="es">es</option><option value="fr">fr</option><option value="id_lang">id</option>
          <option value="kk">kk</option><option value="pt">pt</option><option value="ru">ru</option>
          <option value="th">th</option><option value="tr">tr</option><option value="zh_hant">zh-Hant</option>
        </select>
        <select v-model="sf.kind" class="ad-input ad-mini-w">
          <option value="style">{{ t('kb.kindStyle') }}</option>
          <option value="forbidden">{{ t('kb.kindForbidden') }}</option>
          <option value="replace">{{ t('kb.kindReplace') }}</option>
        </select>
        <input v-model="sf.phrase" :placeholder="phrasePlaceholder" class="ad-input" style="flex:1" />
        <input v-if="sf.kind === 'replace'" v-model="sf.replacement" :placeholder="t('kb.replacementPlaceholder')" class="ad-input" style="flex:1" />
        <button class="ad-btn ad-btn-green" :disabled="!safetyPkgId || !sf.phrase.trim()" @click="addSafety">{{ t('users.create') }}</button>
      </div>

      <!-- LLM 投喂批量导入 -->
      <div class="ad-row" style="margin-bottom:8px">
        <input v-model="bulkJson" :placeholder="t('kb.bulkPlaceholder')" class="ad-input" style="flex:1" />
        <button class="ad-btn" :disabled="!safetyPkgId || !bulkJson.trim()" @click="importSafety">{{ t('kb.bulkImport') }}</button>
      </div>

      <!-- 列表 -->
      <table class="ad-table">
        <thead><tr>
          <th>{{ t('org.colLang') }}</th><th>{{ t('kb.colKind') }}</th><th>{{ t('kb.colRule') }}</th>
          <th v-if="hasReplace">{{ t('kb.colReplacement') }}</th>
          <th>{{ t('kb.colStatus') }}</th><th>{{ t('kb.colSource') }}</th><th>{{ t('org.colActions') }}</th>
        </tr></thead>
        <tbody>
          <tr v-for="sp in filteredSafety" :key="sp.id">
            <td>{{ sp.lang }}</td>
            <td>{{ kindLabel(sp.kind) }}</td>
            <td style="max-width:280px;word-break:break-all">{{ sp.phrase }}</td>
            <td v-if="hasReplace">{{ sp.kind === 'replace' ? (sp.replacement || '—') : '' }}</td>
            <td><span :class="'kb-status-' + (sp.status || 'approved')">{{ statusLabel(sp.status) }}</span></td>
            <td>{{ sp.source === 'llm' ? 'LLM' : t('kb.srcManual') }}</td>
            <td class="ad-td">
              <button v-if="(sp.status || 'approved') !== 'approved'" class="ad-btn-sm" @click="setSafetyStatus(sp, 'approved')">{{ t('kb.approve') }}</button>
              <button v-if="(sp.status || 'approved') === 'pending'" class="ad-btn-sm ad-btn-red" @click="setSafetyStatus(sp, 'rejected')">{{ t('kb.reject') }}</button>
              <button class="ad-btn-sm ad-btn-red" @click="removeSafety(sp)">✕</button>
            </td>
          </tr>
          <tr v-if="!filteredSafety.length"><td colspan="7" class="ad-empty">{{ t('kb.noSafety') }}</td></tr>
        </tbody>
      </table>
    </div>

  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { kbPackages, kbPackageCreate, kbPackageDelete, kbPackageStatus, kbIndexRebuild, safetyPhrases, safetyPhraseAdd, safetyPhraseDelete, safetyPhraseStatus, safetyBulkImport, kbEntries, kbEntryAdd, kbEntryDelete, kbEntriesImport, kbRecognizeFile, kbImportFile, bitextImport, tmxImport } from '@/api'
import { activeTenantId, isSuper, myLevel } from './store'
import { t, tpl } from '@/i18n'

const packages = ref<any[]>([])
const entries = ref<any[]>([])
const selectedPkg = ref<number | null>(null)
// 新建知识库包表单：编码/名称/包类型（industry/enterprise 等）
const pForm = ref({ code: '', name: '', pack_type: 'industry' })
// 新建词条表单：源文本/分层/目标语言/目标文本/所属模块
const eForm = ref({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' })
const entriesMap = ref<Record<number, number>>({})

// ---- KB 文件上传状态 ----
const kbFileInputRef = ref<HTMLInputElement>()
const kbFile = ref<File | null>(null)
const kbRecognizing = ref(false)
const kbRecognized = ref<any>(null)
const kbImportPkg = ref(0)
const kbImporting = ref(false)
const kbImportResult = ref<any>(null)

// 可选的包类型：超管可建全部；租户管理员仅企业包/部门包；部门管理员仅部门包
const packTypeOptions = computed(() => {
  if (myLevel.value <= 2) {
    return [{ value: 'department', label: t('kb.typeDepartment') }]
  }
  const base = [
    { value: 'tenant', label: t('kb.typeTenant') },
    { value: 'department', label: t('kb.typeDepartment') },
  ]
  if (isSuper.value) {
    base.push({ value: 'industry', label: t('kb.typeIndustry') })
    base.push({ value: 'locale', label: t('kb.typeLocale') })
  }
  return base
})

// entryCount 查询指定知识库包的条目数量（来自 entriesMap 预加载计数）。
// 参数 id: 知识库包 ID；返回: 条目数（未加载时为 0）。
function entryCount(id: number) { return entriesMap.value[id] || 0 }
// 加载全部行业包及其条目计数
async function loadPackages() {
  const r = await kbPackages()
  if (r.success) {
    packages.value = (r as any).packages
    entriesMap.value = {}
    for (const p of packages.value) {
      const e = await kbEntries(p.id)
      entriesMap.value[p.id] = (e as any).entries?.length || 0
    }
  }
}
// createPackage 校验编码/名称后创建知识库包（源语言角色）并刷新列表
async function createPackage() {
  if (!pForm.value.code || !pForm.value.name) { alert(t('kb.errorCodeNameRequired')); return }
  const r = await kbPackageCreate({ ...pForm.value, role: 'source' })
  if (!r.success) { alert(r.message); return }
  pForm.value = { code: '', name: '', pack_type: 'industry' }
  await loadPackages()
}
// ---- 语言文化规范（安全句 / Gate）----
const safetyPkgId = ref<number>(0)
const safetyStatusFilter = ref('')
const safetyList = ref<any[]>([])
const bulkJson = ref('')
// 录入表单：语言/类型/内容/替换词
const sf = ref({ lang: 'en', kind: 'style', phrase: '', replacement: '' })

// 语言文化包列表（仅 pack_type=locale）
const localePackages = computed(() => packages.value.filter((p: any) => p.pack_type === 'locale'))
// 是否存在替换对条目（控制表格替换词列显隐）
const hasReplace = computed(() => filteredSafety.value.some((s: any) => s.kind === 'replace'))
// 按所选状态过滤后的安全句（并限定所选语言文化包）
const filteredSafety = computed(() =>
  safetyList.value.filter((s: any) => (!safetyPkgId.value || s.package_id === safetyPkgId.value))
)

// 加载安全句列表
async function loadSafety() {
  const r = await safetyPhrases()
  if (r.success) {
    safetyList.value = (r as any).phrases || []
    if (!safetyPkgId.value && localePackages.value.length) safetyPkgId.value = localePackages.value[0].id
  }
}

// 新增结构化安全句
async function addSafety() {
  if (!safetyPkgId.value || !sf.value.phrase.trim()) return
  const r = await safetyPhraseAdd({
    package_id: safetyPkgId.value,
    lang: sf.value.lang,
    phrase: sf.value.phrase.trim(),
    kind: sf.value.kind,
    replacement: sf.value.kind === 'replace' ? sf.value.replacement.trim() : '',
  })
  if (!r.success) { alert(r.message); return }
  sf.value.phrase = ''
  sf.value.replacement = ''
  await loadSafety()
}

// 审核：通过/驳回
async function setSafetyStatus(sp: any, status: string) {
  const r = await safetyPhraseStatus(sp.id, status)
  if (!r.success) { alert(r.message); return }
  await loadSafety()
}

// 删除安全句
async function removeSafety(sp: any) {
  if (!confirm(t('kb.deleteConfirm'))) return
  const r = await safetyPhraseDelete(sp.id)
  if (!r.success) { alert(r.message); return }
  await loadSafety()
}

// LLM 投喂批量导入：粘贴 JSON 数组 [{lang,phrase,kind,replacement}]
async function importSafety() {
  let items: any[]
  try {
    items = JSON.parse(bulkJson.value)
  } catch { alert(t('kb.bulkInvalid')); return }
  if (!Array.isArray(items) || !items.length) { alert(t('kb.bulkInvalid')); return }
  const r = await safetyBulkImport(safetyPkgId.value, items)
  if (!r.success) { alert(r.message); return }
  alert(tpl('kb.bulkDone', { n: (r as any).added ?? 0 }))
  bulkJson.value = ''
  await loadSafety()
}

// 类型/状态的中文标签
function kindLabel(k?: string) {
  return k === 'forbidden' ? t('kb.kindForbidden') : k === 'replace' ? t('kb.kindReplace') : t('kb.kindStyle')
}
// statusLabel 安全句审核状态转中文标签（pending/rejected/approved）
function statusLabel(s?: string) {
  return s === 'pending' ? t('kb.pending') : s === 'rejected' ? t('kb.rejected') : t('kb.approved')
}
// 录入占位符随类型变化
const phrasePlaceholder = computed(() => {
  if (sf.value.kind === 'forbidden') return t('kb.phraseForbidden')
  if (sf.value.kind === 'replace') return t('kb.phraseReplace')
  return t('kb.phraseStyle')
})

// 重建向量索引（超管）：全量重新嵌入并热替换
const rebuilding = ref(false)
// rebuildIndex 弹确认后重建向量索引（超管），完成后提示嵌入条数
async function rebuildIndex() {
  if (!confirm(t('kb.rebuildConfirm'))) return
  rebuilding.value = true
  try {
    const r = await kbIndexRebuild()
    if (!r.success) { alert(r.message); return }
    alert(tpl('kb.rebuildDone', { n: (r as any).embedded ?? 0 }))
  } finally {
    rebuilding.value = false
  }
}
// 启用/停用包：停用后不参与翻译命中（条目保留，可随时启用）
async function togglePackage(p: any) {
  const next = p.enabled === 0 ? 1 : 0
  const r = await kbPackageStatus(p.id, next)
  if (!r.success) { alert(r.message); return }
  await loadPackages()
}
// removePackage 弹确认后删除知识库包并刷新列表
async function removePackage(p: any) {
  if (!confirm(tpl('kb.confirmDeletePackage', { name: p.name }))) return
  const r = await kbPackageDelete(p.id)
  if (!r.success) alert(r.message)
  await loadPackages()
}
// loadEntries 展开/收起包条目面板，展开时加载该包条目列表
async function loadEntries(p: any) {
  selectedPkg.value = selectedPkg.value === p.id ? null : p.id
  if (selectedPkg.value === p.id) {
    const r = await kbEntries(p.id)
    entries.value = (r as any).entries || []
  }
}
// addEntry 校验原文后向指定包新增词条，清空表单并刷新条目与计数
async function addEntry(pkgId: number) {
  if (!eForm.value.source_text) { alert(t('kb.errorSourceRequired')); return }
  const r = await kbEntryAdd({ package_id: pkgId, ...eForm.value })
  if (!r.success) { alert(r.message); return }
  eForm.value = { source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' }
  await loadEntries({ id: pkgId })
  await loadPackages()
}
// removeEntry 删除指定词条并刷新当前展开包的条目列表
async function removeEntry(e: any) {
  const r = await kbEntryDelete(e.id)
  if (!r.success) alert(r.message)
  const p = packages.value.find((x) => x.id === selectedPkg.value)
  if (p) await loadEntries(p)
}

// 批量导入：每行 "中文|语言|译文"（可含层号：中文|en|译文|2）
const bulkText = ref('')
// 批量导入结果提示（成功/失败信息）
const bulkMsg = ref('')
// bulkImport 按行解析"中文|语言|译文"文本批量导入词条，展示成功/跳过计数
async function bulkImport(pkgId: number) {
  const entries: any[] = []
  for (const line of bulkText.value.split('\n')) {
    const parts = line.split('|').map((s) => s.trim())
    if (parts.length < 3 || !parts[0]) continue
    entries.push({
      source_text: parts[0],
      target_lang: parts[1],
      target_text: parts[2],
      layer: parts.length >= 4 && Number(parts[3]) ? Number(parts[3]) : 2,
    })
  }
  if (!entries.length) { alert(t('kb.errorNoValidLine')); return }
  const r = await kbEntriesImport({ package_id: pkgId, entries })
  if (!r.success) { alert(r.message); return }
  bulkMsg.value = tpl('kb.bulkResult', { added: r.added as number, skipped: r.skipped as number })
  bulkText.value = ''
  await loadEntries({ id: pkgId })
  await loadPackages()
}

onMounted(() => { loadPackages(); loadSafety() })
watch(activeTenantId, loadPackages)

// ---- KB 文件上传：识别 → 导入到所选包 ----

// handleKbFileSelect 选择文件后清除上次识别/导入结果。
function handleKbFileSelect(e: Event) {
  const target = e.target as HTMLInputElement
  kbFile.value = target.files?.[0] || null
  kbRecognized.value = null
  kbImportResult.value = null
  kbImportPkg.value = 0
}

// ---- 双语语料对齐导入：选择文件 → 直接写入 TM 库（module=bitext）----
const bitextFile = ref<File | null>(null)
const bitextInputRef = ref<HTMLInputElement | null>(null)
const bitextImporting = ref(false)
const bitextMsg = ref('')
const bitextOk = ref(false)
// handleBitextSelect 记录用户选择的双语对照文件
function handleBitextSelect(e: Event) {
  bitextFile.value = (e.target as HTMLInputElement).files?.[0] || null
  bitextMsg.value = ''
}
// startBitextImport 上传并导入双语语料，展示写入/跳过计数
async function startBitextImport() {
  if (!bitextFile.value) return
  bitextImporting.value = true
  try {
    const r = await bitextImport(bitextFile.value)
    bitextOk.value = !!r.success
    bitextMsg.value = r.success
      ? `${t('kb.bitextDone')} +${r.added ?? 0} / ${t('kb.bitextSkipped')} ${r.skipped ?? 0}`
      : r.message || '导入失败'
    if (r.success) {
      bitextFile.value = null
      if (bitextInputRef.value) bitextInputRef.value.value = ''
    }
  } finally {
    bitextImporting.value = false
  }
}

// ---- TMX 标准格式导入：解析 tu/tuv → 写入 TM 库（module=tmx）----
const tmxFile = ref<File | null>(null)
const tmxInputRef = ref<HTMLInputElement | null>(null)
const tmxImporting = ref(false)
const tmxMsg = ref('')
const tmxOk = ref(false)
// handleTmxSelect 记录用户选择的 TMX 文件
function handleTmxSelect(e: Event) {
  tmxFile.value = (e.target as HTMLInputElement).files?.[0] || null
  tmxMsg.value = ''
}
// startTmxImport 上传并导入 TMX，展示单元/写入/跳过计数
async function startTmxImport() {
  if (!tmxFile.value) return
  tmxImporting.value = true
  try {
    const r = await tmxImport(tmxFile.value)
    tmxOk.value = !!r.success
    tmxMsg.value = r.success
      ? `${tpl('kb.tmxTus', { n: (r.tus as number) ?? 0 })} · ${t('kb.bitextDone')} +${(r.added as number) ?? 0} / ${t('kb.bitextSkipped')} ${(r.skipped as number) ?? 0}`
      : r.message || '导入失败'
    if (r.success) {
      tmxFile.value = null
      if (tmxInputRef.value) tmxInputRef.value.value = ''
    }
  } finally {
    tmxImporting.value = false
  }
}

// startRecognize 识别上传文件：调用 recognize-kb，返回预览/语言列/temp_id。
async function startRecognize() {
  if (!kbFile.value) return
  kbRecognizing.value = true
  kbImportResult.value = null
  try {
    const r = await kbRecognizeFile(kbFile.value)
    if (r.success) {
      kbRecognized.value = r
    } else {
      alert(r.message || t('kb.recognizeFailed'))
    }
  } catch (err: any) {
    alert(tpl('kb.recognizeErr', { msg: err.message || t('kb.networkErr') }))
  } finally {
    kbRecognizing.value = false
  }
}

// startImport 导入已识别的数据到所选包。
async function startImport() {
  if (!kbRecognized.value?.temp_id || !kbImportPkg.value) return
  kbImporting.value = true
  kbImportResult.value = null
  try {
    const r = await kbImportFile({ temp_id: kbRecognized.value.temp_id, package_id: kbImportPkg.value })
    kbImportResult.value = r
    if (r.success) {
      await loadPackages()
      kbImportPkg.value = 0
    }
  } catch (err: any) {
    kbImportResult.value = { success: false, message: tpl('kb.importErr', { msg: err.message || t('kb.networkErr') }) }
  } finally {
    kbImporting.value = false
  }
}
</script>