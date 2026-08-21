<!-- ============================================================================
   components/admin/Models.vue — 引擎 · 模型配置（模型 + 路由 + 分阶段模型 + 策略）
   职责：单供应商模型配置 + 多供应商路由策略 + 分阶段模型（均超管）+ 匹配策略参数（租管）
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('models.title') }}</h2>

    <!-- 单模型配置（租户 BYOK + 超管全局） -->
    <div class="ad-chart-card">
      <h3>{{ t('models.onlineTitle') }}</h3>
      <div class="ad-hint">{{ t('models.onlineHint') }}</div>
      <div class="ad-row">
        <select v-model="preset" class="ad-input ad-mini-w" @change="applyPreset">
          <option value="">{{ t('models.presetPlaceholder') }}</option>
          <option value="openai">OpenAI (ChatGPT)</option>
          <option value="gemini">Google Gemini</option>
          <option value="deepseek">DeepSeek</option>
          <option value="siliconflow">SiliconFlow</option>
          <option value="zhipu">Zhipu GLM</option>
        </select>
      </div>
      <label class="ad-label">{{ t('models.apiBase') }}</label>
      <input v-model="mForm.api_base" class="ad-input ad-wide" />
      <label class="ad-label">{{ t('models.apiKey') }}</label>
      <input v-model="mForm.api_key" class="ad-input ad-wide" />
      <label class="ad-label">{{ t('models.modelName') }}</label>
      <input v-model="mForm.model" class="ad-input ad-wide" />
      <button class="ad-btn" @click="saveModels">{{ t('models.saveModel') }}</button>
    </div>

    <!-- 多供应商路由（租户 BYOK + 超管全局）：ChatGPT/Gemini 等 OpenAI 兼容端点 -->
    <div class="ad-chart-card">
      <h3>{{ t('models.routingTitle') }}</h3>
      <div class="ad-hint">{{ t('models.routingHint') }}</div>
      <div class="ad-row" style="margin-bottom:8px">
        <select v-model="routePreset" class="ad-input ad-mini-w" @change="applyRoutePreset">
          <option value="">{{ t('models.presetPlaceholder') }}</option>
          <option value="openai">OpenAI (ChatGPT)</option>
          <option value="gemini">Google Gemini</option>
          <option value="deepseek">DeepSeek</option>
          <option value="siliconflow">SiliconFlow</option>
          <option value="zhipu">Zhipu GLM</option>
        </select>
      </div>
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

    <div v-if="isSuper" class="ad-chart-card">
      <h3>{{ t('models.stageTitle') }}</h3>
      <div class="ad-hint">{{ t('models.stageHint') }}</div>
      <div v-for="(k, label) in stageDefs" :key="k" class="ad-stage-card">
        <div class="ad-stage-title">{{ label }}</div>
        <input v-model="stageForm[k].provider" :placeholder="t('models.stageProvider')" class="ad-input ad-wide" />
        <div class="ad-row">
          <input v-model="stageForm[k].api_base" :placeholder="t('models.stageApiBasePlaceholder')" class="ad-input" style="flex:1" />
          <input v-model="stageForm[k].model" :placeholder="t('models.stageModelPlaceholder')" class="ad-input" style="flex:1" />
        </div>
        <input v-model="stageForm[k].api_key" :placeholder="t('models.stageApiKeyPlaceholder')" class="ad-input ad-wide" />
      </div>
      <p class="ad-hint">{{ stageActiveCount ? tpl('models.stageActive', { count: stageActiveCount }) : t('models.stageNone') }}</p>
      <button class="ad-btn ad-btn-green" @click="saveStages">{{ t('models.saveStages') }}</button>
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
import { ref, computed, onMounted, watch } from 'vue'
import { adminModels, adminModelsSave, adminPolicy, adminPolicySave, stageModels, stageModelsSave } from '@/api'
import { activeTenantId, isSuper } from './store'
import { t, tpl } from '@/i18n'

// 主模型配置表单：API 基地址/密钥/模型名
const mForm = ref({ api_base: '', api_key: '', model: '' })
// 翻译策略参数：高/中相似度阈值、评测通过分
const pForm2 = ref({ high_sim: 0.9, med_sim: 0.75, evals_pass_threshold: 75 })
// 多供应商路由表（权重路由/降级链；租户 BYOK + 超管全局）
const routeForm = ref<any[]>([])
// 常用 LLM 供应商预设（OpenAI 兼容格式，含 ChatGPT/Gemini）
const providerPresets: Record<string, { api_base: string; model: string }> = {
  openai: { api_base: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
  gemini: { api_base: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-1.5-flash' },
  deepseek: { api_base: 'https://api.deepseek.com/v1', model: 'deepseek-chat' },
  siliconflow: { api_base: 'https://api.siliconflow.cn/v1', model: 'tencent/Hunyuan-MT-7B' },
  zhipu: { api_base: 'https://open.bigmodel.cn/api/paas/v4', model: 'glm-4-flash' },
}
// 单模型预设选择器
const preset = ref('')
const routePreset = ref('')

// 应用单模型预设：填入 api_base 与 model（Key 留空由用户填）
function applyPreset() {
  const p = providerPresets[preset.value]
  if (p) {
    mForm.value.api_base = p.api_base
    mForm.value.model = p.model
  }
}
// 应用路由预设：追加一条供应商路由（Key 留空由用户填）
function applyRoutePreset() {
  const p = providerPresets[routePreset.value]
  if (p) {
    routeForm.value.push({ provider: routePreset.value, api_base: p.api_base, api_key: '', model: p.model, weight: 0 })
    routePreset.value = ''
  }
}

// 分阶段模型：kb_match/ai_initial/evals/review
const stageDefs: Record<string, string> = {
  kb_match: t('models.stageKbMatch'),
  ai_initial: t('models.stageAiInitial'),
  evals: t('models.stageEvals'),
  review: t('models.stageReview'),
}
const stageForm = ref<Record<string, { provider: string; api_base: string; api_key: string; model: string }>>({})
for (const k of Object.keys(stageDefs)) {
  stageForm.value[k] = { provider: '', api_base: '', api_key: '', model: '' }
}
const stageActiveCount = computed(() => Object.values(stageForm.value).filter((s: any) => s.api_base && s.model).length)

async function loadModels() {
  const r = await adminModels()
  if (r.success) {
    mForm.value = (r as any).model
    routeForm.value = (r as any).routes || []
  }
}
async function saveModels() {
  const r = await adminModelsSave({ ...mForm.value, routes: routeForm.value })
  if (!r.success) { alert(r.message); return }
  alert(t('models.savedModels'))
  await loadModels()
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
async function saveRoutes() {
  const valid = routeForm.value.filter((rt: any) => rt.api_base && rt.model)
  const r = await adminModelsSave({ routes: valid })
  if (!r.success) { alert(r.message); return }
  alert(t('models.savedRoutes'))
  await loadModels()
}
async function loadStages() {
  const r = await stageModels()
  if (r.success) {
    const st = (r as any).stages || {}
    for (const k of Object.keys(stageDefs)) {
      stageForm.value[k] = { provider: st[k]?.provider || '', api_base: st[k]?.api_base || '', api_key: st[k]?.api_key || '', model: st[k]?.model || '' }
    }
  }
}
async function saveStages() {
  const r = await stageModelsSave(stageForm.value)
  if (!r.success) { alert(r.message); return }
  alert(t('models.savedStages'))
  await loadStages()
}

async function loadAll() {
  await loadPolicy()
  // 单模型 + 多供应商路由：租户管理员（BYOK）与超管都可配置
  await loadModels()
  if (isSuper.value) {
    await loadStages()
  }
}
onMounted(loadAll)
watch(activeTenantId, loadAll)
watch(isSuper, (v) => { if (v) loadAll() })
</script>
