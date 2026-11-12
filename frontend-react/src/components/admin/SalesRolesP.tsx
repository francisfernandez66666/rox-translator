// ============ SalesRolesP.tsx · 职责说明 ============
// 三期 W3 角色权限设置页（PanelKey 面板，不加 URL 路由、不动 App.tsx / spa.go）。
//
// 行 = 权限项全集（后端 GET 回传，前端不抄第二份清单）；列 = 四档角色 Switch
// （user / dept_admin / tenant_admin / super_admin，★ 不新增第五种角色常量）。
// 完整 4×10 行式矩阵由后端补齐缺省后回传，前端只显示与回写，杜绝自己再造一份缺省表。
// 所有文案一律 t('键名')，无硬编码中文；组件只用 @/ui/langcross/src 与 ./parts。
// ================================================
import { useCallback, useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { Button, DataTable, EmptyState, InlineBanner, PageHeader, StatusPill, Switch, type TableColumn } from '@/ui/langcross/src'
import { Panel } from './parts'
import { salesRolesGet, salesRolesSave } from '@/api/salesRoles'

/** 权限项 → 界面类别文案（静态 switch，保证每个 t() 都是字面键，不用动态拼键绕闸） */
function permLabel(k: string, t: (key: string) => string): string {
  switch (k) {
    case 'bot_toggle': return t('sales.roles.permManage')
    case 'kb_edit': return t('sales.roles.permEdit')
    case 'script_edit': return t('sales.roles.permEdit')
    case 'lead_view': return t('sales.roles.permView')
    case 'lead_route': return t('sales.roles.permExport')
    case 'quote_issue': return t('sales.roles.permEdit')
    case 'quote_approve': return t('sales.roles.permApprove')
    case 'outbound_config': return t('sales.roles.permManage')
    case 'consent_manage': return t('sales.roles.permManage')
    case 'ops_view': return t('sales.roles.permView')
    default: return t('sales.roles.permView')
  }
}

/** 角色档 → 界面文案（静态 switch，四档一一对应，无第五档） */
function levelLabel(lvl: number, t: (key: string) => string): string {
  switch (lvl) {
    case 1: return t('sales.roles.roleUser')
    case 2: return t('sales.roles.roleDeptAdmin')
    case 3: return t('sales.roles.roleTenantAdmin')
    case 4: return t('sales.roles.roleSuperAdmin')
    default: return t('sales.roles.roleUser')
  }
}

/** 角色权限设置面板（租户管理员及以上） */
export default function SalesRolesP() {
  const [, t] = useT()
  const [permKeys, setPermKeys] = useState<string[]>([])
  const [levels, setLevels] = useState<number[]>([])
  const [matrix, setMatrix] = useState<Record<string, boolean>>({})
  const [dirty, setDirty] = useState<Record<string, boolean>>({})
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)
  const [saved, setSaved] = useState(false)
  const [saveFailed, setSaveFailed] = useState(false)

  /** 读完整矩阵：失败出声，不把「取不到」伪装成出厂默认 */
  const refresh = useCallback(() => {
    return salesRolesGet()
      .then((r) => {
        if (!r.success || !r.matrix) { setFailed(true); return }
        const m: Record<string, boolean> = {}
        for (const row of r.matrix) m[`${row.role_level}:${row.perm_key}`] = row.allowed
        setMatrix(m)
        setPermKeys(Array.isArray(r.perm_keys) ? r.perm_keys : [])
        setLevels(Array.isArray(r.levels) ? r.levels : [])
        setDirty({})
        setFailed(false)
      })
      .catch(() => setFailed(true))
  }, [])

  useEffect(() => { void refresh() }, [refresh])

  /** 本地切换一格（不改后端，攒到「保存」逐项提交，保持每格独立） */
  const toggle = (lvl: number, perm: string, allowed: boolean) => {
    const key = `${lvl}:${perm}`
    setMatrix((m) => ({ ...m, [key]: allowed }))
    setDirty((d) => ({ ...d, [key]: allowed }))
  }

  /** 保存全部脏项（唯一写口 salesRolesSave，逐项独立提交） */
  const saveAll = () => {
    const entries = Object.entries(dirty)
    if (busy || entries.length === 0) return
    setBusy(true); setSaved(false); setSaveFailed(false)
    Promise.all(entries.map(([key, allowed]) => {
      const [lvl, perm] = key.split(':')
      return salesRolesSave({ role_level: Number(lvl), perm_key: perm, allowed })
    }))
      .then((rs) => {
        if (rs.every((r) => r.success)) { setSaved(true); setDirty({}); void refresh() }
        else setSaveFailed(true)
      })
      .catch(() => setSaveFailed(true))
      .finally(() => setBusy(false))
  }

  const columns: TableColumn<string>[] = [
    { key: 'perm', title: t('sales.roles.colPerm'), render: (k) => k },
    { key: 'desc', title: t('sales.roles.colDesc'), render: (k) => permLabel(k, t) },
    ...levels.map((lvl) => ({
      key: `lvl_${lvl}`,
      title: levelLabel(lvl, t),
      render: (k: string) => (
        <Switch
          aria-label={`${levelLabel(lvl, t)}:${k}`}
          checked={matrix[`${lvl}:${k}`] ?? false}
          disabled={busy}
          onChange={(e) => toggle(lvl, k, e.target.checked)}
        />
      ),
    })),
    { key: 'status', title: t('sales.roles.colStatus'), render: (k) => (
      <StatusPill tone="idle">{permLabel(k, t)}</StatusPill>
    ) },
  ]

  return (
    <div data-testid="sales-roles-page">
      <PageHeader title={t('sales.roles.title')} />
      <InlineBanner tone="warn">{t('sales.roles.subtitle')}</InlineBanner>
      {failed && <InlineBanner tone="error">{t('sales.roles.loadFailed')}</InlineBanner>}
      {saved && <InlineBanner tone="success">{t('sales.roles.saved')}</InlineBanner>}
      {saveFailed && <InlineBanner tone="error">{t('sales.roles.saveFailed')}</InlineBanner>}

      <Panel title={t('sales.roles.colRole')}>
        <InlineBanner tone="warn">{t('sales.roles.hintNoNewRole')}</InlineBanner>
        <InlineBanner tone="warn">{t('sales.roles.hintLevelMatrix')}</InlineBanner>
        <span>{t('sales.roles.level2')}</span>
        <span>{t('sales.roles.level3')}</span>
        <span>{t('sales.roles.level4')}</span>
      </Panel>

      {permKeys.length === 0 && !failed && <EmptyState title={t('sales.roles.empty')} />}
      {permKeys.length > 0 && (
        <Panel title={t('sales.roles.title')}>
          <DataTable rows={permKeys} rowKey={(k) => k} columns={columns} />
        </Panel>
      )}

      <Button data-testid="sales-roles-reload" variant="secondary" disabled={busy} onClick={() => void refresh()}>
        {t('sales.roles.reload')}
      </Button>
      <Button data-testid="sales-roles-save" disabled={busy || Object.keys(dirty).length === 0} onClick={saveAll}>
        {t('sales.roles.save')}
      </Button>
    </div>
  )
}
