import type { ReactNode } from "react";

export interface TableColumn<T> {
  key: string;
  title: ReactNode;
  /** 列宽，如 120、"20%"；不传自动 */
  width?: number | string;
  align?:"left"|"right"|"center";
  /** 等宽字体弱化样式（ID / 前缀 / 金额列用） */
  mono?: boolean;
  /** 弱化色（次要信息列） */
  dim?: boolean;
  /** 自定义渲染；不传直接显示 row[key] */
  render?: (row: T, index: number) => ReactNode;
}

export interface DataTableProps<T> {
  columns: TableColumn<T>[];
  rows: T[];
  rowKey: (row: T, index: number) => string;
  emptyText?: string;
  className?: string;
}

/**
 * 数据表（外部调用 / 反馈审批 / 系统与运维 11 屏提炼）：
 * 容器 1px card 边 + 表头 raised 底 + 行 1px 分隔，行高 46，hover 微亮。
 * 操作列的「停用/轮换/限额」用 <Link>、「删除」用 <Link tone="danger">。
 */
export function DataTable<T>({ columns, rows, rowKey, emptyText ="暂无数据", className =""}: DataTableProps<T>) {
  return (
    <div className={`lc-table-wrap ${className}`.trim()}>
      <table className="lc-table">
        <thead>
          <tr>
            {columns.map((col) => (
              <th
                key={col.key}
                style={{ width: col.width, textAlign: col.align ??"left"}}
              >
                {col.title}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 ? (
            <tr>
              <td colSpan={columns.length} style={{ display:"table-cell"}}>
                <div className="lc-table__empty">{emptyText}</div>
              </td>
            </tr>
          ) : (
            rows.map((row, i) => (
              <tr key={rowKey(row, i)}>
                {columns.map((col) => {
                  const cls = [col.mono ?"lc-table__mono":"", col.dim ?"td--dim":""]
                    .filter(Boolean)
                    .join(" ");
                  return (
                    <td key={col.key} className={cls || undefined} style={{ textAlign: col.align ??"left"}}>
                      {col.render ? col.render(row, i) : ((row as Record<string, ReactNode>)[col.key] ?? "—")}
                    </td>
                  );
                })}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}

export interface LinkProps {
  /** danger 红（删除）/ success 白（查看详情）；默认灰可点 */
  tone?:"default"|"danger"|"success";
  onClick?: () => void;
  /** 无障碍名：图标即唯一内容时必填（axe button-name），文字链接可省略 */
  "aria-label"?: string;
  children: ReactNode;
}

/** 表格操作列文字链接：默认 text-2 灰，hover 提亮 + 下划线 */
export function Link({ tone = "default", onClick, children, ...rest }: LinkProps) {
  const cls = ["lc-link", tone ==="danger"?"lc-link--danger":"", tone ==="success"?"lc-link--success":""]
    .filter(Boolean)
    .join(" ");
  return (
    <button type="button" className={cls} onClick={onClick} {...rest}>
      {children}
    </button>
  );
}
