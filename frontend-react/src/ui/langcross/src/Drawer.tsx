// ============ Drawer.tsx · 职责说明 ============
// langcross 设计系统的右侧抽屉基件（工单详情、Webhook 配置、App 级侧栏共用）。
// 与 Dialog 的分工必须保持：抽屉=可长时间停留的详情/表单容器（role="dialog"），
// 对话框=需要用户当场决策（role="alertdialog"）——两者 role 不同，e2e 与读屏都按
// role 区分锚点，混用会让 getByRole('alertdialog') 之类的定位器指错元素。
// 关闭语义只有 onClose 一条（Esc、遮罩点击、右上角关闭按钮都走它），嵌套确认框时
// 焦点陷阱按栈顶让位，禁止再加第二套关闭路径。
// =============================================
import { useEffect, useRef } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { CloseIcon } from "./icons";
import { useFocusTrap } from "./focusTrap";

export interface DrawerProps {
  open: boolean;
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
}

/**
 * 抽屉：宽 480 自右滑入，遮罩 72% 黑，Esc 关闭。
 * ★ a11y（#43）：Tab 焦点由 useFocusTrap 收在抽屉内循环（含嵌套确认框时按栈顶让位）。
 */
export function Drawer({ open, title, onClose, children, footer }: DrawerProps) {
  const boxRef = useRef<HTMLElement>(null);
  useFocusTrap(boxRef, open);
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
      <aside ref={boxRef} className="lc-drawer" role="dialog" aria-modal="true">
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
