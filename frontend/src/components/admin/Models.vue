<!-- ============================================================================
   components/admin/Models.vue — 引擎 · 模型配置（统一网关路由 + 分阶段模型 + 策略）
   职责（★ 2026-08-26 BYOK 移除改造）：
     - 平台统一网关：所有 LLM 调用一律经平台出站，租户侧无任何模型配置入口，
       token 定价权 100% 归平台——原「租户 BYOK 单模型 / 多供应商路由」两大死区块已删除；
     - 全局网关路由卡：超管维护平台唯一模型入口（主路由 + 多供应商降级链）；
     - 分阶段模型：初翻/Embedding/评估/审校各阶段独立端点（超管）；
     - 匹配策略参数：高/中相似度阈值与评估通过分（超管，经 X-Tenant-ID 切换生效租户）。
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('models.title') }}</h2>

    <!-- 平台统一网关 · 全局路由配置（本面板仅超管可见；密钥以掩码回显，留掩码=不修改） -->
    <div class="ad-chart-card">
      <h3>{{ t('models.routingTitle') }}</h3>
      <div class="ad-hint">{{ t('models.onlineHint') }}</div>
      <!-- 供应商预设下拉：一键填入 api_base 与 model（Key 需手工填写） -->
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
      <!-- 主路由（单模型字段）：保存时合并为权重最高的主路由 -->
      <label class="ad-label">{{ t('models.apiBase') }}</label>
      <input v-model="mForm.api_base" class="ad-input ad-wide" :placeholder="t('models.apiBasePlaceholder')" />
      <label class="ad-label">{{ t('models.apiKey') }}</label>
      <input v-model="mForm.api_key" class="ad-input ad-wide" :placeholder="t('models.apiKeyPlaceholder')" />
      <label class="ad-label">{{ t('models.modelName') }}</label>
      <input v-model="mForm.model" class="ad-input ad-wide" :placeholder="t('models.modelNamePlaceholder')" />
      <!-- 备用路由列表：按权重降序构成主模型失败后的降级链 -->
      <div v-for="(r, i) in routeForm" :key="i" class="ad-route-row">
        <input v-model="r.provider" :placeholder="t('models.providerPlaceholder')" class="ad-input" />
        <input v-model="r.api_base" :placeholder="t('models.apiBasePlaceholder')" class="ad-input" style="flex:1" />
        <input v-model="r.api_key" :placeholder="t('models.apiKeyPlaceholder')" class="ad-input" style="flex:1" />
        <input v-model="r.model" :placeholder="t('models.modelNamePlaceholder')" class="ad-input" />
        <input v-model.number="r.weight" type="number" :placeholder="t('models.weightPlaceholder')" class="ad-input ad-mini-w" />
        <button class="ad-btn-sm ad-btn-red" @click="routeForm.splice(i, 1)">{{ t('models.delete') }}</button>
      </div>
      <div class="ad-row">
        <button class="ad-btn" @click="saveModels">{{ t('models.saveModel') }}</button>
        <button class="ad-btn" @click="routeForm.push({ provider: '', api_base: '', api_key: '', model: '', weight: 0 })">{{ t('models.addRoute') }}</button>
        <button class="ad-btn ad-btn-green" @click="saveRoutes">{{ t('models.saveRoutes') }}</button>
      </div>
      <!-- 当前生效状态摘要：主模型 = 权重最高者 -->
      <p class="ad-hint">{{ routeForm.length ? tpl('models.routesActive', { count: routeForm.length, main: (routeForm.find(r => r.weight > 0)?.model || mForm.model || '—') }) : t('models.routesNone') }}</p>
    </div>

    <!-- 超管：业务五阶段模型配置（每卡带供应商预设模板） -->
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

    <!-- 匹配策略参数（后端 requireAdminUser 把关；经租户切换器对指定租户生效） -->
    <div class="ad-chart-card">
      <h3>{{ t('models.policyTitle') }}</h3>
      <label class="ad-label">{{ t('models.highSim') }}</label>
      <input v-model.number="pForm2.high_sim" type="number" step="0.01" class="ad-input" />
      <label class="ad-label">{{ t('models.medSim') }}</label>
      <input v-model.number="pForm2.med_sim" type="number" step="0.01" class="ad-input" />
      <label class="ad-label">{{ t('models.evalsThreshold') }}</label>
      <input v-model.number="pForm2.evals_pass_threshold" type="number" class="ad-input" />
      <!-- ★ KB 继承链（2026-08-26）：租户级「跨部门降级检索」开关——
           开启后链内精确零命中可回退到其他「愿意共享」的部门包（结果打标来源）；
           模糊/语义命中仅作例句参考，绝不直接采用 -->
      <label class="ad-label">{{ t('models.crossDeptFallback') }}</label>
      <select v-model="pForm2.cross_dept_fallback" class="ad-input ad-mini-w">
        <option :value="true">{{ t('models.crossOn') }}</option>
        <option :value="false">{{ t('models.crossOff') }}</option>
      </select>
      <button class="ad-btn" @click="savePolicy">{{ t('models.savePolicy') }}</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { adminModels, adminModelsSave, adminPolicy, adminPolicySave, stageModels, stageModelsSave } from '@/api'
import { activeTenantId } from './store'
import { t, tpl } from '@/i18n'

// ★ BYOK 移除说明：原「isSuper 判断 + 租户单模型/多供应商路由（v-if=!isSuper 死区块）」
//   已随平台统一网关决策删除。本面板在管理后台菜单中仅对超管（L4）开放，
//   因此模板内不再需要角色分支。

// 主模型配置表单：API 基地址/密钥/模型名（对应全局主路由）
const mForm = ref({ api_base: '', api_key: '', model: '' })
// 翻译策略参数：高/中相似度阈值、评测通过分、跨部门降级检索开关（默认开）
const pForm2 = ref<any>({ high_sim: 0.9, med_sim: 0.75, evals_pass_threshold: 75, cross_dept_fallback: true })
// 多供应商备用路由表（权重降序构成主模型失败后的降级链）
const routeForm = ref<any[]>([])
// 常用 LLM 供应商预设（OpenAI 兼容格式，含 ChatGPT/Gemini）
const providerPresets: Record<string, { api_base: string; model: string }> = {
  openai: { api_base: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
  gemini: { api_base: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-1.5-flash' },
  deepseek: { api_base: 'https://api.deepseek.com/v1', model: 'deepseek-chat' },
  siliconflow: { api_base: 'https://api.siliconflow.cn/v1', model: 'tencent/Hunyuan-MT-7B' },
  zhipu: { api_base: 'https://open.bigmodel.cn/api/paas/v4', model: 'glm-4-flash' },
}
// 路由预设选择器当前值（选中即追加一条空路由并回弹）
const routePreset = ref('')

// 应用路由预设：追加一条供应商路由（Key 留空由用户填），并重置下拉
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
// 应用供应商预设：自动填 API 地址 + 模型名（Key 留给用户填；kb_embed 固定 embedding 模型名）
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
// 底部摘要：已配置的阶段数量提示
const stageHint = computed(() => {
  const n = stageCards.filter(c => stActive(c.key)).length
  return n ? tpl('models.stageActive', { count: n }) : t('models.stageNone')
})

// loadModels 加载全局网关配置（主模型 + 多供应商路由，密钥掩码回显）
async function loadModels() {
  const r = await adminModels()
  if (r.success) {
    mForm.value = (r as any).model
    routeForm.value = (r as any).routes || []
  }
}
// saveModels 保存主模型 + 路由配置并回读刷新（后端合并单模型为主路由）
async function saveModels() {
  const r = await adminModelsSave({ ...mForm.value, routes: routeForm.value })
  if (!r.success) { alert(r.message); return }
  alert(t('models.savedModels'))
  await loadModels()
}
// saveRoutes 仅提交有效路由行（地址+模型齐全），主模型字段不动
async function saveRoutes() {
  const valid = routeForm.value.filter((rt: any) => rt.api_base && rt.model)
  const r = await adminModelsSave({ routes: valid })
  if (!r.success) { alert(r.message); return }
  alert(t('models.savedRoutes'))
  await loadModels()
}
// loadPolicy 加载匹配策略参数（含跨部门开关布尔）
async function loadPolicy() {
  const r = await adminPolicy()
  if (r.success) pForm2.value = (r as any).policy
}
// savePolicy 保存匹配策略参数（数值域走 policy 映射；开关单独字段传输）
async function savePolicy() {
  const cross = !!pForm2.value.cross_dept_fallback
  const r = await adminPolicySave(pForm2.value, cross)
  if (!r.success) { alert(r.message); return }
  alert(t('models.savedPolicy'))
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
// 保存五阶段：剥离前端专有字段（preset），仅提交 provider/api_base/api_key/model
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

// loadAll 依次加载策略 / 全局网关路由 / 五阶段配置
async function loadAll() {
  await loadPolicy()
  await loadModels()
  await loadStages()
}
onMounted(loadAll)
watch(activeTenantId, loadAll)
</script>
