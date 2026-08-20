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
      <h3>配额设置（当前组织）</h3>
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
        <thead><tr><th>单号</th><th>tokens</th><th>金额</th><th>状态</th><th>渠道</th><th>时间</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="o in orders" :key="o.id">
            <td>{{ o.order_no }}</td><td>{{ o.amount_tokens }}</td><td>{{ o.amount_money }} 元</td>
            <td>{{ o.status }}</td><td>{{ channelLabel(o.channel) }}</td><td>{{ fmtTime(o.created_at) }}</td>
            <td class="ad-td">
              <button v-if="o.status === 'pending'" class="ad-btn-sm ad-btn-green" @click="payOrder(o)">确认收款</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 在线充值（自助收银台） -->
    <div class="ad-chart-card">
      <h3>在线充值</h3>
      <div class="ad-hint">选择充值 token 数量，扫码（或点击模拟支付）即可到账。渠道：微信支付 / 支付宝 / 模拟。</div>
      <div class="ad-row">
        <select v-model="chForm.channel" class="ad-input">
          <option value="mock">模拟支付（测试）</option>
          <option value="wechat">微信支付</option>
          <option value="alipay">支付宝</option>
        </select>
        <input v-model.number="chForm.tokens" type="number" placeholder="token 数量" class="ad-input" />
        <button class="ad-btn" :disabled="chLoading" @click="openCheckout">{{ chLoading ? '下单中…' : '去支付' }}</button>
      </div>
      <p v-if="curOrder && curOrder.status === 'pending'" class="ad-hint" style="color:#1a237e">
        当前订单 {{ curOrder.order_no }}：待支付 {{ curOrder.amount_tokens }} token
      </p>
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

    <!-- 收银台弹窗（扫码支付） -->
    <div v-if="showCheckout" class="pay-overlay" @click.self="closeCheckout">
      <div class="pay-modal">
        <div class="pay-modal-header">
          <h3>收银台</h3>
          <button class="pay-modal-close" @click="closeCheckout">✕</button>
        </div>
        <div class="pay-modal-body">
          <div v-if="curOrder" class="pay-order-info">
            <p class="pay-amount"><b>{{ curOrder.amount_tokens }}</b> token</p>
            <p class="pay-no">订单号：{{ curOrder.order_no }}</p>
            <p class="pay-no">渠道：{{ channelLabel(curOrder.channel) }} · 状态：{{ statusLabel(curOrder.status) }}</p>
          </div>

          <!-- 支付成功 -->
          <div v-if="curOrder && curOrder.status === 'paid'" class="pay-done">
            <div class="pay-done-icon">✓</div>
            <p>支付成功，{{ curOrder.amount_tokens }} token 已到账</p>
            <button class="ad-btn ad-btn-green" @click="closeCheckout">完成</button>
          </div>

          <!-- 待支付：展示收款码 + 操作 -->
          <div v-else>
            <div class="pay-qr-box" v-if="curOrder">
              <div class="pay-qr-tip">{{ curOrder.channel === 'wechat' ? '微信扫码支付' : curOrder.channel === 'alipay' ? '支付宝扫码支付' : '模拟支付（测试模式）' }}</div>
              <pre class="pay-qr-content">{{ curOrder.qr_content }}</pre>
            </div>
            <div class="pay-actions">
              <button v-if="curOrder && curOrder.channel === 'mock'" class="ad-btn ad-btn-green" :disabled="chLoading" @click="simulatePay">
                {{ chLoading ? '处理中…' : '模拟支付到账' }}
              </button>
              <button v-if="curOrder" class="ad-btn" :disabled="chLoading" @click="checkStatus">刷新状态</button>
            </div>
            <p class="ad-hint" style="text-align:center">支付后请点击「刷新状态」确认到账（或等待自动轮询）</p>
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

// ==================== 在线充值（收银台） ====================

// 收银台状态
const chForm = ref({ channel: 'mock', tokens: 1000 })
const showCheckout = ref(false)
const chLoading = ref(false)
const curOrder = ref<any>(null)
let payTimer: ReturnType<typeof setInterval> | null = null

// 渠道与状态中文标签
function channelLabel(c: string) {
  return { offline: '线下转账', mock: '模拟', wechat: '微信', alipay: '支付宝' }[c || 'offline'] || c || '线下'
}
function statusLabel(s: string) {
  return { pending: '待支付', paid: '已到账', refunded: '已退款', cancelled: '已取消' }[s || 'pending'] || s
}

// 打开收银台：发起下单并展示收款码
async function openCheckout() {
  if (chForm.value.tokens <= 0) { alert('请输入 token 数量'); return }
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