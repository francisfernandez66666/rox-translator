import type { ReactNode } from "react";

export interface PageHeaderProps {
  title: ReactNode;
  /** 副描述（13px 灰）；不传不渲染 */
  desc?: ReactNode;
  /** 右侧动作区：主按钮只放一个，其余 secondary */
  actions?: ReactNode;
  className?: string;
}

/** 页头：20/600 标题 + 13 灰描述 + 右侧动作；后台每屏第一行 */
export function PageHeader({ title, desc, actions, className =""}: PageHeaderProps) {
  return (
    <header className={`lc-page-head ${className}`.trim()}>
      <div>
        <h1 className="lc-page-head__title">{title}</h1>
        {desc ? <p className="lc-page-head__desc">{desc}</p> : null}
      </div>
      {actions ? <div className="lc-page-head__actions">{actions}</div> : null}
    </header>
  );
}

export interface ToolbarProps {
  /** 筛选输入 / 下拉 / 按钮混排；计数文案自动靠右 */
  count?: ReactNode;
  className?: string;
  children?: ReactNode;
}

/** 工具栏：筛选行（输入 32 高 / 胶囊下拉 / 小按钮），gap 8 可换行 */
export function Toolbar({ count, className = "", children }: ToolbarProps) {
  return (
    <div className={`lc-toolbar ${className}`.trim()}>
      {children}
      {count != null ? <span className="lc-toolbar__count">{count}</span> : null}
    </div>
  );
}

export interface ToolbarInputProps {
  value?: string;
  placeholder?: string;
  onChange?: (value: string) => void;
  width?: number | string;
}

/** 工具栏小输入框：32 高，inset 底 + pill 边 */
export function ToolbarInput({ value, placeholder, onChange, width }: ToolbarInputProps) {
  return (
    <input
      className="lc-toolbar__input"
      style={width != null ? { width } : undefined}
      value={value}
      placeholder={placeholder}
      onChange={(e) => onChange?.(e.target.value)}
    />
  );
}
