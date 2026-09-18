// ============================================================================
// components/usePlans.ts — 营销页共用取数：公开价目 /api/plans + UTM 归因捕获
// Landing（未登录 `/`）与 PricingPage（`/pricing`）共用。
// 价格、积分额度、有效期一律渲染后端返回值，前端不写死任何数字。
// ============================================================================
import { useEffect, useState } from 'react'
import { request } from '@/api/core'

/** PlanLite 定价页套餐轻量出参（积分 / 价格 / 有效期） */
export interface PlanLite {
  code: string
  name: string
  /** free(免费体验) / paid(付费包) / increment(增量包) */
  ptype: string
  points: number          // 积分（S1 对外唯一额度口径，前端不感知 token 裸值）
  price_money: number     // 售价，单位＝元（后端 packages.price_money 同为元，渲染时不做分→元换算）
  duration_days: number   // 有效期天数；后端发放时 0＝不限期（不写 PackageExpires），措辞由调用方定
}

/** TrialLite 新用户注册赠送额度（/api/plans 的 free_trial_points / free_trial_days） */
export interface TrialLite { points: number; days: number }

// 注册归因采集的 UTM 参数名清单（落 tenants 归因字段，注册提交时随 ref 上报）
const UTMS = ['utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content']

/** 捕获营销页 UTM 到 localStorage（S4 归因前置；无参数时不写，幂等） */
export function captureUtm() {
  const q = new URLSearchParams(window.location.search)
  const saved: Record<string, string> = {}
  UTMS.forEach((k) => { const v = q.get(k); if (v) saved[k] = v })
  if (!Object.keys(saved).length) return
  try { localStorage.setItem('utm', JSON.stringify({ ...saved, captured_at: Date.now() })) } catch { /* ignore */ }
}

/** usePlans 拉取上架中的全部商业包（免费体验 / 增量包 / 付费包）与体验额度 */
export function usePlans(): { plans: PlanLite[]; trial: TrialLite } {
  // plans 初值为空数组：接口返回前就不渲染卡片——宁可空一段，也不在前端写死一份「看起来像」的价目
  const [plans, setPlans] = useState<PlanLite[]>([])
  // trial 兜底值与后端 EnsureBillingDefaults 的种子一致（300000 token ÷ 300 token/积分 = 1000 积分、14 天），
  // 目的是接口挂了也仍能说清「送多少、多久」，而不是显示 0 或空白
  const [trial, setTrial] = useState<TrialLite>({ points: 1000, days: 14 })

  useEffect(() => {
    // 两个营销页（/ 与 /pricing）都会经过这里，UTM 只在落地这一刻存在于地址栏，
    // 所以顺手在取数前捕获，注册时再从 localStorage 读出上报
    captureUtm()
    // 出参字段少且仅营销页消费，直接用 any 取值，不为公开价目接口另建 DTO
    void request('/api/plans').then((r: any) => {
      if (r?.success) {
        setPlans((r.plans || []) as PlanLite[])
        setTrial({ points: r.free_trial_points ?? 1000, days: r.free_trial_days ?? 14 })
      }
    }).catch(() => { /* 价目卡留空即可：营销页是入口不是功能页，取数失败不值得弹错打扰 */ })
  }, [])

  return { plans, trial }
}
