// ============================================================================
// components/ChatWindow.tsx — 前台工作台（Vue ChatWindow.vue 等价实现）
// 能力：欢迎示例(自动发送)/离线横幅+重试、消息流(自动滚底)、源语言选择、KB/其他/更多
//       语言多选、自定义语言前缀、文件上传翻译(完整 accept)、双模式(pro/fast)持久化、
//       余额/用量展示、停止生成、清空、反馈弹窗。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, Textarea } from 'tdesign-react'
import { UploadIcon, StopCircleIcon, ClearIcon } from 'tdesign-icons-react'
import MessageBubble from './MessageBubble'
import { FeedbackModalFromMessage } from './modals'
import { useChat } from '@/hooks/useChat'
import { myPackage, meContext, request } from '@/api'
import type { ChatMessage } from '@/types'
import { useT } from '@/i18n'
import LangMultiSelect from '@/components/LangMultiSelect'
import ModeToggle from '@/components/ModeToggle'

interface LangItem { code: string; name: string; flag?: string }

// ★ 源语言选项（互译方向；auto=自动检测）——用于语言面板顶部的"源语言"选择
const SOURCE_LANG_OPTIONS = [
  { code: 'auto', flag: '🤖', labelKey: 'chat.sourceAuto' },
  { code: 'zh', flag: '🇨🇳', labelKey: 'lang.zh' },
  { code: 'en', flag: '🇬🇧', labelKey: 'lang.en' },
  { code: 'zh_hant', flag: '🇹🇼', labelKey: 'lang.zhHant' },
]

// ★ KB 语言名称/国旗（本地兜底，后端返回后覆盖）——知识库支持的高质量目标语言
const LANG_OPTIONS: Record<string, { label: string; flag: string }> = {
  en: { label: '英语', flag: '🇬🇧' },
  ru: { label: '俄语', flag: '🇷🇺' },
  ar: { label: '阿拉伯语', flag: '🇸🇦' },
  es: { label: '西班牙语', flag: '🇪🇸' },
  pt: { label: '葡萄牙语', flag: '🇵🇹' },
  fr: { label: '法语', flag: '🇫🇷' },
  kk: { label: '哈萨克语', flag: '🇰🇿' },
  de: { label: '德语', flag: '🇩🇪' },
  zh_hant: { label: '繁体中文', flag: '🇹🇼' },
}

// ★ "其他语言"子选单（非KB语言，AI翻译，直接勾选）——不走知识库的普通 AI 翻译语言
const OTHER_LANG_OPTIONS: Record<string, { label: string; flag: string }> = {
  zh: { label: '中文', flag: '🇨🇳' },
  ja: { label: '日语', flag: '🇯🇵' },
  ko: { label: '韩语', flag: '🇰🇷' },
  th: { label: '泰语', flag: '🇹🇭' },
  vi: { label: '越南语', flag: '🇻🇳' },
  mn: { label: '蒙语', flag: '🇲🇳' },
  ms: { label: '马来语', flag: '🇲🇾' },
  id: { label: '印尼语', flag: '🇮🇩' },
  it: { label: '意大利语', flag: '🇮🇹' },
  pl: { label: '波兰语', flag: '🇵🇱' },
  nl: { label: '荷兰语', flag: '🇳🇱' },
  sv: { label: '瑞典语', flag: '🇸🇪' },
  uk: { label: '乌克兰语', flag: '🇺🇦' },
  tr: { label: '土耳其语', flag: '🇹🇷' },
  hi: { label: '印地语', flag: '🇮🇳' },
  fa: { label: '波斯语', flag: '🇮🇷' },
  he: { label: '希伯来语', flag: '🇮🇱' },
  el: { label: '希腊语', flag: '🇬🇷' },
  my: { label: '缅甸语', flag: '🇲🇲' },
  km: { label: '柬埔寨语', flag: '🇰🇭' },
  lo: { label: '老挝语', flag: '🇱🇦' },
  tl: { label: '菲律宾语', flag: '🇵🇭' },
  gu: { label: '古吉拉特语', flag: '🇮🇳' },
  ur: { label: '乌尔都语', flag: '🇵🇰' },
  te: { label: '泰卢固语', flag: '🇮🇳' },
  mr: { label: '马拉地语', flag: '🇮🇳' },
  bn: { label: '孟加拉语', flag: '🇧🇩' },
  ta: { label: '泰米尔语', flag: '🇮🇳' },
  bo: { label: '藏语', flag: '🇨🇳' },
  ug: { label: '维吾尔语', flag: '🇨🇳' },
  yue: { label: '粤语', flag: '🇨🇳' },
}

// ★ 语言名→代码的本地映射（常见语言中文名/英文名→ISO代码）——用于自定义语言输入解析
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

const FILE_ACCEPT = '.docx,.pptx,.xlsx,.pdf,.txt,.csv,.srt,.vtt,.md,.json,.yaml,.yml'

// 数字千分位格式化，并处理 undefined/负数，用于余额与用量展示
function fmtNum(n: number): string {
  return new Intl.NumberFormat().format(Math.max(0, Math.floor(n || 0)))
}

// 默认导出组件：前台翻译工作台，承载消息流、语言选择、文件上传、双模式与余额展示（等价 Vue ChatWindow.vue）
export default function ChatWindow() {
  const [lang, t2] = useT()
  const chat = useChat()
  const [input, setInput] = useState('')
  const [kbLangs, setKbLangs] = useState<string[]>(['en', 'ru', 'ar', 'es', 'pt', 'fr', 'kk', 'de', 'zh_hant'])
  const [langItems, setLangItems] = useState<LangItem[]>([])
  const [feedbackMsg, setFeedbackMsg] = useState<ChatMessage | null>(null)

  // ★ 待翻译文件
  const [attachedFiles, setAttachedFiles] = useState<File[]>([])
  // ★ 双模式（fast/pro）持久化
  const [mode, setMode] = useState<'fast' | 'pro'>(
    (localStorage.getItem('translate_mode') as 'fast' | 'pro') || 'pro',
  )

  // ★ 余额 / 用量
  const [balance, setBalance] = useState<{ tokens: number; approx: number } | null>(null)
  const [usage, setUsage] = useState<{ today: number; todaySentences: number } | null>(null)
  const [orgBudget, setOrgBudget] = useState<{ limit: number; used: number; name: string } | null>(null)

  const scrollRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // ---- 语言显示名：优先 i18n（lang.<code>），缺失回退本地 label ----
  // 根据语言代码获取展示名，便于在标签中统一显示
  const langLabel = useCallback((code: string, fallback?: string): string => {
    const v = t2(`lang.${code}`)
    return v !== `lang.${code}` ? v : (fallback || code)
  }, [t2])

  // ---- KB 语言从后端动态加载（升级到 KB 区） ----
  // 拉取后端支持的 KB 语言列表，覆盖本地兜底的名称与国旗
  useEffect(() => {
    ;(async () => {
      try {
        const r = await request<{ kb_langs?: LangItem[] }>('/api/translation/langs')
        if (r.kb_langs && r.kb_langs.length) {
          setLangItems(r.kb_langs)
          setKbLangs(r.kb_langs.map((x) => x.code))
          for (const l of r.kb_langs) {
            if (!LANG_OPTIONS[l.code]) LANG_OPTIONS[l.code] = { label: l.name, flag: l.flag || '🌐' }
            else { LANG_OPTIONS[l.code].label = l.name; LANG_OPTIONS[l.code].flag = l.flag || '🌐' }
          }
        }
      } catch { /* 本地兜底 */ }
    })()
  }, [])

  // ---- 余额 / 用量加载 ----
  // 从 myPackage 接口读取个人余额、今日用量及企业预算额度
  const loadBalance = useCallback(async () => {
    try {
      const r: any = await myPackage()
      if (r && r.success) {
        if (typeof r.balance_tokens === 'number') {
          setBalance({
            tokens: r.balance_tokens,
            approx: r.balance_sentences_approx ?? Math.floor(r.balance_tokens / 500),
          })
        }
        const today = typeof r.tokens_used_today === 'number' ? r.tokens_used_today : null
        if (today !== null) {
          setUsage({ today, todaySentences: Math.floor(today / (r.estimate_rate || 500)) })
        }
        if (r.org_budget && r.org_budget.limit > 0) {
          setOrgBudget({ limit: r.org_budget.limit, used: r.org_budget.used_this_month, name: r.org_budget.name })
        } else {
          setOrgBudget(null)
        }
      }
    } catch { setBalance(null) }
  }, [])

  const loadMe = useCallback(async () => {
    try {
      await meContext()
    } catch { /* 静默 */ }
  }, [])

  // 组件挂载时初次加载余额与用户上下文
  useEffect(() => {
    void loadBalance()
    void loadMe()
  }, [loadBalance, loadMe])

  // 每轮翻译结束（消息数变化）后刷新剩余量
  useEffect(() => {
    if (chat.messages.length) void loadBalance()
  }, [chat.messages.length, loadBalance])

  // ---- 进度/消息变化自动滚底 ----
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [chat.messages])

  // ---- 切换模式并持久化 ----
  // fast/pro 模式切换，同时写入 localStorage 以便跨会话记忆
  function setMode2(m: 'fast' | 'pro') {
    setMode(m)
    localStorage.setItem('translate_mode', m)
  }

  // ---- 文件选择 ----
  // 触发隐藏的文件输入框点击
  function triggerFileUpload() { fileInputRef.current?.click() }
  // 处理文件选择：去重并入已选文件列表，最后清空 input 以便重复选择同名文件
  function handleFileSelect(e: React.ChangeEvent<HTMLInputElement>) {
    const target = e.target
    if (!target.files) return
    for (const file of Array.from(target.files)) {
      setAttachedFiles((prev) => (prev.find((f) => f.name === file.name) ? prev : [...prev, file]))
    }
    target.value = ''
  }
  // 按索引移除已选文件
  function removeFile(idx: number) { setAttachedFiles((prev) => prev.filter((_, i) => i !== idx)) }

  // ---- textarea 自动高度 ----
  // 根据内容自动调整输入框高度（最大 120px），避免长文本溢出
  function autoResize() {
    const el = inputRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 120) + 'px'
  }

  // ---- 发送 ----
  // 有附件走文件翻译，否则走文本翻译；目标语言直接取自聊天全局 selectedLangs
  async function handleSend() {
    // 文件翻译
    if (attachedFiles.length > 0) {
      const langs = chat.selectedLangs.length > 0 ? chat.selectedLangs : undefined
      for (const file of attachedFiles) {
        await chat.sendFile(file, langs, input.trim())
      }
      setAttachedFiles([])
      setInput('')
      return
    }

    // 文本翻译
    const rawText = input.trim()
    if (!rawText) return
    setInput('')

    const options: Record<string, unknown> = { target_langs: chat.selectedLangs, lang }
    chat.sendMessage(rawText, options)
  }

  // ★ 示例问题：点击自动发送（对齐 Vue sendExample）
  // 欢迎页示例文案点击后直接以当前选中语言发送
  function sendExample(ex: string) {
    chat.sendMessage(ex, { target_langs: [...chat.selectedLangs] })
  }

  const canSend = input.trim().length > 0 || attachedFiles.length > 0

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 57px)' }}>
      {/* 离线横幅 */}
      {!chat.isBackendOnline && (
        <div style={{ background: '#fff3e0', borderBottom: '1px solid #ffe0b2', padding: '8px 6%', display: 'flex', gap: 10, alignItems: 'center' }}>
          <span style={{ fontSize: 13 }}>
            {chat.isBackendLoading ? t2('chat.checking') : t2('chat.offline')}
          </span>
          {!chat.isBackendLoading && (
            <Button size="small" variant="outline" loading={chat.isBackendChecking} onClick={() => void chat.retryHealth()}>
              {t2('chat.retry')}
            </Button>
          )}
        </div>
      )}

      {/* 余额 / 用量条 */}
      {(balance || usage || orgBudget) && (
        <div style={{ background: '#e8f0fe', color: '#1a73e8', fontSize: 12, padding: '4px 6%', display: 'flex', gap: 14, flexWrap: 'wrap', alignItems: 'center' }}>
          {balance && <span>余额 {fmtNum(balance.tokens)}（≈{fmtNum(balance.approx)} 句）</span>}
          {usage && <span>今日 {fmtNum(usage.today)} token（≈{fmtNum(usage.todaySentences)} 句）</span>}
          {orgBudget && <span>{orgBudget.name} {fmtNum(orgBudget.used)}/{fmtNum(orgBudget.limit)}</span>}
        </div>
      )}

      {/* 消息滚动区 */}
      <div className="chat-scroll" ref={scrollRef}>
        {chat.messages.length === 0 && (
          <div style={{ textAlign: 'center', marginTop: 60 }}>
            <div style={{ fontSize: 26, fontWeight: 800, color: '#1a237e' }}>{t2('chat.welcome')}</div>
            <div style={{ display: 'flex', gap: 10, justifyContent: 'center', flexWrap: 'wrap', maxWidth: 460, margin: '24px auto 0' }}>
              {[t2('chat.example1'), t2('chat.example2'), t2('chat.example3'), t2('chat.example4')].map((ex) => (
                <button key={ex} className="example-bubble" onClick={() => sendExample(ex)}>{ex}</button>
              ))}
            </div>
          </div>
        )}
        {chat.messages.map((m) => (
          <MessageBubble key={m.id} message={m} onFeedback={setFeedbackMsg} />
        ))}
      </div>

      {/* 输入区 */}
      <div className="chat-inputbar">
        {/* 已选文件 / 语言标签行 */}
        {(attachedFiles.length > 0 || chat.selectedLangs.length > 0) && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, paddingBottom: 8 }}>
            {attachedFiles.map((f, idx) => (
              <span key={'f' + idx} className="tag tag-file">
                📄 {f.name}
                <button className="tag-remove" onClick={() => removeFile(idx)}>✕</button>
              </span>
            ))}
            {chat.selectedLangs.filter((l) => LANG_OPTIONS[l]).map((l) => (
              <span key={'l' + l} className="tag tag-lang">
                {LANG_OPTIONS[l].flag} {langLabel(l, LANG_OPTIONS[l].label)}
                <button className="tag-remove" onClick={() => chat.setSelectedLangs(chat.selectedLangs.filter((x) => x !== l))}>✕</button>
              </span>
            ))}
            {chat.selectedLangs.filter((l) => !LANG_OPTIONS[l]).map((l) => (
              <span key={'ol' + l} className="tag tag-other-lang">
                🤖 {langLabel(l, OTHER_LANG_OPTIONS[l]?.label)}
                <button className="tag-remove" onClick={() => chat.setSelectedLangs(chat.selectedLangs.filter((x) => x !== l))}>✕</button>
              </span>
            ))}
          </div>
        )}

        {!!chat.errorMessage && (
          <div style={{ color: '#c62828', fontSize: 13 }}>{chat.errorMessage}</div>
        )}

        <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
          {/* 上传 + 语言选择 */}
          <div style={{ display: 'flex', gap: 4, alignItems: 'center', flexShrink: 0 }}>
            <button className="action-btn" title={t2('chat.uploadFile')} onClick={triggerFileUpload}>＋</button>
            <input ref={fileInputRef} type="file" accept={FILE_ACCEPT} style={{ display: 'none' }} onChange={handleFileSelect} />

            <div style={{ minWidth: 240, flex: '1 1 240px' }}>
              <LangMultiSelect value={chat.selectedLangs} onChange={chat.setSelectedLangs} />
            </div>
          </div>

          <Textarea
            autosize={{ minRows: 1, maxRows: 5 }}
            value={input}
            onChange={(v) => { setInput(v); autoResize() }}
            placeholder={attachedFiles.length > 0 ? t2('chat.sendFile') : t2('chat.placeholder')}
            onKeydown={(v, ctx) => {
              if (ctx.e.key === 'Enter' && !ctx.e.shiftKey) {
                ctx.e.preventDefault()
                void handleSend()
              }
            }}
            style={{ flex: 1 }}
          />

          {/* 双模式切换（与翻译工单共用 ModeToggle） */}
          <ModeToggle value={mode} onChange={setMode2} />
          <Button variant="text" theme="default" icon={<ClearIcon />}
                  onClick={() => { chat.clearMessages() }}>
            {t2('chat.clearChat')}
          </Button>

          {chat.isLoading ? (
            <Button theme="warning" icon={<StopCircleIcon />} onClick={chat.stopGeneration}>{t2('chat.stop')}</Button>
          ) : (
            <Button theme="primary" disabled={!canSend} onClick={() => void handleSend()}>{t2('chat.send')}</Button>
          )}
        </div>

        {/* KB 语言提示条 */}
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          {langItems.map((l) => (
            <span key={l.code} className="tr-lang-chip">{l.flag || ''} {l.name}</span>
          ))}
        </div>
      </div>

      {feedbackMsg && (
        <FeedbackModalFromMessage message={feedbackMsg} onClose={() => setFeedbackMsg(null)} />
      )}
    </div>
  )
}
