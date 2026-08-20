<!-- ============================================================================
   components/admin/Org.vue — 组织 · 组织结构
   职责：组织/部门树 CRUD + 按组织下钻查看用户（管理结构展示层，根组织=租户）
   - 树形展示：租户（根）→ 子组织/部门 → 孙组织
   - 创建子组织 / 重命名 / 删除（子孙上移、用户回收至根组织）
   - 点击组织节点 → 右侧列出该组织及其子孙组织下的用户
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>组织结构</h2>
    <p class="ad-hint">根组织即当前租户，可在其下创建子组织/部门；删除组织时其下用户将回收至根组织。</p>

    <!-- ===== 新建子组织 ===== -->
    <div class="ad-row">
      <select v-model="parentId" class="ad-input">
        <option :value="0">根组织（租户下）</option>
        <option v-for="o in orgs" :key="o.id" :value="o.id">{{ orgPath(o) }}</option>
      </select>
      <input v-model="newName" placeholder="组织/部门名称" class="ad-input" @keydown.enter="createOrg" />
      <button class="ad-btn" @click="createOrg">新建组织</button>
    </div>

    <!-- ===== 左树右表：组织树 + 组织下用户 ===== -->
    <div class="ad-split">
      <!-- 组织树 -->
      <div class="ad-org-tree">
        <div class="ad-tree-item ad-tree-root" @click="selectOrg(0)">
          <span>🏢 {{ tenantName }}（根组织）</span>
          <span class="ad-tree-count">{{ allUsers.length }} 人</span>
        </div>
        <div
          v-for="o in flatTree"
          :key="o.id"
          :class="['ad-tree-item', selectedOrg === o.id ? 'on' : '']"
          :style="{ paddingLeft: (8 + o._depth * 18) + 'px' }"
          @click="selectOrg(o.id)"
        >
          <span>📁 {{ o.name }}</span>
          <span class="ad-tree-actions">
            <button class="ad-btn-xs" title="新建子组织" @click.stop="setParent(o.id)">+</button>
            <button class="ad-btn-xs" title="重命名" @click.stop="renameOrg(o)">✎</button>
            <button class="ad-btn-xs ad-btn-red" title="删除" @click.stop="deleteOrg(o)">✕</button>
          </span>
        </div>
      </div>

      <!-- 组织下用户（含子孙组织归集） -->
      <div class="ad-org-users">
        <h3>{{ selectedOrg === 0 ? '全部用户（根组织）' : `「${selectedOrgName}」及其子组织用户` }}</h3>
        <table class="ad-table">
          <thead><tr><th>ID</th><th>用户名</th><th>名称</th><th>所属组织</th><th>角色</th><th>最近登录</th></tr></thead>
          <tbody>
            <tr v-for="u in orgUserList" :key="u.id">
              <td>{{ u.id }}</td>
              <td>{{ u.username }}</td>
              <td>{{ u.display_name }}</td>
              <td>{{ u.org_name || '根组织' }}</td>
              <td>{{ roleName(u.role) }}</td>
              <td>{{ fmtTime(u.last_login_at) }}</td>
            </tr>
            <tr v-if="!orgUserList.length"><td colspan="6" class="ad-empty">该组织下暂无用户</td></tr>
          </tbody>
        </table>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { orgList, orgCreate, orgRename, orgDelete, orgUsers, type OrgInfo } from '@/api'
import { activeTenantId, tenantList, roleName } from './store'
import { fmtTime } from './ui'

// 组织扁平列表（用于下拉与组装树）
const orgs = ref<OrgInfo[]>([])
// 选中组织（0=根组织）
const selectedOrg = ref(0)
// 新建子组织的父组织
const parentId = ref(0)
const newName = ref('')
// 根组织下全部用户（用于根节点计数）
const allUsers = ref<any[]>([])
// 当前选中组织下用户
const orgUserList = ref<any[]>([])

// 当前生效租户名称（展示根组织）
const tenantName = computed(() => {
  const t = tenantList.value.find(x => x.id === activeTenantId.value)
  return t?.name || `组织 #${activeTenantId.value}`
})

// 组装树形结构（递归计算深度）
const flatTree = computed(() => {
  const byParent: Record<number, OrgInfo[]> = {}
  for (const o of orgs.value) {
    (byParent[o.parent_id] = byParent[o.parent_id] || []).push(o)
  }
  const out: (OrgInfo & { _depth: number })[] = []
  const walk = (pid: number, depth: number) => {
    for (const o of byParent[pid] || []) {
      ;(o as any)._depth = depth
      out.push(o as OrgInfo & { _depth: number })
      walk(o.id, depth + 1)
    }
  }
  walk(0, 0)
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
  if (r.success) orgs.value = r.orgs || []
  if (ru.success) allUsers.value = ru.users || []
  await loadOrgUsers()
}

// 按选中组织加载用户（含子孙归集）
async function loadOrgUsers() {
  const r = await orgUsers(selectedOrg.value || undefined)
  if (r.success) orgUserList.value = r.users || []
}

function selectOrg(id: number) {
  selectedOrg.value = id
  loadOrgUsers()
}

// 快捷：选中某组织作为新建子组织的父级
function setParent(id: number) {
  parentId.value = id
  newName.value = ''
}

async function createOrg() {
  if (!newName.value.trim()) { alert('请输入组织名称'); return }
  const r = await orgCreate({ name: newName.value.trim(), parent_id: parentId.value })
  if (!r.success) { alert(r.message); return }
  newName.value = ''
  await loadAll()
}

async function renameOrg(o: OrgInfo) {
  const name = prompt(`重命名「${o.name}」为：`, o.name)
  if (!name || !name.trim()) return
  const r = await orgRename(o.id, name.trim())
  if (!r.success) { alert(r.message); return }
  await loadAll()
}

async function deleteOrg(o: OrgInfo) {
  if (!confirm(`确认删除组织「${o.name}」？其子孙组织将上移，组织下用户将回收至根组织。`)) return
  const r = await orgDelete(o.id)
  if (!r.success) { alert(r.message); return }
  if (selectedOrg.value === o.id) selectedOrg.value = 0
  await loadAll()
}

onMounted(loadAll)
watch(activeTenantId, () => {
  selectedOrg.value = 0
  loadAll()
})
</script>