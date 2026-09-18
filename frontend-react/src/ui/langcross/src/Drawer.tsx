import { useEffect } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { CloseIcon } from "./icons";

export interface DrawerProps {
  open: boolean;
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
}

/** 抽屉：宽 480 自右滑入，遮罩 72% 黑，Esc 关闭 */
export function Drawer({ open, title, onClose, children, footer }: DrawerProps) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return createPortal(
    <div
      className="lc-drawer-viewport"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <aside className="lc-drawer"role="dialog"aria-modal="true">
        <div className="lc-drawer__head">
          <span className="lc-drawer__title">{title}</span>
          <button
            type="button"
            aria-label="关闭"
            onClick={onClose}
            style={{
              display: "grid",
              placeItems: "center",
              width: 24,
              height: 24,
              border: 0,
              borderRadius: "var(--lc-r-bar)",
              background: "transparent",
              color: "var(--lc-text-3)",
              cursor: "pointer",
            }}
          >
            <CloseIcon size={12} />
          </button>
        </div>
        <div style={{ borderTop:"1px solid var(--lc-border-faint)"}} />
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>{children}</div>
        {footer}
      </aside>
    </div>,
    document.body,
  );
}
