// components/KbUploadDialog.tsx — 前台顶部栏「上传知识库」弹窗
// 复用后台既有逻辑：识别（recognize-kb，需部门管理员及以上）→ 选包 → 导入（import-kb，自动 embed）。
// 普通用户无此入口；后端 requireDeptAdmin 再次校验权限。
// 可选包按角色过滤（后端已做）：超管可见全部；租户管理员仅企业包/部门包；部门管理员仅本部门及子部门部门包。
// 部门包按组织层级以多级下拉框（Cascader）展示，叶子即包名（一级只显示名字）。
import { useEffect, useState } from 'react'
import { Dialog, Button, Cascader, Tag, MessagePlugin } from 'tdesign-react'
import { t } from '@/i18n'
import { kbRecognizeFile, kbPackages, kbImportFile } from '@/api/kb'
import { orgList, type OrgInfo } from '@/api/org'
import { useAdmin } from '@/stores/admin'

interface Props {
  visible: boolean
  onClose: () => void
}

// 通用弱类型别名（与后台面板一致）：用于松类型的接口响应/行数据
type Any = any

interface Pkg {
  id: number
  name: string
  pack_type: string
  org_id?: number
  org_name?: string
  tenant_name?: string
}

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

export default function KbUploadDialog({ visible, onClose }: Props) {
  const ad = useAdmin()
  const [file, setFile] = useState<File | null>(null)
  const [recognizing, setRecognizing] = useState(false)
  const [recognized, setRecognized] = useState<Any | null>(null)
  const [pkgs, setPkgs] = useState<Pkg[]>([])
  const [orgs, setOrgs] = useState<OrgInfo[]>([])
  const [pkgId, setPkgId] = useState<number>(0)
  const [importing, setImporting] = useState(false)
  const [result, setResult] = useState<Any | null>(null)

  // 组织树优先用「登录时静默加载」的全局组织树（ad.orgs）；若为空则本地兜底拉取一次
  const effectiveOrgs = ad.orgs.length > 0 ? ad.orgs : orgs
  // 组织根节点（其下直接为部门）；找不到则从头（0）构建
  const rootOrg = effectiveOrgs.find((o) => o.type === 'root')
  const startParent = rootOrg ? rootOrg.id : 0

  // 打开时加载可导入的知识库包（后端按角色过滤）；组织树优先用全局（登录已加载）
  useEffect(() => {
    if (!visible) return
    setFile(null); setRecognized(null); setResult(null); setPkgId(0)
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

  async function startRecognize() {
    if (!file) return
    setRecognizing(true); setRecognized(null); setResult(null)
    try {
      const r = await kbRecognizeFile(file)
      if (r.success) setRecognized(r as Any)
      else MessagePlugin.error(r.message || t('kb.recognizeFailed'))
    } catch (err: any) {
      MessagePlugin.error(t('kb.recognizeErr').replace('{msg}', err?.message || t('kb.networkErr')))
    } finally { setRecognizing(false) }
  }

  async function startImport() {
    if (!recognized?.temp_id || !pkgId) return
    setImporting(true); setResult(null)
    try {
      const r = await kbImportFile({ temp_id: String(recognized.temp_id), package_id: pkgId })
      setResult(r as Any)
      if (r.success) { setFile(null); setRecognized(null); setPkgId(0) }
    } catch (err: any) {
      setResult({ success: false, message: t('kb.importErr').replace('{msg}', err?.message || t('kb.networkErr')) })
    } finally { setImporting(false) }
  }

  // 按类型分组：企业包 / 部门包（组织树）/ 行业包 / 语言文化包
  const tenantPkgs = pkgs.filter((p) => p.pack_type === 'tenant')
  const industryPkgs = pkgs.filter((p) => p.pack_type === 'industry')
  const localePkgs = pkgs.filter((p) => p.pack_type === 'locale')
  const deptByOrg = new Map<number, Pkg[]>()
  for (const p of pkgs) {
    if (p.pack_type === 'department') {
      const arr = deptByOrg.get(p.org_id || 0) || []
      arr.push(p); deptByOrg.set(p.org_id || 0, arr)
    }
  }
  const options: COpt[] = []
  // 企业包：以「租户名称」作为选项标签（后端“企业包”概念映射为实际租户名，不显示“企业包”字样）
  const tenantName =
    tenantPkgs[0]?.tenant_name ||
    pkgs.find((p) => p.tenant_name)?.tenant_name ||
    t('kb.typeTenant')
  for (const p of tenantPkgs) {
    options.push({ label: tenantName, value: p.id })
  }
  // 部门包：按组织树以「部门名称」为标签多级展示（路径即部门层级），叶子即部门名
  options.push(...orgTreeOptions(effectiveOrgs, deptByOrg, startParent))
  // 兜底：组织树缺失时，部门包以部门名（org_name）作为顶层选项，避免不可见
  const orgIdSet = new Set(effectiveOrgs.map((o) => o.id))
  for (const p of pkgs) {
    if (p.pack_type === 'department' && !orgIdSet.has(p.org_id || 0)) {
      options.push({ label: p.org_name || p.name, value: p.id })
    }
  }
  // 行业包 / 语言文化包（仅超管）：以平台分类名为分组
  if (industryPkgs.length) {
    options.push({ label: t('kb.typeIndustry'), value: -2, children: industryPkgs.map((p) => ({ label: p.name, value: p.id })) })
  }
  if (localePkgs.length) {
    options.push({ label: t('kb.typeLocale'), value: -3, children: localePkgs.map((p) => ({ label: p.name, value: p.id })) })
  }

  return (
    <Dialog
      visible={visible}
      onClose={onClose}
      header={t('kb.topbarUpload')}
      footer={false}
      width={560}
    >
      <div style={{ fontSize: 13, color: '#556', marginBottom: 12 }}>{t('kb.topbarHint')}</div>

      <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 12 }}>
        <input
          type="file"
          accept=".csv,.xlsx,.xls"
          onChange={(e: any) => { setFile(e.target.files?.[0] || null); setRecognized(null); setResult(null); setPkgId(0); e.currentTarget.value = '' }}
        />
        <Button onClick={() => void startRecognize()} disabled={!file || recognizing} loading={recognizing}>
          {recognizing ? t('kb.recognizing') : t('kb.recognize')}
        </Button>
      </div>

      {file && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: '#334', marginBottom: 12 }}>
          <span>📄 {t('kb.fileSelected')}{file.name}{fileExt(file.name) && ` (${fileExt(file.name)})`}</span>
          <Button size="small" variant="text" theme="danger" onClick={() => { setFile(null); setRecognized(null); setResult(null); setPkgId(0) }}>
            {t('kb.fileRemove')}
          </Button>
        </div>
      )}

      {recognized && (
        <div style={{ fontSize: 13, marginBottom: 12 }}>
          <div>
            {t('kb.kbTotal').replace('{total}', recognized.total).replace('{n}', (recognized.lang_cols || []).length)}
            {(recognized.new_langs || []).length > 0 && (
              <span> {t('kb.kbNewLangs')} {(recognized.new_langs || []).map((l: string) => <Tag key={l} size="small">{l}</Tag>)}</span>
            )}
          </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 10 }}>
            {options.length === 0 ? (
              <span style={{ fontSize: 13, color: '#888' }}>{t('kb.noPkg')}</span>
            ) : (
              <Cascader
                value={pkgId || undefined}
                onChange={(v: any) => setPkgId(Number(v))}
                options={options}
                placeholder={t('kb.selectPkg')}
                style={{ minWidth: 320 }}
              />
            )}
            <Button theme="success" onClick={() => void startImport()} disabled={!pkgId || importing} loading={importing}>
              {t('kb.import')}
            </Button>
          </div>
        </div>
      )}

      {result && (
        <div style={{ fontSize: 13, color: result.success ? '#2e7d32' : '#c62828' }}>
          {result.message || (result.success ? t('kb.import') + ' OK' : t('kb.importErr').replace('{msg}', ''))}
        </div>
      )}
    </Dialog>
  )
}

function fileExt(name: string): string {
  const i = name.lastIndexOf('.')
  return i >= 0 ? name.slice(i + 1).toUpperCase() : ''
}
