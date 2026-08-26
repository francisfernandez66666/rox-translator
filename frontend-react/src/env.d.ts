// ============================================================================
// env.d.ts — Vite 环境变量类型声明
// 职责：扩展 ImportMetaEnv，为 import.meta.env 提供 VITE_API_BASE 等变量类型。
// ============================================================================

/// <reference types="vite/client" />

// Vite 注入的环境变量类型
interface ImportMetaEnv {
  // 后端 API 基础地址（可选，例如 https://api.example.com）
  readonly VITE_API_BASE?: string
}

// 扩展 ImportMeta，使 import.meta.env 拥有类型提示
interface ImportMeta { readonly env: ImportMetaEnv }
