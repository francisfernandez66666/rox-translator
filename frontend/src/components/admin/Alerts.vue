<!-- ============================================================================
   components/admin/Alerts.vue — 经营 · 监控告警
   职责：按状态过滤查看告警列表（余额阈值/熔断/错误率），一键解决告警
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('alerts.title') }}</h2>
    <div class="ad-row">
      <button class="ad-btn" @click="loadAlerts">{{ t('alerts.refresh') }}</button>
      <select v-model="alertStatus" class="ad-input" @change="loadAlerts">
        <option value="">{{ t('alerts.all') }}</option><option value="open">{{ t('alerts.open') }}</option><option value="resolved">{{ t('alerts.resolved') }}</option>
      </select>
    </div>
    <table class="ad-table">
      <thead><tr><th>{{ t('alerts.colLevel') }}</th><th>{{ t('alerts.colKind') }}</th><th>{{ t('alerts.colTenant') }}</th><th>{{ t('alerts.colContent') }}</th><th>{{ t('alerts.colStatus') }}</th><th>{{ t('alerts.colTime') }}</th><th></th></tr></thead>
      <tbody>
        <tr v-for="a in alerts" :key="a.id">
          <td>{{ a.level }}</td><td>{{ a.kind }}</td><td>#{{ a.tenant_id }}</td><td>{{ a.message }}</td>
          <td>{{ a.status === 'open' ? t('alerts.open') : t('alerts.resolved') }}</td><td>{{ fmtTime(a.created_at) }}</td>
          <td><button v-if="a.status === 'open'" class="ad-btn-sm" @click="resolveAlert(a)">{{ t('alerts.close') }}</button></td>
        </tr>
        <tr v-if="!alerts.length"><td colspan="7" style="text-align:center;color:#999">{{ t('alerts.empty') }}</td></tr>
      </tbody>
    </table>

    <!-- ===== 注册与触达（超管·自套餐面板迁入，2026-08-26 整合：告警与触达通道同域） ===== -->
    <div class="ad-chart-card" style="margin-top:16px">
      <h3>{{ t('packages.regNotifyTitle') }}</h3>
      <div class="ad-hint">{{ t('packages.regNotifyHint') }}</div>
      <div class="ad-row" style="margin-top:8px">
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
      <div class="ad-row"><input v-model="regCfg.wecom_webhook_url" :placeholder="t('packages.wecomWebhook')" class="ad-input ad-wide" /></div>
      <div class="ad-row"><input v-model="regCfg.dingtalk_webhook_url" :placeholder="t('packages.dingtalkWebhook')" class="ad-input ad-wide" /></div>
      <button class="ad-btn ad-btn-green" style="margin-top:8px" @click="saveRegCfg">{{ t('common.save') }}</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { systemAlerts, alertResolve, adminPackageSettings, adminPackageSettingsSave } from '@/api'
import { activeTenantId } from './store'
import { fmtTime } from './ui'
import { t } from '@/i18n'

const alerts = ref<any[]>([])
// 告警状态过滤条件（空=全部，open=未解决，resolved=已解决）
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

// ===== 注册与触达配置（开关用 "1"/"0" 字符串与 system_config 对齐） =====
const regCfg = ref<Record<string, string | boolean>>({
  email_verify_enabled: '0', email_notify_enabled: '0', registration_review: '0',
  captcha_provider: '', captcha_site_key: '', captcha_secret_key: '',
  wecom_webhook_url: '', dingtalk_webhook_url: '',
})
async function loadRegCfg() {
  const cfg = await adminPackageSettings()
  if (cfg.success) {
    const c = cfg as any
    for (const k of ['email_verify_enabled', 'email_notify_enabled', 'registration_review',
      'captcha_provider', 'captcha_site_key', 'wecom_webhook_url', 'dingtalk_webhook_url']) {
      if (c[k] !== undefined && c[k] !== '') regCfg.value[k] = c[k]
    }
  }
}
async function saveRegCfg() {
  const payload: Record<string, string> = {}
  for (const k of ['email_verify_enabled', 'email_notify_enabled', 'registration_review']) {
    payload[k] = String(regCfg.value[k]) === 'true' || regCfg.value[k] === '1' ? '1' : '0'
  }
  for (const k of ['captcha_provider', 'captcha_site_key', 'wecom_webhook_url', 'dingtalk_webhook_url']) {
    payload[k] = String(regCfg.value[k] || '')
  }
  if (regCfg.value.captcha_secret_key) payload.captcha_secret_key = String(regCfg.value.captcha_secret_key)
  await adminPackageSettingsSave(payload)
}

onMounted(() => { loadAlerts(); loadRegCfg() })
watch(activeTenantId, loadAlerts)
</script>