<!-- ============================================================================
   components/admin/ApiKeys.vue — 开放 · 开放 API Key
   职责：API Key 签发/启停/轮换/删除 + 开放 API 文档入口
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('apikeys.title') }}</h2>
    <div class="ad-row">
      <a class="ad-btn" href="#" @click.prevent="openDocs">📄 {{ t('apikeys.docs') }}</a>
      <button v-if="isSuper" class="ad-btn" style="margin-left:8px" @click="docsCardOpen = !docsCardOpen">{{ docsCardOpen ? '▲' : '▼' }} {{ t('docsEdit.title') }}</button>
    </div>

    <!-- ★ API 文档在线维护（Markdown 源码编辑，仅超管） -->
    <div v-if="isSuper && docsCardOpen" class="ad-chart-card" style="margin-top:12px">
      <h3>{{ t('docsEdit.title') }}</h3>
      <div class="ad-hint">{{ t('docsEdit.hint') }}</div>
      <!-- 语言 Tab -->
      <div style="display:flex;gap:6px;margin-bottom:8px">
        <button :class="['ad-btn-sm', docsLang==='zh' ? 'on' : '']" @click="docsLang='zh'" :style="docsLang==='zh' ? 'background:#1a73e8;color:#fff' : ''">中文</button>
        <button :class="['ad-btn-sm', docsLang==='en' ? 'on' : '']" @click="docsLang='en'" :style="docsLang==='en' ? 'background:#1a73e8;color:#fff' : ''">English</button>
      </div>
      <textarea
        v-model="docsMD"
        class="ad-input docs-editor"
        rows="16"
        spellcheck="false"
        :placeholder="t('docsEdit.placeholder')"
      ></textarea>
      <div class="ad-row" style="margin-top:8px">
        <button class="ad-btn ad-btn-green" :disabled="docsSaving || !docsMD.trim()" @click="saveDocs">💾 {{ docsSaving ? t('docsEdit.saving') : t('common.save') }}</button>
        <button class="ad-btn" :disabled="!docsMD.trim()" @click="previewDocs">👁 {{ t('docsEdit.preview') }}</button>
        <label class="ad-btn" style="cursor:pointer">
          📂 {{ t('docsEdit.import') }}
          <input type="file" accept=".md,.markdown,.txt" hidden @change="importDocs" />
        </label>
        <button class="ad-btn" :disabled="!docsMD.trim()" @click="exportDocs">⬇️ {{ t('docsEdit.export') }}</button>
        <button class="ad-btn ad-btn-red" @click="resetDocs">↺ {{ t('docsEdit.reset') }}</button>
        <span v-if="docsDefaultBadge" class="ad-hint">{{ t('docsEdit.isDefault') }}</span>
      </div>
    </div>
    <div class="ad-row">
      <input v-model="kForm.name" :placeholder="t('apikeys.keyName')" class="ad-input" />
      <select v-model="kForm.perms" class="ad-input">
        <option value="translate">translate</option><option value="kb">kb</option><option value="all">all</option>
      </select>
      <!-- ★ R4 Key 级每日配额（0/留空=不限） -->
      <input v-model.number="kForm.daily_call_limit" type="number" min="0" class="ad-input" style="width:110px" :placeholder="t('apikeys.limitPlaceholder')" :title="t('apikeys.limitTitle')" />
      <button class="ad-btn" @click="createKey">{{ t('apikeys.create') }}</button>
    </div>
    <div v-if="newKey" class="ad-newkey">{{ t('apikeys.newKeyOnce') }}：<code>{{ newKey }}</code>
      <!-- ★ 一键复制（clipboard API，失败降级提示手选复制） -->
      <button class="ad-btn-sm" style="margin-left:8px" @click="copyNewKey">📋 {{ t('apikeys.copy') }}</button>
      <span v-if="copied" class="ad-hint">{{ t('apikeys.copied') }}</span>
    </div>
    <table class="ad-table">
      <thead><tr><th>{{ t('apikeys.colId') }}</th><th>{{ t('apikeys.colPrefix') }}</th><th>{{ t('apikeys.colName') }}</th><th>{{ t('apikeys.colPerms') }}</th><th>{{ t('apikeys.colStatus') }}</th><th>{{ t('apikeys.colCalls') }}</th><th>{{ t('apikeys.colTodayLimit') }}</th><th>{{ t('apikeys.colActions') }}</th></tr></thead>
      <tbody>
        <tr v-for="k in keys" :key="k.id">
          <td>{{ k.id }}</td><td>{{ k.key_prefix }}…</td><td>{{ k.name }}</td><td>{{ k.perms }}</td>
          <td>{{ k.status }}</td>
          <td :style="{ color: isKeyOverQuota(k) ? '#c62828' : '' }">{{ fmtToday(k) }}</td>
          <td class="ad-td">
            <button class="ad-btn-sm" @click="toggleKey(k)">{{ k.status === 'active' ? t('apikeys.disable') : t('apikeys.enable') }}</button>
            <button class="ad-btn-sm" @click="rotateKey(k)">{{ t('apikeys.rotate') }}</button>
            <button class="ad-btn-sm" @click="copyKey(k)" :title="t('apikeys.copy')">📋</button>
            <button class="ad-btn-sm" @click="setLimit(k)">📐 {{ t('apikeys.setLimit') }}</button>
            <button class="ad-btn-sm ad-btn-red" @click="deleteKey(k)">{{ t('apikeys.delete') }}</button>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { isSuper } from './store'
import { apiKeys, apiKeyCreate, apiKeyStatus, apiKeyDelete, apiKeyRotate, openAPIDocsUrl, getOpenAPIDocs, saveOpenAPIDocs, previewOpenAPIDocs, apiKeyLimit, apiKeyReveal } from '@/api'
import { activeTenantId } from './store'
import { t, tpl } from '@/i18n'

const keys = ref<any[]>([])
// 新建成功后展示一次的完整 API Key（仅创建时返回）
const newKey = ref('')
// 新建 Key 表单：名称/权限（默认 translate）
const kForm = ref({ name: '', perms: 'translate', daily_call_limit: undefined as number | undefined })

// copied 复制成功提示状态
const copied = ref(false)

// copyNewKey 一键复制新签发的明文 Key
async function copyNewKey() {
  try {
    await navigator.clipboard.writeText(newKey.value)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  } catch {
    alert(t('apikeys.copyFail'))
  }
}

// copiedId 当前已复制的 Key 行 ID（2 秒后恢复）
const copiedId = ref(0)

// copyKey 解密取回明文并直接写入剪贴板（不展示明文，满足“只能复制不能看”）
async function copyKey(k: any) {
  try {
    const r: any = await apiKeyReveal(k.id)
    if (!r.success) { alert(r.message); return }
    await navigator.clipboard.writeText(r.api_key || '')
    copiedId.value = k.id
    setTimeout(() => (copiedId.value = 0), 2000)
  } catch (e) {
    alert(e instanceof Error ? e.message : String(e))
  }
}

// loadKeys 加载 API Key 列表
async function loadKeys() {
  const r = await apiKeys()
  if (r.success) keys.value = (r as any).keys || []
}
// createKey 校验名称后签发 API Key，展示一次性完整密钥并重置表单、刷新列表
async function createKey() {
  if (!kForm.value.name) { alert(t('apikeys.nameRequired')); return }
  const r = await apiKeyCreate(kForm.value)
  if (!r.success) { alert(r.message); return }
  newKey.value = (r as any).api_key || ''
  kForm.value = { name: '', perms: 'translate', daily_call_limit: undefined }
  await loadKeys()
}
// toggleKey 切换指定 Key 启用/禁用状态并刷新列表
async function toggleKey(k: any) {
  await apiKeyStatus(k.id, k.status === 'active' ? 'disabled' : 'active')
  await loadKeys()
}
// deleteKey 弹确认后删除指定 Key 并刷新列表
async function deleteKey(k: any) {
  if (!confirm(t('apikeys.confirmDelete'))) return
  await apiKeyDelete(k.id)
  await loadKeys()
}
// rotateKey 弹确认后轮换指定 Key，展示新密钥并刷新列表
async function rotateKey(k: any) {
  if (!confirm(tpl('apikeys.confirmRotate', { name: k.name }))) return
  const r = await apiKeyRotate(k.id)
  if (!r.success) { alert(r.message); return }
  newKey.value = (r as any).api_key || ''
  await loadKeys()
}

// openDocs 新窗口打开开放 API 文档页（/openapi/docs）。
// 无参数无返回，由「查看文档」按钮触发。
function openDocs() {
  window.open(openAPIDocsUrl(), '_blank')
}

// ---- ★ API 文档在线维护（仅超管；MD 源码存 system_config，公开页实时生效）----
const docsCardOpen = ref(false)
const docsLang = ref<'zh'|'en'>('zh')
const docsMDZh = ref('')
const docsMDEn = ref('')
const docsSaving = ref(false)
const docsDefaultBadge = ref(false)
const docsLoaded = ref(false)

// 当前 Tab 对应的 MD 内容（textarea 双向绑定）
const docsMD = computed({
  get: () => docsLang.value === 'en' ? docsMDEn.value : docsMDZh.value,
  set: (v: string) => { if (docsLang.value === 'en') docsMDEn.value = v; else docsMDZh.value = v },
})

// loadDocs 拉取双语文档（卡片首次展开时加载一次）
async function loadDocs() {
  if (docsLoaded.value) return
  try {
    const r: any = await getOpenAPIDocs()
    if (r.success) {
      docsMDZh.value = r.md_zh || ''
      docsMDEn.value = r.md_en || ''
      docsDefaultBadge.value = !!r.default_zh && !!r.default_en
      docsLoaded.value = true
    }
  } catch { /* 非超管或网络失败：静默 */ }
}
// 卡片展开时触发加载（watch 由模板切换驱动）
import { watch as vueWatch } from 'vue'
vueWatch(docsCardOpen, (v) => { if (v) loadDocs() })

// saveDocs 保存 MD 源码并发布（空串=恢复内置默认）
async function saveDocs() {
  if (!confirm(t('docsEdit.confirmSave'))) return
  docsSaving.value = true
  try {
    const r = await saveOpenAPIDocs({ lang: docsLang.value, md: docsMD.value })
    if (!r.success) { alert(r.message); return }
    alert(t('docsEdit.saved'))
    await refreshDocsState()
  } finally { docsSaving.value = false }
}

// previewDocs 后端渲染预览（新窗口写 HTML，隔离样式）
async function previewDocs() {
  try {
    const r: any = await previewOpenAPIDocs({ lang: docsLang.value, md: docsMD.value })
    if (!r.success) { alert(r.message); return }
    const w = window.open('', '_blank')
    if (w) { w.document.open(); w.document.write(r.html); w.document.close() }
  } catch (e) { alert(e instanceof Error ? e.message : String(e)) }
}

// importDocs 本地 .md 导入编辑框（FileReader 纯前端）
function importDocs(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  const reader = new FileReader()
  reader.onload = () => { docsMD.value = String(reader.result || '') }
  reader.readAsText(file)
  ;(e.target as HTMLInputElement).value = ''
}

// exportDocs 导出当前源码为 .md
function exportDocs() {
  const blob = new Blob([docsMD.value], { type: 'text/markdown;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = 'openapi-docs.md'
  a.click()
  URL.revokeObjectURL(a.href)
}

// isKeyOverQuota 今日次数是否已达上限（红色警示）
function isKeyOverQuota(k: any): boolean {
  if (!k.daily_call_limit || k.daily_call_limit <= 0) return false
  const today = new Date().toISOString().slice(0, 10)
  const used = k.calls_today_date === today ? k.calls_today : 0
  return used >= k.daily_call_limit
}
// fmtToday 「今日/上限」单元格展示
function fmtToday(k: any): string {
  const limit = k.daily_call_limit && k.daily_call_limit > 0 ? k.daily_call_limit : '∞'
  const today = new Date().toISOString().slice(0, 10)
  const used = k.calls_today_date === today ? k.calls_today : 0
  return `${used}/${limit}`
}

// setLimit 设置指定 Key 的每日调用上限
async function setLimit(k: any) {
  const input = prompt(tpl('apikeys.limitPrompt', { name: k.name, cur: k.daily_call_limit || 0 }))
  if (input === null) return
  const n = Number(input)
  if (!Number.isFinite(n) || n < 0) { alert(t('apikeys.limitInvalid')); return }
  const r = await apiKeyLimit(k.id, Math.floor(n))
  if (!r.success) { alert(r.message); return }
  await loadKeys()
}

// resetDocs 恢复内置默认（清空存储键）
async function resetDocs() {
  if (!confirm(t('docsEdit.confirmReset'))) return
  const r = await saveOpenAPIDocs({ lang: docsLang.value, md: '' })
  if (!r.success) { alert(r.message); return }
  docsMD.value = ''
  await refreshDocsState()
  alert(t('docsEdit.resetDone'))
}

// refreshDocsState 重读服务端状态（保存/重置后同步徽标）
async function refreshDocsState() {
  docsLoaded.value = false
  await loadDocs()
}

onMounted(loadKeys)
watch(activeTenantId, loadKeys)
</script>
<style scoped>
/* ★ API 文档 MD 编辑器 */
.docs-editor { width: 100%; box-sizing: border-box; font-family: SFMono-Regular, Consolas, 'Courier New', monospace; font-size: 12.5px; line-height: 1.55; resize: vertical; }
</style>
