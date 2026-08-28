// ============================================================================
// components/MessageBubble.tsx — 聊天气泡（等价 Vue MessageBubble.vue）
// 能力：Markdown 渲染（h1–h6 / **bold** / *em* 含 lookbehind）、技能徽章、
//      翻译进度条、多语言译文表（模式/来源徽章）、match_report、
//      附件图片内联预览 + 全类型下载（blob 鉴权）、反馈入口。
// ============================================================================
import { useEffect, useMemo, useRef, useState } from 'react'
import { Button } from 'tdesign-react'
import { API_BASE, getAuthToken } from '@/api'
import type { ChatMessage } from '@/types'
import { t } from '@/i18n'
import { SkillBadge } from './SkillBadge'

// ============ 本文件职责中文说明 ============
// 聊天气泡组件：渲染单条消息（Markdown、译文表、附件预览、反馈入口）。
// ========================================

// ---- 轻量 Markdown → HTML（转义优先，行内顺序与 Vue 一致：** __ *em* `code`）----
// 转义 HTML 特殊字符，防止注入并确保后续标签正常解析
function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// 将消息正文渲染为受限 Markdown HTML：引用、标题、列表、分隔线、行内加粗/斜体/代码
export function renderMarkdown(text: string): string {
  let out = text || ''

  // 0. 转义
  out = escapeHtml(out)

  // 1. 引用块
  out = out.replace(/(?:^|\n)((?:&gt;\s*.*\n?)+)/g, (_m, block: string) => {
    const lines = block.trim().split('\n').map((l: string) => l.replace(/^&gt;\s*/, ''))
    return `\n<blockquote>${lines.join('<br>')}</blockquote>\n`
  })

  // 2. 标题 h1–h6
  out = out.replace(/^(#{1,6})\s+(.+)$/gm, (_m, hashes: string, content: string) => {
    const level = hashes.length
    return `<h${level}>${content.trim()}</h${level}>`
  })

  // 3. 分隔线
  out = out.replace(/^(?:---|\*\*\*)\s*$/gm, '<hr>')

  // 4. 列表项
  out = out.replace(/^(\s*)([-*+])\s+(.+)$/gm, '<li class="li-unordered">$3</li>')
  out = out.replace(/^(\s*)(\d+[.)])\s+(.+)$/gm, '<li class="li-ordered">$3</li>')
  out = out.replace(/((?:<li class="li-unordered">.*?<\/li>\s*)+)/gs, (_m, items: string) => {
    const clean = items.replace(/ class="li-unordered"/g, '')
    return `<ul>${clean}</ul>`
  })
  out = out.replace(/((?:<li class="li-ordered">.*?<\/li>\s*)+)/gs, (_m, items: string) => {
    const clean = items.replace(/ class="li-ordered"/g, '')
    return `<ol>${clean}</ol>`
  })

  // 5. 段落
  const lines = out.split('\n')
  const result: string[] = []
  let para: string[] = []
  const flush = () => {
    if (para.length) {
      const p = para.join(' ').trim()
      if (p) result.push(`<p>${p}</p>`)
      para = []
    }
  }
  for (const line of lines) {
    const trimmed = line.trim()
    const isBlock = trimmed.match(/^<(h[1-6]|ul|ol|li|blockquote|hr|p|table)/)
    if (isBlock) { flush(); result.push(line) }
    else if (trimmed === '') { flush() }
    else para.push(trimmed)
  }
  flush()
  out = result.join('\n')

  // 6. 行内格式（Vue 用 lookbehind 避免与 ** 冲突）
  out = out.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
  out = out.replace(/__(.+?)__/g, '<strong>$1</strong>')
  out = out.replace(/(?<!\*)\*(?!\*)(.+?)(?<!\*)\*(?!\*)/g, '<em>$1</em>')
  out = out.replace(/`([^`]+)`/g, '<code>$1</code>')

  // 7. 清理段落内多余 <br>
  out = out.replace(/<p>(.*?)<\/p>/gs, (_m, inner: string) => {
    const cleaned = inner.replace(/<br>\s*$/, '')
    return `<p>${cleaned}</p>`
  })

  return out
}

// ---- 工具函数（文件名/图标/类型标签/图片判定，行为同 Vue 版）----
// 从路径中解码并提取文件名字段
function getFileName(path: string): string {
  try { return decodeURIComponent(path.split('/').pop() || path.split('\\').pop() || path) } catch { return path }
}
function getFileIcon(path: string): string {
  const ext = path.split('.').pop()?.toLowerCase() || ''
  const map: Record<string, string> = { docx: 'W', doc: 'W', pptx: 'P', ppt: 'P', xlsx: 'X', xls: 'X', csv: 'X', pdf: '📄', md: '📝', txt: '📝' }
  return map[ext] || '📄'
}
function getFileTypeLabel(path: string): string {
  const ext = path.split('.').pop()?.toLowerCase() || ''
  const key = `msg.type.${ext}`
  const v = t(key)
  return v !== key ? v : t('msg.file')
}
function isImage(path: string): boolean {
  return /\.(png|jpg|jpeg|gif|webp|bmp)$/i.test(path)
}
function isDocx(path: string): boolean {
  return path.toLowerCase().endsWith('.docx')
}
// 获取语言展示名：优先用数据内 lang_names 映射，其次用 i18n，最后回退原始代码
function getLangName(data: ChatMessage['data'], lang: string): string {
  const names = (data as any)?.lang_names as Record<string, string> | undefined
  if (names && names[lang]) return names[lang]
  const localized = t(`lang.${lang}`)
  return localized !== `lang.${lang}` ? localized : lang
}

// MessageBubble 入参：message 为单条聊天消息；onFeedback 为点击反馈按钮时的回调
interface Props {
  message: ChatMessage
  onFeedback?: (m: ChatMessage) => void
}

// 默认导出组件：渲染单条聊天气泡，区分用户/AI、翻译结果表、附件预览与反馈入口
export default function MessageBubble({ message, onFeedback }: Props) {
  const isUser = message.role === 'user'
  const isAssistant = message.role === 'assistant'

  // 移动端标记（窗口宽度 ≤ 768px）
  const [isMobile, setIsMobile] = useState(typeof window !== 'undefined' && window.innerWidth <= 768)
  useEffect(() => {
    const onResize = () => setIsMobile(window.innerWidth <= 768)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  // ★ 整改 C1：blob URL 经鉴权拉取——裸 <img src>/<a href> 无法带 JWT 必 401
  // 缓存已通过鉴权拉取的附件 blob URL，避免重复请求
  const [blobUrls, setBlobUrls] = useState<Record<string, string>>({})
  const aliveRef = useRef(true)
  useEffect(() => () => { aliveRef.current = false }, [])

  // 带 JWT 鉴权下载附件并转为 blob URL；失败返回空串
  async function loadBlobUrl(fp: string): Promise<string> {
    if (blobUrls[fp]) return blobUrls[fp]
    try {
      const resp = await fetch(`${API_BASE}/api/download/?path=${encodeURIComponent(fp)}`, {
        headers: { Authorization: `Bearer ${getAuthToken()}` },
      })
      if (!resp.ok) return ''
      const url = URL.createObjectURL(await resp.blob())
      if (aliveRef.current) setBlobUrls((prev) => ({ ...prev, [fp]: url }))
      return url
    } catch { return '' }
  }

  // 下载并触发文件保存：复用以鉴权拉取的 blob URL，创建临时 <a> 执行下载
  async function downloadFile(fp: string) {
    const url = await loadBlobUrl(fp)
    if (!url) return
    const a = document.createElement('a')
    a.href = url
    a.download = getFileName(fp)
    document.body.appendChild(a)
    a.click()
    a.remove()
  }

  // 挂载即预取图片类产物（非图片点击时按需）
  // 消息附件中的图片提前拉取 blob URL，提升内联预览加载速度
  useEffect(() => {
    message.files?.forEach((f) => { if (isImage(f)) void loadBlobUrl(f) })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [message.files])

  // 译文表行：抽取翻译结果、来源标注与语言名，供翻译结果表渲染
  const transRows = useMemo(() => {
    const tr = (message.data as any)?.translations as Record<string, string> | undefined
    const sources = (message.data as any)?.translations_source as Record<string, string> | undefined
    if (!tr) return [] as { lc: string; name: string; v: string; src: string }[]
    return Object.entries(tr).filter(([, v]) => !!v)
      .map(([lc, v]) => ({ lc, name: getLangName(message.data, lc), v, src: sources?.[lc] || '' }))
  }, [message.data])

  const hasTranslations = transRows.length > 0

  // 匹配模式徽章（Vue：按中文数据串判定 exact/fuzzy/semantic/online）
  // 根据 message.data.mode 文本判定翻译命中模式并选择对应样式徽章
  const modeBadge = useMemo(() => {
    const mode = String((message.data as any)?.mode || '')
    if (!mode) return null
    if (mode.includes('精确命中')) return { label: t('msg.exactHit'), cls: 'mode-exact' }
    if (mode.includes('模糊')) return { label: t('msg.fuzzy'), cls: 'mode-fuzzy' }
    if (mode.includes('语义高相似')) return { label: t('msg.semantic'), cls: 'mode-semantic' }
    return { label: t('msg.online'), cls: 'mode-model' }
  }, [message.data])

  const matchedZh = String((message.data as any)?.matched_zh || '')
  const showMarkdown = !hasTranslations && !!message.content
  const html = useMemo(() => (showMarkdown ? renderMarkdown(message.content || '') : ''), [showMarkdown, message.content])

  const progress = message.progress
  const showProgress = isAssistant && !!progress && (progress.percent ?? 100) < 100

  return (
    <div className={`message-row ${message.role} ${isMobile ? 'msg-mobile' : ''}`}>
      {isAssistant && (
        <div className="avatar avatar-ai"><span className="avatar-text">AI</span></div>
      )}

      <div className="bubble">
        {/* 技能徽章 */}
        {isAssistant && message.skill && (
          <div className="bubble-badge"><SkillBadge skill={message.skill} /></div>
        )}

        {/* 翻译进度条 */}
        {showProgress && (
          <div className="progress-area">
            <div className="progress-header">
              <span className="progress-step">{progress!.step}</span>
              <span className="progress-percent">{progress!.percent}%</span>
            </div>
            <div className="progress-bar-bg">
              <div className="progress-bar-fill" style={{ width: `${progress!.percent}%` }} />
            </div>
          </div>
        )}

        {/* 翻译结果表 */}
        {hasTranslations && (
          <div className="translation-results">
            <div className="translation-mode">
              {modeBadge && <span className={`mode-badge ${modeBadge.cls}`}>{modeBadge.label}</span>}
              {matchedZh && <span className="mode-match-text">「{matchedZh}」</span>}
            </div>
            {transRows.map(({ lc, name, v, src }) => (
              <div className="lang-row" key={lc}>
                <span className="lang-label">{name}</span>
                <span className="lang-text">{v}</span>
                {src && (
                  <span className={`source-badge ${src === 'kb' ? 'source-kb' : 'source-model'}`}>
                    {src === 'kb' ? t('msg.kb') : t('msg.ai')}
                  </span>
                )}
              </div>
            ))}
            {/* 反馈入口：仅对翻译结果 */}
            <div className="msg-feedback-row">
              <Button size="small" variant="text" theme="primary" title={t('fb.entryTip')} onClick={() => onFeedback?.(message)}>
                💬 {t('fb.entry')}
              </Button>
            </div>
          </div>
        )}

        {/* 普通文本（Markdown） */}
        {showMarkdown && <div className="bubble-text" dangerouslySetInnerHTML={{ __html: html }} />}

        {/* 匹配度报告 */}
        {!!(message.data as any)?.match_report?.length && (
          <div className="match-report">
            <div className="report-title">{t('msg.termReport')}</div>
            <div className="report-grid">
              {(message.data as any).match_report.map((item: any, i: number) => (
                <div className="report-item" key={i}>
                  <span className="report-status">{item.status}</span>
                  <span className="report-lang">{getLangName(message.data, item.lang)}</span>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* 附件：图片内联 / 文件卡 */}
        {!!message.files?.length && (
          <div className="file-downloads">
            {message.files.map((f) =>
              isImage(f) ? (
                <div key={f} className="image-preview">
                  {blobUrls[f]
                    ? <img className="preview-img" src={blobUrls[f]} alt={getFileName(f)}
                           onClick={() => window.open(blobUrls[f], '_blank')} />
                    : <div className="preview-img preview-loading">…</div>}
                  <a href="javascript:void(0)" className="image-download-btn" onClick={(e) => { e.preventDefault(); void downloadFile(f) }}>
                    {t('msg.downloadImage')}
                  </a>
                </div>
              ) : (
                <div key={f} className={`download-card ${isDocx(f) ? 'download-card-docx' : 'download-card-md'}`}>
                  <div className="card-icon">
                    {['W', 'P', 'X'].includes(getFileIcon(f)) ? (
                      <span className={
                        getFileIcon(f) === 'W' ? 'icon-docx' : getFileIcon(f) === 'P' ? 'icon-pptx' : 'icon-xlsx'
                      }>{getFileIcon(f)}</span>
                    ) : (
                      <span className="icon-file">{getFileIcon(f)}</span>
                    )}
                  </div>
                  <div className="card-info">
                    <div className="card-filename">{getFileName(f)}</div>
                    <div className="card-meta">{getFileTypeLabel(f)} {t('msg.clickDownload')}</div>
                  </div>
                  <a href="javascript:void(0)" className="card-btn" onClick={(e) => { e.preventDefault(); void downloadFile(f) }}>📥</a>
                </div>
              ),
            )}
          </div>
        )}
      </div>

      {isUser && (
        <div className="avatar avatar-user"><span className="avatar-text">{t('msg.me')}</span></div>
      )}
    </div>
  )
}
