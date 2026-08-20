<!-- ============================================================================
   components/admin/Tickets.vue — 开放 · 工单工作台 + 审批台
   职责：翻译工单创建/运行/详情 + 待审批工单批准/驳回
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>工单工作台</h2>
    <div class="ad-row">
      <input v-model="tkForm.title" placeholder="标题" class="ad-input" />
      <textarea v-model="tkForm.source_text" placeholder="源文本（中文）" class="ad-input ad-textarea" />
      <input v-model="tkForm.target_langs" placeholder="目标语言，逗号分隔 (en,de)" class="ad-input" />
      <button class="ad-btn" @click="createTicket">创建工单</button>
    </div>
    <table class="ad-table">
      <thead><tr><th>单号</th><th>标题</th><th>状态</th><th>源文本</th><th>目标</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="t in tickets" :key="t.id">
          <td>{{ t.ticket_no }}</td><td>{{ t.title }}</td><td>{{ t.status }}</td>
          <td class="ad-ellipsis">{{ t.source_text }}</td><td>{{ t.target_langs }}</td>
          <td class="ad-td">
            <button class="ad-btn-sm" @click="runTicket(t)">运行流程</button>
            <button class="ad-btn-sm" @click="openTicket(t)">详情</button>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-if="ticketDetail" class="ad-ticket-detail">
      <h3>工单 {{ ticketDetail.ticket_no }} 详情</h3>
      <pre>{{ prettyJSON(ticketDetail.final_result) }}</pre>
      <div v-if="ticketDetail.states && ticketDetail.states.length" class="ad-states">
        <div v-for="st in ticketDetail.states" :key="st.id" class="ad-state">
          {{ st.step }} → {{ st.status }} <span v-if="st.payload">{{ st.payload }}</span>
        </div>
      </div>
    </div>

    <h2 style="margin-top: 32px">审批台</h2>
    <button class="ad-btn" @click="loadApproval">刷新</button>
    <div v-for="t in approvalTickets" :key="t.id" class="ad-approval">
      <div class="ad-approval-head">
        <b>{{ t.ticket_no }} — {{ t.title }}</b>
        <span class="ad-pkg-role">{{ t.status }}</span>
      </div>
      <p class="ad-approval-src">{{ t.source_text }}</p>
      <textarea :value="t.final_result" class="ad-input ad-textarea" readonly />
      <div class="ad-row">
        <button class="ad-btn ad-btn-green" @click="doApprove(t, 'approve')">批准</button>
        <input v-model="t._reason" placeholder="驳回原因" class="ad-input" />
        <input v-model="t._suggestion" placeholder="改进建议" class="ad-input" />
        <button class="ad-btn ad-btn-red" @click="doApprove(t, 'reject')">驳回</button>
      </div>
    </div>
    <div v-if="!approvalTickets.length" class="ad-hint">暂无待审批工单</div>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { ticketList, ticketCreate, ticketRun, ticketDetail, approveList, approveAction, type Ticket } from '@/api'
import { activeTenantId } from './store'
import { prettyJSON } from './ui'

const tickets = ref<Ticket[]>([])
const ticketDetail = ref<any>(null)
// 新建工单表单：标题/源文本/目标语言
const tkForm = ref({ title: '', source_text: '', target_langs: 'en' })
const approvalTickets = ref<any[]>([])

async function loadTickets() {
  const r = await ticketList(false)
  if (r.success) tickets.value = r.tickets || []
}
async function createTicket() {
  if (!tkForm.value.source_text) { alert('源文本必填'); return }
  const r = await ticketCreate(tkForm.value)
  if (!r.success) { alert(r.message); return }
  tkForm.value = { title: '', source_text: '', target_langs: 'en' }
  await loadTickets()
}
async function runTicket(t: Ticket) {
  const r = await ticketRun(t.id)
  if (!r.success) { alert(r.message); return }
  await loadTickets()
  alert(`工单 ${t.ticket_no} 流程执行完成`)
}
async function openTicket(t: Ticket) {
  const r = await ticketDetail(t.id)
  if (r.success) ticketDetail.value = { ...r.ticket, states: r.states }
}

async function loadApproval() {
  const r = await approveList()
  if (r.success) approvalTickets.value = (r as any).tickets || []
}
async function doApprove(t: any, action: 'approve' | 'reject') {
  const r = await approveAction(t.id, action, t._reason || '', t._suggestion || '', '')
  if (!r.success) { alert(r.message); return }
  t._reason = ''
  t._suggestion = ''
  await loadApproval()
  await loadTickets()
}

async function loadAll() {
  await Promise.all([loadTickets(), loadApproval()])
}
onMounted(loadAll)
watch(activeTenantId, loadAll)
</script>