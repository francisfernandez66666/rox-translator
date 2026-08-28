// ============================================================================
// components/admin/parts.tsx — 后台面板公共小件
// 被 panels_a~d 共享：Panel 容器、Field 字段行、toastResp 结果提示、num 数字格式化。
// ============================================================================
import type { ReactNode } from 'react'
import { MessagePlugin } from 'tdesign-react'

// ============ 本文件职责中文说明 ============
// 后台面板公共小部件：Panel 容器、Field 字段行、toastResp 提示、num 数字格式化助手。
// ========================================

/** 面板容器卡片 */
export function Panel({ title, extra, children }: { title: string; extra?: ReactNode; children: ReactNode }) {
  return (
    <div className="panel-card" style={{ marginBottom: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
        <h2 style={{ fontSize: 17, margin: 0 }}>{title}</h2>
        {extra}
      </div>
      {children}
    </div>
  )
}

/** 统一错误提示：非 success 响应 toast（修复 Vue 版各面板各自为战） */
export function toastResp(r: { success?: boolean; message?: string }, okMsg?: string): boolean {
  if (r.success) {
    if (okMsg) void MessagePlugin.success(okMsg)
    return true
  }
  void MessagePlugin.error(r.message || '操作失败')
  return false
}

/** 字段行：label + 控件 */
export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
      <span style={{ minWidth: 130, fontSize: 13, color: '#556' }}>{label}</span>
      <div style={{ flex: 1 }}>{children}</div>
    </div>
  )
}

/** num 数字输入框取值助手：TDesign.Input.value 仅接受 string */
export const num = (v: unknown): string => String(Number(v ?? 0))
