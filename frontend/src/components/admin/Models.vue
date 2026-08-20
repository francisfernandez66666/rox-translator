<!-- ============================================================================
   components/admin/Models.vue — 引擎 · 模型配置（模型 + 路由 + 策略）
   职责：单供应商模型配置 + 多供应商路由策略（超管）+ 匹配策略参数
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>模型配置</h2>
    <div class="ad-chart-card">
      <h3>在线模型</h3>
      <label class="ad-label">API 地址</label>
      <input v-model="mForm.api_base" class="ad-input ad-wide" />
      <label class="ad-label">API Key（留空不修改）</label>
      <input v-model="mForm.api_key" class="ad-input ad-wide" />
      <label class="ad-label">模型名</label>
      <input v-model="mForm.model" class="ad-input ad-wide" />
      <button class="ad-btn" @click="saveModels">保存模型</button>
    </div>

    <div v-if="isSuper" class="ad-chart-card">
      <h3>模型路由策略</h3>
      <div class="ad-hint">多供应商路由：按权重选主模型，失败后按权重降序依次降级。留空=使用单供应商 Online* 配置。</div>
      <div v-for="(r, i) in routeForm" :key="i" class="ad-route-row">
        <input v-model="r.provider" placeholder="供应商 (siliconflow/bigmodel…)" class="ad-input" />
        <input v-model="r.api_base" placeholder="API Base (…/v1)" class="ad-input" style="flex:1" />
        <input v-model="r.api_key" placeholder="API Key" class="ad-input" style="flex:1" />
        <input v-model="r.model" placeholder="模型名" class="ad-input" />
        <input v-model.number="r.weight" type="number" placeholder="权重" class="ad-input ad-mini-w" />
        <button class="ad-btn-sm ad-btn-red" @click="routeForm.splice(i, 1)">删</button>
      </div>
      <div class="ad-row">
        <button class="ad-btn" @click="routeForm.push({ provider: '', api_base: '', api_key: '', model: '', weight: 0 })">+ 添加路由</button>
        <button class="ad-btn ad-btn-green" @click="saveRoutes">保存路由</button>
      </div>
      <p class="ad-hint">当前生效：{{ routeForm.length ? routeForm.length + ' 条路由（主模型 ' + (routeForm.find(r => r.weight > 0)?.model || routeForm[0]?.model || '—') + '）' : '未配置，使用平台默认单供应商' }}</p>
    </div>

    <div class="ad-chart-card">
      <h3>策略参数</h3>
      <label class="ad-label">相似度阈值 high_sim</label>
      <input v-model.number="pForm2.high_sim" type="number" step="0.01" class="ad-input" />
      <label class="ad-label">相似度阈值 med_sim</label>
      <input v-model.number="pForm2.med_sim" type="number" step="0.01" class="ad-input" />
      <label class="ad-label">evals 通过阈值</label>
      <input v-model.number="pForm2.evals_pass_threshold" type="number" class="ad-input" />
      <button class="ad-btn" @click="savePolicy">保存策略</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { adminModels, adminModelsSave, modelRoutes, modelRoutesSave, adminPolicy, adminPolicySave } from '@/api'
import { activeTenantId, isSuper } from './store'

const mForm = ref({ api_base: '', api_key: '', model: '' })
const pForm2 = ref({ high_sim: 0.9, med_sim: 0.75, evals_pass_threshold: 75 })
const routeForm = ref<any[]>([])

async function loadModels() {
  const r = await adminModels()
  if (r.success) mForm.value = (r as any).model
}
async function saveModels() {
  const r = await adminModelsSave(mForm.value)
  if (!r.success) { alert(r.message); return }
  alert('模型配置已保存')
}
async function loadPolicy() {
  const r = await adminPolicy()
  if (r.success) pForm2.value = (r as any).policy
}
async function savePolicy() {
  const r = await adminPolicySave(pForm2.value)
  if (!r.success) { alert(r.message); return }
  alert('策略已保存')
}
async function loadRoutes() {
  const r = await modelRoutes()
  if (r.success) routeForm.value = (r as any).routes || []
}
async function saveRoutes() {
  const valid = routeForm.value.filter((rt: any) => rt.api_base && rt.model)
  const r = await modelRoutesSave({ routes: valid })
  if (!r.success) { alert(r.message); return }
  alert('路由已保存并生效')
  await loadRoutes()
}

async function loadAll() {
  await Promise.all([loadModels(), loadPolicy()])
  if (isSuper.value) await loadRoutes()
}
onMounted(loadAll)
watch(activeTenantId, loadAll)
</script>