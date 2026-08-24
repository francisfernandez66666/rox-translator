// ============================================================================
// stores/chat.ts — 聊天全局状态 Store（Pinia）
// 职责：管理消息列表、加载状态、目标语言、后端健康状态、翻译模型等
// 提供：文本翻译 / 文件翻译 / 示例消息 / 停止生成 / 清除消息等操作
// ============================================================================

// Pinia 状态定义
import { defineStore } from 'pinia'
// Vue 响应式
import { ref, watch } from 'vue'
// API：流式聊天 / 流式文件翻译 / 健康检查
import { chatStream, translateFileStream, healthCheck } from '@/api'
// 类型定义
import type { ChatMessage, ProgressEvent } from '@/types'

// 生成唯一消息 ID（时间戳 + 随机串）
function generateId(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
}

// 聊天 Store 定义（组合式写法）
export const useChatStore = defineStore('chat', () => {
  // 消息列表（用户 + AI 对话记录）
  // ★ 本地缓存：刷新不丢——初始化时从 localStorage 恢复，变更后写回（上限内裁剪）
  const messages = ref<ChatMessage[]>((() => {
    try {
      const raw = localStorage.getItem('chat_msgs_v1')
      if (raw) {
        const arr = JSON.parse(raw) as ChatMessage[]
        if (Array.isArray(arr)) return arr.slice(-200)
      }
    } catch { /* 损坏则忽略 */ }
    return [] as ChatMessage[]
  })())
  // 是否正在生成（发送中）
  const isLoading = ref(false)
  // 已选目标语言列表（本地缓存恢复）
  const selectedLangs = ref<string[]>((() => {
    try { return JSON.parse(localStorage.getItem('chat_langs') || '["en"]') } catch { return ['en'] }
  })())
  // 后端是否在线
  const isBackendOnline = ref(false)
  // 后端是否仍在启动加载中
  const isBackendLoading = ref(true)
  // 后端健康检查进行中（重试按钮点击后禁用，防重复并发检查）
  const isBackendChecking = ref(false)
  // 全局错误信息
  const errorMessage = ref('')
  // 中止控制器（用于取消进行中的 SSE 请求）
  let abortController: AbortController | null = null

  // 消息历史上限：超出后裁剪最旧消息（防长会话内存无界增长）
  const MAX_MESSAGES = 200

  /** 选中的翻译模型（持久化到 localStorage） */
  const selectedModel = ref(localStorage.getItem('translateModel') || 'tencent/Hunyuan-MT-7B')

  // 停止生成：中止请求并清理进度
  function stopGeneration() {
    if (abortController) {
      abortController.abort()
      abortController = null
    }
    isLoading.value = false
    const lastAssistant = [...messages.value].reverse().find(m => m.role === 'assistant')
    if (lastAssistant && !lastAssistant.content) {
      lastAssistant.content = '⏹ 生成已停止'
      lastAssistant.progress = undefined
    } else if (lastAssistant && lastAssistant.progress) {
      lastAssistant.progress = undefined
    }
  }

  // 更新指定消息的翻译进度
  // trimMessages 超限裁剪：保留最近 MAX_MESSAGES 条（从头部丢弃最旧）
  function trimMessages() {
    if (messages.value.length > MAX_MESSAGES) {
      messages.value.splice(0, messages.value.length - MAX_MESSAGES)
    }
  }

  // persistChat 消息与语言选择写回 localStorage（体积保护：>2MB 时仅保留最近 50 条再存）
  function persistChat() {
    try {
      let arr = messages.value
      if (arr.length > 50 && JSON.stringify(arr).length > 2 * 1024 * 1024) arr = arr.slice(-50)
      localStorage.setItem('chat_msgs_v1', JSON.stringify(arr.map(m => ({ ...m, progress: undefined }))))
    } catch { /* 存储满静默 */ }
    localStorage.setItem('chat_langs', JSON.stringify(selectedLangs.value))
  }
  watch(messages, persistChat, { deep: true })
  watch(selectedLangs, persistChat, { deep: true })

  function updateProgress(messageId: string, step: string, percent: number) {
    const msg = messages.value.find(m => m.id === messageId)
    if (msg) msg.progress = { step, percent }
  }

  // 发送文本翻译消息（SSE 流式接收进度与结果）
  async function sendMessage(text: string, options: Record<string, unknown> = {}) {
    if (!text.trim() || isLoading.value) return
    errorMessage.value = ''

    abortController = new AbortController()

    // 生成用户消息并加入列表
    const userMsg: ChatMessage = {
      id: generateId(),
      role: 'user',
      content: text.trim(),
      timestamp: Date.now(),
    }
    messages.value.push(userMsg)

    // 生成占位的 AI 消息（带初始进度）
    const assistantId = generateId()
    const assistantMsg: ChatMessage = {
      id: assistantId,
      role: 'assistant',
      content: '',
      skill: '',
      timestamp: Date.now(),
      progress: { step: '准备翻译...', percent: 0 },
    }
    messages.value.push(assistantMsg)

    isLoading.value = true
    try {
      // 调用流式聊天接口，携带当前所选模型
      const allOptions = { ...options, model: selectedModel.value }
      // ★ 双模式：快速/专业校对随请求透传（localStorage 记忆，默认 pro）
      allOptions.mode = localStorage.getItem('translate_mode') || 'pro'
      const response = await chatStream(text, 'translation', allOptions, (event: ProgressEvent) => {
        updateProgress(assistantId, event.step || '翻译中...', event.percent || 0)
      }, abortController.signal)

      // 写入最终结果到 AI 消息
      const msg = messages.value.find(m => m.id === assistantId)
      if (msg) {
        msg.content = response.reply
        msg.skill = response.skill
        msg.data = response.data
        msg.files = response.files
        msg.progress = undefined
      }
    } catch (error) {
      // 主动中止：静默返回
      if (error instanceof DOMException && error.name === 'AbortError') return
      // 其余错误：写入错误消息
      const errMsg = error instanceof Error ? error.message : '未知错误'
      errorMessage.value = errMsg
      const msg = messages.value.find(m => m.id === assistantId)
      if (msg) {
        msg.content = `❌ ${errMsg}`
        msg.skill = 'error'
        msg.progress = undefined
      }
    } finally {
      isLoading.value = false
      abortController = null
    }
  }

  // 发送文件翻译消息（流式上传 + 进度）
  async function sendFile(file: File, targetLangs: string[] = [], userMessage: string = "") {
    if (isLoading.value) return
    errorMessage.value = ''

    abortController = new AbortController()

    // 生成"上传文件"用户消息
    const userMsg: ChatMessage = {
      id: generateId(),
      role: 'user',
      content: `📄 上传文件：${file.name}`,
      timestamp: Date.now(),
    }
    messages.value.push(userMsg)

    // 生成占位的 AI 消息（带初始进度）
    const assistantId = generateId()
    const assistantMsg: ChatMessage = {
      id: assistantId,
      role: 'assistant',
      content: '',
      skill: '',
      timestamp: Date.now(),
      progress: { step: '上传文件中...', percent: 0 },
    }
    messages.value.push(assistantMsg)

    isLoading.value = true
    try {
      // 调用流式文件翻译接口
      const response = await translateFileStream(
        file,
        targetLangs.length > 0 ? targetLangs : undefined,
        true,
        (event: ProgressEvent) => {
          updateProgress(assistantId, event.step || '翻译中...', event.percent || 0)
        },
        abortController.signal,
        userMessage,
        localStorage.getItem('translate_mode') || 'pro',
      )

      // 写入最终结果到 AI 消息
      const msg = messages.value.find(m => m.id === assistantId)
      if (msg) {
        msg.content = response.reply
        msg.skill = response.skill || 'translation'
        msg.data = response.data
        msg.files = response.files
        msg.progress = undefined
      }
    } catch (error) {
      // 主动中止：静默返回
      if (error instanceof DOMException && error.name === 'AbortError') return
      // 其余错误：写入错误消息
      const errMsg = error instanceof Error ? error.message : '未知错误'
      errorMessage.value = errMsg
      const msg = messages.value.find(m => m.id === assistantId)
      if (msg) {
        msg.content = `❌ 文件翻译失败: ${errMsg}`
        msg.skill = 'error'
        msg.progress = undefined
      }
    } finally {
      isLoading.value = false
      abortController = null
    }
  }

  // 发送示例问题（携带当前所选语言）
  function sendExample(text: string) {
    sendMessage(text, { target_langs: [...selectedLangs.value] })
  }

  // 轮询后端健康状态（最多 30 次，每次间隔 1 秒；单次请求 10 秒超时）
  // 后端挂起时不会无限等待：超时后立即标记离线，供用户点击「重试」再次检查
  async function checkBackendHealth() {
    if (isBackendChecking.value) return
    isBackendChecking.value = true
    isBackendLoading.value = true
    const maxRetries = 30
    for (let i = 0; i < maxRetries; i++) {
      try {
        const result = await healthCheck()
        if (result.status === 'ok') {
          isBackendOnline.value = true
          isBackendLoading.value = false
          isBackendChecking.value = false
          return
        }
      } catch {}
      await new Promise(r => setTimeout(r, 1000))
    }
    isBackendLoading.value = false
    isBackendOnline.value = false
    isBackendChecking.value = false
  }

  // 手动重试后端连接（离线状态栏「重试」按钮）
  function retryBackend() {
    checkBackendHealth()
  }

  // 清空所有消息与错误
  function clearMessages() {
    messages.value = []
    localStorage.removeItem('chat_msgs_v1')
    errorMessage.value = ''
  }

  // 重置全部会话状态（登出/切换账号时调用，防止上个账户的翻译结果泄露）
  function reset() {
    if (abortController) {
      abortController.abort()
      abortController = null
    }
    messages.value = []
    errorMessage.value = ''
    isLoading.value = false
    selectedLangs.value = ['en']
  }

  // 切换翻译模型并持久化
  function setSelectedModel(model: string) {
    selectedModel.value = model
    localStorage.setItem('translateModel', model)
  }

  // 对外暴露的状态与操作
  return {
    messages,
    isLoading,
    selectedLangs,
    isBackendOnline,
    isBackendLoading,
    isBackendChecking,
    errorMessage,
    selectedModel,
    stopGeneration,
    sendMessage,
    sendFile,
    sendExample,
    checkBackendHealth,
    retryBackend,
    clearMessages,
    reset,
    setSelectedModel,
  }
})
