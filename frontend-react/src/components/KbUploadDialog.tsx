// components/KbUploadDialog.tsx — 前台顶部栏「上传知识库」弹窗
// 复用后台既有逻辑：识别（recognize-kb，需部门管理员及以上）→ 选包 → 导入（import-kb，自动 embed）。
// 普通用户无此入口；后端 requireDeptAdmin 再次校验权限。
// 可选包按角色过滤（后端已做）：超管可见全部；租户管理员仅企业包/部门包；部门管理员仅本部门及子部门部门包。
// 部门包按组织层级以多级下拉框（原 Cascader）展示，叶子即包名（一级只显示名字）。
// 呈现层替换：TDesign Dialog→受控浮层（复用 lc-dialog 样式）、Cascader→两级 Select、
//             Tag→Badge(mono)、MessagePlugin→useToast、Button→langcross Button。
import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { Button, Badge, useToast } from '@/ui/langcross/src'
import { t, tpl } from '@/i18n'
import { kbRecognizeFile, kbPackages, kbImportFile } from '@/api/kb'
import { orgList, type OrgInfo } from '@/api/org'
import { useAdmin } from '@/stores/admin'

// ============ 本文件职责中文说明 ============
// 前台「上传知识库」弹窗：识别文件、选择知识包并导入。
// ========================================

// Props 弹窗属性：是否可见与关闭回调
interface Props {
  visible: boolean
  onClose: () => void
}

// 通用弱类型别名（与后台面板一致）：用于松类型的接口响应/行数据
type Any = any

// Pkg 知识包信息（与后端返回字段对应）：标识、名称、类型、归属组织与跨部门共享范围
interface Pkg {
  id: number
  name: string
  pack_type: string
  org_id?: number
  org_name?: string
  tenant_name?: string
  share_cross_dept?: number
  cross_all?: boolean
  cross_orgs?: number[]
}

// COpt 多级下拉（原 Cascader）选项结构：label/value 及可选子节点
// value 用负数当「目录节点」哨兵（-部门ID / -2 行业 / -3 语言文化 / -4 跨部门）：
// 真实包 ID 恒为正，负值只用于展开二级，永远不会被当成可导入的包提交
interface COpt {
  label: string
  value: number
  children?: COpt[]
}

// 由扁平组织列表组装部门树；部门包以「部门名称」为标签挂到对应部门下（不再显示“部门包”字样）。
// startParent 为组织根（其下直接为部门）；根组织节点本身被跳过，部门提升为顶层。
function orgTreeOptions(orgs: OrgInfo[], deptByOrg: Map<number, Pkg[]>, startParent: number): COpt[] {
  const build = (parentId: number): COpt[] => {
    const res: COpt[] = []
    for (const o of orgs) {
      if (o.parent_id !== parentId) continue
      // 跳过根组织节点，将其子部门提升为顶层，避免与“租户名”企业包重复
      if (parentId === 0 && o.type === 'root') {
        res.push(...build(o.id))
        continue
      }
      const packs = deptByOrg.get(o.id) || []
      const children: COpt[] = [...build(o.id)]
      if (packs.length > 0) {
        if (children.length === 0) {
          // 叶子部门：以部门名为选项（单一包直接选；多包则展开包名）
          if (packs.length === 1) res.push({ label: o.name, value: packs[0].id })
          else res.push({ label: o.name, value: -o.id, children: packs.map((p) => ({ label: p.name, value: p.id })) })
        } else {
          // 既有子部门又有本部门包：本部门包作为子项
          children.push({ label: o.name, value: packs[0].id })
          res.push({ label: o.name, value: -o.id, children })
        }
      } else if (children.length > 0) {
        res.push({ label: o.name, value: -o.id, children })
      }
      // 既无包又无子部门：剪掉
    }
    return res
  }
  return build(startParent)
}

// 由知识包列表 + 组织树构建前台上传弹窗所用的两级选包选项（企业包/部门包(组织树)/行业包/语言文化包）。
// 抽出为导出函数，供后台知识库面板复用，保证前后台「选包」交互完全一致。
export function buildKbCascaderOptions(pkgs: Pkg[], orgs: OrgInfo[]): COpt[] {
  const rootOrg = orgs.find((o) => o.type === 'root')
  const startParent = rootOrg ? rootOrg.id : 0

  const tenantPkgs = pkgs.filter((p) => p.pack_type === 'tenant')
  const industryPkgs = pkgs.filter((p) => p.pack_type === 'industry')
  const localePkgs = pkgs.filter((p) => p.pack_type === 'locale')
  const crossDeptPkgs = pkgs.filter((p) => p.pack_type === 'cross_dept')
  const deptByOrg = new Map<number, Pkg[]>()
  for (const p of pkgs) {
    if (p.pack_type === 'department') {
      const arr = deptByOrg.get(p.org_id || 0) || []
      arr.push(p)
      deptByOrg.set(p.org_id || 0, arr)
    }
  }
  const options: COpt[] = []
  const tenantName =
    tenantPkgs[0]?.tenant_name ||
    pkgs.find((p) => p.tenant_name)?.tenant_name ||
    t('kb.typeTenant')
  for (const p of tenantPkgs) options.push({ label: tenantName, value: p.id })
  options.push(...orgTreeOptions(orgs, deptByOrg, startParent))
  const orgIdSet = new Set(orgs.map((o) => o.id))
  for (const p of pkgs) {
    if (p.pack_type === 'department' && !orgIdSet.has(p.org_id || 0)) {
      options.push({ label: p.org_name || p.name, value: p.id })
    }
  }
  if (industryPkgs.length) {
    options.push({ label: t('kb.typeIndustry'), value: -2, children: industryPkgs.map((p) => ({ label: p.name, value: p.id })) })
  }
  if (localePkgs.length) {
    options.push({ label: t('kb.typeLocale'), value: -3, children: localePkgs.map((p) => ({ label: p.name, value: p.id })) })
  }
  if (crossDeptPkgs.length) {
    options.push({ label: t('kb.typeCrossDept'), value: -4, children: crossDeptPkgs.map((p) => ({ label: p.name, value: p.id })) })
  }
  return options
}

// 知识包可见范围文案（按澄清的五档权限模型：部门/跨部门/企业/行业/通用语言习惯包）
function pkgScopeText(p: Pkg, t: (k: string) => string, tpl: (k: string, vars?: Record<string, string | number>) => string): string {
  if (p.pack_type === 'locale') return t('kb.scopeUniversal')
  if (p.pack_type === 'industry') return t('kb.scopeIndustry')
  if (p.pack_type === 'tenant') return t('kb.scopeTenant')
  if (p.pack_type === 'cross_dept') return p.cross_all ? t('kb.scopeCrossAll') : tpl('kb.scopeCrossDepts', { n: (p.cross_orgs || []).length })
  if (p.pack_type === 'department') return (p.share_cross_dept ?? 1) === 1 ? t('kb.scopeCross') : t('kb.scopeDept')
  return ''
}

/** KbUploadDialog · 职责说明：前台顶部栏「上传知识库」弹窗，识别文件 → 选择知识包 → 导入（自动 embed） */
export default function KbUploadDialog({ visible, onClose }: Props) {
  const ad = useAdmin()
  const { toast } = useToast()
  const [file, setFile] = useState<File | null>(null)
  const [recognizing, setRecognizing] = useState(false)
  const [uploadPct, setUploadPct] = useState(0) // ★ H5 分片上传进度
  const [recognized, setRecognized] = useState<Any | null>(null)
  const [pkgs, setPkgs] = useState<Pkg[]>([])
  const [orgs, setOrgs] = useState<OrgInfo[]>([])
  const [pkgId, setPkgId] = useState<number>(0)
  // 两级选包：catValue = 选中的一级（分类/目录）；有子级时才出现二级
  const [catValue, setCatValue] = useState<number | ''>('')
  const [importing, setImporting] = useState(false)
  const [result, setResult] = useState<Any | null>(null)

  // 组织树优先用「登录时静默加载」的全局组织树（ad.orgs）；若为空则本地兜底拉取一次
  const effectiveOrgs = ad.orgs.length > 0 ? ad.orgs : orgs

  // 打开时加载可导入的知识库包（后端按角色过滤）；组织树优先用全局（登录已加载）
  useEffect(() => {
    if (!visible) return
    setFile(null); setRecognized(null); setResult(null); setPkgId(0); setCatValue('')
    ;(async () => {
      try {
        const r = await kbPackages()
        if (r.success) setPkgs(((r as unknown as { packages?: Pkg[] }).packages) || [])
      } catch { /* ignore */ }
      // 全局组织树为空时兜底拉取（正常登录后已由 AdminProvider 静默加载）
      if (ad.orgs.length === 0) {
        try {
          const o = await orgList()
          if (o.success) setOrgs(o.orgs || [])
        } catch { /* ignore */ }
      }
    })()
  }, [visible])

  // Esc 关闭（等价 TDesign Dialog 默认行为）
  useEffect(() => {
    if (!visible) return
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [visible, onClose])

  // startRecognize 识别所选知识文件（recognize-kb），成功后在结果中记录 temp_id 供导入使用
  async function startRecognize() {
    if (!file) return
    setRecognizing(true); setRecognized(null); setResult(null)
    try {
      // ★ H5：大文件（≥4MB）自动分片续传上传，进度可见
      const r = await kbRecognizeFile(file, undefined, (pct) => setUploadPct(pct))
      if (r.success) setRecognized(r as Any)
      else toast({ title: r.message || t('kb.recognizeFailed'), tone: 'error' })
    } catch (err: any) {
      toast({ title: t('kb.recognizeErr').replace('{msg}', err?.message || t('kb.networkErr')), tone: 'error' })
    } finally { setRecognizing(false); setUploadPct(0) }
  }

  // startImport 将已识别文件（temp_id）导入所选知识包（import-kb，自动 embed），成功则清空已选文件
  async function startImport() {
    if (!recognized?.temp_id || !pkgId) return
    setImporting(true); setResult(null)
    try {
      const r = await kbImportFile({ temp_id: String(recognized.temp_id), package_id: pkgId })
      setResult(r as Any)
      if (r.success) { setFile(null); setRecognized(null); setPkgId(0); setCatValue('') }
    } catch (err: any) {
      setResult({ success: false, message: t('kb.importErr').replace('{msg}', err?.message || t('kb.networkErr')) })
    } finally { setImporting(false) }
  }

  // options 由已加载的知识包列表与组织树构建选包下拉选项（企业包/部门包组织树/行业包/语言文化包）
  const options = buildKbCascaderOptions(pkgs, effectiveOrgs)

  // 两级选包联动：选中一级后，若无子级则直接写入 pkgId；有子级则等待二级选择
  // 比对统一转字符串：原生 <select> 的 value 只会是字符串，而目录哨兵是负数，
  // 用 === 直接比 number 会因类型不同恒 false（表现为二级列表永远选不中）
  const catOpt = options.find((o) => String(o.value) === String(catValue)) || null
  const subOpts = catOpt?.children || []
  const subOpt = subOpts.find((c) => String(c.value) === String(pkgId)) || null

  function onPickCat(v: number) {
    setCatValue(v)
    const opt = options.find((o) => o.value === v)
    if (opt && !opt.children) setPkgId(v) // 叶子（如企业包、单包部门）：直接选包
    else setPkgId(0) // 父级（行业/语言/跨部门/多包部门）：等待二级子项
  }
  function onPickSub(v: number) { setPkgId(v) }

  // 二级展示文案
  const subDisplay = subOpt?.label ?? t('kb.selectPkg')

  if (!visible) return null

  // portal 到 body：顶栏容器带 overflow/stacking context，就地渲染会被裁切且压不住页面内容
  return createPortal(
    <div
      className="lc-overlay"
      // 用 mousedown + 严格 sameNode 判定：只有按下点正好落在遮罩自身才关闭，
      // 从对话框内起手的文字拖选、松手在遮罩上都不会误关（click 判定就会）
      onMouseDown={(e) => { if (e.target === e.currentTarget) onClose() }}
    >
      <div className="lc-dialog" role="dialog" aria-modal="true" style={{ width: 560 }}>
        <div className="lc-dialog__title">{t('kb.topbarUpload')}</div>
        <div className="lc-dialog__body">
          <div style={{ fontSize: 13, color: 'var(--lc-text-3)', marginBottom: 12 }}>{t('kb.topbarHint')}</div>

      <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 12 }}>
        <input
          type="file"
          accept=".csv,.xlsx,.xls"
              onChange={(e: any) => { setFile(e.target.files?.[0] || null); setRecognized(null); setResult(null); setPkgId(0); setCatValue(''); e.currentTarget.value = '' }}
            />
            <Button variant="secondary" onClick={() => void startRecognize()} disabled={!file || recognizing}>
          {recognizing ? (uploadPct > 0 && uploadPct < 100 ? `${t('kb.uploading')} ${uploadPct}%` : t('kb.recognizing')) : t('kb.recognize')}
            </Button>
          </div>

      {file && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--lc-text-2)', marginBottom: 12 }}>
              <span> {t('kb.fileSelected')}{file.name}{fileExt(file.name) && ` (${fileExt(file.name)})`}</span>
              <Button size="sm" variant="danger" onClick={() => { setFile(null); setRecognized(null); setResult(null); setPkgId(0); setCatValue('') }}>
            {t('kb.fileRemove')}
              </Button>
            </div>
          )}

      {recognized && (
        <div style={{ fontSize: 13, marginBottom: 12 }}>
          <div>
            {t('kb.kbTotal').replace('{total}', recognized.total).replace('{n}', (recognized.lang_cols || []).length)}
            {(recognized.new_langs || []).length > 0 && (
                  <span> {t('kb.kbNewLangs')} {(recognized.new_langs || []).map((l: string) => <Badge key={l} mono>{l}</Badge>)}</span>
                )}
              </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 10 }}>
            {options.length === 0 ? (
                  <span style={{ fontSize: 13, color: 'var(--lc-text-3)' }}>{t('kb.noPkg')}</span>
            ) : (
                  <>
                    <select
                      className="lc-select"
                      aria-label={t('kb.selectPkg')}
                      value={catValue === '' ? '' : String(catValue)}
                      onChange={(e) => onPickCat(Number(e.target.value))}
                      style={{ minWidth: 200 }}
                    >
                      <option value="">{t('kb.selectPkg')}</option>
                      {options.map((o) => (
                        <option key={o.value} value={o.value}>{o.label}</option>
                      ))}
                    </select>
                    {subOpts.length > 0 && (
                      <select
                        className="lc-select"
                        aria-label={t('kb.selectPkg')}
                        value={subOpt ? String(pkgId) : ''}
                        onChange={(e) => onPickSub(Number(e.target.value))}
                        style={{ minWidth: 200 }}
                      >
                        <option value="">{subDisplay}</option>
                        {subOpts.map((c) => (
                          <option key={c.value} value={c.value}>{c.label}</option>
                        ))}
                      </select>
                    )}
                  </>
                )}
                <Button variant="primary" onClick={() => void startImport()} disabled={!pkgId || importing}>
              {t('kb.import')}
                </Button>
            {pkgId > 0 ? (() => {
              const p = pkgs.find((x) => x.id === pkgId)
              return p ? (
                    <span style={{ fontSize: 12, color: 'var(--lc-text-3)' }}>
                  {t('kb.scopePrefix')}{pkgScopeText(p, t, tpl)}
                </span>
              ) : null
            })() : null}
              </div>
            </div>
          )}

      {result && (
            <div style={{ fontSize: 13, color: result.success ? 'var(--lc-text)' : 'var(--lc-danger)' }}>
          {result.message || (result.success ? t('kb.import') + ' OK' : t('kb.importErr').replace('{msg}', ''))}
            </div>
          )}
        </div>
        <div className="lc-dialog__actions">
          <button type="button" className="lc-btn lc-btn--secondary" style={{ padding: '8px 16px', fontSize: 13 }} onClick={onClose}>{t('common.close')}</button>
        </div>
      </div>
    </div>,
    document.body,
  )
}

// fileExt 取文件名扩展名并转为大写，无扩展名时返回空字符串（用于展示文件类型）
export function fileExt(name: string): string {
  const i = name.lastIndexOf('.')
  return i >= 0 ? name.slice(i + 1).toUpperCase() : ''
}
