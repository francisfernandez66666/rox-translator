// ============================================================================
// api/branding.ts — 品牌与页脚链接接口
// 职责：按域名解析品牌信息、品牌定制授权与保存、平台页脚链接读写
// ============================================================================

import { request } from './core'

/** 品牌页脚链接项：含中英文标签与跳转地址 */
export interface BrandLink {
  label: string
  label_en: string
  url: string
}

/** 获取品牌信息（按域名自动解析；super 可指定 tenant_id） */
export async function tenantBranding(tenantId?: number) {
  return request<{
    success: boolean; tenant_id: number; name: string; code: string; industry: string; industry_name: string
    brand_name: string; brand_logo: string; domain: string; brand_home_bg: string
    brand_paid: boolean; brand_granted: boolean; dedicated_register: boolean
  }>(
    `/api/tenant/branding${tenantId ? `?tenant_id=${tenantId}` : ''}`,
  )
}

/** 超管为指定租户开通/撤销「品牌定制」权限（免套餐） */
export async function brandGrant(tenantId: number, enabled: boolean) {
  return request<{ success: boolean; message?: string }>('/api/admin/tenant/brand-grant', {
    method: 'POST',
    body: JSON.stringify({ tenant_id: tenantId, enabled }),
  })
}

/** 保存品牌（超管可指定 id；租户管理员不传 id，仅改本租户） */
export async function tenantBrandingSave(p: {
  id?: number
  brand_name: string
  brand_logo: string
  domain: string
  brand_home_bg?: string
}) {
  return request<{ success: boolean; message?: string }>('/api/tenant/branding', {
    method: 'POST',
    body: JSON.stringify(p),
  })
}

/** 获取平台级页脚链接（公开接口） */
export async function footerLinksGet() {
  return request<{ success: boolean; links: BrandLink[] }>('/api/footer-links')
}

/** 保存平台级页脚链接（仅超管；入参为 JSON 字符串） */
export async function footerLinksSet(linksJson: string) {
  return request<{ success: boolean; message?: string }>('/api/admin/footer-links', {
    method: 'POST',
    body: JSON.stringify({ links: linksJson }),
  })
}
