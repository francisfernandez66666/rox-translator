export interface SkillInfo {
  name: string
  description: string
  keywords: string[]
}

export interface MatchReportItem {
  lang: string
  status: string
  detail: string
}

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

export interface ProgressEvent {
  type: 'progress' | 'done' | 'error'
  step?: string
  done?: number
  total?: number
  percent?: number
  result?: ChatResponse
  error?: string
}

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

export interface HealthResponse {
  status: string
  version: string
  skills: string[]
}
