// ============================================================================
// api/coupons.ts — 优惠券/促销码域接口（★ #41 商业洞三，2026-09-21）
// 职责：租户侧券试算（收银台输入券码后预览折让）+ 超管券模板 CRUD 与核销流水
// 口径：试算只回金额，不建单不落库；真正的核销发生在下单请求里（payCreate/packageSubscribe
//       带 coupon 字段），金额一律服务端按同一算法重算，前端数字只用于展示。
// ============================================================================
import { request, bizResp, authHeaders, type AdminResp } from './core'

// ★ F-64①（批 I-7，2026-09-26）：券域三个失败口径接口（试算 / 保存 / 删除）后端已从
//   「HTTP 200 承载失败」改为诚实状态码（400/404/409/500/503），本层用 core 的 bizResp
//   把结构化失败体还原成历史 `{success:false, message, ...}` 形态，调用点（PlansP 券区、
//   CouponsP 表单）零改动即保持既有分支与提示。见 core.ts bizResp 注释与 billing.ts 同段说明。

/** 券试算入参：code=券码，points=充值积分数 或 packageCode=订阅包编码（二选一，后端据此定券适用类型） */
export async function couponPreview(data: { code: string; points?: number; package_code?: string }): Promise<AdminResp> {
  return bizResp(() => request('/api/coupon/preview', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 超管：券列表（含 remaining / total_discount 等实时核销汇总） */
export async function adminCoupons(): Promise<AdminResp> {
  return request('/api/admin/coupons', { headers: authHeaders() })
}

/** 超管：新建（id 省略或 0）/ 更新（id>0）券模板 */
export async function adminCouponSave(data: {
  id?: number; code: string; name?: string; kind?: string;
  discount_type: string; discount_value: number; max_discount?: number; min_amount?: number;
  max_uses?: number; per_tenant_limit?: number; valid_from?: string; valid_until?: string;
  enabled?: number; note?: string;
}): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/coupons/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 超管：删除券模板（已产生的核销流水保留，历史对账不受影响） */
export async function adminCouponDelete(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/coupons/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/** 超管：核销流水（couponId 省略=全平台，limit 后端默认 200、上限 500） */
export async function adminCouponRedemptions(couponId?: number, limit = 200): Promise<AdminResp> {
  const q = `?limit=${limit}${couponId ? `&coupon_id=${couponId}` : ''}`
  return request(`/api/admin/coupons/redemptions${q}`, { headers: authHeaders() })
}
