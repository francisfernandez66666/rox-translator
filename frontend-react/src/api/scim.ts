// ============================================================================
// api/scim.ts — ★ H10 SCIM 2.0 自助配置（租户管理员）
// ============================================================================
// ★ F-64②（★ D-3 批 2026-09-29 收敛）：本文件所有接口统一经 core.ts 的 bizResp 接线——
//   出参均为 ScimGetResp（继承 AdminResp）信封，后端结构化 4xx 失败被还原成历史
//   {success:false,...} 形态（details 摊平、401/403 照抛以触发重登录），调用方零改动；
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request（AGENTS §一·5，
//   静态锁见 api/bizRespGate.test.ts）。
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** ScimConfig SCIM 2.0 IdP 同步配置（Token/根组织/启停） */
export interface ScimConfig {
  tenant_id: number
  token: string
  enabled: boolean
  root_org_id: number
  created_at?: string
}

/** ScimGetResp 配置读取出参（endpoint 供 IdP 侧回填） */
export interface ScimGetResp extends AdminResp {
  config?: ScimConfig
  endpoint?: string
}

// 读取租户 SCIM 配置（含密钥掩码）；返回形状＝ScimGetResp 信封（继承 AdminResp），走 bizResp
export async function scimConfigGet(): Promise<ScimGetResp> {
  return bizResp(() => request('/api/tenant/scim', { headers: authHeaders() }))
}

// 保存 SCIM 配置（开关/轮换密钥/默认根部门）；返回形状＝ScimGetResp 信封（继承 AdminResp），走 bizResp
export async function scimConfigSave(data: { enabled?: boolean; rotate?: boolean; root_org_id?: number }): Promise<ScimGetResp> {
  return bizResp(() => request('/api/tenant/scim', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}
