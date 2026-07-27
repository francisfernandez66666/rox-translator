import { defineStore } from 'pinia'
import { ref } from 'vue'
import { chatStream, translateFileStream, healthCheck } from '@/api'
import type { ChatMessage, ProgressEvent } from '@/types'

function generateId(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
}

export const useChatStore = defineStore('chat', () => {
  const messages = ref<ChatMessage[]>([])
  const isLoading = ref(false)
  const selectedLangs = ref<string[]>(['en'])
  const isBackendOnline = ref(false)
  const isBackendLoading = ref(true)
  const errorMessage = ref('')
  let abortController: AbortController | null = null

  /** 选中的翻译模型 */
  const selectedModel = ref(localStorage.getItem('translateModel') || 'THUDM/GLM-4-9B-0414')

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

  function updateProgress(messageId: string, step: string, percent: number) {
    const msg = messages.value.find(m => m.id === messageId)
    if (msg) msg.progress = { step, percent }
  }

  async function sendMessage(text: string, options: Record<string, unknown> = {}) {
    if (!text.trim() || isLoading.value) return
    errorMessage.value = ''

    abortController = new AbortController()

    const userMsg: ChatMessage = {
      id: generateId(),
      role: 'user',
      content: text.trim(),
      timestamp: Date.now(),
    }
    messages.value.push(userMsg)

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
      const allOptions = { ...options, model: selectedModel.value }
      const response = await chatStream(text, 'translation', allOptions, (event: ProgressEvent) => {
        updateProgress(assistantId, event.step || '翻译中...', event.percent || 0)
      }, abortController.signal)

      const msg = messages.value.find(m => m.id === assistantId)
      if (msg) {
        msg.content = response.reply
        msg.skill = response.skill
        msg.data = response.data
        msg.files = response.files
        msg.progress = undefined
      }
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return
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

  async function sendFile(file: File, targetLangs: string[] = [], userMessage: string = "") {
    if (isLoading.value) return
    errorMessage.value = ''

    abortController = new AbortController()

    const userMsg: ChatMessage = {
      id: generateId(),
      role: 'user',
      content: `📄 上传文件：${file.name}`,
      timestamp: Date.now(),
    }
    messages.value.push(userMsg)

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
      const response = await translateFileStream(
        file,
        targetLangs.length > 0 ? targetLangs : undefined,
        true,
        (event: ProgressEvent) => {
          updateProgress(assistantId, event.step || '翻译中...', event.percent || 0)
        },
        abortController.signal,
        userMessage,
      )

      const msg = messages.value.find(m => m.id === assistantId)
      if (msg) {
        msg.content = response.reply
        msg.skill = response.skill || 'translation'
        msg.data = response.data
        msg.files = response.files
        msg.progress = undefined
      }
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return
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

  function sendExample(text: string) {
    sendMessage(text, { target_langs: [...selectedLangs.value] })
  }

  async function checkBackendHealth() {
    isBackendLoading.value = true
    const maxRetries = 30
    for (let i = 0; i < maxRetries; i++) {
      try {
        const result = await healthCheck()
        if (result.status === 'ok') {
          isBackendOnline.value = true
          isBackendLoading.value = false
          return
        }
      } catch {}
      await new Promise(r => setTimeout(r, 1000))
    }
    isBackendLoading.value = false
    isBackendOnline.value = false
  }

  function clearMessages() {
    messages.value = []
    errorMessage.value = ''
  }

  function setSelectedModel(model: string) {
    selectedModel.value = model
    localStorage.setItem('translateModel', model)
  }

  return {
    messages,
    isLoading,
    selectedLangs,
    isBackendOnline,
    isBackendLoading,
    errorMessage,
    selectedModel,
    stopGeneration,
    sendMessage,
    sendFile,
    sendExample,
    checkBackendHealth,
    clearMessages,
    setSelectedModel,
  }
})
