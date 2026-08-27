// components/SiteFooter.tsx — 全站页脚（前台 + 后台共用）
// 品牌与页脚链接：品牌按访问域名解析（租户级）；页脚链接为平台级（超管设置，对所有租户生效）。
import { useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { useBranding, DEFAULT_BRAND_NAME } from '@/branding'
import { footerLinksGet, BrandLink } from '@/api/branding'

// 默认导出组件：全站页脚，按访问域名解析品牌并展示平台级页脚链接（无链接时回退默认协议入口）
export default function SiteFooter() {
  const [lang] = useT()
  const branding = useBranding()
  const brand = branding.brandName || (lang === 'zh' ? DEFAULT_BRAND_NAME : 'LangCross')
  const terms = lang === 'zh' ? '用户协议' : 'User Agreement'
  const privacy = lang === 'zh' ? '隐私协议' : 'Privacy Policy'
  const [links, setLinks] = useState<BrandLink[]>([])

  useEffect(() => {
    footerLinksGet()
      .then((j) => { if (j.success && Array.isArray(j.links)) setLinks(j.links) })
      .catch(() => {})
  }, [])

  return (
    <footer
      style={{
        marginTop: 'auto',
        padding: '14px 20px',
        borderTop: '1px solid #eceff3',
        background: '#fff',
        color: '#8a9099',
        fontSize: 13,
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'center',
        gap: 8,
      }}
    >
      <span>© 2026 {brand} · 翻译平台</span>
      <span style={{ opacity: 0.5 }}>·</span>
      {/* 平台级页脚链接优先；否则回退到《用户协议》《隐私协议》 */}
      {links.length > 0 ? (
        links.map((l, i) => (
          <span key={i} style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
            <a href={l.url} target="_blank" rel="noreferrer" style={{ color: '#5f6b7a' }}>
              {lang === 'zh' ? l.label : (l.label_en || l.label)}
            </a>
            <span style={{ opacity: 0.5 }}>·</span>
          </span>
        ))
      ) : (
        <>
          <a href="/docs/terms" target="_blank" rel="noreferrer" style={{ color: '#5f6b7a' }}>{terms}</a>
          <span style={{ opacity: 0.5 }}>·</span>
          <a href="/docs/privacy" target="_blank" rel="noreferrer" style={{ color: '#5f6b7a' }}>{privacy}</a>
        </>
      )}
    </footer>
  )
}
