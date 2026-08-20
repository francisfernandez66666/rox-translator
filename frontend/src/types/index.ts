// ============================================================================
// types/index.ts — 前端共享 TypeScript 类型定义
// 职责：集中定义聊天 / 流式进度 / 健康检查等数据结构的类型
// ============================================================================

// 技能信息：技能名称 + 描述 + 触发关键词（供后端技能路由与前端展示用）
export interface SkillInfo {
  name: string
  description: string
  keywords: string[]
}

// 匹配报告单项：某个语言在术语库中的匹配状态与说明
export interface MatchReportItem {
  lang: string
  status: string
  detail: string
}

// 聊天接口响应：技能名 + 回复文本 + 结构化数据（翻译结果/匹配报告等） + 附件文件
export interface ChatResponse {
  skill: string
  reply: string
  data?: {
    translations?: Record<string, string>
    translations_source?: Record<string, string>
    lang_names?: Record<string, string>
    kb_langs?: string[]
    other_langs?: string[]
    source_text?: string
    mode?: string
    similarity?: number
    matched_zh?: string
    match_report?: MatchReportItem[]
    [key: string]: unknown
  }
  files?: string[]
  error?: string
}

// SSE 流式事件：翻译进度更新 / 完成 / 出错
export interface ProgressEvent {
  type: 'progress' | 'done' | 'error'
  step?: string
  done?: number
  total?: number
  percent?: number
  result?: ChatResponse
  error?: string
}

// 聊天消息：用户提问或 AI 回复，附带技能 / 数据 / 文件 / 翻译进度
export interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  skill?: string
  data?: ChatResponse['data']
  files?: string[]
  timestamp: number
  progress?: {
    step: string
    percent: number
  }
}

// 健康检查响应：后端状态 + 版本 + 已启用的技能列表
export interface HealthResponse {
  status: string
  version: string
  skills: string[]
}
