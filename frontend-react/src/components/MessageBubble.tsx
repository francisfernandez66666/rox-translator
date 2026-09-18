// ============================================================================
// components/MessageBubble.tsx — 聊天气泡（等价 Vue MessageBubble.vue）
// 能力：Markdown 渲染（h1–h6 / **bold** / *em* 含 lookbehind）、技能徽章、
//      翻译进度条、多语言译文表（模式/来源徽章）、match_report、
//      附件图片内联预览 + 全类型下载（blob 鉴权）、反馈入口。
// ============================================================================
import { useEffect, useMemo, useRef, useState } from 'react'
import { API_BASE, getAuthToken } from '@/api'
import type { ChatMessage } from '@/types'
import { t } from '@/i18n'
import { renderMarkdown } from '@/lib/markdown' // ★ F11：渲染纯函数抽提至 lib/markdown
import { SkillBadge } from './SkillBadge'

// ============ 本文件职责中文说明 ============
// 聊天气泡组件：渲染单条消息（Markdown、译文表、附件预览、反馈入口）。
// ========================================

// 从路径中提取文件名（兼容 / 与 \ 分隔符，尽量 URL 解码）
function getFileName(path: string): string {
  try { return decodeURIComponent(path.split('/').pop() || path.split('\\').pop() || path) } catch { return path }
}
// getFileIcon 根据文件扩展名返回对应的展示徽标字符（Word/PPT/Excel 用字母 W/P/X）
// pdf/md/txt 返回空串：纯黑 UI 全站禁 emoji（原 📄/📝 已废），这类文件走 .icon-file 线条样式
function getFileIcon(path: string): string {
  const ext = path.split('.').pop()?.toLowerCase() || ''
  const map: Record<string, string> = { docx: 'W', doc: 'W', pptx: 'P', ppt: 'P', xlsx: 'X', xls: 'X', csv: 'X', pdf: '', md: '', txt: '' }
  return map[ext] || ''
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

// 翻译检查点动效用的极简延时（仅编排时序，不进业务逻辑）
function cpSleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms))
}

// MessageBubble 入参：message 为单条聊天消息；onFeedback 为点击反馈按钮时的回调
// source（双语对照用的上一条原文）只在渲染期需要，故在组件签名处内联扩展、不进 Props
interface Props {
  message: ChatMessage
  onFeedback?: (m: ChatMessage) => void
}

// 默认导出组件：渲染单条聊天气泡，区分用户/AI、翻译结果表、附件预览与反馈入口
export default function MessageBubble({ message, onFeedback, source }: Props & { source?: string }) {
  // 移动端标记（窗口宽度 ≤ 768px）；typeof window 兜底：SSR/单测环境无 window，初值不能直接读
  const [isMobile, setIsMobile] = useState(typeof window !== 'undefined' && window.innerWidth <= 768)
  const [copied, setCopied] = useState(false) // ★ F7 复制反馈
  const isAssistant = message.role === 'assistant'
  const isUser = message.role === 'user'

  // 移动端标记（窗口宽度 ≤ 768px）
  useEffect(() => {
    const onResize = () => setIsMobile(typeof window !== 'undefined' && window.innerWidth <= 768)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  // ★ 整改 C1：blob URL 经鉴权拉取——裸 <img src>/<a href> 无法带 JWT 必 401
  // 缓存已通过鉴权拉取的附件 blob URL，避免重复请求
  const [blobUrls, setBlobUrls] = useState<Record<string, string>>({})
  const aliveRef = useRef(true)
  useEffect(() => () => { aliveRef.current = false }, [])

  // 翻译检查点动效编排状态（移植 hero-stream.html 的 MOT 峰值逻辑，遵循 §2.4 原则 2/3/4/5/8/9）。
  // 只加视觉/动效，不改动任何后端字段解析、API、i18n 文案或进度数值来源。
  const areaRef = useRef<HTMLDivElement>(null)   // 整块进度区
  const prevStepRef = useRef<string | null>(null) // 上一次检查点 step
  const firstStepRef = useRef(true)               // 首次见到不触发峰值时序
  const genRef = useRef(0)                        // generation 计数器：中止上一轮、避免叠加/竞态

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
  useEffect(() => {
    message.files?.forEach((f) => { if (isImage(f)) void loadBlobUrl(f) })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [message.files])

  // ★ 翻译检查点动效（移植 hero-stream.html 的 MOT 峰值逻辑，遵循 §2.4 原则 2/3/4/5/8/9）
  // 触发点：progress.step（检查点）切换的一刻 —— 整块先压暗 brightness(.84)（沉慢 .5s），
  // 再提亮到峰值时刻（放快 .28s + 闪到 1.6，峰值约 90ms，可截出明显最亮静帧），
  // 随后检查点文字「三连点亮」：step/lang/detail 等速 260ms 步进点亮。
  // ⚠ 现状核对（2026-09-18 气泡重写后）：进度区已不再输出 .cp-light 节点、.cp-dim/.cp-peaking
  //   也还没有对应 CSS 规则，检查点名字改由行内 color transition 过渡——本 effect 因此暂无可见效果。
  //   保留此处即保留动效规格（时长/节拍/收放不对称），补回类名与样式即可复原，不要照「无效果」删掉。
  useEffect(() => {
  const progress = message.progress
    if (!progress) return
    const step = progress.step
    const prefersReduced = typeof window !== 'undefined' && !!window.matchMedia &&
      window.matchMedia('(prefers-reduced-motion: reduce)').matches
    const area = areaRef.current
    if (prefersReduced) {
      // 偏好减少动效：直接到终态，不做任何过渡，并清掉可能残留的临时 class
      if (area) area.classList.remove('cp-dim', 'cp-peaking')
      area?.querySelectorAll('.cp-light').forEach((n) => n.classList.remove('cp-pre', 'cp-lit'))
      prevStepRef.current = step
      firstStepRef.current = false
      return
    }
    // 首次见到该检查点：仅记录，不触发峰值时序（翻译刚起步，不是检查点完成瞬间）
    if (firstStepRef.current) { firstStepRef.current = false; prevStepRef.current = step; return }
    if (prevStepRef.current === step) return // 同一步内 percent 变化不重触发
    const g = ++genRef.current // 新检查点：新 generation，旧轮次自然作废
    void (async () => {
      await cpSleep(0) // 先让本帧渲染/test 跑完，避免同步改 DOM（测试中不推进定时器则全程不落 DOM）
      if (genRef.current !== g) return
      const el = areaRef.current
      if (!el) return
      const lights = Array.from(el.querySelectorAll<HTMLElement>('.cp-light'))
      el.classList.add('cp-dim')   // 峰值前整块压暗，做落差（原则 4）
      await cpSleep(500)           // 沉慢
      if (genRef.current !== g) return
      el.classList.remove('cp-dim')
      el.classList.add('cp-peaking') // 放快：提亮到峰值（约 90ms 明显最亮静帧）
      await cpSleep(280)
      if (genRef.current !== g) return
      el.classList.remove('cp-peaking')
      lights.forEach((n) => n.classList.remove('cp-lit'))
    })()
    prevStepRef.current = step
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [message.progress])

  // 卸载时中止上一轮动效（generation 失效，任何在途的 await 后续都不再落 DOM）
  useEffect(() => () => { genRef.current++ }, [])

  // 译文表行：抽取翻译结果、来源标注与语言名，供翻译结果表渲染
  // ⚠ 来源键名两条链路不一致：工单/orchestrator 的 ticketPayload 用 "sources"，
  //   即时翻译文本链路由 engine.TextTranslateData 序列化、其键为 "translations_source"
  //   （见 types/index.ts 的声明）。取不到值时只是不渲染来源徽标，不会报错——改这里前先确认口径
  const transRows = useMemo(() => {
    const tr = (message.data as any)?.translations as Record<string, string> | undefined
    const sources = (message.data as any)?.sources as Record<string, string> | undefined
    if (!tr) return [] as { lc: string; name: string; v: string; src: string }[]
    return Object.entries(tr).filter(([, v]) => !!v)
      .map(([lc, v]) => ({ lc, name: getLangName(message.data, lc), v, src: sources?.[lc] || '' }))
  }, [message.data])
  const hasTranslations = transRows.length > 0

  // 匹配模式徽章（Vue：按中文数据串判定 exact/fuzzy/semantic/online）
  // 根据 message.data.mode 文本判定翻译命中模式并选择对应样式徽章
  // 兜底走 online：mode 缺失/为空时也保证译文表头部有一枚徽章，不再返回 null 留空档
  const modeBadge = useMemo(() => {
    const mode = String((message.data as any)?.mode || '')
    if (mode.includes('精确命中')) return { label: t('msg.exactHit'), cls: 'mode-exact' }
    if (mode.includes('模糊')) return { label: t('msg.fuzzy'), cls: 'mode-fuzzy' }
    if (mode.includes('语义高相似')) return { label: t('msg.semantic'), cls: 'mode-semantic' }
    return { label: t('msg.online'), cls: 'mode-model' }
  }, [message.data])

  const matchedZh = String((message.data as any)?.matched_zh || '')
  const progress = message.progress
  // 进度态判据从「percent<100」放宽为「有 progress」：useChat 在 done/error/停止时都会把
  // progress 置 undefined，靠字段本身收尾比靠数值更可靠（也不会 99%→100% 瞬间量尺直接消失）
  const showProgress = isAssistant && !!progress
  // 三态互斥：有译文表就只出译文表；进度在就只出量尺，避免同一气泡叠两套呈现
  const showMarkdown = !!message.content && !hasTranslations && !showProgress
  const html = useMemo(() => (showMarkdown ? renderMarkdown(message.content || '') : ''), [showMarkdown, message.content])

  // 外层 .lc-mo-up：气泡进场只动 opacity/transform，motion.css 里已带 prefers-reduced-motion 兜底
  return (
    <div className={`message-row ${message.role} ${isMobile ? 'msg-mobile' : ''} lc-mo-up`}>
      {isAssistant && (
        <div className="avatar avatar-ai"><span className="avatar-text">AI</span></div>
      )}

      <div className="bubble" dir="auto">
      {/* ★ F3：气泡整体按内容方向（RTL 语言镜像） */}
        {/* 技能徽章 */}
        {isAssistant && message.skill && (
          <div className="bubble-badge"><SkillBadge skill={message.skill} /></div>
        )}

        {/* 翻译检查点进度（带检查点名字的三关量尺：术语检索 / 机器翻译 / 术语校准）*/}
        {showProgress && (
          <div className="progress-area" ref={areaRef}>
              {(() => {
              // 后端进度帧理论上可能越界/缺省，先夹到 0–100 再算视觉，防止宽度/左偏移出现负值
              const pct = Math.max(0, Math.min(100, progress!.percent ?? 0))
              // 三关分段（每关约 1/3）：100% 记作 idx=3，即三格全过、无「正在走」的高亮格
              const idx = pct >= 100 ? 3 : pct < 34 ? 0 : pct < 67 ? 1 : 2
              const names = [t('chat.cpRetrieve'), t('chat.cpTranslate'), t('chat.cpCalibrate')]
              const done = pct >= 100
              return (
                <div style={{ marginBottom: 10 }}>
                  {/* 三处检查点名字（序号 + 中文原文）：已过的转亮，正在走的转白 */}
                  <div style={{ display: 'flex', gap: 10, marginBottom: 8, flexWrap: 'wrap' }}>
                    {names.map((nm, i) => {
                      const lit = i < idx
                      const cur = !done && i === idx
                      return (
                        <span key={nm} style={{
                          fontSize: 12, lineHeight: '16px', letterSpacing: '.02em',
                          color: cur ? '#E7E9EA' : lit ? '#C8CCD1' : '#3F444B',
                          display: 'inline-flex', alignItems: 'baseline', gap: 6,
                          transition: 'color .5s ease',
                        }}>
                          <i style={{
                            fontStyle: 'normal', fontFamily: '"Inter","SF Pro Text",Arial,sans-serif',
                            fontSize: 10, fontWeight: 600,
                            color: cur ? '#FFFFFF' : lit ? '#C8CCD1' : '#33383F',
                            transition: 'color .5s ease',
                          }}>{String(i + 1).padStart(2, '0')}</i>
                          {nm}
                  </span>
                      )
                    })}
                  </div>
                  {/* 量尺本体：一根细丝 + 一枚会走的针头（针头停在哪儿、哪一格正在被校准）*/}
                  <div style={{ position: 'relative', height: 12, display: 'flex', alignItems: 'center' }}>
                    <div style={{
                      position: 'absolute', left: 0, top: '50%', height: 2, marginTop: -1, width: `${pct}%`,
                      borderRadius: 2, background: 'linear-gradient(90deg, rgba(255,255,255,.30), #FFFFFF)',
                      boxShadow: '0 0 10px rgba(255,255,255,.40)',
                      transition: 'width 1.05s cubic-bezier(.22,1,.28,1)',
                    }} />
                    <div style={{
                      position: 'absolute', top: '50%', width: 3, height: 12, marginTop: -6, marginLeft: -1.5, left: `${pct}%`,
                      borderRadius: 2, background: '#FFFFFF',
                      transition: 'left 1.05s cubic-bezier(.22,1,.28,1)',
                    }} />
                  </div>
                  {/* 当前步骤文案 + 百分比 */}
                  <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 6, fontSize: 12 }}>
                    <span style={{ color: '#9AA0AA' }}>{done ? t('chat.cpDone') : (progress!.step || t('chat.cpTranslate'))}</span>
                    <span style={{ color: '#E7E9EA', fontFamily: '"JetBrains Mono",monospace', fontWeight: 600 }}>{pct}%</span>
                  </div>
                </div>
              )
              })()}
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
              <button type="button" className="msg-fb-btn" title={t('fb.entryTip')} onClick={() => onFeedback?.(message)}
                      style={{ border: 'none', background: 'none', padding: 0, font: 'inherit', fontSize: 12, color: '#E7E9EA', cursor: 'pointer' }}>
                 {t('fb.entry')}
              </button>
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
              <button type="button" aria-label={t('msg.copy')} style={{ border: 'none', background: 'none', padding: 0, font: 'inherit', fontSize: 12, color: '#E7E9EA', cursor: 'pointer' }}
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
                  {/* 空按钮体：📥 字形按全站禁 emoji 规则移除，下载指示符需由 .card-btn 的 CSS 补 */}
                  <button type="button" className="card-btn" onClick={() => void downloadFile(f)}></button>
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
