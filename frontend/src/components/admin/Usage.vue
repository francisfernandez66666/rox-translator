<!-- ============================================================================
   components/admin/Usage.vue — 经营 · 用量看板
   职责：余额/累计用量/供应商数卡片 + 近7日趋势柱状图 + 按任务类型/供应商横向条形
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('usage.title') }}</h2>
    <button class="ad-btn" @click="loadAll">{{ t('usage.refresh') }}</button>

    <!-- ===== 超级管理员：全平台模型成本核算看板 ===== -->
    <div v-if="isSuper">
      <div class="ad-cards">
        <div class="ad-card"><b>{{ costTotal }}</b><span>{{ t('usage.costTotal') }}</span></div>
        <div class="ad-card"><b>{{ costKeys.length }}</b><span>{{ t('usage.modelCount') }}</span></div>
      </div>
      <div class="ad-chart-card">
        <h3>{{ t('usage.costByModel') }}</h3>
        <table class="ad-table">
          <thead><tr><th>{{ t('usage.colProvider') }}</th><th>{{ t('usage.colModel') }}</th><th>{{ t('usage.colQuantity') }}</th><th>{{ t('usage.colCost') }}</th></tr></thead>
          <tbody>
            <tr v-for="k in costKeys" :key="k">
              <td>{{ k.split(' / ')[0] }}</td><td>{{ k.split(' / ')[1] || '—' }}</td>
              <td>{{ costQuants[k] ?? 0 }}</td><td>{{ costData[k] ?? 0 }}</td>
            </tr>
            <tr v-if="!costKeys.length"><td colspan="4" style="text-align:center;color:#999">{{ t('usage.noCost') }}</td></tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- ===== 租户管理员：组织→子组织→用户下钻看板 ===== -->
    <div v-else-if="isAdmin">
      <div class="ad-chart-card">
        <h3>{{ t('usage.orgTitle') }}</h3>
        <div class="ad-hint">{{ t('usage.orgHint') }}</div>
        <div class="ad-row">
          <select v-model="selectedOrg" class="ad-input" @change="loadOrg">
            <option :value="0">{{ t('usage.allOrg') }}</option>
            <option v-for="o in orgTree" :key="o.id" :value="o.id">{{ '　'.repeat(o.depth) }} {{ o.name }}</option>
          </select>
          <span class="ad-hint">{{ tpl('usage.orgTotal', { total: orgTotal }) }}</span>
        </div>
        <table class="ad-table" style="margin-top:12px">
          <thead><tr><th>{{ t('usage.colUser') }}</th><th>{{ t('usage.colOrg') }}</th><th>{{ t('usage.colCost') }}</th></tr></thead>
          <tbody>
            <tr v-for="u in orgUsers" :key="u.id">
              <td>{{ u.display_name || u.username }}</td><td>{{ u.org_name || '—' }}</td><td>{{ u.cost }}</td>
            </tr>
            <tr v-if="!orgUsers.length"><td colspan="3" style="text-align:center;color:#999">{{ t('usage.noUsers') }}</td></tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- ===== 全部：余额/累计用量/供应商数卡片 + 趋势 + 明细（原看板保留） ===== -->
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
import { billingUsage, billingBalance, usageCost, usageOrg, orgList } from '@/api'
import { activeTenantId, isSuper, isAdmin } from './store'
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

// ===== 超管：模型成本核算 =====
const costData = ref<any>({})
const costQuants = ref<any>({})
const costKeys = computed(() => Object.keys(costData.value || {}))
const costTotal = computed(() => costKeys.value.reduce((s, k) => s + (Number(costData.value[k]) || 0), 0))
// loadCost 加载超管模型成本核算数据（各模型成本与调用量）
async function loadCost() {
  const r = await usageCost()
  if (r.success) {
    costData.value = (r as any).costs || {}
    costQuants.value = (r as any).quants || {}
  }
}

// ===== 租户管理员：组织下钻 =====
const orgTree = ref<any[]>([])
const selectedOrg = ref(0)
const orgUsers = ref<any[]>([])
const orgTotal = ref(0)
// 扁平组织列表转树形深度（用于下拉缩进展示层级）
function buildOrgTree(orgs: any[]) {
  const byParent: Record<number, any[]> = {}
  orgs.forEach((o) => { (byParent[o.parent_id] = byParent[o.parent_id] || []).push(o) })
  const out: any[] = []
  const walk = (pid: number, depth: number) => {
    (byParent[pid] || []).forEach((o) => { out.push({ ...o, depth }); walk(o.id, depth + 1) })
  }
  walk(0, 0)
  return out
}
// loadOrgs 加载组织树（组织维度用量下钻的左侧选择数据）
async function loadOrgs() {
  const r = await orgList()
  if (r.success) {
    const list = (r as any).orgs || []
    orgTree.value = buildOrgTree(list)
  }
}
// loadOrg 按所选组织加载用户级用量汇总与合计
async function loadOrg() {
  const r = await usageOrg(selectedOrg.value || undefined)
  if (r.success) {
    orgUsers.value = (r as any).users || []
    orgTotal.value = (r as any).total || 0
  }
}

// 加载全部（按角色）
async function loadAll() {
  await Promise.all([loadUsage()])
  if (isSuper.value) await loadCost()
  if (isAdmin.value && !isSuper.value) { await loadOrgs(); await loadOrg() }
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

onMounted(loadAll)
watch(activeTenantId, loadAll)
</script>