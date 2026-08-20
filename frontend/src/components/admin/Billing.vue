<!-- ============================================================================
   components/admin/Billing.vue — 经营 · 计费管理
   职责：计费开关（超管）+ 配额设置 + 充值订单（超管）+ 发票管理
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('billing.title') }}</h2>

    <!-- 计费配置（超管） -->
    <div v-if="isSuper" class="ad-chart-card">
      <h3>{{ t('billing.config') }}</h3>
      <div class="ad-hint">{{ t('billing.configHint') }}</div>
      <label class="ad-switch" style="margin-right: 14px">
        <input type="checkbox" v-model="billingEnforced" @change="saveBillingConfig" />
        <span></span>
      </label>
      <span :style="{ color: billingEnforced ? '#2e7d32' : '#888', fontWeight: 600 }">{{ billingEnforced ? t('billing.enforcedOn') : t('billing.enforcedOff') }}</span>
    </div>

    <!-- 配额设置 -->
    <div class="ad-chart-card">
      <h3>{{ t('billing.quotaTitle') }}</h3>
      <div class="ad-hint">{{ t('billing.quotaHint') }}</div>
      <div class="ad-row">
        <input v-model.number="quotaForm.qps" type="number" :placeholder="t('billing.quotaQps')" class="ad-input ad-mini-w" />
        <input v-model.number="quotaForm.concurrent" type="number" :placeholder="t('billing.quotaConcurrent')" class="ad-input ad-mini-w" />
        <input v-model.number="quotaForm.max_daily_chars" type="number" :placeholder="t('billing.quotaDailyChars')" class="ad-input" />
        <button class="ad-btn" @click="saveQuota">{{ t('billing.saveQuota') }}</button>
      </div>
      <p class="ad-hint">{{ tpl('billing.currentQuota', { qps: quotaForm.qps, concurrent: quotaForm.concurrent, daily: quotaForm.max_daily_chars }) }}</p>
    </div>

    <!-- 充值管理（超管） -->
    <div v-if="isSuper" class="ad-chart-card">
      <h3>{{ t('billing.topUpMgmt') }}</h3>
      <div class="ad-row">
        <input v-model="oForm.tokens" type="number" :placeholder="t('billing.tokenCount')" class="ad-input" />
        <input v-model.number="oForm.money" type="number" step="0.01" :placeholder="t('billing.amountYuan')" class="ad-input" />
        <button class="ad-btn" @click="createOrder">{{ t('billing.createOrder') }}</button>
      </div>
      <table class="ad-table">
        <thead><tr><th>{{ t('billing.colOrderNo') }}</th><th>{{ t('billing.colTokens') }}</th><th>{{ t('billing.colAmount') }}</th><th>{{ t('billing.colStatus') }}</th><th>{{ t('billing.colChannel') }}</th><th>{{ t('billing.colTime') }}</th><th>{{ t('common.operations') }}</th></tr></thead>
        <tbody>
          <tr v-for="o in orders" :key="o.id">
            <td>{{ o.order_no }}</td><td>{{ o.amount_tokens }}</td><td>{{ tpl('billing.yuan', { amount: o.amount_money }) }}</td>
            <td>{{ o.status }}</td><td>{{ channelLabel(o.channel) }}</td><td>{{ fmtTime(o.created_at) }}</td>
            <td class="ad-td">
              <button v-if="o.status === 'pending'" class="ad-btn-sm ad-btn-green" @click="payOrder(o)">{{ t('billing.confirmPayment') }}</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 在线充值（自助收银台） -->
    <div class="ad-chart-card">
      <h3>{{ t('billing.onlineTopUp') }}</h3>
      <div class="ad-hint">{{ t('billing.onlineTopUpHint') }}</div>
      <div class="ad-row">
        <select v-model="chForm.channel" class="ad-input">
          <option value="mock">{{ t('billing.chMock') }}</option>
          <option value="wechat">{{ t('billing.chWechat') }}</option>
          <option value="alipay">{{ t('billing.chAlipay') }}</option>
        </select>
        <input v-model.number="chForm.tokens" type="number" :placeholder="t('billing.tokenCount')" class="ad-input" />
        <button class="ad-btn" :disabled="chLoading" @click="openCheckout">{{ chLoading ? t('billing.ordering') : t('billing.goPay') }}</button>
      </div>
      <p v-if="curOrder && curOrder.status === 'pending'" class="ad-hint" style="color:#1a237e">
        {{ tpl('billing.currentOrder', { orderNo: curOrder.order_no, amount: curOrder.amount_tokens }) }}
      </p>
    </div>

    <!-- 发票管理 -->
    <div class="ad-chart-card">
      <h3>{{ t('billing.invoiceMgmt') }}</h3>
      <div class="ad-hint">{{ t('billing.invoiceHint') }}</div>
      <table class="ad-table">
        <thead><tr><th>{{ t('billing.colInvoiceNo') }}</th><th>{{ t('billing.colOrder') }}</th><th>{{ t('billing.colTitle') }}</th><th>{{ t('billing.colTaxNo') }}</th><th>{{ t('billing.colAmountYuan') }}</th><th>{{ t('billing.colInvoiceTime') }}</th></tr></thead>
        <tbody>
          <tr v-for="inv in invoices" :key="inv.id">
            <td>{{ inv.invoice_no }}</td><td>{{ inv.order_no }}</td><td>{{ inv.title }}</td><td>{{ inv.tax_no }}</td>
            <td>{{ inv.amount_money }}</td><td>{{ fmtTime(inv.created_at) }}</td>
          </tr>
          <tr v-if="!invoices.length"><td colspan="6" style="text-align:center;color:#999">{{ t('billing.noInvoices') }}</td></tr>
        </tbody>
      </table>
    </div>

    <!-- 收银台弹窗（扫码支付） -->
    <div v-if="showCheckout" class="pay-overlay" @click.self="closeCheckout">
      <div class="pay-modal">
        <div class="pay-modal-header">
          <h3>{{ t('billing.checkout') }}</h3>
          <button class="pay-modal-close" @click="closeCheckout">✕</button>
        </div>
        <div class="pay-modal-body">
          <div v-if="curOrder" class="pay-order-info">
            <p class="pay-amount"><b>{{ curOrder.amount_tokens }}</b> token</p>
            <p class="pay-no">{{ tpl('billing.orderNo', { orderNo: curOrder.order_no }) }}</p>
            <p class="pay-no">{{ tpl('billing.channelStatus', { channel: channelLabel(curOrder.channel), status: statusLabel(curOrder.status) }) }}</p>
          </div>

          <!-- 支付成功 -->
          <div v-if="curOrder && curOrder.status === 'paid'" class="pay-done">
            <div class="pay-done-icon">✓</div>
            <p>{{ tpl('billing.paySuccess', { amount: curOrder.amount_tokens }) }}</p>
            <button class="ad-btn ad-btn-green" @click="closeCheckout">{{ t('billing.done') }}</button>
          </div>

          <!-- 待支付：展示收款码 + 操作 -->
          <div v-else>
            <div class="pay-qr-box" v-if="curOrder">
              <div class="pay-qr-tip">{{ curOrder.channel === 'wechat' ? t('billing.scanWechat') : curOrder.channel === 'alipay' ? t('billing.scanAlipay') : t('billing.mockPay') }}</div>
              <pre class="pay-qr-content">{{ curOrder.qr_content }}</pre>
            </div>
            <div class="pay-actions">
              <button v-if="curOrder && curOrder.channel === 'mock'" class="ad-btn ad-btn-green" :disabled="chLoading" @click="simulatePay">
                {{ chLoading ? t('billing.processing') : t('billing.mockCredit') }}
              </button>
              <button v-if="curOrder" class="ad-btn" :disabled="chLoading" @click="checkStatus">{{ t('billing.refreshStatus') }}</button>
            </div>
            <p class="ad-hint" style="text-align:center">{{ t('billing.payHint') }}</p>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch, onBeforeUnmount } from 'vue'
import { billingConfig, billingConfigSave, billingQuota, billingQuotaSave, billingOrders, adminOrderCreate, adminOrderPay, billingInvoices, payCreate, payStatus, paySimulate } from '@/api'
import { activeTenantId, isSuper } from './store'
import { fmtTime } from './ui'
import { t, tpl } from '@/i18n'

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
  alert(t('billing.quotaSaved'))
  await loadQuota()
}

// 充值订单
const orders = ref<any[]>([])
// 后台充值表单：token 数/金额（线下转账或 mock 渠道）
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

// ==================== 在线充值（收银台） ====================

// 收银台状态
const chForm = ref({ channel: 'mock', tokens: 1000 })
// 是否展示收银台弹窗
const showCheckout = ref(false)
// 下单请求进行中（禁用按钮）
const chLoading = ref(false)
// 当前正在支付的订单（用于展示二维码与轮询状态）
const curOrder = ref<any>(null)
let payTimer: ReturnType<typeof setInterval> | null = null

// 渠道与状态中文标签
function channelLabel(c: string) {
  return { offline: t('billing.channelOffline'), mock: t('billing.channelMock'), wechat: t('billing.channelWechat'), alipay: t('billing.channelAlipay') }[c || 'offline'] || c || t('billing.channelFallback')
}

// statusLabel 将订单状态代码转换为中文展示文案。
// 参数 s: 订单状态码（pending/paid/refunded/cancelled）；返回: 对应中文标签，未知状态原样返回。
function statusLabel(s: string) {
  return { pending: t('billing.stPending'), paid: t('billing.stPaid'), refunded: t('billing.stRefunded'), cancelled: t('billing.stCancelled') }[s || 'pending'] || s
}

// 打开收银台：发起下单并展示收款码
async function openCheckout() {
  if (chForm.value.tokens <= 0) { alert(t('billing.enterTokenCount')); return }
  chLoading.value = true
  try {
    const r = await payCreate({ tokens: chForm.value.tokens, channel: chForm.value.channel })
    if (!r.success) { alert(r.message); return }
    curOrder.value = (r as any).order
    showCheckout.value = true
    startPolling()
  } finally {
    chLoading.value = false
  }
}

// 关闭收银台
function closeCheckout() {
  showCheckout.value = false
  stopPolling()
  if (curOrder.value && curOrder.value.status === 'paid') loadOrders()
}

// 轮询支付状态（每 3 秒，到账即停）
function startPolling() {
  stopPolling()
  payTimer = setInterval(checkStatus, 3000)
}

// stopPolling 停止支付状态轮询定时器（收银台关闭或支付完成时调用）。
// 无参数无返回；未启动轮询时静默跳过。
function stopPolling() {
  if (payTimer) { clearInterval(payTimer); payTimer = null }
}
async function checkStatus() {
  if (!curOrder.value) return
  const r = await payStatus(curOrder.value.id)
  if (r.success) {
    curOrder.value = (r as any).order
    if (curOrder.value.status === 'paid') stopPolling()
  }
}

// 模拟支付（mock 渠道测试用）
async function simulatePay() {
  if (!curOrder.value) return
  chLoading.value = true
  try {
    const r = await paySimulate(curOrder.value.id)
    if (!r.success) { alert(r.message); return }
    await checkStatus()
  } finally {
    chLoading.value = false
  }
}

onBeforeUnmount(stopPolling)

// 挂载与租户切换时加载
async function loadAll() {
  if (isSuper.value) await Promise.all([loadBillingConfig(), loadOrders()])
  await Promise.all([loadQuota(), loadInvoices()])
}
onMounted(loadAll)
watch(activeTenantId, loadAll)
</script>