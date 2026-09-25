// ============================================================================
// components/admin/SdkP.tsx — 官方 SDK 集成面板（外部调用 Hub 第三子 tab，2026-09-15）
// 职责：向集成方展示 Python / TypeScript / Java 三端 SDK 的安装方式、
//       最小接入示例与文档入口；内容随版本发布（scripts/release-sdk.sh）保持同步。
// 2026-09-18（UI 融合）：Tag/Space 换为 ui/langcross 的 Badge + flex 布局；卡片改用
//       --lc-border-card / --lc-panel 变量描边，代码块底色转暗；语言 emoji 图标位（SdkCard.icon）
//       暂置空串保留占位，接 <Icon> 时在此补图标名即可，不影响复制与渲染逻辑。
// ★ F-27（2026-09-25）假安装命令收口为本地交付物下载：
//   旧版 :35/:44 展示的是「直接 pip / npm 装公共仓库包名」的假命令——包从未发布到
//   PyPI / npm 公共仓库，用户照抄必然 404（负向字面量刻意不在本注释复现：
//   SdkP.dom.test.tsx 对源码做同串负向锁，注释命中也算红）。
//   现在安装命令与下载链接全部指向 scripts/build_sdk.sh 产出的本站托管物
//   （public/sdk/，随前端 dist 静态直出）；文件名运行时读 /sdk/manifest.json，
//   组件源码不落任何版本号（改版本只需重跑脚本，不用动前端——与扩展 latest 链接同一手法）。
//   manifest 取不到时（产物没随源部署）安装行与下载行整体不渲染，绝不回退到假命令。
// ============================================================================

/**
 * SdkP.tsx · 职责说明
 * - 三端卡片：包名 / 本地安装命令 / 快速开始代码片段（一键复制）
 * - ★ F-27：Python / TS 卡各挂本站托管交付物下载链接（文件名来自 /sdk/manifest.json 现读）
 * - 版本与变更：当前 SDK 基线版本 + CHANGELOG 位置说明（本地分发口径，不提公共仓库发版）
 * - 鉴权提示：SDK 使用开放 API Key（引导至「开放 API」子 tab 创建）
 * - ★ 2026-09-23 起另挂「浏览器划词插件」下载卡：zip 由 `scripts/build_extension.sh` 产出、
 *   随 `public/extensions/` 进 dist，本站只做同源直下，没有应用商店渠道
 */

import { useEffect, useState } from 'react'
import { Badge, Button } from '@/ui/langcross/src'
import { useT } from '@/i18n'

// SDK 三端元信息与示例片段（片段内 API 基址与 Key 为占位符）
interface SdkCard {
  key: string; icon: string; pkg: string; version: string;
  install: string; code: string;
}
const SDK_VERSION = '1.0.0'
// 浏览器划词插件的托管下载名（latest 是稳定链接，带版本号的同包也在同目录，便于回溯）
const EXT_ZIP = '/extensions/langcross-extension-latest.zip'
// ★ F-27：SDK 交付物清单的唯一事实源——由 scripts/build_sdk.sh 生成在 public/sdk/ 下，
//   页面运行时 fetch 取真实文件名；本组件源码禁止出现版本号或带版本号的产物文件名字面量。
const SDK_MANIFEST = '/sdk/manifest.json'

/** /sdk/manifest.json 的取值形状（只声明本组件用到的字段，多余字段忽略） */
interface SdkMeta {
  python: { version: string; wheel: string; sdist: string };
  typescript: { version: string; tarball: string };
}
// SDK 卡片配置（各语言 SDK 的示例代码块；install 行中 python/typescript 两条随 manifest 动态生成）
const CARDS: SdkCard[] = [
  {
    key: 'python', icon: '', pkg: 'langcross-translator', version: SDK_VERSION,
    // ★ F-27：占位空串——真实命令 = `pip install <manifest.python.wheel>`，加载 manifest 后渲染
    install: '',
    code: `from langcross_translator import TranslatorClient

client = TranslatorClient(api_key="lxk_your_key", base_url="https://<站点域名>")
result = client.translate_text("您好，欢迎使用。", target_langs=["en", "ja"])
print(result.output_by_lang["en"])`,
  },
  {
    key: 'typescript', icon: '', pkg: '@langcross/translator-sdk', version: SDK_VERSION,
    // ★ F-27：占位空串——真实命令 = `npm install ./<manifest.typescript.tarball>`
    install: '',
    code: `import { TranslatorClient } from '@langcross/translator-sdk'

// 示例代码片段：客户端初始化（文档展示用，非运行实例）
const client = new TranslatorClient({ apiKey: 'lxk_your_key', baseUrl: 'https://<站点域名>' })
// 示例代码片段：同步翻译调用（文档展示用）
const result = await client.translateText('您好，欢迎使用。', ['en', 'ja'])
console.log(result.outputByLang.en)`,
  },
  {
    key: 'java', icon: '', pkg: 'com.langcross:translator-sdk', version: SDK_VERSION,
    // Java 卡维持源码分发文案不动（本批不做 Java 产物托管，见 UAT 修复文档 F-27）
    install: '<!-- Maven 源码分发（见仓库 sdk/java） -->',
    code: `TranslatorClient client = TranslatorClient.builder()
    .apiKey("lxk_your_key").baseUrl("https://<站点域名>").build();
TranslateResult r = client.translateText("您好，欢迎使用。", List.of("en", "ja"));
System.out.println(r.outputByLang().get("en"));`,
  },
]

/** SDK 集成面板：三端本地安装 + 托管交付物下载 + 最小示例 + API Key 引导 */
export default function SdkP() {
  const [, t, tpl] = useT()
  const [copied, setCopied] = useState('')
  // ★ F-27：manifest 加载态；null = 还没取到（或产物没部署），此时 python/ts 卡不渲染安装与下载行
  const [meta, setMeta] = useState<SdkMeta | null>(null)

  useEffect(() => {
    let alive = true
    fetch(SDK_MANIFEST)
      .then((r) => (r.ok ? r.json() : null))
      .then((d) => {
        // 形状校验：缺段即视为产物未部署，保持 null（宁可少显示，不展示假命令/死链）
        if (alive && d && d.python && d.python.wheel && d.typescript && d.typescript.tarball) setMeta(d as SdkMeta)
      })
      .catch(() => { /* 取不到 manifest：静默，安装行整体不渲染 */ })
    return () => { alive = false }
  }, [])

  /** copy 复制文本到剪贴板并短暂反馈 */
  async function copy(k: string, text: string) {
    try { await navigator.clipboard.writeText(text); setCopied(k); setTimeout(() => setCopied(''), 1500) } catch { /* 非安全上下文忽略 */ }
  }

  /** installCmd 按卡取真实安装命令（python/ts 依赖 manifest；java 走静态字段） */
  function installCmd(c: SdkCard): string {
    if (!meta) return c.install
    if (c.key === 'python') return `pip install ${meta.python.wheel}`
    if (c.key === 'typescript') return `npm install ./${meta.typescript.tarball}`
    return c.install
  }

  /** dlOf 按卡取本站托管下载文件名（java 无托管产物，返回空串不渲染链接） */
  function dlOf(c: SdkCard): string {
    if (!meta) return ''
    if (c.key === 'python') return meta.python.wheel
    if (c.key === 'typescript') return meta.typescript.tarball
    return ''
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <p style={{ fontSize: 15, color: 'var(--adm-hint)', margin: 0 }}>{t('sdk.hint')}</p>

      {CARDS.map((c) => {
        const cmd = installCmd(c)
        const file = dlOf(c)
        const ver = meta && c.key === 'python' ? meta.python.version : meta && c.key === 'typescript' ? meta.typescript.version : c.version
        return (
          <div key={c.key} style={{ border: '1.2px solid var(--lc-border-card)', borderRadius: 10, padding: 14, background: 'var(--lc-panel)', boxShadow: 'var(--lc-panel-highlight)' }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 8 }}>
              <span style={{ fontSize: 18 }}>{c.icon}</span>
              <b>{t('sdk.name.' + c.key)}</b>
              <code style={{ fontSize: 14, background: '#0E1014', padding: '1px 6px', borderRadius: 4 }}>{c.pkg}</code>
              <Badge>v{ver}</Badge>
            </div>
            {cmd && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <code style={{ flex: 1, fontSize: 14, background:'var(--lc-inset)', color:'var(--lc-text-1)', padding:'6px 10px', borderRadius: 6, overflowX:'auto', whiteSpace:'nowrap'}}>$ {cmd}</code>
                <Button size="sm" variant="secondary" onClick={() => void copy(c.key + 'i', cmd)}>{copied === c.key + 'i' ? t('sdk.copied') : t('sdk.copy')}</Button>
              </div>
            )}
            {/* ★ F-27：每卡的本地交付物下载链接（href 指向 public/sdk 带版本名，
                下载落地即此文件名，pip/npm 直接可用——latest 别名只留给外部固定链路书签） */}
            {file && (
              <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 8 }}>
                <a href={`/sdk/${file}`} style={{ fontSize: 15 }}>{tpl(c.key === 'python' ? 'sdk.downloadPython' : 'sdk.downloadTs', { name: file })}</a>
              </div>
            )}
            {/* SDK 卡里的 code/pre 底取面板面 #0E1014（= --lc-panel，★ 〇-P 交付值），随全站面档走 */}
            <pre style={{ fontSize: 14, background:'#0E1014', padding:'8px 10px', borderRadius: 6, overflowX:'auto', margin: 0, whiteSpace:'pre'}}>{c.code}</pre>
          </div>
        )
      })}

      {/* ★ 2026-09-23 补扩展交付渠道：zip 由 scripts/build_extension.sh 产出并随 public/ 进 dist，
          这里只挂 latest 固定名——版本号唯一事实源是 extension/manifest.json，界面不复刻第二份，
          否则每发一版都要改前端（历史上「改了没处发」就是因为整条链都不存在）。 */}
      <div style={{ border: '1.2px solid var(--lc-border-card)', borderRadius: 10, padding: 14, background: 'var(--lc-panel)', boxShadow: 'var(--lc-panel-highlight)' }}>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 8 }}>
          <b>{t('sdk.extTitle')}</b>
          <a href={EXT_ZIP} style={{ fontSize: 15 }}>{t('sdk.extDownload')}</a>
        </div>
        <p style={{ fontSize: 14, color: 'var(--adm-hint)', margin: 0 }}>{t('sdk.extDesc')}</p>
      </div>

      {/* ★ F-27：本地交付口径提示（旧 changelogHint 的「npm/PyPI 同步发版」假话已订正） */}
      <p style={{ fontSize: 14, color: 'var(--adm-faint)', margin: 0 }}>{t('sdk.srcHint')}</p>
      <p style={{ fontSize: 14, color: 'var(--adm-faint)', margin: 0 }}>{t('sdk.keysHint')} · {t('sdk.changelogHint')}</p>
    </div>
  )
}
