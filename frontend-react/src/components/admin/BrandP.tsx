// ============================================================================
// components/admin/BrandP.tsx — 品牌定制面板（租户管理员 / 超管）
// 职责：编辑当前（或超管所选）租户的品牌名称、Logo、子域名前缀与登录页样式。
// 鉴权：品牌定制开放给三类租户，满足任一即可编辑——① 租户根（企业租户，is_personal=false）；
// ② 持有有效付费套餐（套餐付费租户）；③ 超管显式指定开通（超管指定租户）。超管始终可编辑。
// 2026-09-18（UI 融合）：本面板内的「预览位」（Logo 虚线框、登录页分栏/全屏预览、
//   登录卡片）配色统一改为暗色主题档位，使预览与线上登录页一致；提示文案去 emoji。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import { Button, StatusPill, Switch, Tabs } from '@/ui/langcross/src'
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'
import { Panel } from './parts'
import FooterP from './FooterP'
import { tenantBranding, tenantBrandingSave, brandGrant } from '@/api/branding'
import { parseBgStyle, BrandBgLayer, BgStyle, parseCardPos, parseLoginLayout, CardPos, LoginLayout } from '@/branding'
import { toastSuccess, toastError } from '@/lib/toastBus'

/** 品牌定制面板组件：编辑当前（或超管所选）租户的品牌名称、Logo、子域名前缀，受套餐付费状态限制 */
export default function BrandP() {
  const [tab, setTab] = useState('brand')
  const ad = useAdmin()
  const [, t, tpl] = useT()
  const isSuper = ad.myLevel >= 4
  // 超管编辑「切换器」当前租户（平台根=rox=ID 1）；租户管理员传本租户 id，确保按本租户解析品牌授权
  // ★ 用 || 而非 ??：activeTenantId=0（超管未选租户）时必须回落到默认租户1（独立演示站=ROX 品牌），
  //   否则 tid=0 会被后端当作「平台主站」→ 品牌定制 tab 显示/保存主站品牌而非演示站自身品牌。
  const targetTenantId = ad.activeTenantId || (isSuper ? 1 : 0)

  // 表单状态：品牌名称、Logo URL、子域名、首页背景图、背景样式、登录页布局、登录卡片位置
  const [name, setName] = useState('')
  const [nameEn, setNameEn] = useState('')
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

  // 加载品牌定制数据：租户品牌名称、Logo、子域名、首页背景等
  // alive 守卫针对的是「超管连点切换器」：前一个租户的请求可能后回来，
  // 不判 alive 就会把 A 的品牌回填进 B 的表单，保存时写错租户（比读错更贵）。
  // 每次进 effect 先 setLoaded(false)：loaded=false 时整块表单不渲染（只剩「…」），
  // 否则切租户的瞬间会继续显示上一个租户的名字/Logo，看着像「改了没生效」。
  // catch 分支同样 setLoaded(true)：拉取失败也让表单照常渲染（字段为空、可手填），
  // 否则整块面板永远停在「…」，用户连重试的入口都看不到。
  useEffect(() => {
    let alive = true
    setLoaded(false)
    tenantBranding(targetTenantId || undefined)
      .then((j) => {
        if (!alive || !j.success) return
        setName(j.brand_name || '')
        setNameEn(j.brand_name_en || '')
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

  // ★ E15：与后端 validateBrandPayloads 同口径的本地预检（Logo ~300KB、背景 ~800KB），
  //   避免超大图整段 base64 进请求体才被拒。
  //   两边量纲并不完全相同：后端限的是 base64 字符串长度（brandLogoMaxLen / brandHomeBgMax），
  //   前端判的是原图字节数，而 base64 会膨胀约 33%，故这里是「拦掉明显超量的大图」的粗筛，
  //   贴着上限的原图仍会放行到后端、由后端给出那句压缩提示（不是前端漏判就能自己收尾的）。
  const checkBrandFile = (file: File, maxKB: number): boolean => {
    if (!file.type.startsWith('image/')) { toastError('请选择图片文件'); return false }
    if (file.size > maxKB * 1024) { toastError(`图片过大（上限约 ${maxKB}KB），请先压缩`); return false }
    return true
  }

  /** Logo 文件选择处理：读取本地文件并转为 Data URL
   *  FileReader 异步回写，故成功后 setLogo 前不做任何乐观更新。
   *  两处 e.currentTarget.value='' 都是必需的：input 的 value 仍是旧路径时，
   *  连续选同一张图（换了尺寸重选、或预检失败后重选）不会触发 onChange，看起来「点了没反应」。 */
  const onLogoFile = (e: any) => {
    const file: File | undefined = e?.target?.files?.[0]
    if (!file) return
    if (!checkBrandFile(file, 300)) { e.currentTarget.value = ''; return }
    const reader = new FileReader()
    reader.onload = () => setLogo(String(reader.result))
    reader.readAsDataURL(file)
    e.currentTarget.value = ''
  }

  /** 首页背景图文件选择处理：读取本地文件并转为 Data URL
   *  与 onLogoFile 同形，只差在体积上限（背景 800KB > Logo 300KB，见 E15 预检） */
  const onHomeBgFile = (e: any) => {
    const file: File | undefined = e?.target?.files?.[0]
    if (!file) return
    if (!checkBrandFile(file, 800)) { e.currentTarget.value = ''; return }
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

  /** 更新背景图位置（根据鼠标拖动偏移计算百分比坐标）
   *  偏移先除以 getBoundingClientRect() 的实时宽高再换算成百分比：
   *  预览框是 max-width:100% 的流式尺寸，按下时缓存一次会在窗口缩放/换 tab 后失真；
   *  存 x0/y0 而不是绝对坐标，也让「从卡片任意处按下」都能按原手感拖动（不吸附到光标）。 */
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
  // el 之所以随布局形态换（全屏=bgPreviewRef、分栏=splitFormRef 那一侧）：
  // x/y 是百分比，必须相对「线上真正承载卡片的那个容器」测量，两种布局下同一个数值才对应同一个视觉位置。
  const cardDragRef = useRef<{ startX: number; startY: number; x0: number; y0: number; el: HTMLElement | null } | null>(null)

  /** 更新登录卡片位置（根据鼠标拖动偏移计算百分比坐标） */
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

  // 全局鼠标事件：处理背景图和登录卡片的拖拽
  // 监听挂 window 而不是预览框：拖出 420px 预览框是很自然的动作，绑在框上会「拖到边缘就停住」；
  // 挂 window 后由 ref 判定这次移动属于哪条拖拽（同一时刻只有一个 ref 非空，不会串）。
  // 空依赖 + 只读写 ref/state setter：整套拖拽不需要重新注册监听，也不会闭包到旧 homeBgStyle。
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

  /** 保存品牌定制配置：调用后端接口持久化所有品牌设置
   *  提交的是「当前表单的完整快照」而非增量：加载时已把九个字段全部回填（见上面的 useEffect），
   *  整串覆写才不会把用户没碰过的字段丢成空值。bg_style / card_pos / login_layout 三支
   *  在库里各是一个 JSON 文本列，前端只负责序列化，字段级合并由后端按整列覆写处理。
   *  入口先判 editable：非可编辑租户即使绕过按钮直接调用，也不该发一次注定被后端拒的写请求。 */
  const save = async () => {
    if (!editable) return
    setSaving(true)
    try {
      const j = await tenantBrandingSave({
        id: targetTenantId,
        brand_name: name,
        brand_name_en: nameEn,
        brand_logo: logo,
        domain,
        brand_home_bg: homeBg,
        brand_home_bg_style: JSON.stringify(homeBgStyle),
        brand_login_card_pos: JSON.stringify(loginCardPos),
        brand_login_layout: JSON.stringify(loginLayout),
      })
      if (j.success) toastSuccess(t('brand.saved'))
      else toastError(j.message || 'error')
    } catch (e: any) {
      toastError(e?.message || 'error')
    } finally {
      setSaving(false)
    }
  }

  /** 超管为当前「切换器所选租户」开通/撤销品牌定制（免套餐）
   *  成功后只就地翻转 brandGranted、不重跑加载 effect：整表 reload 会把用户
   *  已经填进去但没保存的字段全部冲掉，而这次写操作唯一影响的标志就是这一个布尔值。 */
  const toggleGrant = async (val: boolean) => {
    setGranting(true)
    try {
      const j = await brandGrant(targetTenantId, val)
      if (j.success) {
        setBrandGranted(val)
        toastSuccess(val ? t('brand.grantedOn') : t('brand.grantedOff'))
      } else {
        toastError(j.message || 'error')
      }
    } catch (e: any) {
      toastError(e?.message || 'error')
    } finally {
      setGranting(false)
    }
  }

  return (
    <Panel title={t('brand.title')}>
      <Tabs activeKey={tab} onChange={(k) => setTab(k)} items={[
        { key: 'brand', label: t('brand.title') },
        ...(isSuper ? [{ key: 'footer', label: t('footer.title') }] : []),
      ]} />
      {tab === 'brand' && (
        <>
          <p style={{ fontSize: 15, color: 'var(--adm-hint)', marginBottom: 12 }}>{t('brand.hint')}</p>
      <div style={{ fontSize: 15, color: 'var(--adm-info-tx)', background: 'var(--adm-info-bg)', border: '2px solid var(--adm-info-bd)', borderRadius: 8, padding: '10px 12px', marginBottom: 12, lineHeight: 1.6 }}>
         {t('brand.featureDedicated')}
      </div>
      {/* 超管：租户选择器 */}
      {isSuper && (
        <div style={{ marginBottom: 12 }}>
          <div style={{ fontSize: 15, marginBottom: 4 }}>{t('brand.tenantSelect')}</div>
          <select className="lc-select" value={String(targetTenantId)} onChange={(e) => ad.switchTenant(Number(e.target.value))} style={{ width: 320 }}>
            {isSuper && <option value="1">{t('brand.tenantRoot')}</option>}
            {ad.tenants.map((x) => <option key={x.id} value={String(x.id)}>{`#${x.id} ${x.name}`}</option>)}
          </select>
        </div>
      )}
      {/* 超管：品牌定制授权开关（仅对非平台根租户生效） */}
      {isSuper && targetTenantId > 1 && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'var(--adm-purp-bg)', border: '2px solid var(--adm-purp-bd)', borderRadius: 8, padding: '10px 12px', marginBottom: 12, fontSize: 15 }}>
          <span>{tpl('brand.grantLabel', { id: targetTenantId })}</span>
          <Switch checked={brandGranted} disabled={granting} onChange={(e) => toggleGrant(e.target.checked)} />
          {brandGranted && <StatusPill tone="success">{t('brand.grantedTag')}</StatusPill>}
        </div>
      )}
      {/* 未获得编辑权限时显示锁定提示 */}
      {!editable && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, background: 'var(--adm-warn-bg)', border: '2px solid var(--adm-warn-bd)', color: 'var(--adm-warn-tx)', borderRadius: 8, padding: '10px 12px', marginBottom: 12, fontSize: 15 }}>
          <span>{t('brand.locked')}</span>
          {brandGranted && <StatusPill tone="success">{t('brand.grantedTag')}</StatusPill>}
        </div>
      )}
      {!loaded ? (
        <div style={{ color: 'var(--adm-faint)' }}>…</div>
      ) : (
        <div style={{ maxWidth: 560, display: 'flex', flexDirection: 'column', gap: 14 }}>
          {/* 下面每个控件都各自 disabled={!editable} 而不是整块不渲染：
              未开通时字段仍回显后端当前值（只读），配合上方锁定横幅说明「为什么看得到但改不了」；
              整块隐藏会让面板在授权前后长得完全不一样。
              maxWidth 560 给表单收口，避免超宽屏上输入框拉成一条长线。 */}
          {/* 品牌名称输入 */}
          <div>
            <div style={{ fontSize: 15, marginBottom: 4 }}>{t('brand.name')}</div>
            <input className="lc-input" value={name} disabled={!editable} onChange={(e) => setName(e.target.value)} placeholder="能言 LangCross" />
          </div>

          {/* 品牌英文名输入（固定用法种入企业知识库，防止翻译漂移） */}
          <div>
            <div style={{ fontSize: 15, marginBottom: 4 }}>{t('brand.nameEn')}</div>
            <input className="lc-input" value={nameEn} disabled={!editable} onChange={(e) => setNameEn(String(e.target.value ?? ''))} placeholder="LangCross" />
          </div>

           {/* Logo 上传与预览
               预览框用深色虚线（#464C58）：暗色面板下浅色/透明 PNG Logo 也能看清边界
               ★ #68：上面那句里的字面 #464C58 已收口为 --lc-border-input——
               该档在纯黑底上 ≥4:1（readability.test.ts 的描边锁会拦更暗的字面值），
               视觉上仍是「看得见的虚线框」，语义不变。 */}
           <div>
             <div style={{ fontSize: 15, marginBottom: 4 }}>{t('brand.logo')}</div>
             <input type="file" accept="image/*" disabled={!editable} onChange={onLogoFile} />
             {logo && (
               <div style={{ marginTop: 8, padding: 12, border:'2px dashed var(--lc-border-input)', borderRadius: 8, display:'inline-block'}}>
                  <img src={logo} alt="logo" style={{ height: 108, maxWidth: 420, objectFit: 'contain', display: 'block' }} />
               </div>
             )}
             <div style={{ fontSize: 15, margin: '8px 0 4px' }}>{t('brand.logoUrl')}</div>
             <input className="lc-input" value={logo} disabled={!editable} onChange={(e) => setLogo(e.target.value)} placeholder="https://…/logo.png" />
           </div>

           {/* 子域名前缀输入
               targetTenantId===1（平台根 / 默认站，code=rox）时禁用：后端解析专属租户时明确把
               ID 1 与 rox 排除在「租户子域」之外，放开编辑等于让主站前缀去占一个租户子域。
               下面那行提示里的 lexicorn.cn 是写死的展示值，真实后缀取后端
               system_config(base_domain) / env BRAND_DOMAIN_SUFFIX——换根域时两处要一起改。 */}
           <div>
             <div style={{ fontSize: 15, marginBottom: 4 }}>{t('brand.domain')}</div>
                <input className="lc-input" value={domain} disabled={!editable || targetTenantId === 1} onChange={(e) => setDomain(String(e.target.value ?? ''))} placeholder="请输入你想要的域名名称" />
              <div style={{ fontSize: 14, color: 'var(--adm-faint)', marginTop: 4 }}>
                你将改的是 {domain || '前缀'}.lexicorn.cn
              </div>
            </div>

            {/* 首页背景图配置：文件上传 + 登录页布局选择 */}
            <div>
              <div style={{ fontSize: 15, marginBottom: 4 }}>{t('brand.homeBg')}</div>
              <input type="file" accept="image/*" disabled={!editable} onChange={onHomeBgFile} />
              {/* 登录页布局：全屏背景 + 遮罩 / 左右分栏（容器在左或右） */}
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, alignItems: 'center', marginTop: 8 }}>
                <div style={{ fontSize: 15 }}>{t('brand.loginLayout')}</div>
                <select className="lc-select" value={loginLayout.mode} disabled={!editable}
                        onChange={(e) => setLoginLayout((l) => ({ ...l, mode: e.target.value as 'full' | 'split' }))}
                        style={{ width: 160 }}>
                  <option value="full">{t('brand.layoutFull')}</option>
                  <option value="split">{t('brand.layoutSplit')}</option>
                </select>
                {loginLayout.mode === 'split' && (
                  <>
                    <div style={{ fontSize: 15 }}>{t('brand.loginSide')}</div>
                    <select className="lc-select" value={loginLayout.side} disabled={!editable}
                            onChange={(e) => setLoginLayout((l) => ({ ...l, side: e.target.value as 'left' | 'right' }))}
                            style={{ width: 160 }}>
                      <option value="right">{t('brand.sideRight')}</option>
                      <option value="left">{t('brand.sideLeft')}</option>
                    </select>
                  </>
                )}
              </div>
              {homeBg && (
                <div style={{ marginTop: 8 }}>
                  {loginLayout.mode === 'split' ? (
                    /* 分栏预览：一侧背景图，另一侧登录容器（容器在左/右随 side 切换）
                       2026-09-18 配色随暗色主题对齐：容器底 rgba(231,233,234,.06)（浅色按 6% 透明度＝微弱提亮）、
                       登录卡片底 #0E1014（与线上卡片同档）、外框虚线走 --lc-border-input（★ #68：
                       原字面 #464C58 在纯黑上不足 3:1，预览框几乎看不见）。
                       目的是「预览所见 ≈ 登录页实际观感」，避免白底预览、暗色上线的落差。
                       ★ 2026-09-22 还原：卡内示意文字与提示文字原为蓝调灰 #889/#cdd，改走中性灰阶令牌（全站无蓝）。
                       线上卡片文字由登录页组件按 --lc-text-* 渲染；预览示意走 --lc-text-3/--lc-text-2 中性灰档，两个全屏/分栏分支共用同一档。 */
                    <div ref={bgPreviewRef} style={{ position:'relative', width:'100%', maxWidth: 420, height: 180, overflow:'hidden', borderRadius: 8, border:'2px dashed var(--lc-border-input)', display:'flex'}}>
                      {loginLayout.side === 'left' ? (
                        <>
                          {/* 左侧：登录表单容器（可拖拽调整卡片位置） */}
                          <div ref={splitFormRef} style={{ flex: 1, position:'relative', background:'rgba(231,233,234,0.06)', overflow:'hidden'}}>
                            <div onMouseDown={(e) => { e.preventDefault(); cardDragRef.current = { startX: e.clientX, startY: e.clientY, x0: loginCardPos.x, y0: loginCardPos.y, el: splitFormRef.current } }}
                              style={{ position:'absolute', left: `${loginCardPos.x}%`, top: `${loginCardPos.y}%`, transform:'translate(-50%,-50%)', width: 120, height: 80, background:'#0E1014', borderRadius: 8, boxShadow:'0 6px 20px rgba(0,0,0,.2)', display:'flex', alignItems:'center', justifyContent:'center', fontSize: 13, color:'var(--lc-text-3)', cursor:'move'}}>
                              {t('brand.cardPreview')}
                            </div>
                          </div>
                          {/* 右侧：背景图展示 */}
                          <div style={{ flex: 1, position: 'relative', overflow: 'hidden' }}><BrandBgLayer src={homeBg} styleJson={JSON.stringify(homeBgStyle)} /></div>
                        </>
                      ) : (
                        <>
                          {/* 左侧：背景图展示 */}
                          <div style={{ flex: 1, position: 'relative', overflow: 'hidden' }}><BrandBgLayer src={homeBg} styleJson={JSON.stringify(homeBgStyle)} /></div>
                          {/* 右侧：登录表单容器（可拖拽调整卡片位置） */}
                          <div ref={splitFormRef} style={{ flex: 1, position:'relative', background:'rgba(231,233,234,0.06)', overflow:'hidden'}}>
                            <div onMouseDown={(e) => { e.preventDefault(); cardDragRef.current = { startX: e.clientX, startY: e.clientY, x0: loginCardPos.x, y0: loginCardPos.y, el: splitFormRef.current } }}
                              style={{ position:'absolute', left: `${loginCardPos.x}%`, top: `${loginCardPos.y}%`, transform:'translate(-50%,-50%)', width: 120, height: 80, background:'#0E1014', borderRadius: 8, boxShadow:'0 6px 20px rgba(0,0,0,.2)', display:'flex', alignItems:'center', justifyContent:'center', fontSize: 13, color:'var(--lc-text-3)', cursor:'move'}}>
                              {t('brand.cardPreview')}
                            </div>
                          </div>
                        </>
                      )}
                    </div>
                  ) : (
                    /* 全屏预览：背景 + 遮罩 + 可拖拽登录卡片
                       底色 #050607 为暗色主题最深档，未上传背景图时也不会露出白色；
                       卡片沿用 #0E1014，与分栏预览同一色档，保证两种布局观感一致。 */
                    <div
                      ref={bgPreviewRef}
                      onMouseDown={(e) => {
                        e.preventDefault()
                        bgDragRef.current = { startX: e.clientX, startY: e.clientY, x0: homeBgStyle.x, y0: homeBgStyle.y }
                      }}
                      style={{ position:'relative', width:'100%', maxWidth: 420, height: 180, overflow:'hidden', borderRadius: 8, border:'2px dashed var(--lc-border-input)', cursor:'move', background:'#050607'}}
                    >
                      <BrandBgLayer src={homeBg} styleJson={JSON.stringify(homeBgStyle)} />
                      {/* 半透明遮罩层 */}
                      <div style={{ position: 'absolute', inset: 0, background: 'rgba(0,0,0,0.42)' }} />
                      {/* 可拖拽的登录卡片预览 */}
                      <div
                        onMouseDown={(e) => { e.preventDefault(); e.stopPropagation(); cardDragRef.current = { startX: e.clientX, startY: e.clientY, x0: loginCardPos.x, y0: loginCardPos.y, el: bgPreviewRef.current } }}
                        style={{ position:'absolute', left: `${loginCardPos.x}%`, top: `${loginCardPos.y}%`, transform:'translate(-50%,-50%)', width: 140, height: 90, background:'#0E1014', borderRadius: 8, boxShadow:'0 6px 20px rgba(0,0,0,.25)', display:'flex', alignItems:'center', justifyContent:'center', fontSize: 13, color:'var(--lc-text-3)', cursor:'move'}}
                      >
                        {t('brand.cardPreview')}
                      </div>
                      {/* 背景图拖拽提示文字 */}
                      <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--lc-text-2)', fontSize: 14, pointerEvents: 'none' }}>
                        {t('brand.homeBgDragHint')}
                      </div>
                    </div>
                  )}
                  {/* 背景图显示模式、缩放比例、重置按钮 */}
                  <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, alignItems: 'center', marginTop: 10, maxWidth: 420 }}>
                    <div style={{ fontSize: 15 }}>{t('brand.homeBgMode')}</div>
                    <select className="lc-select"
                      value={homeBgStyle.mode}
                      onChange={(e) => setHomeBgStyle((s) => ({ ...s, mode: e.target.value as BgStyle['mode'] }))}
                      style={{ width: 140 }}>
                      <option value="cover">{t('brand.bgCover')}</option>
                      <option value="contain">{t('brand.bgContain')}</option>
                    </select>
                    <div style={{ fontSize: 15, minWidth: 96 }}>{t('brand.homeBgZoom')}</div>
                    {/* 缩放滑块强调色走 --lc-text-1（白）：交付包硬规则「全站无蓝无绿」，
                        UA 默认的蓝 accent-color 会在这套纯黑面板里直接露出来 */}
                    <input type="range" min={0.5} max={3} step={0.1} value={homeBgStyle.scale}
                            onChange={(e) => setHomeBgStyle((s) => ({ ...s, scale: Number(e.target.value) }))}
                            style={{ width: 160, accentColor: 'var(--lc-text-1)' }} />
                    <span style={{ fontSize: 14, color: 'var(--adm-faint)', minWidth: 40 }}>{homeBgStyle.scale.toFixed(1)}x</span>
                    <Button size="sm" variant="secondary" onClick={() => setHomeBgStyle({ scale: 1, x: 50, y: 50, mode: 'cover' })}>
                      {t('brand.homeBgReset')}
                    </Button>
                    <Button size="sm" variant="secondary" onClick={() => setLoginCardPos({ x: 50, y: 50 })}>
                      {t('brand.cardPosReset')}
                    </Button>
                  </div>
                </div>
              )}
              <div style={{ fontSize: 15, margin: '8px 0 4px' }}>{t('brand.homeBgUrl')}</div>
              <input className="lc-input" value={homeBg} disabled={!editable} onChange={(e) => setHomeBg(e.target.value)} placeholder="https://…/bg.png" />
              <div style={{ fontSize: 14, color: 'var(--adm-faint)', marginTop: 4 }}>{t('brand.homeBgHint')}</div>
            </div>

           {/* 保存按钮（仅可编辑时显示） */}
           {editable && (
            <div>
              <Button variant="primary" disabled={saving} onClick={save}>{t('brand.save')}</Button>
            </div>
          )}
        </div>
      )}
        </>
      )}
        {/* 页脚链接并入品牌定制 tab（仅超管，平台级链接）
            再判一次 isSuper 与上面 items 的过滤条件同口径（两处都写，改一处不会漏另一处）；
            FooterP 只在切到该 tab 时才挂载，页脚链接的读取因此不是本面板的常驻开销。 */}
      {tab === 'footer' && isSuper && (
            <FooterP />
      )}
    </Panel>
  )
}
