<!-- ============================================================================
   components/admin/Webhooks.vue — 开放 · Webhook 回调
   职责：配置「翻译完成」事件回调 URL / 签名密钥 / 事件订阅，供客户 TMS/CI 集成
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>Webhook 回调</h2>
    <p class="ad-hint">翻译完成后自动 POST 到回调 URL（携带 X-Signature 签名），失败自动重试 3 次。</p>
    <div class="ad-row">
      <input v-model="wForm.url" placeholder="回调 URL，如 https://example.com/webhook" class="ad-input" style="flex:1" />
      <input v-model="wForm.secret" placeholder="签名密钥（可留空）" class="ad-input" style="width:200px" />
    </div>
    <div class="ad-row">
      <input v-model="wForm.events" placeholder="订阅事件（默认 translation.completed）" class="ad-input" style="flex:1" />
      <button class="ad-btn" @click="saveWebhook">保存配置</button>
    </div>
    <table class="ad-table">
      <thead><tr><th>ID</th><th>URL</th><th>事件</th><th>状态</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="w in hooks" :key="w.id">
          <td>{{ w.id }}</td><td style="word-break:break-all">{{ w.url }}</td><td>{{ w.events }}</td>
          <td>{{ w.enabled ? '启用' : '停用' }}</td>
          <td class="ad-td">
            <button class="ad-btn-sm" @click="testWebhook(w)">测试</button>
            <button class="ad-btn-sm" @click="toggleWebhook(w)">{{ w.enabled ? '停用' : '启用' }}</button>
            <button class="ad-btn-sm ad-btn-red" @click="deleteWebhook(w)">删除</button>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { webhooks, webhookSave, webhookDelete, webhookTest } from '@/api'
import { activeTenantId } from './store'

const hooks = ref<any[]>([])
const wForm = ref({ url: '', secret: '', events: 'translation.completed' })

async function loadHooks() {
  const r = await webhooks()
  if (r.success) hooks.value = (r as any).webhooks || []
}
async function saveWebhook() {
  if (!wForm.value.url) { alert('回调 URL 必填'); return }
  const r = await webhookSave({ url: wForm.value.url, secret: wForm.value.secret, events: wForm.value.events })
  if (!r.success) { alert(r.message); return }
  wForm.value = { url: '', secret: '', events: 'translation.completed' }
  await loadHooks()
}
async function toggleWebhook(w: any) {
  await webhookSave({ id: w.id, url: w.url, secret: w.secret, events: w.events, enabled: w.enabled ? 0 : 1 })
  await loadHooks()
}
async function deleteWebhook(w: any) {
  if (!confirm('删除该 Webhook 配置？')) return
  await webhookDelete(w.id)
  await loadHooks()
}
async function testWebhook(w: any) {
  const r = await webhookTest(w.id)
  alert(r.message || (r.success ? '已发送' : '发送失败'))
}

onMounted(loadHooks)
watch(activeTenantId, loadHooks)
</script>