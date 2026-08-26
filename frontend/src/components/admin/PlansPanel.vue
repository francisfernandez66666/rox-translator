<!-- ============================================================================
   components/admin/PlansPanel.vue — 💰 套餐中心（2026-08-26 整合重构）
   把原「套餐详情(Packages 全部 + Billing 全部)」两面板堆叠重组为按角色分域的单一页面：
     租户管理员（L3·采购视角）：① 当前套餐与余额 ② 选购套餐 ③ 在线充值 ④ 订单与发票 ⑤ 用量配额
     超级管理员（L4·运营视角）：④ 平台订单与发票 ⑤ 生效租户配量（默认折叠）：⑥ 商业包管理 ⑦ 运营参数 ⑧ 待人工确认订单
     —— 超管不出现「当前套餐/选购/充值」（无采购语义）；租管不可见任何超管区。
   与注册触达无关的配置已迁出（→ Alerts 系统告警面板「注册与触达」区）。
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('admin.plansDetail') }}</h2>

    <!-- ===== 分区锚点导航 ===== -->
    <div class="pl-nav">
      <button v-for="sec in sections" :key="sec.key" class="pl-chip" @click="go(sec.id)">{{ t(sec.label) }}</button>
    </div>

    <!-- ========== ① 当前套餐与余额（仅租管；超管为平台视角无采购语义） ========== -->
    <div id="sec-current" v-if="!isSuper" class="ad-chart-card">
      <h3>{{ t('plans.nav.current') }}</h3>
      <div class="pl-cards">
        <div class="pl-card"><b>{{ fmtNum(pkg.balance_tokens) }}</b><span>{{ t('usage.currentBalance') }}</span></div>
        <div class="pl-card"><b class="pl-c-grant">{{ fmtNum(pkg.sub_grants_left) }}</b><span>{{ t('plans.balanceGrants') }}</span></div>
        <div class="pl-card"><b class="pl-c-perm">{{ fmtNum(pkg.permanent_balance) }}</b><span>{{ t('plans.balancePermanent') }}</span></div>
        <div class="pl-card"><b>{{ fmtNum(pkg.tokens_used_month) }}</b><span>{{ t('plans.usedMonth') }}</span></div>
      </div>
      <div class="ad-row" style="margin-top:10px">
        <span class="ad-hint">
          {{ tpl('billing.myPackageCode', { code: pkg.package_code || '—' }) }}
          <template v-if="pkgExpiresLabel"> · {{ t('plans.expiresAt') }}: {{ pkgExpiresLabel }}</template>
          · {{ tpl('billing.myPackageBalance', { balance: pkg.sentence_balance ?? '—' }) }}
        </span>
      </div>
    </div>

    <!-- ========== ② 选购套餐（仅租管） ========== -->
    <div id="sec-shop" v-if="!isSuper" class="ad-chart-card">
      <h3>{{ t('plans.nav.shop') }}</h3>
      <template v-for="g in planGroups" :key="g.type">
        <div class="pl-group-title">{{ g.title }}</div>
        <div class="pl-grid">
          <div v-for="pl in g.items" :key="pl.id" class="pl-plan" :class="{ hot: pl.ptype === 'increment' }">
            <div class="pl-plan-name">{{ pl.name }}</div>
            <div class="pl-plan-price">¥{{ pl.price_money }}<small v-if="pl.ptype === 'paid'"> /{{ pl.duration_days }}d</small></div>
            <ul class="pl-plan-feats">
              <li>{{ tpl('billing.pkgSentences', { n: pl.sentences }) }}</li>
              <li>{{ t('packages.type.' + pl.ptype) }}</li>
            </ul>
            <button class="ad-btn ad-btn-green pl-plan-btn" @click="subscribe(pl)">{{ t('billing.subscribeNow') }}</button>
          </div>
        </div>
        <p v-if="!g.items.length" class="ad-hint">{{ t('billing.noPlans') }}</p>
      </template>
    </div>

    <!-- ========== ③ 在线充值（仅租管） ========== -->
    <div id="sec-topup" v-if="!isSuper" class="ad-chart-card">
      <h3>{{ t('plans.nav.topup') }}</h3>
      <div class="ad-hint">{{ t('billing.onlineTopUpHint') }}</div>
      <div class="ad-row" style="margin-top:8px">
        <select v-model="chForm.channel" class="ad-input">
          <option value="auto">{{ tpl('billing.payModeAuto', { mode: payModeLabel }) }}</option>
          <option v-if="payMode === 'static_qr'" value="manual">{{ t('billing.chStaticQR') }}</option>
          <option v-if="payMode === 'sdk'" value="wechat">{{ t('billing.chWechat') }}</option>
          <option v-if="payMode === 'sdk'" value="alipay">{{ t('billing.chAlipay') }}</option>
          <option value="mock">{{ t('billing.chMock') }}</option>
        </select>
        <input v-model.number="chForm.tokens" type="number" :placeholder="t('billing.tokenCount')" class="ad-input" />
        <button class="ad-btn ad-btn-green" :disabled="chLoading" @click="openCheckout()">{{ chLoading ? t('billing.ordering') : t('billing.goPay') }}</button>
      </div>
      <p v-if="curOrder && curOrder.status === 'pending'" class="ad-hint" style="color:#1a237e">
        {{ tpl('billing.currentOrder', { orderNo: curOrder.order_no, amount: curOrder.amount_tokens }) }}
      </p>
    </div>

    <!-- ========== ④ 订单与发票 ========== -->
    <div id="sec-orders" class="ad-chart-grid">
      <div class="ad-chart-card">
        <h3>{{ t('billing.colOrders') || '我的订单' }}</h3>
        <table class="ad-table">
          <thead><tr><th>{{ t('billing.colOrderNo') }}</th><th>{{ t('billing.colTokens') }}</th><th>{{ t('billing.colAmount') }}</th><th>{{ t('billing.colStatus') }}</th><th>{{ t('common.operations') }}</th></tr></thead>
          <tbody>
            <tr v-for="o in orders.slice(0, 20)" :key="o.id">
              <td>{{ o.order_no }}</td><td>{{ o.amount_tokens }}</td><td>{{ tpl('billing.yuan', { amount: o.amount_money }) }}</td>
              <td><span class="pl-st" :class="'st-' + o.status">{{ statusLabel(o.status) }}</span></td>
              <td class="ad-td">
                <button v-if="o.status === 'pending'" class="ad-btn-sm ad-btn-green" @click="resumePay(o)">{{ t('plans.orderContinue') }}</button>
              </td>
            </tr>
            <tr v-if="!orders.length"><td colspan="5" style="text-align:center;color:#999">{{ t('plans.noOrder') }}</td></tr>
          </tbody>
        </table>
      </div>
      <div class="ad-chart-card">
        <h3>{{ t('billing.invoiceMgmt') }}</h3>
        <table class="ad-table">
          <thead><tr><th>{{ t('billing.colInvoiceNo') }}</th><th>{{ t('billing.colTitle') }}</th><th>{{ t('billing.colAmountYuan') }}</th></tr></thead>
          <tbody>
            <tr v-for="inv in invoices" :key="inv.id">
              <td>{{ inv.invoice_no }}</td><td>{{ inv.title || '—' }}</td><td>{{ inv.amount_money }}</td>
            </tr>
            <tr v-if="!invoices.length"><td colspan="3" style="text-align:center;color:#999">{{ t('billing.noInvoices') }}</td></tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- ========== ⑤ 用量配额 ========== -->
    <div id="sec-quota" class="ad-chart-card">
      <h3>{{ t('plans.nav.quota') }}</h3>
      <div class="ad-hint">{{ t('billing.quotaHint') }}</div>
      <div class="ad-row" style="margin-top:8px">
        <input v-model.number="quotaForm.qps" type="number" :placeholder="t('billing.quotaQps')" class="ad-input ad-mini-w" />
        <input v-model.number="quotaForm.concurrent" type="number" :placeholder="t('billing.quotaConcurrent')" class="ad-input ad-mini-w" />
        <input v-model.number="quotaForm.max_daily_chars" type="number" :placeholder="t('billing.quotaDailyChars')" class="ad-input ad-mini-w" />
        <input v-model.number="quotaForm.max_daily_tokens" type="number" placeholder="每日 token 上限 (D4)" class="ad-input ad-mini-w" />
        <button class="ad-btn" @click="saveQuota">{{ t('billing.saveQuota') }}</button>
      </div>
    </div>

    <!-- ========== ⑥ 商业包管理（超管·折叠） ========== -->
    <details v-if="isSuper" class="pl-fold">
      <summary>🗂 {{ t('plans.nav.pkgMgmt') }}</summary>
      <div class="ad-chart-card">
        <h3>{{ t('packages.createTitle') }}</h3>
        <div class="ad-row">
          <input v-model="pkgForm.code" :placeholder="t('packages.code')" class="ad-input ad-mini-w" />
          <input v-model="pkgForm.name" :placeholder="t('packages.name')" class="ad-input" />
          <select v-model="pkgForm.ptype" class="ad-input ad-mini-w">
            <option value="paid">{{ t('packages.type.paid') }}</option>
            <option value="increment">{{ t('packages.type.increment') }}</option>
            <option value="free">{{ t('packages.type.free') }}</option>
          </select>
          <input v-model.number="pkgForm.sentences" type="number" :placeholder="t('packages.sentences')" class="ad-input ad-mini-w" />
          <input v-model.number="pkgForm.price_money" type="number" step="0.01" :placeholder="t('packages.price')" class="ad-input ad-mini-w" />
          <input v-model.number="pkgForm.duration_days" type="number" :placeholder="t('packages.duration')" class="ad-input ad-mini-w" />
          <button class="ad-btn" @click="createPkg">{{ t('common.save') }}</button>
        </div>
        <table class="ad-table" style="margin-top:10px">
          <thead><tr><th>code</th><th>{{ t('packages.name') }}</th><th>{{ t('packages.type') }}</th><th>{{ t('packages.sentences') }}</th><th>{{ t('packages.price') }}</th><th>{{ t('common.status') }}</th><th>{{ t('common.operations') }}</th></tr></thead>
          <tbody>
            <tr v-for="p in pkgs" :key="p.id">
              <td>{{ p.code }}</td><td>{{ p.name }}</td><td>{{ typeLabel(p.ptype) }}</td>
              <td>{{ p.sentences }}</td><td>¥{{ p.price_money }}</td>
              <td><button class="ad-btn-sm" :class="p.enabled ? 'ad-btn-green' : ''" @click="togglePkg(p)">{{ p.enabled ? t('common.active') : t('common.disabled') }}</button></td>
              <td class="ad-td"><button class="ad-btn-sm ad-btn-red" @click="deletePkg(p)">✕</button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </details>

    <!-- ========== ⑦ 运营参数（超管·折叠） ========== -->
    <details v-if="isSuper" class="pl-fold">
      <summary>🛠 {{ t('plans.nav.ops') }}</summary>
      <div class="ad-chart-card">
        <label class="ad-switch" style="margin-right:14px"><input type="checkbox" v-model="billingEnforced" @change="saveEnforce" /><span></span></label>
        <span :style="{ color: billingEnforced ? '#2e7d32' : '#888', fontWeight: 600 }">{{ billingEnforced ? t('billing.enforcedOn') : t('billing.enforcedOff') }}</span>
        <div class="ad-row" style="margin-top:12px">
          <span class="ad-hint">{{ t('packages.trialLabel') }}</span>
          <input v-model.number="trialSentences" type="number" class="ad-input ad-mini-w" />
          <span class="ad-hint" style="margin-left:12px">{{ t('packages.markupLabel') }}</span>
          <input v-model.number="markupMultiplier" type="number" step="0.1" min="1" class="ad-input ad-mini-w" />
          <span class="ad-hint" style="margin-left:12px">{{ t('packages.rateLabel') }}</span>
          <input v-model.number="tokensPerSentence" type="number" min="1" class="ad-input ad-mini-w" />
          <button class="ad-btn" @click="saveBillingParams">{{ t('common.save') }}</button>
        </div>
        <div class="ad-row" style="margin-top:12px">
          <span class="ad-hint">{{ t('packages.payModeTitle') }}</span>
          <select v-model="payModeCfg" class="ad-input ad-mini-w">
            <option value="mock">{{ t('packages.payMock') }}</option>
            <option value="sdk">{{ t('packages.paySdk') }}</option>
            <option value="static_qr">{{ t('packages.payStaticQR') }}</option>
          </select>
          <button class="ad-btn" @click="savePayMode">{{ t('common.save') }}</button>
        </div>
        <div v-if="payModeCfg === 'static_qr'" class="ad-row" style="margin-top:8px">
          <input v-model="staticQRImage" :placeholder="t('packages.staticQRPlaceholder')" class="ad-input ad-wide" />
          <button class="ad-btn" @click="saveStaticQR">{{ t('common.save') }}</button>
        </div>
      </div>
    </details>

    <!-- ========== ⑧ 待人工确认订单（超管·折叠） ========== -->
    <details v-if="isSuper" class="pl-fold">
      <summary>⏳ {{ t('plans.nav.manual') }}</summary>
      <div class="ad-chart-card">
        <table class="ad-table">
          <thead><tr><th>{{ t('billing.colOrderNo') }}</th><th>{{ t('billing.colTokens') }}</th><th>{{ t('billing.colTenant') }}</th><th>{{ t('billing.colTime') }}</th><th>{{ t('common.operations') }}</th></tr></thead>
          <tbody>
            <tr v-for="o in manualOrders" :key="o.id">
              <td>{{ o.order_no }}</td><td>{{ o.amount_tokens }}</td><td>#{{ o.tenant_id }}</td><td>{{ fmtTime(o.created_at) }}</td>
              <td class="ad-td"><button class="ad-btn-sm ad-btn-green" @click="confirmManual(o)">{{ t('billing.confirmPayment') }}</button></td>
            </tr>
            <tr v-if="!manualOrders.length"><td colspan="5" style="text-align:center;color:#999">{{ t('billing.noManualOrders') }}</td></tr>
          </tbody>
        </table>
      </div>
    </details>

    <!-- ===== 收银台弹窗 ===== -->
    <div v-if="showCheckout" class="pl-overlay" @click.self="closeCheckout">
      <div class="pl-modal">
        <div class="pl-modal-head"><h3>{{ t('billing.checkout') }}</h3><button class="pl-x" @click="closeCheckout">✕</button></div>
        <div v-if="curOrder && curOrder.status === 'paid'" class="pl-paid">
          <div class="pl-paid-icon">✓</div>
          <p>{{ tpl('billing.paySuccess', { amount: curOrder.amount_tokens }) }}</p>
          <button class="ad-btn ad-btn-green" @click="closeCheckout">{{ t('billing.done') }}</button>
        </div>
        <div v-else>
          <div v-if="curOrder" class="pl-qr-box">
            <template v-if="curOrder.channel === 'manual'">
              <div class="ad-hint">{{ t('billing.staticQR') }}</div>
              <img v-if="isImage(curOrder.qr_content)" :src="curOrder.qr_content" class="pl-qr-img" alt="qr" />
              <pre v-else class="pl-qr-pre">{{ curOrder.qr_content }}</pre>
            </template>
            <template v-else>
              <pre class="pl-qr-pre">{{ curOrder.qr_content }}</pre>
            </template>
            <p class="ad-hint">{{ tpl('billing.orderNo', { orderNo: curOrder.order_no }) }}</p>
          </div>
          <div class="pl-actions">
            <button v-if="curOrder?.channel === 'manual'" class="ad-btn ad-btn-green" :disabled="chLoading" @click="manualConfirm">{{ chLoading ? t('billing.processing') : t('billing.iPaid') }}</button>
            <button v-if="curOrder?.channel === 'mock'" class="ad-btn ad-btn-green" :disabled="chLoading" @click="simulatePay">{{ t('billing.mockCredit') }}</button>
            <button v-if="curOrder" class="ad-btn" @click="checkStatus">{{ t('billing.refreshStatus') }}</button>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
// ==================== 套餐中心（整合原 Packages/Billing 双面板） ====================
import { ref, computed, onMounted, watch, onBeforeUnmount } from 'vue'
import {
  myPackage, plans, packageSubscribe, billingOrders, billingInvoices, billingQuota, billingQuotaSave,
  adminPackages, adminPackageCreate, adminPackageUpdate, adminPackageDelete, adminPackageSettings,
  adminPackageSettingsSave, adminOrderCreate, adminOrderPay, manualConfirmOrders,
  payCreate, payStatus, paySimulate, payManualConfirm,
} from '@/api'
import { activeTenantId, isSuper } from './store'
import { fmtTime } from './ui'
import { t, tpl } from '@/i18n'

// ===== 分区导航（★ 按角色分域，2026-08-26 线上反馈修正） =====
// 租管(L3)=采购视角五区；超管(L4)=平台运营视角（订单发票/配额 + 三个折叠运营区），
// 不再出现当前套餐/选购/充值等采购语义分区。
const sections = computed(() => isSuper.value ? [
  { id: 'sec-orders', key: 'orders', label: 'plans.nav.orders' },
  { id: 'sec-quota', key: 'quota', label: 'plans.nav.quota' },
  { id: 'sec-pkg', key: 'pkgMgmt', label: 'plans.nav.pkgMgmt' },
  { id: 'sec-ops', key: 'ops', label: 'plans.nav.ops' },
  { id: 'sec-manual', key: 'manual', label: 'plans.nav.manual' },
] : [
  { id: 'sec-current', key: 'current', label: 'plans.nav.current' },
  { id: 'sec-shop', key: 'shop', label: 'plans.nav.shop' },
  { id: 'sec-topup', key: 'topup', label: 'plans.nav.topup' },
  { id: 'sec-orders', key: 'orders', label: 'plans.nav.orders' },
  { id: 'sec-quota', key: 'quota', label: 'plans.nav.quota' },
])
function go(id: string) { document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' }) }

const fmtNum = (n: unknown) => typeof n === 'number' ? new Intl.NumberFormat().format(n) : '—'

// ===== ① 当前套餐 =====
const pkg = ref<any>({})
const pkgExpiresLabel = computed(() => {
  const v = pkg.value?.package_expires as string
  if (!v) return ''
  const d = new Date(v)
  return isNaN(d.getTime()) ? '' : d.toISOString().slice(0, 10)
})

// ===== ② 选购 =====
const planList = ref<any[]>([])
const planGroups = computed(() => ([
  { type: 'paid', title: t('plans.groupPaid'), items: planList.value.filter(p => p.ptype === 'paid') },
  { type: 'increment', title: t('plans.groupIncrement'), items: planList.value.filter(p => p.ptype === 'increment') },
]))

// ===== 支付渠道（收银台共用状态） =====
const payMode = ref('mock')
const payModeCfg = ref('mock')
const payModeLabel = computed(() => ({ mock: t('billing.chMock'), sdk: t('billing.chSdk'), static_qr: t('billing.chStaticQR') }[payMode.value] || payMode.value))

async function loadPackage() {
  const r = await myPackage()
  if (r.success) {
    pkg.value = r as any
    if ((r as any).pay_mode) { payMode.value = (r as any).pay_mode; payModeCfg.value = (r as any).pay_mode }
  }
  const p = await plans()
  if (p.success) planList.value = (p as any).plans || []
}

async function subscribe(pl: any) {
  const r = await packageSubscribe(pl.code)
  if (!r.success) return
  const o = (r as any).order
  if (o) { curOrder.value = o; showCheckout.value = true; if (o.channel !== 'manual') startPolling() }
  await loadPackage()
}

// ===== ③④ 充值 / 订单 / 发票 =====
const orders = ref<any[]>([])
const invoices = ref<any[]>([])
const chForm = ref({ channel: 'auto', tokens: 100000 })
const showCheckout = ref(false)
const chLoading = ref(false)
const curOrder = ref<any>(null)
let payTimer: ReturnType<typeof setInterval> | null = null

function isImage(s?: string): boolean {
  if (!s) return false
  return s.startsWith('data:image') || /^https?:\/\/.+\.(png|jpe?g|gif|webp)/i.test(s)
}
function statusLabel(s: string) {
  return { pending: t('billing.stPending'), paid: t('billing.stPaid'), refunded: t('billing.stRefunded'), cancelled: t('billing.stCancelled') }[s] || s
}

async function openCheckout() {
  if (chForm.value.tokens <= 0) return
  chLoading.value = true
  try {
    const channel = chForm.value.channel === 'auto' ? '' : chForm.value.channel
    const r = await payCreate({ tokens: chForm.value.tokens, channel })
    if (!r.success) return
    curOrder.value = (r as any).order
    showCheckout.value = true
    if (curOrder.value.channel !== 'manual') startPolling()
  } finally { chLoading.value = false }
}
// resumePay 从订单列表继续支付未完结单（回填 token 数重新下单——旧 pending 单 15min 自动关闭）
function resumePay(_o: any) { go('sec-topup') }
function closeCheckout() { showCheckout.value = false; stopPolling(); loadOrders(); loadPackage() }
function startPolling() { stopPolling(); payTimer = setInterval(checkStatus, 3000) }
function stopPolling() { if (payTimer) { clearInterval(payTimer); payTimer = null } }
async function checkStatus() {
  if (!curOrder.value) return
  const r = await payStatus(curOrder.value.id)
  if (r.success) { curOrder.value = (r as any).order; if (curOrder.value.status === 'paid') stopPolling() }
}
async function simulatePay() {
  if (!curOrder.value) return
  chLoading.value = true
  try { const r = await paySimulate(curOrder.value.id); if (r.success) await checkStatus() } finally { chLoading.value = false }
}
async function manualConfirm() {
  if (!curOrder.value) return
  chLoading.value = true
  try { const r = await payManualConfirm(curOrder.value.id); if (r.success) { stopPolling(); closeCheckout() } } finally { chLoading.value = false }
}
onBeforeUnmount(stopPolling)

// ===== ⑤ 配额 =====
const quotaForm = ref<any>({ qps: 10, concurrent: 3, max_daily_chars: 0, max_daily_tokens: 0 })
async function loadQuota() {
  const r = await billingQuota()
  // ★ 修复（2026-08-26 全仓评审 D4）：恢复配额时补 max_daily_tokens 字段——
  //   旧实现漏掉该键，保存时 Math.max(0, undefined) 得 NaN，JSON 序列化为 null 提交后端
  if (r.success) quotaForm.value = { qps: (r as any).qps || 10, concurrent: (r as any).concurrent || 3, max_daily_chars: (r as any).max_daily_chars || 0, max_daily_tokens: (r as any).max_daily_tokens ?? 0 }
}
async function saveQuota() {
  await billingQuotaSave({
    qps: Math.max(1, quotaForm.value.qps),
    concurrent: Math.max(1, quotaForm.value.concurrent),
    max_daily_chars: Math.max(0, quotaForm.value.max_daily_chars),
    max_daily_tokens: Math.max(0, quotaForm.value.max_daily_tokens),
  })
  await loadQuota()
}

// ===== ⑥⑦⑧ 超管区 =====
const pkgs = ref<any[]>([])
const billingEnforced = ref(false)
const trialSentences = ref(100)
const markupMultiplier = ref(1.5)
const tokensPerSentence = ref(500)
const staticQRImage = ref('')
const pkgForm = ref({ code: '', name: '', ptype: 'paid', sentences: 1000, price_money: 0, duration_days: 30 })
const manualOrders = ref<any[]>([])

function typeLabel(p: string) {
  return { free: t('packages.type.free'), paid: t('packages.type.paid'), increment: t('packages.type.increment') }[p] || p
}
async function loadPkgs() {
  if (!isSuper.value) return
  const r = await adminPackages()
  if (r.success) pkgs.value = (r as any).packages || []
  const cfg = await adminPackageSettings()
  if (cfg.success) {
    const c = cfg as any
    billingEnforced.value = c.billing_enforced === '1' || c.billing_enforced === true
    if (c.trial_sentences) trialSentences.value = Number(c.trial_sentences)
    if (typeof c.billing_markup_multiplier === 'number') markupMultiplier.value = c.billing_markup_multiplier
    if (c.estimate_tokens_per_sentence) tokensPerSentence.value = Number(c.estimate_tokens_per_sentence)
    if (c.pay_mode) payModeCfg.value = c.pay_mode
    if (c.static_qr_image) staticQRImage.value = c.static_qr_image
  }
  const m = await manualConfirmOrders()
  if (m.success) manualOrders.value = (m as any).orders || []
}
async function createPkg() {
  if (!pkgForm.value.code || !pkgForm.value.name) return
  const r = await adminPackageCreate(pkgForm.value)
  if (!r.success) return
  pkgForm.value = { code: '', name: '', ptype: 'paid', sentences: 1000, price_money: 0, duration_days: 30 }
  await loadPkgs()
}
async function togglePkg(p: any) { await adminPackageUpdate({ id: p.id, enabled: p.enabled ? 0 : 1 }); await loadPkgs() }
async function deletePkg(p: any) { await adminPackageDelete(p.id); await loadPkgs() }
async function saveEnforce() { await adminPackageSettingsSave({ billing_enforced: billingEnforced.value ? '1' : '0' } as any) }
async function saveBillingParams() {
  if (!(markupMultiplier.value >= 1)) return
  if (!(tokensPerSentence.value > 0)) return
  await adminPackageSettingsSave({ billing_markup_multiplier: markupMultiplier.value, estimate_tokens_per_sentence: tokensPerSentence.value } as any)
}
async function savePayMode() { await adminPackageSettingsSave({ pay_mode: payModeCfg.value }); payMode.value = payModeCfg.value }
async function saveStaticQR() { await adminPackageSettingsSave({ static_qr_image: staticQRImage.value }) }
async function confirmManual(o: any) { const r = await adminOrderPay(o.id); if (r.success) await Promise.all([loadPkgs(), loadOrders()]) }

// ===== 装载（按角色裁剪：超管不拉采购数据） =====
async function loadAll() {
  if (!isSuper.value) await loadPackage()
  await Promise.all([loadOrders(), loadInvoices(), loadQuota(), loadPkgs()])
}
async function loadOrders() { const r = await billingOrders(); if (r.success) orders.value = (r as any).orders || [] }
async function loadInvoices() { const r = await billingInvoices(); if (r.success) invoices.value = (r as any).invoices || [] }

onMounted(loadAll)
watch(activeTenantId, loadAll)
</script>

<style scoped>
/* ===== 分区锚点导航 =====
   ★ 悬浮底座修复（2026-08-26 线上反馈）：sticky 相对滚动容器 .ad-main 吸附于 top:0，
   负 margin 抵消其 padding(28px 32px) 铺满整行；实底色 + 毛玻璃 + 投影，
   杜绝此前 background:inherit 透明导致下滑时内容从锚点条底下透出 */
/* ★ 修复（2026-08-26 全仓评审 D4）：原文件此处 <style scoped> 标签连写两次，
   SFC 结构损坏，删除多余的一个 */
.pl-nav {
  position: sticky; top: 0; z-index: 30;
  display: flex; flex-wrap: wrap; gap: 8px;
  margin: -28px -32px 16px; padding: 12px 32px;
  background: rgba(245, 247, 251, .96);
  backdrop-filter: blur(6px); -webkit-backdrop-filter: blur(6px);
  box-shadow: 0 4px 14px rgba(16, 24, 40, .08);
}
.pl-chip { border: none; background: #eef2f7; color: #33475b; border-radius: 16px; padding: 6px 14px; font-size: 13px; cursor: pointer; white-space: nowrap; }
.pl-chip:hover { background: #e0e7f0; }
/* 锚点跳转落点预留吸附条高度，防止目标卡片标题被遮 */
.pl-nav + .ad-chart-card, [id^="sec-"] { scroll-margin-top: 64px; }
/* ===== 当前套餐卡片 ===== */
.pl-cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 10px; }
.pl-card { background: #f7f9fc; border-radius: 10px; padding: 12px 14px; display: flex; flex-direction: column; gap: 2px; }
.pl-card b { font-size: 20px; color: #1a237e; }
.pl-card span { font-size: 12px; color: #78909c; }
.pl-c-grant { color: #e65100 !important; }
.pl-c-perm { color: #2e7d32 !important; }
/* ===== 套餐卡片 ===== */
.pl-group-title { font-weight: 600; font-size: 13.5px; color: #455a64; margin: 10px 0 6px; }
.pl-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); gap: 12px; }
.pl-plan { border: 1.5px solid #e3eaf2; border-radius: 12px; padding: 14px; display: flex; flex-direction: column; gap: 6px; background: #fff; transition: box-shadow .15s; }
.pl-plan:hover { box-shadow: 0 6px 18px rgba(26,35,126,.08); }
.pl-plan.hot { border-color: #ff9800; }
.pl-plan-name { font-weight: 600; font-size: 15px; }
.pl-plan-price { font-size: 22px; font-weight: 700; color: #1a237e; }
.pl-plan-price small { font-size: 12px; color: #90a4ae; font-weight: 400; }
.pl-plan-feats { margin: 0 0 4px 16px; padding: 0; font-size: 12.5px; color: #607d8b; line-height: 1.7; }
.pl-plan-btn { margin-top: auto; }
/* ===== 订单状态徽标 ===== */
.pl-st { padding: 2px 8px; border-radius: 10px; font-size: 12px; }
.st-pending { background: rgba(230,81,0,.12); color: #e65100; }
.st-paid { background: rgba(46,125,50,.12); color: #2e7d32; }
.st-refunded, .st-cancelled { background: rgba(117,117,117,.12); color: #757575; }
/* ===== 折叠区 ===== */
.pl-fold { margin-top: 12px; border: 1px solid #eceff3; border-radius: 10px; padding: 10px 12px; background: #fbfcfe; }
.pl-fold summary { cursor: pointer; font-weight: 600; font-size: 13.5px; color: #546e7a; }
.pl-fold[open] summary { margin-bottom: 8px; }
/* ===== 收银台 ===== */
.pl-overlay { position: fixed; inset: 0; background: rgba(0,0,0,.45); display: flex; align-items: center; justify-content: center; z-index: 3000; }
.pl-modal { width: 380px; max-width: calc(100vw - 32px); background: #fff; border-radius: 14px; padding: 18px; }
.pl-modal-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px; }
.pl-x { border: none; background: transparent; font-size: 16px; cursor: pointer; color: #90a4ae; }
.pl-qr-box { text-align: center; }
.pl-qr-img { max-width: 200px; border-radius: 8px; border: 1px solid #eee; margin: 8px 0; }
.pl-qr-pre { white-space: pre-wrap; word-break: break-all; background: #f7f9fc; border-radius: 8px; padding: 12px; font-size: 12px; max-height: 140px; overflow: auto; }
.pl-actions { display: flex; gap: 10px; justify-content: center; flex-wrap: wrap; margin-top: 12px; }
.pl-paid { text-align: center; padding: 10px 0; }
.pl-paid-icon { width: 52px; height: 52px; line-height: 52px; border-radius: 50%; background: #e8f5e9; color: #2e7d32; font-size: 28px; margin: 0 auto 8px; }
</style>
