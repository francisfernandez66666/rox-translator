// ============================================================================
// components/LangMultiSelect.tsx — 目标语言多选（TDesign Select 分组 + 自定义语言）
// 功能等价 Vue 版：KB 九语分组 / 其他常用语分组 / 手输自定义语言代码加入选中集。
// ============================================================================
import { useEffect, useMemo, useState } from 'react'
import { Select, Input, Button, Space } from 'tdesign-react'
import { t } from '@/i18n'

// KB 九语（与后端 /api/translation/langs 对齐的本地兜底；挂载后由父组件动态覆盖可选）
// 知识库支持的高质量目标语言（本地兜底，后端返回后覆盖名称与国旗）
const KB_LANGS: Array<{ code: string; label: string; flag?: string }> = [
  { code: 'en', label: '英语', flag: '🇬🇧' },
  { code: 'ru', label: '俄语', flag: '🇷🇺' },
  { code: 'ar', label: '阿拉伯语', flag: '🇸🇦' },
  { code: 'es', label: '西班牙语', flag: '🇪🇸' },
  { code: 'pt', label: '葡萄牙语', flag: '🇵🇹' },
  { code: 'fr', label: '法语', flag: '🇫🇷' },
  { code: 'kk', label: '哈萨克语', flag: '🇰🇿' },
  { code: 'de', label: '德语', flag: '🇩🇪' },
  { code: 'zh_hant', label: '繁体中文', flag: '🇹🇼' },
]

// 其他常用语言（非 KB，走 AI 翻译）分组选项
const OTHER_LANGS: Array<{ code: string; label: string; flag?: string }> = [
  { code: 'ja', label: '日语', flag: '🇯🇵' },
  { code: 'ko', label: '韩语', flag: '🇰🇷' },
  { code: 'th', label: '泰语', flag: '🇹🇭' },
  { code: 'vi', label: '越南语', flag: '🇻🇳' },
  { code: 'ms', label: '马来语', flag: '🇲🇾' },
  { code: 'id_lang', label: '印尼语', flag: '🇮🇩' },
  { code: 'it', label: '意大利语', flag: '🇮🇹' },
  { code: 'pl', label: '波兰语', flag: '🇵🇱' },
  { code: 'tr', label: '土耳其语', flag: '🇹🇷' },
]

// LangMultiSelect 入参：value 当前选中语言代码数组；onChange 变更回调；kbLangs 覆盖 KB 分组
interface Props {
  value: string[]
  onChange: (v: string[]) => void
  /** 覆盖 KB 分组选项（默认本地九语） */
  kbLangs?: string[]
}

// 默认导出组件：目标语言多选（分组 Select + 自定义语言输入），等价 Vue 版 LanguagePicker
export default function LangMultiSelect({ value, onChange, kbLangs }: Props) {
  const [custom, setCustom] = useState('')
  const [extraCodes, setExtraCodes] = useState<string[]>([])
  // 从后端加载 KB 支持语言（新语言升级进 KB 区，名称/国旗覆盖本地兜底）
  const [apiKb, setApiKb] = useState<Array<{ code: string; label: string; flag?: string }>>([])

  // 挂载时从后端拉取 KB 语言列表，覆盖/补充本地兜底选项
  useEffect(() => {
    ;(async () => {
      try {
        const resp = await fetch('/api/translation/langs')
        if (!resp.ok) return
        const data = await resp.json()
        if (data.kb_langs?.length) {
          setApiKb(data.kb_langs.map((l: any) => ({ code: l.code, label: l.name, flag: l.flag })))
        }
      } catch { /* 静默保留内置选项 */ }
    })()
  }, [])

  // 构造分组选项：KB 分组 + 其他语言分组 + 自定义语言分组（如有）
  const options = useMemo(() => {
    const kbSet = kbLangs?.length ? kbLangs : KB_LANGS.map((x) => x.code)
    const kbMap = new Map<string, { code: string; label: string; flag?: string }>()
    for (const x of [...KB_LANGS, ...apiKb]) kbMap.set(x.code, { ...(kbMap.get(x.code) || {}), ...x })
    const nameOf = (code: string): string => {
      const hit = [...kbMap.values(), ...OTHER_LANGS].find((x) => x.code === code)
      return hit ? `${hit.flag || ''} ${hit.label}` : code
    }
    return [
      {
        group: t('chat.kbGroup'),
        children: [...kbMap.values()].filter((x) => kbSet.includes(x.code))
          .map((x) => ({ label: `${x.flag || ''} ${x.label}`, value: x.code })),
      },
      {
        group: t('chat.otherGroup'),
        children: OTHER_LANGS.map((x) => ({ label: `${x.flag || ''} ${x.label}`, value: x.code })),
      },
      ...(extraCodes.length
        ? [{
            group: t('chat.customGroup'),
            children: extraCodes.map((c) => ({ label: nameOf(c), value: c })),
          }]
        : []),
    ]
  }, [kbLangs, extraCodes])

  // 将手输语言代码加入自定义分组与选中值
  function addCustom() {
    const code = custom.trim()
    if (!code || value.includes(code)) return
    setExtraCodes((prev) => (prev.includes(code) ? prev : [...prev, code]))
    onChange([...value, code])
    setCustom('')
  }

  return (
    <Space direction="vertical" style={{ width: '100%' }} size={6}>
      <Select
        multiple
        clearable
        filterable
        value={value}
        options={options as never}
        placeholder={t('chat.langPlaceholder')}
        onChange={(v) => onChange((v as string[]) || [])}
        style={{ width: '100%' }}
      />
      <Space size={6}>
        <Input size="small" style={{ width: 180 }} value={custom}
               placeholder={t('chat.customLangPlaceholder')}
               onChange={setCustom}
               onEnter={() => addCustom()} />
        <Button size="small" variant="outline" disabled={!custom.trim()} onClick={addCustom}>＋</Button>
      </Space>
    </Space>
  )
}
