// ============================================================================
// components/admin/TmFlowP.tsx — 翻译记忆「提审进度」面板（★ F-62，2026-09-26 批 I-8）
// 职责：租户侧**只读**查看自己提交的双语语料 / TMX / 命中候选在平台的审核去向与进度。
//
// 为什么存在（缺陷因果链）：
//   ① 导入双语语料/TMX 后回执说的是「已提交，待平台审核」（F-24 把文案腿改诚实了）；
//   ② 审批确实会写入本租户翻译记忆（超管「通过」→ SaveBack module=manual）；
//   ③ 但审核进度**只有超管接口**（/api/admin/tm-review/*，租户调用直接 403），
//      客户被告知「待审」却永远看不到去向 ⇒ 文案诚实了，链路仍是静默降级。
// 本面板补的就是 ③：读 GET /api/me/tm-review/list（后端按 token 内租户裁剪，前端不传租户号）。
//
// 权限与形态：
//   - 平台超管（tenant_id=0）在本接口没有租户归属（后端直接 403），这里对超管显示一句
//     「本视图按租户展示」的中性提示，不打必然失败的请求；
//     ★ 诚实口径：超管侧的审批目前**只有接口**（/api/admin/tm-review/*），前台没有审核台页面，
//       所以提示文案**不得**写「请使用平台审核台」——那是把 F-62 的「说到做不到」换个位置再犯一遍；
//   - 全表只读：本面板没有任何审批/驳回入口（审批权仍在超管独占）。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { Button, DataTable, StatusPill } from '@/ui/langcross/src'
import { useT, tpl } from '@/i18n'
import { listMyTmReview, type MyTmReviewItem, type MyTmReviewSummary } from '@/api/tmreview'
import { useAdmin } from '@/stores/admin'
import { langLabel } from '@/lib/langNames'
import { fmtTime } from '@/lib/ui'
import { Panel } from './parts'

// 状态筛选档位：'' = 全部（后端白名单只有三态，别在这里发明第四态）
const STATUS_OPTIONS = ['', 'pending', 'approved', 'rejected'] as const

// toneForStatus 三态 → 交付语气档（success/danger/warn 都是 StatusPill 既有档，不新造颜色，
// 遵守 §一·5「UI 规格=交付真值」：这里只选档，不写死任何 hex）。
function toneForStatus(status: string): 'success' | 'danger' | 'warn' | 'idle' {
  if (status === 'approved') return 'success'
  if (status === 'rejected') return 'danger'
  if (status === 'pending') return 'warn'
  return 'idle'
}

/**
 * TmFlowP 提审进度面板（只读）。
 * 顶部：审核时效说明 + 三态计数摘要 + 状态筛选 + 刷新；
 * 表格：源句 / 译文 / 语言 / 状态 / 命中次数 / 提交时间 / 审核时间。
 */
export default function TmFlowP() {
  const [uiLang, t] = useT()
  const { isSuper } = useAdmin()
  const [status, setStatus] = useState<string>('')
  const [rows, setRows] = useState<MyTmReviewItem[]>([])
  const [summary, setSummary] = useState<MyTmReviewSummary | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [loading, setLoading] = useState(false)
  const [failed, setFailed] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setFailed(false)
    try {
      const r = await listMyTmReview(status)
      if (r.success) {
        setRows(r.candidates || [])
        if (r.summary) setSummary(r.summary)
        setTruncated(!!r.truncated)
      } else {
        setFailed(true)
      }
    } catch {
      // 403/网络错都在这里收敛成「加载失败 + 重试」，不把后端原文甩进界面
      setFailed(true)
    } finally {
      setLoading(false)
    }
  }, [status])

  useEffect(() => {
    if (isSuper) return // 超管无租户归属：不打这一下（面板显示审核台指引）
    void load()
  }, [isSuper, load])

  if (isSuper) {
    return (
      <Panel title={t('kb.tmFlowTitle')}>
        <div style={{ fontSize: 15, color: 'var(--adm-hint)', padding: '8px 0' }}>{t('kb.tmFlowSuperHint')}</div>
      </Panel>
    )
  }

  return (
    <Panel title={t('kb.tmFlowTitle')}>
      {/* 审核时效说明（F-62 文案腿：告诉客户「待审」之后会发生什么、多久） */}
      <div style={{ fontSize: 15, color: 'var(--adm-hint)', margin: '4px 0 10px', lineHeight: 1.7 }}>
        {t('kb.tmFlowSla')}
      </div>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
        <select className="lc-select" value={status} onChange={(e) => setStatus(e.target.value)} style={{ width: 150 }}>
          {STATUS_OPTIONS.map((s) => (
            <option key={s || 'all'} value={s}>
              {s === '' ? t('kb.allStatus') : s === 'pending' ? t('kb.pending') : s === 'approved' ? t('kb.approved') : t('kb.rejected')}
            </option>
          ))}
        </select>
        <Button onClick={() => void load()} disabled={loading}>{t('common.refresh')}</Button>
        {summary && (
          <span style={{ fontSize: 15, color: 'var(--adm-faint)' }}>
            {tpl('kb.tmFlowSummary', {
              pending: summary.pending, approved: summary.approved, rejected: summary.rejected, total: summary.total,
            })}
          </span>
        )}
      </div>
      {failed && (
        <div style={{ fontSize: 15, color: 'var(--lc-danger)', marginBottom: 8 }}>{t('kb.tmFlowFail')}</div>
      )}
      {!failed && (
        <DataTable<MyTmReviewItem>
          rowKey={(row) => String(row.id)}
          rows={rows}
          columns={[
            { key: 'zh', title: t('kb.colSource') },
            { key: 'trans', title: t('kb.colTranslation') },
            { key: 'lang', title: t('kb.colLang'), width: 120, render: (row) => langLabel(row.lang, uiLang) },
            {
              key: 'status', title: t('kb.colStatus'), width: 110, render: (row) => (
                <StatusPill tone={toneForStatus(row.status)}>
                  {row.status === 'pending' ? t('kb.pending') : row.status === 'approved' ? t('kb.approved') : row.status === 'rejected' ? t('kb.rejected') : row.status}
                </StatusPill>
              ),
            },
            { key: 'hit', title: t('kb.tmFlowColHit'), width: 90, render: (row) => String(row.hit_count ?? 0) },
            { key: 'created', title: t('kb.tmFlowColSubmitted'), width: 160, render: (row) => fmtTime(row.created_at, true) },
            { key: 'reviewed', title: t('kb.tmFlowColReviewed'), width: 160, render: (row) => fmtTime(row.reviewed_at, true) },
          ]}
        />
      )}
      {!failed && !loading && rows.length === 0 && (
        <div style={{ fontSize: 15, color: 'var(--adm-faint)', marginTop: 8 }}>{t('kb.tmFlowEmpty')}</div>
      )}
      {/* 列表有 200 条上限：被截断时如实说明，摘要数字仍是全量真计数（不拿 rows.length 冒充总数） */}
      {truncated && (
        <div style={{ fontSize: 14, color: 'var(--adm-faint)', marginTop: 8 }}>
          {tpl('kb.tmFlowTruncated', { n: rows.length })}
        </div>
      )}
    </Panel>
  )
}
