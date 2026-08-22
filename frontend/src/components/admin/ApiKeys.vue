<!-- ============================================================================
   components/admin/ApiKeys.vue — 开放 · 开放 API Key
   职责：API Key 签发/启停/轮换/删除 + 开放 API 文档入口
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('apikeys.title') }}</h2>
    <div class="ad-row">
      <a class="ad-btn" href="#" @click.prevent="openDocs">📄 {{ t('apikeys.docs') }}</a>
    </div>
    <div class="ad-row">
      <input v-model="kForm.name" :placeholder="t('apikeys.keyName')" class="ad-input" />
      <select v-model="kForm.perms" class="ad-input">
        <option value="translate">translate</option><option value="kb">kb</option><option value="all">all</option>
      </select>
      <button class="ad-btn" @click="createKey">{{ t('apikeys.create') }}</button>
    </div>
    <div v-if="newKey" class="ad-newkey">{{ t('apikeys.newKeyOnce') }}：<code>{{ newKey }}</code></div>
    <table class="ad-table">
      <thead><tr><th>{{ t('apikeys.colId') }}</th><th>{{ t('apikeys.colPrefix') }}</th><th>{{ t('apikeys.colName') }}</th><th>{{ t('apikeys.colPerms') }}</th><th>{{ t('apikeys.colStatus') }}</th><th>{{ t('apikeys.colCalls') }}</th><th>{{ t('apikeys.colActions') }}</th></tr></thead>
      <tbody>
        <tr v-for="k in keys" :key="k.id">
          <td>{{ k.id }}</td><td>{{ k.key_prefix }}…</td><td>{{ k.name }}</td><td>{{ k.perms }}</td>
          <td>{{ k.status }}</td><td>{{ k.call_count }}</td>
          <td class="ad-td">
            <button class="ad-btn-sm" @click="toggleKey(k)">{{ k.status === 'active' ? t('apikeys.disable') : t('apikeys.enable') }}</button>
            <button class="ad-btn-sm" @click="rotateKey(k)">{{ t('apikeys.rotate') }}</button>
            <button class="ad-btn-sm ad-btn-red" @click="deleteKey(k)">{{ t('apikeys.delete') }}</button>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { apiKeys, apiKeyCreate, apiKeyStatus, apiKeyDelete, apiKeyRotate, openAPIDocsUrl } from '@/api'
import { activeTenantId } from './store'
import { t, tpl } from '@/i18n'

const keys = ref<any[]>([])
// 新建成功后展示一次的完整 API Key（仅创建时返回）
const newKey = ref('')
// 新建 Key 表单：名称/权限（默认 translate）
const kForm = ref({ name: '', perms: 'translate' })

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
  kForm.value = { name: '', perms: 'translate' }
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

onMounted(loadKeys)
watch(activeTenantId, loadKeys)
</script>