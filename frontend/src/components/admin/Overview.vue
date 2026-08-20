<!-- ============================================================================
   components/admin/Overview.vue — 经营 · 系统看板
   职责：系统健康状态卡片（KB/余额/流程/熔断/错误率）+ 最近审计日志 + 导出 CSV
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>系统看板</h2>
    <button class="ad-btn" @click="loadDash">刷新</button>
    <button class="ad-btn ad-btn-green" style="margin-left: 8px" @click="exportAuditCSV">导出审计 CSV</button>
    <button class="ad-btn" style="margin-left: 8px" @click="openMetrics">📊 Prometheus 指标</button>
    <div v-if="health" class="ad-cards">
      <div class="ad-card"><b>{{ health.kb_entries }}</b><span>知识库条目</span></div>
      <div class="ad-card"><b>{{ health.balance?.balance }}</b><span>租户余额 (token)</span></div>
      <div class="ad-card"><b>{{ health.flow_steps_enabled }}/{{ health.flow_steps_total }}</b><span>流程步骤启用</span></div>
      <div class="ad-card"><b>{{ health.usage ? Object.keys(health.usage).length : 0 }}</b><span>用量类型</span></div>
      <div class="ad-card"><b>{{ health.breaker_open ? '🔴 熔断' : '🟢 正常' }}</b><span>主模型状态</span></div>
      <div class="ad-card"><b>{{ health.llm_error_rate }}</b><span>LLM 错误率</span></div>
    </div>
    <div v-if="audit && audit.length" class="ad-audit">
      <h3>最近审计日志</h3>
      <table class="ad-table">
        <thead><tr><th>时间</th><th>操作</th><th>资源</th><th>详情</th><th>变更轨迹</th></tr></thead>
        <tbody>
          <tr v-for="l in audit" :key="l.id">
            <td>{{ fmtTime(l.created_at) }}</td><td>{{ l.action }}</td><td>{{ l.resource }}</td><td>{{ l.detail }}</td>
            <td class="ad-td">
              <span v-if="l.before_val && l.after_val" class="ad-diff">旧 {{ shortJSON(l.before_val) }} → 新 {{ shortJSON(l.after_val) }}</span>
              <span v-else>—</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { systemHealth, systemAudit, API_BASE, getAuthToken, getActiveTenantId } from '@/api'
import { activeTenantId } from './store'
import { fmtTime, shortJSON } from './ui'

// 系统健康数据 / 审计日志
const health = ref<any>(null)
const audit = ref<any[]>([])
// 加载系统看板数据（健康状态 + 审计日志）
async function loadDash() {
  const h = await systemHealth()
  if (h.success) health.value = (h as any).health
  const a = await systemAudit()
  if (a.success) audit.value = (a as any).logs
}

// 审计 CSV 导出：直接以当前租户身份下载（后端返回 UTF-8 BOM）
function exportAuditCSV() {
  const url = `${API_BASE}/api/system/audit?export=csv`
  const xhr = new XMLHttpRequest()
  xhr.open('GET', url, true)
  if (getAuthToken()) xhr.setRequestHeader('Authorization', `Bearer ${getAuthToken()}`)
  if (getActiveTenantId() > 0) xhr.setRequestHeader('X-Tenant-ID', String(getActiveTenantId()))
  xhr.responseType = 'blob'
  xhr.onload = () => {
    if (xhr.status !== 200) { alert('导出失败'); return }
    const a = document.createElement('a')
    a.href = URL.createObjectURL(xhr.response)
    a.download = `audit_${new Date().toISOString().slice(0, 10)}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
  }
  xhr.send()
}

// 打开 Prometheus 指标页
function openMetrics() {
  window.open(`${API_BASE}/metrics`, '_blank')
}

// 挂载与租户切换时加载数据
onMounted(loadDash)
watch(activeTenantId, loadDash)
</script>