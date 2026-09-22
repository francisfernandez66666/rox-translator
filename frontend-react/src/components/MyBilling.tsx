// ============================================================================
// MyBilling.tsx — 自服务「账单中心」增强（★ F8）
// 组合：BalancePanel（余额+不足横幅+充值引导，复用） + 用量趋势图（SVG 柱状，
// 近 30 天） + 订单/台账/奖励/发票四个可分页可筛选明细表。
// 后端端点：/api/billing/my/{overview,orders,ledger,rewards,invoices}。
// 呈现层替换：TDesign Card→.mb-card 自绘卡片、Select→原生 select + .lc-select、
//             Tabs.TabPanel（非受控）→langcross Tabs（受控 items）+ 外部 tab 状态。
// ============================================================================
import { useEffect, useState } from 'react'
import { fmtPoints } from '@/utils/points' // ★ S1 积分展示
import { Button, Tabs } from '@/ui/langcross/src'
import { t } from '@/i18n'
import { runGuarded } from '@/lib/runGuarded' // ★ #42：明细加载失败必须有可见出口（见各 Tab 的取数注释）
import {
  myOverview, myOrders, myLedger, myRewards, myInvoices,
  type OverviewResp, type MyOrder, type MyLedgerRow, type MyReward, type MyInvoice,
} from '@/api/mybilling'
import { BalancePanel } from './selfservice'

// 数字/积分的千分位格式化统一走 utils/points 的 fmtPoints（与余额、预估消耗同口径），本文件不自带

// ---------- 用量趋势图（近 30 天，UTC 日；SVG 柱状无第三方依赖） ----------
function TrendCard() {
  const [ov, setOv] = useState<OverviewResp | null>(null)
  // ★ #42（前端坏味道：数据加载的空 catch）：旧写法 `.catch(() => {/* 静默 */})` 把网络/鉴权错误
  //   整口吞掉，趋势图于是按「全月零消耗」画出来——用户看到的是「我这个月没用量」，而不是「取数失败」，
  //   属于最伤的一种误导。现走 runGuarded 弹后端原文（不新增 i18n 键），成功路径与旧实现完全一致。
  useEffect(() => {
    void (async () => {
      const r = await runGuarded(() => myOverview())
      if (r?.success) setOv(r)
    })()
  }, [])
  // 30 与后端 MyDailyUsage(tid, uid, 30) 的窗口保持一致
  const days = 30
  // 后端按 GROUP BY 日聚合，只回「有 ledger 行」的日子；这里先转 Map 便于按日 O(1) 回查
  const byDate = new Map((ov?.daily ?? []).map((d) => [d.date, d]))
  const bars: { date: string; cost: number; count: number }[] = []
  const today = new Date()
  // 逐日往前推 30 天补零：缺日也占一格，柱子才能等宽铺满横轴。
  // toISOString().slice(0,10) 取的是 UTC 日，与后端 substr(created_at,1,10)（RFC3339 前 10 位）同口径；
  // ⚠ 若应用服务器时区不是 UTC，两侧会在跨日边界差一天
  for (let i = days - 1; i >= 0; i--) {
    const dt = new Date(today.getTime() - i * 86400000).toISOString().slice(0, 10)
    const hit = byDate.get(dt)
    bars.push({ date: dt, cost: hit?.cost_points ?? 0, count: hit?.count ?? 0 })
  }
  // 下限取 1：整月无消耗时 max 为 0，下面 cost/max 会算出 Infinity/NaN 把 SVG 坐标打坏
  const max = Math.max(1, ...bars.map((b) => b.cost))
  const W = 600; const H = 120; const bw = W / days
  return (
    <div className="mb-card">
      <h3 style={{ margin: '4px 0 10px', fontSize: 15 }}>{t('ss2.trendTitle')}</h3>
      {ov ? (
        // viewBox 高度额外留 18px 给底部的首/末日标签，宽度交给 CSS 100% 自适应
        <svg viewBox={`0 0 ${W} ${H + 18}`} style={{ width: '100%', height: 'auto', display: 'block' }} role="img" aria-label={t('ss2.trendTitle')}>
          {bars.map((b, i) => {
            const h = Math.round((b.cost / max) * (H - 10))
            return (
              // 有消耗但极小的日至少给 2px 高，否则四舍五入成 0 就看不见；零消耗日高度夹 0 不画
              // ⚠ 顺带一提：cost=0 时 height 已是 0，下面 0.15 的透明度分支实际不会产生可见淡柱
              <rect key={b.date} x={i * bw + 1} y={H - h} width={bw - 2} height={Math.max(h, b.cost > 0 ? 2 : 0)}
                    // 单色规范：柱体用前景白，换掉原先的 TDesign 品牌蓝 --td-brand-color
                    rx={2} fill="var(--lc-text)" opacity={b.cost > 0 ? 0.85 : 0.15}>
                {/* 原生 SVG <title> 即悬停提示，省掉图表库的 tooltip 层 */}
                <title>{`${b.date} · ${fmtPoints(b.cost)} ${t('ss2.unitPoints')} · ${b.count}${t('ss2.unitCnt')}`}</title>
              </rect>
            )
          })}
          <text x={0} y={H + 14} fontSize={10} fill="var(--lc-text-3)">{bars[0].date}</text>
          <text x={W} y={H + 14} fontSize={10} fill="var(--lc-text-3)" textAnchor="end">{bars[days - 1].date}</text>
        </svg>
      ) : <div style={{ fontSize: 12, color: 'var(--lc-text-3)' }}>{t('ss2.loading')}</div>}
      {/* 纵轴没有刻度，改为在底部标出「最高单日」当作唯一参照值。
          ⚠ 整月无消耗时 max 是除零兜底的 1，fmtPoints(1) 因「非零最小显示 1 积分」规则会显示 1 而不是 0 */}
      <div style={{ fontSize: 12, color: 'var(--lc-text-4)', marginTop: 4 }}>{t('ss2.trendMax')}：{fmtPoints(max)} {t('ss2.unitPoints')}</div>
    </div>
  )
}

// ---------- 通用分页条 ----------
// 只做「上一页/下一页」：后端分页接口不返回页码列表，跳页无意义
function Pager(props: { page: number; total: number; size: number; onPage: (p: number) => void }) {
  // Math.max(1, …)：total=0 时 ceil 出来是 0，会让「下一页」按钮的禁用判断退化成一页都不存在
  // ⚠ 调用处写死的 size=10 必须与 api/mybilling 里 page() 的默认 size 一致，否则页数与后端真实切片对不上
  const pages = Math.max(1, Math.ceil(props.total / props.size))
  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 8, fontSize: 12 }}>
      <Button size="sm" variant="secondary" disabled={props.page <= 1} onClick={() => props.onPage(props.page - 1)}>{t('ss2.prev')}</Button>
      <span>{t('ss2.pageOf').replace('{p}', String(props.page)).replace('{n}', String(pages))}</span>
      <Button size="sm" variant="secondary" disabled={props.page >= pages} onClick={() => props.onPage(props.page + 1)}>{t('ss2.next')}</Button>
      <span style={{ color: 'var(--lc-text-3)' }}>{t('ss2.totalN').replace('{n}', String(props.total))}</span>
    </div>
  )
}

// ---------- 订单 ----------
function OrdersTab() {
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [rows, setRows] = useState<MyOrder[]>([])
  const [total, setTotal] = useState(0)
  // page/status 任一变化都重新拉取：筛选交给后端做（分页必须先过滤再切页，前端过滤会算错 total）。
  // ★ #42：原 `.catch(() => {/* 静默 */})` 让取数失败长成一个空表 + 「暂无数据」，改走 runGuarded。
  useEffect(() => {
    void (async () => {
      const r = await runGuarded(() => myOrders(page, status))
      if (r) { setRows(r.orders ?? []); setTotal(r.total ?? 0) }
    })()
  }, [page, status])
  return (
    <div>
      {/* 原生 select 借组件库 .lc-select 类取得同样的外观；空值项=「全部状态」，不进筛选 */}
      <select className="lc-select" aria-label={t('ss2.statusFilterAria')} style={{ width: 160, marginBottom: 8 }} value={status}
              onChange={(e) => { setStatus(e.target.value); setPage(1) }}>
        <option value="">{t('ss2.allStatus')}</option>
        <option value="paid">{t('ss2.stPaid')}</option>
        <option value="pending">{t('ss2.stPending')}</option>
        <option value="refunded">{t('ss2.stRefunded')}</option>
        <option value="cancelled">{t('ss2.stCancelled')}</option>
      </select>
      <table className="ss-table"><thead><tr>
        <th>{t('ss2.colOrderNo')}</th><th>{t('ss2.colPoints')}</th><th>{t('ss2.colMoney')}</th><th>{t('ss2.colStatus')}</th><th>{t('ss2.colChannel')}</th><th>{t('ss2.colCreatedAt')}</th>
      </tr></thead><tbody>
        {rows.map((o) => (
          <tr key={o.id}>
            <td>{o.order_no}</td><td>{fmtPoints(o.amount_points)}</td><td>{o.amount_money.toFixed(2)}</td>
            {/* 状态词按枚举首字母大写拼词典键（ss2.stPaid / stPending / stRefunded / stCancelled）：
                ⚠ 新增状态必须同步 i18n/panels/mybilling.ts，否则 t() 取不到词会把键名直接显示出来。
                manual_confirm=1 的线下/静态码单在超管确认前一直是 pending，追加「待管理员确认」免得用户以为卡死 */}
            <td>{t(`ss2.st${o.status.charAt(0).toUpperCase()}${o.status.slice(1)}`)}{o.manual_confirm === 1 && o.status === 'pending' ? ` · ${t('ss2.awaitConfirm')}` : ''}</td>
            {/* 线上渠道用 channel，线下/静态码收款只有 pay_method，二者取先有值的那个 */}
            <td>{o.channel || o.pay_method || '-'}</td><td>{o.created_at.slice(0, 10)}</td>
          </tr>
        ))}
        {!rows.length && <tr><td colSpan={6} style={{ color: 'var(--lc-text-3)' }}>{t('ss2.empty')}</td></tr>}
      </tbody></table>
      <Pager page={page} total={total} size={10} onPage={setPage} />
    </div>
  )
}

// ---------- 用量台账 ----------
function LedgerTab() {
  const [page, setPage] = useState(1)
  const [biz, setBiz] = useState('')
  const [rows, setRows] = useState<MyLedgerRow[]>([])
  const [total, setTotal] = useState(0)
  // ★ #42：口径同 OrdersTab —— 取数失败原先静默成空表，现走 runGuarded 把后端原文弹出来。
  useEffect(() => {
    void (async () => {
      const r = await runGuarded(() => myLedger(page, biz))
      if (r) { setRows(r.rows ?? []); setTotal(r.total ?? 0) }
    })()
  }, [page, biz])
  return (
    <div>
      <select className="lc-select" aria-label={t('ss2.bizFilterAria')} style={{ width: 160, marginBottom: 8 }} value={biz}
              onChange={(e) => { setBiz(e.target.value); setPage(1) }}>
        <option value="">{t('ss2.allBiz')}</option>
        <option value="text">{t('ss2.bizText')}</option>
        <option value="file">{t('ss2.bizFile')}</option>
      </select>
      <table className="ss-table"><thead><tr>
        <th>{t('ss2.colTime')}</th><th>{t('ss2.colBiz')}</th><th>{t('ss2.colMode')}</th><th>{t('ss2.colQty')}</th><th>{t('ss2.colCost')}</th><th>{t('ss2.colModel')}</th>
      </tr></thead><tbody>
        {rows.map((l) => (
          <tr key={l.id}>
            <td>{l.created_at.replace('T', ' ').slice(0, 16)}</td>
            {/* biz_kind 同订单状态一样按枚举拼键（bizText/bizFile）：
                ⚠ 台账还会出现 biz_kind='settle'（额度结算清零行），没有 ss2.bizSettle 词条时会露出键名 */}
            <td>{l.biz_kind ? t(`ss2.biz${l.biz_kind.charAt(0).toUpperCase()}${l.biz_kind.slice(1)}`) : '-'}</td>
            <td>{l.biz_mode === 'fast' ? t('ss2.modeFast') : l.biz_mode === 'pro' ? t('ss2.modePro') : '-'}</td>
            {/* 接口出口字段已是积分口径（cost_points 由后端换算），前端直接展示不再折算。
                ⚠ quantity 按后端注释是「字符数/句数」而不是积分，套 fmtPoints 只做千分位格式化 */}
            <td>{fmtPoints(l.quantity)}</td><td>{fmtPoints(l.cost_points)}</td><td>{l.model || '-'}</td>
          </tr>
        ))}
        {!rows.length && <tr><td colSpan={6} style={{ color: 'var(--lc-text-3)' }}>{t('ss2.empty')}</td></tr>}
      </tbody></table>
      <Pager page={page} total={total} size={10} onPage={setPage} />
    </div>
  )
}

// ---------- 邀请奖励明细 ----------
function RewardsTab() {
  const [page, setPage] = useState(1)
  const [rows, setRows] = useState<MyReward[]>([])
  const [total, setTotal] = useState(0)
  useEffect(() => {
    // ★ #42：原空 catch 让「拉取失败」和「这个月没有奖励」长成同一张空表
    void (async () => {
      const r = await runGuarded(() => myRewards(page))
      if (r) { setRows(r.rewards ?? []); setTotal(r.total ?? 0) }
    })()
  }, [page])
  return (
    <div>
      <table className="ss-table"><thead><tr>
        <th>{t('ss2.colInvitee')}</th><th>{t('ss2.colRwType')}</th><th>{t('ss2.colPoints')}</th><th>{t('ss2.colDays')}</th><th>{t('ss2.colRwPaid')}</th><th>{t('ss2.colTime')}</th>
      </tr></thead><tbody>
        {rows.map((rw) => (
          // 推荐奖励行来自 ListReferrals、接口没给自增 id，只能用「被邀请人 + 奖励类型」拼复合键
          <tr key={`${rw.invitee_uid}-${rw.type}`}>
            <td>{rw.invitee_name}{rw.invitee_email ? ` (${rw.invitee_email})` : ''}</td>
            {/* 未知 type 原样显示而不报错：奖励类型可能先在后端扩展，字典还没补 */}
            <td>{rw.type === 'trial_stack' ? t('ss2.rwTrial') : rw.type === 'paid_perm' ? t('ss2.rwPaidPerm') : rw.type}</td>
            <td>{fmtPoints(rw.reward_points)}</td><td>{rw.days > 0 ? rw.days : '-'}</td>
            <td>{rw.paid ? t('ss2.yes') : t('ss2.no')}</td><td>{rw.created_at.slice(0, 10)}</td>
          </tr>
        ))}
        {!rows.length && <tr><td colSpan={6} style={{ color: 'var(--lc-text-3)' }}>{t('ss2.empty')}</td></tr>}
      </tbody></table>
      <Pager page={page} total={total} size={10} onPage={setPage} />
    </div>
  )
}

// ---------- 发票（状态：待开/已开/已冲红，配合 C16） ----------
function InvoicesTab() {
  const [page, setPage] = useState(1)
  const [rows, setRows] = useState<MyInvoice[]>([])
  const [total, setTotal] = useState(0)
  useEffect(() => {
    // ★ #42：同上，发票列表取数失败必须可见（用户会以为申请没落单）
    void (async () => {
      const r = await runGuarded(() => myInvoices(page))
      if (r) { setRows(r.invoices ?? []); setTotal(r.total ?? 0) }
    })()
  }, [page])
  return (
    <div>
      <table className="ss-table"><thead><tr>
        <th>{t('ss2.colInvNo')}</th><th>{t('ss2.colInvTitle')}</th><th>{t('ss2.colInvTax')}</th><th>{t('ss2.colMoney')}</th><th>{t('ss2.colInvStatus')}</th><th>{t('ss2.colTime')}</th>
      </tr></thead><tbody>
        {rows.map((iv) => (
          <tr key={iv.id}>
            <td>{iv.invoice_no}</td><td>{iv.title}</td><td>{iv.tax_no}</td><td>{iv.amount_money.toFixed(2)}</td>
            {/* 后端直出 pending/issued/cancelled 三态；财务口径上 cancelled 就是「已冲红」，
                所以这里不取字面键 ss2.stCancelled（那是订单的「已取消」），而是显式映射到 ss2.invVoided。
                金额固定两位小数（元），不走 fmtPoints：这是现金不是积分 */}
            <td>{iv.status === 'pending' ? t('ss2.invPending') : iv.status === 'issued' ? t('ss2.invIssued') : iv.status === 'cancelled' ? t('ss2.invVoided') : iv.status}</td>
            <td>{iv.created_at.slice(0, 10)}</td>
          </tr>
        ))}
        {!rows.length && <tr><td colSpan={6} style={{ color: 'var(--lc-text-3)' }}>{t('ss2.empty')}</td></tr>}
      </tbody></table>
      <Pager page={page} total={total} size={10} onPage={setPage} />
    </div>
  )
}

// BillingCenter 计费中心页：概览 / 订单 / 消费流水 / 奖励 / 发票
export default function BillingCenter() {
  // langcross Tabs 是受控组件（不像原 TDesign Tabs 自带 defaultValue 内部态），切换状态得自己拿
  const [tab, setTab] = useState('orders')
  return (
    <div style={{ width: '100%', maxWidth: 900, margin: '0 auto', padding: 16 }}>
      <BalancePanel />
      <TrendCard />
      <div className="mb-card">
        <Tabs activeKey={tab} onChange={setTab} items={[
          { key: 'orders', label: t('ss2.tabOrders') },
          { key: 'ledger', label: t('ss2.tabLedger') },
          { key: 'rewards', label: t('ss2.tabRewards') },
          { key: 'invoices', label: t('ss2.tabInvoices') },
              ]} />
        {/* 四个面板只挂当前那个：切回来时各自的 useEffect 会重跑，数据保证是最新的，代价是重复请求 */}
        {tab === 'orders' && <OrdersTab />}
        {tab === 'ledger' && <LedgerTab />}
        {tab === 'rewards' && <RewardsTab />}
        {tab === 'invoices' && <InvoicesTab />}
    </div>
      {/* 页面级 CSS 随组件挂载注入（本项目约定：不引全局样式表，前缀隔离） */}
      <style>{CSS_MB}</style>
    </div>
  )
}

// 页面级样式：mb- 前缀（防与组件库/其他页面类名重名）
// ⚠ 本页表格用的 .ss-table 不在这里——它由 App.tsx 的全局 <style> 提供（theme.css 另有深色覆盖），
//    想调表格边框/内边距去改那两处，写在本文件里会被全局规则的优先级比掉
const CSS_MB = `
.mb-card{background:var(--lc-panel);border:1.2px solid var(--lc-border-card);border-radius:14px;padding:20px;margin-bottom:12px;box-shadow:var(--lc-panel-highlight)}
`
