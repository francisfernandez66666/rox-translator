<!-- ============================================================================
   components/admin/Tenants.vue — 组织 · 租户管理（超管）
   职责：开通租户（可带管理员账号）+ 启停/删除 + 充值 + 数据导出/清除(GDPR)
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>组织管理</h2>
    <div class="ad-row">
      <input v-model="tForm.code" placeholder="编码 (如 bmw)" class="ad-input" />
      <input v-model="tForm.name" placeholder="名称" class="ad-input" />
      <input v-model="tForm.expires" type="date" class="ad-input" />
      <button class="ad-btn" @click="createTenant">开通组织</button>
    </div>
    <div class="ad-row">
      <input v-model="tForm.adminUser" placeholder="组织管理员用户名" class="ad-input" />
      <input v-model="tForm.adminPass" type="password" placeholder="初始密码" class="ad-input" />
    </div>
    <div class="ad-hint">权限 JSON（langs 允许语言，max_daily_chars 日字符上限）</div>
    <input v-model="tForm.permissions" placeholder='{"langs":["de","en"],"max_daily_chars":100000}' class="ad-input ad-wide" />
    <table class="ad-table">
      <thead><tr><th>ID</th><th>编码</th><th>名称</th><th>状态</th><th>有效期</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="t in tenants" :key="t.id">
          <td>{{ t.id }}</td><td>{{ t.code }}</td><td>{{ t.name }}</td>
          <td>{{ statusLabel(t.status) }}</td>
          <td>{{ t.expires_at || '永久' }}</td>
          <td class="ad-td">
            <button class="ad-btn-sm" @click="toggleTenant(t)">{{ t.status === 'active' ? '停用' : '启用' }}</button>
            <button v-if="t.id !== 1" class="ad-btn-sm ad-btn-red" @click="removeTenant(t)">删除</button>
            <button class="ad-btn-sm" @click="chargeTenant(t)">充值</button>
            <button class="ad-btn-sm" @click="exportTenant(t)">导出数据</button>
            <button v-if="t.id !== 1" class="ad-btn-sm ad-btn-red" @click="eraseTenant(t)">清除数据</button>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { tenantList as apiTenantList, tenantCreate, tenantSetStatus, tenantDelete, tenantExport, tenantErase, adminOrderCreate, adminOrderPay, API_BASE, getAuthToken } from '@/api'
import { loadTenants as refreshTenantStore } from './store'
import { statusLabel } from './ui'

const tenants = ref<any[]>([])
const tForm = ref({ code: '', name: '', expires: '', permissions: '{}', adminUser: '', adminPass: '' })

// 加载租户列表（与共享 store 同步，供壳组件租户切换器使用）
async function loadTenants() {
  const r = await apiTenantList()
  if (r.success) tenants.value = r.tenants || []
  await refreshTenantStore()
}

async function createTenant() {
  if (!tForm.value.code) { alert('组织编码必填'); return }
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
  if (!confirm(`确认删除组织「${t.name}」？其数据一并删除。`)) return
  const r = await tenantDelete(t.id)
  if (!r.success) alert(r.message)
  await loadTenants()
}

async function chargeTenant(t: any) {
  const tokens = prompt(`为「${t.name}」充值 token 数量：`)
  if (!tokens || Number(tokens) <= 0) return
  const r = await adminOrderCreate({ tenant_id: t.id, tokens: Number(tokens), money: 0 })
  if (!r.success) { alert(r.message); return }
  const o = (r as any).order
  await adminOrderPay(o.id)
  alert(`已充值 ${tokens} token`)
}

// 组织数据主权：导出 / GDPR 清除
async function exportTenant(t: any) {
  if (!confirm(`导出组织「${t.name}」全部数据（JSON）？`)) return
  const url = `${API_BASE}/api/tenant/export`
  const xhr = new XMLHttpRequest()
  xhr.open('POST', url, true)
  xhr.setRequestHeader('Content-Type', 'application/json')
  if (getAuthToken()) xhr.setRequestHeader('Authorization', `Bearer ${getAuthToken()}`)
  xhr.responseType = 'blob'
  xhr.onload = () => {
    if (xhr.status !== 200) { alert('导出失败'); return }
    const a = document.createElement('a')
    a.href = URL.createObjectURL(xhr.response)
    a.download = `tenant_${t.id}_${t.code}.json`
    a.click()
    URL.revokeObjectURL(a.href)
  }
  xhr.send(JSON.stringify({ id: t.id }))
}

async function eraseTenant(t: any) {
  if (!confirm(`确认清除组织「${t.name}」的全部业务数据？此操作不可恢复，请先导出备份。`)) return
  if (!confirm(`再次确认：${t.name} 的用户/订单/用量/审计/KB 将全部删除。`)) return
  const r = await tenantErase(t.id)
  if (!r.success) { alert(r.message); return }
  alert('组织业务数据已清除')
  await loadTenants()
}

onMounted(loadTenants)
</script>