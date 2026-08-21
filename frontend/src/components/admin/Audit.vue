<!-- ============================================================================
   components/admin/Audit.vue — 审计日志（租户管理员：本租户全部关键操作）
   超管的全量视图（含租户名/操作者）在平台总览 Overview 中展示。
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('audit.title') }}</h2>
    <p class="ad-hint">{{ t('audit.hint') }}</p>

    <div class="ad-row" style="margin-bottom:8px">
      <select v-model="fAction" class="ad-input ad-mini-w">
        <option value="">{{ t('audit.allActions') }}</option>
        <option v-for="a in actions" :key="a" :value="a">{{ a }}</option>
      </select>
      <input v-model="fFrom" type="date" class="ad-input ad-mini-w" />
      <span class="ad-hint">→</span>
      <input v-model="fTo" type="date" class="ad-input ad-mini-w" />
      <button class="ad-btn" @click="load">{{ t('common.refresh') }}</button>
      <button class="ad-btn-sm" @click="exportCsv">{{ t('audit.export') }}</button>
    </div>

    <table class="ad-table">
      <thead><tr><th>{{ t('overview.colTime') }}</th><th>{{ t('audit.operator') }}</th><th>{{ t('overview.colAction') }}</th><th>{{ t('overview.colResource') }}</th><th>{{ t('overview.colDetail') }}</th><th>{{ t('overview.colChange') }}</th></tr></thead>
      <tbody>
        <tr v-for="l in logs" :key="l.id">
          <td>{{ fmtTime(l.created_at) }}</td>
          <td>{{ l.username || l.user_id || '—' }}</td>
          <td>{{ l.action }}</td><td>{{ l.resource }}</td><td>{{ l.detail }}</td>
          <td class="ad-td">
            <span v-if="l.before_val && l.after_val" class="ad-diff">{{ tpl('overview.diffOldNew', { old: shortJSON(l.before_val), new: shortJSON(l.after_val) }) }}</span>
            <span v-else>—</span>
          </td>
        </tr>
        <tr v-if="!logs.length"><td colspan="6" class="ad-empty">{{ t('audit.empty') }}</td></tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { systemAudit, API_BASE, getAuthToken } from '@/api'
import { t, tpl } from '@/i18n'
import { fmtTime } from './ui'

const logs = ref<any[]>([])
const fAction = ref('')
const fFrom = ref('')
const fTo = ref('')
// 常见动作枚举（供筛选下拉；后端为自由字符串）
const actions = ['login', 'user_create', 'user_update', 'user_delete', 'user_reset_pwd',
  'org_create', 'org_rename', 'org_delete', 'kb_package_create', 'kb_package_status',
  'kb_entries_import', 'model_save', 'stage_models_save', 'package_subscribe']

async function load() {
  const qs = new URLSearchParams()
  if (fAction.value) qs.set('action', fAction.value)
  if (fFrom.value) qs.set('from', fFrom.value)
  if (fTo.value) qs.set('to', fTo.value)
  const sep = qs.toString() ? '&' : ''
  const r = await systemAudit()
  if (r.success) {
    let list = (r as any).logs || []
    // 前端二次过滤时间范围（接口已支持 action；from/to 由 URL 参数透传更优，这里兜底过滤）
    if (fFrom.value) list = list.filter((l: any) => l.created_at >= fFrom.value)
    if (fTo.value) list = list.filter((l: any) => l.created_at <= fTo.value + 'T23:59:59')
    void sep
    logs.value = list
  }
}

function shortJSON(s: string): string {
  try {
    const o = JSON.parse(s)
    const keys = Object.keys(o)
    return keys.slice(0, 3).map(k => `${k}=${o[k]}`).join(',') + (keys.length > 3 ? '…' : '')
  } catch { return s.length > 24 ? s.slice(0, 24) + '…' : s }
}

function exportCsv() {
  const url = `${API_BASE}/api/system/audit?export=csv`
  const a = document.createElement('a')
  a.href = url
  a.download = `audit_${new Date().toISOString().slice(0, 10)}.csv`
  a.click()
}

onMounted(load)
void getAuthToken
</script>