// ============================================================================
// components/admin/SalesConfigP.tsx — 二期「能前端配置的都交给前端配置」管理台面板
//
// 三段配置各占一节：销售模型（阶段 → 模型档位）、销售流程引擎（开关／巡检／步数／场景）、
// 销售策略（场景／语种／挖掘档位），另加词表编辑器（销售策略的落地面）。
// ★ 文案全部走 t() 键；枚举全集（阶段／类别／场景／语种）一律由后端 GET 回传，
//   前端不抄第二份清单（后端 validateSalesConfig 是唯一校验口）。
// ★ 平台默认档只读展示，租户档可写；删除类动作先在动作函数体内弹一次确认框。
// ★ 引擎本体尚未迁入时配置面照样可存可读，但不冒充「已生效」——页头事实说明照直写。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { confirmDialog } from '@/components/uiDialogs'
import {
  salesConfigGet, salesConfigSave, salesLexiconSave, salesLexiconDelete, salesLexiconSeed,
  type SalesConfig, type SalesLexiconRow,
} from '@/api/salesConfig'

/** 词表编辑表单（新增／覆盖一档租户词表） */
interface LexiconDraft {
  scene: string
  lang: string
  kind: string
  words: string
}

/** 销售配置管理面板（租户管理员及以上） */
export default function SalesConfigP() {
  const [, t] = useT()
  const ad = useAdmin()
  const [cfg, setCfg] = useState<SalesConfig | null>(null)
  const [rows, setRows] = useState<SalesLexiconRow[]>([])
  const [stages, setStages] = useState<string[]>([])
  const [kinds, setKinds] = useState<string[]>([])
  const [scenes, setScenes] = useState<string[]>([])
  const [langs, setLangs] = useState<string[]>([])
  const [draft, setDraft] = useState<LexiconDraft>({ scene: '', lang: '', kind: '', words: '' })
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)
  const [saved, setSaved] = useState(false)

  /** 读配置面：失败出声，不把「取不到」伪装成出厂默认 */
  const refresh = useCallback(() => {
    return salesConfigGet()
      .then((r) => {
        if (!r.success || !r.config) { setFailed(true); return }
        setCfg(r.config)
        setRows(Array.isArray(r.lexicon) ? r.lexicon : [])
        setStages(Array.isArray(r.stages) ? r.stages : [])
        setKinds(Array.isArray(r.kinds) ? r.kinds : [])
        setScenes(Array.isArray(r.scenes) ? r.scenes : [])
        setLangs(Array.isArray(r.langs) ? r.langs : [])
        if (!draft.scene && r.scenes?.length) setDraft((d) => ({ ...d, scene: r.scenes![0] }))
        if (!draft.lang && r.langs?.length) setDraft((d) => ({ ...d, lang: r.langs![0] }))
        if (!draft.kind && r.kinds?.length) setDraft((d) => ({ ...d, kind: r.kinds![0] }))
        setFailed(false)
      })
      .catch(() => setFailed(true))
  }, [draft.scene, draft.lang, draft.kind])

  useEffect(() => { refresh() }, []) // 只在挂载时拨一次；保存后显式 refresh

  /** 保存三段配置（唯一配置写口） */
  const saveConfig = () => {
    if (!cfg || busy) return
    setBusy(true); setSaved(false); setFailed(false)
    salesConfigSave(cfg)
      .then((r) => { if (r.success) { setSaved(true); refresh() } else setFailed(true) })
      .catch(() => setFailed(true))
      .finally(() => setBusy(false))
  }

  /** 保存一档租户词表（textarea 按行切词，空行由后端规范化丢掉） */
  const saveLexicon = () => {
    if (busy || !draft.scene || !draft.lang || !draft.kind) return
    setBusy(true); setSaved(false); setFailed(false)
    salesLexiconSave({ scene: draft.scene, lang: draft.lang, kind: draft.kind, words: draft.words.split('\n') })
      .then((r) => { if (r.success) { setSaved(true); setDraft((d) => ({ ...d, words: '' })); refresh() } else setFailed(true) })
      .catch(() => setFailed(true))
      .finally(() => setBusy(false))
  }

  /** 删除租户自有的一档：确认框只在动作函数体内弹一次且 danger */
  const deleteLexicon = async (id: number) => {
    if (busy) return
    if (!(await confirmDialog({ body: t('common.confirmTitle'), danger: true }))) return
    setBusy(true); setSaved(false); setFailed(false)
    salesLexiconDelete(id)
      .then((r) => { if (r.success) refresh(); else setFailed(true) })
      .catch(() => setFailed(true))
      .finally(() => setBusy(false))
  }

  /** 平台默认档补种（仅超管可见；幂等） */
  const seedLexicon = () => {
    if (busy) return
    setBusy(true); setSaved(false); setFailed(false)
    salesLexiconSeed()
      .then((r) => { if (r.success) { setSaved(true); refresh() } else setFailed(true) })
      .catch(() => setFailed(true))
      .finally(() => setBusy(false))
  }

  /** 改三段配置的一个字段（保持不可变更新，避免整页重渲染丢输入） */
  const patchFlow = (k: string, v: boolean | number | string) => {
    if (!cfg) return
    setCfg({ ...cfg, flow: { ...cfg.flow, [k]: v } })
  }
  const patchStrategy = (k: string, v: boolean | number | string) => {
    if (!cfg) return
    setCfg({ ...cfg, strategy: { ...cfg.strategy, [k]: v } })
  }

  return (
    <section data-testid="sales-config-page">
      <h2>{t('sales.config.title')}</h2>
      <p data-testid="sales-config-note">{t('sales.config.note')}</p>
      {failed && <p data-testid="sales-config-failed">{t('common.fail')}</p>}
      {saved && <p data-testid="sales-config-saved">{t('common.saved')}</p>}
      {!cfg && !failed && <p>{t('common.noData')}</p>}

      {cfg && (
        <>
          <h3>{t('sales.config.model.title')}</h3>
          <table data-testid="sales-config-model-table">
            <thead><tr><th>{t('sales.config.model.stage')}</th><th>{t('sales.config.model.value')}</th></tr></thead>
            <tbody>
              {stages.map((s) => (
                <tr key={s}>
                  <td>{s}</td>
                  <td>
                    <input aria-label={s} value={cfg.model_stages?.[s] ?? ''}
                           onChange={(e) => setCfg({ ...cfg, model_stages: { ...cfg.model_stages, [s]: e.target.value } })} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          <h3>{t('sales.config.flow.title')}</h3>
          <label>
            <input type="checkbox" checked={cfg.flow.enabled}
                   onChange={(e) => patchFlow('enabled', e.target.checked)} />
            {t('common.active')}
          </label>
          <label>
            <input type="checkbox" checked={cfg.flow.sweep_requeue}
                   onChange={(e) => patchFlow('sweep_requeue', e.target.checked)} />
            {t('sales.config.flow.sweepRequeue')}
          </label>
          <label>
            {t('sales.config.flow.maxSteps')}
            <input type="number" min={1} max={100} value={cfg.flow.max_steps}
                   onChange={(e) => patchFlow('max_steps', Number(e.target.value))} />
          </label>
          <label>
            {t('sales.config.flow.scene')}
            <select value={cfg.flow.default_scene} onChange={(e) => patchFlow('default_scene', e.target.value)}>
              {scenes.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
          </label>

          <h3>{t('sales.config.strategy.title')}</h3>
          <label>
            {t('sales.config.strategy.scene')}
            <select value={cfg.strategy.scene} onChange={(e) => patchStrategy('scene', e.target.value)}>
              {scenes.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
          </label>
          <label>
            {t('sales.config.strategy.lang')}
            <select value={cfg.strategy.lang} onChange={(e) => patchStrategy('lang', e.target.value)}>
              {langs.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
          </label>
          <label>
            {t('sales.config.strategy.minSamples')}
            <input type="number" min={1} max={1000} value={cfg.strategy.min_samples}
                   onChange={(e) => patchStrategy('min_samples', Number(e.target.value))} />
          </label>
          <label>
            {t('sales.config.strategy.draftEnabled')}
            <input type="checkbox" checked={cfg.strategy.draft_enabled}
                   onChange={(e) => patchStrategy('draft_enabled', e.target.checked)} />
          </label>
          <label>
            {t('sales.config.strategy.maxDrafts')}
            <input type="number" min={0} max={1000} value={cfg.strategy.max_drafts_per_run}
                   onChange={(e) => patchStrategy('max_drafts_per_run', Number(e.target.value))} />
          </label>
          <label>
            {t('sales.config.strategy.windowDays')}
            <input type="number" min={1} max={365} value={cfg.strategy.window_days}
                   onChange={(e) => patchStrategy('window_days', Number(e.target.value))} />
          </label>

          <h3>{t('sales.config.lexicon.title')}</h3>
          {rows.length === 0 && <p>{t('sales.config.lexicon.empty')}</p>}
          <div data-testid="sales-config-lexicon-list">
            {rows.map((r) => (
              <div key={r.id} data-testid="sales-config-lexicon-row" data-tenant={r.tenant_id}>
                <span>{r.scene}/{r.lang}/{r.kind}</span>
                <span>{r.words.length}</span>
                {r.tenant_id === 0 && <small>{t('sales.config.lexicon.platform')}</small>}
                {r.tenant_id !== 0 && (
                  <button data-testid="sales-config-lexicon-delete" disabled={busy}
                          onClick={() => deleteLexicon(r.id)}>{t('common.delete')}</button>
                )}
              </div>
            ))}
          </div>
          <div data-testid="sales-config-lexicon-form">
            <select aria-label="scene" value={draft.scene} onChange={(e) => setDraft({ ...draft, scene: e.target.value })}>
              {scenes.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
            <select aria-label="lang" value={draft.lang} onChange={(e) => setDraft({ ...draft, lang: e.target.value })}>
              {langs.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
            <select aria-label="kind" value={draft.kind} onChange={(e) => setDraft({ ...draft, kind: e.target.value })}>
              {kinds.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
            <textarea aria-label={t('sales.config.lexicon.words')} value={draft.words}
                      placeholder={t('sales.config.lexicon.words')}
                      onChange={(e) => setDraft({ ...draft, words: e.target.value })} />
            <button data-testid="sales-config-lexicon-save" disabled={busy} onClick={saveLexicon}>
              {t('common.save')}
            </button>
            {ad.isSuper && (
              <button data-testid="sales-config-lexicon-seed" disabled={busy} onClick={seedLexicon}>
                {t('sales.config.lexicon.seed')}
              </button>
            )}
          </div>

          <button data-testid="sales-config-save" disabled={busy} onClick={saveConfig}>
            {t('common.save')}
          </button>
        </>
      )}
    </section>
  )
}
