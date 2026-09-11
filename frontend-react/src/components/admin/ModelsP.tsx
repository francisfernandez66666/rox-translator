// ============================================================================
// components/admin/ModelsP.tsx — 模型配置面板
// 职责：主模型配置、多供应商路由、分阶段模型、翻译策略参数
// 从 panels_d.tsx 拆分
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import {
  Button, Input, Select, MessagePlugin,
} from 'tdesign-react'
import {
  adminModels, adminModelsSave, stageModels, stageModelsSave,
  adminPolicy, adminPolicySave,
} from '@/api'
import { Panel, Field, num } from './parts'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'

type Any = Record<string, any>

const rowMt: any = { display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginTop: 8 }

const providerPresets: Record<string, { api_base: string; model: string }> = {
  openai: { api_base: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
  gemini: { api_base: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-1.5-flash' },
  deepseek: { api_base: 'https://api.deepseek.com/v1', model: 'deepseek-chat' },
  siliconflow: { api_base: 'https://api.siliconflow.cn/v1', model: 'tencent/Hunyuan-MT-7B' },
  zhipu: { api_base: 'https://open.bigmodel.cn/api/paas/v4', model: 'glm-4-flash' },
}

/** 模型配置面板 */
export function ModelsP() {
  const [, t, tpl] = useT()
  const { activeTenantId } = useAdmin()
  const [mForm, setMForm] = useState<Any>({ api_base: '', api_key: '', model: '' })
  const [routeForm, setRouteForm] = useState<Any[]>([])
  const [routePreset, setRoutePreset] = useState('')
  const [pForm2, setPForm2] = useState<Any>({ high_sim: 0.9, med_sim: 0.75, evals_pass_threshold: 75, cross_dept_fallback: true, data_feedback_opt_out: false })
  const [stForm, setStForm] = useState<Any>({})
  const [eForm, setEForm] = useState<Any>({ api_key: '', api_base: '' })
  const [keyState, setKeyState] = useState<Any>({ translation: false, embedding: false, embeddingMasked: '', embeddingBase: '' })

  const stageCards = [
    { key: 'ai_initial', title: t('models.s5Initial'), hint: t('models.s5InitialHint') },
    { key: 'kb_embed', title: t('models.s5Embed'), hint: t('models.s5EmbedHint') },
    { key: 'initial_evals', title: t('models.s5InitialEvals'), hint: t('models.s5InitialEvalsHint') },
    { key: 'review', title: t('models.s5Review'), hint: t('models.s5ReviewHint') },
    { key: 'review_evals', title: t('models.s5ReviewEvals'), hint: t('models.s5ReviewEvalsHint') },
  ]

  const loadModels = useCallback(async () => {
    const r = await adminModels()
    if (r.success) {
      const d = r as unknown as { model?: Any; embedding?: Any; routes?: Any[] }
      setMForm(d.model || {})
      setRouteForm(d.routes || [])
      setEForm({ api_key: '', api_base: (d.embedding?.api_base as string) || '' })
      setKeyState({ translation: !!(d.model && d.model.set), embedding: !!(d.embedding && d.embedding.set), embeddingMasked: (d.embedding?.masked as string) || '', embeddingBase: (d.embedding?.api_base as string) || '' })
    }
  }, [])
  const loadPolicy = useCallback(async () => {
    const r = await adminPolicy()
    if (r.success) setPForm2((r as unknown as { policy?: Any }).policy || {})
  }, [])
  const loadStages = useCallback(async () => {
    const r = await stageModels()
    if (r.success) {
      const st = (r as unknown as { stages?: Record<string, Any> }).stages || {}
      const nf: Any = {}
      for (const c of stageCards) {
        const s = st[c.key] || {}
        nf[c.key] = { preset: '', provider: c.key, api_base: s.api_base || '', api_key: s.api_key || '', model: s.model || '' }
      }
      setStForm(nf)
    }
  }, [])
  const loadAll = useCallback(async () => { await loadPolicy(); await loadModels(); await loadStages() }, [loadPolicy, loadModels, loadStages])
  useEffect(() => { void loadAll() }, [activeTenantId, loadAll])

  function applyRoutePreset() {
    const p = providerPresets[routePreset]
    if (p) { setRouteForm([...routeForm, { provider: routePreset, api_base: p.api_base, api_key: '', model: p.model, weight: 0 }]); setRoutePreset('') }
  }
  function applyStagePreset(key: string) {
    const p = providerPresets[stForm[key]?.preset]
    if (p) setStForm({ ...stForm, [key]: { ...stForm[key], api_base: p.api_base, model: key === 'kb_embed' ? 'embedding-2' : p.model } })
  }
  const stActive = (key: string) => !!(stForm[key]?.api_base && stForm[key]?.model)
  const stageHint = stageCards.filter((c) => stActive(c.key)).length

  async function saveModels() {
    const r = await adminModelsSave({ ...mForm, routes: routeForm } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.savedModels')); await loadModels()
  }
  async function saveRoutes() {
    const valid = routeForm.filter((rt: Any) => rt.api_base && rt.model)
    const r = await adminModelsSave({ routes: valid } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.savedRoutes')); await loadModels()
  }
  async function saveStages() {
    const payload: Record<string, Any> = {}
    for (const c of stageCards) {
      const { preset, ...rest } = stForm[c.key] || {}
      void preset
      payload[c.key] = rest
    }
    const r = await stageModelsSave(payload as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.savedStages')); await loadStages()
  }
  async function savePolicy() {
    const cross = !!pForm2.cross_dept_fallback
    const fbOut = !!pForm2.data_feedback_opt_out
    const policy = { high_sim: Number(pForm2.high_sim), med_sim: Number(pForm2.med_sim), evals_pass_threshold: Number(pForm2.evals_pass_threshold) }
    const r = await adminPolicySave(policy as never, cross, fbOut)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.savedPolicy'))
  }
  async function saveEmbed() {
    const r = await adminModelsSave({ embed_api_key: eForm.api_key, embed_api_base: eForm.api_base } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.savedEmbed')); await loadModels()
  }
  async function clearEmbed() {
    const r = await adminModelsSave({ clear_keys: ['embedding'] } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.clearedEmbed')); await loadModels()
  }
  async function clearTrans() {
    const r = await adminModelsSave({ clear_keys: ['translation'] } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.clearedTrans')); await loadModels()
  }

  const mainModel = (routeForm.find((r: Any) => Number(r.weight) > 0)?.model) || mForm.model || '—'

  return (
    <>
      <h2 style={{ margin: '4px 0 12px' }}>{t('models.title')}</h2>

      <Panel title={t('models.routingTitle')}>
        <div style={{ fontSize: 12, color: '#667', marginBottom: 8 }}>{t('models.onlineHint')}</div>
        <div style={rowMt}>
          <Select value={routePreset} onChange={(v: any) => setRoutePreset(String(v))} placeholder={t('models.presetPlaceholder')} style={{ width: 220 }} clearable
            options={[{ label: 'OpenAI (ChatGPT)', value: 'openai' }, { label: 'Google Gemini', value: 'gemini' }, { label: 'DeepSeek', value: 'deepseek' }, { label: 'SiliconFlow', value: 'siliconflow' }, { label: 'Zhipu GLM', value: 'zhipu' }]} />
        </div>
        <Field label={t('models.apiBase')}><Input value={String(mForm.api_base ?? '')} onChange={(v: any) => setMForm({ ...mForm, api_base: v })} placeholder={t('models.apiBasePlaceholder')} /></Field>
        <form onSubmit={(e) => e.preventDefault()}><Field label={t('models.apiKey')}><Input type="password" autocomplete="new-password" value={String(mForm.api_key ?? '')} onChange={(v: any) => setMForm({ ...mForm, api_key: v })} placeholder={t('models.apiKeyPlaceholder')} /></Field></form>
        <Field label={t('models.modelName')}><Input value={String(mForm.model ?? '')} onChange={(v: any) => setMForm({ ...mForm, model: v })} placeholder={t('models.modelNamePlaceholder')} /></Field>

        {routeForm.map((r, i) => (
          <div key={i} style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 8, flexWrap: 'wrap' }}>
            <Input value={String(r.provider || '')} onChange={(v: any) => { const n = [...routeForm]; n[i] = { ...n[i], provider: v }; setRouteForm(n) }} placeholder={t('models.providerPlaceholder')} style={{ width: 150 }} />
            <Input value={String(r.api_base || '')} onChange={(v: any) => { const n = [...routeForm]; n[i] = { ...n[i], api_base: v }; setRouteForm(n) }} placeholder={t('models.apiBasePlaceholder')} style={{ flex: 1 }} />
            <Input value={String(r.api_key || '')} onChange={(v: any) => { const n = [...routeForm]; n[i] = { ...n[i], api_key: v }; setRouteForm(n) }} placeholder={t('models.apiKeyPlaceholder')} style={{ flex: 1 }} />
            <Input value={String(r.model || '')} onChange={(v: any) => { const n = [...routeForm]; n[i] = { ...n[i], model: v }; setRouteForm(n) }} placeholder={t('models.modelNamePlaceholder')} style={{ flex: 1 }} />
            <Input type="number" value={num(r.weight ?? 0)} onChange={(v: any) => { const n = [...routeForm]; n[i] = { ...n[i], weight: Number(v) }; setRouteForm(n) }} placeholder={t('models.weightPlaceholder')} style={{ width: 90 }} />
            <Button size="small" theme="danger" variant="text" onClick={() => setRouteForm(routeForm.filter((_, j) => j !== i))}>{t('models.delete')}</Button>
          </div>
        ))}

        <div style={rowMt}>
          <Button onClick={() => void saveModels()}>{t('models.saveModel')}</Button>
          <Button onClick={() => setRouteForm([...routeForm, { provider: '', api_base: '', api_key: '', model: '', weight: 0 }])}>{t('models.addRoute')}</Button>
          <Button theme="success" onClick={() => void saveRoutes()}>{t('models.saveRoutes')}</Button>
        </div>
        <p style={{ fontSize: 12, color: '#667', margin: '8px 0 0' }}>
          {routeForm.length ? tpl('models.routesActive', { count: routeForm.length, main: mainModel }) : t('models.routesNone')}
        </p>
      </Panel>

      <Panel title={t('models.llmKeyTitle')}>
        <div style={{ fontSize: 12, color: '#667', marginBottom: 8 }}>{t('models.llmKeyHint')}</div>
        <div style={rowMt}>
          <span style={{ fontSize: 13 }}>{t('models.translationKeyLabel')}：{keyState.translation ? `✓ ${t('models.configured')}` : `✗ ${t('models.notConfigured')}`}</span>
          {keyState.translation && <Button size="small" theme="danger" variant="outline" onClick={() => void clearTrans()}>{t('models.clearTranslation')}</Button>}
        </div>
        <form onSubmit={(e) => e.preventDefault()}><Field label={t('models.embedApiKey')}><Input type="password" autocomplete="new-password" value={String(eForm.api_key ?? '')} onChange={(v: any) => setEForm({ ...eForm, api_key: v })} placeholder={t('models.embedApiKeyPlaceholder')} /></Field></form>
        <Field label={t('models.embedApiBase')}><Input value={String(eForm.api_base ?? '')} onChange={(v: any) => setEForm({ ...eForm, api_base: v })} placeholder={t('models.embedApiBasePlaceholder')} /></Field>
        <div style={rowMt}>
          <Button onClick={() => void saveEmbed()}>{t('models.saveEmbed')}</Button>
          {keyState.embedding && <Button size="small" theme="danger" variant="outline" onClick={() => void clearEmbed()}>{t('models.clearEmbed')}</Button>}
          {keyState.embedding && <span style={{ fontSize: 12, color: '#1a7f37' }}>✓ {keyState.embeddingMasked}</span>}
        </div>
      </Panel>

      {stageCards.map((st) => (
        <Panel key={st.key} title={st.title}>
          <div style={{ fontSize: 12, color: '#667', marginBottom: 8 }}>{st.hint}</div>
          <div style={rowMt}>
            <Select value={stForm[st.key]?.preset || ''} onChange={(v: any) => { setStForm({ ...stForm, [st.key]: { ...stForm[st.key], preset: v } }); applyStagePreset(st.key) }} placeholder={t('models.presetPlaceholder')} style={{ width: 220 }} clearable
              options={[{ label: 'OpenAI (ChatGPT)', value: 'openai' }, { label: 'Google Gemini', value: 'gemini' }, { label: 'DeepSeek', value: 'deepseek' }, { label: 'SiliconFlow', value: 'siliconflow' }, { label: 'Zhipu GLM', value: 'zhipu' }]} />
            {stActive(st.key) && <span style={{ fontSize: 12, color: '#1a7f37' }}>✓ {t('models.stageConfigured' as never)}</span>}
          </div>
          <Field label={t('models.apiBase')}><Input value={String(stForm[st.key]?.api_base ?? '')} onChange={(v: any) => setStForm({ ...stForm, [st.key]: { ...stForm[st.key], api_base: v } })} placeholder={t('models.stageApiBasePlaceholder' as never)} /></Field>
          <form onSubmit={(e) => e.preventDefault()}><Field label={t('models.apiKey')}><Input type="password" autocomplete="new-password" value={String(stForm[st.key]?.api_key ?? '')} onChange={(v: any) => setStForm({ ...stForm, [st.key]: { ...stForm[st.key], api_key: v } })} placeholder={t('models.stageApiKeyPlaceholder' as never)} /></Field></form>
          <Field label={t('models.modelName')}><Input value={String(stForm[st.key]?.model ?? '')} onChange={(v: any) => setStForm({ ...stForm, [st.key]: { ...stForm[st.key], model: v } })} placeholder={t('models.stageModelPlaceholder' as never)} /></Field>
        </Panel>
      ))}
      <div style={rowMt}>
        <Button theme="success" onClick={() => void saveStages()}>{t('models.saveStages')}</Button>
        <span style={{ fontSize: 12, color: '#667' }}>{stageHint ? tpl('models.stageActive', { count: stageHint }) : t('models.stageNone')}</span>
      </div>

      <Panel title={t('models.policyTitle')}>
        <Field label={t('models.highSim')}><Input type="number" value={num(pForm2.high_sim ?? 0)} onChange={(v: any) => setPForm2({ ...pForm2, high_sim: v })} style={{ maxWidth: 200 }} /></Field>
        <Field label={t('models.medSim')}><Input type="number" value={num(pForm2.med_sim ?? 0)} onChange={(v: any) => setPForm2({ ...pForm2, med_sim: v })} style={{ maxWidth: 200 }} /></Field>
        <Field label={t('models.evalsThreshold')}><Input type="number" value={num(pForm2.evals_pass_threshold ?? 0)} onChange={(v: any) => setPForm2({ ...pForm2, evals_pass_threshold: v })} style={{ maxWidth: 200 }} /></Field>
        <Field label={t('models.crossDeptFallback')}>
          <Select value={!!pForm2.cross_dept_fallback} onChange={(v: any) => setPForm2({ ...pForm2, cross_dept_fallback: v })}
            options={[{ label: t('models.crossOn'), value: true }, { label: t('models.crossOff'), value: false }]} style={{ width: 220 }} />
        </Field>
        <Field label={t('models.feedbackOptOut')}>
          <Select value={!!pForm2.data_feedback_opt_out} onChange={(v: any) => setPForm2({ ...pForm2, data_feedback_opt_out: v })}
            options={[{ label: t('models.fbOff'), value: true }, { label: t('models.fbOn'), value: false }]} style={{ width: 260 }} />
        </Field>
        <Button theme="primary" style={{ marginTop: 8 }} onClick={() => void savePolicy()}>{t('models.savePolicy')}</Button>
      </Panel>
    </>
  )
}
