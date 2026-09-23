// 标签页单项：键、文案、可选角标
export interface TabItem {
  key: string;
  label: string;
}

// 标签页入参：项列表 + 当前值 + 切换回调
export interface TabsProps {
  items: TabItem[];
  activeKey: string;
  onChange?: (key: string) => void;
  className?: string;
}

/**
 * 页签（系统看板/用量看板、套餐与订单三页签提炼）：
 * 文字 13px，活跃项白字 + 2px 白色下划线，容器 1px 分隔底线。
 */
export function Tabs({ items, activeKey, onChange, className =""}: TabsProps) {
  return (
    <div className={`lc-tabs ${className}`.trim()} role="tablist">
      {items.map((item) => (
        <button
          key={item.key}
          type="button"
          role="tab"
          aria-selected={item.key === activeKey}
          className={`lc-tab${item.key === activeKey ? " lc-tab--active":""}`}
          onClick={() => onChange?.(item.key)}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
