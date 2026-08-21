<!-- ============================================================================
   components/Bell.vue — 站内信铃铛（通知中心入口）
   职责：未读红点轮询（30s）+ 下拉列表 + 单条/全部已读
   ============================================================================ -->
<template>
  <div class="bell-wrap" style="position:relative">
    <button class="clear-btn" :title="t('bell.title')" @click="open = !open; open && load()">
      🔔<span v-if="unread > 0" class="bell-badge">{{ unread > 99 ? '99+' : unread }}</span>
    </button>
    <div v-if="open" class="bell-drop">
      <div class="ad-row" style="justify-content:space-between;padding:6px 10px;border-bottom:1px solid #eee">
        <b>{{ t('bell.title') }}</b>
        <button class="ad-btn-sm" @click="readAll">{{ t('bell.readAll') }}</button>
      </div>
      <div class="bell-list">
        <div v-for="n in list" :key="n.id" :class="['bell-item', n.read_at ? '' : 'unread']" @click="readOne(n)">
          <b style="font-size:13px">{{ n.title }}</b>
          <div class="ad-hint" style="margin:2px 0 0">{{ n.body }}</div>
          <div class="ad-hint" style="font-size:11px">{{ fmtTime(n.created_at) }}</div>
        </div>
        <div v-if="!list.length" class="ad-empty" style="padding:16px">{{ t('bell.empty') }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { notifications, notificationsUnread, notificationRead, notificationsReadAll } from '@/api'
import { t } from '@/i18n'
import { fmtTime } from './admin/ui'

const open = ref(false)
const unread = ref(0)
const list = ref<any[]>([])
let timer: number | undefined

// 拉取未读数（轮询）
async function poll() {
  try {
    const r = await notificationsUnread()
    if (r.success) unread.value = (r as any).unread || 0
  } catch {}
}

// 拉取列表
async function load() {
  const r = await notifications()
  if (r.success) list.value = (r as any).notifications || []
}

// 单条已读
async function readOne(n: any) {
  if (!n.read_at) {
    await notificationRead(n.id)
    n.read_at = new Date().toISOString()
    unread.value = Math.max(0, unread.value - 1)
  }
}

// 全部已读
async function readAll() {
  await notificationsReadAll()
  await Promise.all([load(), poll()])
}

onMounted(() => { poll(); timer = window.setInterval(poll, 30000) })
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<style scoped>
.bell-badge {
  position: absolute; top: -4px; right: -6px;
  background: #e74c3c; color: #fff; border-radius: 8px;
  font-size: 10px; padding: 0 4px; line-height: 14px;
}
.bell-drop {
  position: absolute; right: 0; top: 34px; width: 320px; max-height: 420px;
  background: #fff; border: 1px solid #e3e3e3; border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0,0,0,.12); z-index: 50; overflow: hidden;
}
.bell-list { max-height: 360px; overflow: auto; }
.bell-item { padding: 8px 12px; cursor: pointer; border-bottom: 1px solid #f2f2f2; }
.bell-item:hover { background: #f7f9fc; }
.bell-item.unread { background: #fff8e6; }
</style>