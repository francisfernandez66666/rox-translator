<!-- ============================================================================
   components/admin/Referral.vue — 邀请裂变面板（白皮书 §五）
   职责：个人邀请码/专属链接展示与复制、二维码预览与下载、邀请记录与奖励统计
   规则：多邀多得（不同被邀人叠加）、单邀单个（同对每种奖励仅一次）
   ============================================================================ -->
<template>
  <section class="ad-section">
    <h2>{{ t('referral.title') }}</h2>
    <div class="ad-hint">{{ t('referral.hint') }}</div>

    <!-- 邀请链接 + 二维码 -->
    <div class="ref-top">
      <div class="ref-link-box">
        <div class="ad-hint">{{ t('referral.myCode') }}</div>
        <div class="ref-code">{{ refCode || '—' }}</div>
        <div class="ad-hint" style="margin-top:8px">{{ t('referral.linkLabel') }}</div>
        <div class="ad-row">
          <input :value="inviteUrl" readonly class="ad-input" style="flex:1" @focus="($event.target as HTMLInputElement).select()" />
          <button class="ad-btn" @click="copyLink">📋 {{ t('referral.copy') }}</button>
          <a class="ad-btn ad-btn-green" :href="qrDownloadUrl" :download="`invite-${refCode}.png`">⬇️ {{ t('referral.downloadQr') }}</a>
        </div>
        <div class="ref-stats">
          <span>👥 {{ t('referral.invitedCount') }}：<b>{{ invited }}</b></span>
          <span>🎁 {{ t('referral.trialRewards') }}：<b>{{ trialTokens }}</b> token / {{ trialCount }} {{ t('referral.times') }}</span>
          <span>💰 {{ t('referral.paidRewards') }}：<b>{{ paidTokens }}</b> token</span>
        </div>
      </div>
      <img v-if="inviteUrl" :src="qrImgUrl" alt="QR" class="ref-qr" />
    </div>

    <!-- 邀请记录（2026-08-26 需求增强：被邀人ID/邮箱 + 邀请状态 + 支付状态 + 奖励明细） -->
    <table class="ad-table" style="margin-top:16px">
      <thead><tr>
        <th>{{ t('referral.colInvitee') }}</th>
        <th>{{ t('referral.colEmail') }}</th>
        <th>{{ t('referral.colInviteStatus') }}</th>
        <th>{{ t('referral.colPayStatus') }}</th>
        <th>{{ t('referral.colReward') }}</th>
        <th>{{ t('referral.colTime') }}</th>
      </tr></thead>
      <tbody>
        <tr v-for="(r, i) in records" :key="i">
          <td>{{ r.invitee_name }} (#{{ r.invitee_uid }})</td>
          <td>{{ r.invitee_email || '—' }}</td>
          <td><span class="ref-ok">✅ {{ t('referral.invSuccess') }}</span></td>
          <td>
            <span v-if="r.paid" class="ref-ok">✅ {{ t('referral.payYes') }}</span>
            <span v-else class="ref-no">⏳ {{ t('referral.payNo') }}</span>
          </td>
          <td>
            <template v-if="r.type === 'trial_stack'">
              +{{ fmtNum(r.tokens) }} token<template v-if="r.days"> / +{{ r.days }} {{ t('referral.daysUnit') }}</template>
            </template>
            <template v-else>+{{ fmtNum(r.tokens) }} token</template>
          </td>
          <td>{{ fmtTime(r.created_at) }}</td>
        </tr>
        <tr v-if="!records.length"><td colspan="6" style="text-align:center;color:#999">{{ t('referral.empty') }}</td></tr>
      </tbody>
    </table>
  </section>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { referralMy, referralQrUrl, type ReferralRecord } from '@/api'
import { fmtTime } from './ui'
import { t } from '@/i18n'

// 面板状态：邀请码/链接/记录/统计
const refCode = ref('')
const inviteUrl = ref('')
const records = ref<ReferralRecord[]>([])
const invited = ref(0)
const trialCount = ref(0)
const trialTokens = ref(0)
const paidTokens = ref(0)
// 二维码地址：img 预览 + a 标签下载
const qrImgUrl = referralQrUrl(false)
const qrDownloadUrl = referralQrUrl(true)

// load 加载我的邀请数据
async function load() {
  try {
    const r = await referralMy()
    if (!r.success) return
    refCode.value = r.ref_code || ''
    inviteUrl.value = r.invite_url || ''
    records.value = r.records || []
    invited.value = r.invited || 0
    trialCount.value = r.trial_count || 0
    trialTokens.value = r.trial_tokens || 0
    paidTokens.value = r.paid_tokens || 0
  } catch { /* 未登录等场景忽略 */ }
}

// fmtNum 千分位格式化（奖励 token 展示）
function fmtNum(n: number): string {
  return n ? n.toLocaleString('en-US') : '0'
}

// copyLink 复制专属邀请链接到剪贴板
async function copyLink() {
  if (!inviteUrl.value) return
  try {
    await navigator.clipboard.writeText(inviteUrl.value)
  } catch {
    // 剪贴板 API 不可用时回退 execCommand
    const el = document.createElement('textarea')
    el.value = inviteUrl.value
    document.body.appendChild(el)
    el.select()
    document.execCommand('copy')
    el.remove()
  }
}

onMounted(load)
</script>

<style scoped>
/* 顶部两栏：左链接信息 + 右二维码 */
.ref-top { display: flex; gap: 20px; align-items: flex-start; flex-wrap: wrap; }
.ref-link-box { flex: 1; min-width: 320px; background: rgba(64,128,255,.06); border: 1px solid rgba(64,128,255,.18); border-radius: 10px; padding: 14px 16px; }
.ref-code { font-size: 22px; font-weight: 700; letter-spacing: 2px; color: #1a237e; margin-top: 2px; }
.ref-stats { display: flex; gap: 18px; flex-wrap: wrap; margin-top: 12px; font-size: 13px; color: #555; }
.ref-qr { width: 150px; height: 150px; border-radius: 10px; border: 1px solid #e3e6ef; background: #fff; }
.ref-ok { color: #1b8a3f; font-weight: 600; }
.ref-no { color: #b26a00; }
</style>
