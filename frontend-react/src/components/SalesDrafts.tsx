// ============================================================================
// components/SalesDrafts.tsx — 二期卖家面「说法草稿」inbox（施工_二期 §9.4）
//
// 形态：左＝草稿清单（draft/approved/rejected 三态过滤），右＝详情（客户原话＋
// 建议说法＋锚位＋溯源）。**唯一写动作＝转正**——没有删除／批量／自动生效口，
// 前端不造第二个写侧调用（锁 salesDrafts.noAutoApprove）；草稿转正前永不生效，
// 召回命中 0 由后端 M1/M3 判（store/sales_draft_gate_test.go）。
//
// 文案纪律：
//  ① 能力三态 off/half/on 各出不同形的事实读数（半套配置不许呈现为已生效）；
//     探不到（null）fail-open——页面照常可用，不钉死「这个站永远没有销售引擎」。
//  ② 引擎未开启那档的卖家面文案复用一期 ExhaustNotice（copyFor 单一出口），
//     本组件不自造第三处耗尽/停用文案（锁 salesDrafts.copySingleSource）。
//
// 本文件由 App.tsx 的登录态壳 React.lazy 加载，禁止入口包静态 import。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { Button, EmptyState, InlineBanner, PageHeader, Tabs } from '@/ui/langcross/src'
import { salesCapability, salesDraft, salesDraftApprove, salesDrafts, type SalesDraft } from '@/api/sales'
import { ExhaustNotice } from './ProductShelf'

/** 状态过滤档（空＝全部）；与后端 sales_drafts.status 三值同源 */
const FILTERS = ['', 'draft', 'approved', 'rejected'] as const

/** 说法草稿 inbox（登录态卖家面） */
export default function SalesDrafts() {
  const [, t] = useT()
  const [filter, setFilter] = useState<string>('')
  const [list, setList] = useState<SalesDraft[]>([])
  const [selected, setSelected] = useState<SalesDraft | null>(null)
  // null＝探针未回/失败 ⇒ fail-open 照常渲染；off/half/on 三态各不同形
  const [capState, setCapState] = useState<string | null>(null)
  const [loadFailed, setLoadFailed] = useState(false)
  const [approveFailed, setApproveFailed] = useState(false)
  const [busy, setBusy] = useState(false)

  /** 列草稿：失败出声（不伪装成空数据），成功时空集就是 [] */
  const refresh = useCallback(() => {
    salesDrafts(filter || undefined)
      .then((r) => {
        if (r.success) {
          setList(Array.isArray(r.drafts) ? r.drafts : [])
          setLoadFailed(false)
        } else {
          setLoadFailed(true)
        }
      })
      .catch(() => setLoadFailed(true))
  }, [filter])

  useEffect(() => { refresh() }, [refresh])

  // 能力探针只拨一次；失败不写状态（一次抖动不许钉成「永远没有」）
  useEffect(() => {
    let alive = true
    salesCapability()
      .then((r) => {
        if (alive && r.success && typeof r.status === 'string') setCapState(r.status)
      })
      .catch(() => { /* fail-open：页面照常可用 */ })
    return () => { alive = false }
  }, [])

  /** 选一条草稿看详情（按 id 取最新，列表行可能是旧快照） */
  const select = (id: string) => {
    salesDraft(id)
      .then((r) => { if (r.success && r.draft) setSelected(r.draft) })
      .catch(() => { /* 取不到详情保留上一次选中 */ })
  }

  /** 唯一写动作：人审转正。成功后列表与详情同步刷新；失败出声不假装成功 */
  const approve = () => {
    if (!selected || busy || selected.status !== 'draft') return
    setBusy(true)
    setApproveFailed(false)
    salesDraftApprove(selected.id)
      .then((r) => {
        if (r.success) {
          setSelected({ ...selected, status: 'approved' })
          refresh()
        } else {
          setApproveFailed(true)
        }
      })
      .catch(() => setApproveFailed(true))
      .finally(() => setBusy(false))
  }

  /** 状态词取词（三态各不同键；未知状态不猜，直出原值供排障） */
  const statusLabel = (s: string) =>
    s === 'draft' || s === 'approved' || s === 'rejected' ? t(`sales.drafts.status.${s}`) : s

  return (
    <div data-testid="sales-drafts-page">
      <PageHeader title={t('sales.drafts.title')} />
      {/* 能力三态事实读数：off/half/on 各不同形；null 不渲染（fail-open）。
          data-state 把「这台此刻的后端状态词」原样带出来，供 e2e 做等值判据：
          译文会随语种变，拿文案做等值必然假绿，必须拿状态词本身比。 */}
      {capState !== null && (
        <p data-testid="sales-capability-state" data-state={capState}>{t(`sales.capability.${capState}`)}</p>
      )}
      {/* 引擎未开启那档的卖家面事实文案：只经一期 copyFor 单一出口（ExhaustNotice） */}
      {capState === 'off' && <ExhaustNotice reason="disabled" audience="owner" />}
      <div aria-label={t('sales.drafts.title')}>
        <Tabs
          items={FILTERS.map((f) => ({
            key: f || 'all',
            label: t(f ? `sales.drafts.filter.${f}` : 'sales.drafts.filter.all'),
          }))}
          activeKey={filter || 'all'}
          onChange={(k) => setFilter(k === 'all' ? '' : k)}
        />
      </div>
      {loadFailed && (
        <div data-testid="sales-drafts-load-failed">
          <InlineBanner tone="error">{t('sales.drafts.loadFailed')}</InlineBanner>
        </div>
      )}
      {/* 两栏布局改用组件库 --lc-* 类名，不再写内联 style */}
      <div className="lc-page-head">
        <div className="lc-shell__main" data-testid="sales-drafts-list">
          {!loadFailed && list.length === 0 && <EmptyState title={t('sales.drafts.empty')} />}
          {list.map((d) => (
            <div key={d.id} data-testid="sales-draft-row">
              <Button variant="secondary" size="sm" onClick={() => select(d.id)}
                aria-current={selected?.id === d.id}>
                {d.suggested_text || d.source_text || d.id}
              </Button>
              <small>{statusLabel(d.status)}</small>
            </div>
          ))}
        </div>
        <div className="lc-shell__main" data-testid="sales-draft-detail">
          {!selected && <EmptyState title={t('sales.drafts.detailEmpty')} />}
          {selected && (
            <div>
              <p><strong>{t('sales.drafts.source')}</strong>：{selected.source_text}</p>
              <p><strong>{t('sales.drafts.suggested')}</strong>：{selected.suggested_text}</p>
              <p><strong>{t('sales.drafts.anchor')}</strong>：{selected.anchor || '—'}</p>
              <p><strong>{t('sales.drafts.attribution')}</strong>：{selected.attribution_ref || '—'}</p>
              <p data-testid="sales-draft-status">{statusLabel(selected.status)}</p>
              {/* 唯一写动作：只在草稿态渲染；已转正/已拒绝没有可写按钮 */}
              {selected.status === 'draft' && (
                <Button data-testid="sales-approve-btn" onClick={approve} disabled={busy}>
                  {t('sales.drafts.approve')}
                </Button>
              )}
              {approveFailed && (
                <div data-testid="sales-approve-failed">
                  <InlineBanner tone="error">{t('sales.drafts.approveFailed')}</InlineBanner>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
