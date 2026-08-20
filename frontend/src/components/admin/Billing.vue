<!-- ============================================================================
   components/admin/Billing.vue — 经营 · 计费管理
   职责：计费开关（超管）+ 配额设置 + 充值订单（超管）+ 发票管理
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>计费管理</h2>

    <!-- 计费配置（超管） -->
    <div v-if="isSuper" class="ad-chart-card">
      <h3>计费配置</h3>
      <div class="ad-hint">开启强制计费后，所有翻译请求按用量扣减租户余额，余额不足将被拒绝服务。</div>
      <label class="ad-switch" style="margin-right: 14px">
        <input type="checkbox" v-model="billingEnforced" @change="saveBillingConfig" />
        <span></span>
      </label>
      <span :style="{ color: billingEnforced ? '#2e7d32' : '#888', fontWeight: 600 }">{{ billingEnforced ? '强制计费已开启' : '强制计费已关闭（仅记录用量不扣费）' }}</span>
    </div>

    <!-- 配额设置 -->
    <div class="ad-chart-card">
      <h3>配额设置（当前租户）</h3>
      <div class="ad-hint">QPS 每秒请求上限、并发同时翻译任务数、每日累计字符上限（0=不限）。</div>
      <div class="ad-row">
        <input v-model.number="quotaForm.qps" type="number" placeholder="QPS" class="ad-input ad-mini-w" />
        <input v-model.number="quotaForm.concurrent" type="number" placeholder="并发" class="ad-input ad-mini-w" />
        <input v-model.number="quotaForm.max_daily_chars" type="number" placeholder="每日字符上限" class="ad-input" />
        <button class="ad-btn" @click="saveQuota">保存配额</button>
      </div>
      <p class="ad-hint">当前：QPS {{ quotaForm.qps }}，并发 {{ quotaForm.concurrent }}，每日上限 {{ quotaForm.max_daily_chars }} 字符</p>
    </div>

    <!-- 充值管理（超管） -->
    <div v-if="isSuper" class="ad-chart-card">
      <h3>充值管理</h3>
      <div class="ad-row">
        <input v-model="oForm.tokens" type="number" placeholder="token 数量" class="ad-input" />
        <input v-model.number="oForm.money" type="number" step="0.01" placeholder="金额 (元)" class="ad-input" />
        <button class="ad-btn" @click="createOrder">创建充值订单</button>
      </div>
      <table class="ad-table">
        <thead><tr><th>单号</th><th>tokens</th><th>金额</th><th>状态</th><th>时间</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="o in orders" :key="o.id">
            <td>{{ o.order_no }}</td><td>{{ o.amount_tokens }}</td><td>{{ o.amount_money }} 元</td>
            <td>{{ o.status }}</td><td>{{ fmtTime(o.created_at) }}</td>
            <td class="ad-td">
              <button v-if="o.status === 'pending'" class="ad-btn-sm ad-btn-green" @click="payOrder(o)">确认收款</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 发票管理 -->
    <div class="ad-chart-card">
      <h3>发票管理</h3>
      <div class="ad-hint">仅可对本租户已支付订单开票。开票后金额不可修改。</div>
      <table class="ad-table">
        <thead><tr><th>发票号</th><th>关联订单</th><th>抬头</th><th>税号</th><th>金额(元)</th><th>开票时间</th></tr></thead>
        <tbody>
          <tr v-for="inv in invoices" :key="inv.id">
            <td>{{ inv.invoice_no }}</td><td>{{ inv.order_no }}</td><td>{{ inv.title }}</td><td>{{ inv.tax_no }}</td>
            <td>{{ inv.amount_money }}</td><td>{{ fmtTime(inv.created_at) }}</td>
          </tr>
          <tr v-if="!invoices.length"><td colspan="6" style="text-align:center;color:#999">暂无发票</td></tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { billingConfig, billingConfigSave, billingQuota, billingQuotaSave, billingOrders, adminOrderCreate, adminOrderPay, billingInvoices } from '@/api'
import { activeTenantId, isSuper } from './store'
import { fmtTime } from './ui'

// 计费配置（是否强制计费）
const billingEnforced = ref(false)
async function loadBillingConfig() {
  const r = await billingConfig()
  if (r.success) billingEnforced.value = (r as any).billing_enforced === true
}
async function saveBillingConfig() {
  const r = await billingConfigSave({ billing_enforced: billingEnforced.value })
  if (!r.success) { alert(r.message); billingEnforced.value = !billingEnforced.value }
}

// 配额设置
const quotaForm = ref({ qps: 10, concurrent: 3, max_daily_chars: 0 })
async function loadQuota() {
  const r = await billingQuota()
  if (r.success) {
    quotaForm.value = {
      qps: (r as any).qps || 10,
      concurrent: (r as any).concurrent || 3,
      max_daily_chars: (r as any).max_daily_chars || 0,
    }
  }
}
async function saveQuota() {
  const r = await billingQuotaSave({
    qps: Math.max(1, quotaForm.value.qps),
    concurrent: Math.max(1, quotaForm.value.concurrent),
    max_daily_chars: Math.max(0, quotaForm.value.max_daily_chars),
  })
  if (!r.success) { alert(r.message); return }
  alert('配额已保存')
  await loadQuota()
}

// 充值订单
const orders = ref<any[]>([])
const oForm = ref({ tokens: 1000, money: 0 })
async function loadOrders() {
  const r = await billingOrders()
  if (r.success) orders.value = (r as any).orders || []
}
async function createOrder() {
  const r = await adminOrderCreate({ tenant_id: 1, tokens: Number(oForm.value.tokens), money: oForm.value.money })
  if (!r.success) { alert(r.message); return }
  await loadOrders()
}
async function payOrder(o: any) {
  const r = await adminOrderPay(o.id)
  if (!r.success) { alert(r.message); return }
  await loadOrders()
}

// 发票
const invoices = ref<any[]>([])
async function loadInvoices() {
  const r = await billingInvoices()
  if (r.success) invoices.value = (r as any).invoices || []
}

// 挂载与租户切换时加载
async function loadAll() {
  if (isSuper.value) await Promise.all([loadBillingConfig(), loadOrders()])
  await Promise.all([loadQuota(), loadInvoices()])
}
onMounted(loadAll)
watch(activeTenantId, loadAll)
</script>