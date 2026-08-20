// ============================================================================
// main.ts — Vue 应用入口
// ============================================================================
// 【作用】创建 Vue 应用实例，挂载 Pinia 状态管理和路由
// 这是整个前端最先执行的文件

// Vue 框架核心（创建应用实例）
import { createApp } from 'vue'
// Pinia 状态管理（创建 store 实例）
import { createPinia } from 'pinia'
// 根组件
import App from './App.vue'

// 创建 Vue 应用
const app = createApp(App)

// 挂载 Pinia 状态管理（全局状态，所有组件共享）
app.use(createPinia())

// 挂载到 HTML 的 #app 元素
app.mount('#app')
