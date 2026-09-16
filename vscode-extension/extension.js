// ============================================================================
// 能言 LangCross VS Code 插件入口（★ H12）
//   · 侧边栏 Webview 工作台：输入/选目标语言/模式 → /openapi/v1/translate
//   · 编辑器命令：选中文本就地翻译替换（Cmd/Ctrl+Alt+T）
//   · API Key 支持设置项或 SecretStorage（后者更安全，命令式录入）
// ============================================================================
const vscode = require('vscode')

// ★ 缺陷修复（2026-09-16 核实与修复_测试盲区补全）：旧值 'langcros…iKey' 含省略号
//   字符（编辑器粘贴事故）——读写虽一致但属数据完整性隐患，更正为规范键名。
const SECRET_KEY = 'langcross.apiKey'

// 读取 langcross.* 插件配置（baseUrl / apiKey / 默认语言对）
function cfg() {
  return vscode.workspace.getConfiguration('langcross')
}

async function getApiKey(context) {
  const stored = await context.secrets.get(SECRET_KEY)
  return stored || cfg().get('apiKey') || ''
}

// translate 调用平台开放接口；返回 { translations } 或抛错文本
async function callTranslate(context, text) {
  const server = (cfg().get('serverUrl') || '').replace(/\/+$/, '')
  const key = await getApiKey(context)
  if (!server) throw new Error('请先设置 langcross.serverUrl（命令：能言：设置服务地址）')
  if (!key) throw new Error('请先设置 API Key（命令：能言：设置 API Key）')
  const langs = (cfg().get('targetLangs') || ['en']).slice(0, 5)
  const mode = cfg().get('mode') || 'fast'
  const res = await fetch(server + '/openapi/v1/translate', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + key },
    body: JSON.stringify({ text, target_langs: langs, mode }),
  })
  const j = await res.json().catch(() => null)
  if (!res.ok || !j || j.success === false) {
    throw new Error((j && (j.message || j.error_code)) || `HTTP ${res.status}`)
  }
  return j
}

// searchTerms 调平台术语检索开放接口；返回命中数组
async function searchTerms(context, q) {
  const server = (cfg().get('serverUrl') || '').replace(/\/+$/, '')
  const key = await getApiKey(context)
  if (!server || !key) throw new Error('请先配置服务地址与 API Key')
  const langs = (cfg().get('targetLangs') || []).slice(0, 1)
  const url = server + '/openapi/v1/terms?q=' + encodeURIComponent(q) + (langs.length ? '&lang=' + encodeURIComponent(langs[0]) : '')
  const res = await fetch(url, { headers: { Authorization: 'Bearer ' + key } })
  const j = await res.json().catch(() => null)
  if (!res.ok || !j || j.success === false) {
    throw new Error((j && (j.message || j.error_code)) || `HTTP ${res.status}`)
  }
  return j.terms || []
}

// activate 插件激活入口：注册翻译命令、选中文本直翻、术语查询与侧边栏视图
function activate(context) {
  const provider = new LangcrossPanel(context)
  context.subscriptions.push(
    vscode.window.registerWebviewViewProvider('langcross.panel', provider, {
      webviewOptions: { retainContextWhenHidden: true },
    })
  )

  context.subscriptions.push(
    vscode.commands.registerCommand('langcross.setApiKey', async () => {
      const v = await vscode.window.showInputBox({
        prompt: '输入能言开放接口 API Key（留空则清除）',
        password: true,
        ignoreFocusOut: true,
      })
      if (v === undefined) return
      if (v === '') {
        await context.secrets.delete(SECRET_KEY)
        vscode.window.showInformationMessage('能言：已清除 API Key')
      } else {
        await context.secrets.store(SECRET_KEY, v)
        vscode.window.showInformationMessage('能言：API Key 已保存（仅本机）')
      }
    }),
    vscode.commands.registerCommand('langcross.setServer', async () => {
      const v = await vscode.window.showInputBox({
        prompt: '平台服务地址（如 https://translate.example.com）',
        value: cfg().get('serverUrl') || '',
      })
      if (v) await cfg().update('serverUrl', v.replace(/\/+$/, ''), vscode.ConfigurationTarget.Global)
    }),
    vscode.commands.registerCommand('langcross.searchTerms', async () => {
      const q = await vscode.window.showInputBox({ prompt: '输入要检索的术语（源文或译文，≤100 字）' })
      if (!q || !q.trim()) return
      try {
        const terms = await vscode.window.withProgress(
          { location: vscode.ProgressLocation.Notification, title: '能言术语检索中…' },
          () => searchTerms(context, q.trim())
        )
        if (!terms.length) {
          vscode.window.showInformationMessage('能言：术语表无命中')
          return
        }
        const pick = await vscode.window.showQuickPick(
          terms.map((tm) => ({
            label: `${tm.source} → ${tm.target}`,
            description: `${tm.source_lang}→${tm.target_lang} · ${tm.package || ''}${tm.exact ? ' · 精确命中' : ''}`,
            tm,
          })),
          { placeHolder: `${terms.length} 条命中，回车复制到剪贴板` }
        )
        if (pick) {
          await vscode.env.clipboard.writeText(pick.tm.target)
          vscode.window.showInformationMessage(`已复制译文：${pick.tm.target}`)
        }
      } catch (e) {
        vscode.window.showErrorMessage(`能言：${e.message}`)
      }
    }),
    vscode.commands.registerCommand('langcross.translateSelection', async () => {
      const editor = vscode.window.activeTextEditor
      if (!editor || editor.selection.isEmpty) {
        vscode.window.showWarningMessage('能言：请先在编辑器中选中要翻译的文本')
        return
      }
      const text = editor.document.getText(editor.selection)
      try {
        const out = await vscode.window.withProgress(
          { location: vscode.ProgressLocation.Notification, title: '能言翻译中…' },
          () => callTranslate(context, text)
        )
        const langs = Object.keys(out.translations || {})
        if (!langs.length) throw new Error('无译文返回')
        const pick = langs.length === 1 ? langs[0] : await vscode.window.showQuickPick(langs, { placeHolder: '选择要替换的目标语言' })
        if (!pick) return
        await editor.edit((b) => b.replace(editor.selection, out.translations[pick]))
      } catch (e) {
        vscode.window.showErrorMessage(`能言：${e.message}`)
      }
    })
  )
}

class LangcrossPanel {
  constructor(context) {
    this.context = context
  }
  resolveWebviewView(view) {
    this.view = view
    view.webview.options = { enableScripts: true }
    view.webview.html = this.html()
    view.webview.onDidReceiveMessage(async (msg) => {
      if (msg && msg.type === 'terms') {
        try {
          const terms = await searchTerms(this.context, String(msg.text || ''))
          void view.webview.postMessage({ type: 'terms_result', terms })
        } catch (e) {
          void view.webview.postMessage({ type: 'terms_result', terms: [], error: e.message })
        }
        return
      }
      if (msg && msg.type === 'translate') {
        try {
          const out = await callTranslate(this.context, String(msg.text || ''))
          void view.webview.postMessage({ type: 'result', translations: out.translations })
        } catch (e) {
          void view.webview.postMessage({ type: 'error', message: e.message })
        }
      }
    })
  }
  html() {
    const csp = `default-src 'none'; style-src ${this.view.webview.cspSource} 'unsafe-inline'; script-src 'unsafe-inline';`
    return /* html */ `<!DOCTYPE html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${csp}">
<style>
 body{font-family:var(--vscode-font-family);padding:8px;color:var(--vscode-foreground)}
 textarea{width:100%;height:90px;background:var(--vscode-input-background);color:var(--vscode-input-foreground);border:1px solid var(--vscode-input-border);border-radius:3px;padding:6px;box-sizing:border-box}
 button{margin-top:6px;padding:4px 14px;background:var(--vscode-button-background);color:var(--vscode-button-foreground);border:none;border-radius:3px;cursor:pointer}
 .out{margin-top:10px;padding:8px;border:1px solid var(--vscode-panel-border);border-radius:4px;white-space:pre-wrap;font-size:13px}
 .err{color:var(--vscode-errorForeground)}
 .lang{font-weight:bold;font-size:12px;margin-top:8px}
</style></head><body>
<textarea id="src" placeholder="输入要翻译的文本（源语言按内容自动识别）"></textarea>
<button id="go">翻译</button><span id="busy"></span>
<div id="res"></div>
<div style="margin-top:14px;border-top:1px solid var(--vscode-panel-border);padding-top:10px">
 <input id="tq" placeholder="术语检索（源文或译文）" style="width:70%;background:var(--vscode-input-background);color:var(--vscode-input-foreground);border:1px solid var(--vscode-input-border);border-radius:3px;padding:4px 6px">
 <button id="tg">查术语</button>
 <div id="tres" style="font-size:12px"></div>
</div>
<script>
 const vscode = acquireVsCodeApi();
 const res = document.getElementById('res'), busy = document.getElementById('busy');
 document.getElementById('go').onclick = () => {
   const text = document.getElementById('src').value.trim();
   if (!text) return;
   res.innerHTML = ''; busy.textContent = '翻译中…';
   vscode.postMessage({ type: 'translate', text });
 };
 const tres = document.getElementById('tres');
 document.getElementById('tg').onclick = () => {
   const q = document.getElementById('tq').value.trim();
   if (!q) return;
   tres.textContent = '检索中…';
   vscode.postMessage({ type: 'terms', text: q });
 };
 window.addEventListener('message', (e) => {
   const m = e.data; busy.textContent = '';
   // ★ XSS 修复（2026-09-16）：esc 提升到监听器顶层——错误消息同样经 HTML 转义后才可入
   //   innerHTML（旧实现仅 terms/译文路径转义，error 路径直插服务端文本，同文件口径不一致）
   const esc = (x) => String(x == null ? '' : x).replace(/&/g, '&amp;').replace(/</g, '&lt;');
   if (m.type === 'terms_result') {
     tres.innerHTML = (m.terms || []).slice(0, 20).map((t) =>
       '<div class="out">' + esc(t.source) + ' → ' + esc(t.target) +
       ' <span style="opacity:.7">(' + esc(t.target_lang) + (t.exact ? ' · 精确' : '') + ')</span></div>').join('')
       || ('<div class="out err">' + esc(m.error || '无命中') + '</div>');
     return;
   }
    if (m.type === 'error') { res.innerHTML = '<div class="out err">' + esc(m.message) + '</div>'; return }
   if (m.type === 'result') {
     const ts = m.translations || {};
     res.innerHTML = Object.keys(ts).map((l) => '<div class="lang">' + l + '</div><div class="out">' +
       String(ts[l]).replace(/&/g, '&amp;').replace(/</g, '&lt;') + '</div>').join('') || '<div class="out err">无译文</div>';
   }
 });
</script></body></html>`
  }
}

// deactivate 插件停用钩子（当前无需额外清理）
function deactivate() {}

module.exports = { activate, deactivate }
