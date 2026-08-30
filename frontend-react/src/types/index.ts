// ============ types/index.ts · 职责说明 ============
// 前端共享 TypeScript 类型定义
// 集中定义聊天、流式进度与健康检查等数据结构的类型。
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
  // 后端返回的错误信息
  error?: string
}

/** SSE 流式事件：翻译进度更新 / 完成 / 出错 */
export interface ProgressEvent {
  // 事件类型：progress 进度 / done 完成 / error 错误
  type: 'progress' | 'done' | 'error'
  step?: string      // 当前步骤文案
  done?: number      // 已完成数量
  total?: number     // 总数量
  percent?: number   // 进度百分比
  result?: ChatResponse // 完成时返回的最终结果
  error?: string     // 错误信息
}

/** 聊天消息：用户提问或 AI 回复，附带技能 / 数据 / 文件 / 翻译进度 */
export interface ChatMessage {
  id: string              // 消息唯一 id
  role: 'user' | 'assistant' // 消息发送者角色
  content: string         // 消息文本内容
  skill?: string          // 命中的技能名称
  data?: ChatResponse['data'] // 结构化响应数据
  files?: string[]        // 附件文件路径列表
  timestamp: number       // 消息时间戳（毫秒）
  progress?: {
    step: string    // 当前进度步骤文案
    percent: number // 当前进度百分比
  }
}

/** 健康检查响应：后端状态 + 版本 + 已启用的技能列表 */
export interface HealthResponse {
  status: string  // 服务状态
  version: string // 后端版本号
  skills: string[] // 已启用的技能列表
}
