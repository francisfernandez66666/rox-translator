// components/SiteFooter.tsx — 全站页脚（前台 + 后台共用）
// 品牌与页脚链接：品牌按访问域名解析（租户级）；页脚链接为平台级（超管设置，对所有租户生效）。
import { useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { useBranding, DEFAULT_BRAND_NAME } from '@/branding'
import { footerLinksGet, BrandLink } from '@/api/branding'

// ============ 本文件职责中文说明 ============
// 全站页脚组件：按访问域名解析品牌并展示平台级页脚链接。
// ========================================
//
// ★ 为什么本文件仍留着 #575F6C / #8a9099 / #5f6b7a 这些写死灰值而没有换成 var(--lc-*)：
//   src/styles/readability.test.ts 的两条锁（次级灰禁写死 / 描边禁写死暗值）共用一份 EXEMPT
//   白名单，SiteFooter.tsx 在列——那两条锁的口径是「只管登录后的界面」，与 Landing/Pricing/Login
//   同批放行（本组件的两个渲染点是 App 抽屉页脚与 AdminDashboard 页脚，都属页脚条这类装饰性弱文字）。
//   代价是这里不会随令牌换档自动跟随：日后调 --lc-border-faint / --lc-text-* 时，
//   若要让页脚同变，得手工把这三处字面值一起对一遍。

// 默认导出组件：全站页脚，按访问域名解析品牌并展示平台级页脚链接（无链接时回退默认协议入口）
export default function SiteFooter() {
  const [lang] = useT()
  const branding = useBranding()
  // 品牌名三级兜底：域名解析出的租户品牌 → 按语言的内置默认名（中文站用中文名、英文站用 LangCross）
  const brand = branding.brandName || (lang === 'zh' ? DEFAULT_BRAND_NAME : 'LangCross')
  // ⚠ 这两个词是就地硬编码的短标签，没走 auth.userAgreement / auth.privacyPolicy 词典键
  //   （那两条带书名号、页脚不带），改文案时记得两处一起看
  const terms = lang === 'zh' ? '用户协议' : 'User Agreement'
  const privacy = lang === 'zh' ? '隐私协议' : 'Privacy Policy'
  const [links, setLinks] = useState<BrandLink[]>([])

  // 页脚链接是平台级配置（超管设置、所有租户共用），挂载时拉一次即可，无需轮询；拉不到就留空走默认协议入口
  useEffect(() => {
    footerLinksGet()
      .then((j) => { if (j.success && Array.isArray(j.links)) setLinks(j.links) })
      .catch(() => {})
  }, [])

  return (
    <footer
      style={{
        // 粘性页脚：App 外壳是 minHeight:100vh 的 flex 列，auto 上边距把页脚顶到列尾，
        // 内容不足一屏时也不会半途悬着
        marginTop: 'auto',
        padding: '14px 20px',
        // 配色对齐深色外壳（#0E1014 与 App.tsx 的 --npz-page-bg 兜底色同一支），
        // 分隔线只留最浅一档描边，避免页脚比正文还抢眼
        borderTop: '1px solid #575F6C',
        background: '#0E1014',
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
          // key 用数组下标：后端整串替换 links，不存在单条重排，索引键不会引起错位
          <span key={i} style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
            {/* target=_blank 的外链一律补 rel=noreferrer，避免 Referer 把租户域名带给第三方站点 */}
            {/* 中英双标签：非中文语种下 label_en 可能没填（后台只强制中文名），
                故 `l.label_en || l.label` 宁可用中文名也不渲染一个空链接 */}
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
