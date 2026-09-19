import { useState, type ReactNode } from "react";
import { Icon } from "./Icon";

export interface NavItem {
  key: string;
  label: string;
  /** 16px 视觉宽度的图标位：可传 emoji 字符串或自绘 icon */
  icon?: ReactNode;
}

export interface AdminShellProps {
  /** 侧栏菜单（管理后台 11 屏提炼：9 项 + EN 语言钮） */
  nav: NavItem[];
  activeKey: string;
  onNavigate?: (key: string) => void;
  /** 左上角产品名，默认「能言管理后台」 */
  appName?: string;
  appIcon?: ReactNode;
  /** 顶栏右侧集群：铃铛 / 角色胶囊 / 租户胶囊 / 账号胶囊 */
  topbar?: ReactNode;
  /** ★ #23：侧栏底部插槽（真实用法=LangSelect 语言下拉）。
   *  旧版这里硬写一个无 onClick 的「EN」装饰按钮属假交互，已换成受控插槽；不传则底部留空。 */
  sideFoot?: ReactNode;
  /** 右下角悬浮 AI 按钮（不传则不渲染） */
  fab?: ReactNode;
  /** 内容区是否收窄内边距（默认 40/24） */
  flush?: boolean;
  className?: string;
  children?: ReactNode;
}

export interface AdminTopBarProps {
  onBell?: () => void;
  /** 角色名，如「平台管理员」；不传不渲染 */
  role?: string;
  /** 租户名，如「平台根（全局）」；不传不渲染 */
  tenant?: string;
  /** 账号名，如「admin」；不传不渲染 */
  account?: string;
  className?: string;
}

/**
 * 后台顶栏右侧集群：🔔 + 角色 / 租户 / 账号三个胶囊。
 * 胶囊统一 30 高、raised 底、input 档描边，hover 升到 done 档。
 */
export function AdminTopBar({ onBell, role, tenant, account, className =""}: AdminTopBarProps) {
  return (
    <div className={`lc-shell-bar ${className}`.trim()}>
      <button type="button" className="lc-shell-bell" aria-label="通知" onClick={onBell}>
        <Icon n="bell" />
      </button>
      {role ? (
        <button type="button"className="lc-shell-pill lc-shell-pill--muted">
          {role}
        </button>
      ) : null}
      {tenant ? (
        <button type="button"className="lc-shell-pill">
          {tenant} <span aria-hidden>▾</span>
        </button>
      ) : null}
      {account ? (
        <button type="button"className="lc-shell-pill">
 {account} <span aria-hidden>▾</span>
        </button>
      ) : null}
    </div>
  );
}

/**
 * 管理后台 Shell：侧栏 238（#0A0B0D）+ 顶栏 + 内容区（#000）。
 * 活跃菜单 = raised 底 + 白字——与画布 11 屏一一对应。
 */
export function AdminShell({
  nav,
  activeKey,
  onNavigate,
  appName = "能言管理后台",
 appIcon = "",
  topbar,
  sideFoot,
  fab,
  flush = false,
  className = "",
  children,
}: AdminShellProps) {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const closeDrawer = () => setDrawerOpen(false);
  return (
    <div className={`lc-shell ${className}`.trim()}>
      <aside className={`lc-sidebar${drawerOpen ? " lc-sidebar--open":""}`}>
        <div className="lc-sidebar__logo">
          {appIcon ? <span aria-hidden>{appIcon}</span> : null}
          {appName}
        </div>
        <nav className="lc-sidebar__nav">
          {nav.map((item) => (
            <button
              key={item.key}
              type="button"
              className={`lc-side-item${item.key === activeKey ? " lc-side-item--active":""}`}
              onClick={() => {
                closeDrawer();
                onNavigate?.(item.key);
              }}
            >
              {item.icon != null ? <span className="lc-side-item__icon">{item.icon}</span> : null}
              {item.label}
            </button>
          ))}
        </nav>
        <div className="lc-sidebar__foot">
          {sideFoot ?? null}
        </div>
      </aside>
      {drawerOpen ? <div className="lc-shell-scrim"onClick={closeDrawer} aria-hidden /> : null}
      <div className="lc-shell__main">
        <div className="lc-shell-toprow">
          <button
            type="button"
            className="lc-shell-burger"
            aria-label="打开菜单"
            onClick={() => setDrawerOpen(true)}
          >
            <Icon n="menu" />
          </button>
          {topbar}
        </div>
        <main className={`lc-content${flush ? " lc-content--flush":""}`}>{children}</main>
      </div>
      {fab}
    </div>
  );
}

/** 悬浮 AI 按钮：右下 52 圆，inset 底 + card 边 + 顶缘高光 */
export function Fab({ onClick, label ="AI", icon =""}: { onClick?: () => void; label?: string; icon?: ReactNode }) {
  return (
    <button type="button"className="lc-fab"aria-label={label} onClick={onClick}>
      {icon}
    </button>
  );
}

/** 版权条：最深内嵌底（#050607），放在内容区末尾 */
export function FooterBar({ text }: { text: ReactNode }) {
  return <div className="lc-footer-bar">© {text}</div>;
}
