<!-- ============================================================================
   components/admin/Workflow.vue — 引擎 · 流程引擎 + evals 看板
   职责：工单翻译流程步骤启停 + evals 评估记录
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('workflow.title') }}</h2>
    <p class="ad-hint">{{ t('workflow.hint') }}</p>
    <div v-for="st in flowSteps" :key="st.key" class="ad-flow-row">
      <label class="ad-switch">
        <input type="checkbox" v-model="st.enable" />
        <span></span>
      </label>
      <span class="ad-flow-name">{{ st.name }}</span>
      <code class="ad-flow-key">{{ st.key }}</code>
    </div>
    <button class="ad-btn" @click="saveFlow">{{ t('workflow.saveFlow') }}</button>

    <h2 style="margin-top: 32px">{{ t('workflow.evalsTitle') }}</h2>
    <button class="ad-btn" @click="loadEvals">{{ t('workflow.refresh') }}</button>
    <table class="ad-table">
      <thead><tr><th>{{ t('workflow.colId') }}</th><th>{{ t('workflow.colTask') }}</th><th>{{ t('workflow.colLang') }}</th><th>{{ t('workflow.colScore') }}</th><th>{{ t('workflow.colStatus') }}</th><th>{{ t('workflow.colTime') }}</th><th>{{ t('workflow.colOutput') }}</th></tr></thead>
      <tbody>
        <tr v-for="r in evals" :key="r.id">
          <td>{{ r.id }}</td><td>{{ r.task_type }}</td><td>{{ r.model }}</td>
          <td>{{ r.total?.toFixed ? r.total.toFixed(1) : r.total }}</td>
          <td>{{ r.status }}</td><td>{{ fmtTime(r.created_at) }}</td>
          <td class="ad-ellipsis">{{ r.output_text }}</td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { flowConfig, flowSave, evalsList, type FlowStepItem } from '@/api'
import { activeTenantId } from './store'
import { fmtTime } from './ui'
import { t } from '@/i18n'

const flowSteps = ref<FlowStepItem[]>([])
async function loadFlow() {
  const r = await flowConfig()
  if (r.success) flowSteps.value = (r as any).steps
}
async function saveFlow() {
  const r = await flowSave(flowSteps.value)
  if (!r.success) { alert(r.message); return }
  alert(t('workflow.savedFlow'))
}

const evals = ref<any[]>([])
async function loadEvals() {
  const r = await evalsList()
  if (r.success) evals.value = (r as any).records
}

async function loadAll() {
  await Promise.all([loadFlow(), loadEvals()])
}
onMounted(loadAll)
watch(activeTenantId, loadAll)
</script>