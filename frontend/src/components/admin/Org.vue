<!-- ============================================================================
   components/admin/Org.vue — 组织 · 组织结构
   职责：组织/部门树 CRUD + 按组织下钻查看用户（管理结构展示层）
   - 超管：平台树（平台根 → 各租户根组织 → 组织/部门 → 用户）
   - 租户管理员：本租户树（租户根 → 组织/部门 → 用户）
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('org.title') }}</h2>
    <p class="ad-hint">{{ t('org.treeHint') }}</p>

    <!-- ===== 新建组织/部门（部门管理员及以上） ===== -->
    <div class="ad-row" v-if="myLevel >= 2">
      <select v-model="parentId" class="ad-input">
        <option :value="0" v-if="myLevel >= 3">{{ t('org.rootOption') }}</option>
        <option v-for="o in flatTree" :key="o.id" :value="o.id">{{ orgPath(o) }}</option>
      </select>
      <input v-model="newName" :placeholder="t('org.namePlaceholder')" class="ad-input" @keydown.enter="createOrg" />
      <button class="ad-btn" @click="createOrg">{{ parentId === 0 ? t('org.create') : t('org.createDept') }}</button>
    </div>

    <!-- ===== 左树右表：组织树 + 组织下用户 ===== -->
    <div class="ad-split">
      <!-- 组织树 -->
      <div class="ad-org-tree">
        <div
          class="ad-tree-item ad-tree-root"
          :class="{ 'drag-over': canDrag && dragOrgId !== null }"
          @click="selectRoot"
          @dragover.prevent="canDrag && (dragOverId = 0)"
          @drop.prevent="onDropRoot"
        >
          <span>{{ rootOrgIcon }} {{ rootOrgName }}（{{ isPlatformView || isSuper ? t('org.typePlatform') : t('org.typeRoot') }}）</span>
          <span class="ad-tree-actions" v-if="myLevel >= 3 && !isPlatformView">
            <button class="ad-btn-xs" :title="t('org.renameRoot')" @click.stop="renameRootOrg">✎</button>
          </span>
        </div>
        <div
          v-for="o in flatTree"
          :key="o.id"
          :class="['ad-tree-item', selectedOrg === o.id ? 'on' : '', dragOverId === o.id ? 'drag-over' : '']"
          :style="{ paddingLeft: (8 + o._depth * 18) + 'px' }"
          @click="selectOrg(o)"
          :draggable="canDrag ? 'true' : 'false'"
          @dragstart="startDrag(o, $event)"
          @dragend="dragOrgId = null; dragOverId = null"
          @dragover.prevent="onDragOver(o)"
          @dragleave="dragOverId = null"
          @drop.prevent="onDropTo(o)"
        >
          <span class="drag-handle" v-if="canDrag">⠿</span>
          <span>{{ orgIcon(o) }} {{ o.name }}</span>
          <span class="ad-tree-actions">
            <button class="ad-btn-xs" :title="t('org.addChild')" @click.stop="setParent(o.id)">+</button>
            <button v-if="o.type !== 'root'" class="ad-btn-xs" :title="t('org.rename')" @click.stop="renameOrg(o)">✎</button>
            <button v-if="o.type !== 'root' && myLevel >= 3" class="ad-btn-xs ad-btn-red" :title="t('org.delete')" @click.stop="deleteOrg(o)">✕</button>
          </span>
        </div>
      </div>

      <!-- 组织下用户（含子孙组织归集） -->
      <div class="ad-org-users">
        <h3>{{ selectedOrg === 0 ? t('org.allUsersRoot') : tpl('org.usersInChildren', { name: selectedOrgName }) }}</h3>
        <div class="ad-hint">{{ t('org.usersHint') }}</div>
        <table class="ad-table">
          <thead><tr><th>{{ t('org.colId') }}</th><th>{{ t('org.colUsername') }}</th><th>{{ t('org.colName') }}</th><th>{{ t('org.colOrg') }}</th><th>{{ t('org.colRole') }}</th><th>{{ t('org.colLastLogin') }}</th><th>{{ t('org.colActions') }}</th></tr></thead>
          <tbody>
            <tr v-for="u in orgUserList" :key="u.id">
              <td>{{ u.id }}</td>
              <td>{{ u.username }}</td>
              <td><input :value="u.display_name" class="ad-mini" @change="editUser(u, 'display_name', ($event.target as HTMLInputElement).value)" /></td>
              <td>
                <select :value="u.org_id || 0" class="ad-mini" @change="editUser(u, 'org_id', ($event.target as HTMLSelectElement).value)">
                  <option :value="0">{{ t('org.rootOption') }}</option>
                  <option v-for="o in flatTree" :key="o.id" :value="o.id">{{ orgPath(o) }}</option>
                </select>
              </td>
              <td>
                <select :value="u.role" class="ad-mini" @change="editUser(u, 'role', ($event.target as HTMLSelectElement).value)">
                  <option v-for="r in roleOptions" :key="r" :value="r">{{ t('users.role.' + r) }}</option>
                </select>
              </td>
              <td>{{ fmtTime(u.last_login_at) }}</td>
              <td class="ad-td">
                <button class="ad-btn-sm" @click="resetPwd(u)">{{ t('org.resetPwd') }}</button>
                <button v-if="u.status === 'disabled'" class="ad-btn-sm" @click="setStatus(u, 'active')">{{ t('org.enable') }}</button>
                <button v-else class="ad-btn-sm ad-btn-red" @click="setStatus(u, 'disabled')">{{ t('org.disable') }}</button>
              </td>
            </tr>
            <tr v-if="!orgUserList.length"><td colspan="7" class="ad-empty">{{ t('org.noUsers') }}</td></tr>
          </tbody>
        </table>

        <!-- 开通用户 -->
        <div class="ad-chart-card" style="margin-top:16px">
          <h3>{{ tpl('org.addUser', { org: selectedOrg === 0 ? t('org.rootOrg') : selectedOrgName }) }}</h3>
          <div class="ad-row">
            <input v-model="nu.username" :placeholder="t('org.usernamePlaceholder')" class="ad-input ad-mini-w" />
            <input v-model="nu.password" :placeholder="t('org.passPlaceholder')" class="ad-input" />
            <input v-model="nu.display_name" :placeholder="t('org.displayNamePlaceholder')" class="ad-input" />
            <select v-model="nu.role" class="ad-input">
              <option v-for="r in roleOptions" :key="r" :value="r">{{ t('users.role.' + r) }}</option>
            </select>
            <button class="ad-btn ad-btn-green" :disabled="creating" @click="createUser">{{ creating ? t('org.creating') : t('org.addUserBtn') }}</button>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { t, tpl } from '@/i18n'
import { orgList, orgCreate, orgRename, orgMove, orgDelete, orgUsers, type OrgInfo } from '@/api'
import { adminUserCreate, adminUserUpdate, adminUserResetPassword } from '@/api'
import { activeTenantId, tenantList, isSuper, myLevel, roleOptions, roleName } from './store'
import { fmtTime } from './ui'

// 组织扁平列表（用于下拉与组装树）
const orgs = ref<OrgInfo[]>([])
// 根组织行（type='root'，名称可自定义；平台视图=平台根）
const rootOrg = ref<OrgInfo | null>(null)
// 是否平台视图（超管：平台根 → 各租户根 → 组织/部门）
const isPlatformView = ref(false)
// 拖拽权限：仅租户管理员及以上可拖拽调整层级
const canDrag = computed(() => myLevel.value >= 3 && !isPlatformView.value)
// 选中组织（0=根组织）
const selectedOrg = ref(0)
// 新建子组织的父组织
const parentId = ref(0)
// 新建组织的名称输入
const newName = ref('')
// 根组织下全部用户（用于根节点计数）
const allUsers = ref<any[]>([])
// 当前选中组织下用户
const orgUserList = ref<any[]>([])

// 开通用户表单
const creating = ref(false)
// 新用户信息：用户名/密码/显示名/角色
const nu = ref({ username: '', password: '', display_name: '', role: 'user' })

// 根组织名称（优先用根组织行的自定义名称，回退租户名）
const rootOrgName = computed(() => {
  if (rootOrg.value?.name) return rootOrg.value.name
  const t = tenantList.value.find(x => x.id === activeTenantId.value)
  return t?.name || tpl('org.orgHash', { id: activeTenantId.value })
})
// 根组织图标
const rootOrgIcon = computed(() => '🏢')
// 组织节点图标（按类型：根=🏢 组织=🏬 部门=🏷️）
function orgIcon(o: OrgInfo): string {
  if (o.type === 'dept') return '🏷️'
  if (o.type === 'org') return '🏬'
  return '🏢'
}

// 组装树形结构（递归计算深度）
// 平台视图：从平台根 ID 开始遍历（各租户根组织的 parent_id 已被后端改写为平台根 ID）；
// 租户视图：从 0 开始（根下直属组织 parent_id=0）。
const flatTree = computed(() => {
  const byParent: Record<number, OrgInfo[]> = {}
  for (const o of orgs.value) {
    (byParent[o.parent_id] = byParent[o.parent_id] || []).push(o)
  }
  const out: (OrgInfo & { _depth: number })[] = []
  // walk 深度优先遍历组织树：按父级分组递归收集节点并记录层级深度
  const walk = (pid: number, depth: number) => {
    for (const o of byParent[pid] || []) {
      ;(o as any)._depth = depth
      out.push(o as OrgInfo & { _depth: number })
      walk(o.id, depth + 1)
    }
  }
  walk(isPlatformView.value ? (rootOrg.value?.id || 0) : 0, 0)
  return out
})

// 组织路径（下拉展示父级路径）
function orgPath(o: OrgInfo): string {
  if (o.parent_id === 0) return o.name
  const parent = orgs.value.find(x => x.id === o.parent_id)
  return parent ? `${orgPath(parent)} / ${o.name}` : o.name
}

const selectedOrgName = computed(() => {
  if (selectedOrg.value === 0) return ''
  return orgs.value.find(x => x.id === selectedOrg.value)?.name || ''
})

// 加载组织 + 用户
async function loadAll() {
  const [r, ru] = await Promise.all([orgList(), orgUsers()])
  if (r.success) {
    isPlatformView.value = !!(r as any).platform
    rootOrg.value = r.root || null
    if (isPlatformView.value) {
      // 平台视图：保留各租户根组织（type=root），仅剔除平台根本身（它单独显示为根节点）
      orgs.value = (r.orgs || []).filter((o: any) => !(o.type === 'root' && rootOrg.value && o.id === rootOrg.value.id))
    } else {
      // 租户/部门视图：根组织行单独显示，列表排除全部 root 行
      orgs.value = (r.orgs || []).filter((o: any) => o.type !== 'root')
    }
  }
  if (ru.success) allUsers.value = ru.users || []
  await loadOrgUsers()
}

// selectRoot 选中根组织（加载根组织下全部用户）。
function selectRoot() {
  selectOrg(0)
}

// renameRootOrg 重命名根组织。
async function renameRootOrg() {
  if (!rootOrg.value) return
  const name = prompt(t('org.renameRoot'), rootOrg.value.name)
  if (!name || !name.trim()) return
  const r = await orgRename(rootOrg.value.id, name.trim())
  if (!r.success) { alert(r.message); return }
  await loadAll()
}

// 按选中组织加载用户（含子孙归集）
async function loadOrgUsers() {
  const r = await orgUsers(selectedOrg.value || undefined)
  if (r.success) orgUserList.value = r.users || []
}

// selectOrg 选择组织：更新选中态并加载该组织的用户列表。
// 参数 id: 目标组织 ID；无返回。
function selectOrg(id: number) {
  selectedOrg.value = id
  loadOrgUsers()
}

// 开通用户：在选中组织下创建账号（根组织=租户直属）
async function createUser() {
  if (!nu.value.username.trim() || nu.value.password.length < 6) {
    alert(t('org.userValidation'))
    return
  }
  creating.value = true
  try {
    const r = await adminUserCreate({
      username: nu.value.username.trim(),
      password: nu.value.password,
      display_name: nu.value.display_name.trim() || nu.value.username.trim(),
      role: nu.value.role,
      org_id: selectedOrg.value || undefined,
    })
    if (!r.success) { alert(r.message); return }
    alert(tpl('org.userCreated', { name: nu.value.username }))
    nu.value = { username: '', password: '', display_name: '', role: 'user' }
    await loadAll()
  } finally {
    creating.value = false
  }
}

// 重置用户密码
async function resetPwd(u: any) {
  const pwd = prompt(tpl('org.resetPwdPrompt', { name: u.username }))
  if (!pwd || pwd.length < 6) { alert(t('org.pwdMinLength')); return }
  const r = await adminUserResetPassword(u.id, pwd)
  if (!r.success) { alert(r.message); return }
  alert(t('org.pwdReset'))
}

// 启用/停用用户
async function setStatus(u: any, status: string) {
  const r = await adminUserUpdate(u.id, { display_name: u.display_name, role: u.role, status })
  if (!r.success) { alert(r.message); return }
  await loadAll()
}

// 行内编辑用户字段（display_name/org_id/role），org_id 转数字提交
async function editUser(u: any, field: string, val: string) {
  const data: any = { display_name: u.display_name, role: u.role, status: u.status, org_id: u.org_id || 0 }
  data[field] = field === 'org_id' ? Number(val) : val
  if (field === 'display_name' && !String(val).trim()) return
  const r = await adminUserUpdate(u.id, data)
  if (!r.success) { alert(r.message); return }
  await loadAll()
}

// 快捷：选中某组织作为新建子组织的父级
function setParent(id: number) {
  parentId.value = id
  newName.value = ''
}

async function createOrg() {
  if (!newName.value.trim()) { alert(t('org.nameRequired')); return }
  // 父=根组织(0) → 组织；父=组织/部门 → 部门
  const r = await orgCreate({ name: newName.value.trim(), parent_id: parentId.value, type: parentId.value === 0 ? 'org' : 'dept' })
  if (!r.success) { alert(r.message); return }
  newName.value = ''
  await loadAll()
}

async function renameOrg(o: OrgInfo) {
  const name = prompt(tpl('org.renamePrompt', { name: o.name }), o.name)
  if (!name || !name.trim()) return
  const r = await orgRename(o.id, name.trim())
  if (!r.success) { alert(r.message); return }
  await loadAll()
}

async function deleteOrg(o: OrgInfo) {
  if (!confirm(tpl('org.deleteConfirm', { name: o.name }))) return
  const r = await orgDelete(o.id)
  if (!r.success) { alert(r.message); return }
  if (selectedOrg.value === o.id) selectedOrg.value = 0
  await loadAll()
}

// ---- 拖拽调整层级（把组织/部门拖到另一节点下） ----
const dragOrgId = ref<number | null>(null) // 正在拖拽的节点 ID
const dragOverId = ref<number | null>(null) // 悬停高亮的目标节点 ID

// startDrag 记录被拖拽节点。
function startDrag(o: OrgInfo, e: DragEvent) {
  dragOrgId.value = o.id
  dragOverId.value = null
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}

// onDragOver 高亮可放置的目标节点。
function onDragOver(o: OrgInfo) {
  if (dragOrgId.value !== null && dragOrgId.value !== o.id) dragOverId.value = o.id
}

// onDropTo 把被拖节点移到目标节点下。
async function onDropTo(o: OrgInfo) {
  const id = dragOrgId.value
  dragOrgId.value = null
  dragOverId.value = null
  if (id === null || id === o.id) return
  const r = await orgMove(id, o.id)
  if (!r.success) { alert(r.message); return }
  await loadAll()
}

// onDropRoot 把被拖节点移回根组织下。
async function onDropRoot() {
  const id = dragOrgId.value
  dragOrgId.value = null
  dragOverId.value = null
  if (id === null) return
  const r = await orgMove(id, 0)
  if (!r.success) { alert(r.message); return }
  await loadAll()
}

onMounted(loadAll)
watch(activeTenantId, () => {
  selectedOrg.value = 0
  loadAll()
})
</script>