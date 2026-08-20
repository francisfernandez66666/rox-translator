// ============================================================================
// components/ChatWindow.vue — 翻译聊天窗口（对话式 + 文件上传 + 语言选择 + 进度条）
// ============================================================================
// 【功能】
//   - 顶部：🌐 翻译 + 清空按钮
//   - 中部：对话消息列表（含翻译进度条）
//   - 首次进入：欢迎语 + 示例问题气泡
//   - 底部输入栏：📎上传 + 🌐语言选择 + 输入框 + 发送
// ★ "其他语言"统一为一个选项，用户在输入框指定语言，后端解析prompt提取语言后走模型翻译
// ============================================================================

<template>
  <!-- ===== 聊天窗口整体容器（响应式移动端样式） ===== -->
  <div class="chat-window" :class="{ 'chat-mobile': isMobile }">
    <!-- ===== 顶部标题栏 ===== -->
    <header class="chat-header" :class="{ 'chat-mobile-header': isMobile }" style="border-bottom-color: #2e7d32;">
      <span>{{ t('chat.title') }}</span>
      <button class="clear-btn" @click="store.clearMessages()" :title="t('chat.clearChat')">
        🗑️
      </button>
    </header>

    <!-- ===== 离线提示条 ===== -->
    <div v-if="!store.isBackendOnline" class="offline-bar">
      <span>{{ t('chat.offline') }}</span>
      <button
        class="retry-btn"
        :disabled="store.isBackendChecking"
        @click="store.retryBackend()"
      >
        {{ store.isBackendChecking ? t('chat.checking') : t('chat.retry') }}
      </button>
    </div>

    <!-- ===== 消息列表区域 ===== -->
    <div class="messages-area" ref="messagesContainer">
      <!-- ===== 空状态：没有消息时显示欢迎页 + 示例问题 ===== -->
      <div v-if="store.messages.length === 0" class="empty-state">
        <div class="empty-icon">{{ skillConfig.icon }}</div>
        <div class="empty-title">{{ skillConfig.welcome }}</div>
        <div class="example-list">
          <button
            v-for="(ex, idx) in skillConfig.examples"
            :key="idx"
            class="example-bubble"
            @click="store.sendExample(ex)"
          >
            {{ ex }}
          </button>
        </div>
      </div>

      <!-- ===== 对话消息列表（渲染每条消息气泡） ===== -->
      <MessageBubble
        v-for="msg in store.messages"
        :key="msg.id"
        :message="msg"
      />

      <!-- ===== 加载动画（非翻译进度时显示"正在翻译"点动画） ===== -->
      <div v-if="store.isLoading && !hasActiveProgress" class="loading-indicator">
        <div class="loading-dots">
          <span></span><span></span><span></span>
        </div>
        <span class="loading-text">{{ loadingText }}</span>
      </div>
    </div>

    <!-- ===== 底部输入区域 ===== -->
    <div class="input-area" :class="{ 'input-mobile': isMobile }">
      <!-- ===== 已选文件 / 语言标签行（可逐个移除） ===== -->
      <div v-if="attachedFiles.length > 0 || store.selectedLangs.length > 0" class="tags-row">
        <span
          v-for="(f, idx) in attachedFiles"
          :key="'f'+idx"
          class="tag tag-file"
        >
          📄 {{ f.name }}
          <button class="tag-remove" @click="removeFile(idx)">✕</button>
        </span>
        <!-- ★ KB语言标签（只显示LANG_OPTIONS中有的语言） -->
        <span
          v-for="lang in store.selectedLangs.filter(l => l !== 'other' && LANG_OPTIONS[l])"
          :key="'l'+lang"
          class="tag tag-lang"
        >
{{ LANG_OPTIONS[lang]?.flag || '🌐' }} {{ langLabel(lang, LANG_OPTIONS[lang]?.label) }}
          <button class="tag-remove" @click="toggleLang(lang)">✕</button>
        </span>
        <!-- ★ 非KB语言标签（如 ja/ko/th 等直接勾选的） -->
        <span
          v-for="lang in store.selectedLangs.filter(l => l !== 'other' && !LANG_OPTIONS[l])"
          :key="'ol'+lang"
          class="tag tag-other-lang"
        >
          🤖 {{ langLabel(lang, OTHER_LANG_OPTIONS[lang]?.label) }}
          <button class="tag-remove" @click="toggleLang(lang)">✕</button>
        </span>
        <!-- ★ "其他语言"手写标签（自定义语言名，不在任何列表中的） -->
        <span
          v-if="store.selectedLangs.includes('other')"
          class="tag tag-other-lang"
        >
          🤖 {{ t('chat.otherTag') }}
          <button class="tag-remove" @click="toggleLang('other')">✕</button>
        </span>
        <!-- ★ 自定义语言标签（从"更多语言"输入框添加的） -->
        <span
          v-for="(clang, idx) in customLangs"
          :key="'cl'+idx"
          class="tag tag-other-lang"
        >
          🤖 {{ clang }}
          <button class="tag-remove" @click="customLangs.splice(idx, 1)">✕</button>
        </span>
      </div>

      <!-- ===== 输入行：上传 / 语言选择 / 文本框 / 导入 / 发送 ===== -->
      <div class="input-row">
        <div class="input-actions">
          <!-- ===== 文件上传按钮（docx/pptx/xlsx） ===== -->
          <button
            class="action-btn"
            :title="t('chat.uploadFile')"
            @click="triggerFileUpload"
          >
            ＋
          </button>
          <input
            ref="fileInputRef"
            type="file"
            accept=".docx,.pptx,.xlsx"
            style="display:none"
            @change="handleFileSelect"
          />

          <!-- ===== 语言选择下拉（知识库语言 + 其他 AI 翻译语言） ===== -->
          <div class="lang-selector" :class="{ open: langDropdownOpen }">
            <button class="action-btn lang-btn" @click="langDropdownOpen = !langDropdownOpen" :title="t('chat.selectTargetLang')">
              🌐 {{ langBtnLabel }}
            </button>
            <div v-if="langDropdownOpen" class="lang-dropdown">
              <!-- ★ 源语言选择（互译方向；auto=自动检测） -->
              <div class="lang-section-title">{{ t('chat.sourceLang') }}</div>
              <div class="source-lang-row">
                <button
                  v-for="opt in sourceLangOptions"
                  :key="opt.code"
                  class="source-lang-btn"
                  :class="{ active: sourceLang === opt.code }"
                  @click="sourceLang = opt.code"
                >{{ opt.flag }} {{ t(opt.labelKey) }}</button>
              </div>
              <div class="lang-divider"></div>
              <!-- ★ 知识库语言区 -->
              <div class="lang-section-title">{{ t('chat.kbLangs') }}</div>
              <label
                v-for="lang in kbLangs"
                :key="lang"
                class="lang-option"
              >
                <input
                  type="checkbox"
                  :checked="store.selectedLangs.includes(lang)"
                  @change="toggleLang(lang)"
                />
                <span>{{ LANG_OPTIONS[lang]?.flag || '🌐' }} {{ LANG_OPTIONS[lang]?.label || lang }}</span>
              </label>

              <div class="lang-divider"></div>

              <!-- ★ 其他语言：子选单直接勾选 + 手写兜底 -->
              <div class="lang-section-title">{{ t('chat.otherLangs') }}</div>
              <label
                v-for="olang in otherLangList"
                :key="olang.code"
                class="lang-option lang-option-other"
              >
                <input
                  type="checkbox"
                  :checked="store.selectedLangs.includes(olang.code)"
                  @change="toggleLang(olang.code)"
                />
                <span>{{ olang.flag }} {{ langLabel(olang.code, olang.label) }}</span>
              </label>
              <div class="lang-divider" style="margin: 4px 0"></div>
              <label class="lang-option lang-option-other">
                <input
                  type="checkbox"
                  :checked="store.selectedLangs.includes('other')"
                  @change="toggleLang('other')"
                />
                <span>{{ t('chat.moreLangs') }}</span>
              </label>
              <!-- ★ "更多语言"输入框：选了"更多语言"后展开，用户直接输入语言名 -->
              <div v-if="store.selectedLangs.includes('other')" class="lang-custom-input-wrap">
                <input
                  ref="customLangInputRef"
                  v-model="customLangText"
                  class="lang-custom-input"
                  :placeholder="t('chat.customLangPlaceholder')"
                  @keydown.enter.prevent="addCustomLang"
                />
                <button class="lang-custom-add-btn" @click="addCustomLang" :disabled="!customLangText.trim()">＋</button>
              </div>
            </div>
          </div>
        </div>

        <textarea
          ref="inputRef"
          v-model="inputText"
          class="message-input"
          :placeholder="inputPlaceholder"
          rows="1"
          @keydown.enter.exact="handleSend"
          @input="autoResizeInput"
          @focus="langDropdownOpen = false"
        ></textarea>

        <!-- 📚 导入新翻译按钮 -->
        <button
          class="action-btn kb-upload-btn"
          :title="t('chat.importKb')"
          @click="resetKbModal(); showKbModal = true"
        >
          📚
        </button>

        <button
          v-if="store.isLoading"
          class="send-btn stop-btn"
          :style="{ background: '#d93025' }"
          @click="store.stopGeneration()"
          :title="t('chat.stop')"
        >
          ■
        </button>
        <button
          v-else
          class="send-btn"
          :disabled="!canSend"
          :style="{ background: canSend ? '#2e7d32' : '#e0e0e0' }"
          @click="handleSend"
          :title="t('chat.send')"
        >
          ➤
        </button>
      </div>
    </div>

    <!-- ===== 📚 导入新翻译弹窗 ===== -->
    <div v-if="showKbModal" class="kb-modal-overlay" @click.self="showKbModal = false">
      <div class="kb-modal">
        <div class="kb-modal-header">
          <h3>{{ t('chat.importKbTitle') }}</h3>
          <button class="kb-modal-close" @click="showKbModal = false">✕</button>
        </div>

        <div class="kb-modal-body">
          <!-- 第1步：上传文件 -->
          <div class="kb-step" :class="{ active: kbStep === 1, done: kbStep > 1 }">
            <div class="kb-step-header">
              <span class="kb-step-num">1</span>
              <span class="kb-step-title">{{ t('chat.kbStep1') }}</span>
              <span v-if="kbStep > 1" class="kb-step-check">✅</span>
            </div>
            <div class="kb-step-content" v-if="kbStep >= 1">
              <div
                class="kb-drop-zone"
                :class="{ dragging: kbDragging, 'has-file': kbFile }"
                @dragover.prevent="kbDragging = true"
                @dragleave.prevent="kbDragging = false"
                @drop.prevent="handleKbDrop"
                @click="triggerKbFileInput"
              >
                <input ref="kbFileInputRef" type="file" accept=".csv,.xlsx,.xls" style="display:none" @change="handleKbFileSelect" />
                <template v-if="!kbFile">
                  <div class="kb-drop-icon">📄</div>
                  <div>{{ kbDragging ? t('chat.kbDropRelease') : t('chat.kbDropClick') }}</div>
                  <div class="kb-drop-hint">{{ t('chat.kbDropHint') }}</div>
                </template>
                <template v-else>
                  <div class="kb-file-info">📎 {{ kbFile.name }}</div>
                  <button class="kb-file-remove" @click.stop="kbFile = null; kbRecognized = null">{{ t('chat.kbRemove') }}</button>
                </template>
              </div>
            </div>
          </div>

          <!-- 第2步：识别数据 -->
          <div class="kb-step" :class="{ active: kbStep === 2, done: kbStep > 2, disabled: kbStep < 2 }">
            <div class="kb-step-header">
              <span class="kb-step-num">2</span>
              <span class="kb-step-title">{{ t('chat.kbStep2') }}</span>
              <span v-if="kbStep > 2" class="kb-step-check">✅</span>
            </div>
            <div class="kb-step-content" v-if="kbStep >= 2">
              <div v-if="kbRecognizing" class="kb-progress">
                <div class="kb-progress-bar">
                  <div class="kb-progress-fill kb-progress-animated"></div>
                </div>
                <div class="kb-progress-text">{{ t('chat.kbRecognizing') }}</div>
              </div>
              <template v-else-if="kbRecognized">
                <div class="kb-recognized-info">
                  <div class="kb-recognized-stat">
                    {{ tpl('chat.kbTotal', { total: kbRecognized.total, n: kbRecognized.lang_cols?.length || 0 }) }}
                  </div>
                  <div class="kb-recognized-langs">
                    {{ t('chat.kbLangCols') }}<span v-for="lang in kbRecognized.lang_cols" :key="lang" class="kb-lang-tag">{{ lang }}</span>
                  </div>
                  <div v-if="kbRecognized.new_langs?.length" class="kb-recognized-new">
                    {{ t('chat.kbNewLangs') }}<span v-for="lang in kbRecognized.new_langs" :key="lang" class="kb-lang-tag new">{{ lang }}</span>
                  </div>
                  <!-- 预览表 -->
                  <div class="kb-preview" v-if="kbRecognized.preview?.length">
                    <div class="kb-preview-title">{{ tpl('chat.kbPreview', { n: kbRecognized.preview.length }) }}</div>
                    <div class="kb-preview-table">
                      <div class="kb-preview-row kb-preview-header">
                        <span>{{ t('chat.kbChinese') }}</span><span v-for="lang in kbRecognized.lang_cols" :key="lang">{{ lang }}</span>
                      </div>
                      <div class="kb-preview-row" v-for="(row, idx) in kbRecognized.preview" :key="idx">
                        <span>{{ row.zh }}</span>
                        <span v-for="lang in kbRecognized.lang_cols" :key="lang">{{ row[lang] || '-' }}</span>
                      </div>
                    </div>
                  </div>
                </div>
                <button class="kb-action-btn" @click="startRecognize" :disabled="true">
                  {{ t('chat.kbRecognized') }}
                </button>
              </template>
              <button v-else class="kb-action-btn" @click="startRecognize" :disabled="!kbFile">
                {{ t('chat.kbRecognize') }}
              </button>
            </div>
          </div>

          <!-- 第3步：导入数据 -->
          <div class="kb-step" :class="{ active: kbStep === 3, disabled: kbStep < 3 }">
            <div class="kb-step-header">
              <span class="kb-step-num">3</span>
              <span class="kb-step-title">{{ t('chat.kbStep3') }}</span>
            </div>
            <div class="kb-step-content" v-if="kbStep >= 3">
              <div v-if="kbImporting" class="kb-progress">
                <div class="kb-progress-bar">
                  <div class="kb-progress-fill kb-progress-animated" :style="{ width: kbImportProgress + '%' }"></div>
                </div>
                <div class="kb-progress-text">{{ kbImportProgress < 50 ? t('chat.kbWriting') : t('chat.kbIndexing') }} {{ kbImportProgress }}%</div>
              </div>
              <template v-else-if="kbImportResult">
                <div :class="kbImportResult.success ? 'kb-result-success' : 'kb-result-error'">
                  {{ kbImportResult.message }}
                </div>
              </template>
              <button v-else class="kb-action-btn primary" @click="startImport" :disabled="!kbRecognized?.temp_id">
                {{ t('chat.kbImport') }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>

  </div>
</template>

<script setup lang="ts">
// Vue 核心组合式 API（响应式、计算、DOM 更新、监听、生命周期）
import { ref, computed, nextTick, watch, onMounted, onUnmounted } from 'vue'
// 全局聊天状态 Store
import { useChatStore } from '@/stores/chat'
// 国际化取词
import { t, tpl } from '@/i18n'
// 子组件：消息气泡
import MessageBubble from './MessageBubble.vue'

// 全局聊天 Store 实例
const store = useChatStore()

// ---- 技能配置 ----
const skillConfig = computed(() => ({
  icon: '🌐',
  label: t('chat.skillLabel'),
  color: '#2e7d32',
  placeholder: t('chat.placeholder'),
  welcome: t('chat.welcome'),
  examples: [t('chat.example1'), t('chat.example2'), t('chat.example3'), t('chat.example4')]
}))

// ---- 本地状态 ----
const inputText = ref('')
const messagesContainer = ref<HTMLElement>()
const inputRef = ref<HTMLInputElement>()
const fileInputRef = ref<HTMLInputElement>()
// 语言下拉框是否展开（点击切换显隐）
const langDropdownOpen = ref(false)
// ★ "更多语言"自定义输入
const customLangText = ref('')
const customLangInputRef = ref<HTMLInputElement>()
// ★ 已添加的自定义语言列表（语言名数组，发送时拼入消息）
const customLangs = ref<string[]>([])
// ★ 源语言（互译方向；auto=自动检测，发送时非 auto 则带上 source_lang）
const sourceLang = ref('auto')
const sourceLangOptions = [
  { code: 'auto', flag: '🤖', labelKey: 'chat.sourceAuto' },
  { code: 'zh', flag: '🇨🇳', labelKey: 'lang.zh' },
  { code: 'en', flag: '🇬🇧', labelKey: 'lang.en' },
  { code: 'zh_hant', flag: '🇹🇼', labelKey: 'lang.zhHant' },
]

// ★ 语言名→代码的本地映射（常见语言中文名/英文名→ISO代码）
const _LANG_NAME_TO_CODE: Record<string, string> = {
  '日语': 'ja', '日本語': 'ja', 'japanese': 'ja', 'ja': 'ja',
  '韩语': 'ko', '朝鲜语': 'ko', 'korean': 'ko', 'ko': 'ko',
  '泰语': 'th', 'thai': 'th', 'th': 'th',
  '越南语': 'vi', 'vietnamese': 'vi', 'vi': 'vi',
  '蒙语': 'mn', '蒙古语': 'mn', 'mongolian': 'mn', 'mn': 'mn',
  '马来语': 'ms', 'malay': 'ms', 'ms': 'ms',
  '印尼语': 'id', '印度尼西亚语': 'id', 'indonesian': 'id', 'id': 'id',
  '意大利语': 'it', 'italian': 'it', 'it': 'it',
  '波兰语': 'pl', 'polish': 'pl', 'pl': 'pl',
  '荷兰语': 'nl', 'dutch': 'nl', 'nl': 'nl',
  '瑞典语': 'sv', 'swedish': 'sv', 'sv': 'sv',
  '乌克兰语': 'uk', 'ukrainian': 'uk', 'uk': 'uk',
  '土耳其语': 'tr', 'turkish': 'tr', 'tr': 'tr',
  '印地语': 'hi', 'hindi': 'hi', 'hi': 'hi',
  '波斯语': 'fa', 'iranian': 'fa', 'fa': 'fa',
  '希伯来语': 'he', 'hebrew': 'he', 'he': 'he',
  '希腊语': 'el', 'greek': 'el', 'el': 'el',
  '缅甸语': 'my', 'burmese': 'my', 'my': 'my',
  '柬埔寨语': 'km', 'khmer': 'km', 'km': 'km',
  '老挝语': 'lo', 'lao': 'lo', 'lo': 'lo',
  '僧伽罗语': 'si', 'sinhala': 'si', 'si': 'si',
  '捷克语': 'cs', 'czech': 'cs', 'cs': 'cs',
  '罗马尼亚语': 'ro', 'romanian': 'ro', 'ro': 'ro',
  '匈牙利语': 'hu', 'hungarian': 'hu', 'hu': 'hu',
  '芬兰语': 'fi', 'finnish': 'fi', 'fi': 'fi',
  '丹麦语': 'da', 'danish': 'da', 'da': 'da',
  '挪威语': 'no', 'norwegian': 'no', 'no': 'no',
  '斯洛伐克语': 'sk', 'slovak': 'sk', 'sk': 'sk',
  '保加利亚语': 'bg', 'bulgarian': 'bg', 'bg': 'bg',
  '克罗地亚语': 'hr', 'croatian': 'hr', 'hr': 'hr',
  '塞尔维亚语': 'sr', 'serbian': 'sr', 'sr': 'sr',
  '斯洛文尼亚语': 'sl', 'slovenian': 'sl', 'sl': 'sl',
  '立陶宛语': 'lt', 'lithuanian': 'lt', 'lt': 'lt',
  '拉脱维亚语': 'lv', 'latvian': 'lv', 'lv': 'lv',
  '爱沙尼亚语': 'et', 'estonian': 'et', 'et': 'et',
  '冰岛语': 'is', 'icelandic': 'is', 'is': 'is',
  '加泰罗尼亚语': 'ca', 'catalan': 'ca', 'ca': 'ca',
  '巴斯克语': 'eu', 'basque': 'eu', 'eu': 'eu',
  '威尔士语': 'cy', 'welsh': 'cy', 'cy': 'cy',
  '乌尔都语': 'ur', 'urdu': 'ur', 'ur': 'ur',
  '孟加拉语': 'bn', 'bengali': 'bn', 'bn': 'bn',
  '泰米尔语': 'ta', 'tamil': 'ta', 'ta': 'ta',
  '旁遮普语': 'pa', 'punjabi': 'pa', 'pa': 'pa',
  '马拉地语': 'mr', 'marathi': 'mr', 'mr': 'mr',
  '尼泊尔语': 'ne', 'nepali': 'ne', 'ne': 'ne',
  '斯瓦希里语': 'sw', 'swahili': 'sw', 'sw': 'sw',
  '阿姆哈拉语': 'am', 'amharic': 'am', 'am': 'am',
  '祖鲁语': 'zu', 'zulu': 'zu', 'zu': 'zu',
  '豪萨语': 'ha', 'hausa': 'ha', 'ha': 'ha',
  '格鲁吉亚语': 'ka', 'georgian': 'ka', 'ka': 'ka',
  '亚美尼亚语': 'hy', 'armenian': 'hy', 'hy': 'hy',
  '阿塞拜疆语': 'az', 'azerbaijani': 'az', 'az': 'az',
  '乌兹别克语': 'uz', 'uzbek': 'uz', 'uz': 'uz',
  '哈萨克语': 'kk', 'kazakh': 'kk', 'kk': 'kk',
  '吉尔吉斯语': 'ky', 'kyrgyz': 'ky', 'ky': 'ky',
  '塔吉克语': 'tg', 'tajik': 'tg', 'tg': 'tg',
  '土库曼语': 'tk', 'turkmen': 'tk', 'tk': 'tk',
  '波斯尼亚语': 'bs', 'bosnian': 'bs', 'bs': 'bs',
  '阿尔巴尼亚语': 'sq', 'albanian': 'sq', 'sq': 'sq',
  '马其顿语': 'mk', 'macedonian': 'mk', 'mk': 'mk',
  '黑山语': 'sr-me', 'montenegrin': 'sr-me',
  '马耳他语': 'mt', 'maltese': 'mt', 'mt': 'mt',
  '爱尔兰语': 'ga', 'irish': 'ga', 'ga': 'ga',
  '苏格兰盖尔语': 'gd', 'scottish gaelic': 'gd', 'gd': 'gd',
  '菲律宾语': 'fil', 'filipino': 'fil', 'fil': 'fil',
  '爪哇语': 'jv', 'javanese': 'jv', 'jv': 'jv',
  '信德语': 'sd', 'sindhi': 'sd', 'sd': 'sd',
  '卡纳达语': 'kn', 'kannada': 'kn', 'kn': 'kn',
  '马拉雅拉姆语': 'ml', 'malayalam': 'ml', 'ml': 'ml',
  '泰卢固语': 'te', 'telugu': 'te', 'te': 'te',
  '奥里亚语': 'or', 'oriya': 'or', 'or': 'or',
  '古吉拉特语': 'gu', 'gujarati': 'gu', 'gu': 'gu',
  '库尔德语': 'ku', 'kurdish': 'ku', 'ku': 'ku',
  '普什图语': 'ps', 'pashto': 'ps', 'ps': 'ps',
  '达里语': 'fa-af', 'dari': 'fa-af',
}

/** ★ 添加自定义语言：输入框回车或点+号 */
function addCustomLang() {
  const raw = customLangText.value.trim()
  if (!raw) return
  // 支持顿号/逗号分隔多语言
  const parts = raw.split(/[、，,\s]+/).map(s => s.trim()).filter(Boolean)
  for (const part of parts) {
    const key = part.toLowerCase()
    const code = _LANG_NAME_TO_CODE[key] || _LANG_NAME_TO_CODE[part]
    if (code && !store.selectedLangs.includes(code)) {
      // 已知语言代码 → 直接加到 selectedLangs（文件/文本翻译都会带上）
      store.selectedLangs.push(code)
    } else if (!code) {
      // 未知语言 → 存入 customLangs，发送时拼入消息让后端解析
      if (!customLangs.value.includes(part)) {
        customLangs.value.push(part)
      }
    }
  }
  customLangText.value = ''
}
// 已附加的待翻译文件列表
const attachedFiles = ref<File[]>([])
const kbFileInputRef = ref<HTMLInputElement>()
// ★ KB导入弹窗状态
const showKbModal = ref(false)
const kbStep = ref(1)           // 当前步骤 1/2/3
const kbFile = ref<File | null>(null)
// 拖拽文件悬停标记（高亮上传区）
const kbDragging = ref(false)
// 识别中状态（控制进度条）
const kbRecognizing = ref(false)
const kbRecognized = ref<any>(null)  // 识别结果
// 导入中状态（控制进度条与按钮禁用）
const kbImporting = ref(false)
// 导入进度（0-100，前端模拟推进）
const kbImportProgress = ref(0)
// 导入结果（成功/失败提示文案）
const kbImportResult = ref<any>(null)

// ---- 语言配置 ----
// ★ KB 语言从后端动态获取
const kbLangs = ref<string[]>(['en', 'ru', 'ar', 'es', 'pt', 'fr', 'kk', 'de', 'zh_hant'])

// ★ 语言名称和国旗映射（本地兜底，后端返回后覆盖）
const LANG_OPTIONS: Record<string, { label: string; flag: string }> = {
  en:  { label: '英语',     flag: '🇬🇧' },
  ru:  { label: '俄语',     flag: '🇷🇺' },
  ar:  { label: '阿拉伯语', flag: '🇸🇦' },
  es:  { label: '西班牙语', flag: '🇪🇸' },
  pt:  { label: '葡萄牙语', flag: '🇵🇹' },
  fr:  { label: '法语',     flag: '🇫🇷' },
  kk:  { label: '哈萨克语', flag: '🇰🇿' },
  de:  { label: '德语',     flag: '🇩🇪' },
  zh_hant: { label: '繁体中文', flag: '🇹🇼' },
}

// ★ "其他语言"子选单（非KB语言，AI翻译，直接勾选无需手写提示词）
const OTHER_LANG_OPTIONS = ref<Record<string, { label: string; flag: string }>>({
  zh:  { label: '中文',     flag: '🇨🇳' },
  ja:  { label: '日语',     flag: '🇯🇵' },
  ko:  { label: '韩语',     flag: '🇰🇷' },
  th:  { label: '泰语',     flag: '🇹🇭' },
  vi:  { label: '越南语',   flag: '🇻🇳' },
  mn:  { label: '蒙语',     flag: '🇲🇳' },
  ms:  { label: '马来语',   flag: '🇲🇾' },
  id:  { label: '印尼语',   flag: '🇮🇩' },
  it:  { label: '意大利语', flag: '🇮🇹' },
  pl:  { label: '波兰语',   flag: '🇵🇱' },
  nl:  { label: '荷兰语',   flag: '🇳🇱' },
  sv:  { label: '瑞典语',   flag: '🇸🇪' },
  uk:  { label: '乌克兰语', flag: '🇺🇦' },
  tr:  { label: '土耳其语', flag: '🇹🇷' },
  hi:  { label: '印地语',   flag: '🇮🇳' },
  fa:  { label: '波斯语',   flag: '🇮🇷' },
  he:  { label: '希伯来语', flag: '🇮🇱' },
  el:  { label: '希腊语',   flag: '🇬🇷' },
  my:  { label: '缅甸语',   flag: '🇲🇲' },
  km:  { label: '柬埔寨语', flag: '🇰🇭' },
  lo:  { label: '老挝语',   flag: '🇱🇦' },
  tl:  { label: '菲律宾语', flag: '🇵🇭' },
  gu:  { label: '古吉拉特语', flag: '🇮🇳' },
  ur:  { label: '乌尔都语', flag: '🇵🇰' },
  te:  { label: '泰卢固语', flag: '🇮🇳' },
  mr:  { label: '马拉地语', flag: '🇮🇳' },
  bn:  { label: '孟加拉语', flag: '🇧🇩' },
  ta:  { label: '泰米尔语', flag: '🇮🇳' },
  bo:  { label: '藏语',     flag: '🇨🇳' },
  ug:  { label: '维吾尔语', flag: '🇨🇳' },
  yue: { label: '粤语',     flag: '🇨🇳' },
})

// ★ 扁平化的其他语言列表（供 v-for 遍历，响应式）
const otherLangList = computed(() =>
  Object.entries(OTHER_LANG_OPTIONS.value).map(([code, info]) => ({ code, ...info }))
)

// ★ 语言显示名：优先 i18n（lang.<code>），缺失回退本地 label
function langLabel(code: string, fallback?: string): string {
  const v = t(`lang.${code}`)
  return v !== `lang.${code}` ? v : (fallback || code)
}

// ★ 语言按钮标签
const langBtnLabel = computed(() => {
  const sel = store.selectedLangs
  if (sel.length === 0) return t('chat.langZero')
  if (sel.length <= 2) {
    // 1-2种语言直接显示名称
    const names = sel.map(l => {
      if (l === 'other') return t('chat.langOther')
      return langLabel(l, LANG_OPTIONS[l]?.label || OTHER_LANG_OPTIONS.value[l]?.label)
    })
    return names.join('+')
  }
  // 3种以上只显示数量
  return tpl('chat.langCount', { n: sel.length })
})

// ---- 响应式判断 ----
const isMobile = ref(window.innerWidth <= 768)

// onResize 监听窗口尺寸变化，实时更新移动端标记（窄屏时折叠侧栏）。
// 无参数无返回，由窗口 resize 事件触发。
function onResize() { isMobile.value = window.innerWidth <= 768 }

const inputPlaceholder = computed(() => {
  if (attachedFiles.value.length > 0) return t('chat.sendFile')
  return skillConfig.value.placeholder
})

const canSend = computed(() => {
  return inputText.value.trim().length > 0 || attachedFiles.value.length > 0
})

// ---- 是否有正在进行的翻译进度 ----
const hasActiveProgress = computed(() => {
  return store.messages.some(m => m.progress && m.progress.percent < 100)
})

// ---- 加载提示 ----
const loadingText = computed(() => {
  return attachedFiles.value.length > 0 ? t('chat.translatingFile') : t('chat.translating')
})

// ---- 文件上传 ----
function triggerFileUpload() { fileInputRef.value?.click() }

// handleFileSelect 处理文件选择事件：将选中的文件加入待翻译列表（按文件名去重）。
// 参数 e: 文件输入框 change 事件；无返回。
function handleFileSelect(e: Event) {
  const target = e.target as HTMLInputElement
  if (!target.files) return
  for (const file of Array.from(target.files)) {
    if (!attachedFiles.value.find(f => f.name === file.name)) {
      attachedFiles.value.push(file)
    }
  }
  target.value = ''
}

// removeFile 从待翻译列表中移除指定下标的文件。
// 参数 idx: 文件下标；无返回。
function removeFile(idx: number) { attachedFiles.value.splice(idx, 1) }

// ---- KB知识库导入弹窗 ----

/** ★ 重置弹窗状态 */
function resetKbModal() {
  kbStep.value = 1
  kbFile.value = null
  kbDragging.value = false
  kbRecognizing.value = false
  kbRecognized.value = null
  kbImporting.value = false
  kbImportProgress.value = 0
  kbImportResult.value = null
}

/** ★ 打开文件选择 */
function triggerKbFileInput() { kbFileInputRef.value?.click() }

/** ★ 文件选择回调 */
function handleKbFileSelect(e: Event) {
  const target = e.target as HTMLInputElement
  if (target.files?.[0]) {
    kbFile.value = target.files[0]
    kbRecognized.value = null
    kbStep.value = 2
  }
  target.value = ''
}

/** ★ 拖拽上传回调 */
function handleKbDrop(e: DragEvent) {
  kbDragging.value = false
  const file = e.dataTransfer?.files?.[0]
  if (!file) return
  const ext = file.name.split('.').pop()?.toLowerCase()
  if (!['csv', 'xlsx', 'xls'].includes(ext || '')) {
    alert(t('chat.kbOnlyCsv'))
    return
  }
  kbFile.value = file
  kbRecognized.value = null
  kbStep.value = 2
}

/** ★ 第2步：识别数据 */
async function startRecognize() {
  if (!kbFile.value) return
  kbRecognizing.value = true
  kbRecognized.value = null
  try {
    const formData = new FormData()
    formData.append('file', kbFile.value)
    const resp = await fetch('/api/translation/recognize-kb', {
      method: 'POST',
      body: formData,
    })
    const data = await resp.json()
    if (data.success) {
      kbRecognized.value = data
      kbStep.value = 3
    } else {
      alert(data.message || t('chat.kbRecognizeFailed'))
    }
  } catch (err: any) {
    alert(tpl('chat.kbRecognizeErr', { msg: err.message || t('chat.kbNetworkErr') }))
  } finally {
    kbRecognizing.value = false
  }
}

/** ★ 第3步：导入数据 */
// startImport 第3步：导入已识别的知识库数据（含模拟进度条与结果提示）。
// 无参数无返回；导入成功后刷新知识库统计并提示导入结果。
async function startImport() {
  if (!kbRecognized.value?.temp_id) return
  kbImporting.value = true
  kbImportProgress.value = 0
  kbImportResult.value = null

  // ★ 模拟进度（后端import是同步的，先快进到80%等返回）
  const progressTimer = setInterval(() => {
    if (kbImportProgress.value < 80) {
      kbImportProgress.value += Math.random() * 15
      if (kbImportProgress.value > 80) kbImportProgress.value = 80
    }
  }, 300)

  try {
    const resp = await fetch('/api/translation/import-kb', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ temp_id: kbRecognized.value.temp_id }),
    })
    const data = await resp.json()

    clearInterval(progressTimer)
    kbImportProgress.value = 100

    kbImportResult.value = data
    if (data.success) {
      await loadTranslationLangs()
    }
  } catch (err: any) {
    clearInterval(progressTimer)
    kbImportProgress.value = 0
    kbImportResult.value = { success: false, message: tpl('chat.kbImportErr', { msg: err.message || t('chat.kbNetworkErr') }) }
  } finally {
    kbImporting.value = false
  }
}

// ---- 输入框自动调整高度（textarea多行） ----
function autoResizeInput() {
  const el = inputRef.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 120) + 'px'
}

// ---- 语言选择 ----
function toggleLang(lang: string) {
  const idx = store.selectedLangs.indexOf(lang)
  if (idx >= 0) {
    // 至少保留一个语言
    if (store.selectedLangs.length > 1) store.selectedLangs.splice(idx, 1)
  } else {
    store.selectedLangs.push(lang)
  }
}

// ---- 发送消息 ----
async function handleSend(e?: Event) {
  if (e instanceof KeyboardEvent && !e.shiftKey) {
    e.preventDefault()
  } else if (e instanceof KeyboardEvent && e.shiftKey) {
    return
  }

  // ★ 拼接自定义语言提示词（从"更多语言"输入框添加的）
  const customLangPrefix = customLangs.value.length > 0
    ? tpl('chat.customLangPrefix', { l: customLangs.value.join('、') })
    : ''

  // 文件翻译
  if (attachedFiles.value.length > 0) {
    for (const file of attachedFiles.value) {
      // ★ 文件翻译时拼入自定义语言提示词 + 输入框内容
      const msg = customLangPrefix + inputText.value.trim()
      // 把自定义语言名解析成代码并入 target_langs；解析不出的保留在 message 由后端解析
      const customCodes = customLangs.value
        .map(cl => _LANG_NAME_TO_CODE[cl.toLowerCase()] || _LANG_NAME_TO_CODE[cl])
        .filter((c): c is string => !!c)
      const langs = [...new Set([...store.selectedLangs.filter(l => l !== 'other'), ...customCodes])]
      await store.sendFile(file, langs.length > 0 ? langs : undefined, msg)
    }
    attachedFiles.value = []
    inputText.value = ''
    customLangs.value = []
    nextTick(() => { autoResizeInput() })
    return
  }

  // 文本消息
  const rawText = inputText.value.trim()
  if (!rawText && !customLangPrefix) return
  const text = customLangPrefix + rawText
  inputText.value = ''
  customLangs.value = []
  nextTick(() => { autoResizeInput() })

  // ★ 翻译技能带上选中的语言与源语言
  const options: Record<string, unknown> = {}
  options.target_langs = [...store.selectedLangs]
  if (sourceLang.value !== 'auto') options.source_lang = sourceLang.value
  store.sendMessage(text, options)
}

// ---- 点击外部关闭下拉 ----
function onClickOutside(e: MouseEvent) {
  if (!(e.target as HTMLElement).closest('.lang-selector')) {
    langDropdownOpen.value = false
  }
}

// ---- 新消息时滚到底 ----
watch(() => store.messages.length, async () => {
  await nextTick()
  scrollToBottom()
})

watch(
  () => store.messages.map(m => m.progress?.percent),
  async () => {
    await nextTick()
    scrollToBottom()
  },
  { deep: true }
)

// 滚动消息列表到底部（新消息或进度更新时调用）
function scrollToBottom() {
  if (messagesContainer.value) {
    messagesContainer.value.scrollTop = messagesContainer.value.scrollHeight
  }
}

onMounted(() => {
  window.addEventListener('resize', onResize)
  document.addEventListener('click', onClickOutside)
  loadTranslationLangs()
})

onUnmounted(() => {
  window.removeEventListener('resize', onResize)
  document.removeEventListener('click', onClickOutside)
})

/** ★ 从后端加载翻译语言列表（仅KB语言，"其他语言"统一为一个选项） */
// loadTranslationLangs 加载知识库支持的语言列表，并同步到语言选择区（新语言升级到 KB 区）。
// 无参数无返回；接口失败时静默跳过，保留内置语言选项。
async function loadTranslationLangs() {
  try {
    const resp = await fetch('/api/translation/langs')
    if (!resp.ok) return
    const data = await resp.json()
    if (data.kb_langs?.length) {
      kbLangs.value = data.kb_langs.map((l: any) => l.code)
      for (const l of data.kb_langs) {
        if (!LANG_OPTIONS[l.code]) LANG_OPTIONS[l.code] = { label: l.name, flag: l.flag }
        else { LANG_OPTIONS[l.code].label = l.name; LANG_OPTIONS[l.code].flag = l.flag }
        // ★ KB上传新增语言后，从"其他语言"子选单升级到KB区
        if (OTHER_LANG_OPTIONS.value[l.code]) {
          delete OTHER_LANG_OPTIONS.value[l.code]
        }
      }
    }
  } catch {
    // 后端不可用时保持本地默认
  }
}
</script>

<style scoped>
/* ===== 聊天窗口整体：纵向 Flex，占满父容器高度 ===== */
.chat-window {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

/* ==================== 顶部标题栏 ==================== */
.chat-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 2px solid #e0e0e0;
  flex-shrink: 0;
  background: #fff;
}
.header-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.header-info { display: flex; align-items: center; gap: 6px; }
.header-icon { font-size: 18px; }
.header-title { font-size: 15px; font-weight: 600; color: #202124; }
.clear-btn {
  border: none; background: transparent; font-size: 18px;
  cursor: pointer; padding: 4px 8px; border-radius: 6px;
  transition: background 0.2s; opacity: 0.5;
}
.clear-btn:hover { background: #f5f5f5; opacity: 1; }

/* ==================== 离线提示条 ==================== */
.offline-bar {
  padding: 8px 16px; background: #fff3e0; color: #e65100;
  text-align: center; font-size: 13px; flex-shrink: 0;
  display: flex; align-items: center; justify-content: center; gap: 10px;
}
.retry-btn {
  border: 1px solid #e65100; background: #fff; color: #e65100;
  font-size: 12px; padding: 2px 12px; border-radius: 6px; cursor: pointer;
  transition: background 0.2s;
}
.retry-btn:hover { background: #ffe0b2; }
.retry-btn:disabled { opacity: 0.6; cursor: not-allowed; }

/* ==================== 消息列表区域：滚动容器 ==================== */
.messages-area {
  flex: 1; overflow-y: auto; padding: 16px 0;
  -webkit-overflow-scrolling: touch;
}

/* ==================== 空状态：欢迎页 + 示例问题 ==================== */
.empty-state {
  display: flex; flex-direction: column; align-items: center;
  justify-content: center; padding: 40px 20px;
  text-align: center; min-height: 200px;
}
.empty-icon { font-size: 48px; margin-bottom: 16px; }
.empty-title { font-size: 15px; color: #5f6368; margin-bottom: 24px; line-height: 1.6; }
.example-list {
  display: flex; flex-direction: column; gap: 10px;
  width: 100%; max-width: 400px;
}
.example-bubble {
  padding: 12px 18px; border: 1px solid #e0e0e0; border-radius: 20px;
  background: #f8f9fa; color: #202124; font-size: 14px;
  cursor: pointer; transition: all 0.2s; text-align: left;
  -webkit-tap-highlight-color: transparent;
}
.example-bubble:hover { background: #e8f0fe; border-color: #1a73e8; }
.example-bubble:active { transform: scale(0.98); }

/* ==================== 加载动画 ==================== */
.loading-indicator {
  display: flex; align-items: center; gap: 8px;
  padding: 12px 20px; color: #5f6368; font-size: 13px;
}
.loading-dots { display: flex; gap: 4px; }
.loading-dots span {
  width: 6px; height: 6px; border-radius: 50%;
  background: #1a73e8; animation: bounce 1.4s infinite ease-in-out;
}
.loading-dots span:nth-child(2) { animation-delay: 0.2s; }
.loading-dots span:nth-child(3) { animation-delay: 0.4s; }
@keyframes bounce {
  0%, 80%, 100% { transform: scale(0.6); opacity: 0.4; }
  40% { transform: scale(1); opacity: 1; }
}

/* ==================== 底部输入区域：标签行 + 输入行 ==================== */
.input-area {
  border-top: 1px solid #e0e0e0; padding: 8px 16px;
  background: #fafafa; flex-shrink: 0;
}
.tags-row { display: flex; flex-wrap: wrap; gap: 6px; padding-bottom: 8px; }
.tag {
  display: inline-flex; align-items: center; gap: 4px;
  padding: 4px 10px; border-radius: 14px; font-size: 12px; line-height: 1.4;
}
.tag-file { background: #e8f0fe; color: #1a73e8; border: 1px solid #d2e3fc; }
.tag-lang { background: #e6f4ea; color: #1e8e3e; border: 1px solid #ceead6; }
.tag-remove {
  border: none; background: transparent; color: inherit;
  font-size: 11px; cursor: pointer; padding: 0 2px; opacity: 0.6; line-height: 1;
}
.tag-remove:hover { opacity: 1; }

/* ★ "其他语言"标签（橙色区分） */
.tag-other-lang {
  background: #fff3e0; color: #e65100; border: 1px solid #ffe0b2;
}

.input-row {
  display: flex; gap: 6px; align-items: center;
  max-width: 900px; margin: 0 auto;
}
.input-actions { display: flex; gap: 4px; align-items: center; flex-shrink: 0; }
.action-btn {
  width: 36px; height: 36px;
  border: 1px solid #dadce0; border-radius: 50%;
  background: #fff; color: #5f6368; font-size: 18px;
  cursor: pointer; display: flex; align-items: center; justify-content: center;
  transition: all 0.2s; flex-shrink: 0;
  -webkit-tap-highlight-color: transparent;
}
.action-btn:hover { background: #f1f3f4; border-color: #bdc1c6; }
.lang-btn { font-size: 14px; width: auto; border-radius: 18px; padding: 0 10px; gap: 2px; }

/* 语言下拉 */
.lang-selector { position: relative; }
.lang-dropdown {
  position: absolute; bottom: calc(100% + 8px); left: 0;
  min-width: 200px; background: #fff; border: 1px solid #dadce0;
  border-radius: 12px; box-shadow: 0 4px 16px rgba(0,0,0,0.12);
  padding: 8px 0; z-index: 100; max-height: 360px; overflow-y: auto;
}
.lang-option {
  display: flex; align-items: center; gap: 8px;
  padding: 8px 16px; cursor: pointer; font-size: 13px;
  color: #202124; transition: background 0.15s;
}
.lang-option:hover { background: #f1f3f4; }
.lang-option input[type="checkbox"] { width: 16px; height: 16px; accent-color: #1a73e8; }
.lang-divider { height: 1px; background: #e0e0e0; margin: 0 16px 4px; }

/* ★ 语言区标题 */
.lang-section-title {
  padding: 8px 16px 4px; font-size: 12px; font-weight: 600;
  color: #5f6368; display: flex; align-items: center; gap: 4px;
}
.section-hint {
  font-size: 10px; font-weight: 400; color: #999;
  background: #f0f0f0; padding: 1px 6px; border-radius: 8px; margin-left: 4px;
}

/* ★ 源语言选择行 */
.source-lang-row {
  display: flex; flex-wrap: wrap; gap: 6px; padding: 4px 16px 8px;
}
.source-lang-btn {
  padding: 4px 10px; border: 1px solid #dadce0; border-radius: 14px;
  background: #fff; font-size: 12px; color: #5f6368; cursor: pointer;
  transition: all 0.15s;
}
.source-lang-btn:hover { background: #f1f3f4; }
.source-lang-btn.active { background: #e8f0fe; border-color: #1a73e8; color: #1a73e8; font-weight: 600; }

/* ★ "其他语言"选项提示 */
.lang-option-other { font-weight: 500; }
.lang-option-hint {
  padding: 2px 16px 8px 40px; font-size: 11px; color: #999; line-height: 1.4;
}

/* ★ "更多语言"输入框 */
.lang-custom-input-wrap {
  display: flex; align-items: center; gap: 6px;
  padding: 4px 12px 8px 40px;
}
.lang-custom-input {
  flex: 1; padding: 6px 10px;
  border: 1px solid #dadce0; border-radius: 8px;
  font-size: 13px; outline: none; transition: border-color 0.2s;
  background: #fff;
}
.lang-custom-input:focus { border-color: #1a73e8; }
.lang-custom-input::placeholder { color: #aaa; }
.lang-custom-add-btn {
  width: 30px; height: 30px; border-radius: 8px;
  border: none; background: #1a73e8; color: #fff;
  font-size: 16px; cursor: pointer; display: flex;
  align-items: center; justify-content: center;
  transition: background 0.2s;
}
.lang-custom-add-btn:hover:not(:disabled) { background: #1557b0; }
.lang-custom-add-btn:disabled { background: #ccc; cursor: not-allowed; }

.message-input {
  flex: 1; padding: 10px 16px;
  border: 1px solid #dadce0; border-radius: 24px;
  font-size: 15px; outline: none; transition: border-color 0.2s;
  background: #fff; min-width: 0;
  resize: none; overflow-y: auto; line-height: 1.5;
  max-height: 120px; font-family: inherit;
}
.message-input:focus { border-color: #1a73e8; }

.send-btn {
  width: 42px; height: 42px; border: none; border-radius: 50%;
  color: white; font-size: 18px; cursor: pointer;
  transition: all 0.2s; display: flex; align-items: center; justify-content: center;
  flex-shrink: 0;
}
.send-btn:disabled { cursor: not-allowed; }
.send-btn:not(:disabled):active { transform: scale(0.92); }

/* ★ 停止按钮样式 */
.stop-btn { font-size: 14px; animation: stopPulse 1.5s ease-in-out infinite; }
.stop-btn:hover { background: #b71c1c !important; }
@keyframes stopPulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.7; } }

/* ==================== 移动端适配 ==================== */
.chat-mobile .chat-header { padding: 10px 12px; }
.chat-mobile-header { padding-top: calc(10px + env(safe-area-inset-top)); }
.chat-mobile .messages-area { padding: 12px 0; }
.chat-mobile .empty-state { padding: 24px 16px; }
.chat-mobile .empty-icon { font-size: 36px; margin-bottom: 12px; }
.chat-mobile .example-list { max-width: 100%; }
.chat-mobile .example-bubble { padding: 10px 14px; font-size: 13px; }
.chat-mobile .input-area {
  padding: 6px 10px;
  padding-bottom: calc(6px + env(safe-area-inset-bottom));
}
.chat-mobile .action-btn { width: 32px; height: 32px; font-size: 16px; }
.chat-mobile .lang-btn { font-size: 12px; padding: 0 8px; }
.chat-mobile .message-input { font-size: 16px; padding: 8px 14px; }
.chat-mobile .send-btn { width: 38px; height: 38px; }
.chat-mobile .tag { font-size: 11px; padding: 3px 8px; }
.chat-mobile .lang-dropdown { left: -10px; min-width: 180px; }
/* ===== 📚 KB导入弹窗 ===== */
.kb-modal-overlay {
  position: fixed; top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(0,0,0,0.4); z-index: 1000;
  display: flex; align-items: center; justify-content: center;
}
.kb-modal {
  width: 520px; max-width: 92vw; max-height: 85vh;
  background: #fff; border-radius: 16px;
  box-shadow: 0 12px 40px rgba(0,0,0,0.2);
  overflow-y: auto;
}
.kb-modal-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 20px 24px 16px; border-bottom: 1px solid #f0f0f0;
}
.kb-modal-header h3 { font-size: 18px; color: #202124; margin: 0; }
.kb-modal-close {
  width: 32px; height: 32px; border: none; border-radius: 50%;
  background: transparent; font-size: 18px; cursor: pointer; color: #5f6368;
  display: flex; align-items: center; justify-content: center;
  transition: background 0.2s;
}
.kb-modal-close:hover { background: #f1f3f4; }
.kb-modal-body { padding: 20px 24px 24px; }

/* 步骤 */
.kb-step {
  margin-bottom: 20px; opacity: 0.5; transition: opacity 0.3s;
}
.kb-step.active, .kb-step.done { opacity: 1; }
.kb-step.disabled { pointer-events: none; }
.kb-step-header {
  display: flex; align-items: center; gap: 10px; margin-bottom: 12px;
}
.kb-step-num {
  width: 28px; height: 28px; border-radius: 50%;
  background: #e0e0e0; color: #999; font-size: 14px; font-weight: 700;
  display: flex; align-items: center; justify-content: center;
}
.kb-step.active .kb-step-num { background: #1a73e8; color: #fff; }
.kb-step.done .kb-step-num { background: #34a853; color: #fff; }
.kb-step-title { font-size: 15px; font-weight: 600; color: #202124; }
.kb-step-check { font-size: 16px; }
.kb-step-content { padding-left: 38px; }

/* 上传区域 */
.kb-drop-zone {
  border: 2px dashed #dadce0; border-radius: 12px;
  padding: 28px 20px; text-align: center; cursor: pointer;
  transition: all 0.2s; font-size: 14px; color: #5f6368;
}
.kb-drop-zone:hover { border-color: #1a73e8; background: #f8faff; }
.kb-drop-zone.dragging { border-color: #1a73e8; background: #e8f0fe; }
.kb-drop-zone.has-file { border-style: solid; border-color: #34a853; background: #f0faf0; }
.kb-drop-icon { font-size: 32px; margin-bottom: 8px; }
.kb-drop-hint { font-size: 12px; color: #999; margin-top: 4px; }
.kb-file-info { font-size: 14px; color: #202124; font-weight: 500; }
.kb-file-remove {
  margin-top: 8px; padding: 4px 12px; border: 1px solid #d93025; border-radius: 6px;
  background: transparent; color: #d93025; font-size: 12px; cursor: pointer;
  transition: all 0.2s;
}
.kb-file-remove:hover { background: #fce8e6; }

/* 按钮 */
.kb-action-btn {
  margin-top: 12px; padding: 10px 24px; border: 1px solid #1a73e8;
  border-radius: 8px; background: #fff; color: #1a73e8;
  font-size: 14px; font-weight: 600; cursor: pointer; transition: all 0.2s;
}
.kb-action-btn:hover:not(:disabled) { background: #f8faff; }
.kb-action-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.kb-action-btn.primary {
  background: #1a73e8; color: #fff; border-color: #1a73e8;
}
.kb-action-btn.primary:hover:not(:disabled) { background: #1557b0; }

/* 进度条 */
.kb-progress { margin-top: 12px; }
.kb-progress-bar {
  height: 8px; background: #e0e0e0; border-radius: 4px; overflow: hidden;
}
.kb-progress-fill {
  height: 100%; background: #1a73e8; border-radius: 4px; transition: width 0.3s;
}
.kb-progress-animated {
  background: linear-gradient(90deg, #1a73e8, #4fc3f7, #1a73e8);
  background-size: 200% 100%;
  animation: kb-shimmer 1.5s infinite;
}
@keyframes kb-shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }
.kb-progress-text { font-size: 13px; color: #5f6368; margin-top: 6px; text-align: center; }

/* 识别结果 */
.kb-recognized-info { margin-top: 8px; }
.kb-recognized-stat { font-size: 14px; color: #202124; margin-bottom: 8px; }
.kb-recognized-langs { font-size: 13px; color: #5f6368; margin-bottom: 4px; }
.kb-recognized-new { font-size: 13px; color: #1a73e8; margin-bottom: 8px; }
.kb-lang-tag {
  display: inline-block; padding: 2px 8px; margin: 2px;
  background: #f1f3f4; border-radius: 4px; font-size: 12px; color: #5f6368;
}
.kb-lang-tag.new { background: #e8f0fe; color: #1a73e8; }

/* 预览表 */
.kb-preview { margin-top: 12px; }
.kb-preview-title { font-size: 12px; color: #999; margin-bottom: 6px; }
.kb-preview-table {
  border: 1px solid #e0e0e0; border-radius: 8px; overflow: hidden;
  font-size: 12px;
}
.kb-preview-row {
  display: flex; border-bottom: 1px solid #f0f0f0;
}
.kb-preview-row:last-child { border-bottom: none; }
.kb-preview-row > span {
  flex: 1; padding: 6px 8px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.kb-preview-header {
  background: #f8f9fa; font-weight: 600; color: #5f6368;
}

/* 结果 */
.kb-result-success {
  padding: 12px 16px; border-radius: 8px;
  background: #e6f4ea; color: #1e8e3e; font-size: 14px; margin-top: 8px;
}
.kb-result-error {
  padding: 12px 16px; border-radius: 8px;
  background: #fce8e6; color: #d93025; font-size: 14px; margin-top: 8px;
}
</style>
