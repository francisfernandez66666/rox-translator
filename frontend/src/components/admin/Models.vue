<!-- ============================================================================
   components/admin/Models.vue — 引擎 · 模型配置（模型 + 路由 + 分阶段模型 + 策略）
   职责：单供应商模型配置 + 多供应商路由策略 + 分阶段模型（均超管）+ 匹配策略参数（租管）
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('models.title') }}</h2>

    <!-- 超管：业务五阶段模型配置（每卡带供应商预设模板） -->
    <template v-if="isSuper">
      <div v-for="st in stageCards" :key="st.key" class="ad-chart-card">
        <h3>{{ st.title }}</h3>
        <div class="ad-hint">{{ st.hint }}</div>
        <div class="ad-row" style="margin-bottom:8px">
          <select v-model="stForm[st.key].preset" class="ad-input ad-mini-w" @change="applyStagePreset(st.key)">
            <option value="">{{ t('models.presetPlaceholder') }}</option>
            <option value="openai">OpenAI (ChatGPT)</option>
            <option value="gemini">Google Gemini</option>
            <option value="deepseek">DeepSeek</option>
            <option value="siliconflow">SiliconFlow</option>
            <option value="zhipu">Zhipu GLM</option>
          </select>
          <span v-if="stActive(st.key)" class="ad-hint" style="color:#1a7f37">✓ {{ t('models.stageConfigured') }}</span>
        </div>
        <label class="ad-label">{{ t('models.apiBase') }}</label>
        <input v-model="stForm[st.key].api_base" class="ad-input ad-wide" :placeholder="t('models.apiBasePlaceholder')" />
        <label class="ad-label">{{ t('models.apiKey') }}</label>
        <input v-model="stForm[st.key].api_key" class="ad-input ad-wide" :placeholder="t('models.stageApiKeyKeep')" />
        <label class="ad-label">{{ t('models.modelName') }}</label>
        <input v-model="stForm[st.key].model" class="ad-input ad-wide" :placeholder="t('models.modelNamePlaceholder')" />
      </div>
      <div class="ad-row">
        <button class="ad-btn ad-btn-green" @click="saveStages">{{ t('models.saveStages') }}</button>
        <span class="ad-hint">{{ stageHint }}</span>
      </div>
    </template>

    <!-- 租户 BYOK 单模型配置 -->
    <div v-if="!isSuper" class="ad-chart-card">
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

    <!-- 多供应商路由（租户 BYOK）：ChatGPT/Gemini 等 OpenAI 兼容端点 -->
    <div v-if="!isSuper" class="ad-chart-card">
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

// 业务五阶段（面向翻译流程的模型配置；每阶段独立端点/密钥/模型）
const stageCards = [
  { key: 'ai_initial', title: t('models.s5Initial'), hint: t('models.s5InitialHint') },
  { key: 'kb_embed', title: t('models.s5Embed'), hint: t('models.s5EmbedHint') },
  { key: 'initial_evals', title: t('models.s5InitialEvals'), hint: t('models.s5InitialEvalsHint') },
  { key: 'review', title: t('models.s5Review'), hint: t('models.s5ReviewHint') },
  { key: 'review_evals', title: t('models.s5ReviewEvals'), hint: t('models.s5ReviewEvalsHint') },
]
interface StageCfg { preset: string; provider: string; api_base: string; api_key: string; model: string }
const stForm = ref<Record<string, StageCfg>>({})
for (const c of stageCards) {
  stForm.value[c.key] = { preset: '', provider: c.key, api_base: '', api_key: '', model: '' }
}
// 应用供应商预设：自动填 API 地址 + 模型名（Key 留给用户填）
function applyStagePreset(key: string) {
  const p = providerPresets[stForm.value[key].preset]
  if (p) {
    stForm.value[key].api_base = p.api_base
    stForm.value[key].model = key === 'kb_embed' ? 'embedding-2' : p.model
  }
}
// 阶段是否已配置（有地址+模型名）
function stActive(key: string) {
  return !!(stForm.value[key].api_base && stForm.value[key].model)
}
const stageHint = computed(() => {
  const n = stageCards.filter(c => stActive(c.key)).length
  return n ? tpl('models.stageActive', { count: n }) : t('models.stageNone')
})

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
// 加载五阶段配置（掩码 Key 回显；preset 字段不落库）
async function loadStages() {
  const r = await stageModels()
  if (r.success) {
    const st = (r as any).stages || {}
    for (const c of stageCards) {
      const s = st[c.key] || {}
      stForm.value[c.key] = { preset: '', provider: c.key, api_base: s.api_base || '', api_key: s.api_key || '', model: s.model || '' }
    }
  }
}
// 保存五阶段：剥离前端专有字段，仅提交 provider/api_base/api_key/model
async function saveStages() {
  const payload: Record<string, any> = {}
  for (const c of stageCards) {
    const { preset, ...rest } = stForm.value[c.key]
    void preset
    payload[c.key] = rest
  }
  const r = await stageModelsSave(payload)
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
