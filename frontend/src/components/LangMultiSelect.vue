<!-- ============================================================================
   components/LangMultiSelect.vue — 多选目标语言选择器（与工作台同款交互）
   职责：🌐 按钮 + 下拉多选（知识库语言区 / 其他语言区 / 更多语言手输）；
   v-model 绑定语言代码数组；KB 语言列表从 /api/translation/langs 动态加载。
   ============================================================================ -->
<template>
  <div class="lms" ref="rootRef">
    <button type="button" class="lms-btn" @click.stop="open = !open">
      🌐 {{ btnLabel }}
    </button>
    <div v-if="open" class="lang-dropdown lms-dropdown">
      <div class="lang-section-title">{{ t('chat.kbLangs') }}</div>
      <label v-for="lang in kbLangs" :key="'kb-' + lang" class="lang-option">
        <input type="checkbox" :checked="modelValue.includes(lang)" @change="toggle(lang)" />
        <span>{{ LANG_OPTIONS[lang]?.flag || '🌐' }} {{ langLabel(lang, LANG_OPTIONS[lang]?.label) }}</span>
      </label>
      <div class="lang-divider"></div>
      <div class="lang-section-title">{{ t('chat.otherLangs') }}</div>
      <label v-for="olang in otherLangList" :key="'o-' + olang.code" class="lang-option">
        <input type="checkbox" :checked="modelValue.includes(olang.code)" @change="toggle(olang.code)" />
        <span>{{ olang.flag }} {{ langLabel(olang.code, olang.label) }}</span>
      </label>
      <div class="lang-divider" style="margin:4px 0"></div>
      <label class="lang-option">
        <input type="checkbox" :checked="modelValue.includes('other')" @change="toggle('other')" />
        <span>{{ t('chat.moreLangs') }}</span>
      </label>
      <div v-if="modelValue.includes('other')" class="lms-custom">
        <input v-model="customText" class="lang-custom-input" :placeholder="t('chat.customLangPlaceholder')" @keydown.enter.prevent="addCustom" />
        <button type="button" class="lms-add" @click="addCustom" :disabled="!customText.trim()">＋</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { t } from '@/i18n'

// v-model：已选语言代码数组
const props = defineProps<{ modelValue: string[] }>()
const emit = defineEmits<{ 'update:modelValue': [string[]] }>()

// KB 语言（默认兜底，挂载后从后端动态覆盖）
const kbLangs = ref<string[]>(['en', 'ru', 'ar', 'es', 'pt', 'fr', 'kk', 'de', 'zh_hant'])
// 语言名称/国旗映射（本地兜底）
const LANG_OPTIONS: Record<string, { label: string; flag: string }> = {
  en: { label: '英语', flag: '🇬🇧' }, ru: { label: '俄语', flag: '🇷🇺' },
  ar: { label: '阿拉伯语', flag: '🇸🇦' }, es: { label: '西班牙语', flag: '🇪🇸' },
  pt: { label: '葡萄牙语', flag: '🇵🇹' }, fr: { label: '法语', flag: '🇫🇷' },
  kk: { label: '哈萨克语', flag: '🇰🇿' }, de: { label: '德语', flag: '🇩🇪' },
  zh_hant: { label: '繁体中文', flag: '🇹🇼' },
}
// 其他 AI 翻译语言
const OTHER_LANG_OPTIONS = ref<Record<string, { label: string; flag: string }>>({
  ja: { label: '日语', flag: '🇯🇵' }, ko: { label: '韩语', flag: '🇰🇷' },
  th: { label: '泰语', flag: '🇹🇭' }, vi: { label: '越南语', flag: '🇻🇳' },
  ms: { label: '马来语', flag: '🇲🇾' }, id_lang: { label: '印尼语', flag: '🇮🇩' },
  it: { label: '意大利语', flag: '🇮🇹' }, pl: { label: '波兰语', flag: '🇵🇱' },
  tr: { label: '土耳其语', flag: '🇹🇷' },
})
const otherLangList = computed(() =>
  Object.entries(OTHER_LANG_OPTIONS.value).map(([code, info]) => ({ code, ...info }))
)

const open = ref(false)
const customText = ref('')
const rootRef = ref<HTMLElement | null>(null)

// 切换勾选（other 为「更多语言」开关位，不入数组）
function toggle(lang: string) {
  if (lang === 'other') return
  const sel = props.modelValue.includes(lang)
    ? props.modelValue.filter(l => l !== lang)
    : [...props.modelValue, lang]
  emit('update:modelValue', sel)
}

// 添加自定义语言
function addCustom() {
  const raw = customText.value.trim()
  if (!raw) return
  if (!props.modelValue.includes(raw)) {
    emit('update:modelValue', [...props.modelValue, raw])
  }
  customText.value = ''
}

// 显示名：优先 i18n lang.<code>，回退本地映射
function langLabel(code: string, fallback?: string): string {
  const v = t(`lang.${code}`)
  return v !== `lang.${code}` ? v : (fallback || code)
}

// 按钮标签：0=0语；≤2 展开名称；更多显示数量
const btnLabel = computed(() => {
  const sel = props.modelValue
  if (sel.length === 0) return t('chat.langZero')
  if (sel.length <= 2) {
    const names = sel.map(l => {
      if (l === 'other') return t('chat.langOther')
      return langLabel(l, LANG_OPTIONS[l]?.label || OTHER_LANG_OPTIONS.value[l]?.label)
    })
    return names.join('+')
  }
  return t('chat.langCount').replace('{n}', String(sel.length))
})

// 从后端加载 KB 支持的语言（新语言升级进 KB 区）
async function loadTranslationLangs() {
  try {
    const resp = await fetch('/api/translation/langs')
    if (!resp.ok) return
    const data = await resp.json()
    if (data.kb_langs?.length) {
      kbLangs.value = data.kb_langs.map((l: any) => l.code)
      for (const l of data.kb_langs) {
        if (!LANG_OPTIONS[l.code]) LANG_OPTIONS[l.code] = { label: l.name, flag: l.flag }
        else { LANG_OPTIONS[l.code].label = l.name; LANG_OPTIONS[l.code].flag = l.flag }
        if (OTHER_LANG_OPTIONS.value[l.code]) delete OTHER_LANG_OPTIONS.value[l.code]
      }
    }
  } catch { /* 静默保留内置选项 */ }
}

// 点击外部关闭下拉
function onClickOutside(e: MouseEvent) {
  if (rootRef.value && !rootRef.value.contains(e.target as Node)) open.value = false
}
onMounted(() => {
  loadTranslationLangs()
  document.addEventListener('click', onClickOutside)
})
onUnmounted(() => document.removeEventListener('click', onClickOutside))
</script>

<style scoped>
.lms { position: relative; display: inline-block; }
.lms-btn {
  border: 1px solid #d8dee6; background: #fff; border-radius: 8px;
  padding: 7px 14px; font-size: 14px; cursor: pointer;
}
.lms-btn:hover { border-color: #bdc1c6; }
.lms-dropdown {
  position: absolute; bottom: calc(100% + 6px); left: 0; min-width: 220px;
  background: #fff; border: 1px solid #dadce0; border-radius: 12px;
  box-shadow: 0 4px 16px rgba(0,0,0,.12); padding: 8px 0;
  z-index: 100; max-height: 360px; overflow-y: auto;
}
.lang-option { display: flex; align-items: center; gap: 8px; padding: 8px 16px; cursor: pointer; font-size: 13px; color: #202124; }
.lang-option:hover { background: #f1f3f4; }
.lang-option input[type="checkbox"] { width: 16px; height: 16px; accent-color: #1a73e8; }
.lang-divider { height: 1px; background: #e0e0e0; margin: 0 16px 4px; }
.lang-section-title { padding: 8px 16px 4px; font-size: 12px; font-weight: 600; color: #5f6368; }
.lms-custom { display: flex; align-items: center; gap: 6px; padding: 4px 12px 8px 40px; }
.lang-custom-input {
  flex: 1; border: 1px solid #d8dee6; border-radius: 6px; padding: 5px 8px; font-size: 12px;
}
.lms-add {
  border: none; background: #1a73e8; color: #fff; width: 24px; height: 24px;
  border-radius: 50%; cursor: pointer; font-size: 13px; line-height: 1;
}
.lms-add:disabled { opacity: .4; cursor: not-allowed; }
</style>