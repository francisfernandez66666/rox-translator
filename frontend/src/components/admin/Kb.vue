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
    </div>

    <div class="ad-row">
      <input v-model="pForm.code" :placeholder="t('kb.codePlaceholder')" class="ad-input" />
      <input v-model="pForm.name" :placeholder="t('kb.namePlaceholder')" class="ad-input" />
      <select v-model="pForm.pack_type" class="ad-input">
        <option v-for="opt in packTypeOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
      </select>
      <button class="ad-btn" @click="createPackage">{{ t('kb.createPackage') }}</button>
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
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { kbPackages, kbPackageCreate, kbPackageDelete, kbPackageStatus, kbEntries, kbEntryAdd, kbEntryDelete, kbEntriesImport, kbRecognizeFile, kbImportFile } from '@/api'
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
async function createPackage() {
  if (!pForm.value.code || !pForm.value.name) { alert(t('kb.errorCodeNameRequired')); return }
  const r = await kbPackageCreate({ ...pForm.value, role: 'source' })
  if (!r.success) { alert(r.message); return }
  pForm.value = { code: '', name: '', pack_type: 'industry' }
  await loadPackages()
}
// 启用/停用包：停用后不参与翻译命中（条目保留，可随时启用）
async function togglePackage(p: any) {
  const next = p.enabled === 0 ? 1 : 0
  const r = await kbPackageStatus(p.id, next)
  if (!r.success) { alert(r.message); return }
  await loadPackages()
}
async function removePackage(p: any) {
  if (!confirm(tpl('kb.confirmDeletePackage', { name: p.name }))) return
  const r = await kbPackageDelete(p.id)
  if (!r.success) alert(r.message)
  await loadPackages()
}
async function loadEntries(p: any) {
  selectedPkg.value = selectedPkg.value === p.id ? null : p.id
  if (selectedPkg.value === p.id) {
    const r = await kbEntries(p.id)
    entries.value = (r as any).entries || []
  }
}
async function addEntry(pkgId: number) {
  if (!eForm.value.source_text) { alert(t('kb.errorSourceRequired')); return }
  const r = await kbEntryAdd({ package_id: pkgId, ...eForm.value })
  if (!r.success) { alert(r.message); return }
  eForm.value = { source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' }
  await loadEntries({ id: pkgId })
  await loadPackages()
}
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
  bulkMsg.value = tpl('kb.bulkResult', { added: r.added, skipped: r.skipped })
  bulkText.value = ''
  await loadEntries({ id: pkgId })
  await loadPackages()
}

onMounted(loadPackages)
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