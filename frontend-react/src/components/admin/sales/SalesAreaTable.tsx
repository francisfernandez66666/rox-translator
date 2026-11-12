// ============================================================================
// components/admin/sales/SalesAreaTable.tsx — 销售域「区域表 + 编辑弹窗」复用件（三期 W1）
//
// 从 AssistP.tsx 原样抽出（DOM 不变）：四类管理对象（知识库/话术/流程/功能入口）的
// 区域定义 AREAS、列表 Panel（AreaTable）与编辑 Dialog（AreaDialog）。
// 数据读写（assistAdminList/Create/Update/Delete）仍留在 AssistP：本件只做渲染与表单装配，
// 后续销售域面板（W3/W6）复用同一套表格/弹窗形态时不必再抄一份列渲染与字段分派。
//
// 口径：文案全走 t()；样式类名沿用 lc-as-*（由宿主面板 AssistP 的 ASSIST_CSS 提供，
// 本件不另铺一份样式，避免两份尺子）；不写内联 style、不写死色值（A01 闸射程）。
// ============================================================================
import { Button, DataTable, Dialog, Input, Link, Select, StatusPill, Switch, Textarea } from '@/ui/langcross/src'
import { useT } from '@/i18n'
import { Panel, Field } from '../parts'
import type { AssistArea, AssistRow } from '@/api/assistAdmin'

// 宽松记录别名（后端字段渐进增加，避免每加一列改一处类型）
type Any = Record<string, any>

/** 表单字段描述：kind 决定控件；options 仅 select 用（值 + 文案 i18n 键） */
export interface FormField {
  field: string
  labelKey: string
  kind: 'text' | 'textarea' | 'number' | 'select'
  options?: [string, string][]
  wide?: boolean
}

/** 区域定义：列展示 + 表单字段 + 空值默认（与 assist 侧建表默认值一致，避免提交漏字段） */
export interface AreaDef {
  area: AssistArea
  tabKey: string
  titleKey: string
  cols: { field: string; labelKey: string; width: number; wide?: boolean }[]
  fields: FormField[]
  defaults: AssistRow
}

/** 编辑弹窗的受控状态（AssistP 持有，本件只渲染与回传变更） */
export interface AreaFormState {
  def: AreaDef
  row: Any
}

/** AREAS 四类管理对象的唯一事实源（新增一类只改这里，表格与表单自动跟随） */
export const AREAS: AreaDef[] = [
  {
    area: 'kb', tabKey: 'kb', titleKey: 'assist.tabKb',
    cols: [
      { field: 'key', labelKey: 'assist.colKey', width: 140 },
      { field: 'category', labelKey: 'assist.fCategory', width: 100 },
      { field: 'title', labelKey: 'assist.fTitle', width: 180, wide: true },
      { field: 'keywords', labelKey: 'assist.fKeywords', width: 180, wide: true },
      { field: 'priority', labelKey: 'assist.fPriority', width: 80 },
    ],
    fields: [
      { field: 'category', labelKey: 'assist.fCategory', kind: 'text' },
      { field: 'title', labelKey: 'assist.fTitle', kind: 'text' },
      { field: 'content', labelKey: 'assist.fContent', kind: 'textarea', wide: true },
      { field: 'keywords', labelKey: 'assist.fKeywords', kind: 'text', wide: true },
      { field: 'link_keys', labelKey: 'assist.fLinkKeys', kind: 'text' },
      { field: 'priority', labelKey: 'assist.fPriority', kind: 'number' },
    ],
    defaults: { category: 'usage', title: '', content: '', keywords: '', link_keys: '', priority: 5, enabled: 1 },
  },
  {
    area: 'scripts', tabKey: 'scripts', titleKey: 'assist.tabScripts',
    cols: [
      { field: 'key', labelKey: 'assist.colKey', width: 140 },
      { field: 'stype', labelKey: 'assist.fStype', width: 100 },
      { field: 'title', labelKey: 'assist.fTitle', width: 160, wide: true },
      { field: 'content', labelKey: 'assist.fContent', width: 240, wide: true },
      { field: 'keywords', labelKey: 'assist.fKeywords', width: 160, wide: true },
    ],
    fields: [
      { field: 'stype', labelKey: 'assist.fStype', kind: 'select', options: [['keyword', 'assist.stKeyword'], ['greeting', 'assist.stGreeting'], ['fallback', 'assist.stFallback']] },
      { field: 'title', labelKey: 'assist.fTitle', kind: 'text' },
      { field: 'content', labelKey: 'assist.fContent', kind: 'textarea', wide: true },
      { field: 'keywords', labelKey: 'assist.fKeywords', kind: 'text', wide: true },
      { field: 'link_keys', labelKey: 'assist.fLinkKeys', kind: 'text' },
      { field: 'priority', labelKey: 'assist.fPriority', kind: 'number' },
    ],
    defaults: { stype: 'keyword', title: '', content: '', keywords: '', link_keys: '', priority: 5, enabled: 1 },
  },
  {
    area: 'flows', tabKey: 'flows', titleKey: 'assist.tabFlows',
    cols: [
      { field: 'key', labelKey: 'assist.colKey', width: 140 },
      { field: 'name', labelKey: 'assist.fName', width: 160, wide: true },
      { field: 'trigger_keywords', labelKey: 'assist.fTrigger', width: 200, wide: true },
      { field: 'steps_json', labelKey: 'assist.fSteps', width: 240, wide: true },
    ],
    fields: [
      { field: 'name', labelKey: 'assist.fName', kind: 'text' },
      { field: 'description', labelKey: 'assist.fDesc', kind: 'text', wide: true },
      { field: 'trigger_keywords', labelKey: 'assist.fTrigger', kind: 'text', wide: true },
      { field: 'steps_json', labelKey: 'assist.fSteps', kind: 'textarea', wide: true },
    ],
    defaults: { name: '', description: '', trigger_keywords: '', steps_json: '[]', enabled: 1 },
  },
  {
    area: 'features', tabKey: 'features', titleKey: 'assist.tabFeatures',
    cols: [
      { field: 'key', labelKey: 'assist.colKey', width: 140 },
      { field: 'name', labelKey: 'assist.fName', width: 160, wide: true },
      { field: 'url', labelKey: 'assist.fUrl', width: 220, wide: true },
      { field: 'ftype', labelKey: 'assist.fFtype', width: 100 },
      { field: 'sort', labelKey: 'assist.fSort', width: 80 },
    ],
    fields: [
      { field: 'name', labelKey: 'assist.fName', kind: 'text' },
      { field: 'description', labelKey: 'assist.fDesc', kind: 'text', wide: true },
      { field: 'url', labelKey: 'assist.fUrl', kind: 'text', wide: true },
      { field: 'ftype', labelKey: 'assist.fFtype', kind: 'select', options: [['route', 'assist.ftRoute'], ['link', 'assist.ftLink']] },
      { field: 'icon', labelKey: 'assist.fIcon', kind: 'text' },
      { field: 'sort', labelKey: 'assist.fSort', kind: 'number' },
    ],
    defaults: { name: '', description: '', url: '', ftype: 'route', icon: '', sort: 50, enabled: 1 },
  },
]

/** 区域列表：标题 + 新建入口 + 数据表（启停/编辑/删除都回传宿主处理，本件不发请求） */
export function AreaTable({ def, rows, onCreate, onEdit, onToggle, onRemove }: {
  def: AreaDef
  rows: AssistRow[]
  onCreate: () => void
  onEdit: (row: Any) => void
  onToggle: (row: AssistRow, on: boolean) => void
  onRemove: (row: AssistRow) => void
}) {
  const [, t] = useT()
  return (
    <Panel title={t(def.titleKey)} extra={
      <Button size="sm" variant="primary" onClick={onCreate}>
        {t('assist.create')}
      </Button>
    }>
      <DataTable
        rowKey={(r) => String((r as Any).id)} rows={rows} emptyText={t('assist.noRows')}
        columns={[
          ...def.cols.map((c) => ({
            key: c.field, title: t(c.labelKey), width: c.width,
            render: (r: Any) => (c.wide ? String(r[c.field] ?? '') : <code>{String(r[c.field] ?? '')}</code>),
          })),
          {
            key: 'enabled', title: t('common.status'), width: 130, render: (r: Any) => (
              <div className="lc-as-cell">
                <Switch checked={Number(r.enabled) === 1} onChange={(e) => onToggle(r as AssistRow, e.target.checked)} />
                <StatusPill tone={Number(r.enabled) === 1 ? 'success' : 'idle'}>
                  {Number(r.enabled) === 1 ? t('assist.enabled') : t('ind.off')}
                </StatusPill>
              </div>
            ),
          },
          {
            key: 'op', title: '', width: 150, render: (r: Any) => (
              <div className="lc-as-ops">
                <Link onClick={() => onEdit(r)}>{t('coupons.edit')}</Link>
                <Link tone="danger" onClick={() => onRemove(r as AssistRow)}>{t('common.delete')}</Link>
              </div>
            ),
          },
        ]}
      />
    </Panel>
  )
}

/** 区域编辑弹窗：key 编辑态禁用（assist 侧按 key 命中，改 key 等于换条目）；字段按 kind 分派控件 */
export function AreaDialog({ form, onChange, onSave, onCancel }: {
  form: AreaFormState
  onChange: (next: AreaFormState) => void
  onSave: () => void
  onCancel: () => void
}) {
  const [, t] = useT()
  return (
    <Dialog
      open onCancel={onCancel} onConfirm={onSave}
      title={`${Number(form.row.id) > 0 ? t('assist.editTitle') : t('assist.create')}${t(form.def.titleKey)}`}
      confirmText={t('common.save')} cancelText={t('common.cancel')}
    >
      <Field label={t('assist.colKey')}>
        {/* key 是 assist 侧的唯一定位标识（话术/知识命中都按它），编辑时不允许改，避免断引用 */}
        <Input className="lc-as-w240" value={String(form.row.key ?? '')} aria-label={t('assist.colKey')}
          disabled={Number(form.row.id) > 0} onChange={(e) => onChange({ ...form, row: { ...form.row, key: e.target.value } })} />
      </Field>
      {form.def.fields.map((f) => (
        <Field key={f.field} label={t(f.labelKey)}>
          {f.kind === 'textarea' && (
            <Textarea className="lc-as-ta-md" aria-label={t(f.labelKey)}
              value={String(form.row[f.field] ?? '')} onChange={(e) => onChange({ ...form, row: { ...form.row, [f.field]: e.target.value } })} />
          )}
          {f.kind === 'number' && (
            <Input className="lc-as-w140" type="number" min={0} aria-label={t(f.labelKey)}
              value={String(form.row[f.field] ?? '')} onChange={(e) => onChange({ ...form, row: { ...form.row, [f.field]: e.target.value } })} />
          )}
          {f.kind === 'select' && (() => {
            const cur = String(form.row[f.field] ?? f.options?.[0]?.[0] ?? '')
            const labelKey = (f.options || []).find(([v]) => v === cur)?.[1]
            return (
              <Select className="lc-as-sel" aria-label={t(f.labelKey)} value={cur}
                display={labelKey ? t(labelKey) : ''}
                onChange={(e) => onChange({ ...form, row: { ...form.row, [f.field]: e.target.value } })}>
                {(f.options || []).map(([v, k]) => <option key={v} value={v}>{t(k)}</option>)}
              </Select>
            )
          })()}
          {f.kind === 'text' && (
            <Input className={f.wide ? 'lc-as-wide' : 'lc-as-w300'} aria-label={t(f.labelKey)}
              value={String(form.row[f.field] ?? '')} onChange={(e) => onChange({ ...form, row: { ...form.row, [f.field]: e.target.value } })} />
          )}
        </Field>
      ))}
      <Field label={t('assist.enabled')}>
        <Switch checked={Number(form.row.enabled ?? 1) === 1}
          onChange={(e) => onChange({ ...form, row: { ...form.row, enabled: e.target.checked ? 1 : 0 } })} />
      </Field>
    </Dialog>
  )
}