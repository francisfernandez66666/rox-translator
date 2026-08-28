// components/admin/BrandP.tsx — 品牌定制面板（租户管理员 / 超管）
// 仅含：品牌名称、Logo（图片上传+预览）、子域名前缀。页脚链接为平台级，见 FooterP。
// 鉴权：品牌定制开放给三类租户，满足任一即可编辑——① 租户根（企业租户，is_personal=false）；
// ② 持有有效付费套餐（套餐付费租户）；③ 超管显式指定开通（超管指定租户）。超管始终可编辑。
import { useEffect, useRef, useState } from 'react'
import { Button, Input, MessagePlugin, Switch, Tag, Select, Slider } from 'tdesign-react'
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'
import { Panel } from './parts'
import { tenantBranding, tenantBrandingSave, brandGrant } from '@/api/branding'
import { parseBgStyle, BrandBgLayer, BgStyle, parseCardPos, parseLoginLayout, CardPos, LoginLayout } from '@/branding'

// ============ 本文件职责中文说明 ============
// 品牌定制面板：编辑当前（或超管所选）租户的品牌名称、Logo、子域名前缀与登录页样式。
// ========================================

// 品牌定制面板组件：编辑当前（或超管所选）租户的品牌名称、Logo、子域名前缀，受套餐付费状态限制
export default function BrandP() {
  const ad = useAdmin()
  const [, t, tpl] = useT()
  const isSuper = ad.myLevel >= 4
  // 超管编辑「切换器」当前租户（平台根=rox=ID 1）；租户管理员传本租户 id，确保按本租户解析品牌授权
  const targetTenantId = ad.activeTenantId ?? (isSuper ? 1 : 0)

  const [name, setName] = useState('')
  const [logo, setLogo] = useState('')
  const [domain, setDomain] = useState('')
  const [homeBg, setHomeBg] = useState('')
  const [homeBgStyle, setHomeBgStyle] = useState<BgStyle>({ scale: 1, x: 50, y: 50, mode: 'cover' })
  const [loginLayout, setLoginLayout] = useState<LoginLayout>({ mode: 'full', side: 'right' })
  const [loginCardPos, setLoginCardPos] = useState<CardPos>({ x: 50, y: 50 })
  const [saving, setSaving] = useState(false)
  const [loaded, setLoaded] = useState(false)
  // 后端回传：当前编辑租户是否已购有效付费套餐 / 是否被超管授权（二者任一即可编辑）
  const [brandPaid, setBrandPaid] = useState(false)
  const [brandGranted, setBrandGranted] = useState(false)
  const [brandRoot, setBrandRoot] = useState(false)
  const [granting, setGranting] = useState(false)
  // 品牌定制开放给三类租户：租户根（企业租户）/ 付费套餐租户 / 超管指定租户；超管始终可编辑
  const editable = isSuper || brandPaid || brandGranted || brandRoot

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
        setHomeBgStyle(parseBgStyle(j.brand_home_bg_style))
        setLoginLayout(parseLoginLayout(j.brand_login_layout))
        setLoginCardPos(parseCardPos(j.brand_login_card_pos))
        setBrandPaid(!!j.brand_paid)
        setBrandGranted(!!j.brand_granted)
        setBrandRoot(!!j.brand_root)
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

  // 背景图拖拽调整位置：在预览框内按下并移动即更新 x/y（百分比）
  const bgPreviewRef = useRef<HTMLDivElement>(null)
  const splitFormRef = useRef<HTMLDivElement>(null)
  // 记录按下时的光标位置与背景中心，拖动时按相对位移移动（不吸附光标，手感更顺滑）
  const bgDragRef = useRef<{ startX: number; startY: number; x0: number; y0: number } | null>(null)
  const updateBgPos = (e: { clientX: number; clientY: number }) => {
    const d = bgDragRef.current
    const el = bgPreviewRef.current
    if (!d || !el) return
    const rect = el.getBoundingClientRect()
    const dx = ((e.clientX - d.startX) / rect.width) * 100
    const dy = ((e.clientY - d.startY) / rect.height) * 100
    const x = Math.min(100, Math.max(0, d.x0 + dx))
    const y = Math.min(100, Math.max(0, d.y0 + dy))
    setHomeBgStyle((s) => ({ ...s, x, y }))
  }
  // 登录卡片拖拽调整位置（全屏/分栏均生效）：拖动卡片更新 x/y（相对其所在容器）
  const cardDragRef = useRef<{ startX: number; startY: number; x0: number; y0: number; el: HTMLElement | null } | null>(null)
  const updateCardPos = (e: { clientX: number; clientY: number }) => {
    const d = cardDragRef.current
    const el = d?.el
    if (!d || !el) return
    const rect = el.getBoundingClientRect()
    const dx = ((e.clientX - d.startX) / rect.width) * 100
    const dy = ((e.clientY - d.startY) / rect.height) * 100
    const x = Math.min(100, Math.max(0, d.x0 + dx))
    const y = Math.min(100, Math.max(0, d.y0 + dy))
    setLoginCardPos({ x, y })
  }
  useEffect(() => {
    const move = (e: MouseEvent) => {
      if (bgDragRef.current) updateBgPos(e)
      if (cardDragRef.current) updateCardPos(e)
    }
    const up = () => { bgDragRef.current = null; cardDragRef.current = null }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    return () => { window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', up) }
  }, [])

  const save = async () => {
    if (!editable) return
    setSaving(true)
    try {
      const j = await tenantBrandingSave({
        id: targetTenantId,
        brand_name: name,
        brand_logo: logo,
        domain,
        brand_home_bg: homeBg,
        brand_home_bg_style: JSON.stringify(homeBgStyle),
        brand_login_card_pos: JSON.stringify(loginCardPos),
        brand_login_layout: JSON.stringify(loginLayout),
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
        <div style={{ marginBottom: 12 }}>
          <div style={{ fontSize: 13, marginBottom: 4 }}>{t('brand.tenantSelect')}</div>
          <Select
            value={targetTenantId}
            onChange={(v: any) => ad.switchTenant(Number(v))}
            style={{ width: 320 }}
            options={[
              ...(isSuper ? [{ label: t('brand.tenantRoot'), value: 0 }] : []),
              ...ad.tenants.map((x) => ({ label: `#${x.id} ${x.name}`, value: x.id })),
            ]}
          />
        </div>
      )}
      {isSuper && targetTenantId !== 0 && (
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
               <Input value={domain} disabled={!editable || targetTenantId === 0} onChange={(v: any) => setDomain(String(v ?? ''))} placeholder="请输入你想要的域名名称" />
              <div style={{ fontSize: 12, color: '#889', marginTop: 4 }}>
                你将改的是 {domain || '前缀'}.lexicorn.cn
              </div>
            </div>

            <div>
              <div style={{ fontSize: 13, marginBottom: 4 }}>{t('brand.homeBg')}</div>
              <input type="file" accept="image/*" disabled={!editable} onChange={onHomeBgFile} />
              {/* 登录页布局：全屏背景 + 遮罩 / 左右分栏（容器在左或右） */}
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, alignItems: 'center', marginTop: 8 }}>
                <div style={{ fontSize: 13 }}>{t('brand.loginLayout')}</div>
                <Select value={loginLayout.mode} disabled={!editable}
                        onChange={(v: any) => setLoginLayout((l) => ({ ...l, mode: v }))}
                        style={{ width: 160 }}
                        options={[{ label: t('brand.layoutFull'), value: 'full' }, { label: t('brand.layoutSplit'), value: 'split' }]} />
                {loginLayout.mode === 'split' && (
                  <>
                    <div style={{ fontSize: 13 }}>{t('brand.loginSide')}</div>
                    <Select value={loginLayout.side} disabled={!editable}
                            onChange={(v: any) => setLoginLayout((l) => ({ ...l, side: v }))}
                            style={{ width: 160 }}
                            options={[{ label: t('brand.sideRight'), value: 'right' }, { label: t('brand.sideLeft'), value: 'left' }]} />
                  </>
                )}
              </div>
              {homeBg && (
                <div style={{ marginTop: 8 }}>
                  {loginLayout.mode === 'split' ? (
                    /* 分栏预览：一侧背景图，另一侧登录容器（容器在左/右随 side 切换） */
                    <div ref={bgPreviewRef} style={{ position: 'relative', width: '100%', maxWidth: 420, height: 180, overflow: 'hidden', borderRadius: 8, border: '1px dashed #d0d5e0', display: 'flex' }}>
                      {loginLayout.side === 'left' ? (
                        <>
                          <div ref={splitFormRef} style={{ flex: 1, position: 'relative', background: '#eef1f8', overflow: 'hidden' }}>
                            <div onMouseDown={(e) => { e.preventDefault(); cardDragRef.current = { startX: e.clientX, startY: e.clientY, x0: loginCardPos.x, y0: loginCardPos.y, el: splitFormRef.current } }}
                              style={{ position: 'absolute', left: `${loginCardPos.x}%`, top: `${loginCardPos.y}%`, transform: 'translate(-50%,-50%)', width: 120, height: 80, background: '#fff', borderRadius: 8, boxShadow: '0 6px 20px rgba(0,0,0,.2)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 11, color: '#889', cursor: 'move' }}>
                              {t('brand.cardPreview')}
                            </div>
                          </div>
                          <div style={{ flex: 1, position: 'relative', overflow: 'hidden' }}><BrandBgLayer src={homeBg} styleJson={JSON.stringify(homeBgStyle)} /></div>
                        </>
                      ) : (
                        <>
                          <div style={{ flex: 1, position: 'relative', overflow: 'hidden' }}><BrandBgLayer src={homeBg} styleJson={JSON.stringify(homeBgStyle)} /></div>
                          <div ref={splitFormRef} style={{ flex: 1, position: 'relative', background: '#eef1f8', overflow: 'hidden' }}>
                            <div onMouseDown={(e) => { e.preventDefault(); cardDragRef.current = { startX: e.clientX, startY: e.clientY, x0: loginCardPos.x, y0: loginCardPos.y, el: splitFormRef.current } }}
                              style={{ position: 'absolute', left: `${loginCardPos.x}%`, top: `${loginCardPos.y}%`, transform: 'translate(-50%,-50%)', width: 120, height: 80, background: '#fff', borderRadius: 8, boxShadow: '0 6px 20px rgba(0,0,0,.2)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 11, color: '#889', cursor: 'move' }}>
                              {t('brand.cardPreview')}
                            </div>
                          </div>
                        </>
                      )}
                    </div>
                  ) : (
                    /* 全屏预览：背景 + 遮罩 + 可拖拽登录卡片 */
                    <div
                      ref={bgPreviewRef}
                      onMouseDown={(e) => {
                        e.preventDefault()
                        bgDragRef.current = { startX: e.clientX, startY: e.clientY, x0: homeBgStyle.x, y0: homeBgStyle.y }
                      }}
                      style={{ position: 'relative', width: '100%', maxWidth: 420, height: 180, overflow: 'hidden', borderRadius: 8, border: '1px dashed #d0d5e0', cursor: 'move', background: '#0d1b3e' }}
                    >
                      <BrandBgLayer src={homeBg} styleJson={JSON.stringify(homeBgStyle)} />
                      <div style={{ position: 'absolute', inset: 0, background: 'rgba(10,16,40,0.42)' }} />
                      <div
                        onMouseDown={(e) => { e.preventDefault(); e.stopPropagation(); cardDragRef.current = { startX: e.clientX, startY: e.clientY, x0: loginCardPos.x, y0: loginCardPos.y, el: bgPreviewRef.current } }}
                        style={{ position: 'absolute', left: `${loginCardPos.x}%`, top: `${loginCardPos.y}%`, transform: 'translate(-50%,-50%)', width: 140, height: 90, background: '#fff', borderRadius: 8, boxShadow: '0 6px 20px rgba(0,0,0,.25)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 11, color: '#889', cursor: 'move' }}
                      >
                        {t('brand.cardPreview')}
                      </div>
                      <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#cdd', fontSize: 12, pointerEvents: 'none' }}>
                        {t('brand.homeBgDragHint')}
                      </div>
                    </div>
                  )}
                  <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, alignItems: 'center', marginTop: 10, maxWidth: 420 }}>
                    <div style={{ fontSize: 13 }}>{t('brand.homeBgMode')}</div>
                    <Select
                      value={homeBgStyle.mode}
                      onChange={(v: any) => setHomeBgStyle((s) => ({ ...s, mode: v }))}
                      style={{ width: 140 }}
                      options={[
                        { label: t('brand.bgCover'), value: 'cover' },
                        { label: t('brand.bgContain'), value: 'contain' },
                      ]}
                    />
                    <div style={{ fontSize: 13, minWidth: 96 }}>{t('brand.homeBgZoom')}</div>
                    <Slider value={homeBgStyle.scale} min={0.5} max={3} step={0.1}
                            onChange={(v: any) => setHomeBgStyle((s) => ({ ...s, scale: Number(v) }))}
                            style={{ width: 160 }} />
                    <span style={{ fontSize: 12, color: '#889', minWidth: 40 }}>{homeBgStyle.scale.toFixed(1)}x</span>
                    <Button size="small" variant="text" onClick={() => setHomeBgStyle({ scale: 1, x: 50, y: 50, mode: 'cover' })}>
                      {t('brand.homeBgReset')}
                    </Button>
                    <Button size="small" variant="text" onClick={() => setLoginCardPos({ x: 50, y: 50 })}>
                      {t('brand.cardPosReset')}
                    </Button>
                  </div>
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
