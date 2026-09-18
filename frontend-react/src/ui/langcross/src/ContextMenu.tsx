import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";

export interface MenuItem {
  key: string;
  label: ReactNode;
  /** 危险项：红字；若传 willBeLast，渲染时其上方自动加 1px 分隔 */
  danger?: boolean;
  onSelect?: () => void;
}

export interface ContextMenuProps {
  open: boolean;
  /** 锚点坐标（一般是右键 / 点击位置） */
  x: number;
  y: number;
  items: MenuItem[];
  onClose: () => void;
}

/**
 * 上下文菜单：宽 220；最后一项若为 danger（删除），
 * 上方自动加 1px 分隔——这是规范，不靠调用方手塞 divider。
 */
export function ContextMenu({ open, x, y, items, onClose }: ContextMenuProps) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ left: x, top: y });

  useLayoutEffect(() => {
    if (!open || !ref.current) return;
    const rect = ref.current.getBoundingClientRect();
    const left = Math.min(x, window.innerWidth - rect.width - 8);
    const top = Math.min(y, window.innerHeight - rect.height - 8);
    setPos({ left: Math.max(8, left), top: Math.max(8, top) });
  }, [open, x, y]);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, onClose]);

  if (!open) return null;

  const last = items.length - 1;

  return createPortal(
    <div ref={ref} className="lc-menu"role="menu"style={{ left: pos.left, top: pos.top }}>
      {items.map((item, i) => (
        <div key={item.key} style={{ display:"contents"}}>
          {item.danger && i === last ? <div className="lc-menu__divider"/> : null}
          <button
            type="button"
            role="menuitem"
            className={`lc-menu__item ${item.danger ?"lc-menu__item--danger":""}`.trim()}
            onClick={() => {
              item.onSelect?.();
              onClose();
            }}
          >
            {item.label}
          </button>
        </div>
      ))}
    </div>,
    document.body,
  );
}
