/// <reference types="vite/client" />
// ============ 本文件职责中文说明 ============
// TS 环境声明与全局类型文件：声明 Vite 客户端类型及 import.meta.env 内的 VITE_API_BASE 变量。
// ========================================
interface ImportMetaEnv { readonly VITE_API_BASE?: string }
interface ImportMeta { readonly env: ImportMetaEnv }
