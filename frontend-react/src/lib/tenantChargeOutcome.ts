// ============================================================================
// lib/tenantChargeOutcome.ts — ★ 2026-11-04 体验批 §4.1/§4.2「三张脸」的**唯一判据**。
//
// 为什么收成一个纯函数：超管给租户注入资金是两步（建单 → 置付），旧面板把这两步串成
// 「建单成功就 toastResp，置付**不判结果**，然后无条件绿条」。缺陷形态是"接口全绿而钱没进去"，
// 而它恰好落在**分支判断**上——分支判断最该被机械测到，而不是被"渲染整个面板再点按钮"测到
// （后者要 mock 六个 store/api，mock 装一半就会出现"栈顶落在产品代码里"的历史误判）。
//
// ⇒ 所以：判定在这儿（可穷举单测），出栈（toast 语气/文案键）在 TenantsP，
//   两者之间只隔一个函数调用；接线由 tenantChargeOutcome.wiring.lock.test.ts 机械钉住。
//
// 六档互斥，每档只答一件事：
//   create-failed   建单就没成（含业务失败信封）              ⇒ 红条，不出账、不走第二步
//   no-order-id     建单说成功却没回订单号（半套回执）        ⇒ 红条，让人去订单面板核对
//   pay-failed      置付失败/网络断                          ⇒ 红条 + 点名单号，**绝不绿**
//   unverified      置付成功但后端没回余额（读数缺席）        ⇒ 如实"没测到"，不判成功也不判失败
//   verify-failed   余额里连这一笔都装不下                    ⇒ 红条「注入未生效」
//   credited        三张脸里唯一的一张绿                      ⇒ 绿条
// ============================================================================

export type ChargeOutcomeKind =
  | 'create-failed'
  | 'no-order-id'
  | 'pay-failed'
  | 'unverified'
  | 'verify-failed'
  | 'credited'

/** 判据入参：只吃"已经发生的事实"，不做任何请求 */
export interface ChargeFacts {
  /** 建单那一步：runGuarded 返回体是否 success（null=请求本身没走通） */
  created: boolean
  /** 建单响应里的订单号/ID 是否拿到的（两者任一在位即可） */
  hasOrderId: boolean
  /** 置付那一步是否走到（请求发出且拿到响应） */
  payReached: boolean
  /** 置付响应 success 是否为真 */
  paySuccess: boolean
  /** 置付响应的 balance_points（到账后现读余额）；后端读失败时整条缺席 → null */
  balancePoints: number | null
  /** 置付响应的 order_points（本单积分）；同上 */
  orderPoints: number | null
}

/** 判据出口：kind + 该给客户的文案键 + 插值 */
export interface ChargeOutcome {
  kind: ChargeOutcomeKind
  /** i18n 键（TenantsP 负责取词与语气档，这里不碰词典） */
  msgKey: string
  /** 是否走"成功"那一只 toast（唯一 true 的一档是 credited） */
  success: boolean
}

// 文案键表也收在这儿——面板里出现中文串就是"两处事实源"（AGENTS 跨端等值那条）
const MSG: Record<ChargeOutcomeKind, string> = {
  'create-failed': 'tenants.chargeCreateFailed',
  'no-order-id': 'tenants.chargeNoOrderId',
  'pay-failed': 'tenants.chargeNotCredited',
  'unverified': 'tenants.chargeUnverified',
  'verify-failed': 'tenants.chargeVerifyFail',
  'credited': 'tenants.charged',
}

/**
 * decideChargeOutcome 按六档互斥顺序判定。
 * 顺序即优先级：前一步没成，后面的读数一律不看（"建单失败却去查余额"会报出第三种脸）。
 */
export function decideChargeOutcome(f: ChargeFactslike): ChargeOutcome {
  return outcome(f)
}

// outcome 六档判定的实现体（对外只暴露 decideChargeOutcome 这一个口，避免有人绕过包装层自己拼）。
function outcome(f: ChargeFacts): ChargeOutcome {
  if (!f.created) return mk('create-failed')
  if (!f.hasOrderId) return mk('no-order-id')
  if (!f.payReached || !f.paySuccess) return mk('pay-failed')
  if (f.balancePoints === null) return mk('unverified')
  // 回验是**单向**的：余额必须装得下这一笔。
  // 为什么不判"前值+本单=后值"：那需要一个按租户号读余额的接口（后台没有），
  // 为它新开一条跨租户余额读点属于安全改动，不是测试改动。
  // order_points 也缺席时只能判 unverified 之外的最弱档——这里刻意**不**判成功：
  // 少了本单积分数，连单向比较都做不了，"没测到"比"看起来对"诚实。
  if (f.orderPoints === null) return mk('unverified')
  if (f.balancePoints < f.orderPoints) return mk('verify-failed')
  return mk('credited')
}

// mk 把"档位"翻成"该说哪句文案 + 该不该发成功徽标"——语气与文案键的唯一映射点。
function mk(kind: ChargeOutcomeKind): ChargeOutcome {
  return { kind, msgKey: MSG[kind], success: kind === 'credited' }
}

// 兼容写法（避免调用方传对象字面量时的窄推断）：只允许 ChargeFacts 的形态
export type ChargeFactslike = ChargeFacts
