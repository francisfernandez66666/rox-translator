// components/admin/BrandP.tsx — 品牌定制面板（租户管理员 / 超管）
// 仅含：品牌名称、Logo（图片上传+预览）、子域名前缀。页脚链接为平台级，见 FooterP。
// 鉴权：品牌定制为付费套餐功能，需持有有效付费套餐（且未过期）才能编辑；
// 未付费/已过期的租户仅展示不可编辑，并提示「套餐付费功能」。超管拥有覆盖权。
import { useEffect, useState } from 'react'
import { Button, Input, MessagePlugin, Switch, Tag } from 'tdesign-react'
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'
import { Panel } from './parts'
import { tenantBranding, tenantBrandingSave, brandGrant } from '@/api/branding'

// 品牌定制面板组件：编辑当前（或超管所选）租户的品牌名称、Logo、子域名前缀，受套餐付费状态限制
export default function BrandP() {
  const ad = useAdmin()
  const [, t, tpl] = useT()
  const isSuper = ad.myLevel >= 4
  // 超管编辑「切换器」当前租户（平台根=rox=ID 1）；租户管理员不传 id，仅本租户
  const targetTenantId = isSuper ? ad.activeTenantId || 1 : 0

  const [name, setName] = useState('')
  const [logo, setLogo] = useState('')
  const [domain, setDomain] = useState('')
  const [homeBg, setHomeBg] = useState('')
  const [saving, setSaving] = useState(false)
  const [loaded, setLoaded] = useState(false)
  // 后端回传：当前编辑租户是否已购有效付费套餐 / 是否被超管授权（二者任一即可编辑）
  const [brandPaid, setBrandPaid] = useState(false)
  const [brandGranted, setBrandGranted] = useState(false)
  const [granting, setGranting] = useState(false)
  const editable = isSuper || brandPaid || brandGranted

  useEffect(() => {
    let alive = true
    setLoaded(false)
    tenantBranding(targetTenantId || undefined)
      .then((j) => {
        if (!alive || !j.success) return
        setName(j.brand_name || '')
        setLogo(j.brand_logo || '')
        setDomain(j.domain || '')
        setHomeBg(j.brand_home_bg || '')
        setBrandPaid(!!j.brand_paid)
        setBrandGranted(!!j.brand_granted)
        setLoaded(true)
      })
      .catch(() => setLoaded(true))
    return () => { alive = false }
  }, [targetTenantId])

  const onLogoFile = (e: any) => {
    const file: File | undefined = e?.target?.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => setLogo(String(reader.result))
    reader.readAsDataURL(file)
    e.currentTarget.value = ''
  }

  const onHomeBgFile = (e: any) => {
    const file: File | undefined = e?.target?.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => setHomeBg(String(reader.result))
    reader.readAsDataURL(file)
    e.currentTarget.value = ''
  }

  const save = async () => {
    if (!editable) return
    setSaving(true)
    try {
      const j = await tenantBrandingSave({
        id: targetTenantId || undefined,
        brand_name: name,
        brand_logo: logo,
        domain,
        brand_home_bg: homeBg,
      })
      if (j.success) MessagePlugin.success(t('brand.saved'))
      else MessagePlugin.error(j.message || 'error')
    } catch (e: any) {
      MessagePlugin.error(e?.message || 'error')
    } finally {
      setSaving(false)
    }
  }

  // 超管为当前「切换器所选租户」开通/撤销品牌定制（免套餐）
  const toggleGrant = async (val: boolean) => {
    setGranting(true)
    try {
      const j = await brandGrant(targetTenantId, val)
      if (j.success) {
        setBrandGranted(val)
        MessagePlugin.success(val ? t('brand.grantedOn') : t('brand.grantedOff'))
      } else {
        MessagePlugin.error(j.message || 'error')
      }
    } catch (e: any) {
      MessagePlugin.error(e?.message || 'error')
    } finally {
      setGranting(false)
    }
  }

  return (
    <Panel title={t('brand.title')}>
      <p style={{ fontSize: 13, color: '#667', marginBottom: 12 }}>{t('brand.hint')}</p>
      <div style={{ fontSize: 13, color: '#335', background: '#eef4ff', border: '1px solid #c9ddff', borderRadius: 8, padding: '10px 12px', marginBottom: 12, lineHeight: 1.6 }}>
        🌟 {t('brand.featureDedicated')}
      </div>
      {isSuper && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, background: '#f3f0ff', border: '1px solid #d6c8ff', borderRadius: 8, padding: '10px 12px', marginBottom: 12, fontSize: 13 }}>
          <span>{tpl('brand.grantLabel', { id: targetTenantId })}</span>
          <Switch value={brandGranted} loading={granting} onChange={(v) => toggleGrant(Boolean(v))} />
          {brandGranted && <Tag theme="success" variant="light">{t('brand.grantedTag')}</Tag>}
        </div>
      )}
      {!editable && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, background: '#fff7e6', border: '1px solid #ffd591', color: '#ad6800', borderRadius: 8, padding: '10px 12px', marginBottom: 12, fontSize: 13 }}>
          <span>{t('brand.locked')}</span>
          {brandGranted && <Tag theme="success" variant="light">{t('brand.grantedTag')}</Tag>}
        </div>
      )}
      {!loaded ? (
        <div style={{ color: '#889' }}>…</div>
      ) : (
        <div style={{ maxWidth: 560, display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div>
            <div style={{ fontSize: 13, marginBottom: 4 }}>{t('brand.name')}</div>
            <Input value={name} disabled={!editable} onChange={setName} placeholder="能言 LangCross" />
          </div>

           <div>
             <div style={{ fontSize: 13, marginBottom: 4 }}>{t('brand.logo')}</div>
             <input type="file" accept="image/*" disabled={!editable} onChange={onLogoFile} />
             {logo && (
               <div style={{ marginTop: 8, padding: 12, border: '1px dashed #d0d5e0', borderRadius: 8, display: 'inline-block' }}>
                  <img src={logo} alt="logo" style={{ height: 108, maxWidth: 420, objectFit: 'contain', display: 'block' }} />
               </div>
             )}
             <div style={{ fontSize: 13, margin: '8px 0 4px' }}>{t('brand.logoUrl')}</div>
             <Input value={logo} disabled={!editable} onChange={setLogo} placeholder="https://…/logo.png" />
           </div>

           <div>
             <div style={{ fontSize: 13, marginBottom: 4 }}>{t('brand.domain')}</div>
              <Input value={domain} disabled={!editable} onChange={(v: any) => setDomain(String(v ?? ''))} placeholder="请输入你想要的域名名称" />
              <div style={{ fontSize: 12, color: '#889', marginTop: 4 }}>
                你将改的是 {domain || '前缀'}.lexicorn.cn
              </div>
            </div>

            <div>
              <div style={{ fontSize: 13, marginBottom: 4 }}>{t('brand.homeBg')}</div>
              <input type="file" accept="image/*" disabled={!editable} onChange={onHomeBgFile} />
              {homeBg && (
                <div style={{ marginTop: 8, padding: 12, border: '1px dashed #d0d5e0', borderRadius: 8, display: 'inline-block' }}>
                  <img src={homeBg} alt="bg" style={{ height: 96, maxWidth: 420, objectFit: 'cover', display: 'block' }} />
                </div>
              )}
              <div style={{ fontSize: 13, margin: '8px 0 4px' }}>{t('brand.homeBgUrl')}</div>
              <Input value={homeBg} disabled={!editable} onChange={setHomeBg} placeholder="https://…/bg.png" />
              <div style={{ fontSize: 12, color: '#889', marginTop: 4 }}>{t('brand.homeBgHint')}</div>
            </div>

           {editable && (
            <div>
              <Button theme="primary" loading={saving} onClick={save}>{t('brand.save')}</Button>
            </div>
          )}
        </div>
      )}
    </Panel>
  )
}
