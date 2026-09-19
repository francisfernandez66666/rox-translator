// ============ types/index.ts · 职责说明 ============
// 前端共享 TypeScript 类型定义
// 集中定义聊天、流式进度与健康检查等数据结构的类型。
// 这些类型只描述与后端的传输契约（SSE 事件 / ChatResponse / HealthResponse），
// 不含展示层字段；2026-09-17 换肤未改动任何字段，仅整理注释。
// 2026-09-19 B1 流式双态例外：ChatMessage 新增 draft（逐语言初译草稿）——
// 它承载的是流式期间未定稿的译文视图，随 done/停止清空且不落盘，属于会话状态而非展示层。
// =============================================

/** 技能信息：技能名称 + 描述 + 触发关键词（供后端技能路由与前端展示用） */
export interface SkillInfo {
  name: string        // 技能唯一标识名
  description: string // 技能功能描述
  keywords: string[]  // 触发该技能的关键词列表
}

/** 匹配报告单项：某个语言在术语库中的匹配状态与说明 */
export interface MatchReportItem {
  lang: string   // 语言代码
  status: string // 匹配状态
  detail: string // 匹配详情说明
}

/** 聊天接口响应：技能名 + 回复文本 + 结构化数据（翻译结果/匹配报告等） + 附件文件 */
export interface ChatResponse {
  skill: string // 命中的技能名称
  reply: string // AI 回复文本
  data?: {
    // 各目标语言的翻译结果
    translations?: Record<string, string>
    // 各目标语言的原文对照
    translations_source?: Record<string, string>
    // 各语言代码的展示名称
    lang_names?: Record<string, string>
    // 知识库支持的语言列表
    kb_langs?: string[]
    // 其他候选语言列表
    other_langs?: string[]
    // 原始输入文本
    source_text?: string
    // 当前使用的翻译模式
    mode?: string
    // 相似度分数
    similarity?: number
    // 匹配到的中文内容
    matched_zh?: string
    // 术语匹配报告
    match_report?: MatchReportItem[]
    // 允许后端扩展其他字段
    [key: string]: unknown
  }
  // 返回的附件文件路径列表
  files?: string[]
  // ★ 2026-09-19 积分口径：本次翻译实际消耗（积分，后端由内部 token 折算）
  points_used?: number
  // 后端返回的错误信息
  error?: string
}

/** SSE 流式事件：翻译进度更新 / 完成 / 出错 / token 增量 / ★B3 文件逐段事件 */
export interface ProgressEvent {
  // 事件类型：progress 进度 / done 完成 / error 错误 / delta token 级增量（D20）
  // segment_done|segment_final|segments_sealed=文件翻译逐段上屏事件（★B3，方案 A2）
  type: 'progress' | 'done' | 'error' | 'delta' | 'segment_done' | 'segment_final' | 'segments_sealed'
  step?: string      // 当前步骤文案
  done?: number      // 已完成数量
  total?: number     // 总数量
  percent?: number   // 进度百分比
  result?: ChatResponse // 完成时返回的最终结果
  error?: string     // 错误信息
 // —— 以下三个字段为流式改造新增：error_code 供 UI 精准分流（充值 / 次日再试），
 //    lang + text 由 api/translate.ts 的 consumeSSEStream 透传给 onDelta，
 //    hooks/useChat 再按目标语言过滤、逐字回灌到助手气泡（D20）——
  error_code?: string // ★ E11：稳定错误码（insufficient_balance / daily_quota_exceeded）
  lang?: string      // ★ D20：delta 事件的目标语言；★B3 同样标识段落事件语言
  text?: string      // ★ D20：delta 增量文本
  // —— ★ B3（方案 A2）逐段事件字段（与后端 engine/file_events.go 载荷一一对应，
  //    全积分口径、零 token 裸值；身份稳定键 = index（首次出现段序号）+ source_hash ——
  index?: number         // 段落首次出现序号（重复源文共享同一行键）
  source?: string        // 原始源文（非品牌保护改写后的文本）
  source_hash?: string   // 源文 16 位 hex 散列（前端二次校验行键）
  draft?: string         // segment_done：初译草稿（未定稿，可能被审校/闸门改写）
  target?: string        // segment_final：终稿
  stage?: string         // initial | reviewed | gated
  placeholder?: boolean  // 敏感词拦截占位段：不得当译文展示/编辑
}

/** ★ B3（方案 A2）：consumeSSEStream 归一化后的文件逐段事件（kind 判别 + 字段拉平） */
export interface FileSegmentEvent {
  kind: 'segment_done' | 'segment_final' | 'segments_sealed'
  lang: string
  index?: number
  source?: string
  sourceHash?: string
  text?: string // segment_done → draft；segment_final → target
  stage?: string
  placeholder?: boolean
}

/** ★ B3：某语言逐段实时状态桶（rows 以首次出现段序号为键；sealed=该语言不再变化） */
export interface FileSegBucket {
  sealed: boolean
  rows: Record<number, { text: string; final: boolean; placeholder: boolean }>
}

/** 聊天消息：用户提问或 AI 回复，附带技能 / 数据 / 文件 / 翻译进度 */
export interface ChatMessage {
  id: string              // 消息唯一 id
  role: 'user' | 'assistant' // 消息发送者角色
  content: string         // 消息文本内容
  skill?: string          // 命中的技能名称
  data?: ChatResponse['data'] // 结构化响应数据
  files?: string[]        // 附件文件路径列表
  points_used?: number    // ★ 2026-09-19 积分口径：本次翻译消耗（积分）
  // ★ B1（2026-09-19 流式双态）：逐语言 token 级初译草稿（已按后端 <t> 输出契约清洗）。
  //   流式期间与 progress 共存呈现（草稿行 + 细进度条）；done/error/停止时清空，不落盘。
  draft?: Record<string, string>
  // ★ B3（方案 A2）：文件翻译流式期间的逐段实时状态（lang → 状态桶）。
  //   done 落定即弃（切下载卡）、停止清空、中断保留但标记非交付物；与 draft 同属展示层临时态，不落盘。
  segments?: Record<string, FileSegBucket>
  segmentsAborted?: boolean
  timestamp: number       // 消息时间戳（毫秒）
  progress?: {
    step: string    // 当前进度步骤文案
    done?: number   // 已完成数量
    total?: number  // 总数量
    percent: number // 当前进度百分比
  }
}

/** 健康检查响应：后端状态 + 版本 + 已启用的技能列表 */
export interface HealthResponse {
  status: string  // 服务状态
  version: string // 后端版本号
  skills: string[] // 已启用的技能列表
}
