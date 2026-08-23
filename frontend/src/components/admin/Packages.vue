<!-- ============================================================================
   components/admin/Packages.vue — 经营 · 商业包管理
   职责：超管维护付费包/增量包/免费体验包（句数/价格/有效期/启停）
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('packages.title') }}</h2>
    <p class="ad-hint">{{ t('packages.hint') }}</p>

    <!-- 新建商业包 -->
    <div class="ad-chart-card">
      <h3>{{ t('packages.createTitle') }}</h3>
      <div class="ad-row">
        <input v-model="form.code" :placeholder="t('packages.code')" class="ad-input ad-mini-w" />
        <input v-model="form.name" :placeholder="t('packages.name')" class="ad-input" />
        <select v-model="form.ptype" class="ad-input ad-mini-w">
          <option value="paid">{{ t('packages.type.paid') }}</option>
          <option value="increment">{{ t('packages.type.increment') }}</option>
          <option value="free">{{ t('packages.type.free') }}</option>
        </select>
        <input v-model.number="form.sentences" type="number" :placeholder="t('packages.sentences')" class="ad-input ad-mini-w" />
        <input v-model.number="form.price_money" type="number" step="0.01" :placeholder="t('packages.price')" class="ad-input ad-mini-w" />
        <input v-model.number="form.duration_days" type="number" :placeholder="t('packages.duration')" class="ad-input ad-mini-w" />
        <button class="ad-btn" @click="createPkg">{{ t('common.save') }}</button>
      </div>
    </div>

    <!-- 商业包列表 -->
    <div class="ad-chart-card">
      <h3>{{ t('packages.listTitle') }}</h3>
      <table class="ad-table">
        <thead>
          <tr>
            <th>code</th><th>{{ t('packages.name') }}</th><th>{{ t('packages.type') }}</th>
            <th>{{ t('packages.sentences') }}</th><th>{{ t('packages.price') }}</th><th>{{ t('packages.duration') }}</th>
            <th>{{ t('common.status') }}</th><th>{{ t('common.operations') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in pkgs" :key="p.id">
            <td>{{ p.code }}</td><td>{{ p.name }}</td>
            <td>{{ typeLabel(p.ptype) }}</td><td>{{ p.sentences }}</td><td>{{ p.price_money }}</td>
            <td>{{ p.duration_days }}</td>
            <td>
              <button class="ad-btn-sm" :class="p.enabled ? 'ad-btn-green' : ''" @click="togglePkg(p)">
                {{ p.enabled ? t('common.active') : t('common.disabled') }}
              </button>
            </td>
            <td class="ad-td">
              <button class="ad-btn-sm ad-btn-red" @click="deletePkg(p)">{{ t('common.cancel') }}</button>
            </td>
          </tr>
          <tr v-if="!pkgs.length"><td colspan="8" style="text-align:center;color:#999">{{ t('packages.noPackages') }}</td></tr>
        </tbody>
      </table>
    </div>

    <!-- 句数强制开关 + 试用句数配置（超管） -->
    <div class="ad-chart-card">
      <h3>{{ t('packages.enforceTitle') }}</h3>
      <div class="ad-hint">{{ t('packages.enforceHint') }}</div>
      <div class="ad-row">
        <label class="ad-switch" style="margin-right: 14px">
          <input type="checkbox" v-model="billingEnforced" @change="saveEnforce" />
          <span></span>
        </label>
        <span :style="{ color: billingEnforced ? '#2e7d32' : '#888', fontWeight: 600 }">
          {{ billingEnforced ? t('packages.enforceOn') : t('packages.enforceOff') }}
        </span>
      </div>
      <div class="ad-row" style="margin-top: 12px">
        <span class="ad-hint">{{ t('packages.trialLabel') }}</span>
        <input v-model.number="trialSentences" type="number" class="ad-input ad-mini-w" />
        <button class="ad-btn" @click="saveTrial">{{ t('common.save') }}</button>
      </div>
    </div>

    <!-- 支付模式（超管）：sdk / 静态码 / mock + 静态收款码配置 -->
    <div class="ad-chart-card">
      <h3>{{ t('packages.payModeTitle') }}</h3>
      <div class="ad-hint">{{ t('packages.payModeHint') }}</div>
      <div class="ad-row">
        <select v-model="payMode" class="ad-input ad-mini-w">
          <option value="mock">{{ t('packages.payMock') }}</option>
          <option value="sdk">{{ t('packages.paySdk') }}</option>
          <option value="static_qr">{{ t('packages.payStaticQR') }}</option>
        </select>
        <button class="ad-btn" @click="savePayMode">{{ t('common.save') }}</button>
      </div>
      <div v-if="payMode === 'static_qr'" style="margin-top: 12px">
        <div class="ad-hint">{{ t('packages.staticQRHint') }}</div>
        <div class="ad-row">
          <input v-model="staticQRImage" type="text" :placeholder="t('packages.staticQRPlaceholder')" class="ad-input" />
          <button class="ad-btn" @click="saveStaticQR">{{ t('common.save') }}</button>
        </div>
        <img v-if="isImageContent(staticQRImage)" :src="staticQRImage" alt="static-qr" style="max-width:200px;margin-top:10px;border:1px solid #e0e0e0;border-radius:8px" />
      </div>
    </div>

    <!-- 注册与触达（超管）：邮箱验证 / 注册审核 / 人机验证 / 群机器人 -->
    <div class="ad-chart-card">
      <h3>{{ t('packages.regNotifyTitle') }}</h3>
      <div class="ad-hint">{{ t('packages.regNotifyHint') }}</div>
      <div class="ad-row" style="margin-top: 8px">
        <label class="ad-switch"><input type="checkbox" v-model="regCfg.email_verify_enabled" /><span></span></label>
        <span class="ad-hint">{{ t('packages.emailVerify') }}</span>
      </div>
      <div class="ad-row">
        <label class="ad-switch"><input type="checkbox" v-model="regCfg.email_notify_enabled" /><span></span></label>
        <span class="ad-hint">{{ t('packages.emailNotify') }}</span>
      </div>
      <div class="ad-row">
        <label class="ad-switch"><input type="checkbox" v-model="regCfg.registration_review" /><span></span></label>
        <span class="ad-hint">{{ t('packages.regReview') }}</span>
      </div>
      <div class="ad-row">
        <span class="ad-hint">{{ t('packages.captchaProvider') }}</span>
        <select v-model="regCfg.captcha_provider" class="ad-input ad-mini-w">
          <option value="">{{ t('packages.captchaOff') }}</option>
          <option value="turnstile">Turnstile</option>
        </select>
      </div>
      <div class="ad-row" v-if="regCfg.captcha_provider === 'turnstile'">
        <input v-model="regCfg.captcha_site_key" :placeholder="t('packages.captchaSiteKey')" class="ad-input ad-wide" />
        <input v-model="regCfg.captcha_secret_key" type="password" :placeholder="t('packages.captchaSecretKey')" class="ad-input ad-wide" />
      </div>
      <div class="ad-row">
        <input v-model="regCfg.wecom_webhook_url" :placeholder="t('packages.wecomWebhook')" class="ad-input ad-wide" />
      </div>
      <div class="ad-row">
        <input v-model="regCfg.dingtalk_webhook_url" :placeholder="t('packages.dingtalkWebhook')" class="ad-input ad-wide" />
      </div>
      <button class="ad-btn ad-btn-green" style="margin-top: 8px" @click="saveRegCfg">{{ t('common.save') }}</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { adminPackages, adminPackageCreate, adminPackageUpdate, adminPackageDelete, adminPackageSettings, adminPackageSettingsSave } from '@/api'
import { t } from '@/i18n'

const pkgs = ref<any[]>([])
const billingEnforced = ref(false) // ★ 强制计费总开关（billing_enforced：token 实费扣减唯一开关）
const trialSentences = ref(100)
const payMode = ref('mock')

// 注册与触达配置（三期）：开关用 "1"/"0" 字符串与后端 system_config 对齐
const regCfg = ref<Record<string, string>>({
  email_verify_enabled: '0',
  email_notify_enabled: '0',
  registration_review: '0',
  captcha_provider: '',
  captcha_site_key: '',
  captcha_secret_key: '',
  wecom_webhook_url: '',
  dingtalk_webhook_url: '',
})
const staticQRImage = ref('')
const form = ref({ code: '', name: '', ptype: 'paid', sentences: 1000, price_money: 0, duration_days: 30 })

// typeLabel 套餐类型转中文标签
function typeLabel(p: string) {
  return { free: t('packages.type.free'), paid: t('packages.type.paid'), increment: t('packages.type.increment') }[p] || p
}

// isImageContent 判断二维码内容是否为图片（base64 或 http(s) 图片链接）
function isImageContent(s: string): boolean {
  if (!s) return false
  return s.startsWith('data:image') || s.startsWith('http://') || s.startsWith('https://') || /\.(png|jpe?g|gif|webp)/i.test(s)
}

// loadAll 加载套餐列表与全局套餐设置（强制安全句/试用额度/支付方式/静态码）
async function loadAll() {
  const r = await adminPackages()
  if (r.success) pkgs.value = (r as any).packages || []
  const cfg = await adminPackageSettings()
  if (cfg.success) {
    billingEnforced.value = (cfg as any).billing_enforced === '1'
    if ((cfg as any).trial_sentences) trialSentences.value = Number((cfg as any).trial_sentences)
    if ((cfg as any).pay_mode) payMode.value = (cfg as any).pay_mode
    if ((cfg as any).static_qr_image) staticQRImage.value = (cfg as any).static_qr_image
    // 回填注册与触达配置（captcha_secret_key 后端不回显，保持输入框为空表示不修改）
    const c = cfg as any
    for (const k of ['email_verify_enabled', 'email_notify_enabled', 'registration_review',
      'captcha_provider', 'captcha_site_key', 'wecom_webhook_url', 'dingtalk_webhook_url']) {
      if (c[k] !== undefined && c[k] !== '') regCfg.value[k] = c[k]
    }
  }
}

// saveRegCfg 保存注册与触达配置（开关转 "1"/"0"；secret_key 留空则不提交=不修改）
async function saveRegCfg() {
  const payload: Record<string, string> = {}
  for (const k of ['email_verify_enabled', 'email_notify_enabled', 'registration_review']) {
    payload[k] = regCfg.value[k] === '1' || regCfg.value[k] === true ? '1' : '0'
  }
  for (const k of ['captcha_provider', 'captcha_site_key', 'wecom_webhook_url', 'dingtalk_webhook_url']) {
    payload[k] = regCfg.value[k] || ''
  }
  if (regCfg.value.captcha_secret_key) payload.captcha_secret_key = regCfg.value.captcha_secret_key
  const r = await adminPackageSettingsSave(payload)
  if (!r.success) { alert(r.message); return }
  alert(t('packages.saved'))
}

// savePayMode 保存支付模式（mock/sdk/static_qr）
async function savePayMode() {
  const r = await adminPackageSettingsSave({ pay_mode: payMode.value })
  if (!r.success) { alert(r.message); return }
  alert(t('packages.saved'))
}

// saveStaticQR 保存静态收款二维码图片内容
async function saveStaticQR() {
  const r = await adminPackageSettingsSave({ static_qr_image: staticQRImage.value })
  if (!r.success) { alert(r.message); return }
  alert(t('packages.saved'))
}

// createPkg 校验必填项后创建套餐并重置表单、刷新列表
async function createPkg() {
  if (!form.value.code || !form.value.name || form.value.sentences <= 0) { alert(t('packages.fillRequired')); return }
  const r = await adminPackageCreate(form.value)
  if (!r.success) { alert(r.message); return }
  form.value = { code: '', name: '', ptype: 'paid', sentences: 1000, price_money: 0, duration_days: 30 }
  await loadAll()
}

// togglePkg 切换指定套餐启用/停用状态并刷新列表
async function togglePkg(p: any) {
  const r = await adminPackageUpdate({ id: p.id, enabled: p.enabled ? 0 : 1 })
  if (!r.success) { alert(r.message); return }
  await loadAll()
}

// deletePkg 弹确认后删除指定套餐并刷新列表
async function deletePkg(p: any) {
  if (!confirm(t('packages.confirmDelete'))) return
  const r = await adminPackageDelete(p.id)
  if (!r.success) { alert(r.message); return }
  await loadAll()
}

// saveEnforce 保存是否强制安全句检查开关
async function saveEnforce() {
  const r = await adminPackageSettingsSave({ billing_enforced: billingEnforced.value ? '1' : '0' })
  if (!r.success) { alert(r.message); return }
}

// saveTrial 保存试用翻译句数额度
async function saveTrial() {
  const r = await adminPackageSettingsSave({ trial_sentences: trialSentences.value })
  if (!r.success) { alert(r.message); return }
  alert(t('packages.saved'))
}

onMounted(loadAll)
</script>