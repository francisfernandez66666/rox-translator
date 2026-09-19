// ============================================================================
// components/LangSelect.tsx — 界面语言下拉切换器（★ #23，2026-09-19）
// 取代旧的 zh/en 二元 toggleLang 按钮：顶栏 / 登录卡 / 管理后台三处共用。
// 选项永远用各语言自称（native），当前语种打勾；选完即 setLang 持久化。
// 词表与语种代码唯一来源是 @/i18n 的 LANG_OPTIONS——新语种进表即进菜单。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import { LANG_OPTIONS, setLang, useT } from '@/i18n'
import { GlobeIcon } from '@/ui/langcross/src'

/** props.align='right'：菜单向触发钮左侧收拢，顶栏右端用，防窄屏溢出视口 */
export function LangSelect({ align = 'right' }: { align?: 'left' | 'right' }) {
  const [lang, t] = useT()
  const [open, setOpen] = useState(false)
  const wrapRef = useRef<HTMLDivElement>(null)
  const current = LANG_OPTIONS.find((o) => o.code === lang) ?? LANG_OPTIONS[0]

  // 外点/Esc 收起：菜单是小部件不做焦点陷阱，但点哪儿都能关是底线体验
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div className="lang-sel" ref={wrapRef}>
      <button
        type="button"
        className={`lang-sel-btn${open ? ' is-open' : ''}`}
        aria-label={t('app.langSwitch')}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <GlobeIcon size={14} />
        <span className="lang-sel-cur">{current.native}</span>
      </button>
      {open && (
        <div className={`lang-sel-menu${align === 'right' ? ' lang-sel-menu--right' : ''}`} role="listbox" aria-label={t('app.langSwitch')}>
          {LANG_OPTIONS.map((o) => (
            <button
              key={o.code}
              type="button"
              role="option"
              aria-selected={o.code === lang}
              className={`lang-sel-item${o.code === lang ? ' is-cur' : ''}`}
              onClick={() => { setLang(o.code); setOpen(false) }}
            >
              <span className="lang-sel-native">{o.native}</span>
              {o.code === lang && <span className="lang-sel-check" aria-hidden="true">✓</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
