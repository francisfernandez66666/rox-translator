// ============================================================================
// api/salesConfig.ts — 二期「能前端配置的都交给前端配置」接口层
//
// 与 api/sales.ts 同纪律：每个返回 {success,...} 信封的调用一律
// `return bizResp(() => request(...))`，不进任何豁免档。
//
// 后端契约（backend-go/internal/api/sales_config_api.go 实读）：
//   GET  /api/admin/sales/config          三段配置＋词表＋枚举全集
//   POST /api/admin/sales/config/save     保存模型／流程／策略三段配置
//   POST /api/admin/sales/lexicon/save    保存一档租户词表
//   POST /api/admin/sales/lexicon/delete  删除租户自有的一档
//   POST /api/admin/sales/lexicon/seed    平台默认档补种（仅超管）
// ★ 词表写口只写当前租户档；平台默认档不许由租户写（后端 validate 收口）。
// ============================================================================
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 销售流程引擎可配参数 */
export interface SalesFlowConfig {
  enabled: boolean
  sweep_requeue: boolean
  max_steps: number
  default_scene: string
}

/** 销售策略可配参数 */
export interface SalesStrategyConfig {
  scene: string
  lang: string
  min_samples: number
  draft_enabled: boolean
  max_drafts_per_run: number
  window_days: number
}

/** 三段配置（模型阶段 → 模型档位；流程；策略） */
export interface SalesConfig {
  model_stages: Record<string, string>
  flow: SalesFlowConfig
  strategy: SalesStrategyConfig
}

/** 词表一档（平台档 tenant_id=0，租户档为当前租户） */
export interface SalesLexiconRow {
  id: number
  tenant_id: number
  scene: string
  lang: string
  kind: string
  words: string[]
  updated_at: string
}

/** 配置面读响应（空词表出 []，不是 null） */
export interface SalesConfigResp extends AdminResp {
  config?: SalesConfig
  configured?: boolean
  lexicon?: SalesLexiconRow[]
  stages?: string[]
  kinds?: string[]
  scenes?: string[]
  langs?: string[]
}

/** 只读配置面（租户管理员及以上） */
export async function salesConfigGet(): Promise<SalesConfigResp> {
  return bizResp(() => request('/api/admin/sales/config', { headers: authHeaders() }))
}

/** 保存三段配置（唯一配置写口，后端留审计） */
export async function salesConfigSave(config: SalesConfig): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/sales/config/save', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(config),
  }))
}

/** 保存一档租户词表（整份覆盖） */
export async function salesLexiconSave(row: {
  scene: string; lang: string; kind: string; words: string[]
}): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/sales/lexicon/save', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(row),
  }))
}

/** 删除租户自有的一档词表（删除类动作调用方负责先弹一次确认框） */
export async function salesLexiconDelete(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/sales/lexicon/delete', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ id }),
  }))
}

/** 补种平台默认档（仅超管；幂等，不覆盖运营改动） */
export async function salesLexiconSeed(): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/sales/lexicon/seed', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({}),
  }))
}
