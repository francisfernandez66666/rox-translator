// ============================================================================
// api/ops.ts — 运营策略引擎接口
// 职责：计费/模式/套餐/时间窗/邀请等运营参数因子配置的读取与保存
// ============================================================================

// ★ F-64②（★ D-3 批 2026-09-29 收敛）：本文件所有接口统一经 core.ts 的 bizResp 接线——
//   出参全部是 AdminResp（或其交叉类型）信封，后端结构化 4xx 失败被还原成历史
//   {success:false,...} 形态（details 摊平、401/403 照抛以触发重登录），调用方零改动；
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request（AGENTS §一·5，
//   静态锁见 api/bizRespGate.test.ts）。
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 读取运营策略（平台默认/租户覆盖/基础/最终/窗口命中态） */
/** ★ H11 SLO/SLI 燃烧率快照（超管） */
/** ★ H7 供应商路由实时统计（超管） */
export async function opsRoutes(): Promise<AdminResp & { routes?: Array<{ route: string; samples: number; p50_ms: number; p95_ms: number; ok: number; fail: number; err_rate: number; tokens_per_call: number }>; dynamic_routing?: boolean; hedge_enabled?: boolean }> {
  return bizResp(() => request('/api/admin/ops/routes', { headers: authHeaders() }))
}

// 后台 SLO 指标（各链路可用性/延迟/错误率）
export async function opsSlo(): Promise<AdminResp & { slos?: Array<{ key: string; name: string; target: number; level?: string; burn_1h: number; burn_6h: number; budget_left_pct?: number; stats?: Array<{ window: string; samples: number; err_rate: number; burn: number }> }> }> {
  return bizResp(() => request('/api/admin/ops/slo', { headers: authHeaders() }))
}

// 查询运营策略（促销窗口等生效配置）
export async function opsPolicy(): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/ops/policy', { headers: authHeaders() }))
}

/** 保存运营策略：scope=platform（超管）| tenant（租户管理员，默认） */
export async function opsPolicySave(scope: 'platform' | 'tenant', policy: Record<string, unknown>): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/ops/policy/save', {
    method: 'POST', headers: authHeaders(), body: JSON.stringify({ scope, policy }),
  }))
}

/** 保存单个推广时间窗（超管） */
export async function opsWindowSave(window: Record<string, unknown>): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/ops/policy/window/save', {
    method: 'POST', headers: authHeaders(), body: JSON.stringify({ window }),
  }))
}

/** 重置当前套餐月度用量（租户管理员+） */
export async function opsPackageReset(tenantId?: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/billing/package/reset', {
    method: 'POST', headers: authHeaders(),
    body: tenantId ? JSON.stringify({ tenant_id: tenantId }) : '{}',
  }))
}

// ============================================================================
// ★ 2026-11-02 用户令「把 CF 的 token 配置放在管理后台，平台管理员配置并可热更新」
//   —— Cloudflare 边缘缓存凭据的三个入口（超管；后端 api/admin_cf_cache.go）。
// 出参字段名与后端**逐字同形**（跨语言等值锁 admin_cf_cache_gate_test.go
//   的 TestCFCacheKeyLiteralsAreSingleSourced 按 ops.ts 里这几个键名判）：
//   set / masked / db_masked / source / env_overridden / zone / token_file / file_present / purge_last
// ⚠️ 三个入口都不返回明文：浏览器只需要"掩码 + 是否已配置"（〇-LK 那条口径——
//   明文进响应体＝进代理日志与 Playwright trace）。
// ============================================================================

/** CF 边缘缓存凭据状态（永不含明文）。 */
export interface CFCacheState {
  set: boolean
  source: string // env | db | none
  masked: string // 生效值掩码
  db_masked: string // 库内密文解出来的掩码（env 覆盖时仍看得见库里是哪个）
  env_overridden: boolean // env 占着生效位 ⇒ 库内值要等 env 撤掉才生效
  zone: string // 要清的 zone 名（zone id 由服务端现查，前端不写死）
  token_file: string // 导出给换源执行体的文件路径（none＝显式关闭）
  file_present: boolean // 那把文件此刻在不在磁盘上
  purge_last: string // 上一次 purge 的留痕（JSON 文本）
}

/** 读 CF 凭据状态（GET /api/admin/ops/cf-cache）。 */
export async function cfCacheState(): Promise<AdminResp & Partial<CFCacheState>> {
  return bizResp(() => request('/api/admin/ops/cf-cache', { headers: authHeaders() }))
}

/** 保存 CF 凭据／zone／导出路径（留空与掩码＝未改动；clear=true 才是显式清空）。 */
export async function cfCacheSave(req: { token?: string; zone?: string; token_file?: string; clear?: boolean }):
  Promise<AdminResp & Partial<CFCacheState> & { changed?: boolean; file_written?: boolean; file_note?: string }> {
  return bizResp(() => request('/api/admin/ops/cf-cache', {
    method: 'POST', headers: authHeaders(), body: JSON.stringify(req),
  }))
}

/** 立即清一次边缘缓存（不必发版；成功回 ok=true）。 */
export async function cfCachePurge(): Promise<AdminResp & { ok?: boolean; zone?: string; http_code?: string; source?: string }> {
  return bizResp(() => request('/api/admin/ops/cf-cache/purge', { method: 'POST', headers: authHeaders(), body: '{}' }))
}
