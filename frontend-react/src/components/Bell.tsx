// ============================================================================
// components/Bell.tsx — 站内通知铃铛（TDesign Badge + Popup）
// 行为对齐：30s 未读轮询、下拉列表、单条已读/全部已读、feedback 类跳转后台反馈面板。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { Badge, Popup, Button, Empty } from 'tdesign-react'
import { notificationsUnread, notifications, notificationRead, notificationsReadAll } from '@/api'
import { fmtTime } from '@/lib/ui'
import { t } from '@/i18n'
import { useAdmin } from '@/stores/admin'

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

  // 刷新未读数；下拉展开时同步拉取最近 20 条通知列表
  const refresh = useCallback(async () => {
    try {
      const u = await notificationsUnread()
      if (u.success) setUnread(Number((u as unknown as { unread?: number }).unread || 0))
      if (open) {
        const r = await notifications()
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

  // 标记单条已读并刷新列表
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
    if (isSuper && n.ref_type === 'feedback') openFeedback(n.ref_id as number)
    else gotoPanel('tickets')
    setOpen(false)
  }

  return (
    <Popup
      trigger="click"
      placement="bottom-right"
      showArrow
      visible={open}
      onVisibleChange={(v) => { setOpen(v as boolean); if (v) void refresh() }}
      content={
        <div style={{ width: 340, maxHeight: 420, overflowY: 'auto', padding: 8 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
            <b style={{ fontSize: 13 }}>{t('bell.title')}</b>
            <Button size="small" variant="text" onClick={markAll}>{t('bell.readAll')}</Button>
          </div>
          {items.length === 0 && <Empty description={t('bell.empty')} />}
          {items.map((n) => (
            <div key={n.id}
                 onClick={() => onItemClick(n)}
                 style={{ padding: '8px 6px', borderBottom: '1px solid #f0f2f7', cursor: 'pointer' }}>
              <div style={{ fontWeight: 600, fontSize: 13 }}>{n.title}</div>
              <div style={{ fontSize: 12, color: '#667', marginTop: 2, whiteSpace: 'pre-wrap' }}>{n.body}</div>
              <div style={{ fontSize: 11, color: '#99a', marginTop: 2 }}>{fmtTime(n.created_at)}</div>
            </div>
          ))}
        </div>
      }
    >
      <span style={{ cursor: 'pointer', display: 'inline-flex', padding: 6 }}>
        <Badge count={unread} dot={false} size="medium">🔔</Badge>
      </span>
    </Popup>
  )
}
