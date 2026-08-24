<!-- ============================================================================
   components/admin/PlansPanel.vue — 套餐详情（合并面板，四期体验增强）
   职责：商业包管理 + 订阅收银合一入口
   - 超管：可见全部设置与包 CRUD（Packages.vue 全功能）
   - 非超管：仅套餐/订阅查看与订阅操作（Billing.vue 收银台）
   ============================================================================ -->

<template>
  <div>
    <!-- ★ 商业包管理卡片仅超管渲染（非超管无商业套餐设置权限） -->
    <Packages v-if="isSuper" />
    <Billing />
  </div>
</template>

<script setup lang="ts">
import Packages from './Packages.vue'
import Billing from './Billing.vue'
import { isSuper } from './store'
</script>
