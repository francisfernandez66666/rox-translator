<!-- ============================================================================
   components/admin/Invites.vue — 组织 · 邀请码（超管）
   职责：生成邀请码（绑定租户或新建租户）+ 列表展示使用状态
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>邀请码管理</h2>
    <div class="ad-hint">绑定租户的邀请码：受邀用户加入该租户（普通用户）；未绑定租户的邀请码：受邀用户自助新建租户。</div>
    <div class="ad-row">
      <input v-model="invForm.code" placeholder="邀请码" class="ad-input" />
      <select v-model.number="invForm.tenant_id" class="ad-input">
        <option :value="0">(新建租户)</option>
        <option v-for="t in tenantList" :key="t.id" :value="t.id">{{ t.name }} (#{{ t.id }})</option>
      </select>
      <button class="ad-btn" @click="createInvite">生成邀请码</button>
    </div>
    <table class="ad-table">
      <thead><tr><th>邀请码</th><th>绑定租户</th><th>状态</th><th>使用者</th><th>创建时间</th><th>使用时间</th></tr></thead>
      <tbody>
        <tr v-for="c in invites" :key="c.id">
          <td>{{ c.code }}</td><td>{{ c.tenant_id > 0 ? '#' + c.tenant_id : '新建租户' }}</td>
          <td>{{ c.used ? '已使用' : '未使用' }}</td><td>{{ c.used_by || '—' }}</td>
          <td>{{ fmtTime(c.created_at) }}</td><td>{{ fmtTime(c.used_at) }}</td>
        </tr>
        <tr v-if="!invites.length"><td colspan="6" style="text-align:center;color:#999">暂无邀请码</td></tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { inviteCodes, inviteCodeCreate } from '@/api'
import { activeTenantId, tenantList } from './store'
import { fmtTime } from './ui'

const invites = ref<any[]>([])
const invForm = ref({ code: '', tenant_id: 0 })
async function loadInvites() {
  const r = await inviteCodes()
  if (r.success) invites.value = (r as any).codes || []
}
async function createInvite() {
  if (!invForm.value.code) { alert('邀请码必填'); return }
  const r = await inviteCodeCreate(invForm.value)
  if (!r.success) { alert(r.message); return }
  invForm.value = { code: '', tenant_id: 0 }
  await loadInvites()
}

onMounted(loadInvites)
watch(activeTenantId, loadInvites)
</script>