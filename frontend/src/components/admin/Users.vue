<!-- ============================================================================
   components/admin/Users.vue — 组织 · 账户管理
   职责：用户列表 + 创建用户 + 行内编辑（名称/角色）+ 启停 + 重置密码
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('users.title') }}</h2>
    <div class="ad-row">
      <input v-model="uForm.username" :placeholder="t('users.usernamePlaceholder')" class="ad-input" />
      <input v-model="uForm.password" :placeholder="t('users.passPlaceholder')" class="ad-input" />
      <input v-model="uForm.display_name" :placeholder="t('users.displayNamePlaceholder')" class="ad-input" />
      <select v-if="isSuper" v-model.number="uForm.tenant_id" class="ad-input" @change="onCascadeChange">
        <option v-for="t in tenantList" :key="t.id" :value="t.id">{{ tpl('users.orgItem', { id: t.id, code: t.code }) }}</option>
      </select>
      <select v-model.number="uForm.org_id" class="ad-input" @change="onCascadeChange">
        <option value="0">{{ t('org.rootOption') }}</option>
        <option v-for="o in cascadeOrgs" :key="o.id" :value="o.id">{{ o.type === 'root' ? '🏢 ' : '' }}{{ o.name }}</option>
      </select>
      <select v-model="uForm.role" class="ad-input">
        <option v-for="r in cascadeRoles" :key="r" :value="r">{{ t('users.role.' + r) }}</option>
      </select>
      <button class="ad-btn" @click="createUser">{{ t('users.create') }}</button>
    </div>
    <table class="ad-table">
      <thead><tr><th>{{ t('users.colId') }}</th><th>{{ t('users.colUsername') }}</th><th>{{ t('users.colName') }}</th><th>{{ t('users.colOrg') }}</th><th>{{ t('users.colRole') }}</th><th>{{ t('users.colStatus') }}</th><th>{{ t('users.colLastLogin') }}</th><th>{{ t('users.colActions') }}</th></tr></thead>
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
              <option v-for="r in roleOptions" :key="r" :value="r">{{ t('users.role.' + r) }}</option>
            </select>
          </td>
          <td>{{ u.status === 'active' ? t('users.enable') : u.status === 'disabled' ? t('users.disable') : u.status }}</td>
          <td>{{ fmtTime(u.last_login_at) }}</td>
          <td class="ad-td">
            <button class="ad-btn-sm" @click="resetPwd(u)">{{ t('users.resetPwd') }}</button>
            <button class="ad-btn-sm ad-btn-red" @click="toggleUser(u)">{{ u.status === 'active' ? t('users.disable') : t('users.enable') }}</button>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { t, tpl } from '@/i18n'
import { adminUsers, adminUserCreate, adminUserUpdate, adminUserResetPassword, orgList, type OrgInfo } from '@/api'
import { activeTenantId, tenantList, isSuper, myLevel } from './store'
import { fmtTime } from './ui'

const users = ref<any[]>([])
const orgs = ref<OrgInfo[]>([])
// 新建用户表单：用户名/密码/显示名/角色/归属租户与组织
const uForm = ref({ username: '', password: '', display_name: '', role: 'user', tenant_id: 1, org_id: 0 })

// 组织下拉选项：根组织 + 全部子组织
const orgOptions = computed(() => {
  const root = [{ id: 0, name: t('users.rootOrgOption') }]
  const children = orgs.value.map(o => ({ id: o.id, name: orgPath(o) }))
  return [...root, ...children]
})

// 组织路径（父级前缀，下拉中展示层级）
function orgPath(o: OrgInfo): string {
  if (o.parent_id === 0) return o.name
  const parent = orgs.value.find(x => x.id === o.parent_id)
  return parent ? `${orgPath(parent)} / ${o.name}` : o.name
}

// 用户所属组织名（表格展示；0=根组织）
function userOrgName(orgId: number): string {
  if (!orgId) return t('users.rootOrg')
  return orgs.value.find(o => o.id === orgId)?.name || tpl('users.orgHash', { id: orgId })
}

// 加载用户列表（当前生效租户）
async function loadUsers() {
  const r = await adminUsers()
  if (r.success) users.value = (r as any).users
}

// 加载组织树（供创建/编辑用户时归属选择）
async function loadOrgs() {
  const r = await orgList()
  if (r.success) orgs.value = r.orgs || []
}

// 创建用户（用户名/密码必填；可选角色/租户(超管)/所属组织）
async function createUser() {
  if (!uForm.value.username || !uForm.value.password) { alert(t('users.required')); return }
  const r = await adminUserCreate({ ...uForm.value })
  if (!r.success) { alert(r.message); return }
  uForm.value = { username: '', password: '', display_name: '', role: 'user', tenant_id: activeTenantId.value || 1, org_id: 0 }
  await loadUsers()
}

// 级联：可选部门随超管所选租户过滤；角色选项随部门层级收窄（与组织架构面板一致）
const cascadeOrgs = computed(() => {
  if (!isSuper.value) return orgs.value
  const tid = Number(uForm.value.tenant_id || 0)
  return orgs.value.filter((o: any) => o.tenant_id === tid)
})
const cascadeRoles = computed<string[]>(() => {
  const oid = Number(uForm.value.org_id || 0)
  if (!oid) {
    // 根级：租管及以上可任命租户管理员
    return myLevel.value >= 3 ? ['tenant_admin', 'dept_admin', 'user'] : ['user']
  }
  const org = orgs.value.find((x: any) => x.id === oid)
  if (org && org.type === 'root') {
    return myLevel.value >= 3 ? ['tenant_admin', 'dept_admin', 'user'] : ['user']
  }
  // 部门/子组织：部门管理员或普通用户
  return myLevel.value >= 2 ? ['dept_admin', 'user'] : ['user']
})
// 切换归属后校正角色
function onCascadeChange() {
  if (!cascadeRoles.value.includes(uForm.value.role)) {
    uForm.value.role = cascadeRoles.value[cascadeRoles.value.length - 1] || 'user'
  }
}

// 行内编辑：更新指定字段（org_id 转为数字提交，其余原样）
async function editUser(u: any, field: string, val: string) {
  const data: any = { display_name: u.display_name, role: u.role, status: u.status, org_id: u.org_id || 0 }
  if (field === 'org_id') data.org_id = Number(val)
  else data[field] = val
  const r = await adminUserUpdate(u.id, data)
  if (!r.success) alert(r.message)
  await loadUsers()
}

// 启停用户（active ⇄ disabled）
async function toggleUser(u: any) {
  const r = await adminUserUpdate(u.id, { display_name: u.display_name, role: u.role, status: u.status === 'active' ? 'disabled' : 'active', org_id: u.org_id || 0 })
  if (!r.success) alert(r.message)
  await loadUsers()
}

// 重置指定用户密码（弹窗输入新密码）
async function resetPwd(u: any) {
  const pwd = prompt(tpl('users.resetPwdPrompt', { name: u.username }))
  if (!pwd) return
  const r = await adminUserResetPassword(u.id, pwd)
  if (!r.success) alert(r.message)
}

// 挂载与租户切换时加载
onMounted(() => { loadUsers(); loadOrgs() })
watch(activeTenantId, () => { loadUsers(); loadOrgs() })
</script>