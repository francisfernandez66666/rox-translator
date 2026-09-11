// ============================================================================
// components/admin/ReferralP.tsx — 推荐奖励面板
// 职责：邀请码、二维码、邀请记录展示
// 从 panels_c.tsx 拆分
// ============================================================================
import { useEffect, useState } from 'react'
import { Button, Table, Input, Space } from 'tdesign-react'
import { referralMy, fetchReferralQrBlob } from '@/api'
import { Panel } from './parts'
import { fmtNum, fmtTime } from '@/lib/ui'
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'

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
  const [trialTokens, setTrialTokens] = useState(0)
  const [paidTokens, setPaidTokens] = useState(0)

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
          setTrialTokens((r.trial_tokens as number) || 0)
          setPaidTokens((r.paid_tokens as number) || 0)
        }
      } catch { /* ignore */ }
      const blob = await fetchReferralQrBlob()
      if (blob) setQrUrl(URL.createObjectURL(blob))
    })()
  }, [isPersonal])

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
      <Panel title={t('referral.title')} extra={<Space size={8}><Button onClick={downloadQr}>⬇️ {t('referral.downloadQr')}</Button></Space>}>
        <div style={{ display: 'flex', gap: 20, alignItems: 'flex-start', flexWrap: 'wrap' }}>
          <div style={{ flex: 1, minWidth: 320, background: 'rgba(64,128,255,.06)', border: '1px solid rgba(64,128,255,.18)', borderRadius: 8, padding: '14px 16px' }}>
            <div style={{ fontSize: 12, color: '#556' }}>{t('referral.myCode')}</div>
            <div style={{ fontSize: 22, fontWeight: 700, letterSpacing: 2, color: 'var(--td-brand-color-active, #1f33d6)', marginTop: 2 }}>{refCode || '—'}</div>
            <div style={{ fontSize: 12, color: '#667', marginTop: 8 }}>{t('referral.linkLabel')}</div>
            <div style={{ display: 'flex', gap: 8, marginTop: 4 }}>
              <Input readOnly value={inviteUrl} onFocus={(e: any) => e.target.select()} style={{ flex: 1 }} />
              <Button onClick={copyLink}>📋 {t('referral.copy')}</Button>
            </div>
            <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap', marginTop: 12, fontSize: 13, color: '#555' }}>
              <span>👥 {t('referral.invitedCount')}：<b>{invited}</b></span>
              <span>🎁 {t('referral.trialRewards')}：<b>{fmtNum(trialTokens)}</b> token / {trialCount} {t('referral.times')}</span>
              <span>💰 {t('referral.paidRewards')}：<b>{fmtNum(paidTokens)}</b> token</span>
            </div>
          </div>
          {qrUrl && <img src={qrUrl} alt="QR" width={150} height={150} style={{ borderRadius: 8, border: '1px solid #e3e6ef', background: '#fff' }} />}
        </div>
      </Panel>

      <Panel title={t('referral.title')}>
        <Table rowKey="invitee_uid" size="small" data={records as Any[]}
               columns={[
                 { colKey: 'invitee', title: t('referral.colInvitee'), cell: ({ row }: any) => `${String(row.invitee_name)} (#${String(row.invitee_uid)})` },
                 { colKey: 'invitee_email', title: t('referral.colEmail'), cell: ({ row }: any) => row.invitee_email || '—' },
                 { colKey: 'invite_status', title: t('referral.colInviteStatus'), width: 120, cell: () => <span style={{ color: '#1b8a3f', fontWeight: 600 }}>✅ {t('referral.invSuccess')}</span> },
                 { colKey: 'pay_status', title: t('referral.colPayStatus'), width: 120, cell: ({ row }: any) =>
                   row.paid
                     ? <span style={{ color: '#1b8a3f', fontWeight: 600 }}>✅ {t('referral.payYes')}</span>
                     : <span style={{ color: '#b26a00' }}>⏳ {t('referral.payNo')}</span> },
                 { colKey: 'reward', title: t('referral.colReward'), cell: ({ row }: any) =>
                   row.type === 'trial_stack'
                     ? <>+{fmtNum(row.tokens as number)} token{row.days ? ` / +${row.days} ${t('referral.daysUnit')}` : ''}</>
                     : <>+{fmtNum(row.tokens as number)} token</> },
                 { colKey: 'created_at', title: t('referral.colTime'), width: 165, cell: ({ row }: any) => fmtTime(row.created_at as string) },
               ] as never} />
        {!records.length && <div style={{ textAlign: 'center', color: '#999', padding: 8 }}>{t('referral.empty')}</div>}
      </Panel>
    </>
  )
}
