// ============================================================================
// components/admin/ModelsP.tsx — 模型配置面板
// 职责：主模型配置、多供应商路由、分阶段模型、翻译策略参数
// 从 panels_d.tsx 拆分
// 2026-09-18（UI 融合）：密钥「已配置/未配置」不再用 ✓/✗ 图形字符（emoji 清理），
//   改由文案 + 掩码回显承担；保存链路与密钥掩码口径不变（明文永不回显）。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
// 2026-09-18（UI 融合）：按钮/行内操作改吃自研 @/ui/langcross 原语。口径注意：
//   langcross Button 只有 primary/secondary/danger 三档，旧 theme="success" 一律归并为 primary；
//   行内「删除」不再是红色小按钮，统一降为 <Link tone="danger">。
import { Button, Link } from '@/ui/langcross/src'
// toastBus 是同步总线（不返回 Promise），所以调用点去掉了原先的 void 吞返回值写法
import { toastSuccess, toastError } from '@/lib/toastBus'
import {
  adminModels, adminModelsSave, stageModels, stageModelsSave,
  adminPolicy, adminPolicySave,
} from '@/api'
import { Panel, Field, num } from './parts'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'

/** Any 模型配置出参宽松别名 */
type Any = Record<string, any>

// 行布局样式（横向排布 + 顶距）
const rowMt: any = { display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginTop: 8 }

// 常用 OpenAI 兼容端点预设（供应商 → api_base/默认模型）
const providerPresets: Record<string, { api_base: string; model: string }> = {
  openai: { api_base: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
  gemini: { api_base: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-1.5-flash' },
  deepseek: { api_base: 'https://api.deepseek.com/v1', model: 'deepseek-chat' },
  siliconflow: { api_base: 'https://api.siliconflow.cn/v1', model: 'tencent/Hunyuan-MT-7B' },
  zhipu: { api_base: 'https://open.bigmodel.cn/api/paas/v4', model: 'glm-4-flash' },
}

/** 模型配置面板 */
export function ModelsP() {
  // ===== 面板状态：主模型/embedding/路由表/分阶段配置（各表单独立，保存时只提交变更块） =====
  const [, t, tpl] = useT()
  const { activeTenantId } = useAdmin()
  // mForm 主模型三元组（端点/密钥/模型名）。api_key 只写不读：读取一律走 keyState 的布尔与掩码
  const [mForm, setMForm] = useState<Any>({ api_base: '', api_key: '', model: '' })
  // routeForm 多供应商路由表。后端按「整表替换」保存，所以改任何一行都必须把全表带回
  const [routeForm, setRouteForm] = useState<Any[]>([])
  // routePreset 只暂存「下一行要套用哪个预设」，本身不入库，套完即清空（避免重复追加）
  const [routePreset, setRoutePreset] = useState('')
  // pForm2 策略表单：三个阈值 + 两个开关；开关在接口里与阈值分开传（后端分列存储）
  const [pForm2, setPForm2] = useState<Any>({ high_sim: 0.9, med_sim: 0.75, evals_pass_threshold: 75, cross_dept_fallback: true, data_feedback_opt_out: false })
  // stForm 分阶段模型，按 stageCards.key 建索引；每阶段多带一个 preset（纯前端下拉态）
  const [stForm, setStForm] = useState<Any>({})
  // eForm 向量（embedding）端点与密钥：与主模型彻底解耦，可以给 embedding 单配一家供应商
  const [eForm, setEForm] = useState<Any>({ api_key: '', api_base: '' })
  // keyState 密钥态：translation/embedding 是否已配置 + embedding 掩码/端点，明文永不回传前端
  const [keyState, setKeyState] = useState<Any>({ translation: false, embedding: false, embeddingMasked: '', embeddingBase: '' })

  // stageCards 五张卡即翻译流水线的五个取模点（初翻→嵌入→初评审校→复核→复评审校）；
  // key 必须与后端 stage 名逐字一致，否则 loadStages 回填不到、saveStages 也存不进
  const stageCards = [
    { key: 'ai_initial', title: t('models.s5Initial'), hint: t('models.s5InitialHint') },
    { key: 'kb_embed', title: t('models.s5Embed'), hint: t('models.s5EmbedHint') },
    { key: 'initial_evals', title: t('models.s5InitialEvals'), hint: t('models.s5InitialEvalsHint') },
    { key: 'review', title: t('models.s5Review'), hint: t('models.s5ReviewHint') },
    { key: 'review_evals', title: t('models.s5ReviewEvals'), hint: t('models.s5ReviewEvalsHint') },
  ]

  // loadModels 拉取主模型/embedding/多供应商路由（密钥只回掩码，不回明文）
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
  // loadPolicy 拉取模型调度策略（熔断阈值/采样/兜底链）
  const loadPolicy = useCallback(async () => {
    const r = await adminPolicy()
    if (r.success) setPForm2((r as unknown as { policy?: Any }).policy || {})
  }, [])
  // loadStages 拉取分阶段模型（初翻/校对/Judge 各自模型）并回填表单
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
  // loadAll 模型面板整体刷新：策略 → 模型 → 阶段（有依赖顺序，避免表单互相覆盖）
  const loadAll = useCallback(async () => { await loadPolicy(); await loadModels(); await loadStages() }, [loadPolicy, loadModels, loadStages])
  useEffect(() => { void loadAll() }, [activeTenantId, loadAll])

  // applyRoutePreset 预设只补端点与默认模型，密钥一律留空由管理员手填
  // （providerPresets 是随代码分发的静态表，绝不能带任何密钥）
  function applyRoutePreset() {
    const p = providerPresets[routePreset]
    if (p) { setRouteForm([...routeForm, { provider: routePreset, api_base: p.api_base, api_key: '', model: p.model, weight: 0 }]); setRoutePreset('') }
  }
  // applyStagePreset 与路由版的差异：kb_embed 阶段套用对话模型没意义，端点照用但模型名强制改写
  function applyStagePreset(key: string) {
    const p = providerPresets[stForm[key]?.preset]
    if (p) setStForm({ ...stForm, [key]: { ...stForm[key], api_base: p.api_base, model: key === 'kb_embed' ? 'embedding-2' : p.model } })
  }
  // stActive 阶段就绪判定：只看端点+模型。密钥可以沿用全局那把，所以不作为必要条件
  const stActive = (key: string) => !!(stForm[key]?.api_base && stForm[key]?.model)
  // stageHint 已就绪阶段数，供底部「N 个阶段已生效」文案
  const stageHint = stageCards.filter((c) => stActive(c.key)).length

  // saveModels 一次提交主模型 + 全量路由表：routes 是整表替换语义，漏传等于清空线上分流
  async function saveModels() {
    const r = await adminModelsSave({ ...mForm, routes: routeForm } as never)
    if (!r.success) { toastError(r.message); return }
    toastSuccess(t('models.savedModels')); await loadModels()
  }
  // saveRoutes 只保存端点+模型都填全的行：半成品行会真线上分流，宁可不存
  async function saveRoutes() {
    const valid = routeForm.filter((rt: Any) => rt.api_base && rt.model)
    const r = await adminModelsSave({ routes: valid } as never)
    if (!r.success) { toastError(r.message); return }
    toastSuccess(t('models.savedRoutes')); await loadModels()
  }
  // saveStages 逐阶段剥掉 preset 再提交：它是下拉辅助值，后端 stage 里没有这一列
  async function saveStages() {
    const payload: Record<string, Any> = {}
    for (const c of stageCards) {
      const { preset, ...rest } = stForm[c.key] || {}
      void preset
      payload[c.key] = rest
    }
    const r = await stageModelsSave(payload as never)
    if (!r.success) { toastError(r.message); return }
    toastSuccess(t('models.savedStages')); await loadStages()
  }
  // savePolicy 三个阈值必须 Number 化（换成原生 input 后 onChange 拿到的是字符串，直接提交会变字符串落库）；
  // 两个开关不进 policy 对象，作为独立参数走同一接口
  async function savePolicy() {
    const cross = !!pForm2.cross_dept_fallback
    const fbOut = !!pForm2.data_feedback_opt_out
    const policy = { high_sim: Number(pForm2.high_sim), med_sim: Number(pForm2.med_sim), evals_pass_threshold: Number(pForm2.evals_pass_threshold) }
    const r = await adminPolicySave(policy as never, cross, fbOut)
    if (!r.success) { toastError(r.message); return }
    toastSuccess(t('models.savedPolicy'))
  }
  // saveEmbed 复用同一个 adminModelsSave，只带 embed_* 两项：后端按「非空才写」合并，主模型配置不受影响
  async function saveEmbed() {
    const r = await adminModelsSave({ embed_api_key: eForm.api_key, embed_api_base: eForm.api_base } as never)
    if (!r.success) { toastError(r.message); return }
    toastSuccess(t('models.savedEmbed')); await loadModels()
  }
  // clearEmbed 删除必须显式传 clear_keys：提交空串在后端等于「不修改」，永远删不掉已存密钥
  async function clearEmbed() {
    const r = await adminModelsSave({ clear_keys: ['embedding'] } as never)
    if (!r.success) { toastError(r.message); return }
    toastSuccess(t('models.clearedEmbed')); await loadModels()
  }
  // clearTrans 清的是 translation 作用域（密钥/网关/模型三项一起回退），运行配置随后恢复占位 Key；
  // 与 clearEmbed 走同一入口，只是作用域不同——所以两者都要重拉 loadModels 才能刷新「已配置」态
  async function clearTrans() {
    const r = await adminModelsSave({ clear_keys: ['translation'] } as never)
    if (!r.success) { toastError(r.message); return }
    toastSuccess(t('models.clearedTrans')); await loadModels()
  }

  // mainModel 展示口径：权重>0 的首条路由即当前主力模型；没有启用权重的路由时回落到主模型输入值
  const mainModel = (routeForm.find((r: Any) => Number(r.weight) > 0)?.model) || mForm.model || '—'

  return (
    <>
      <h2 style={{ margin: '4px 0 12px' }}>{t('models.title')}</h2>

      <Panel title={t('models.routingTitle')}>
        <div style={{ fontSize: 12, color: 'var(--adm-hint)', marginBottom: 8 }}>{t('models.onlineHint')}</div>
        {/* 预设下拉改原生 select：不再有 clearable 属性，placeholder 由 value="" 的空 option 兼任，选它即取消预设 */}
        <div style={rowMt}>
          <select className="lc-select" value={routePreset} onChange={(e) => setRoutePreset(e.target.value)} style={{ width: 220 }}>
            <option value="">{t('models.presetPlaceholder')}</option>
            <option value="openai">OpenAI (ChatGPT)</option>
            <option value="gemini">Google Gemini</option>
            <option value="deepseek">DeepSeek</option>
            <option value="siliconflow">SiliconFlow</option>
            <option value="zhipu">Zhipu GLM</option>
          </select>
        </div>
        {/* 主模型三项输入已换原生 lc-input：onChange 回的是事件对象而非值，取值统一 e.target.value；
            外层 <form onSubmit=preventDefault> 保留，防单输入框表单回车直接刷新页面 */}
        <Field label={t('models.apiBase')}><input className="lc-input" value={String(mForm.api_base ?? '')} onChange={(e) => setMForm({ ...mForm, api_base: e.target.value })} placeholder={t('models.apiBasePlaceholder')} /></Field>
        {/* autoComplete="new-password"：屏蔽密码管理器对密钥框的自动填充/保存。
            旧代码写的是小写 autocomplete，React 不认这个属性名，等于从来没生效过（本次顺手修正，仍是「只写不读」口径） */}
        <form onSubmit={(e) => e.preventDefault()}><Field label={t('models.apiKey')}><input className="lc-input" type="password" autoComplete="new-password" value={String(mForm.api_key ?? '')} onChange={(e) => setMForm({ ...mForm, api_key: e.target.value })} placeholder={t('models.apiKeyPlaceholder')} /></Field></form>
        <Field label={t('models.modelName')}><input className="lc-input" value={String(mForm.model ?? '')} onChange={(e) => setMForm({ ...mForm, model: e.target.value })} placeholder={t('models.modelNamePlaceholder')} /></Field>

        {/* 路由行内编辑：删除键由 danger Button 降为 <Link tone="danger">，与表格操作列同一口径；
            weight 是相对权重，填 0 表示「只登记端点、不参与分流」（mainModel 只认 weight>0 的首行） */}
        {routeForm.map((r, i) => (
          <div key={i} style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 8, flexWrap: 'wrap' }}>
            <input className="lc-input" value={String(r.provider || '')} onChange={(e) => { const n = [...routeForm]; n[i] = { ...n[i], provider: e.target.value }; setRouteForm(n) }} placeholder={t('models.providerPlaceholder')} style={{ width: 150 }} />
            <input className="lc-input" value={String(r.api_base || '')} onChange={(e) => { const n = [...routeForm]; n[i] = { ...n[i], api_base: e.target.value }; setRouteForm(n) }} placeholder={t('models.apiBasePlaceholder')} style={{ flex: 1 }} />
            <input className="lc-input" value={String(r.api_key || '')} onChange={(e) => { const n = [...routeForm]; n[i] = { ...n[i], api_key: e.target.value }; setRouteForm(n) }} placeholder={t('models.apiKeyPlaceholder')} style={{ flex: 1 }} />
            <input className="lc-input" value={String(r.model || '')} onChange={(e) => { const n = [...routeForm]; n[i] = { ...n[i], model: e.target.value }; setRouteForm(n) }} placeholder={t('models.modelNamePlaceholder')} style={{ flex: 1 }} />
            <input className="lc-input" type="number" value={num(r.weight ?? 0)} onChange={(e) => { const n = [...routeForm]; n[i] = { ...n[i], weight: Number(e.target.value) }; setRouteForm(n) }} placeholder={t('models.weightPlaceholder')} style={{ width: 90 }} />
            <Link tone="danger" onClick={() => setRouteForm(routeForm.filter((_, j) => j !== i))}>{t('models.delete')}</Link>
          </div>
        ))}

        {/* 「添加路由」是双关按钮：下拉已选预设就走 applyRoutePreset 套用，否则只追加一行空白手工填 */}
        <div style={rowMt}>
          <Button variant="primary" onClick={() => void saveModels()}>{t('models.saveModel')}</Button>
          {/* ★ E14：预设选择后一键添加即套用（applyRoutePreset 接入调用点）——注意：JSX 子节点位置的 `//` 注释会被当文本渲染上屏，必须用花括号注释 */}
          <Button variant="secondary" onClick={() => { if (routePreset) applyRoutePreset(); else setRouteForm([...routeForm, { provider: '', api_base: '', api_key: '', model: '', weight: 0 }]) }}>{t('models.addRoute')}</Button>
          <Button variant="secondary" onClick={() => void saveRoutes()}>{t('models.saveRoutes')}</Button>
        </div>
        <p style={{ fontSize: 12, color: 'var(--adm-hint)', margin: '8px 0 0' }}>
          {routeForm.length ? tpl('models.routesActive', { count: routeForm.length, main: mainModel }) : t('models.routesNone')}
        </p>
      </Panel>

      <Panel title={t('models.llmKeyTitle')}>
        <div style={{ fontSize: 12, color: 'var(--adm-hint)', marginBottom: 8 }}>{t('models.llmKeyHint')}</div>
        <div style={rowMt}>
          {/* 翻译主密钥状态回显：后端只下发 configured 布尔（✓/✗ 图形字符已于 2026-09-18 移除，
              状态改由「已配置/未配置」文案表意），明文密钥任何情况下都不回传前端。 */}
          <span style={{ fontSize: 13 }}>{t('models.translationKeyLabel')}：{keyState.translation ? ` ${t('models.configured')}` : ` ${t('models.notConfigured')}`}</span>
          {keyState.translation && <Button size="sm" variant="danger" onClick={() => void clearTrans()}>{t('models.clearTranslation')}</Button>}
        </div>
        {/* embedding 密钥框同为 new-password；输入框刻意不回填任何值（后端只给 set/masked），
            留空提交即「不改密钥只改端点」，这正是掩码不回写的同一套口径 */}
        <form onSubmit={(e) => e.preventDefault()}><Field label={t('models.embedApiKey')}><input className="lc-input" type="password" autoComplete="new-password" value={String(eForm.api_key ?? '')} onChange={(e) => setEForm({ ...eForm, api_key: e.target.value })} placeholder={t('models.embedApiKeyPlaceholder')} /></Field></form>
        <Field label={t('models.embedApiBase')}><input className="lc-input" value={String(eForm.api_base ?? '')} onChange={(e) => setEForm({ ...eForm, api_base: e.target.value })} placeholder={t('models.embedApiBasePlaceholder')} /></Field>
        <div style={rowMt}>
          <Button variant="primary" onClick={() => void saveEmbed()}>{t('models.saveEmbed')}</Button>
          {keyState.embedding && <Button size="sm" variant="danger" onClick={() => void clearEmbed()}>{t('models.clearEmbed')}</Button>}
 {/* 掩码回显只为让管理员确认「要清的到底是哪把 key」，串里全是 ****，不含明文片段 */}
 {keyState.embedding && <span style={{ fontSize: 12, color:'var(--adm-ok-tx)'}}> {keyState.embeddingMasked}</span>}
        </div>
      </Panel>

      {/* 分阶段卡片：逐阶段独立 preset，套用只覆盖本卡端点/模型，阶段之间互不联动 */}
      {stageCards.map((st) => (
        <Panel key={st.key} title={st.title}>
          <div style={{ fontSize: 12, color: 'var(--adm-hint)', marginBottom: 8 }}>{st.hint}</div>
          <div style={rowMt}>
            <select className="lc-select" value={stForm[st.key]?.preset || ''} onChange={(e) => { setStForm({ ...stForm, [st.key]: { ...stForm[st.key], preset: e.target.value } }); applyStagePreset(st.key) }} style={{ width: 220 }}>
              <option value="">{t('models.presetPlaceholder')}</option>
              <option value="openai">OpenAI (ChatGPT)</option>
              <option value="gemini">Google Gemini</option>
              <option value="deepseek">DeepSeek</option>
              <option value="siliconflow">SiliconFlow</option>
              <option value="zhipu">Zhipu GLM</option>
            </select>
            {/* preset 选中即套用（onChange 里直接 applyStagePreset），不再需要额外「应用」按钮 */}
 {/* 阶段就绪提示：与主密钥区同一处理，原 ✓ 前缀 2026-09-18 起移除，状态全靠文案 */}
 {stActive(st.key) && <span style={{ fontSize: 12, color:'var(--adm-ok-tx)'}}> {t('models.stageConfigured'as never)}</span>}
          </div>
          <Field label={t('models.apiBase')}><input className="lc-input" value={String(stForm[st.key]?.api_base ?? '')} onChange={(e) => setStForm({ ...stForm, [st.key]: { ...stForm[st.key], api_base: e.target.value } })} placeholder={t('models.stageApiBasePlaceholder' as never)} /></Field>
          <form onSubmit={(e) => e.preventDefault()}><Field label={t('models.apiKey')}><input className="lc-input" type="password" autoComplete="new-password" value={String(stForm[st.key]?.api_key ?? '')} onChange={(e) => setStForm({ ...stForm, [st.key]: { ...stForm[st.key], api_key: e.target.value } })} placeholder={t('models.stageApiKeyPlaceholder' as never)} /></Field></form>
          <Field label={t('models.modelName')}><input className="lc-input" value={String(stForm[st.key]?.model ?? '')} onChange={(e) => setStForm({ ...stForm, [st.key]: { ...stForm[st.key], model: e.target.value } })} placeholder={t('models.stageModelPlaceholder' as never)} /></Field>
        </Panel>
      ))}
      <div style={rowMt}>
        <Button variant="primary" onClick={() => void saveStages()}>{t('models.saveStages')}</Button>
        <span style={{ fontSize: 12, color: 'var(--adm-hint)' }}>{stageHint ? tpl('models.stageActive', { count: stageHint }) : t('models.stageNone')}</span>
      </div>

      <Panel title={t('models.policyTitle')}>
        {/* 三个阈值用 type=number 原生框：显示值经 num() 兜成字符串（input.value 只吃 string，
            直接塞 undefined 会让 React 把框切成非受控），提交时再由 savePolicy 转回 Number */}
        <Field label={t('models.highSim')}><input className="lc-input" type="number" value={num(pForm2.high_sim ?? 0)} onChange={(e) => setPForm2({ ...pForm2, high_sim: e.target.value })} style={{ maxWidth: 200 }} /></Field>
        <Field label={t('models.medSim')}><input className="lc-input" type="number" value={num(pForm2.med_sim ?? 0)} onChange={(e) => setPForm2({ ...pForm2, med_sim: e.target.value })} style={{ maxWidth: 200 }} /></Field>
        <Field label={t('models.evalsThreshold')}><input className="lc-input" type="number" value={num(pForm2.evals_pass_threshold ?? 0)} onChange={(e) => setPForm2({ ...pForm2, evals_pass_threshold: e.target.value })} style={{ maxWidth: 200 }} /></Field>
        {/* 布尔策略在原生 select 里只能承载字符串，故值用 'true'/'false' 字面量，回读时再 === 'true' 折成布尔；
            TDesign 时代 options 直接传 boolean value，这条转换是新底座必须的步骤，别在 onChange 里漏掉 */}
        <Field label={t('models.crossDeptFallback')}>
          <select className="lc-select" value={pForm2.cross_dept_fallback ? 'true' : 'false'} onChange={(e) => setPForm2({ ...pForm2, cross_dept_fallback: e.target.value === 'true' })} style={{ width: 220 }}>
            <option value="true">{t('models.crossOn')}</option>
            <option value="false">{t('models.crossOff')}</option>
          </select>
        </Field>
        {/* 注意本项是 opt-out 语义：value=true 对应「不入平台审核池」，value=false 才是「参与回流（默认）」，
            值与文案的正反极易读反，改这里前先对 i18n 原文（models.fbOff / models.fbOn） */}
        <Field label={t('models.feedbackOptOut')}>
          <select className="lc-select" value={pForm2.data_feedback_opt_out ? 'true' : 'false'} onChange={(e) => setPForm2({ ...pForm2, data_feedback_opt_out: e.target.value === 'true' })} style={{ width: 260 }}>
            <option value="true">{t('models.fbOff')}</option>
            <option value="false">{t('models.fbOn')}</option>
          </select>
        </Field>
        <Button variant="primary" style={{ marginTop: 8 }} onClick={() => void savePolicy()}>{t('models.savePolicy')}</Button>
      </Panel>
    </>
  )
}
