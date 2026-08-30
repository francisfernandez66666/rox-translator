// ============================================================================
// env.d.ts — Vite 环境变量类型声明
// 职责：扩展 ImportMetaEnv，为 import.meta.env 提供 VITE_API_BASE 等变量类型。
// ============================================================================

/**
 * env.d.ts · 职责说明
 * TypeScript 类型声明文件，用于：
 * - 扩展 Vite 的 ImportMetaEnv 接口
 * - 为 import.meta.env 提供类型提示
 * - 声明项目中使用的环境变量类型
 */

/// <reference types="vite/client" />

// Vite 注入的环境变量类型
interface ImportMetaEnv {
  // 后端 API 基础地址（可选，例如 https://api.example.com）
  readonly VITE_API_BASE?: string
}

// 扩展 ImportMeta，使 import.meta.env 拥有类型提示
interface ImportMeta { readonly env: ImportMetaEnv }
