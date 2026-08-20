<!-- ============================================================================
   components/admin/ApiKeys.vue — 开放 · 开放 API Key
   职责：API Key 签发/启停/轮换/删除 + 开放 API 文档入口
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>开放 API Key</h2>
    <div class="ad-row">
      <a class="ad-btn" href="#" @click.prevent="openDocs">📄 查看 API 文档</a>
    </div>
    <div class="ad-row">
      <input v-model="kForm.name" placeholder="Key 名称" class="ad-input" />
      <select v-model="kForm.perms" class="ad-input">
        <option value="translate">translate</option><option value="kb">kb</option><option value="all">all</option>
      </select>
      <button class="ad-btn" @click="createKey">签发 Key</button>
    </div>
    <div v-if="newKey" class="ad-newkey">新 Key（仅显示一次）：<code>{{ newKey }}</code></div>
    <table class="ad-table">
      <thead><tr><th>ID</th><th>前缀</th><th>名称</th><th>权限</th><th>状态</th><th>调用次数</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="k in keys" :key="k.id">
          <td>{{ k.id }}</td><td>{{ k.key_prefix }}…</td><td>{{ k.name }}</td><td>{{ k.perms }}</td>
          <td>{{ k.status }}</td><td>{{ k.call_count }}</td>
          <td class="ad-td">
            <button class="ad-btn-sm" @click="toggleKey(k)">{{ k.status === 'active' ? '停用' : '启用' }}</button>
            <button class="ad-btn-sm" @click="rotateKey(k)">轮换</button>
            <button class="ad-btn-sm ad-btn-red" @click="deleteKey(k)">删除</button>
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

const keys = ref<any[]>([])
const newKey = ref('')
const kForm = ref({ name: '', perms: 'translate' })

async function loadKeys() {
  const r = await apiKeys()
  if (r.success) keys.value = (r as any).keys || []
}
async function createKey() {
  if (!kForm.value.name) { alert('名称必填'); return }
  const r = await apiKeyCreate(kForm.value)
  if (!r.success) { alert(r.message); return }
  newKey.value = (r as any).api_key || ''
  kForm.value = { name: '', perms: 'translate' }
  await loadKeys()
}
async function toggleKey(k: any) {
  await apiKeyStatus(k.id, k.status === 'active' ? 'disabled' : 'active')
  await loadKeys()
}
async function deleteKey(k: any) {
  if (!confirm('删除该 API Key？')) return
  await apiKeyDelete(k.id)
  await loadKeys()
}
async function rotateKey(k: any) {
  if (!confirm(`确认轮换「${k.name}」？旧 Key 将立即失效。`)) return
  const r = await apiKeyRotate(k.id)
  if (!r.success) { alert(r.message); return }
  newKey.value = (r as any).api_key || ''
  await loadKeys()
}
function openDocs() {
  window.open(openAPIDocsUrl(), '_blank')
}

onMounted(loadKeys)
watch(activeTenantId, loadKeys)
</script>