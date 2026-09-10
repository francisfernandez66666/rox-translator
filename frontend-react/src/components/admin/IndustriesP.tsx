// ============================================================================
// components/admin/IndustriesP.tsx — 行业管理面板（2026-09-10 需求：超管可创建/维护行业）
// 行业以 kb_packages 平台行业包（pack_type=industry，宿主租户0）为承载：
//  - 列表：全部行业（code / 名称 / 语料条目数 / 启用状态 / 创建时间）
//  - 新建行业：填 code（小写字母/数字/下划线，全局唯一）+ 中文名
//  - 编辑行业名 / 启用停用 / 删除（被企业租户引用的行业拒绝删除，提示改用停用）
// 行业字典经 GET /api/admin/industries 提供；注册页/租户表单/数据采集下拉均动态拉取，
// 故此处新建/编辑即时影响全站行业下拉（停用后注册选择中消失）。
// ============================================================================
import { useEffect, useState } from 'react'
import { Button, Table, Input, Dialog, MessagePlugin, Popconfirm, Space, Tag, Switch } from 'tdesign-react'
import { industries, industryCreate, industryUpdate, industryStatus, industryDelete, type IndustryItem } from '@/api/industry'
import { useT } from '@/i18n'

type Props = Record<string, never>

/** 表格时间格式：截取到分（2026-09-10T08:00:00+08:00 → 08:00） */
const shortTime = (s?: string) => (s && s.length >= 19 ? s.slice(11, 16) : s || '—')

/**
 * IndustriesP 行业管理面板（2026-09-10 需求：超管可创建和维护行业字段）。
 * 展示平台全部行业；超管可新建（code 唯一）、编辑名称、启停、删除（引用保护）。
 */
export default function IndustriesP(_props: Props) {
  const [, t] = useT()
  const [rows, setRows] = useState<IndustryItem[]>([])
  const [dlg, setDlg] = useState<null | { mode: 'create' } | { mode: 'edit'; row: IndustryItem }>(null)
  const [code, setCode] = useState('')   // 新建行业 code（小写字母/数字/下划线）
  const [name, setName] = useState('')   // 行业显示名（中/英文均可）

  const load = async () => {
    const r = await industries()
    if (r.success && r.industries) setRows(r.industries)
  }
  useEffect(() => { void load() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // 新建行业
  const doCreate = async () => {
    const c = code.trim()
    const n = name.trim()
    if (!c || !n) { void MessagePlugin.warning('请填写行业 code 与名称'); return }
    const r = await industryCreate({ code: c, name: n })
    if (r.success) {
      void MessagePlugin.success('行业已创建')
      setDlg(null); setCode(''); setName('')
      await load()
    } else {
      void MessagePlugin.error(r.message || '创建失败')
    }
  }

  // 编辑行业名
  const doUpdate = async () => {
    if (!(dlg && dlg.mode === 'edit')) return
    const n = name.trim()
    if (!n) { void MessagePlugin.warning('名称不能为空'); return }
    const r = await industryUpdate(dlg.row.id, n)
    if (r.success) {
      void MessagePlugin.success('已保存')
      setDlg(null)
      await load()
    } else {
      void MessagePlugin.error(r.message || '保存失败')
    }
  }

  // 启停行业
  const doToggle = async (row: IndustryItem, enabled: number) => {
    const r = await industryStatus(row.id, enabled)
    if (r.success) {
      void MessagePlugin.success(enabled === 1 ? '行业已启用' : '行业已停用')
      await load()
    } else {
      void MessagePlugin.error(r.message || '操作失败')
    }
  }

  // 删除行业
  const doDelete = async (row: IndustryItem) => {
    const r = await industryDelete(row.id)
    if (r.success) {
      void MessagePlugin.success('行业已删除')
      await load()
    } else {
      void MessagePlugin.error(r.message || '删除失败')
    }
  }

  return (
    <>
      <div style={{ marginBottom: 10, fontSize: 13, color: '#667' }}>
        {t('kb.title')} · 行业管理：行业以平台行业包承载，全站行业下拉（注册/租户/数据采集）动态拉取本字典。
        新建行业即时生效；停用后不再出现在注册选择中；被企业租户引用的行业不可删除（可停用替代）。
      </div>
      <Button theme="primary" onClick={() => { setCode(''); setName(''); setDlg({ mode: 'create' }) }}>＋ 新建行业</Button>
      <Table rowKey="id" size="small" data={rows}
             columns={[
               { colKey: 'id', title: 'ID', width: 50 },
               { colKey: 'code', title: '行业 code', width: 130, cell: ({ row }: any) => <code>{row.code}</code> },
               { colKey: 'name', title: '名称' },
               { colKey: 'entry_count', title: '语料条目', width: 90, cell: ({ row }: any) => row.entry_count || 0 },
               { colKey: 'enabled', title: '状态', width: 90, cell: ({ row }: any) => (
                 <Tag theme={row.enabled === 1 ? 'success' : 'default'}>{row.enabled === 1 ? '启用' : '停用'}</Tag>
               ) },
               { colKey: 'created_at', title: '创建时间', width: 100, cell: ({ row }: any) => shortTime(row.created_at) },
               { colKey: 'op', title: '操作', width: 260, cell: ({ row }: any) => (
                 <Space size={2}>
                   <Switch size="small" value={row.enabled === 1}
                           onChange={(v: boolean) => void doToggle(row, v ? 1 : 0)} />
                   <Button size="small" variant="text" onClick={() => { setName(row.name); setDlg({ mode: 'edit', row }) }}>编辑</Button>
                   <Popconfirm content="删除该行业将清除其下语料，确认？" onConfirm={() => doDelete(row)}>
                     <Button size="small" variant="text" theme="danger">删除</Button>
                   </Popconfirm>
                 </Space>
               ) },
             ] as never} />

      {/* 新建行业弹窗 */}
      <Dialog visible={!!dlg && dlg.mode === 'create'} onClose={() => setDlg(null)} header="新建行业" width={440} onConfirm={doCreate}>
        <div style={{ marginBottom: 12 }}>
          <div style={{ marginBottom: 4, fontSize: 13, color: '#556' }}>行业 code（全局唯一，仅小写字母/数字/下划线）</div>
          <Input value={code} onChange={(v) => setCode(String(v))} placeholder="auto / realestate / ..." />
        </div>
        <div>
          <div style={{ marginBottom: 4, fontSize: 13, color: '#556' }}>行业名称</div>
          <Input value={name} onChange={(v) => setName(String(v))} placeholder="汽车 / 房产装修 / ..." />
        </div>
      </Dialog>

      {/* 编辑行业名弹窗 */}
      <Dialog visible={!!dlg && dlg.mode === 'edit'} onClose={() => setDlg(null)} header="编辑行业" width={440} onConfirm={doUpdate}>
        <div>
          <div style={{ marginBottom: 4, fontSize: 13, color: '#556' }}>行业名称</div>
          <Input value={name} onChange={(v) => setName(String(v))} />
        </div>
      </Dialog>
    </>
  )
}
