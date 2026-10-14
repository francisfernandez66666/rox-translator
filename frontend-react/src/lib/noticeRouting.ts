// ============================================================================
// lib/noticeRouting.ts — 站内信「点一条通知去哪个面板」的唯一落点表（★ #46，2026-10-14）
//
// 为什么需要这一个文件：铃铛原来只有一条分支——feedback 打开反馈面板，**其余全部跳工单面板**。
// 也就是说采集完成（kb_scrape）、静态码待确认（pay_manual）、预算墙命中（quota）、
// 订阅到期/品牌宽限（tenant）这四类通知点了只会把人丢进工单列表，而正文里明明写着
// 「可前往数据源面板查看」。这不是形状难看，是**承诺与能力脱节**：
// 后端在正文里点名了面板，前端却不知道那个面板的键名。
//
// 三条口径：
//  ① ref_type 的**名单只有一份**，在 backend-go/internal/store/notifications_ref.go；
//     本表必须与它逐字等值（缺一项＝那一类通知点不开，多一项＝后端写了个前端认不到的类型）。
//     这条等值由 noticeRouting.lock.test.ts 读 Go 源码机械保证——
//     新增一类却忘了在这里补落点，闸门直接红，而不是等某个超管点进去发现"没反应"。
//  ② 落点必须带**等级门槛**：后台侧边栏按 minLevel 过滤菜单，而 renderPanel 对任何 key 都渲染，
//     所以把 L2 部门管理员直接送进 L3 面板＝AdminDashboard 那条注释里点名的「看得见、点进去全线 403 的死路」。
//     等级不够就回落到工单面板（那里对 L2+ 都有意义），**不是**硬跳。
//  ③ 未知/缺省 ref_type 一律回落工单面板——点了没反应比跳错面板更坏。
//
// ⚠️ panel 值必须是 stores/admin.tsx 的 PanelKey 字面量；写错不报错、只会永远回落到工单。
// ⚠️ feedback 是唯一"带 ref_id 打开详情"的落点，其余只到面板列表。
// ============================================================================
import type { PanelKey } from '@/stores/admin'

/** 站内信落点：panel＝切到后台某面板（带最低可见等级）；feedback＝打开反馈处理面板并定位那一条。 */
export type NoticeLanding =
  | { kind: 'panel'; panel: PanelKey; minLevel: number }
  | { kind: 'feedback'; minLevel: number }

/**
 * ref_type → 落点，唯一事实源。六条与后端 store.NotifyRefTypes 一一对应（跨语言等值锁）：
 *  · ticket     工单生命周期（完成/失败/产物过期）→ 工单面板（L2+）
 *  · feedback   问题反馈 / 销售线索 / 反馈补充 → 反馈面板（超管面，带 ref_id 定位）
 *  · quota      部门/组织预算墙命中 → 组织面板（预算在那里配、在那里看；收件人可能是 L2 部门管理员 ⇒ 有回落）
 *  · tenant     订阅到期/续费/品牌宽限/体验额度 → 计费 Hub（租户管理员面）
 *  · kb_scrape  行业包采集轮次完成 → 数据源面板（正文点名的就是这一面；超管面）
 *  · pay_manual 静态码/USDT 收款待人工确认 → 套餐面板（人工核销入口在那里；超管面）
 *
 * minLevel 取值与 AdminDashboard 的菜单档位同源（org/billing=3，tickets=2）；
 * 超管专面（dataSources/plans/反馈）取 4——后台侧边栏对这两面本就不向 L3 展示。
 */
export const NOTICE_LANDINGS: Record<string, NoticeLanding> = {
  ticket: { kind: 'panel', panel: 'tickets', minLevel: 2 },
  feedback: { kind: 'feedback', minLevel: 4 },
  quota: { kind: 'panel', panel: 'org', minLevel: 3 },
  tenant: { kind: 'panel', panel: 'billing', minLevel: 3 },
  kb_scrape: { kind: 'panel', panel: 'dataSources', minLevel: 4 },
  pay_manual: { kind: 'panel', panel: 'plans', minLevel: 4 },
}

/** 兜底落点：等级不够、类型不认识、老数据缺 ref_type 时都落到这里（工单面板对 L2+ 都有意义）。 */
export const NOTICE_FALLBACK_PANEL: PanelKey = 'tickets'

/**
 * 按 ref_type 与当前用户等级取落点。
 * 参数 refType：后端 notifications.ref_type（可缺省）；level：admin store 的 myLevel（缺省按 0＝最保守）。
 * 返回的 kind 只描述"该做什么"，等级不够时一律被折成 panel=tickets（调用方无需再判等级）。
 */
export function noticeLandingFor(refType: string | undefined, level: number = 0): NoticeLanding {
  const hit = refType ? NOTICE_LANDINGS[refType] : undefined
  const need = hit ? hit.minLevel : Number.POSITIVE_INFINITY
  if (hit && level >= need) return hit
  return { kind: 'panel', panel: NOTICE_FALLBACK_PANEL, minLevel: 2 }
}

/** 本表覆盖的 ref_type 名单（给跨语言锁与排障用；判据仍以后端那一份为准）。 */
export function noticeLandingKeys(): string[] {
  return Object.keys(NOTICE_LANDINGS)
}
