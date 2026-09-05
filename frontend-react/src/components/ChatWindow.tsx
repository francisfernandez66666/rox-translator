// ============================================================================
// components/ChatWindow.tsx — 前台工作台（Vue ChatWindow.vue 等价实现）
// 能力：欢迎示例(自动发送)/离线横幅+重试、消息流(自动滚底)、源语言选择、KB/其他/更多
//       语言多选、自定义语言前缀、文件上传翻译(完整 accept)、双模式(pro/fast)持久化、
//       余额/用量展示、停止生成、清空、反馈弹窗。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, Textarea } from 'tdesign-react'
import { StopCircleIcon, ClearIcon } from 'tdesign-icons-react'
import { MessagePlugin } from 'tdesign-react'
import MessageBubble from './MessageBubble'
import { FeedbackModalFromMessage } from './modals'
import { useChat } from '@/hooks/useChat'
import { myPackage, meContext, request } from '@/api'
import type { ChatMessage } from '@/types'
import { useT } from '@/i18n'
import LangMultiSelect from '@/components/LangMultiSelect'
import ModeToggle from '@/components/ModeToggle'

// ============ 本文件职责中文说明 ============
// 前台工作台（聊天主界面）：消息流、语言选择、文件翻译、双模式与反馈。
// ========================================

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
  kk: { label: '哈萨克语（哈萨克斯坦）', flag: '🇰🇿' },
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

// 数字千分位格式化，并处理 undefined/负数，用于余额与用量展示
function fmtNum(n: number): string {
  return new Intl.NumberFormat().format(Math.max(0, Math.floor(n || 0)))
}

// 默认导出组件：前台翻译工作台，承载消息流、语言选择、双模式与余额展示（等价 Vue ChatWindow.vue）
export default function ChatWindow() {
  const [lang, t2] = useT()
  const chat = useChat()
  const [input, setInput] = useState('')
  const [feedbackMsg, setFeedbackMsg] = useState<ChatMessage | null>(null)

  // ★ 双模式（fast/pro）持久化
  const [mode, setMode] = useState<'fast' | 'pro'>(
    (localStorage.getItem('translate_mode') as 'fast' | 'pro') || 'pro',
  )

  // ★ 缩翻（任务7）：勾选+最长字符限制（0=未启用）
  const [condenseOn, setCondenseOn] = useState(false)
  const [condenseMax, setCondenseMax] = useState(200)

  // ★ 余额 / 用量
  const [balance, setBalance] = useState<{ tokens: number; approx: number } | null>(null)
  const [usage, setUsage] = useState<{ today: number; todaySentences: number } | null>(null)
  const [orgBudget, setOrgBudget] = useState<{ limit: number; used: number; name: string } | null>(null)

  const scrollRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  // ---- 语言显示名：优先 i18n（lang.<code>），缺失回退本地 label ----
  // 根据语言代码获取展示名，便于在标签中统一显示
  const langLabel = useCallback((code: string, fallback?: string): string => {
    const v = t2(`lang.${code}`)
    return v !== `lang.${code}` ? v : (fallback || code)
  }, [t2])

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
          // ★ 修复（2026-09-02 前端契约审计）：后端 /api/me/package 无 estimate_rate 字段。
          //   改用余额行「可用 token ÷ ≈句数」反推实际换算率（无余额时兜底 500 句/token）。
          let rate = 500
          if (typeof r.balance_tokens === 'number' && r.balance_tokens > 0
              && typeof r.balance_sentences_approx === 'number' && r.balance_sentences_approx > 0) {
            rate = r.balance_tokens / r.balance_sentences_approx
          }
          setUsage({ today, todaySentences: Math.floor(today / rate) })
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

  // ---- textarea 自动高度 ----
  // 根据内容自动调整输入框高度（最大 120px），避免长文本溢出
  function autoResize() {
    const el = inputRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 120) + 'px'
  }

  // 停止生成：中断当前翻译流；已翻译部分消耗的 token 不会退还（产品规则）
  function handleStop() {
    chat.stopGeneration()
    void MessagePlugin.info(t2('chat.stopTokenNote'))
  }

  // ---- 发送 ----
  // 走文本翻译；目标语言直接取自聊天全局 selectedLangs
  // ★ 缩翻（任务7）：勾选后把最长字符限制透传后端（0=未启用）
  async function handleSend() {
    const rawText = input.trim()
    if (!rawText) return
    setInput('')

    const options: Record<string, unknown> = { target_langs: chat.selectedLangs, lang }
    if (condenseOn && condenseMax > 0) options.max_length = condenseMax
    chat.sendMessage(rawText, options)
  }

  const canSend = input.trim().length > 0

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
        <div style={{ background: '#e8f0fe', color: 'var(--td-brand-color, #2f47f5)', fontSize: 12, padding: '4px 6%', display: 'flex', gap: 14, flexWrap: 'wrap', alignItems: 'center' }}>
          {balance && <span>余额 {fmtNum(balance.tokens)}（≈{fmtNum(balance.approx)} 句）</span>}
          {usage && <span>今日 {fmtNum(usage.today)} token（≈{fmtNum(usage.todaySentences)} 句）</span>}
          {orgBudget && <span>{orgBudget.name} {fmtNum(orgBudget.used)}/{fmtNum(orgBudget.limit)}</span>}
        </div>
      )}

      {/* 消息滚动区 */}
      <div className="chat-scroll" ref={scrollRef}>
        {chat.messages.length === 0 && (
          <div style={{ textAlign: 'center', marginTop: 60 }}>
            <div style={{ fontSize: 26, fontWeight: 800, color: 'var(--td-brand-color-active, #1f33d6)' }}>{t2('chat.welcome')}</div>
            <div style={{ fontSize: 14, color: '#5f6b7a', marginTop: 10, maxWidth: 480, margin: '10px auto 0' }}>
              {t2('chat.welcomeSub')}
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
        {chat.selectedLangs.length > 0 && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, paddingBottom: 8 }}>
            {chat.selectedLangs.filter((l) => LANG_OPTIONS[l]).map((l) => (
              <span key={'l' + l} className="tag tag-lang">
                {LANG_OPTIONS[l].flag} {langLabel(l, LANG_OPTIONS[l].label)}
                <Button size="small" variant="text" theme="default" className="tag-close" onClick={() => chat.setSelectedLangs(chat.selectedLangs.filter((x) => x !== l))}>✕</Button>
              </span>
            ))}
            {chat.selectedLangs.filter((l) => !LANG_OPTIONS[l]).map((l) => (
              <span key={'ol' + l} className="tag tag-other-lang">
                🤖 {langLabel(l, OTHER_LANG_OPTIONS[l]?.label)}
                <Button size="small" variant="text" theme="default" className="tag-close" onClick={() => chat.setSelectedLangs(chat.selectedLangs.filter((x) => x !== l))}>✕</Button>
              </span>
            ))}
          </div>
        )}

        {!!chat.errorMessage && (
          <div style={{ color: '#c62828', fontSize: 13 }}>{chat.errorMessage}</div>
        )}

        <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
          {/* 上传 + 语言选择 */}
          <div style={{ display: 'flex', gap: 4, alignItems: 'center', flexShrink: 1, minWidth: 0 }}>
            <div style={{ minWidth: 0, flex: '1 1 200px' }}>
              <LangMultiSelect value={chat.selectedLangs} onChange={chat.setSelectedLangs} />
            </div>
          </div>

          <Textarea
            data-testid="translate-input"
            autosize={{ minRows: 1, maxRows: 5 }}
            value={input}
            onChange={(v) => { setInput(v); autoResize() }}
            placeholder={t2('chat.placeholder')}
            onKeydown={(v, ctx) => {
              if (ctx.e.key === 'Enter' && !ctx.e.shiftKey) {
                ctx.e.preventDefault()
                void handleSend()
              }
            }}
            style={{ flex: 1 }}
          />

          {/* 双模式切换（与翻译工单共用 ModeToggle，顺序与工单页保持一致：左快速/右专业） */}
          <ModeToggle value={mode} onChange={setMode2} fastFirst />
          {/* ★ 缩翻（任务7）：勾选并输入最长字符限制，提示模型精简输出。
              预留定宽槽位（72px）——勾选只显隐输入框、不改变行宽，避免模式/清空/发送按钮位置跳动 */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 4, flexShrink: 0 }}>
            <label style={{ fontSize: 12, color: '#555', display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}>
              <input type="checkbox" checked={condenseOn} onChange={(e) => setCondenseOn(e.target.checked)} /> 缩翻
            </label>
            <div style={{ width: 72, flexShrink: 0 }}>
              {condenseOn && (
                <input type="number" min={1} max={10000} value={condenseMax}
                  onChange={(e) => setCondenseMax(parseInt(e.target.value) || 0)}
                  style={{ width: '100%', boxSizing: 'border-box', height: 28, fontSize: 12, border: '1px solid #d8dee6', borderRadius: 6, padding: '0 6px' }}
                  title="最长字符长度" />
              )}
            </div>
          </div>
          <Button variant="text" theme="default" size="medium" icon={<ClearIcon />}
                  onClick={() => { chat.clearMessages() }}>
            {t2('chat.clearChat')}
          </Button>

          {chat.isLoading ? (
            <Button theme="warning" size="medium" icon={<StopCircleIcon />} onClick={handleStop}>{t2('chat.stop')}</Button>
          ) : (
            <Button theme="primary" size="medium" disabled={!canSend} onClick={() => void handleSend()}>{t2('chat.send')}</Button>
          )}
        </div>
      </div>

      {feedbackMsg && (
        <FeedbackModalFromMessage message={feedbackMsg} onClose={() => setFeedbackMsg(null)} />
      )}
    </div>
  )
}
