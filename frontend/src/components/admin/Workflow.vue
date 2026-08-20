<!-- ============================================================================
   components/admin/Workflow.vue — 引擎 · 流程引擎 + evals 看板
   职责：工单翻译流程步骤启停 + evals 评估记录
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>流程引擎设置</h2>
    <p class="ad-hint">工单翻译流程步骤启停。关闭某步则跳过（审批关闭后工单翻译完成后直接完成）。</p>
    <div v-for="st in flowSteps" :key="st.key" class="ad-flow-row">
      <label class="ad-switch">
        <input type="checkbox" v-model="st.enable" />
        <span></span>
      </label>
      <span class="ad-flow-name">{{ st.name }}</span>
      <code class="ad-flow-key">{{ st.key }}</code>
    </div>
    <button class="ad-btn" @click="saveFlow">保存流程配置</button>

    <h2 style="margin-top: 32px">evals 评估看板</h2>
    <button class="ad-btn" @click="loadEvals">刷新</button>
    <table class="ad-table">
      <thead><tr><th>ID</th><th>任务</th><th>语言</th><th>总分</th><th>状态</th><th>时间</th><th>译文</th></tr></thead>
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

const flowSteps = ref<FlowStepItem[]>([])
async function loadFlow() {
  const r = await flowConfig()
  if (r.success) flowSteps.value = (r as any).steps
}
async function saveFlow() {
  const r = await flowSave(flowSteps.value)
  if (!r.success) { alert(r.message); return }
  alert('流程配置已保存')
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