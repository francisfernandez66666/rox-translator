<!-- ============================================================================
   components/admin/Webhooks.vue — 开放 · Webhook 回调
   职责：配置「翻译完成」事件回调 URL / 签名密钥 / 事件订阅，供客户 TMS/CI 集成
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('webhooks.title') }}</h2>
    <p class="ad-hint">{{ t('webhooks.hint') }}</p>
    <div class="ad-row">
      <input v-model="wForm.url" :placeholder="t('webhooks.urlPlaceholder')" class="ad-input" style="flex:1" />
      <input v-model="wForm.secret" :placeholder="t('webhooks.secretPlaceholder')" class="ad-input" style="width:200px" />
    </div>
    <div class="ad-row">
      <input v-model="wForm.events" :placeholder="t('webhooks.eventsPlaceholder')" class="ad-input" style="flex:1" />
      <button class="ad-btn" @click="saveWebhook">{{ t('webhooks.saveConfig') }}</button>
    </div>
    <table class="ad-table">
      <thead><tr><th>{{ t('webhooks.colId') }}</th><th>{{ t('webhooks.colUrl') }}</th><th>{{ t('webhooks.colEvents') }}</th><th>{{ t('webhooks.colStatus') }}</th><th>{{ t('webhooks.colActions') }}</th></tr></thead>
      <tbody>
        <tr v-for="w in hooks" :key="w.id">
          <td>{{ w.id }}</td><td style="word-break:break-all">{{ w.url }}</td><td>{{ w.events }}</td>
          <td>{{ w.enabled ? t('webhooks.enable') : t('webhooks.disable') }}</td>
          <td class="ad-td">
            <button class="ad-btn-sm" @click="testWebhook(w)">{{ t('webhooks.test') }}</button>
            <button class="ad-btn-sm" @click="toggleWebhook(w)">{{ w.enabled ? t('webhooks.disable') : t('webhooks.enable') }}</button>
            <button class="ad-btn-sm ad-btn-red" @click="deleteWebhook(w)">{{ t('webhooks.delete') }}</button>
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
import { t } from '@/i18n'

const hooks = ref<any[]>([])
// 新增/编辑 webhook 表单：回调 URL、签名密钥、订阅事件（逗号分隔）
const wForm = ref({ url: '', secret: '', events: 'translation.completed' })

async function loadHooks() {
  const r = await webhooks()
  if (r.success) hooks.value = (r as any).webhooks || []
}
async function saveWebhook() {
  if (!wForm.value.url) { alert(t('webhooks.urlRequired')); return }
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
  if (!confirm(t('webhooks.confirmDelete'))) return
  await webhookDelete(w.id)
  await loadHooks()
}
async function testWebhook(w: any) {
  const r = await webhookTest(w.id)
  alert(r.message || (r.success ? t('webhooks.testSent') : t('webhooks.testFailed')))
}

onMounted(loadHooks)
watch(activeTenantId, loadHooks)
</script>