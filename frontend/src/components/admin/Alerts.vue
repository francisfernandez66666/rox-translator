<!-- ============================================================================
   components/admin/Alerts.vue — 经营 · 监控告警
   职责：按状态过滤查看告警列表（余额阈值/熔断/错误率），一键解决告警
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('alerts.title') }}</h2>
    <div class="ad-row">
      <button class="ad-btn" @click="loadAlerts">{{ t('alerts.refresh') }}</button>
      <select v-model="alertStatus" class="ad-input" @change="loadAlerts">
        <option value="">{{ t('alerts.all') }}</option><option value="open">{{ t('alerts.open') }}</option><option value="resolved">{{ t('alerts.resolved') }}</option>
      </select>
    </div>
    <table class="ad-table">
      <thead><tr><th>{{ t('alerts.colLevel') }}</th><th>{{ t('alerts.colKind') }}</th><th>{{ t('alerts.colTenant') }}</th><th>{{ t('alerts.colContent') }}</th><th>{{ t('alerts.colStatus') }}</th><th>{{ t('alerts.colTime') }}</th><th></th></tr></thead>
      <tbody>
        <tr v-for="a in alerts" :key="a.id">
          <td>{{ a.level }}</td><td>{{ a.kind }}</td><td>#{{ a.tenant_id }}</td><td>{{ a.message }}</td>
          <td>{{ a.status === 'open' ? t('alerts.open') : t('alerts.resolved') }}</td><td>{{ fmtTime(a.created_at) }}</td>
          <td><button v-if="a.status === 'open'" class="ad-btn-sm" @click="resolveAlert(a)">{{ t('alerts.close') }}</button></td>
        </tr>
        <tr v-if="!alerts.length"><td colspan="7" style="text-align:center;color:#999">{{ t('alerts.empty') }}</td></tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { systemAlerts, alertResolve } from '@/api'
import { activeTenantId } from './store'
import { fmtTime } from './ui'
import { t } from '@/i18n'

const alerts = ref<any[]>([])
// 告警状态过滤条件（空=全部，open=未解决，resolved=已解决）
const alertStatus = ref('')
// 按状态加载告警列表
async function loadAlerts() {
  const r = await systemAlerts(alertStatus.value || undefined)
  if (r.success) alerts.value = (r as any).alerts || []
}
// 解决告警并刷新
async function resolveAlert(a: any) {
  await alertResolve(a.id)
  await loadAlerts()
}

onMounted(loadAlerts)
watch(activeTenantId, loadAlerts)
</script>