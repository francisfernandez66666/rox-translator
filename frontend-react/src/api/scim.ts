// ============================================================================
// api/scim.ts — ★ H10 SCIM 2.0 自助配置（租户管理员）
// ============================================================================
import { request, authHeaders, type AdminResp } from './core'

export interface ScimConfig {
  tenant_id: number
  token: string
  enabled: boolean
  root_org_id: number
  created_at?: string
}

export interface ScimGetResp extends AdminResp {
  config?: ScimConfig
  endpoint?: string
}

// 读取租户 SCIM 配置（含密钥掩码）
export async function scimConfigGet(): Promise<ScimGetResp> {
  return request('/api/tenant/scim', { headers: authHeaders() })
}

// 保存 SCIM 配置（开关/轮换密钥/默认根部门）
export async function scimConfigSave(data: { enabled?: boolean; rotate?: boolean; root_org_id?: number }): Promise<ScimGetResp> {
  return request('/api/tenant/scim', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}
