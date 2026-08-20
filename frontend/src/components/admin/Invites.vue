<!-- ============================================================================
   components/admin/Invites.vue — 组织 · 邀请码（超管）
   职责：生成邀请码（绑定租户或新建租户）+ 列表展示使用状态
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('invites.title') }}</h2>
    <div class="ad-hint">{{ t('invites.hint') }}</div>
    <div class="ad-row">
      <input v-model="invForm.code" :placeholder="t('invites.codePlaceholder')" class="ad-input" />
      <select v-model.number="invForm.tenant_id" class="ad-input">
        <option :value="0">{{ t('invites.newOrg') }}</option>
        <option v-for="t in tenantList" :key="t.id" :value="t.id">{{ t.name }} (#{{ t.id }})</option>
      </select>
      <button class="ad-btn" @click="createInvite">{{ t('invites.create') }}</button>
    </div>
    <table class="ad-table">
      <thead><tr><th>{{ t('invites.colCode') }}</th><th>{{ t('invites.colTenant') }}</th><th>{{ t('invites.colStatus') }}</th><th>{{ t('invites.colUsedBy') }}</th><th>{{ t('invites.colCreatedAt') }}</th><th>{{ t('invites.colUsedAt') }}</th></tr></thead>
      <tbody>
        <tr v-for="c in invites" :key="c.id">
          <td>{{ c.code }}</td><td>{{ c.tenant_id > 0 ? '#' + c.tenant_id : t('invites.newOrg') }}</td>
          <td>{{ c.used ? t('invites.used') : t('invites.unused') }}</td><td>{{ c.used_by || '—' }}</td>
          <td>{{ fmtTime(c.created_at) }}</td><td>{{ fmtTime(c.used_at) }}</td>
        </tr>
        <tr v-if="!invites.length"><td colspan="6" style="text-align:center;color:#999">{{ t('invites.empty') }}</td></tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { inviteCodes, inviteCodeCreate } from '@/api'
import { activeTenantId, tenantList } from './store'
import { fmtTime } from './ui'
import { t } from '@/i18n'

const invites = ref<any[]>([])
// 新建邀请码表单：邀请码内容/绑定租户 ID
const invForm = ref({ code: '', tenant_id: 0 })
async function loadInvites() {
  const r = await inviteCodes()
  if (r.success) invites.value = (r as any).codes || []
}
async function createInvite() {
  if (!invForm.value.code) { alert(t('invites.codeRequired')); return }
  const r = await inviteCodeCreate(invForm.value)
  if (!r.success) { alert(r.message); return }
  invForm.value = { code: '', tenant_id: 0 }
  await loadInvites()
}

onMounted(loadInvites)
watch(activeTenantId, loadInvites)
</script>