// ============================================================================
// api/assistAdmin.ts — AI 助手「管理面」前端接口（★ #34 后台 AI 助手前端重做，2026-09-21）
//
// 职责：超管后台 AssistP 面板的唯一数据通道——知识库条目 / 话术 / 流程 / 功能入口 /
//       配置 / 会话统计 / LLM 连通测试，全部走主后台同源接口 `/api/admin/assist/*`。
//
// 为什么不再直连 assist 服务（旧 iframe 方案的教训）：
//   1. 管理 Token 由主后台服务端注入（X-Assist-Admin），浏览器侧**永远拿不到凭据**，
//      也就不再需要 localStorage('assist_tok') 这种把密钥放可读区的 hack；
//   2. 同源单端口 → 跨域 iframe 下 Token 注入失效的老问题一并消失；
//   3. 鉴权口径回到主后台（仅超管），Playwright 只需登录主站即可端到端覆盖助手管理面。
//
// 出参口径：代理把 assist 服务的原始 JSON 原样回传（{rows:[]} / {configs:[]} / {sessions…}），
//   因此本文件的类型与 assist 侧表结构逐字段对齐；一旦 fail-closed（Token 未配置、服务不可达），
//   主后台回 {success:false,message}，由 ok() 统一抛 AssistBizError，面板集中展示 message。
//
// ★ F-64②（★ D-3 批 2026-09-29 收敛）：本文件所有 request 调用统一经 core.ts 的 bizResp 接线。
//   判据逐处过：成功形态虽多为 {rows:[]}/{configs:[]} 这类**非信封**裸对象，但失败形态一律是
//   {success:false,message} 信封（含后端状态码诚实化后的结构化 4xx）——bizResp 把结构化 4xx
//   还原成历史 {success:false,...} 壳后，恰好落进 guardBiz 的同一条「抛 AssistBizError」通道，
//   面板维持「只处理一条失败路径」的设计不变（401/403 照抛 ApiError，见 core.bizResp 注释）。
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request（AGENTS §一·5，
//   静态锁见 api/bizRespGate.test.ts）。
// ============================================================================
import { bizResp, request, authHeaders } from './core'

/** 管理面区域 → 主后台代理路径（与后端 assistProxyRoutes 白名单一一对应） */
export type AssistArea = 'kb' | 'scripts' | 'flows' | 'features'

// AI 顾问三个域（kb/config/sessions）共用同一条管理台路径前缀
const areaPath = (area: AssistArea) => `/api/admin/assist/${area}`

/** 代理侧业务失败（Token 未配置 / 服务不可达 / 上游 4xx5xx 的业务提示） */
export class AssistBizError extends Error {}

/**
 * 原始响应里带 success:false 即视为业务失败，统一抛错让面板只处理一条失败路径。
 * 与 bizResp 的配合关系（D-3 批）：结构化 4xx 失败体已由 bizResp 还原成 {success:false,message}
 * 壳，在这里与历史 200+success:false 透传体走同一个判据抛 AssistBizError，两条失败路径合一。
 */
function guardBiz(data: any) {
  if (data && data.success === false && data.message) throw new AssistBizError(String(data.message))
  return data
}

/** 配置项（assist configs 表：key/value 两列，llm_api_key 回显为掩码） */
export interface AssistConfigRow { key: string; value: string }

/** 数据行动态列：assist 侧为 SQLite 动态表，字段随区域不同（见 internal/assist/store） */
export type AssistRow = Record<string, string | number>

/** 会话统计响应（含 R0.2 未答问题清单与 R0.3 LLM 生效模式） */
export interface AssistSessionsResp {
  sessions: AssistRow[]
  total: number
  messages: number
  unanswered: string[]
  llm_mode: string
}

/** 状态条响应：上游可达性 + Token 来源（env/db/none），不含 Token 明文 */
export interface AssistStatusResp {
  success: boolean
  base_url: string
  reachable: boolean
  token_src: string
  message: string
}

/** LLM 连通测试结果 */
export interface AssistLLMTestResp { ok: boolean; model?: string; ms?: number; sample?: string; error?: string }

/** 服务状态（面板挂载即调；不可达时面板直接给出处置指引而不是逐 tab 报「加载失败」）；返回形状＝AssistStatusResp 信封（含 success），走 bizResp */
export async function assistAdminStatus(): Promise<AssistStatusResp> {
  // 与同文件其余接口一致过 guardBiz：面板调用点是 runGuarded(() => assistAdminStatus())，
  // 只吃异常通道——若在这里直接返回 {success:false} 壳，`if (r) setStatus(r)` 会把失败体
  // 当成状态载进面板（D-3 复核修正，2026-09-29）
  return guardBiz(await bizResp(() => request<any>('/api/admin/assist/status', { headers: authHeaders() }))) as AssistStatusResp
}

/** 配置全量读取（含掩码后的 llm_api_key）；成功形＝{configs:[...]} 裸对象，失败形＝{success:false} 信封 → 按失败形走 bizResp（见文件头判据） */
export async function assistAdminConfig(): Promise<AssistConfigRow[]> {
  const d = guardBiz(await bizResp(() => request<any>(`/api/admin/assist/config`, { headers: authHeaders() })))
  return (d?.configs as AssistConfigRow[]) || []
}

/** 单项配置写入（key 必须在服务端白名单内，否则 assist 侧回 400）；失败形＝{success:false} 信封，走 bizResp */
export async function assistAdminConfigSet(key: string, value: string): Promise<any> {
  return guardBiz(await bizResp(() => request<any>('/api/admin/assist/config', {
    method: 'PUT', headers: authHeaders(), body: JSON.stringify({ key, value }),
  })))
}

/** 列表读取（知识库 / 话术 / 流程 / 功能入口）；成功形＝{rows:[...]} 裸对象，失败形＝{success:false} 信封 → 按失败形走 bizResp */
export async function assistAdminList(area: AssistArea): Promise<AssistRow[]> {
  const d = guardBiz(await bizResp(() => request<any>(areaPath(area), { headers: authHeaders() })))
  return (d?.rows as AssistRow[]) || []
}

/** 新建记录：body 为整行字段（key 唯一标识由用户填，assist 侧 UNIQUE 约束）；失败形＝{success:false} 信封，走 bizResp */
export async function assistAdminCreate(area: AssistArea, data: AssistRow): Promise<any> {
  return guardBiz(await bizResp(() => request<any>(areaPath(area), {
    method: 'POST', headers: authHeaders(), body: JSON.stringify(data),
  })))
}

/** 更新记录（assist 侧按 ?id= 定位，body 为变更字段）；失败形＝{success:false} 信封，走 bizResp */
export async function assistAdminUpdate(area: AssistArea, id: number, data: AssistRow): Promise<any> {
  return guardBiz(await bizResp(() => request<any>(`${areaPath(area)}?id=${id}`, {
    method: 'PUT', headers: authHeaders(), body: JSON.stringify(data),
  })))
}

/** 删除记录；失败形＝{success:false} 信封，走 bizResp */
export async function assistAdminDelete(area: AssistArea, id: number): Promise<any> {
  return guardBiz(await bizResp(() => request<any>(`${areaPath(area)}?id=${id}`, { method: 'DELETE', headers: authHeaders() })))
}

/** 会话列表 + 统计 + 未答问题 + LLM 生效模式；成功形＝{sessions,total…} 裸对象，失败形＝{success:false} 信封 → 按失败形走 bizResp */
export async function assistAdminSessions(): Promise<AssistSessionsResp> {
  const d = guardBiz(await bizResp(() => request<any>('/api/admin/assist/sessions', { headers: authHeaders() })))
  return {
    sessions: (d?.sessions as AssistRow[]) || [],
    total: Number(d?.total ?? 0),
    messages: Number(d?.messages ?? 0),
    unanswered: (d?.unanswered as string[]) || [],
    llm_mode: String(d?.llm_mode ?? 'rule'),
  }
}

/** 测试 LLM 连通（用当前生效配置发一条极短请求，耗时可能到秒级，代理侧超时 30s）；成功形＝{ok,model…} 裸对象，失败形＝{success:false} 信封 → 按失败形走 bizResp */
export async function assistAdminLLMTest(): Promise<AssistLLMTestResp> {
  const d = guardBiz(await bizResp(() => request<any>('/api/admin/assist/llm-test', {
    method: 'POST', headers: authHeaders(), body: '{}', timeoutMs: 35000,
  })))
  return d as AssistLLMTestResp
}
