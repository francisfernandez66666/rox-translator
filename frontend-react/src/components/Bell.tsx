// ============================================================================
// components/Bell.tsx — 站内通知铃铛（langcross Badge + CSS 绝对定位浮层）
// 行为对齐：30s 未读轮询、下拉列表、单条已读/全部已读、feedback 类跳转后台反馈面板。
// 呈现层替换：TDesign Badge→Badge、Popup→页面级 CSS 浮层、Empty→EmptyState。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { Badge, BellIcon, EmptyState } from '@/ui/langcross/src'
import { notificationsUnread, notifications, notificationRead, notificationsReadAll } from '@/api'
import { fmtTime } from '@/lib/ui'
import { t } from '@/i18n'
import { useAdmin } from '@/stores/admin'

// ============ 本文件职责中文说明 ============
// 站内通知铃铛：未读轮询、下拉列表与已读操作。
// 挂载点有两处：前台顶栏（App.tsx 的 FrontShell，懒加载）与后台顶栏（admin/AdminDashboard，静态 import），
// 所以浮层不依赖任何外壳 CSS，样式与定位全在本文件自带。
// ========================================

// 站内通知条目数据结构（字段口径同后端 /api/notifications 列表项，不做前端改名）
interface NoticeItem {
  id: number
  title: string
  body: string
  ref_type?: string // 关联对象类型：feedback / ticket / quota / kb_scrape，决定点击跳去哪
  ref_id?: number // 关联对象 ID，含义随 ref_type 变（可为 0/缺省）
  created_at: string
}

// 默认导出组件：通知铃铛，负责未读轮询、下拉列表与已读操作（等价 Vue Bell.vue）
export default function Bell() {
  const [unread, setUnread] = useState(0)
  const [items, setItems] = useState<NoticeItem[]>([])
  const [open, setOpen] = useState(false)
  const { gotoPanel, isSuper, openFeedback } = useAdmin() // 后台跳转与超管判定都取自 admin store，铃铛自己不管路由
  const timerRef = useRef<number | null>(null) // 30s 轮询句柄：用 ref 而非 state，改它不该触发重渲染
  const wrapRef = useRef<HTMLDivElement>(null) // 「点击外部」判定基准：铃铛钮 + 浮层都在这层里

  // 刷新未读数；下拉展开时同步拉取最近 20 条通知列表
  // 列表只在 open 时才请求：收起状态下 30s 轮询只多打一个轻量的 unread 接口，省掉一次列表查询。
  // ⚠ 代价是 open 进了依赖数组 → 每次展开/收起都会重建 refresh → 下面那个 30s 定时器随之重启
  const refresh = useCallback(async () => {
    try {
      const u = await notificationsUnread()
      // `|| 0` 把缺省字段与 0 统一成数字 0，避免把 undefined 塞进 state 后徽标处比不出大小
      if (u.success) setUnread(Number((u as unknown as { unread?: number }).unread || 0))
      if (open) {
        const r = await notifications()
        // 后端一次最多回 100 条且无分页参数，这里再截到 20：浮层高 420px 也放不下更多，纯粹是自我上限
        if (r.success) setItems(((r as unknown as { notifications?: NoticeItem[] }).notifications || []).slice(0, 20))
      }
    } catch { /* 忽略 */ } // 顶栏铃铛是旁路信息：拉不到就保持上一次的读数，不 toast 不打断操作
  }, [open])

  // 挂载即刷新并启动 30s 未读轮询；卸载时清除定时器
  // deps 挂 refresh：open 一翻转就要换绑成「会顺带拉列表」的那版定时器回调，
  // 所以收起/展开会把 30s 计时重新开始计（顶栏可接受的小偏差，换来少一个 ref 转发）。
  // 定时器本身不 await 上一轮：慢响应按到达先后互相覆盖（后写赢），未读数这种幂等值无所谓。
  useEffect(() => {
    void refresh() // void 显式丢弃 promise：这里不关心结果，只触发
    timerRef.current = window.setInterval(refresh, 30000)
    // 与工单列表轮询不同，这里没有 document.hidden 判断：收起时也只打一个轻量 unread 接口
    return () => { if (timerRef.current) window.clearInterval(timerRef.current) }
  }, [refresh])

  // 点击浮层外部关闭（等价 TDesign Popup 的 trigger="click" 点击外部收起）
  // 监听随 open 挂/卸，收起态不在 document 上留全局 mousedown；
  // 铃铛钮与浮层都在 wrapRef 子树内，「点内部」由 contains 一次判掉，无需各元素 stopPropagation。
  // 用 mousedown 而不是 click：按下那一瞬就收起，浮层不会在该次点击落到别处时还杵着。
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  // 标记单条已读并刷新列表
  // 未套 runGuarded：请求失败只留一个未处理 rejection、不弹提示，未读数靠下一次 30s 轮询自愈
  // （这里也不弹：onItemClick 不 await markRead，toast 会漂到跳转后的新页面上）
  async function markRead(id: number) {
    await notificationRead(id)
    void refresh()
  }
  // 标记全部已读并刷新列表（不本地 setUnread(0)：以服务端回读为准，避免并发新通知被抹掉读数）
  async function markAll() {
    await notificationsReadAll()
    void refresh()
  }

  // 点击通知：先标记已读，再按类型跳转（feedback→超管反馈面板，其余→工单页）
  function onItemClick(n: NoticeItem) {
    void markRead(n.id) // 不 await：跳转要立刻发生，已读失败不该把用户卡在铃铛里
    // feedback 类通知 → 超管打开反馈处理面板；其余跳转工单
    // ref_id 的含义随 ref_type 变（feedback=反馈单 ID、ticket=工单 ID、quota=组织/租户 ID、kb_scrape=0），
    // 只有 feedback 分支会真的读它，所以强转写在这一行里；非超管打不开反馈面板，退化成工单列表
    // 注：gotoPanel / openFeedback 都来自 admin store，落点是后台 /admin 的工单面板
    //     （store 内 set panel + navigate('/admin')），不是前台 /tickets；跳转也不带 ref_id，
    //     所以除 feedback 外的通知只做到「把你送到工单面板」，不定位到具体那条工单。
    if (isSuper && n.ref_type === 'feedback') openFeedback(n.ref_id as number)
    else gotoPanel('tickets')
    setOpen(false) // 收起浮层：已在后台时点通知不会触发路由跳转，浮层得自己收，否则一直盖在内容上
  }

  // 切换浮层；展开时刷新列表
  // 展开额外打一次 refresh 而非等 30s 轮询：用户点开就是要看最新内容，等轮询会看到旧列表
  function toggle() {
    const next = !open
    setOpen(next)
    if (next) void refresh()
  }

  return (
    <div className="bell-wrap" ref={wrapRef}>
      {/* 样式随组件注入（与 TicketsPage 的 tk- 同口径：bell- 前缀只此一处在用）。
          字阶按 UI 真值 T 系（标题 13 / 正文 12 / 时间 11）；历史上 theme.css §十一 曾用
          `html .bell-trigger{padding:9px}` 提权覆写并放大本浮层字阶（#67/#68），
          2026-09-22 全站还原时该覆写层已删除，本文件即唯一来源。 */}
      <style>{BELL_CSS}</style>
      {/* aria-expanded 让读屏报出「已展开/已折叠」；未读变化不做 aria-live 播报，
          顶栏每 30s 刷一次，实时播报会持续打断用户 */}
      <button type="button" className="bell-trigger" onClick={toggle} aria-label={t('bell.title')} aria-expanded={open}>
        <BellIcon size={18} />
        {/* 未读为 0 时整个徽标不渲染（而非显示 0），铃铛回到纯图标 */}
        {unread > 0 && <Badge className="bell-count">{unread}</Badge>}
      </button>
      {/* 浮层自绘：绝对定位挂在 .bell-wrap(position:relative) 之下，替代原 Popup 的 popper 定位。
          ⚠ 这里只标了 role="menu"，条目仍是可点 div、没有 menuitem/方向键导航，键盘用户只能 Tab 到关闭 */}
      {open && (
        <div className="bell-panel" role="menu">
          <div className="bell-head">
            <b style={{ fontSize: 13 }}>{t('bell.title')}</b>
            <button type="button" className="bell-readall" onClick={markAll}>{t('bell.readAll')}</button>
          </div>
          {/* 空态与列表并存：无通知时只出 EmptyState（items 为空，map 自然产不出节点） */}
          {items.length === 0 && <EmptyState title={t('bell.empty')} />}
          {items.map((n) => (
            <div key={n.id} className="bell-item" onClick={() => onItemClick(n)}>
              <div style={{ fontWeight: 600, fontSize: 13 }}>{n.title}</div>
              {/* pre-wrap 保住后端正文里的换行（工单/反馈摘要常带 \n），否则整段塌成一行 */}
              <div style={{ fontSize: 12, color: 'var(--lc-text-3)', marginTop: 2, whiteSpace: 'pre-wrap' }}>{n.body}</div>
              <div style={{ fontSize: 11, color: 'var(--lc-text-4)', marginTop: 2 }}>{fmtTime(n.created_at)}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// —— 浮层（bell- 前缀，避免与组件库类名重名）——
const BELL_CSS = `
.bell-wrap{position:relative;display:inline-flex}
.bell-trigger{position:relative;display:inline-flex;align-items:center;justify-content:center;padding:6px;
  background:transparent;border:0;color:var(--lc-text-2);cursor:pointer;font-family:var(--lc-font);border-radius:var(--lc-r-bar)}
.bell-trigger:hover{color:var(--lc-text)}
/* 焦点环用 :focus-visible 而非 :focus：鼠标点击铃铛不画环，键盘 Tab 过来才出（顶栏不需要每次都糊一圈） */
.bell-trigger:focus-visible{outline:1.2px solid var(--lc-border-input);outline-offset:2px}
/* 未读数徽标挂在铃铛右上角（★ 2026-09-22 还原：#67 放大顶栏时曾外移到 -7/-9
   避免压住 18px 铃铛，现随字阶回档回到贴角 -1/-1）。 */
.bell-count{position:absolute;top:-1px;right:-1px}
/* 浮层 z-index:60 只在顶栏这个层叠上下文（.app-header 为 sticky + z-index:20，见 theme.css）
   内部比大小——够盖住下方页面内容，但对外盖不过页面级模态遮罩（如工单页 .tk-overlay 的 1200）。
   宽 340 + max-height 420 的固定盒：条目在 refresh 里已截到 20 条，靠自身 overflow-y 滚动，不做虚拟列表。 */
.bell-panel{position:absolute;top:calc(100% + 8px);right:0;z-index:60;width:340px;max-height:420px;overflow-y:auto;
  padding:8px;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-modal);
  box-shadow:var(--lc-panel-highlight)}
.bell-head{display:flex;justify-content:space-between;align-items:center;margin-bottom:6px}
.bell-readall{background:none;border:0;color:var(--lc-text-3);font-size:12px;cursor:pointer;font-family:var(--lc-font);padding:0}
.bell-readall:hover{color:var(--lc-text);text-decoration:underline}
.bell-item{padding:8px 6px;border-bottom:1px solid var(--lc-border-faint);cursor:pointer}
.bell-item:last-child{border-bottom:0}
.bell-item:hover{background:var(--lc-raised)}
`
