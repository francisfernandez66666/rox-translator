// ============================================================================
// api/salesInbox.ts — 三期 W2 事件收件箱**只读**接口层
//
// 后端契约（backend-go/internal/api/sales_inbox_api.go 实读）：
//   GET /api/admin/sales/inbox/event?event_id=  单条事件处理状态
//
// 纪律：信封调用一律 bizResp 包裹。本文件**零写侧**——收件箱的幂等落账与
// 完成标记只发生在主服务 mq 消费者（sales_mq_wiring.go）内部，
// 收件箱状态不许外部改写，本面只服务「这条事件消费了没」的排障。
// ============================================================================
import { bizResp, request, authHeaders, type AdminResp } from './core'

export interface SalesInboxEventResp extends AdminResp {
  found?: boolean
  status?: string
  processed_at?: string
}

/** 单条事件状态（查无 found=false，不是 404——「还没到」是正常态） */
export async function salesInboxEvent(eventID: string): Promise<SalesInboxEventResp> {
  return bizResp(() => request(`/api/admin/sales/inbox/event?event_id=${encodeURIComponent(eventID)}`, { headers: authHeaders() }))
}
