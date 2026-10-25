// ============================================================================
// components/admin/InviteLinkCard.tsx — 企业邀请链接的「出图与转发」积木（★ 多身份方案 B4-c）
// 职责：把一枚刚生成的专属邀请链接渲染成可转发的卡片（复制链接／发邮件／二维码／下载），
//       并承载 §6.5 那条「API Key 级安全警示」的唯一一份文案与样式。
//
// 三条硬口径（改本件前必读）：
// ① **警示语只有一个出处**（§6.5）：`InviteLinkSecurityNote` 同时服务「创建后即时警示框」
//    与「邀请管理页常驻警示条」两面。两处各写一份字面量，迟早漂成「一页提示发信风险、
//    另一页什么都不说」——这一族在本仓反复出现过（页脚尾注、报价公式都是同一病）。
// ② **链接字符串由服务端出栈，前端只转发不拼**。列表与创建回执都带 `url`
//    （host 取法见 api/invites.ts 的注释）；这里禁止出现 `${location.host}/?invite=` 的拼法。
// ③ **二维码走 blob 且必须 revoke**。objectURL 不随组件卸载回收（§4.2-1 老账），
//    且拉图失败要把后端那句「链接已不可用」出出来，不许静默留一张坏图。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import { Button, Icon, StatusPill } from '@/ui/langcross/src'
import { fetchInviteLinkQrBlob, type InviteLinkCreateResult } from '@/api'
import { useT } from '@/i18n'
import { toastError, toastSuccess } from '@/lib/toastBus'
import { fmtTime } from '@/lib/ui'

// 面板内所有 <Icon n="…" style={MI}> 的同一档图标基线（14px、与文字同基线、右留 6px）；
// 本台不新增视觉规格，字号与其余管理台面同值（AGENTS §一·5 界面规格＝UI 交付真值）。
const MI = { fontSize: 14, verticalAlign: '-2px', marginInlineEnd: 6 }

/**
 * copyTextWithFallback 复制链接：优先异步剪贴板，非安全上下文／被拒时退回临时 textarea。
 * 只此一份实现（卡片与列表行共用）——两处各写一遍 execCommand 兜底，将来只修一处。
 */
export async function copyTextWithFallback(text: string): Promise<boolean> {
  if (!text) return false
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch { /* 落到下面的兜底路径 */ }
  try {
    const el = document.createElement('textarea')
    el.value = text
    document.body.appendChild(el)
    el.select()
    const ok = document.execCommand('copy')
    el.remove()
    return ok
  } catch {
    return false
  }
}

/**
 * InviteLinkSecurityNote §6.5 安全警示（一面一句、两面共用）。
 * 取向写在文案里：**持续可见 ＋ 随时撤销 ＋ 使用留痕**，刻意不做「阅后即焚」——
 * 焚掉之后管理员手里就没有那枚码了，误发的链接反而撤不掉（那是更坏的安全形态）。
 */
export function InviteLinkSecurityNote({ tone = 'warn', withIcon = true }: { tone?: 'warn' | 'inline'; withIcon?: boolean }) {
  const [, t] = useT()
  const box: React.CSSProperties = tone === 'warn'
    // 描边 1.2px ＋ --adm-warn-* 语义色＝交付档（AGENTS §一·5 〇-P 与 ApiKeysP 同值）
    ? { background: 'var(--adm-warn-bg)', border: '1.2px solid var(--adm-warn-bd)', borderRadius: 8, padding: 10, marginBottom: 10 }
    : { color: 'var(--adm-faint)', fontSize: 14, marginTop: 6 }
  return (
    <div data-testid="invite-link-note" style={box}>
      {withIcon && tone === 'warn' && <Icon n="alert" style={MI} />}
      <b style={{ fontSize: 14 }}>{t('inviteLink.warnTitle')}</b>
      <p style={{ fontSize: 14, margin: '4px 0 0', color: 'var(--adm-hint)' }}>{t('inviteLink.warnBody')}</p>
    </div>
  )
}

/**
 * InviteLinkCard 刚生成那一枚链接的结果卡（UsersP／OrgP 建号成功后展示）。
 * 卡片存在的意义就是「这一趟没建号，只发了一张入场券」——所以标题与正文都不许写成
 * 「用户已创建」，那是 R9 之前口径，会把「本人还没注册」这件事实抹掉。
 */
export function InviteLinkCard({ link, email }: { link: InviteLinkCreateResult; email?: string }) {
  const [, t] = useT()
  const [copied, setCopied] = useState(false)
  const [qr, setQr] = useState('')
  const [qrBusy, setQrBusy] = useState(false)
  // 当前 objectURL 留在 ref 里：卸载与重取都按它释放（同 ReferralP §4.2-1 口径）
  const qrRef = useRef('')
  const url = String(link.url || '')
  const id = Number(link.id || 0)

  useEffect(() => () => {
    if (qrRef.current) URL.revokeObjectURL(qrRef.current)
    qrRef.current = ''
  }, [])

  /** 取二维码（创建回执一到手就取一次：管理员当场就能转发，不用多点一下） */
  async function loadQr(download: boolean) {
    if (!id) return null
    setQrBusy(true)
    try {
      const blob = await fetchInviteLinkQrBlob(id, download)
      if (!download) {
        if (qrRef.current) URL.revokeObjectURL(qrRef.current)
        const objectUrl = URL.createObjectURL(blob)
        qrRef.current = objectUrl
        setQr(objectUrl)
      }
      return blob
    } catch (e) {
      // 后端对死链回的那句「请重新生成后再发二维码」要原样出现在提示里
      toastError((e as Error)?.message || t('inviteLink.qrFail'))
      return null
    } finally {
      setQrBusy(false)
    }
  }

  async function doCopy() {
    if (await copyTextWithFallback(url)) { setCopied(true); toastSuccess(t('inviteLink.copied')) }
    else toastError(t('inviteLink.copyFail'))
  }

  /** 下载二维码：同一次请求带 download=1（后端这才给 Content-Disposition） */
  async function doDownloadQr() {
    const blob = await loadQr(true)
    if (!blob) return
    const a = document.createElement('a')
    const u = URL.createObjectURL(blob)
    a.href = u
    a.download = `invite-${String(link.code || 'code')}.png`
    document.body.appendChild(a)
    a.click()
    a.remove()
    // 这一次是一次性下载件，点完即释放，不进 qrRef（那一格只挂展示用的图）
    URL.revokeObjectURL(u)
  }

  // mailto 的正文只放链接与一句说明：不预填「您的账号是 X／初始密码是 Y」——
  // 初始密码这件事在 R9 之后根本不存在，写进模板就是在教管理员发一封假的凭证邮件。
  const mailto = `mailto:${encodeURIComponent(email || '')}?subject=${encodeURIComponent(t('inviteLink.mailSubject'))}&body=${encodeURIComponent(`${t('inviteLink.mailIntro')}\n\n${url}`)}`

  return (
    <div data-testid="invite-link-card">
      <InviteLinkSecurityNote />
      <p style={{ fontSize: 14, margin: '0 0 6px', color: 'var(--adm-hint)' }}>{t('inviteLink.createdHint')}</p>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        <input
          className="lc-input"
          data-testid="invite-link-url"
          readOnly
          value={url}
          onFocus={(e) => e.target.select()}
          style={{ flex: 1, minWidth: 240 }}
        />
        <Button size="sm" variant="secondary" onClick={() => { void doCopy() }}><Icon n="clipboard" style={MI} />{t('inviteLink.copy')}</Button>
        {!!email && (
          <a className="lc-btn" href={mailto} style={{ textDecoration: 'none' }}><Icon n="mail" style={MI} />{t('inviteLink.mail')}</a>
        )}
        <Button size="sm" variant="secondary" onClick={() => { void loadQr(false) }} disabled={qrBusy}>{t('inviteLink.qr')}</Button>
        <Button size="sm" variant="secondary" onClick={() => { void doDownloadQr() }} disabled={qrBusy}><Icon n="download" style={MI} />{t('inviteLink.qrDownload')}</Button>
        {copied && <span style={{ fontSize: 14, color: 'var(--adm-hint)' }}>{t('inviteLink.copied')}</span>}
      </div>
      {!!link.expires_at && (
        <p style={{ fontSize: 14, color: 'var(--adm-faint)', margin: '8px 0 0' }}>
          {t('inviteLink.expiresPrefix')} <StatusPill tone="idle">{fmtTime(link.expires_at)}</StatusPill>
        </p>
      )}
      {qr && (
        <div style={{ marginTop: 10 }}>
          <img src={qr} width={150} height={150} alt="QR" style={{ borderRadius: 8, border: '1.2px solid var(--lc-border-card)', background: '#fff' }} />
        </div>
      )}
    </div>
  )
}
