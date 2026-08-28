// ============================================================================
// main.tsx — 应用入口
// 职责：创建 React 18 根节点，引入全局样式，渲染 <App />。
// ============================================================================
import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
// 项目主题覆盖样式
import './styles/theme.css'

// TDesign 全量样式（与 Vue 版视觉基线对齐；细节覆盖见 theme.css）
import 'tdesign-react/es/style/index.css'

// ============ 本文件职责中文说明 ============
// 应用入口：创建 React 根节点并引入全局样式。
// ========================================

// 挂载应用到 DOM 节点，并开启严格模式
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
