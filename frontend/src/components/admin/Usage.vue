<!-- ============================================================================
   components/admin/Usage.vue — 经营 · 用量看板
   职责：余额/累计用量/供应商数卡片 + 近7日趋势柱状图 + 按任务类型/供应商横向条形
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>用量看板</h2>
    <button class="ad-btn" @click="loadUsage">刷新</button>
    <div v-if="usageData" class="ad-cards">
      <div class="ad-card"><b>{{ usageData.balance?.balance ?? '—' }}</b><span>当前余额 (token)</span></div>
      <div class="ad-card"><b>{{ usageData.total }}</b><span>累计用量 (token)</span></div>
      <div class="ad-card"><b>{{ usageData.provider_count }}</b><span>使用供应商数</span></div>
    </div>

    <div v-if="usageData && trendItems.length" class="ad-chart-card">
      <h3>近 7 日用量趋势</h3>
      <div class="ad-chart-bars">
        <div v-for="t in trendItems" :key="t.key" class="ad-chart-col" :title="`${t.key}: ${t.val} token`">
          <div class="ad-chart-bar" :style="{ height: barHeight(t.val, trendMax) }"></div>
          <span class="ad-chart-label">{{ t.key.slice(5) }}</span>
        </div>
      </div>
      <p class="ad-hint" style="text-align:center">峰值 {{ trendMax }} token</p>
    </div>

    <div v-if="usageData" class="ad-chart-grid">
      <div class="ad-chart-card">
        <h3>按任务类型</h3>
        <div v-for="item in usageItems" :key="item.key" class="ad-hbar">
          <span class="ad-hbar-label">{{ item.key }}</span>
          <div class="ad-hbar-track"><div class="ad-hbar-fill" :style="{ width: barHeight(item.val, usageMax) }"></div></div>
          <span class="ad-hbar-val">{{ item.val }}</span>
        </div>
        <p v-if="!usageItems.length" class="ad-hint">暂无用量</p>
      </div>

      <div class="ad-chart-card">
        <h3>按供应商/模型（成本核算）</h3>
        <div v-for="item in providerItems" :key="item.key" class="ad-hbar">
          <span class="ad-hbar-label" :title="item.key">{{ item.short }}</span>
          <div class="ad-hbar-track"><div class="ad-hbar-fill fill-2" :style="{ width: barHeight(item.val, providerMax) }"></div></div>
          <span class="ad-hbar-val">{{ item.val }}</span>
        </div>
        <p v-if="!providerItems.length" class="ad-hint">暂无供应商数据</p>
      </div>
    </div>

    <div class="ad-chart-card">
      <h3>用量明细</h3>
      <table class="ad-table">
        <thead><tr><th>时间</th><th>类型</th><th>供应商</th><th>模型</th><th>量</th><th>单价</th><th>消耗</th></tr></thead>
        <tbody>
          <tr v-for="l in usageData?.ledger || []" :key="l.id">
            <td>{{ fmtTime(l.created_at) }}</td><td>{{ l.task_type }}</td><td>{{ l.provider || '—' }}</td>
            <td>{{ l.model || '—' }}</td><td>{{ l.quantity }}</td><td>{{ l.unit_price }}</td><td>{{ l.cost }}</td>
          </tr>
          <tr v-if="!((usageData?.ledger || []).length)"><td colspan="7" style="color:#999">暂无明细</td></tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { billingUsage, billingBalance } from '@/api'
import { activeTenantId } from './store'
import { fmtTime, barHeight } from './ui'

const usageData = ref<any>(null)
// 加载用量看板数据（用量 + 余额，并统计供应商数）
async function loadUsage() {
  const [u, b] = await Promise.all([billingUsage(), billingBalance()])
  if (u.success) {
    const du = u as any
    usageData.value = {
      ...du,
      balance: b.success ? (b as any).balance : null,
      provider_count: Object.keys(du.provider_usage || {}).length,
    }
  }
}

// 用量图表数据（纯 CSS 条形图，零依赖）
const trendItems = computed(() => {
  const trend = usageData.value?.trend || {}
  const keys = Object.keys(trend).sort()
  return keys.map((k) => ({ key: k, val: Number(trend[k]) || 0 }))
})
const trendMax = computed(() => Math.max(1, ...trendItems.value.map((t) => t.val)))
const usageItems = computed(() => {
  const u = usageData.value?.usage || {}
  return Object.keys(u).map((k) => ({ key: k, val: Number(u[k]) || 0 }))
})
const usageMax = computed(() => Math.max(1, ...usageItems.value.map((t) => t.val)))
const providerItems = computed(() => {
  const p = usageData.value?.provider_usage || {}
  return Object.keys(p).map((k) => ({
    key: k,
    short: k.length > 28 ? k.slice(0, 26) + '…' : k,
    val: Number(p[k]) || 0,
  }))
})
const providerMax = computed(() => Math.max(1, ...providerItems.value.map((t) => t.val)))

onMounted(loadUsage)
watch(activeTenantId, loadUsage)
</script>