// ============================================================================
// components/admin/TenantsP.tsx — 租户管理面板
// 职责：租户 CRUD、试用开通、充值、导出、状态启停、GDPR 擦除
// 从 panels_b.tsx 拆分
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
// 2026-09-18（UI 融合）：TDesign → 自研 @/ui/langcross 原语。本面板踩到的三个不兼容点记在这里：
//   ① Table 的 colKey/cell 换成 DataTable 的 key/render，且 rowKey 必须给函数（不再是字段名）；
//   ② Dialog 的 visible/header/onClose/width 换成 open/title/onCancel，宽度固定不再逐处传；
//   ③ 新底座没有 Popconfirm 与 Space，气泡确认改走 uiDialogs.confirmDialog，行内动作容器改 flex div。
import { Button, DataTable, Dialog, Switch, StatusPill, Link } from '@/ui/langcross/src'
// toastBus 是同步总线（不像 MessagePlugin 返回 Promise），旧调用点的 void 前缀因此全部去掉
import { toastSuccess, toastError } from '@/lib/toastBus'
import { confirmDialog, promptText } from '@/components/uiDialogs'
import {
  tenantList, tenantCreate, tenantUpdate, tenantSetStatus, tenantDelete,
  tenantGrantTrial, tenantErase, adminOrderCreate, adminOrderPay,
  API_BASE, getAuthToken, handleUnauthorized,
  type TenantInfo,
} from '@/api'
import { runGuarded } from '@/lib/runGuarded'
import { Panel, Field, toastResp, num } from './parts'
import { useAdmin } from '@/stores/admin'
import { t, tpl, useT, type Lang } from '@/i18n'
import { industryName, industryCodeOf } from '@/lib/industries'
import { industries as fetchIndustries } from '@/api/industry'

/** Any 租户管理出参宽松别名 */
type Any = any

/** 行业 code → 当前语言展示名（列表列用） */
const industryLabel = (code: string, lang: Lang): string => industryName(code || '', lang) || '—'

/** 租户 CRUD、试用开通、充值、导出、状态启停、GDPR 擦除组件 */
export function TenantsP() {
  const ad = useAdmin()
  // ===== 面板状态：租户列表、弹窗上下文（新建/编辑/充值）、行业枚举 =====
  const [lang] = useT()
  // rows 租户列表；每个写操作之后整表重拉，不做本地乐观更新（status/expires 以后端计算为准）
  const [rows, setRows] = useState<TenantInfo[]>([])
  // dlg 一个状态承载三种弹窗形态：'create' 新建 / {edit} 编辑 / {order} 充值单，null 即关闭
  const [dlg, setDlg] = useState<null | 'create' | { edit: TenantInfo } | { order: TenantInfo }>(null)
  // form 三种弹窗共用的键值袋：字段按 dlg 形态各取所需，初值只在打开入口灌入（见各按钮的 setForm）
  const [form, setForm] = useState<Any>({})
  // indList 行业枚举：列表展示名与表单下拉共用一份；拉取失败静默降级为「—」，不阻塞建租户
  const [indList, setIndList] = useState<Array<{ code: string; name: string; enabled: number }>>([])
  // codeErr 编码必填的就地错误文案（2026-10-16 用户报障「点确定完全没反应」的收口之一）：
  // 旧写法只弹一条 warn toast 就 return，用户没注意到 toast 时整个动作看上去什么都没发生；
  // 现在把这句话钉在输入框下方（移开视线也还在），并给输入框加 aria-invalid。
  const [codeErr, setCodeErr] = useState('')

  // loadInd 拉取行业枚举（租户表单行业下拉数据源）
  const loadInd = useCallback(async () => {
    try {
      const r = await fetchIndustries()
      if (r.success && r.industries) setIndList(r.industries)
    } catch { /* ignore */ }
  }, [])
  useEffect(() => { void loadInd() }, [loadInd])

  // load 拉取租户列表
  // ★ #42（前端坏味道：取数错误不可见）：旧写法裸 await，接口一挂就是一条未捕获 rejection +
  //   一张空表，超管完全看不出是「没有租户」还是「请求失败」；改走 runGuarded 统一提示口径。
  const load = useCallback(async () => {
    const r = await runGuarded(() => tenantList())
    if (r?.success) setRows(r.tenants || [])
  }, [])
  useEffect(() => { void load() }, [load])

  // save 新建/编辑同一入口：code、管理员账号、初始密码只在创建态提交，
  // 编辑分支刻意不带这三项（改码会断外链映射，重置密码更不该由普通编辑动作顺带发生）
  // save 新建/编辑同一入口：每个写入都必须是「有结果」的——runGuarded 接网络/超时/401/403，
  // toastResp 接业务失败（4xx 经 bizResp 还原成 success:false）。两者各出一句红条，
  // 绝不允许再出现「点确定之后什么都没发生」的分支（2026-10-16 用户报障的定性落点）。
  async function save() {
    if (dlg === 'create') {
      // code 必填先在前端拦一刀（后端同校验），少一次往返；就地报错而不是只弹 toast
      if (!String(form.code || '').trim()) { setCodeErr(t('tenants.codeRequired')); return }
      const r = await runGuarded(() => tenantCreate({
        code: String(form.code || ''), name: String(form.name || ''),
        expires_at: String(form.expires_at || ''), permissions: String(form.permissions || '{}'),
        admin_user: form.admin_user ? String(form.admin_user) : undefined,
        admin_pass: form.admin_pass ? String(form.admin_pass) : undefined,
        industry: form.industry ? String(form.industry) : undefined,
      }))
      if (!r || !toastResp(r)) return
      // 建完除了刷本页列表还要 ad.loadTenants()：顶栏租户切换器读的是 store 缓存，
      // 只刷本页会出现「列表里有、切不过去」的割裂状态
      setForm({}); setCodeErr(''); setDlg(null); await load(); ad.loadTenants()
    } else if (dlg && typeof dlg === 'object' && 'edit' in dlg) {
      const tt = dlg.edit
      const r = await runGuarded(() => tenantUpdate({
        id: tt.id, name: String(form.name ?? tt.name),
        expires_at: String(form.expires_at ?? tt.expires_at ?? ''),
        permissions: String(form.permissions ?? tt.permissions ?? '{}'),
        // 下拉里可能是展示名也可能是 code（历史数据未归一），提交前统一折回 code
        industry: String(form.industry ? industryCodeOf(String(form.industry)) : (tt.industry ?? '')),
      }))
      if (!r || !toastResp(r)) return
      setDlg(null); await load()
    }
  }

  // doExport 用裸 XMLHttpRequest 而不是 api 封装：响应要按 blob 收（封装层固定按 JSON 解析会毁掉文件流），
  // 且 Authorization 头得手工补；导出前先确认，因为落盘文件里是该租户的全量业务数据
  async function doExport(tt: TenantInfo) {
    if (!(await confirmDialog({ body: tpl('tenants.exportConfirm', { name: tt.name }) }))) return
    const url = `${API_BASE}/api/tenant/export`
    const xhr = new XMLHttpRequest()
    xhr.open('POST', url, true)
    xhr.setRequestHeader('Content-Type', 'application/json')
    const tk = getAuthToken()
    if (tk) xhr.setRequestHeader('Authorization', `Bearer ${tk}`)
    xhr.responseType = 'blob'
    xhr.onload = () => {
      // ★ #42（§4.2-2 组件侧补漏）：401 单独立即交给 core 统一清态回登录。
      //   旧写法把 401 混进「非 200」只弹一句「导出失败」——会话早已过期却仍停在后台，
      //   用户反复点导出也只得到同一句提示。XHR/blob 通道不经 request()，401 口径必须自己补齐。
      if (xhr.status === 401) { handleUnauthorized(url); return }
      // 失败时响应体也是 blob，读不到后端 message，只能回退到通用「导出失败」文案
      if (xhr.status !== 200) { toastError(t('tenants.exportFailed')); return }
      const a = document.createElement('a')
      a.href = URL.createObjectURL(xhr.response)
      a.download = `tenant_${tt.id}_${tt.code}.json`
      a.click()
      // 用完立刻 revoke：不释放的话每次导出的整包 JSON 都会挂在内存里
      URL.revokeObjectURL(a.href)
    }
    // 链路断掉时 onload 根本不会触发（旧写法只有 onload）⇒ 点导出什么都没发生；这里补一句出声
    xhr.onerror = () => toastError(t('tenants.exportFailed'))
    xhr.send(JSON.stringify({ id: tt.id }))
  }

  // 直接充值（走创建+支付两步）★ S1 积分口径（2026-09-15）：录入/提示均为积分，传 points 字段
  // 两步的目的：adminOrderCreate 先落一张 money=0 的订单（积分→内部 token 由后端按当期汇率换算，
  // 前端不碰 token 裸值），再 adminOrderPay 直接置付，等效超管代客「免收银台」到账。
  async function charge(tt: TenantInfo) {
    const points = await promptText({ body: tpl('tenants.chargePrompt', { name: tt.name }) })
    // 取消输入或填 0/负数都静默返回：取消不是错误，不该弹红条吓人
    if (!points || Number(points) <= 0) return
    const r = await runGuarded(() => adminOrderCreate({ tenant_id: tt.id, points: Number(points), money: 0 }))
    if (!r || !toastResp(r)) return
    // r.order 未在 AdminResp 里声明（后端随成功体附带），取用时按宽松别名读
    const o = (r as Any).order
    // 支付这步不再判 success：钱已由 create 记账，置付失败宁可留在「待支付」让人工接手，
    // 也不能在这里回滚或重复下单。但「网络断了」必须出声——旧写法裸 await，
    // 置付请求连不上时是一条未处理 rejection，超管以为已经到账。
    if (o && o.id) await runGuarded(() => adminOrderPay(o.id, tt.id))
    toastSuccess(tpl('tenants.charged', { points }))
    await load()
  }

  // removeTenant 删的是租户行本身：后端逐表清该 tenant_id 作用域下的全部业务表、最后删租户行
  // （不可逆，失败表会回在响应里），所以前端一次确认即够；删完必须刷 store，
  // 否则顶栏还能切到已不存在的租户
  async function removeTenant(tt: TenantInfo) {
    // ★ 2026-10-23（用户令「超管好像还是不能删除租户」）：默认租户（id=1）**不再隐藏删除入口**，
    //   但确认档位抬到「手输编码」——它会连带演示站与全站数据一起消失，一次确认框挡不住手滑。
    //   其它租户仍是单次 danger 确认（行内 Link 不叠第二层，详见操作列注释）。
    if (tt.id === 1) {
      const typed = await promptText({
        header: t('tenants.delete'),
        body: tpl('tenants.deleteConfirm', { name: tt.name }),
        placeholder: tt.code || tt.name,
        confirmText: t('tenants.delete'),
      })
      // 取消（null）或输错：静默返回，不发请求。后端也会按 confirm 逐字比对兜一道。
      if (typed === null || typed.trim() !== (tt.code || tt.name)) return
    } else if (!(await confirmDialog({ body: tpl('tenants.deleteConfirm', { name: tt.name }), danger: true }))) {
      return
    }
    // ★ 2026-10-23：confirm 传该行的 code（无 code 时退回 name），让服务端核对「要删的就是这一行」——
    // 列表若是旧数据（租户被改名/重建），服务端判 400 而不是照 id 删掉另一个租户
    const r = await runGuarded(() => tenantDelete(tt.id, tt.code || tt.name))
    if (!r || !toastResp(r)) return
    await load(); ad.loadTenants()
  }

  // erase 走 GDPR 删除权：清空该租户全部业务数据（含磁盘产物），但租户行与账号本身保留，
  // 所以它是 removeTenant 之外的独立入口、不可逆，故意连做两次不同文案的确认
  async function erase(tt: TenantInfo) {
    if (!(await confirmDialog({ body: tpl('tenants.eraseConfirm', { name: tt.name }), danger: true }))) return
    if (!(await confirmDialog({ body: tpl('tenants.eraseConfirm2', { name: tt.name }), danger: true }))) return
    const r = await runGuarded(() => tenantErase(tt.id))
    if (!r || !toastResp(r)) return
    toastSuccess(t('tenants.erased'))
    await load()
  }

  // setInvite 开关即落库，没有「保存」这一步：tenantUpdate 是全字段接口，
  // 所以未改动的 name/expires_at/permissions 必须原值带上，否则会被空值覆盖
  async function setInvite(tt: TenantInfo, val: boolean) {
    const r = await runGuarded(() => tenantUpdate({
      id: tt.id, name: tt.name, expires_at: tt.expires_at || '',
      permissions: tt.permissions || '{}', invite_enabled: val,
    }))
    // 开关失败必须回读列表把开关拨回真实状态，否则界面停在用户拨的那一格＝显示与库不一致
    if (!r || !toastResp(r)) { void load(); return }
    await load()
  }

  // grantTrial 试用额度与到期日完全由后端裁定，前端只确认+刷新，
  // 免得两端各存一套试用口径导致「显示 30 天实到 14 天」这类对不上
  async function grantTrial(tt: TenantInfo) {
    if (!(await confirmDialog({ body: tpl('tenants.grantTrialConfirm', { name: tt.name }) }))) return
    const r = await runGuarded(() => tenantGrantTrial(tt.id))
    if (!r || !toastResp(r)) return
    toastSuccess(t('tenants.grantTrialDone'))
    await load()
  }

  return (
    <Panel title={t('tenants.title')} extra={<Button variant="primary" onClick={() => { setForm({ permissions: '{}' }); setCodeErr(''); setDlg('create') }}>{t('tenants.create')}</Button>}>
      {/* 数据表格 */}
      <DataTable<any> rowKey={(row) => String(row.id)} rows={rows}
             columns={[
               { key: 'id', title: t('tenants.colId'), width: 60 },
               { key: 'code', title: t('tenants.colCode'), width: 120 },
               { key: 'name', title: t('tenants.colName') },
               { key: 'industry', title: t('tenants.industry'), width: 130, render: (row) => industryLabel(row.industry || '', lang) },
               // 状态列：原 Tag theme 三态映射到 StatusPill tone —— active=success、expired=warn（提醒续期）、
               // 其余（含 disabled）=idle；warn 而非 danger 是有意的，到期不等于故障
               { key: 'status', title: t('tenants.colStatus'), width: 90, render: (row) => {
                 const s = row.status
                 const label = s === 'active' ? t('tenants.enable') : s === 'disabled' ? t('tenants.disable') : t('tenants.expired')
                 return <StatusPill tone={s === 'active' ? 'success' : s === 'expired' ? 'warn' : 'idle'}>{label}</StatusPill>
               } },
                { key: 'expires_at', title: t('tenants.colExpires'), width: 120, render: (row) => row.expires_at || t('tenants.forever') },
                // 邀请开关没有「保存」按钮：onChange 里 void 起异步，改完立刻 setInvite 落库并整表重拉
                { key: 'invite', title: t('tenants.invite'), width: 110, render: (row) => (
                  <Switch checked={!!row.invite_enabled} onChange={(e) => { void setInvite(row, e.target.checked) }} />
                ) },
               // 操作列改为 <Link> 序列 + flex 容器（DataTable 规范：行内动作用链接态，破坏性用 tone="danger"）；
               // 新底座没有 Popconfirm，删除的二次确认挪进 onClick 内走全局 confirmDialog，链路等价
               { key: 'op', title: t('tenants.colActions'), width: 380, render: (row) => (
                 <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
                   <Link onClick={() => { setForm({ name: row.name, expires_at: row.expires_at || '', permissions: row.permissions || '{}', industry: row.industry || '' }); setDlg({ edit: row }) }}>{t('tenants.edit')}</Link>
                   <Link onClick={() => grantTrial(row)}>{t('tenants.grantTrial')}</Link>
                    <Link
                             onClick={async () => {
                               const r = await runGuarded(() => tenantSetStatus(row.id, row.status === 'active' ? 'disabled' : 'active'))
                               if (!r || !toastResp(r)) return
                               await load()
                             }}>
                      {row.status === 'active' ? t('tenants.disable') : t('tenants.enable')}
                    </Link>
                   {/* ★ 2026-10-23（用户令）：删除入口对**所有**租户都渲染，含默认租户 id=1。
                       默认租户的闸门从「不给按钮」改成「必须手输编码」（见 removeTenant）——
                       藏按钮会让超管以为「这个功能不存在」（用户当场就是这么报的），
                       而放开按钮又不该让一次点击就能删掉平台根基。
                       「清除数据」（erase，GDPR 擦除）仍对 id=1 隐藏：它保留租户行、只清业务数据，
                       和删除不是一件事，等真有需求再单独放开。
                       ★ 删除确认只在 removeTenant() 里那一次（2026-10-16 双确认批）：
                         旧写法这里先 confirm 一次、进去又 confirm 一次（且后面那次的文案不带租户名），
                         点第一次「确定」时请求还没发、列表不动——与 ApiKeysP 删除同形，一起收敛。 */}
                   <Link tone="danger" onClick={() => { void removeTenant(row) }}>{t('tenants.delete')}</Link>
                   <Link onClick={() => charge(row)}>{t('tenants.charge')}</Link>
                   <Link onClick={() => doExport(row)}>{t('tenants.exportData')}</Link>
                   {row.id !== 1 && (
                     <Link tone="danger" onClick={() => erase(row)}>{t('tenants.eraseData')}</Link>
                   )}
                 </div>
               ) },
             ]}  />

      {/* 新建与编辑复用同一弹窗：open 条件里同时判 'create' 与 {edit} 两种形态，
          标题按形态切换；管理员账号/初始密码只在创建态出现（编辑态不允许顺带重置密码） */}
      <Dialog open={!!dlg && (dlg === 'create' || (dlg && typeof dlg === 'object' && 'edit' in dlg))}
               onCancel={() => setDlg(null)} title={dlg === 'create' ? t('tenants.create') : t('tenants.title')}
               onConfirm={save}>
        {(dlg === 'create' || (dlg && typeof dlg === 'object' && 'edit' in dlg)) && (
          <>
            {dlg === 'create' && (
              <Field label={t('tenants.codePlaceholder')}>
                <input className="lc-input" aria-invalid={codeErr ? true : undefined} value={String(form.code || '')}
                       onChange={(e) => { setForm({ ...form, code: e.target.value }); if (codeErr) setCodeErr('') }} />
                {/* 就地错误：只弹一条 toast 就静默 return 的旧形态正是「点确定完全没反应」的观感来源 */}
                {codeErr ? <div role="alert" data-testid="tenant-code-err" style={{ color: 'var(--lc-danger, #E5484D)', fontSize: 13, marginTop: 4 }}>{codeErr}</div> : null}
              </Field>
            )}
            <Field label={t('tenants.namePlaceholder')}><input className="lc-input" value={String(form.name ?? '')} onChange={(e) => setForm({ ...form, name: e.target.value })} /></Field>
            {/* 有效期用手填 YYYY-MM-DD：与后端按日解析的口径一致，日期控件容易多带时分/时区而差一天 */}
            <Field label={t('tenants.colExpires')}><input className="lc-input" value={String(form.expires_at ?? '')} placeholder="YYYY-MM-DD" onChange={(e) => setForm({ ...form, expires_at: e.target.value })} /></Field>
            {/* 权限直接编辑 JSON 文本（默认 '{}'）：结构由后端解析，前端不做表单化，免得每加一类权限都要改面板 */}
            <Field label={t('tenants.permissionsHint')}><textarea className="lc-textarea" rows={3} value={String(form.permissions ?? '{}')} onChange={(e) => setForm({ ...form, permissions: e.target.value })} /></Field>
            {/* 行业下拉数据来自 loadInd（后端可增删启停），空值项=未设置；提交前由 save 折回 code */}
            <Field label={t('tenants.industry')}>
              <select className="lc-select" value={String(form.industry ?? '')} onChange={(e) => setForm({ ...form, industry: e.target.value })}>
                <option value="">{t('common.unset')}</option>
                {indList.map((o) => <option key={o.code} value={o.code}>{o.name}</option>)}
              </select>
            </Field>
            {dlg === 'create' && (
              <>
                <Field label={t('tenants.adminUserPlaceholder')}><input className="lc-input" value={String(form.admin_user || '')} onChange={(e) => setForm({ ...form, admin_user: e.target.value })} /></Field>
                <Field label={t('tenants.initPassPlaceholder')}><input className="lc-input" type="password" value={String(form.admin_pass || '')} onChange={(e) => setForm({ ...form, admin_pass: e.target.value })} /></Field>
              </>
            )}
          </>
        )}
      </Dialog>

      {/* dlg 的第三种形态 {order}：手填积分/金额的充值单，与表格里 charge() 的「一步免收银台」互补，
          用于需要同时记现金金额的场景；注意输入框 state 名仍是历史遗留的 tokens，
          但提交走的是 points 字段、标签也是积分（S1 积分口径），别按 token 理解 */}
      <Dialog open={!!dlg && typeof dlg === 'object' && 'order' in dlg}
              onCancel={() => setDlg(null)} title={dlg && typeof dlg === 'object' && 'order' in dlg ? `#${(dlg as { order: TenantInfo }).order.id} ${t('tenants.charge')}` : ''}
              onConfirm={async () => {
                if (!(dlg && typeof dlg === 'object' && 'order' in dlg)) return
                const tt = (dlg as { order: TenantInfo }).order
                const r = await runGuarded(() => adminOrderCreate({ tenant_id: tt.id, points: Number(form.tokens || 0), money: Number(form.money || 0) }))
                // toastResp 已负责失败弹错/成功提示：返回 false 时保持弹窗不关，让超管改数重试而不是白丢一张单
                if (r && toastResp(r, t('tenants.charged'))) {
                  const oid = Number((r as Any).order?.id ?? (r as Any).id ?? 0)
                  // 置付失败只出声不回滚（订单留在待支付），理由同 charge()
                  if (oid > 0) await runGuarded(() => adminOrderPay(oid, tt.id))
                  setDlg(null)
                }
              }}>
        <Field label={t('tenants.fieldPoints')}><input className="lc-input" type="number" value={num(form.tokens || 0)} onChange={(e) => setForm({ ...form, tokens: e.target.value })} /></Field>
        <Field label="¥"><input className="lc-input" type="number" value={num(form.money || 0)} onChange={(e) => setForm({ ...form, money: e.target.value })} /></Field>
      </Dialog>
    </Panel>
  )
}
