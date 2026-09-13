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
import { renderMarkdown } from '@/lib/markdown' // ★ F11：渲染纯函数抽提至 lib/markdown
// 从路径中提取文件名（兼容 / 与 \ 分隔符，尽量 URL 解码）
function getFileName(path: string): string {
  try { return decodeURIComponent(path.split('/').pop() || path.split('\\').pop() || path) } catch { return path }
}
// getFileIcon 根据文件扩展名返回对应的展示图标（如 Word/PPT/Excel/PDF/文本等）
function getFileIcon(path: string): string {
  const ext = path.split('.').pop()?.toLowerCase() || ''
  const map: Record<string, string> = { docx: 'W', doc: 'W', pptx: 'P', ppt: 'P', xlsx: 'X', xls: 'X', csv: 'X', pdf: '📄', md: '📝', txt: '📝' }
  return map[ext] || '📄'
}
// getFileTypeLabel 返回文件类型中文标签（Word/PPT/Excel/PDF 等）
function getFileTypeLabel(path: string): string {
  const ext = path.split('.').pop()?.toLowerCase() || ''
  const key = `msg.type.${ext}`
  const v = t(key)
  return v !== key ? v : t('msg.file')
}
// isImage 判断路径是否为常见图片格式
function isImage(path: string): boolean {
  return /\.(png|jpg|jpeg|gif|webp|bmp)$/i.test(path)
}
// isDocx 判断路径是否以 .docx 结尾（Word 文档）
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
export default function MessageBubble({ message, onFeedback, source }: Props & { source?: string }) {
  const isUser = message.role === 'user'
  const [copied, setCopied] = useState(false) // ★ F7 复制反馈
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

      {/* ★ F3：气泡整体按内容方向（RTL 语言镜像） */}
      <div className="bubble" dir="auto">
        {/* 技能徽章 */}
        {isAssistant && message.skill && (
          <div className="bubble-badge"><SkillBadge skill={message.skill} /></div>
        )}

        {/* 翻译进度条 */}
        {showProgress && (
          <div className="progress-area">
            <div className="progress-header">
              {/* 解析 step 格式：file_translate|初翻|en 或 第2步/3：翻译英文（45/120） */}
              {(() => {
                const step = progress!.step || ''
                const parts = step.split('|')
                if (parts.length === 3 && parts[0] === 'file_translate') {
                  // 文件翻译细粒度进度：阶段 + 语言
                  const phase = parts[1] // 初翻/校对
                  const lang = parts[2] // en/ja/ko 等
                  const langName = lang.toUpperCase()
                  return (
                    <>
                      <span className="progress-step">{phase}</span>
                      <span className="progress-lang">{langName}</span>
                      <span className="progress-detail">{progress!.done}/{progress!.total} 段</span>
                    </>
                  )
                }
                // 通用进度：直接显示 step 文案
                return <span className="progress-step">{step}</span>
              })()}
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
        {showMarkdown && <div dir="auto" className="bubble-text" dangerouslySetInnerHTML={{ __html: html }} />}

        {/* ★ F7：双语对照（折叠显示原文）+ 一键复制译文 */}
        {isAssistant && (source || message.content) && (
          <div className="msg-srcbar" style={{ display: 'flex', gap: 10, alignItems: 'center', marginTop: 6 }}>
            {!!source && (
              <details>
                <summary style={{ fontSize: 12, color: '#889', cursor: 'pointer' }}>{t('msg.showSrc')}</summary>
                <div dir="auto" style={{ fontSize: 12, color: '#667', whiteSpace: 'pre-wrap', marginTop: 4, padding: '4px 8px', background: 'rgba(128,128,128,.08)', borderRadius: 4 }}>{source}</div>
              </details>
            )}
            {!!message.content && (
              <button type="button" aria-label={t('msg.copy')} style={{ border: 'none', background: 'none', padding: 0, font: 'inherit', fontSize: 12, color: '#4a7dff', cursor: 'pointer' }}
                      onClick={() => { void navigator.clipboard?.writeText(message.content || ''); setCopied(true); window.setTimeout(() => setCopied(false), 1500) }}>
                {copied ? t('msg.copied') : t('msg.copy')}
              </button>
            )}
          </div>
        )}

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
                  <button type="button" className="image-download-btn" onClick={() => void downloadFile(f)}>
                    {t('msg.downloadImage')}
                  </button>
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
                  <button type="button" className="card-btn" onClick={() => void downloadFile(f)}>📥</button>
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
