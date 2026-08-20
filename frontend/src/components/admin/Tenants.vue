<!-- ============================================================================
   components/admin/Tenants.vue — 组织 · 租户管理（超管）
   职责：开通租户（可带管理员账号）+ 启停/删除 + 充值 + 数据导出/清除(GDPR)
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('tenants.title') }}</h2>
    <div class="ad-row">
      <input v-model="tForm.code" :placeholder="t('tenants.codePlaceholder')" class="ad-input" />
      <input v-model="tForm.name" :placeholder="t('tenants.namePlaceholder')" class="ad-input" />
      <input v-model="tForm.expires" type="date" class="ad-input" />
      <button class="ad-btn" @click="createTenant">{{ t('tenants.create') }}</button>
    </div>
    <div class="ad-row">
      <input v-model="tForm.adminUser" :placeholder="t('tenants.adminUserPlaceholder')" class="ad-input" />
      <input v-model="tForm.adminPass" type="password" :placeholder="t('tenants.initPassPlaceholder')" class="ad-input" />
    </div>
    <div class="ad-hint">{{ t('tenants.permissionsHint') }}</div>
    <input v-model="tForm.permissions" placeholder='{"langs":["de","en"],"max_daily_chars":100000}' class="ad-input ad-wide" />
    <table class="ad-table">
      <thead><tr><th>{{ t('tenants.colId') }}</th><th>{{ t('tenants.colCode') }}</th><th>{{ t('tenants.colName') }}</th><th>{{ t('tenants.colStatus') }}</th><th>{{ t('tenants.colExpires') }}</th><th>{{ t('tenants.colActions') }}</th></tr></thead>
      <tbody>
        <tr v-for="row in tenants" :key="row.id">
          <td>{{ row.id }}</td><td>{{ row.code }}</td><td>{{ row.name }}</td>
          <td>{{ row.status === 'active' ? t('tenants.enable') : row.status === 'disabled' ? t('tenants.disable') : t('tenants.expired') }}</td>
          <td>{{ row.expires_at || t('tenants.forever') }}</td>
          <td class="ad-td">
            <button class="ad-btn-sm" @click="toggleTenant(row)">{{ row.status === 'active' ? t('tenants.disable') : t('tenants.enable') }}</button>
            <button v-if="row.id !== 1" class="ad-btn-sm ad-btn-red" @click="removeTenant(row)">{{ t('tenants.delete') }}</button>
            <button class="ad-btn-sm" @click="chargeTenant(row)">{{ t('tenants.charge') }}</button>
            <button class="ad-btn-sm" @click="exportTenant(row)">{{ t('tenants.exportData') }}</button>
            <button v-if="row.id !== 1" class="ad-btn-sm ad-btn-red" @click="eraseTenant(row)">{{ t('tenants.eraseData') }}</button>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { t, tpl } from '@/i18n'
import { tenantList as apiTenantList, tenantCreate, tenantSetStatus, tenantDelete, tenantExport, tenantErase, adminOrderCreate, adminOrderPay, API_BASE, getAuthToken } from '@/api'
import { loadTenants as refreshTenantStore } from './store'

const tenants = ref<any[]>([])
// 新建租户表单：编码/名称/过期时间/权限 JSON/超管用户名密码
const tForm = ref({ code: '', name: '', expires: '', permissions: '{}', adminUser: '', adminPass: '' })

// 加载租户列表（与共享 store 同步，供壳组件租户切换器使用）
async function loadTenants() {
  const r = await apiTenantList()
  if (r.success) tenants.value = r.tenants || []
  await refreshTenantStore()
}

async function createTenant() {
  if (!tForm.value.code) { alert(t('tenants.codeRequired')); return }
  const r = await tenantCreate({
    code: tForm.value.code, name: tForm.value.name,
    expires_at: tForm.value.expires, permissions: tForm.value.permissions,
    admin_user: tForm.value.adminUser, admin_pass: tForm.value.adminPass,
  })
  if (!r.success) { alert(r.message); return }
  tForm.value = { code: '', name: '', expires: '', permissions: '{}', adminUser: '', adminPass: '' }
  await loadTenants()
}

async function toggleTenant(t: any) {
  const r = await tenantSetStatus(t.id, t.status === 'active' ? 'disabled' : 'active')
  if (!r.success) alert(r.message)
  await loadTenants()
}

async function removeTenant(t: any) {
  if (!confirm(tpl('tenants.deleteConfirm', { name: t.name }))) return
  const r = await tenantDelete(t.id)
  if (!r.success) alert(r.message)
  await loadTenants()
}

async function chargeTenant(t: any) {
  const tokens = prompt(tpl('tenants.chargePrompt', { name: t.name }))
  if (!tokens || Number(tokens) <= 0) return
  const r = await adminOrderCreate({ tenant_id: t.id, tokens: Number(tokens), money: 0 })
  if (!r.success) { alert(r.message); return }
  // 下单成功后自动模拟支付（mock 渠道），完成充值入账
  const o = (r as any).order
  await adminOrderPay(o.id)
  alert(tpl('tenants.charged', { tokens }))
}

// 组织数据主权：导出 / GDPR 清除
async function exportTenant(t: any) {
  if (!confirm(tpl('tenants.exportConfirm', { name: t.name }))) return
  const url = `${API_BASE}/api/tenant/export`
  const xhr = new XMLHttpRequest()
  xhr.open('POST', url, true)
  xhr.setRequestHeader('Content-Type', 'application/json')
  if (getAuthToken()) xhr.setRequestHeader('Authorization', `Bearer ${getAuthToken()}`)
  xhr.responseType = 'blob'
  xhr.onload = () => {
    if (xhr.status !== 200) { alert(t('tenants.exportFailed')); return }
    const a = document.createElement('a')
    a.href = URL.createObjectURL(xhr.response)
    a.download = `tenant_${t.id}_${t.code}.json`
    a.click()
    URL.revokeObjectURL(a.href)
  }
  xhr.send(JSON.stringify({ id: t.id }))
}

async function eraseTenant(t: any) {
  if (!confirm(tpl('tenants.eraseConfirm', { name: t.name }))) return
  if (!confirm(tpl('tenants.eraseConfirm2', { name: t.name }))) return
  const r = await tenantErase(t.id)
  if (!r.success) { alert(r.message); return }
  alert(t('tenants.erased'))
  await loadTenants()
}

onMounted(loadTenants)
</script>