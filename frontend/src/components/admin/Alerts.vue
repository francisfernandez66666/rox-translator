<!-- ============================================================================
   components/admin/Alerts.vue — 经营 · 监控告警
   职责：按状态过滤查看告警列表（余额阈值/熔断/错误率），一键解决告警
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>监控告警</h2>
    <div class="ad-row">
      <button class="ad-btn" @click="loadAlerts">刷新</button>
      <select v-model="alertStatus" class="ad-input" @change="loadAlerts">
        <option value="">全部</option><option value="open">未处理</option><option value="resolved">已解决</option>
      </select>
    </div>
    <table class="ad-table">
      <thead><tr><th>级别</th><th>类型</th><th>租户</th><th>内容</th><th>状态</th><th>时间</th><th></th></tr></thead>
      <tbody>
        <tr v-for="a in alerts" :key="a.id">
          <td>{{ a.level }}</td><td>{{ a.kind }}</td><td>#{{ a.tenant_id }}</td><td>{{ a.message }}</td>
          <td>{{ a.status === 'open' ? '未处理' : '已解决' }}</td><td>{{ fmtTime(a.created_at) }}</td>
          <td><button v-if="a.status === 'open'" class="ad-btn-sm" @click="resolveAlert(a)">关闭</button></td>
        </tr>
        <tr v-if="!alerts.length"><td colspan="7" style="text-align:center;color:#999">暂无告警</td></tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { systemAlerts, alertResolve } from '@/api'
import { activeTenantId } from './store'
import { fmtTime } from './ui'

const alerts = ref<any[]>([])
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