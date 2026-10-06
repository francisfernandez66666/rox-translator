// ============================================================================
// usdtPromise.ts — ★ ⑮（2026-10-05 第 3 波）USDT 收银台「自动入账」承诺的唯一判据
//
// 缺陷本体：链上到账监听自开闸起每 30s 失败一次、8 天零成功（TronGrid 链头端点被裸打
// /v1/blocks 恒回 404），而失败只落一行日志——不进 alerts、不进 /api/health；
// 同一时刻收银台还在对客写「达到确认数后自动入账」⇒ **对外承诺与内部能力脱节**，
// 客户真转完账只能干等（钱到账却永远不会结算）。
//
// 后端现在把「这台此刻能不能自动入账」作为一个独立字段出栈：
//   auto_settle_live = 开关开着 **且** 到账监听最近一轮全链健康（四档见 pay_usdt_watch.go）。
// 本文件只认这一个布尔值，**禁止**在前端再造第二套优先级（比如再去看开关字段
// auto_settle_on／channel 是否 usdt）——「开关开着」和「监听活着」是两件事，
// 当年就是把前者当成了后者。
//
// 取不到该字段（旧后端／缓存里的历史单）一律按**不承诺自动**处理：
// 判错的代价只是文案保守一档，反过来就是把 ⑮ 的假承诺原样重写一遍。
// ============================================================================

/** 收银台 USDT 说明文案的两个档位键（zh/en 词典同源，另十语种全量覆盖） */
export type UsdtCheckoutKey = 'billing.usdtCheckoutHint' | 'billing.usdtCheckoutManual'

/**
 * usdtCheckoutKey 按后端 live 读数选收银台那句承诺。
 * 只有字面 `true` 才允许说「达到确认数后自动入账」；false／undefined／null／字符串
 * 'true' 之类一律降级为「提交链上哈希、运营人工核销」。
 */
export function usdtCheckoutKey(autoSettleLive: unknown): UsdtCheckoutKey {
  return autoSettleLive === true ? 'billing.usdtCheckoutHint' : 'billing.usdtCheckoutManual'
}

/** usdtPromiseIsLive 界面着色用的同一个判据（别再在组件里写一次 === true） */
export function usdtPromiseIsLive(autoSettleLive: unknown): boolean {
  return autoSettleLive === true
}
