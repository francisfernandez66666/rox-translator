// ============================================================================
// components/usePricingMeta.ts — 公开算价元数据取数（GET /api/pricing/meta）
// 供「比价与算价」页（/compare）消费：两档积分系数 + 每积分人民币单价。
// ★ 口径红线（AGENTS §一·5「公开接口零 token 裸值」+ 价格一律渲染后端返回值）：
//   本 hook **不写任何兜底数值**。接口没回来就是 ready=false，页面渲染「系数暂时取不到」，
//   绝不在前端留一份"看起来像"的系数——那正是 F-12 那次「定价页/收银台/管理台三口径打架」
//   的复发形态：超管调档后，前端写死的那份会一直报旧价，而且永远不会自己红。
//   （对照 usePlans 的 trial 兜底：那是「注册送多少」的文案兜底，不参与任何金额计算；
//     这里的数直接乘进客户看到的报价，所以判据必须更严。）
// ============================================================================
import { useEffect, useState } from 'react'
import { request } from '@/api/core'

/** PricingModeMeta 单档算价系数（全部积分口径，接口侧就不下发 token 裸值） */
export interface PricingModeMeta {
  /** 每「1000 源字符 × 1 个目标语种」的线性积分档 */
  per1k: number
  /** 每次建单的一次性固定积分档（可能是小数，如 pro=7.5） */
  fixed: number
}

/** PricingMetaSnapshot 一次取数的结果；ready=false 时数值一律不可用 */
export interface PricingMetaSnapshot {
  ready: boolean
  /** 模式码 → 系数（fast / pro） */
  modes: Record<string, PricingModeMeta>
  /** 每积分对应人民币（元）：与「3,000 积分 = ¥299」套餐面值同联动 */
  pointsPrice: number
}

// EMPTY 取数未就绪时的初值：ready=false 且**没有任何系数**（pointsPrice 的 0 只是占位，
// 页面在 ready=false 分支下不会拿它乘出任何金额——见文件头的「宁缺勿错」口径）。
const EMPTY: PricingMetaSnapshot = { ready: false, modes: {}, pointsPrice: 0 }

/** usePricingMeta 拉取公开算价系数；失败保持 ready=false 由页面显示不可用态 */
export function usePricingMeta(): PricingMetaSnapshot {
  const [snap, setSnap] = useState<PricingMetaSnapshot>(EMPTY)

  useEffect(() => {
    let alive = true
    void request('/api/pricing/meta').then((r: any) => {
      if (!alive) return
      if (!r?.success) return
      const modes: Record<string, PricingModeMeta> = {}
      for (const m of (r.modes || []) as any[]) {
        const code = String(m?.code || '')
        const per1k = Number(m?.points_per_1k_chars)
        const fixed = Number(m?.points_fixed)
        // 逐档验数：任一系数不是有限非负数就整包判为不可用（宁缺勿错——
        // 半个系数也能算出一个"看起来很确定"的价，那种错没人能从页面上看出来）
        if (!code || !Number.isFinite(per1k) || per1k < 0 || !Number.isFinite(fixed) || fixed < 0) return
        modes[code] = { per1k, fixed }
      }
      const price = Number(r.points_price_money)
      if (!modes.fast || !modes.pro || !Number.isFinite(price) || price <= 0) return
      setSnap({ ready: true, modes, pointsPrice: price })
    }).catch(() => { /* 留 ready=false：页面显示不可用态，不猜价 */ })
    return () => { alive = false }
  }, [])

  return snap
}
