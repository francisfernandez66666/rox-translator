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
import { Button, Checkbox, DataTable, EmptyState, InlineBanner, Input, PageHeader, Select, Textarea } from '@/ui/langcross/src'
import { Field, Panel } from './parts'
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
    <div data-testid="sales-config-page">
      <PageHeader title={t('sales.config.title')} />
      <p data-testid="sales-config-note">{t('sales.config.note')}</p>
      {failed && (
        <div data-testid="sales-config-failed">
          <InlineBanner tone="error">{t('common.fail')}</InlineBanner>
        </div>
      )}
      {saved && (
        <div data-testid="sales-config-saved">
          <InlineBanner tone="success">{t('common.saved')}</InlineBanner>
        </div>
      )}
      {!cfg && !failed && <EmptyState title={t('common.noData')} />}

      {cfg && (
        <>
          <Panel title={t('sales.config.model.title')}>
            <div data-testid="sales-config-model-table">
              <DataTable
                rows={stages.map((s) => ({ stage: s }))}
                rowKey={(r) => r.stage}
                columns={[
                  { key: 'stage', title: t('sales.config.model.stage') },
                  { key: 'value', title: t('sales.config.model.value'), render: (r: { stage: string }) => (
                    <Input aria-label={r.stage} value={cfg.model_stages?.[r.stage] ?? ''}
                      onChange={(e) => setCfg({ ...cfg, model_stages: { ...cfg.model_stages, [r.stage]: e.target.value } })} />
                  ) },
                ]}
              />
            </div>
          </Panel>

          <Panel title={t('sales.config.flow.title')}>
            <Checkbox label={t('common.active')} checked={cfg.flow.enabled}
              onChange={(e) => patchFlow('enabled', e.target.checked)} />
            <Checkbox label={t('sales.config.flow.sweepRequeue')} checked={cfg.flow.sweep_requeue}
              onChange={(e) => patchFlow('sweep_requeue', e.target.checked)} />
            <Field label={t('sales.config.flow.maxSteps')}>
              <Input type="number" min={1} max={100} value={cfg.flow.max_steps}
                onChange={(e) => patchFlow('max_steps', Number(e.target.value))} />
            </Field>
            <Field label={t('sales.config.flow.scene')}>
              <Select display={cfg.flow.default_scene} value={cfg.flow.default_scene}
                onChange={(e) => patchFlow('default_scene', e.target.value)}>
                {scenes.map((s) => <option key={s} value={s}>{s}</option>)}
              </Select>
            </Field>
          </Panel>

          <Panel title={t('sales.config.strategy.title')}>
            <Field label={t('sales.config.strategy.scene')}>
              <Select display={cfg.strategy.scene} value={cfg.strategy.scene}
                onChange={(e) => patchStrategy('scene', e.target.value)}>
                {scenes.map((s) => <option key={s} value={s}>{s}</option>)}
              </Select>
            </Field>
            <Field label={t('sales.config.strategy.lang')}>
              <Select display={cfg.strategy.lang} value={cfg.strategy.lang}
                onChange={(e) => patchStrategy('lang', e.target.value)}>
                {langs.map((s) => <option key={s} value={s}>{s}</option>)}
              </Select>
            </Field>
            <Field label={t('sales.config.strategy.minSamples')}>
              <Input type="number" min={1} max={1000} value={cfg.strategy.min_samples}
                onChange={(e) => patchStrategy('min_samples', Number(e.target.value))} />
            </Field>
            <Checkbox label={t('sales.config.strategy.draftEnabled')} checked={cfg.strategy.draft_enabled}
              onChange={(e) => patchStrategy('draft_enabled', e.target.checked)} />
            <Field label={t('sales.config.strategy.maxDrafts')}>
              <Input type="number" min={0} max={1000} value={cfg.strategy.max_drafts_per_run}
                onChange={(e) => patchStrategy('max_drafts_per_run', Number(e.target.value))} />
            </Field>
            <Field label={t('sales.config.strategy.windowDays')}>
              <Input type="number" min={1} max={365} value={cfg.strategy.window_days}
                onChange={(e) => patchStrategy('window_days', Number(e.target.value))} />
            </Field>
          </Panel>

          <Panel title={t('sales.config.lexicon.title')}>
            {rows.length === 0 && <EmptyState title={t('sales.config.lexicon.empty')} />}
            <div data-testid="sales-config-lexicon-list">
              {rows.map((r) => (
                <div key={r.id} data-testid="sales-config-lexicon-row" data-tenant={r.tenant_id}>
                  <span>{r.scene}/{r.lang}/{r.kind}</span>
                  <span>{r.words.length}</span>
                  {r.tenant_id === 0 && <small>{t('sales.config.lexicon.platform')}</small>}
                  {r.tenant_id !== 0 && (
                    <Button data-testid="sales-config-lexicon-delete" variant="danger" size="sm" disabled={busy}
                      onClick={() => deleteLexicon(r.id)}>{t('common.delete')}</Button>
                  )}
                </div>
              ))}
            </div>
            <div data-testid="sales-config-lexicon-form">
              <Select aria-label="scene" display={draft.scene} value={draft.scene}
                onChange={(e) => setDraft({ ...draft, scene: e.target.value })}>
                {scenes.map((s) => <option key={s} value={s}>{s}</option>)}
              </Select>
              <Select aria-label="lang" display={draft.lang} value={draft.lang}
                onChange={(e) => setDraft({ ...draft, lang: e.target.value })}>
                {langs.map((s) => <option key={s} value={s}>{s}</option>)}
              </Select>
              <Select aria-label="kind" display={draft.kind} value={draft.kind}
                onChange={(e) => setDraft({ ...draft, kind: e.target.value })}>
                {kinds.map((s) => <option key={s} value={s}>{s}</option>)}
              </Select>
              <Textarea aria-label={t('sales.config.lexicon.words')} value={draft.words}
                placeholder={t('sales.config.lexicon.words')}
                onChange={(e) => setDraft({ ...draft, words: e.target.value })} />
              <Button data-testid="sales-config-lexicon-save" size="sm" disabled={busy} onClick={saveLexicon}>
                {t('common.save')}
              </Button>
              {ad.isSuper && (
                <Button data-testid="sales-config-lexicon-seed" size="sm" variant="secondary" disabled={busy} onClick={seedLexicon}>
                  {t('sales.config.lexicon.seed')}
                </Button>
              )}
            </div>
          </Panel>

          <Button data-testid="sales-config-save" disabled={busy} onClick={saveConfig}>
            {t('common.save')}
          </Button>
        </>
      )}
    </div>
  )
}
