// ============================================================================
// components/admin/SdkP.tsx — 官方 SDK 集成面板（外部调用 Hub 第三子 tab，2026-09-15）
// 职责：向集成方展示 Python / TypeScript / Java 三端 SDK 的安装方式、
//       最小接入示例与文档入口；内容随版本发布（scripts/release-sdk.sh）保持同步。
// ============================================================================

/**
 * SdkP.tsx · 职责说明
 * - 三端卡片：包名 / 安装命令 / 快速开始代码片段（一键复制）
 * - 版本与变更：当前 SDK 基线版本 + CHANGELOG 位置说明
 * - 鉴权提示：SDK 使用开放 API Key（引导至「开放 API」子 tab 创建）
 */

import { useState } from 'react'
import { Button, Tag, Space } from 'tdesign-react'
import { useT } from '@/i18n'

// SDK 三端元信息与示例片段（片段内 API 基址与 Key 为占位符）
interface SdkCard {
  key: string; icon: string; pkg: string; version: string;
  install: string; code: string;
}
const SDK_VERSION = '1.0.0'
// SDK 卡片配置（各语言 SDK 的安装/示例代码块）
const CARDS: SdkCard[] = [
  {
    key: 'python', icon: '🐍', pkg: 'langcross-translator', version: SDK_VERSION,
    install: 'pip install langcross-translator',
    code: `from langcross_translator import TranslatorClient

client = TranslatorClient(api_key="lxk_your_key", base_url="https://<站点域名>")
result = client.translate_text("您好，欢迎使用。", target_langs=["en", "ja"])
print(result.output_by_lang["en"])`,
  },
  {
    key: 'typescript', icon: '🟦', pkg: '@langcross/translator-sdk', version: SDK_VERSION,
    install: 'npm install @langcross/translator-sdk',
    code: `import { TranslatorClient } from '@langcross/translator-sdk'

// 示例代码片段：客户端初始化（文档展示用，非运行实例）
const client = new TranslatorClient({ apiKey: 'lxk_your_key', baseUrl: 'https://<站点域名>' })
// 示例代码片段：同步翻译调用（文档展示用）
const result = await client.translateText('您好，欢迎使用。', ['en', 'ja'])
console.log(result.outputByLang.en)`,
  },
  {
    key: 'java', icon: '☕', pkg: 'com.langcross:translator-sdk', version: SDK_VERSION,
    install: '<!-- Maven 源码分发（见仓库 sdk/java） -->',
    code: `TranslatorClient client = TranslatorClient.builder()
    .apiKey("lxk_your_key").baseUrl("https://<站点域名>").build();
TranslateResult r = client.translateText("您好，欢迎使用。", List.of("en", "ja"));
System.out.println(r.outputByLang().get("en"));`,
  },
]

/** SDK 集成面板：三端安装 + 最小示例 + API Key 引导 */
export default function SdkP() {
  const [, t] = useT()
  const [copied, setCopied] = useState('')

  /** copy 复制文本到剪贴板并短暂反馈 */
  async function copy(k: string, text: string) {
    try { await navigator.clipboard.writeText(text); setCopied(k); setTimeout(() => setCopied(''), 1500) } catch { /* 非安全上下文忽略 */ }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <p style={{ fontSize: 13, color: 'var(--adm-hint)', margin: 0 }}>{t('sdk.hint')}</p>

      {CARDS.map((c) => (
        <div key={c.key} style={{ border: '1px solid var(--td-border-color, #e3e6ef)', borderRadius: 10, padding: 14 }}>
          <Space size={8} align="center" style={{ marginBottom: 8 }}>
            <span style={{ fontSize: 16 }}>{c.icon}</span>
            <b>{t('sdk.name.' + c.key)}</b>
            <code style={{ fontSize: 12, background: '#f5f7fb', padding: '1px 6px', borderRadius: 4 }}>{c.pkg}</code>
            <Tag theme="primary" variant="light">v{c.version}</Tag>
          </Space>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
            <code style={{ flex: 1, fontSize: 12, background: '#0b1021', color: '#9fe8b8', padding: '6px 10px', borderRadius: 6, overflowX: 'auto', whiteSpace: 'nowrap' }}>$ {c.install}</code>
            <Button size="small" variant="outline" onClick={() => void copy(c.key + 'i', c.install)}>{copied === c.key + 'i' ? t('sdk.copied') : t('sdk.copy')}</Button>
          </div>
          <pre style={{ fontSize: 12, background: '#f5f7fb', padding: '8px 10px', borderRadius: 6, overflowX: 'auto', margin: 0, whiteSpace: 'pre' }}>{c.code}</pre>
        </div>
      ))}

      <p style={{ fontSize: 12, color: 'var(--adm-faint)', margin: 0 }}>{t('sdk.keysHint')} · {t('sdk.changelogHint')}</p>
    </div>
  )
}
