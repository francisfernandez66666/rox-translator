// ============================================================================
// components/Bell.tsx — 站内通知铃铛（langcross Badge + CSS 绝对定位浮层）
// 行为对齐：30s 未读轮询、下拉列表、单条已读/全部已读、feedback 类跳转后台反馈面板。
// 呈现层替换：TDesign Badge→Badge、Popup→页面级 CSS 浮层、Empty→EmptyState。
// ★ 2026-10-15 用户令：前台顶栏不再挂独立铃铛——通知以「消息(N)」菜单项收进账号下拉框
//   （见 AccountMenu.tsx）。为此把数据层抽成 useNotify()、面板抽成 NotifyPanel()：
//   默认导出 Bell（后台顶栏仍在用）＝触发器 + 面板；AccountMenu 只复用 NotifyPanel。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { Badge, BellIcon, EmptyState } from '@/ui/langcross/src'
import { notificationsUnread, notifications, notificationRead, notificationsReadAll } from '@/api'
import { fmtTime } from '@/lib/ui'
import { noticeLandingFor } from '@/lib/noticeRouting'
import { t } from '@/i18n'
import { useAdmin } from '@/stores/admin'

// ============ 本文件职责中文说明 ============
// 站内通知铃铛：未读轮询、下拉列表与已读操作。
// 挂载点：后台顶栏（admin/AdminDashboard）、前台账号下拉（AccountMenu 复用 NotifyPanel），
// 所以浮层不依赖任何外壳 CSS，样式与定位全在本文件自带。
// ========================================

// 站内通知条目数据结构（字段口径同后端 /api/notifications 列表项，不做前端改名）
export interface NoticeItem {
  id: number
  title: string
  body: string
  ref_type?: string // 关联对象类型：feedback / ticket / quota / kb_scrape，决定点击跳去哪
  ref_id?: number // 关联对象 ID，含义随 ref_type 变（可为 0/缺省）
  created_at: string
}

// 浮层一页的条数：与后端默认档（store.NotificationPageDefault＝20）同值，也是浮层高度放得下的上限。
const BELL_PAGE = 20

// useNotify 通知数据层：未读轮询、分页列表与已读操作（面板与触发器共用同一份状态）
export interface NotifyCtl {
  unread: number
  items: NoticeItem[]
  open: boolean
  hasMore: boolean
  loadingMore: boolean
  /** 触发器（Bell）的定位基准；AccountMenu 场景不用它，由调用方自己管外点 */
  wrapRef: React.RefObject<HTMLDivElement>
  setOpen: (v: boolean) => void
  toggle: () => void
  /** 展开时手动拉一页列表（AccountMenu 点开「消息」时调它） */
  loadList: () => Promise<void>
  markRead: (id: number) => Promise<void>
  markAll: () => Promise<void>
  loadMore: () => Promise<void>
}

/** 通知数据层：未读轮询 + 列表分页 + 已读操作。Bell 与 AccountMenu 共用。 */
export function useNotify(): NotifyCtl {
  const [unread, setUnread] = useState(0)
  const [items, setItems] = useState<NoticeItem[]>([])
  const [open, setOpen] = useState(false)
  const [hasMore, setHasMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  // ★ F-11 顺手项（批G 2026-09-25）：open 的同步镜像——refresh 经它现读展开态，
  //   于是 refresh 引用不再随 open 翻转而更换（旧版靠 open 进 deps 重建 refresh，
  //   才连带产生「toggle 里调的是旧闭包」的 stale-refresh）
  const openRef = useRef(false)
  const timerRef = useRef<number | null>(null)
  const wrapRef = useRef<HTMLDivElement>(null)

  // 拉取最近 20 条通知列表（deps 空 ⇒ 引用稳定，不连带重启 30s 轮询）
  const loadList = useCallback(async () => {
    try {
      const r = await notifications(BELL_PAGE, 0)
      if (r.success) {
        const box = r as unknown as { notifications?: NoticeItem[]; has_more?: boolean }
        setItems(box.notifications || [])
        setHasMore(box.has_more === true)
      }
    } catch { /* 忽略 */ } // 拉不到就保持上一次的列表，不打断操作
  }, [])

  // 追加下一页（offset 取"当前已到手条数"；中途新通知落库最坏是这一页边界挪一位，下次重拉修正）
  const loadMore = useCallback(async () => {
    setLoadingMore(true)
    try {
      const r = await notifications(BELL_PAGE, items.length)
      if (r.success) {
        const box = r as unknown as { notifications?: NoticeItem[]; has_more?: boolean }
        const more = box.notifications || []
        setItems((prev) => [...prev, ...more])
        setHasMore(more.length > 0 && box.has_more === true)
      }
    } catch { /* 忽略 */ } // 拉不到就维持"还能翻"这一乐观档：下一次点击重试，不收按钮
    finally { setLoadingMore(false) }
  }, [items.length])

  // 刷新未读数；下拉展开时（openRef 现读）同步拉取最近 20 条通知列表
  const refresh = useCallback(async () => {
    try {
      const u = await notificationsUnread()
      if (u.success) setUnread(Number((u as unknown as { unread?: number }).unread || 0))
      if (openRef.current) await loadList()
    } catch { /* 忽略 */ } // 旁路信息：拉不到就保持上一次的读数
  }, [loadList])

  // 挂载即刷新并启动 30s 未读轮询；卸载时清除定时器
  useEffect(() => {
    void refresh()
    timerRef.current = window.setInterval(refresh, 30000)
    return () => { if (timerRef.current) window.clearInterval(timerRef.current) }
  }, [refresh])

  // 点击浮层外部关闭（只在默认 Bell 触发器场景生效：wrapRef 包住钮与浮层）
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  // 标记单条已读并刷新列表
  const markRead = useCallback(async (id: number) => {
    await notificationRead(id)
    void refresh()
  }, [refresh])
  // 标记全部已读并刷新列表（不本地 setUnread(0)：以服务端回读为准）
  const markAll = useCallback(async () => {
    await notificationsReadAll()
    void refresh()
  }, [refresh])

  // 切换浮层；展开时刷新列表（同步置 openRef，展开瞬间即在途列表）
  function toggle() {
    const next = !open
    openRef.current = next
    setOpen(next)
    if (next) void loadList()
  }

  return { unread, items, open, hasMore, loadingMore, wrapRef, setOpen, toggle, loadList, markRead, markAll, loadMore }
}

/** 通知浮层（.bell-panel）：头部未读全清 + 列表 + 分页「更多」。调用方决定何时挂载。 */
export function NotifyPanel({ ctl }: { ctl: NotifyCtl }) {
  // 落点表取自 lib/noticeRouting.ts（与后端 store.NotifyRefTypes 跨语言等值）
  const { gotoPanel, openFeedback, myLevel } = useAdmin()
  const { items, hasMore, loadingMore, markRead, markAll, loadMore } = ctl

  function onItemClick(n: NoticeItem) {
    void markRead(n.id) // 不 await：跳转要立刻发生
    const landing = noticeLandingFor(n.ref_type, myLevel)
    if (landing.kind === 'feedback') openFeedback(n.ref_id as number)
    else gotoPanel(landing.panel)
    ctl.setOpen(false) // 收起浮层：已在后台时点通知不触发路由跳转，得自己收
  }

  return (
    <>
      <style>{BELL_CSS}</style>
      {/* ⚠ 这里只标了 role="menu"，条目仍是可点 div、没有 menuitem/方向键导航 */}
      <div className="bell-panel" role="menu">
        <div className="bell-head">
          <b style={{ fontSize: 15 }}>{t('bell.title')}</b>
          <button type="button" className="bell-readall" onClick={markAll}>{t('bell.readAll')}</button>
        </div>
        {items.length === 0 && <EmptyState title={t('bell.empty')} />}
        {items.map((n) => (
          <div key={n.id} className="bell-item" onClick={() => onItemClick(n)}>
            <div style={{ fontWeight: 600, fontSize: 15 }}>{n.title}</div>
            <div style={{ fontSize: 14, color: 'var(--lc-text-3)', marginTop: 2, whiteSpace: 'pre-wrap' }}>{n.body}</div>
            <div style={{ fontSize: 13, color: 'var(--lc-text-4)', marginTop: 2 }}>{fmtTime(n.created_at)}</div>
          </div>
        ))}
        {hasMore && (
          <button type="button" className="bell-more" disabled={loadingMore} onClick={() => void loadMore()}>
            {t('app.more')}
          </button>
        )}
      </div>
    </>
  )
}

// 默认导出组件：通知铃铛触发器 + 浮层（后台顶栏用；前台改由账号下拉承载）
export default function Bell() {
  const ctl = useNotify()
  return (
    <div className="bell-wrap" ref={ctl.wrapRef}>
      <style>{BELL_CSS}</style>
      <button type="button" className="bell-trigger" onClick={ctl.toggle} aria-label={t('bell.title')} aria-expanded={ctl.open}>
        <BellIcon size={18} />
        {ctl.unread > 0 && <Badge className="bell-count">{ctl.unread}</Badge>}
      </button>
      {ctl.open && <NotifyPanel ctl={ctl} />}
    </div>
  )
}

// —— 浮层（bell- 前缀，避免与组件库类名重名）——
const BELL_CSS = `
.bell-wrap{position:relative;display:inline-flex}
.bell-trigger{position:relative;display:inline-flex;align-items:center;justify-content:center;padding:6px;
  background:transparent;border:0;color:var(--lc-text-2);cursor:pointer;font-family:var(--lc-font);border-radius:var(--lc-r-bar)}
.bell-trigger:hover{color:var(--lc-text)}
.bell-trigger:focus-visible{outline: 2px solid var(--lc-border-input);outline-offset:2px}
.bell-count{position:absolute;top:-1px;inset-inline-end:-1px}
.bell-panel{position:absolute;top:calc(100% + 8px);inset-inline-end:0;z-index:60;width:340px;max-height:420px;overflow-y:auto;
  padding:8px;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-modal);
  box-shadow:var(--lc-panel-highlight)}
.bell-head{display:flex;justify-content:space-between;align-items:center;margin-bottom:6px}
.bell-readall{background:none;border:0;color:var(--lc-text-3);font-size:14px;cursor:pointer;font-family:var(--lc-font);padding:0}
.bell-readall:hover{color:var(--lc-text);text-decoration:underline}
.bell-item{padding:8px 6px;border-bottom:1px solid var(--lc-border-faint);cursor:pointer}
.bell-item:last-child{border-bottom:0}
.bell-item:hover{background:var(--lc-raised)}
.bell-more{display:block;width:100%;margin-top:6px;padding:6px 0;background:none;border:0;
  color:var(--lc-text-3);font-size:14px;cursor:pointer;font-family:var(--lc-font);border-radius:var(--lc-r-bar)}
.bell-more:hover{color:var(--lc-text);text-decoration:underline}
.bell-more:disabled{opacity:.5;cursor:default;text-decoration:none}
`
