// ============================================================================
// components/admin/ReferralP.tsx — 推荐奖励面板
// 职责：邀请码、二维码、邀请记录展示
// 从 panels_c.tsx 拆分
// 2026-09-18（UI 融合）：按钮/统计行/状态列的 emoji 前缀清理，邀请码大字色改用
//   主题色变量兜底（变量缺失时回落到暗色主题的正文浅色）；奖励额度一律
//   积分口径（接口出口即积分，fmtPoints 只做千分位格式化）。
// ============================================================================
import { useEffect, useState } from 'react'
import { Button, DataTable } from '@/ui/langcross/src'
import { referralMy, fetchReferralQrBlob } from '@/api'
import { Panel } from './parts'
import { fmtTime } from '@/lib/ui'
import { fmtPoints } from '@/utils/points' // ★ S1 积分口径：千分位格式化
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'

/** Any 邀请返佣出参宽松别名 */
type Any = Record<string, any>

/** 推荐奖励面板：邀请码、二维码、邀请记录 */
export function ReferralP() {
  const ad = useAdmin()
  const [, t] = useT()
  const isPersonal = ad.isPersonal
  const [refCode, setRefCode] = useState('')
  const [inviteUrl, setInviteUrl] = useState('')
  const [records, setRecords] = useState<Any[]>([])
  const [qrUrl, setQrUrl] = useState('')
  const [invited, setInvited] = useState(0)
  const [trialCount, setTrialCount] = useState(0)
  const [trialPoints, setTrialPoints] = useState(0)
  const [paidPoints, setPaidPoints] = useState(0)

  // 拉取我的邀请数据 + 生成二维码：非个人用户直接跳过（不发无意义的请求）；
  // 二维码走 blob（后端出图，前端只挂 objectURL），故能随「下载二维码」按钮直接落盘。
  useEffect(() => {
    if (!isPersonal) return
    void (async () => {
      try {
        const r: Any = await referralMy()
        if (r.success) {
          setRefCode(r.ref_code || '')
          setInviteUrl(r.invite_url || '')
          setRecords((r.records as Any[]) || [])
          setInvited((r.invited as number) || 0)
          setTrialCount((r.trial_count as number) || 0)
          setTrialPoints((r.trial_points as number) || 0)
          setPaidPoints((r.paid_points as number) || 0)
        }
      } catch { /* ignore */ }
      const blob = await fetchReferralQrBlob()
      if (blob) setQrUrl(URL.createObjectURL(blob))
    })()
  }, [isPersonal])

  // 权限收口（2026-09）：邀请裂变仅个人用户参与。这里放在所有 hooks 之后 return，
  // 顺序不能提前——否则 hook 数量在不同用户间变化会触发 React 报错。
  if (!isPersonal) return null

  function downloadQr() {
    if (!qrUrl) return
    const a = document.createElement('a')
    a.href = qrUrl; a.download = `invite-${refCode || 'code'}.png`
    document.body.appendChild(a); a.click(); a.remove()
  }

  function fallbackCopy(text: string) {
    const el = document.createElement('textarea')
    el.value = text
    document.body.appendChild(el)
    el.select()
    try { document.execCommand('copy') } catch { /* ignore */ }
    el.remove()
  }

  function copyLink() {
    if (!inviteUrl) return
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        void navigator.clipboard.writeText(inviteUrl).catch(() => fallbackCopy(inviteUrl))
      } else fallbackCopy(inviteUrl)
    } catch { fallbackCopy(inviteUrl) }
  }

  return (
    <>
 <Panel title={t('referral.title')} extra={<Button variant="secondary" onClick={downloadQr}> {t('referral.downloadQr')}</Button>}>
        <div style={{ display: 'flex', gap: 20, alignItems: 'flex-start', flexWrap: 'wrap' }}>
          <div style={{ flex: 1, minWidth: 320, background: 'rgba(255,255,255,.04)', border: '1.2px solid var(--lc-border-card)', borderRadius: 8, padding: '14px 16px' }}>
            <div style={{ fontSize: 12, color: 'var(--adm-hint)' }}>{t('referral.myCode')}</div>
            <div style={{ fontSize: 22, fontWeight: 700, letterSpacing: 2, color: 'var(--lc-text-1)', marginTop: 2 }}>{refCode || '—'}</div>
            <div style={{ fontSize: 12, color: 'var(--adm-hint)', marginTop: 8 }}>{t('referral.linkLabel')}</div>
            <div style={{ display: 'flex', gap: 8, marginTop: 4 }}>
              <input className="lc-input" readOnly value={inviteUrl} onFocus={(e: any) => e.target.select()} style={{ flex: 1 }} />
              <Button onClick={copyLink}> {t('referral.copy')}</Button>
            </div>
            {/* 三项汇总：邀请人数 / 试用奖励（积分 + 次数）/ 付费奖励（积分）。
                接口出口即积分口径，界面直接展示。 */}
            <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap', marginTop: 12, fontSize: 13, color: 'var(--adm-hint)' }}>
              <span> {t('referral.invitedCount')}：<b>{invited}</b></span>
              <span> {t('referral.trialRewards')}：<b>{fmtPoints(trialPoints)}</b> {t('referral.unitPoints')} / {trialCount} {t('referral.times')}</span>
              <span> {t('referral.paidRewards')}：<b>{fmtPoints(paidPoints)}</b> {t('referral.unitPoints')}</span>
            </div>
          </div>
          {qrUrl && <img src={qrUrl} alt="QR" width={150} height={150} style={{ borderRadius: 8, border: '1.2px solid var(--lc-border-card)', background: '#fff' }} />}
        </div>
      </Panel>

      <Panel title={t('referral.title')}>
        <DataTable<any> rowKey={(row) => String(row.invitee_uid)} rows={records as Any[]}
               columns={[
                 { key: 'invitee', title: t('referral.colInvitee'), render: (row) => `${String(row.invitee_name)} (#${String(row.invitee_uid)})` },
                 { key: 'invitee_email', title: t('referral.colEmail'), render: (row) => row.invitee_email || '—' },
                 // 邀请状态：能进列表的记录即已入库成功，故不读 row，恒显示「邀请成功」
                 // （✅ 图形字符 2026-09-18 起移除，状态由成功色 + 文案表达）
 { key: 'invite_status', title: t('referral.colInviteStatus'), width: 120, render: () => <span style={{ color: 'var(--adm-ok-tx)', fontWeight: 600 }}> {t('referral.invSuccess')}</span> },
                 // 付费状态：被邀请人是否已付费——已付用成功色、未付用琥珀色（催付语义）
                 { key: 'pay_status', title: t('referral.colPayStatus'), width: 120, render: (row) =>
                   row.paid
 ? <span style={{ color: 'var(--adm-ok-tx)', fontWeight: 600 }}> {t('referral.payYes')}</span>
                     : <span style={{ color: 'var(--adm-amber-tx)' }}>{t('referral.payNo')}</span> },
                 { key: 'reward', title: t('referral.colReward'), render: (row) =>
                   row.type === 'trial_stack'
                     ? <>+{fmtPoints(row.reward_points as number)} {t('referral.unitPoints')}{row.days ? ` / +${row.days} ${t('referral.daysUnit')}` : ''}</>
                     : <>+{fmtPoints(row.reward_points as number)} {t('referral.unitPoints')}</> },
                 { key: 'created_at', title: t('referral.colTime'), width: 165, render: (row) => fmtTime(row.created_at as string) },
               ]}  />
        {!records.length && <div style={{ textAlign: 'center', color: 'var(--adm-faint)', padding: 8 }}>{t('referral.empty')}</div>}
      </Panel>
    </>
  )
}
