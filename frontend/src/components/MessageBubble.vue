// ============================================================================
// components/MessageBubble.vue — 消息气泡（含翻译进度条 + Markdown渲染 + 下载卡片）
// ============================================================================
// 【功能】
//   - 用户消息靠右蓝色，AI回复靠左灰色
//   - AI回复带技能徽章
//   - ★ 翻译进度条：实时显示步骤 + 百分比
//   - 翻译结果：多语言表格 + 匹配度报告
//   - ★ 文件下载卡片：醒目的Word文档下载卡片（替代一行小字链接）
//   - ★ Markdown渲染：标题/段落/列表/加粗/代码等
// ============================================================================

<template>
  <!-- ===== 单条消息行：靠左（AI）或靠右（用户） ===== -->
  <div class="message-row" :class="[message.role, { 'msg-mobile': isMobile }]">
    <!-- AI 头像 -->
    <div v-if="message.role === 'assistant'" class="avatar avatar-ai">
      <span class="avatar-text">AI</span>
    </div>

    <!-- ===== 气泡主体内容 ===== -->
    <div class="bubble" :class="message.role">
      <!-- 技能徽章 -->
      <div v-if="message.role === 'assistant' && message.skill" class="bubble-badge">
        <SkillBadge :skill="message.skill" />
      </div>

      <!-- ===== 翻译进度条：实时步骤 + 百分比 ===== -->
      <div
        v-if="message.role === 'assistant' && message.progress && message.progress.percent < 100"
        class="progress-area"
      >
        <div class="progress-header">
          <span class="progress-step">{{ message.progress.step }}</span>
          <span class="progress-percent">{{ message.progress.percent }}%</span>
        </div>
        <div class="progress-bar-bg">
          <div
            class="progress-bar-fill"
            :style="{ width: message.progress.percent + '%' }"
          ></div>
        </div>
      </div>

      <!-- ===== 翻译结果：多语言表格展示 ===== -->
      <div
        v-if="message.role === 'assistant' && message.data?.translations"
        class="translation-results"
      >
        <!-- 匹配模式指示（精确命中/模糊匹配/语义相似/在线翻译） -->
        <div v-if="message.data.mode" class="translation-mode">
          <span v-if="message.data.mode.includes('精确命中')" class="mode-badge mode-exact">✅ 精确命中</span>
          <span v-else-if="message.data.mode.includes('模糊')" class="mode-badge mode-fuzzy">🔄 模糊匹配</span>
          <span v-else-if="message.data.mode.includes('语义高相似')" class="mode-badge mode-semantic">🔍 语义相似</span>
          <span v-else class="mode-badge mode-model">🤖 在线翻译</span>
          <span v-if="message.data.matched_zh" class="mode-match-text">「{{ message.data.matched_zh }}」</span>
        </div>
        <div
          v-for="(text, lang) in message.data.translations"
          :key="lang"
          class="lang-row"
        >
          <span class="lang-label">{{ getLangName(lang) }}</span>
          <span class="lang-text">{{ text }}</span>
          <span
            v-if="message.data.translations_source?.[lang]"
            class="source-badge"
            :class="message.data.translations_source[lang] === 'kb' ? 'source-kb' : 'source-model'"
          >
            {{ message.data.translations_source[lang] === 'kb' ? '📚 知识库' : '🤖 AI翻译' }}
          </span>
        </div>
      </div>

      <!-- ★ 普通文本内容（Markdown渲染） -->
      <div
        v-if="!message.data?.translations && message.content"
        class="bubble-text"
        v-html="formattedContent"
      ></div>

      <!-- 匹配度报告 -->
      <div
        v-if="message.data?.match_report && message.data.match_report.length > 0"
        class="match-report"
      >
        <div class="report-title">📊 术语匹配</div>
        <div class="report-grid">
          <div
            v-for="item in message.data.match_report"
            :key="item.lang"
            class="report-item"
          >
            <span class="report-status">{{ item.status }}</span>
            <span class="report-lang">{{ getLangName(item.lang) }}</span>
          </div>
        </div>
      </div>

      <!-- ===== 附件文件展示：图片内联 + 非图片下载卡片 ===== -->
      <div v-if="message.files && message.files.length > 0" class="file-downloads">
        <template v-for="file in message.files" :key="file">
          <!-- 图片文件：内联展示 + 点击放大 -->
          <div v-if="isImage(file)" class="image-preview">
            <img
              :src="getDownloadUrl(file)"
              :alt="getFileName(file)"
              class="preview-img"
              @click="openImage(getDownloadUrl(file))"
            />
            <a :href="getDownloadUrl(file)" class="image-download-btn" target="_blank" download>
              📥 下载图片
            </a>
          </div>
          <!-- 非图片文件：下载卡片 -->
          <div
            v-else
            class="download-card"
            :class="{ 'download-card-docx': isDocx(file), 'download-card-md': !isDocx(file) }"
          >
            <div class="card-icon">
              <span v-if="['W','P','X'].includes(getFileIcon(file))" :class="{
                'icon-docx': getFileIcon(file) === 'W',
                'icon-pptx': getFileIcon(file) === 'P',
                'icon-xlsx': getFileIcon(file) === 'X',
              }">{{ getFileIcon(file) }}</span>
              <span v-else class="icon-file">{{ getFileIcon(file) }}</span>
            </div>
            <div class="card-info">
              <div class="card-filename">{{ getFileName(file) }}</div>
              <div class="card-meta">
                {{ getFileTypeLabel(file) }} · 点击下载
              </div>
            </div>
            <a :href="getDownloadUrl(file)" class="card-btn" target="_blank" download>
              📥
            </a>
          </div>
        </template>
      </div>
    </div>

    <!-- 用户头像 -->
    <div v-if="message.role === 'user'" class="avatar avatar-user">
      <span class="avatar-text">我</span>
    </div>
  </div>
</template>

<script setup lang="ts">
// Vue 响应式与生命周期
import { computed, ref, onMounted } from 'vue'
// 技能徽章组件
import SkillBadge from './SkillBadge.vue'
// API：获取文件下载 URL
import { getDownloadUrl } from '@/api'
// 类型定义
import type { ChatMessage } from '@/types'

// 语言代码 → 中文名本地映射（渲染翻译结果时展示语言名）
const LANG_NAMES: Record<string, string> = {
  // KB语言
  en: '英语', ru: '俄语', ar: '阿拉伯语', es: '西班牙语',
  pt: '葡萄牙语', fr: '法语', kk: '哈萨克语', de: '德语',
  zh_hant: '繁体中文',
  // ★ 其他常见语言（持续扩展，避免前端显示代码缩写）
  ja: '日语', ko: '韩语', th: '泰语', vi: '越南语', ms: '马来语',
  id: '印尼语', it: '意大利语', pl: '波兰语', sv: '瑞典语', nl: '荷兰语',
  uk: '乌克兰语', el: '希腊语', cs: '捷克语', ro: '罗马尼亚语',
  hu: '匈牙利语', fi: '芬兰语', da: '丹麦语', no: '挪威语',
  tr: '土耳其语', he: '希伯来语', fa: '波斯语', hi: '印地语',
  ur: '乌尔都语', bn: '孟加拉语', ta: '泰米尔语', mn: '蒙语',
  my: '缅甸语', km: '柬埔寨语', lo: '老挝语', fil: '菲律宾语',
}

/** ★ 获取语言中文名：优先后端返回的 lang_names，否则查本地 LANG_NAMES，最后回退为语言代码 */
function getLangName(lang: string): string {
  // 后端返回的 lang_names 映射最权威（包含动态解析的"其他语言"中文名）
  const dataNames = props.message.data?.lang_names as Record<string, string> | undefined
  if (dataNames && dataNames[lang]) return dataNames[lang]
  return LANG_NAMES[lang] || lang
}

// 组件入参：需要渲染的单条聊天消息
const props = defineProps<{ message: ChatMessage }>()

// 移动端标记：窗口宽度 ≤ 768px 时启用移动端样式
const isMobile = ref(window.innerWidth <= 768)
onMounted(() => {
  const onResize = () => { isMobile.value = window.innerWidth <= 768 }
  window.addEventListener('resize', onResize)
})

// ★ 根据文件扩展名获取文件类型描述
function getFileTypeLabel(path: string): string {
  const ext = path.split('.').pop()?.toLowerCase() || ''
  const typeMap: Record<string, string> = {
    docx: 'Word 文档', doc: 'Word 文档',
    pptx: 'PPT 演示文稿', ppt: 'PPT 演示文稿',
    xlsx: 'Excel 表格', xls: 'Excel 表格', csv: 'CSV 表格',
    pdf: 'PDF 文档',
    md: 'Markdown 文件', txt: '文本文件',
    png: '图片', jpg: '图片', jpeg: '图片', gif: '图片', webp: '图片',
  }
  return typeMap[ext] || '文件'
}

// ★ 获取文件图标
function getFileIcon(path: string): string {
  const ext = path.split('.').pop()?.toLowerCase() || ''
  const iconMap: Record<string, string> = {
    docx: 'W', doc: 'W',
    pptx: 'P', ppt: 'P',
    xlsx: 'X', xls: 'X', csv: 'X',
    pdf: '📄', md: '📝', txt: '📝',
  }
  return iconMap[ext] || '📄'
}

// 兼容旧函数
function isDocx(path: string): boolean {
  return path.toLowerCase().endsWith('.docx')
}

// ★ 判断文件是否为图片
function isImage(path: string): boolean {
  return /\.(png|jpg|jpeg|gif|webp|bmp)$/i.test(path)
}

// ★ 点击图片放大查看
function openImage(url: string) {
  window.open(url, '_blank')
}

// ============================================================================
// ★ 轻量 Markdown → HTML 转换器
// ============================================================================

function renderMarkdown(text: string): string {
  // ---- 0. HTML转义 ----
  text = text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')

  // ---- 1. 引用块 ----
  text = text.replace(/(?:^|\n)((?:&gt;\s*.*\n?)+)/g, (_match, block: string) => {
    const lines = block.trim().split('\n').map((l: string) => l.replace(/^&gt;\s*/, ''))
    return `\n<blockquote>${lines.join('<br>')}</blockquote>\n`
  })

  // ---- 2. 标题 ----
  text = text.replace(/^(#{1,6})\s+(.+)$/gm, (_match, hashes: string, content: string) => {
    const level = hashes.length
    return `<h${level}>${content.trim()}</h${level}>`
  })

  // ---- 3. 分隔线 ----
  text = text.replace(/^(?:---|\*\*\*)\s*$/gm, '<hr>')

  // ---- 4. 列表项 ----
  text = text.replace(/^(\s*)([-*+])\s+(.+)$/gm, '<li class="li-unordered">$3</li>')
  text = text.replace(/^(\s*)(\d+[.)])\s+(.+)$/gm, '<li class="li-ordered">$3</li>')

  text = text.replace(/((?:<li class="li-unordered">.*?<\/li>\s*)+)/g, (_match, items: string) => {
    const cleanItems = items.replace(/ class="li-unordered"/g, '')
    return `<ul>${cleanItems}</ul>`
  })
  text = text.replace(/((?:<li class="li-ordered">.*?<\/li>\s*)+)/g, (_match, items: string) => {
    const cleanItems = items.replace(/ class="li-ordered"/g, '')
    return `<ol>${cleanItems}</ol>`
  })

  // ---- 5. 段落 ----
  const lines = text.split('\n')
  const result: string[] = []
  let currentParagraph: string[] = []

  for (const line of lines) {
    const trimmed = line.trim()
    const isBlockElement = trimmed.match(/^<(h[1-6]|ul|ol|li|blockquote|hr|p|table)/)

    if (isBlockElement) {
      if (currentParagraph.length > 0) {
        const pText = currentParagraph.join(' ').trim()
        if (pText) result.push(`<p>${pText}</p>`)
        currentParagraph = []
      }
      result.push(line)
    } else if (trimmed === '') {
      if (currentParagraph.length > 0) {
        const pText = currentParagraph.join(' ').trim()
        if (pText) result.push(`<p>${pText}</p>`)
        currentParagraph = []
      }
    } else {
      currentParagraph.push(trimmed)
    }
  }
  if (currentParagraph.length > 0) {
    const pText = currentParagraph.join(' ').trim()
    if (pText) result.push(`<p>${pText}</p>`)
  }

  text = result.join('\n')

  // ---- 6. 行内格式 ----
  text = text.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
  text = text.replace(/__(.+?)__/g, '<strong>$1</strong>')
  text = text.replace(/(?<!\*)\*(?!\*)(.+?)(?<!\*)\*(?!\*)/g, '<em>$1</em>')
  text = text.replace(/`([^`]+)`/g, '<code>$1</code>')

  // ---- 7. 清理段落内多余的 <br> ----
  text = text.replace(/<p>(.*?)<\/p>/gs, (_match, inner: string) => {
    const cleaned = inner.replace(/<br>\s*$/, '')
    return `<p>${cleaned}</p>`
  })

  return text
}

// 计算属性：将消息内容经 Markdown 转换器渲染为 HTML
const formattedContent = computed(() => {
  const text = props.message.content
  if (!text) return ''
  return renderMarkdown(text)
})

// 从文件路径中提取文件名（去掉目录部分）
function getFileName(path: string): string {
  return path.split('/').pop() || path
}
</script>

<style scoped>
/* ===== 消息行布局：Flex 行，用户消息反向排列（靠右） ===== */
.message-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-bottom: 16px;
  padding: 0 16px;
}
.message-row.user { flex-direction: row-reverse; }

/* ===== 头像：AI 蓝色 / 用户灰色 ===== */
.avatar {
  width: 36px; height: 36px; border-radius: 50%;
  display: flex; align-items: center; justify-content: center; flex-shrink: 0;
}
.avatar-ai { background: #1a73e8; }
.avatar-user { background: #5f6368; }
.avatar-text { color: white; font-size: 12px; font-weight: 600; }

/* ===== 气泡主体：用户蓝色靠右 / AI 灰色靠左 ===== */
.bubble {
  max-width: 70%;
  padding: 12px 16px;
  border-radius: 16px;
  line-height: 1.6;
  font-size: 14px;
  word-break: break-word;
}
.bubble.user {
  background: #1a73e8; color: white;
  border-bottom-right-radius: 4px;
}
.bubble.assistant {
  background: #f1f3f4; color: #202124;
  border-bottom-left-radius: 4px;
}

.bubble-badge { margin-bottom: 6px; }

/* ==================== Markdown渲染样式 ==================== */
.bubble-text {
  word-break: break-word;
  overflow-wrap: break-word;
}

.bubble-text :deep(p) {
  margin: 0 0 8px 0;
  line-height: 1.7;
}
.bubble-text :deep(p:last-child) {
  margin-bottom: 0;
}

.bubble-text :deep(h1) {
  font-size: 18px; font-weight: 700; margin: 16px 0 8px 0;
  padding-bottom: 4px; border-bottom: 1px solid #dadce0;
}
.bubble-text :deep(h2) {
  font-size: 16px; font-weight: 700; margin: 14px 0 6px 0;
  padding-bottom: 3px; border-bottom: 1px solid #e8eaed;
}
.bubble-text :deep(h3) {
  font-size: 15px; font-weight: 600; margin: 12px 0 6px 0;
}
.bubble-text :deep(h4) {
  font-size: 14px; font-weight: 600; margin: 10px 0 4px 0;
}
.bubble-text :deep(h5), .bubble-text :deep(h6) {
  font-size: 13px; font-weight: 600; margin: 8px 0 4px 0;
}

.bubble-text :deep(ul), .bubble-text :deep(ol) {
  margin: 4px 0 8px 0;
  padding-left: 20px;
}
.bubble-text :deep(li) {
  margin-bottom: 2px;
  line-height: 1.6;
}

.bubble-text :deep(blockquote) {
  margin: 8px 0;
  padding: 4px 12px;
  border-left: 3px solid #1a73e8;
  background: rgba(26, 115, 232, 0.05);
  color: #5f6368;
  border-radius: 0 4px 4px 0;
}

.bubble-text :deep(code) {
  background: rgba(0, 0, 0, 0.06);
  padding: 1px 5px;
  border-radius: 3px;
  font-size: 13px;
  font-family: 'SF Mono', Menlo, Consolas, monospace;
}

.bubble-text :deep(hr) {
  border: none;
  border-top: 1px solid #dadce0;
  margin: 12px 0;
}

.bubble-text :deep(strong) { font-weight: 600; }
.bubble-text :deep(em) { font-style: italic; }

/* 用户气泡Markdown适配 */
.bubble.user .bubble-text :deep(blockquote) {
  border-left-color: rgba(255,255,255,0.6);
  background: rgba(255,255,255,0.1);
  color: rgba(255,255,255,0.9);
}
.bubble.user .bubble-text :deep(h1),
.bubble.user .bubble-text :deep(h2) {
  border-bottom-color: rgba(255,255,255,0.3);
}
.bubble.user .bubble-text :deep(code) {
  background: rgba(255,255,255,0.15);
}

/* ==================== 翻译进度条 ==================== */
.progress-area {
  margin-bottom: 10px;
}
.progress-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 6px;
}
.progress-step {
  font-size: 13px;
  color: #5f6368;
}
.progress-percent {
  font-size: 13px;
  font-weight: 600;
  color: #1a73e8;
  font-family: 'SF Mono', Menlo, monospace;
}
.progress-bar-bg {
  width: 100%;
  height: 6px;
  background: #e0e0e0;
  border-radius: 3px;
  overflow: hidden;
}
.progress-bar-fill {
  height: 100%;
  background: linear-gradient(90deg, #1a73e8, #4fc3f7);
  border-radius: 3px;
  transition: width 0.3s ease;
}

/* ==================== 翻译结果 ==================== */
.translation-results {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.lang-row { display: flex; gap: 8px; align-items: baseline; }
.lang-label { min-width: 56px; font-size: 12px; color: #5f6368; font-weight: 500; flex-shrink: 0; }
.lang-text { font-size: 14px; color: #202124; line-height: 1.5; white-space: pre-wrap; flex: 1; }

/* 来源标签 */
.source-badge {
  font-size: 11px; padding: 1px 6px; border-radius: 4px;
  font-weight: 500; flex-shrink: 0; white-space: nowrap;
}
.source-kb { background: #e8f5e9; color: #2e7d32; }
.source-model { background: #e3f2fd; color: #1565c0; }

/* 匹配模式指示 */
.translation-mode {
  display: flex; align-items: center; gap: 8px;
  padding-bottom: 6px; border-bottom: 1px solid #e8e8e8;
}
.mode-badge { font-size: 12px; padding: 2px 8px; border-radius: 4px; font-weight: 500; }
.mode-exact { background: #e8f5e9; color: #2e7d32; }
.mode-fuzzy { background: #fff3e0; color: #e65100; }
.mode-semantic { background: #f3e5f5; color: #6a1b9a; }
.mode-model { background: #e3f2fd; color: #1565c0; }
.mode-match-text { font-size: 12px; color: #9e9e9e; }

/* ==================== 匹配度报告 ==================== */
.match-report {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid #e0e0e0;
}
.report-title { font-size: 12px; font-weight: 600; color: #5f6368; margin-bottom: 6px; }
.report-grid { display: flex; flex-wrap: wrap; gap: 6px; }
.report-item {
  display: flex; align-items: center; gap: 4px;
  font-size: 11px; padding: 2px 6px; background: #fff; border-radius: 4px;
}
.report-status { font-size: 12px; }
.report-lang { color: #5f6368; }

/* ==================== ★ 图片预览 ==================== */
.image-preview {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.preview-img {
  max-width: 100%;
  max-height: 400px;
  border-radius: 10px;
  cursor: pointer;
  transition: transform 0.2s, box-shadow 0.2s;
  box-shadow: 0 2px 8px rgba(0,0,0,0.1);
}

.preview-img:hover {
  transform: scale(1.02);
  box-shadow: 0 4px 16px rgba(0,0,0,0.15);
}

.image-download-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 6px 14px;
  border-radius: 8px;
  background: #f1f3f4;
  color: #5f6368;
  font-size: 13px;
  text-decoration: none;
  transition: all 0.2s;
  align-self: flex-start;
}

.image-download-btn:hover {
  background: #e8eaed;
  color: #202124;
}

/* ==================== ★ 文件下载卡片 ==================== */
.file-downloads {
  margin-top: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* 下载卡片整体 */
.download-card {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border-radius: 12px;
  background: white;
  border: 1.5px solid #e0e0e0;
  transition: all 0.2s ease;
  cursor: default;
}

/* Word文档卡片 — 蓝色主题 */
.download-card-docx {
  border-color: #c2d7f2;
  background: linear-gradient(135deg, #f0f6ff 0%, #ffffff 100%);
}
.download-card-docx:hover {
  border-color: #1a73e8;
  box-shadow: 0 2px 12px rgba(26, 115, 232, 0.15);
}

/* Markdown文件卡片 — 灰色主题 */
.download-card-md {
  border-color: #e0e0e0;
  background: linear-gradient(135deg, #fafafa 0%, #ffffff 100%);
}
.download-card-md:hover {
  border-color: #888;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
}

/* 文件图标 */
.card-icon {
  flex-shrink: 0;
  width: 42px;
  height: 42px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
}

/* Word图标 — 蓝底白字W */
.icon-docx {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 42px;
  height: 42px;
  border-radius: 10px;
  background: #1a73e8;
  color: white;
  font-size: 20px;
  font-weight: 800;
  font-family: 'Segoe UI', 'SF Pro', sans-serif;
  letter-spacing: -1px;
}

/* ★ PPT图标 — 橙红色 */
.icon-pptx {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 42px;
  height: 42px;
  border-radius: 10px;
  background: #d4422e;
  color: white;
  font-size: 20px;
  font-weight: 800;
  font-family: 'Segoe UI', 'SF Pro', sans-serif;
  letter-spacing: -1px;
}

/* ★ Excel图标 — 绿色 */
.icon-xlsx {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 42px;
  height: 42px;
  border-radius: 10px;
  background: #1e8e3e;
  color: white;
  font-size: 20px;
  font-weight: 800;
  font-family: 'Segoe UI', 'SF Pro', sans-serif;
  letter-spacing: -1px;
}

/* 通用文件图标 */
.icon-file {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 42px;
  height: 42px;
  border-radius: 10px;
  background: #f5f5f5;
  font-size: 22px;
}

/* 文件信息 */
.card-info {
  flex: 1;
  min-width: 0;
}

.card-filename {
  font-size: 14px;
  font-weight: 600;
  color: #202124;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.card-meta {
  font-size: 12px;
  color: #5f6368;
  margin-top: 2px;
}

/* 下载按钮 */
.card-btn {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  border-radius: 8px;
  background: #1a73e8;
  color: white;
  display: flex;
  align-items: center;
  justify-content: center;
  text-decoration: none;
  font-size: 16px;
  transition: all 0.2s ease;
}
.card-btn:hover {
  background: #1558b0;
  transform: scale(1.08);
}

/* ==================== 移动端适配 ==================== */
.msg-mobile .bubble { max-width: 85%; }
.msg-mobile .lang-label { min-width: 48px; font-size: 11px; }
.msg-mobile .lang-text { font-size: 13px; }
.msg-mobile .avatar { width: 30px; height: 30px; }
.msg-mobile .avatar-text { font-size: 10px; }
.msg-mobile .message-row { gap: 6px; padding: 0 10px; margin-bottom: 10px; }
.msg-mobile .progress-step { font-size: 12px; }
.msg-mobile .progress-percent { font-size: 12px; }
.msg-mobile .bubble-text :deep(h1) { font-size: 16px; }
.msg-mobile .bubble-text :deep(h2) { font-size: 15px; }
.msg-mobile .bubble-text :deep(h3) { font-size: 14px; }

/* 移动端下载卡片适配 */
.msg-mobile .download-card {
  padding: 10px 12px;
  gap: 10px;
}
.msg-mobile .icon-docx,
.msg-mobile .icon-file {
  width: 36px;
  height: 36px;
  font-size: 18px;
}
.msg-mobile .card-filename { font-size: 13px; }
.msg-mobile .card-meta { font-size: 11px; }
.msg-mobile .card-btn {
  width: 32px;
  height: 32px;
  font-size: 14px;
}
</style>
