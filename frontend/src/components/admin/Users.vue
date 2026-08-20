<!-- ============================================================================
   components/admin/Users.vue — 组织 · 账户管理
   职责：用户列表 + 创建用户 + 行内编辑（名称/角色）+ 启停 + 重置密码
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>账户管理</h2>
    <div class="ad-row">
      <input v-model="uForm.username" placeholder="用户名" class="ad-input" />
      <input v-model="uForm.password" placeholder="初始密码" class="ad-input" />
      <input v-model="uForm.display_name" placeholder="显示名称" class="ad-input" />
      <select v-model="uForm.role" class="ad-input">
        <option v-for="r in roleOptions" :key="r" :value="r">{{ r }}</option>
      </select>
      <select v-if="isSuper" v-model="uForm.tenant_id" class="ad-input">
        <option v-for="t in tenantList" :key="t.id" :value="t.id">组织 {{ t.id }} ({{ t.code }})</option>
      </select>
      <select v-model="uForm.org_id" class="ad-input">
        <option v-for="o in orgOptions" :key="o.id" :value="o.id">{{ o.name }}</option>
      </select>
      <button class="ad-btn" @click="createUser">创建</button>
    </div>
    <table class="ad-table">
      <thead><tr><th>ID</th><th>用户名</th><th>名称</th><th>组织</th><th>角色</th><th>状态</th><th>最近登录</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="u in users" :key="u.id">
          <td>{{ u.id }}</td><td>{{ u.username }}</td>
          <td><input :value="u.display_name" class="ad-mini" @change="editUser(u, 'display_name', ($event.target as HTMLInputElement).value)" /></td>
          <td>
            <select :value="u.org_id || 0" class="ad-mini" @change="editUser(u, 'org_id', ($event.target as HTMLSelectElement).value)">
              <option v-for="o in orgOptions" :key="o.id" :value="o.id">{{ o.name }}</option>
            </select>
          </td>
          <td>
            <select :value="u.role" class="ad-mini" @change="editUser(u, 'role', ($event.target as HTMLSelectElement).value)">
              <option v-for="r in roleOptions" :key="r" :value="r">{{ r }}</option>
            </select>
          </td>
          <td>{{ u.status }}</td>
          <td>{{ fmtTime(u.last_login_at) }}</td>
          <td class="ad-td">
            <button class="ad-btn-sm" @click="resetPwd(u)">重置密码</button>
            <button class="ad-btn-sm ad-btn-red" @click="toggleUser(u)">{{ u.status === 'active' ? '停用' : '启用' }}</button>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { adminUsers, adminUserCreate, adminUserUpdate, adminUserResetPassword, orgList, type OrgInfo } from '@/api'
import { activeTenantId, tenantList, isSuper, roleOptions } from './store'
import { fmtTime } from './ui'

const users = ref<any[]>([])
const orgs = ref<OrgInfo[]>([])
const uForm = ref({ username: '', password: '', display_name: '', role: 'user', tenant_id: 1, org_id: 0 })

// 组织下拉选项：根组织 + 全部子组织
const orgOptions = computed(() => {
  const root = [{ id: 0, name: '根组织（未分配）' }]
  const children = orgs.value.map(o => ({ id: o.id, name: orgPath(o) }))
  return [...root, ...children]
})

// 组织路径（父级前缀）
function orgPath(o: OrgInfo): string {
  if (o.parent_id === 0) return o.name
  const parent = orgs.value.find(x => x.id === o.parent_id)
  return parent ? `${orgPath(parent)} / ${o.name}` : o.name
}

// 用户所属组织名（展示）
function userOrgName(orgId: number): string {
  if (!orgId) return '根组织'
  return orgs.value.find(o => o.id === orgId)?.name || `组织 #${orgId}`
}

async function loadUsers() {
  const r = await adminUsers()
  if (r.success) users.value = (r as any).users
}
async function loadOrgs() {
  const r = await orgList()
  if (r.success) orgs.value = r.orgs || []
}
async function createUser() {
  if (!uForm.value.username || !uForm.value.password) { alert('用户名和密码必填'); return }
  const r = await adminUserCreate({ ...uForm.value })
  if (!r.success) { alert(r.message); return }
  uForm.value = { username: '', password: '', display_name: '', role: 'user', tenant_id: activeTenantId.value || 1, org_id: 0 }
  await loadUsers()
}
async function editUser(u: any, field: string, val: string) {
  const data: any = { display_name: u.display_name, role: u.role, status: u.status, org_id: u.org_id || 0 }
  if (field === 'org_id') data.org_id = Number(val)
  else data[field] = val
  const r = await adminUserUpdate(u.id, data)
  if (!r.success) alert(r.message)
  await loadUsers()
}
async function toggleUser(u: any) {
  const r = await adminUserUpdate(u.id, { display_name: u.display_name, role: u.role, status: u.status === 'active' ? 'disabled' : 'active', org_id: u.org_id || 0 })
  if (!r.success) alert(r.message)
  await loadUsers()
}
async function resetPwd(u: any) {
  const pwd = prompt(`为 ${u.username} 设置新密码：`)
  if (!pwd) return
  const r = await adminUserResetPassword(u.id, pwd)
  if (!r.success) alert(r.message)
}

onMounted(() => { loadUsers(); loadOrgs() })
watch(activeTenantId, () => { loadUsers(); loadOrgs() })
</script>