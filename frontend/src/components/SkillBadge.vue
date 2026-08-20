<!-- ============================================================================
   components/SkillBadge.vue — 技能徽章组件
   职责：在 AI 回复气泡顶部显示当前技能标识（目前固定展示"🌐 翻译"）
   ============================================================================ -->
<template>
  <!-- 技能徽章：翻译 -->
  <span class="skill-badge">🌐 {{ t('chat.skillLabel') }}</span>
</template>

<script setup lang="ts">
import { t } from '@/i18n'
</script>

<style scoped>
/* 技能徽章样式：绿色圆角小标签 */
.skill-badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 6px;
  font-size: 11px;
  background: rgba(46, 125, 50, 0.12);
  color: #2e7d32;
  font-weight: 500;
}
</style>
