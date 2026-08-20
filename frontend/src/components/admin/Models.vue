<!-- ============================================================================
   components/admin/Models.vue — 引擎 · 模型配置（模型 + 路由 + 策略）
   职责：单供应商模型配置 + 多供应商路由策略（超管）+ 匹配策略参数
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('models.title') }}</h2>
    <div class="ad-chart-card">
      <h3>{{ t('models.onlineTitle') }}</h3>
      <label class="ad-label">{{ t('models.apiBase') }}</label>
      <input v-model="mForm.api_base" class="ad-input ad-wide" />
      <label class="ad-label">{{ t('models.apiKey') }}</label>
      <input v-model="mForm.api_key" class="ad-input ad-wide" />
      <label class="ad-label">{{ t('models.modelName') }}</label>
      <input v-model="mForm.model" class="ad-input ad-wide" />
      <button class="ad-btn" @click="saveModels">{{ t('models.saveModel') }}</button>
    </div>

    <div v-if="isSuper" class="ad-chart-card">
      <h3>{{ t('models.routingTitle') }}</h3>
      <div class="ad-hint">{{ t('models.routingHint') }}</div>
      <div v-for="(r, i) in routeForm" :key="i" class="ad-route-row">
        <input v-model="r.provider" :placeholder="t('models.providerPlaceholder')" class="ad-input" />
        <input v-model="r.api_base" :placeholder="t('models.apiBasePlaceholder')" class="ad-input" style="flex:1" />
        <input v-model="r.api_key" :placeholder="t('models.apiKeyPlaceholder')" class="ad-input" style="flex:1" />
        <input v-model="r.model" :placeholder="t('models.modelNamePlaceholder')" class="ad-input" />
        <input v-model.number="r.weight" type="number" :placeholder="t('models.weightPlaceholder')" class="ad-input ad-mini-w" />
        <button class="ad-btn-sm ad-btn-red" @click="routeForm.splice(i, 1)">{{ t('models.delete') }}</button>
      </div>
      <div class="ad-row">
        <button class="ad-btn" @click="routeForm.push({ provider: '', api_base: '', api_key: '', model: '', weight: 0 })">{{ t('models.addRoute') }}</button>
        <button class="ad-btn ad-btn-green" @click="saveRoutes">{{ t('models.saveRoutes') }}</button>
      </div>
      <p class="ad-hint">{{ routeForm.length ? tpl('models.routesActive', { count: routeForm.length, main: (routeForm.find(r => r.weight > 0)?.model || routeForm[0]?.model || '—') }) : t('models.routesNone') }}</p>
    </div>

    <div class="ad-chart-card">
      <h3>{{ t('models.policyTitle') }}</h3>
      <label class="ad-label">{{ t('models.highSim') }}</label>
      <input v-model.number="pForm2.high_sim" type="number" step="0.01" class="ad-input" />
      <label class="ad-label">{{ t('models.medSim') }}</label>
      <input v-model.number="pForm2.med_sim" type="number" step="0.01" class="ad-input" />
      <label class="ad-label">{{ t('models.evalsThreshold') }}</label>
      <input v-model.number="pForm2.evals_pass_threshold" type="number" class="ad-input" />
      <button class="ad-btn" @click="savePolicy">{{ t('models.savePolicy') }}</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { adminModels, adminModelsSave, modelRoutes, modelRoutesSave, adminPolicy, adminPolicySave } from '@/api'
import { activeTenantId, isSuper } from './store'
import { t, tpl } from '@/i18n'

// 主模型配置表单：API 基地址/密钥/模型名
const mForm = ref({ api_base: '', api_key: '', model: '' })
// 翻译策略参数：高/中相似度阈值、评测通过分
const pForm2 = ref({ high_sim: 0.9, med_sim: 0.75, evals_pass_threshold: 75 })
// 多供应商路由表（权重路由/降级链）
const routeForm = ref<any[]>([])

async function loadModels() {
  const r = await adminModels()
  if (r.success) mForm.value = (r as any).model
}
async function saveModels() {
  const r = await adminModelsSave(mForm.value)
  if (!r.success) { alert(r.message); return }
  alert(t('models.savedModels'))
}
async function loadPolicy() {
  const r = await adminPolicy()
  if (r.success) pForm2.value = (r as any).policy
}
async function savePolicy() {
  const r = await adminPolicySave(pForm2.value)
  if (!r.success) { alert(r.message); return }
  alert(t('models.savedPolicy'))
}
async function loadRoutes() {
  const r = await modelRoutes()
  if (r.success) routeForm.value = (r as any).routes || []
}
async function saveRoutes() {
  const valid = routeForm.value.filter((rt: any) => rt.api_base && rt.model)
  const r = await modelRoutesSave({ routes: valid })
  if (!r.success) { alert(r.message); return }
  alert(t('models.savedRoutes'))
  await loadRoutes()
}

async function loadAll() {
  await Promise.all([loadModels(), loadPolicy()])
  if (isSuper.value) await loadRoutes()
}
onMounted(loadAll)
watch(activeTenantId, loadAll)
</script>