<!-- ============================================================================
   components/admin/Usage.vue — 经营 · 用量看板
   职责：余额/累计用量/供应商数卡片 + 近7日趋势柱状图 + 按任务类型/供应商横向条形
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('usage.title') }}</h2>
    <button class="ad-btn" @click="loadUsage">{{ t('usage.refresh') }}</button>
    <div v-if="usageData" class="ad-cards">
      <div class="ad-card"><b>{{ usageData.balance?.balance ?? '—' }}</b><span>{{ t('usage.currentBalance') }}</span></div>
      <div class="ad-card"><b>{{ usageData.total }}</b><span>{{ t('usage.totalUsage') }}</span></div>
      <div class="ad-card"><b>{{ usageData.provider_count }}</b><span>{{ t('usage.providerCount') }}</span></div>
    </div>

    <div v-if="usageData && trendItems.length" class="ad-chart-card">
      <h3>{{ t('usage.trendTitle') }}</h3>
      <div class="ad-chart-bars">
        <div v-for="t in trendItems" :key="t.key" class="ad-chart-col" :title="tpl('usage.trendTip', { date: t.key, val: t.val })">
          <div class="ad-chart-bar" :style="{ height: barHeight(t.val, trendMax) }"></div>
          <span class="ad-chart-label">{{ t.key.slice(5) }}</span>
        </div>
      </div>
      <p class="ad-hint" style="text-align:center">{{ tpl('usage.peak', { max: trendMax }) }}</p>
    </div>

    <div v-if="usageData" class="ad-chart-grid">
      <div class="ad-chart-card">
        <h3>{{ t('usage.byTaskType') }}</h3>
        <div v-for="item in usageItems" :key="item.key" class="ad-hbar">
          <span class="ad-hbar-label">{{ item.key }}</span>
          <div class="ad-hbar-track"><div class="ad-hbar-fill" :style="{ width: barHeight(item.val, usageMax) }"></div></div>
          <span class="ad-hbar-val">{{ item.val }}</span>
        </div>
        <p v-if="!usageItems.length" class="ad-hint">{{ t('usage.noUsage') }}</p>
      </div>

      <div class="ad-chart-card">
        <h3>{{ t('usage.byProvider') }}</h3>
        <div v-for="item in providerItems" :key="item.key" class="ad-hbar">
          <span class="ad-hbar-label" :title="item.key">{{ item.short }}</span>
          <div class="ad-hbar-track"><div class="ad-hbar-fill fill-2" :style="{ width: barHeight(item.val, providerMax) }"></div></div>
          <span class="ad-hbar-val">{{ item.val }}</span>
        </div>
        <p v-if="!providerItems.length" class="ad-hint">{{ t('usage.noProviderData') }}</p>
      </div>
    </div>

    <div class="ad-chart-card">
      <h3>{{ t('usage.ledgerTitle') }}</h3>
      <table class="ad-table">
        <thead><tr><th>{{ t('usage.colTime') }}</th><th>{{ t('usage.colType') }}</th><th>{{ t('usage.colProvider') }}</th><th>{{ t('usage.colModel') }}</th><th>{{ t('usage.colQuantity') }}</th><th>{{ t('usage.colUnitPrice') }}</th><th>{{ t('usage.colCost') }}</th></tr></thead>
        <tbody>
          <tr v-for="l in usageData?.ledger || []" :key="l.id">
            <td>{{ fmtTime(l.created_at) }}</td><td>{{ l.task_type }}</td><td>{{ l.provider || '—' }}</td>
            <td>{{ l.model || '—' }}</td><td>{{ l.quantity }}</td><td>{{ l.unit_price }}</td><td>{{ l.cost }}</td>
          </tr>
          <tr v-if="!((usageData?.ledger || []).length)"><td colspan="7" style="color:#999">{{ t('usage.noLedger') }}</td></tr>
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
import { t, tpl } from '@/i18n'

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
// 趋势柱状图的最大值（保证至少为 1，避免 0 高度）
const trendMax = computed(() => Math.max(1, ...trendItems.value.map((t) => t.val)))
const usageItems = computed(() => {
  const u = usageData.value?.usage || {}
  return Object.keys(u).map((k) => ({ key: k, val: Number(u[k]) || 0 }))
})
// 任务类型条形图最大值
const usageMax = computed(() => Math.max(1, ...usageItems.value.map((t) => t.val)))
const providerItems = computed(() => {
  const p = usageData.value?.provider_usage || {}
  return Object.keys(p).map((k) => ({
    key: k,
    short: k.length > 28 ? k.slice(0, 26) + '…' : k,
    val: Number(p[k]) || 0,
  }))
})
// 供应商条形图最大值（过长的供应商名截断展示）
const providerMax = computed(() => Math.max(1, ...providerItems.value.map((t) => t.val)))

onMounted(loadUsage)
watch(activeTenantId, loadUsage)
</script>