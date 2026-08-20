<!-- ============================================================================
   components/admin/Kb.vue — 内容 · 行业管理（KB 包）
   职责：行业包 CRUD + 包内条目（L1术语/L2 TM/L3安全句/L4碎片）+ 批量导入
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>行业管理（KB 包）</h2>
    <div class="ad-row">
      <input v-model="pForm.code" placeholder="编码 (如 auto)" class="ad-input" />
      <input v-model="pForm.name" placeholder="名称 (如 汽车行业包)" class="ad-input" />
      <select v-model="pForm.pack_type" class="ad-input">
        <option value="tenant">企业包</option><option value="industry">行业包</option><option value="locale">语言文化包</option>
      </select>
      <button class="ad-btn" @click="createPackage">创建包</button>
    </div>
    <div class="ad-hint">包内条目: 层 L1术语 / L2 TM / L3 安全句 / L4 碎片；源=中文，目标语言代码 (en/de/fr…)</div>
    <div v-for="p in packages" :key="p.id" class="ad-pkg">
      <div class="ad-pkg-head">
        <b>[{{ p.pack_type }}] {{ p.name }}</b>
        <span class="ad-pkg-role">{{ p.role }}</span>
        <button class="ad-btn-sm ad-btn-red" @click="removePackage(p)">删除包</button>
        <button class="ad-btn-sm" @click="loadEntries(p)">查看条目 ({{ entryCount(p.id) }})</button>
      </div>
      <div v-if="selectedPkg === p.id" class="ad-pkg-entries">
        <div class="ad-row">
          <input v-model="eForm.source_text" placeholder="中文源句" class="ad-input" />
          <select v-model="eForm.layer" class="ad-input">
            <option :value="1">L1 术语</option><option :value="2">L2 TM</option><option :value="3">L3 安全句</option><option :value="4">L4 碎片</option>
          </select>
          <input v-model="eForm.target_lang" placeholder="目标语言 (en)" class="ad-input ad-mini-w" />
          <input v-model="eForm.target_text" placeholder="译文" class="ad-input" />
          <button class="ad-btn" @click="addEntry(p.id)">添加</button>
        </div>
        <details class="ad-bulk">
          <summary>批量导入（每行一条：中文|语言|译文，可多行）</summary>
          <textarea v-model="bulkText" class="ad-input ad-wide ad-textarea" placeholder="制动系统|en|Brake system&#10;点火开关|en|Ignition switch" />
          <button class="ad-btn" @click="bulkImport(p.id)">批量导入</button>
          <span class="ad-hint" style="margin-left: 10px">{{ bulkMsg }}</span>
        </details>
        <table class="ad-table">
          <thead><tr><th>ID</th><th>层</th><th>源句</th><th>语言</th><th>译文</th><th></th></tr></thead>
          <tbody>
            <tr v-for="e in entries" :key="e.id">
              <td>{{ e.id }}</td><td>L{{ e.layer }}</td><td>{{ e.source_text }}</td>
              <td>{{ e.target_lang }}</td><td>{{ e.target_text }}</td>
              <td><button class="ad-btn-sm ad-btn-red" @click="removeEntry(e)">删</button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { kbPackages, kbPackageCreate, kbPackageDelete, kbEntries, kbEntryAdd, kbEntryDelete, kbEntriesImport } from '@/api'
import { activeTenantId } from './store'

const packages = ref<any[]>([])
const entries = ref<any[]>([])
const selectedPkg = ref<number | null>(null)
// 新建知识库包表单：编码/名称/包类型（industry/enterprise 等）
const pForm = ref({ code: '', name: '', pack_type: 'industry' })
// 新建词条表单：源文本/分层/目标语言/目标文本/所属模块
const eForm = ref({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' })
const entriesMap = ref<Record<number, number>>({})

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
  if (!pForm.value.code || !pForm.value.name) { alert('编码和名称必填'); return }
  const r = await kbPackageCreate({ ...pForm.value, role: 'source' })
  if (!r.success) { alert(r.message); return }
  pForm.value = { code: '', name: '', pack_type: 'industry' }
  await loadPackages()
}
async function removePackage(p: any) {
  if (!confirm(`删除包「${p.name}」及全部条目？`)) return
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
  if (!eForm.value.source_text) { alert('源句必填'); return }
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
  if (!entries.length) { alert('没有有效行（每行：中文|语言|译文）'); return }
  const r = await kbEntriesImport({ package_id: pkgId, entries })
  if (!r.success) { alert(r.message); return }
  bulkMsg.value = `✅ 导入 ${r.added} 条，跳过 ${r.skipped} 条`
  bulkText.value = ''
  await loadEntries({ id: pkgId })
  await loadPackages()
}

onMounted(loadPackages)
watch(activeTenantId, loadPackages)
</script>