import { useEffect, useRef } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { Button } from "./Button";

export interface DialogProps {
  open: boolean;
  title: ReactNode;
  children?: ReactNode;
  /** 危险操作：整框描边 #402323，确认按钮自动变红底 */
  danger?: boolean;
  confirmText?: ReactNode;
  cancelText?: ReactNode;
  onConfirm?: () => void;
  onCancel?: () => void;
  /** 点遮罩是否关闭，默认 true */
  dismissOnOverlay?: boolean;
}

/**
 * 对话框：440 宽 / 圆角 14 / 遮罩 72% 黑 / 顶缘高光。
 * Esc 与遮罩点击走 onCancel；确认不自动关——由调用方在 onConfirm 里控制。
 */
export function Dialog({
  open,
  title,
  children,
  danger = false,
  confirmText = "确定",
  cancelText = "取消",
  onConfirm,
  onCancel,
  dismissOnOverlay = true,
}: DialogProps) {
  const cancelRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel?.();
    };
    document.addEventListener("keydown", onKey);
    cancelRef.current?.focus();
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onCancel]);

  if (!open) return null;

  return createPortal(
    <div
      className="lc-overlay"
      onMouseDown={(e) => {
        if (dismissOnOverlay && e.target === e.currentTarget) onCancel?.();
      }}
    >
      <div className={`lc-dialog ${danger ?"lc-dialog--danger":""}`.trim()} role="alertdialog"aria-modal="true">
        <div className="lc-dialog__title">{title}</div>
        {children ? <div className="lc-dialog__body">{children}</div> : null}
        <div className="lc-dialog__actions">
          <button
            ref={cancelRef}
            type="button"
            className="lc-btn lc-btn--secondary"
            style={{ padding: "8px 16px", fontSize: 13 }}
            onClick={onCancel}
          >
            {cancelText}
          </button>
          <Button variant={danger ?"danger":"primary"} onClick={onConfirm}>
            {confirmText}
          </Button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
