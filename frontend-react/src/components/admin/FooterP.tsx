// ============================================================================
// components/admin/FooterP.tsx — 平台级页脚链接（仅超管）
// 职责：维护对所有租户统一生效的页脚链接，页脚链接与租户无关。
// ============================================================================
import { useEffect, useState } from 'react'
import { Button, Input, MessagePlugin, Space } from 'tdesign-react'
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
      if (j.success) MessagePlugin.success(t('brand.saved'))
      else MessagePlugin.error(j.message || 'error')
    } catch (e: any) {
      MessagePlugin.error(e?.message || 'error')
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
      <p style={{ fontSize: 13, color: '#667', marginBottom: 12 }}>{t('footer.hint')}</p>
      {!loaded ? (
        <div style={{ color: '#889' }}>…</div>
      ) : (
        <div style={{ maxWidth: 640, display: 'flex', flexDirection: 'column', gap: 14 }}>
          {/* 链接列表：每行包含中文标签、英文标签、URL 与删除按钮 */}
          <Space direction="vertical" style={{ width: '100%' }} size={8}>
            {links.map((l, i) => (
              <div key={i} style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                <Input value={l.label} onChange={(v) => setLink(i, 'label', v)} placeholder={t('brand.linkLabel')} style={{ width: 140 }} />
                <Input value={l.label_en} onChange={(v) => setLink(i, 'label_en', v)} placeholder={t('brand.linkLabelEn')} style={{ width: 140 }} />
                <Input value={l.url} onChange={(v) => setLink(i, 'url', v)} placeholder={t('brand.linkUrl')} style={{ flex: 1 }} />
                <Button size="small" variant="text" theme="danger" onClick={() => removeLink(i)}>✕</Button>
              </div>
            ))}
          </Space>
          {/* 新增链接按钮 */}
          <Button size="small" variant="outline" theme="primary" onClick={addLink}>+ {t('brand.addLink')}</Button>
          {/* 保存按钮 */}
          <div>
            <Button theme="primary" loading={saving} onClick={save}>{t('brand.save')}</Button>
          </div>
        </div>
      )}
    </Panel>
  )
}
