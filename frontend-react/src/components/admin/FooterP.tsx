// ============================================================================
// components/admin/FooterP.tsx — 平台级页脚链接（仅超管）
// 职责：维护对所有租户统一生效的页脚链接，页脚链接与租户无关。
// 2026-09-18（UI 融合）：面板控件整体从 TDesign 迁到 ui/langcross ——
//   Button/Icon/Link 取组件库，输入框改原生 input + lc-input 样式，
//   提示改走 lib/toastBus（toastSuccess/toastError，不再直连 MessagePlugin）；
//   行内删除由「✕ 文本按钮」改为 Link + <Icon n="close">，无障碍名挂在外层 span 上。
// ============================================================================
import { useEffect, useState } from 'react'
import { Button, Icon, Link } from '@/ui/langcross/src'
import { toastSuccess, toastError } from '@/lib/toastBus'
import { useT } from '@/i18n'
import { Panel } from './parts'
import { footerLinksGet, footerLinksSet, BrandLink } from '@/api/branding'

/** 平台级页脚链接面板组件（仅超管）：维护对所有租户统一生效的页脚链接列表 */
export default function FooterP() {
  const [, t] = useT()
  // 页脚链接列表状态
  const [links, setLinks] = useState<BrandLink[]>([])
  const [saving, setSaving] = useState(false)
  const [loaded, setLoaded] = useState(false)

  // 加载页脚链接数据
  useEffect(() => {
    let alive = true
    footerLinksGet()
      .then((j) => {
        if (!alive || !j.success) return
        setLinks(Array.isArray(j.links) ? j.links : [])
        setLoaded(true)
      })
      .catch(() => setLoaded(true))
    return () => { alive = false }
  }, [])

  /** 保存页脚链接：将当前链接列表序列化为 JSON 并提交 */
  const save = async () => {
    setSaving(true)
    try {
      const j = await footerLinksSet(JSON.stringify(links))
      if (j.success) toastSuccess(t('brand.saved'))
      else toastError(j.message || 'error')
    } catch (e: any) {
      toastError(e?.message || 'error')
    } finally {
      setSaving(false)
    }
  }

  /** 修改第 i 条链接的指定字段（label/label_en/url） */
  const setLink = (i: number, k: keyof BrandLink, v: string) => {
    setLinks((arr) => arr.map((l, idx) => (idx === i ? { ...l, [k]: v } : l)))
  }
  /** 新增一条空白链接 */
  const addLink = () => setLinks((arr) => [...arr, { label: '', label_en: '', url: '' }])
  /** 删除第 i 条链接 */
  const removeLink = (i: number) => setLinks((arr) => arr.filter((_, idx) => idx !== i))

  return (
    <Panel title={t('footer.title')}>
      <p style={{ fontSize: 13, color: 'var(--adm-hint)', marginBottom: 12 }}>{t('footer.hint')}</p>
      {!loaded ? (
        <div style={{ color: 'var(--adm-faint)' }}>…</div>
      ) : (
        <div style={{ maxWidth: 640, display: 'flex', flexDirection: 'column', gap: 14 }}>
          {/* 链接列表：每行包含中文标签、英文标签、URL 与删除按钮 */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, width: '100%' }}>
            {links.map((l, i) => (
              <div key={i} style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                <input className="lc-input" value={l.label} onChange={(e) => setLink(i, 'label', e.target.value)} placeholder={t('brand.linkLabel')} style={{ width: 140 }} />
                <input className="lc-input" value={l.label_en} onChange={(e) => setLink(i, 'label_en', e.target.value)} placeholder={t('brand.linkLabelEn')} style={{ width: 140 }} />
                <input className="lc-input" value={l.url} onChange={(e) => setLink(i, 'url', e.target.value)} placeholder={t('brand.linkUrl')} style={{ flex: 1 }} />
                {/* aria-label 必须挂在 button 本身（Link 已透传）：挂外层 span 不给内部按钮命名，axe button-name 仍判 critical */}
                <Link tone="danger" onClick={() => removeLink(i)} aria-label={t('common.delete')}><Icon n="close" /></Link>
              </div>
            ))}
          </div>
          {/* 新增链接按钮 */}
          <Button size="sm" variant="secondary" onClick={addLink}>+ {t('brand.addLink')}</Button>
          {/* 保存按钮
              ui/langcross Button 无 loading 属性（不像 TDesign），故用 disabled={saving}
              表达「提交中不可重复点」，避免连点重复写库。 */}
          <div>
            <Button variant="primary" disabled={saving} onClick={save}>{t('brand.save')}</Button>
          </div>
        </div>
      )}
    </Panel>
  )
}
