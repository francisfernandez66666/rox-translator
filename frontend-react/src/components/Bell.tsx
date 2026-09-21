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
// ========================================

// 站内通知条目数据结构
interface NoticeItem {
  id: number
  title: string
  body: string
  ref_type?: string
  ref_id?: number
  created_at: string
}

// 默认导出组件：通知铃铛，负责未读轮询、下拉列表与已读操作（等价 Vue Bell.vue）
export default function Bell() {
  const [unread, setUnread] = useState(0)
  const [items, setItems] = useState<NoticeItem[]>([])
  const [open, setOpen] = useState(false)
  const { gotoPanel, isSuper, openFeedback } = useAdmin()
  const timerRef = useRef<number | null>(null)
  const wrapRef = useRef<HTMLDivElement>(null)

  // 刷新未读数；下拉展开时同步拉取最近 20 条通知列表
  // 列表只在 open 时才请求：收起状态下 30s 轮询只多打一个轻量的 unread 接口，省掉一次列表查询。
  // ⚠ 代价是 open 进了依赖数组 → 每次展开/收起都会重建 refresh → 下面那个 30s 定时器随之重启
  const refresh = useCallback(async () => {
    try {
      const u = await notificationsUnread()
      if (u.success) setUnread(Number((u as unknown as { unread?: number }).unread || 0))
      if (open) {
        const r = await notifications()
        // 后端一次最多回 100 条且无分页参数，这里再截到 20：浮层高 420px 也放不下更多，纯粹是自我上限
        if (r.success) setItems(((r as unknown as { notifications?: NoticeItem[] }).notifications || []).slice(0, 20))
      }
    } catch { /* 忽略 */ }
  }, [open])

  // 挂载即刷新并启动 30s 未读轮询；卸载时清除定时器
  useEffect(() => {
    void refresh()
    timerRef.current = window.setInterval(refresh, 30000)
    return () => { if (timerRef.current) window.clearInterval(timerRef.current) }
  }, [refresh])

  // 点击浮层外部关闭（等价 TDesign Popup 的 trigger="click" 点击外部收起）
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
  // 标记全部已读并刷新列表
  async function markAll() {
    await notificationsReadAll()
    void refresh()
  }

  // 点击通知：先标记已读，再按类型跳转（feedback→超管反馈面板，其余→工单页）
  function onItemClick(n: NoticeItem) {
    void markRead(n.id)
    // feedback 类通知 → 超管打开反馈处理面板；其余跳转工单
    // ref_id 的含义随 ref_type 变（feedback=反馈单 ID、ticket=工单 ID、quota=组织/租户 ID、kb_scrape=0），
    // 只有 feedback 分支会真的读它，所以强转写在这一行里；非超管打不开反馈面板，退化成工单列表
    if (isSuper && n.ref_type === 'feedback') openFeedback(n.ref_id as number)
    else gotoPanel('tickets')
    setOpen(false)
  }

  // 切换浮层；展开时刷新列表
  function toggle() {
    const next = !open
    setOpen(next)
    if (next) void refresh()
  }

  return (
    <div className="bell-wrap" ref={wrapRef}>
      <style>{BELL_CSS}</style>
      <button type="button" className="bell-trigger" onClick={toggle} aria-label={t('bell.title')} aria-expanded={open}>
        <BellIcon size={18} />
        {unread > 0 && <Badge className="bell-count">{unread}</Badge>}
      </button>
      {/* 浮层自绘：绝对定位挂在 .bell-wrap(position:relative) 之下，替代原 Popup 的 popper 定位。
          ⚠ 这里只标了 role="menu"，条目仍是可点 div、没有 menuitem/方向键导航，键盘用户只能 Tab 到关闭 */}
      {open && (
        <div className="bell-panel" role="menu">
          <div className="bell-head">
            <b style={{ fontSize: 14 }}>{t('bell.title')}</b>
            <button type="button" className="bell-readall" onClick={markAll}>{t('bell.readAll')}</button>
          </div>
          {items.length === 0 && <EmptyState title={t('bell.empty')} />}
          {items.map((n) => (
            <div key={n.id} className="bell-item" onClick={() => onItemClick(n)}>
              <div style={{ fontWeight: 600, fontSize: 14 }}>{n.title}</div>
              <div style={{ fontSize: 13, color: 'var(--lc-text-3)', marginTop: 2, whiteSpace: 'pre-wrap' }}>{n.body}</div>
              <div style={{ fontSize: 12, color: 'var(--lc-text-4)', marginTop: 2 }}>{fmtTime(n.created_at)}</div>
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
.bell-trigger:focus-visible{outline:1.2px solid var(--lc-border-input);outline-offset:2px}
.bell-count{position:absolute;top:-1px;right:-1px}
.bell-panel{position:absolute;top:calc(100% + 8px);right:0;z-index:60;width:340px;max-height:420px;overflow-y:auto;
  padding:8px;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-modal);
  box-shadow:var(--lc-panel-highlight)}
.bell-head{display:flex;justify-content:space-between;align-items:center;margin-bottom:6px}
.bell-readall{background:none;border:0;color:var(--lc-text-3);font-size:13px;cursor:pointer;font-family:var(--lc-font);padding:0}
.bell-readall:hover{color:var(--lc-text);text-decoration:underline}
.bell-item{padding:8px 6px;border-bottom:1px solid var(--lc-border-faint);cursor:pointer}
.bell-item:last-child{border-bottom:0}
.bell-item:hover{background:var(--lc-raised)}
`
