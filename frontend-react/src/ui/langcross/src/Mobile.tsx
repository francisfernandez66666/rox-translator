import type { ButtonHTMLAttributes, ReactNode } from "react";

/* ============================================================
 * 移动端组件（画布「移动端 UI · 390」页提炼）
 * 结构：lc-mob-screen = 状态栏(44) + 导航顶栏(52) + 内容栈(16 边距/gap 12)
 *      + 底部浮动 Tab Bar 胶囊(62 高 / r36 / 活跃项实心白底黑字)
 * ============================================================ */

/** MobScreen 骨架插槽：topbar/tabBar 任一可缺省（登录屏无导航、后台屏无 TabBar），children 即内容栈 */
export interface MobScreenProps {
  /** 顶部导航（MobTopBar）；登录等无导航屏可不传 */
  topbar?: ReactNode;
  /** 底部浮动 TabBar（TabBar 组件）；后台类屏不传 */
  tabBar?: ReactNode;
  className?: string;
  children?: ReactNode;
}

/** 移动端整屏骨架：状态栏 + 顶栏 + 内容栈 + Tab Bar */
export function MobScreen({ topbar, tabBar, className = "", children }: MobScreenProps) {
  return (
    <div className={`lc-mob-screen ${className}`.trim()}>
      <StatusBar />
      {topbar}
      <div className="lc-mob-body">{children}</div>
      {tabBar}
    </div>
  );
}

/** 系统状态栏：9:41 + 电量手势区（示意） */
export function StatusBar({ time ="9:41", className =""}: { time?: string; className?: string }) {
  return (
    <div className={`lc-statusbar ${className}`.trim()}>
      <span>{time}</span>
      <span className="lc-statusbar__glyph"aria-hidden>
        ▂▄▆
      </span>
    </div>
  );
}

// 移动端顶栏入参：标题与左右动作（高 52）
export interface MobTopBarProps {
  title: string;
  /** 左侧动作（默认 ☰ 汉堡）；传 null 不渲染 */
  leading?: ReactNode;
  onLeading?: () => void;
  /** 右侧动作；默认渲染通知圆点占位，传 null 不渲染 */
  trailing?: ReactNode;
  className?: string;
}

/** 移动端导航顶栏：汉堡/返回 + 标题 + 右侧动作 */
export function MobTopBar({ title, leading, onLeading, trailing, className =""}: MobTopBarProps) {
  return (
    <header className={`lc-mob-topbar ${className}`.trim()}>
      {leading === null ? (
        <span />
      ) : typeof leading ==="string"|| leading == null ? (
        <button type="button"className="lc-mob-topbar__icon"onClick={onLeading} aria-label="菜单">
 {leading ?? ""}
        </button>
      ) : (
        leading
      )}
      <span className="lc-mob-topbar__title">{title}</span>
      {trailing === null ? (
        <span />
      ) : trailing != null ? (
        trailing
      ) : (
        <span className="lc-mob-topbar__dot"aria-hidden />
      )}
    </header>
  );
}

// 底部标签项：图标 + 文案 + 路由
export interface MobTabItem {
  key: string;
  label: string;
  /** 图标位：emoji 或自绘 icon */
  icon?: ReactNode;
}

// 移动端底部 TabBar 入参：标签项 + 当前路径（高 62，后台页不用）
export interface TabBarProps {
  items: MobTabItem[];
  activeKey: string;
  onChange?: (key: string) => void;
  className?: string;
}

/** 底部浮动 Tab Bar：inset 胶囊 + 活跃项实心白底黑字（X/Grok 单色） */
export function TabBar({ items, activeKey, onChange, className =""}: TabBarProps) {
  return (
    <nav className={`lc-tabbar ${className}`.trim()} aria-label="主导航">
      {items.map((it) => (
        <button
          key={it.key}
          type="button"
          className={`lc-tabbar__item${it.key === activeKey ? " lc-tabbar__item--active":""}`}
          onClick={() => onChange?.(it.key)}
        >
          {it.icon != null ? (
            <span className="lc-tabbar__icon"aria-hidden>
              {it.icon}
            </span>
          ) : null}
          {it.label}
        </button>
      ))}
    </nav>
  );
}

// 列表卡左侧状态点档位（none 即不显点）
export type DotTone ="success"|"warn"|"danger"|"none";

// 移动端列表卡入参：标题/副文案/右侧值与状态点
export interface ListCardProps {
  /** 左侧状态点颜色；不传无点 */
  dot?: DotTone;
  title: ReactNode;
  /** 弱化标题（如已恢复项） */
  titleDim?: boolean;
  /** Inter 11 号弱说明，如「工单 #2417 · 张工 · 10:24」 */
  meta?: ReactNode;
  /** 12 号正文摘要 */
  body?: ReactNode;
  /** 右侧 › 箭头（行卡）；设置后整卡横排 */
  chevron?: boolean;
  /** 底部操作区（按钮组，自动均分） */
  actions?: ReactNode;
  onClick?: () => void;
  className?: string;
  children?: ReactNode;
}

// 状态点档位 → 交付真值色映射（单一事实源，避免各页写死）
const DOT_COLOR: Record<Exclude<DotTone, "none">, string> = {
  success: "var(--lc-success)",
  warn: "var(--lc-warn)",
  danger: "var(--lc-red)",
};

/** 移动端列表卡：面板底 + 状态点/标题/meta/摘要 + 操作区 */
export function ListCard({
  dot = "none",
  title,
  titleDim = false,
  meta,
  body,
  chevron = false,
  actions,
  onClick,
  className = "",
  children,
}: ListCardProps) {
  const Tag = onClick && !actions ?"button":"div";
  return (
    <Tag
      type={Tag ==="button"?"button": undefined}
      className={`lc-mcard${chevron ? " lc-mcard--row":""} ${className}`.trim()}
      onClick={onClick}
    >
      <div className="lc-mcard__info">
        <div className="lc-mcard__head">
          {dot !=="none"? (
            <span className="lc-mcard__dot"style={{ background: DOT_COLOR[dot] }} aria-hidden />
          ) : null}
          <span className={`lc-mcard__title${titleDim ? " lc-mcard__title--dim":""}`}>{title}</span>
        </div>
        {meta != null ? <span className="lc-mcard__meta">{meta}</span> : null}
        {body != null ? <span className="lc-mcard__body">{body}</span> : null}
        {children}
      </div>
      {chevron ? (
        <span className="lc-mcard__chevron"aria-hidden>
          ›
        </span>
      ) : null}
      {actions != null ? <div className="lc-mcard__actions">{actions}</div> : null}
    </Tag>
  );
}

// 移动端统计块入参：数值 + 标签 + 语气档
export interface MStatProps {
  label: string;
  value: ReactNode;
  /** 涨跌说明；tone 决定颜色（涨白 / 琥珀提示） */
  delta?: ReactNode;
  deltaTone?:"up"|"warn";
  className?: string;
}

/** 移动端指标卡：与 MStatGrid 配合成 2×2 */
export function MStat({ label, value, delta, deltaTone ="up", className =""}: MStatProps) {
  return (
    <div className={`lc-mstat ${className}`.trim()}>
      <span className="lc-mstat__label">{label}</span>
      <span className="lc-mstat__value">{value}</span>
      {delta != null ? (
        <span className={`lc-mstat__delta lc-mstat__delta--${deltaTone}`}>{delta}</span>
      ) : null}
    </div>
  );
}

/** 指标网格：桌面 4 列 → 平板/移动 2×2（≤620 仍 2 列，值缩到 19px 由 demo 控制） */
export function MStatGrid({ children, className =""}: { children: ReactNode; className?: string }) {
  return <div className={`lc-mstat-grid ${className}`.trim()}>{children}</div>;
}

/** 移动端搜索框（占位文案样式，非真输入） */
export function MSearch({ placeholder, className =""}: { placeholder: string; className?: string }) {
  return <div className={`lc-msearch ${className}`.trim()}>{placeholder}</div>;
}

/** 分组小标题（如「最新动态」） */
export function MSection({ children, className =""}: { children: ReactNode; className?: string }) {
  return <div className={`lc-msection ${className}`.trim()}>{children}</div>;
}

/** 移动端主操作按钮（白底黑字，占满一行） */
export function MobButton({
  variant = "primary",
  className = "",
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?:"primary"|"outline"|"danger"}) {
  const v = variant ==="primary"? "lc-btn--primary": variant ==="danger"? "lc-btn--danger":"lc-btn--secondary";
  return <button type="button"className={`lc-btn lc-btn--block${v} ${className}`.trim()} {...rest} />;
}
