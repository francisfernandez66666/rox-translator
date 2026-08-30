// ============================================================================
// components/selfservice.tsx — 端用户交易自服务面板
// 路由：/billing 余额  /invites 我的邀请  /packages 我的套餐  /my 我的账号
// 由 FrontShell 按 pathname 渲染，复用已通的计费/邀请/套餐接口。
// ============================================================================

// ============ 本文件职责中文说明 ============
// 端用户交易自服务面板：余额、邀请、套餐与账号信息展示（路由 /billing /invites /packages /my）。
// ========================================

import { useEffect, useState } from 'react'
import { Card, Tag, Loading, Button } from 'tdesign-react'
import { billingBalance } from '@/api/billing'
import { myPackage } from '@/api/billing'
import { referralMy, type ReferralMyResp } from '@/api/referral'
import { meContext } from '@/api'
import { useAuth } from '@/stores/auth'
import { fmtNum } from '@/lib/ui'

// useAsync：通用异步数据加载 Hook，自动管理 data/err/loading 状态；组件卸载后不再写入
function useAsync<T>(fn: () => Promise<T>, deps: readonly unknown[]) {
  const [data, setData] = useState<T | null>(null)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    let alive = true
    setLoading(true)
    fn().then(r => { if (alive) { setData(r as any); setErr('') } })
      .catch(e => { if (alive) setErr(String(e?.message ?? e)) })
      .finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  return { data, err, loading }
}

// 余额面板：展示当前用户的永久余额、发放台账与可用总额
export function BalancePanel() {
  const { data, err, loading } = useAsync(() => billingBalance(), [])
  if (loading) return <Loading className="ss-loading" />
  if (err) return <Card><Tag theme="danger">{err}</Tag></Card>
  const b = (data as any)?.balance ?? {}
  return (
    <div className="ss-grid">
      <Card>
        <h3>我的余额</h3>
        <div className="ss-row"><span>永久余额</span><b>{fmtNum(b.balance ?? 0)}</b></div>
        <div className="ss-row"><span>发放台账</span><b>{fmtNum((data as any)?.sub_grants_left ?? 0)}</b></div>
        <div className="ss-row"><span>可用总额</span><b>{fmtNum((data as any)?.total_available ?? 0)}</b></div>
      </Card>
    </div>
  )
}

// 我的邀请面板：展示个人邀请码、邀请链接、奖励统计与邀请记录
export function ReferralPanel() {
  const { data, err, loading } = useAsync<ReferralMyResp>(() => referralMy(), [])
  if (loading) return <Loading className="ss-loading" />
  if (err) return <Card><Tag theme="danger">{err}</Tag></Card>
  const d = data as ReferralMyResp
  const code = d?.ref_code ?? ''
  const url = d?.invite_url ?? ''
  const records = d?.records ?? []
  return (
    <div className="ss-grid">
      <Card>
        <h3>我的邀请</h3>
        <div className="ss-row">
          <span>个人邀请码</span>
          <div className="ss-copy">
            <Tag>{code}</Tag>
            <Button size="small" variant="outline" onClick={() => { navigator.clipboard?.writeText(code) }}>复制</Button>
          </div>
        </div>
        {url && <div className="ss-row"><span>邀请链接</span><Tag>{url}</Tag></div>}
        <div className="ss-stats">
          <div className="ss-stat"><span>体验叠加</span><b>{fmtNum(d?.trial_tokens ?? 0)}</b></div>
          <div className="ss-stat"><span>付费奖励</span><b>{fmtNum(d?.paid_tokens ?? 0)}</b></div>
          <div className="ss-stat"><span>已邀人数</span><b>{d?.invited ?? 0}</b></div>
        </div>
      </Card>
      {records.length > 0 && <Card>
        <h3>邀请记录</h3>
        <table className="ss-table"><thead><tr><th>类型</th><th>token</th><th>日期</th></tr></thead>
          <tbody>{records.map((r, i) => <tr key={i}><td>{r.type === 'paid_perm' ? '付费永久' : '体验叠加'}</td><td>{fmtNum(r.tokens)}</td><td>{r.created_at?.slice(0, 10)}</td></tr>)}</tbody></table>
      </Card>}
    </div>
  )
}

// 我的套餐面板：展示当前套餐、剩余句数、可用 token 与永久余额
export function MyPackagePanel() {
  const { data, err, loading } = useAsync(() => myPackage(), [])
  if (loading) return <Loading className="ss-loading" />
  if (err) return <Card><Tag theme="danger">{err}</Tag></Card>
  const p = (data as any) ?? {}
  return (
    <div className="ss-grid">
      <Card>
        <h3>我的套餐</h3>
        <div className="ss-row"><span>当前包</span><Tag>{p.package_code ?? '—'}</Tag></div>
        <div className="ss-row"><span>剩余句数</span><b>{fmtNum(p.sentence_balance ?? 0)} 句</b></div>
        <div className="ss-row"><span>可用 token</span><b>{fmtNum(p.tokens ?? p.balance_tokens ?? 0)}</b></div>
        <div className="ss-row"><span>永久余额</span><b>{fmtNum(p.permanent_balance ?? 0)}</b></div>
      </Card>
    </div>
  )
}

// 我的账号面板：展示用户名/邮箱/角色/租户，并提供余额/邀请/套餐快捷入口
export function AccountPanel() {
  const { user } = useAuth()
  const { data, err, loading } = useAsync(() => meContext(), [])
  const ctx = (data as any) ?? {}
  if (loading) return <Loading className="ss-loading" />
  if (err) return <Card><Tag theme="danger">{err}</Tag></Card>
  return (
    <div className="ss-grid">
      <Card>
        <h3>我的账号</h3>
        <div className="ss-row"><span>用户名</span><b>{ctx.username ?? user?.username ?? '—'}</b></div>
        <div className="ss-row"><span>邮箱</span><Tag>{ctx.email ?? user?.email ?? '未绑定'}</Tag></div>
        <div className="ss-row"><span>角色</span><Tag>{ctx.role ?? user?.role ?? '—'}</Tag></div>
        <div className="ss-row"><span>租户</span><b>{ctx.tenant_name ?? ctx.tenant_id ?? '—'}</b></div>
      </Card>
      <Card>
        <h3>快速入口</h3>
        <div className="ss-quick">
          <Button size="small" variant="outline" onClick={() => { window.history.pushState({}, '', '/billing'); window.dispatchEvent(new PopStateEvent('popstate')) }}>💰 我的余额</Button>
          <Button size="small" variant="outline" onClick={() => { window.history.pushState({}, '', '/invites'); window.dispatchEvent(new PopStateEvent('popstate')) }}>🔗 我的邀请</Button>
          <Button size="small" variant="outline" onClick={() => { window.history.pushState({}, '', '/packages'); window.dispatchEvent(new PopStateEvent('popstate')) }}>💎 我的套餐</Button>
        </div>
      </Card>
    </div>
  )
}
