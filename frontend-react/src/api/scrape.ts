// ============================================================================
// api/scrape.ts — 行业包/语言文化包自动采集域接口（超管）
// 职责：数据源 CRUD/启停/手动触发、待审池列表、批量审批（热加载）
// ============================================================================

/**
 * api/scrape.ts · 职责说明
 * 封装「行业包/语言文化包自动采集」的后台管理接口：
 * - 数据源管理：列表、新增、更新、启停、手动立即采集一轮
 * - 待审池：条目/安全句列表（可按包类型/状态/语言筛选）
 * - 批量审批：通过（落正式库+热加载）/ 驳回
 * - 概览：待审数/源数/最近完成日
 */

import { request, authHeaders, type AdminResp } from './core'

/** 数据源实体 */
export interface ScrapeSource {
  id: number
  kind: string        // official_api / limited_web / llm_gen
  name: string
  base_url: string
  lang: string
  industry: string
  pack_type: string   // industry / locale
  enabled: number
  freq_hours: number
  tier: number        // 1官方 / 2受限抓取 / 3LLM
  last_run_at: string
  last_status: string
  created_at: string
}

/** 待审条目实体 */
export interface StagedEntry {
  id: number
  target_pack_id: number
  pack_type: string
  source_id: number
  tier: number
  layer: number
  src_lang: string
  src_text: string
  tgt_lang: string
  tgt_text: string
  source_url: string
  status: string
  created_at: string
}

/** 待审安全句实体 */
export interface StagedPhrase {
  id: number
  package_id: number
  lang: string
  phrase: string
  kind: string
  replacement: string
  tier: number
  status: string
  created_at: string
}

/** 待审合并行（entries/phrases 两表 UNION 统一结构，服务端分页返回） */
export interface StagedMergedRow {
  key: string            // kind:id 复合键（rowKey/选中键）
  id: number
  kind: 'entries' | 'phrases'
  phrase_kind: string    // phrases 的 style/forbidden/replace（entries 为空）
  pack_type: string
  tier: number
  src_lang: string
  src_text: string       // entries=源文本；phrases=语言文化短语
  tgt_lang: string
  tgt_text: string       // entries=译文；phrases=规范/替换词
  source_url: string
  status: string
}

/** 概览 */
export interface ScrapeSummary {
  pending_entries: number
  pending_phrases: number
  sources_total: number
  sources_enabled: number
  last_daily: string
}

/** 数据源列表 */
export async function scrapeSources(): Promise<AdminResp & { sources?: ScrapeSource[] }> {
  return request('/api/admin/kb-scrape/sources', { headers: authHeaders() })
}

/** 新增数据源 */
export async function scrapeSourceCreate(data: Partial<ScrapeSource>): Promise<AdminResp & { id?: number }> {
  return request('/api/admin/kb-scrape/sources/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 更新数据源 */
export async function scrapeSourceUpdate(data: Partial<ScrapeSource>): Promise<AdminResp> {
  return request('/api/admin/kb-scrape/sources/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 启停数据源 */
export async function scrapeSourceStatus(id: number, enabled: number): Promise<AdminResp> {
  return request('/api/admin/kb-scrape/sources/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, enabled }) })
}

/** 手动立即采集一轮 */
export async function scrapeSourceRun(): Promise<AdminResp & { sources_done?: number }> {
  return request('/api/admin/kb-scrape/sources/run', { method: 'POST', headers: authHeaders() })
}

/** 待审池列表（服务端分页：limit/offset + 合并行集 rows + 精确总数 total） */
export async function scrapeStaged(params: { pack_type?: string; status?: string; lang?: string; limit?: number; offset?: number }): Promise<AdminResp & { rows?: StagedMergedRow[]; total?: number; limit?: number; offset?: number }> {
  const q = new URLSearchParams()
  if (params.pack_type) q.set('pack_type', params.pack_type)
  if (params.status) q.set('status', params.status)
  if (params.lang) q.set('lang', params.lang)
  if (params.limit) q.set('limit', String(params.limit))
  if (params.offset) q.set('offset', String(params.offset))
  return request(`/api/admin/kb-scrape/staged?${q.toString()}`, { headers: authHeaders() })
}

/** 批量审批：通过（落正式库+热加载）/ 驳回 */
export async function scrapeApprove(kind: 'entries' | 'phrases', ids: number[], action: 'approve' | 'reject'): Promise<AdminResp & { updated?: number; applied?: number; rewards?: { tenant_id?: number; tokens?: number; chars?: number; per_char?: number }[] }> {
  return request('/api/admin/kb-scrape/approve', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ kind, ids, action }) })
}

/** 还原为待审：把已通过/已驳回条目拉回待审池，支持还原前编辑内容 */
export async function scrapeRestore(
  kind: 'entries' | 'phrases',
  ids: number[],
  edits?: Record<string, { src_text?: string; tgt_text?: string; phrase?: string; replacement?: string }>,
): Promise<AdminResp & { restored?: number; reverted?: number }> {
  return request('/api/admin/kb-scrape/restore', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ kind, ids, edits: edits ?? {} }) })
}

/** 采集概览 */
export async function scrapeSummary(): Promise<AdminResp & { summary?: ScrapeSummary }> {
  return request('/api/admin/kb-scrape/summary', { headers: authHeaders() })
}

/** KB 上传奖励配置：读取开关/单价/日封顶 */
export async function kbRewardConfigGet(): Promise<AdminResp & { enabled?: boolean; per_char?: number; daily_cap?: number }> {
  return request('/api/admin/kb-reward', { headers: authHeaders() })
}

/** KB 上传奖励配置：更新（enabled 传 null=不改） */
export async function kbRewardConfigSet(body: { enabled?: boolean | null; per_char?: number; daily_cap?: number }): Promise<AdminResp & { enabled?: boolean; per_char?: number; daily_cap?: number }> {
  return request('/api/admin/kb-reward', { method: 'POST', headers: authHeaders(), body: JSON.stringify(body) })
}
