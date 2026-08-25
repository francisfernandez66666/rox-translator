// ============ office.go · 职责说明 ============
// Office 在线预览回调接口：为 OnlyOffice/WOPI 类前端提供文件回源与鉴权。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// Office(WPS/Word) 划译任务窗格插件（三期）：
//   - GET /office/manifest.xml —— 侧加载清单（Word: 插入→获取加载项→上传我的加载项）
//   - GET /office/taskpane.html —— 任务窗格页面（Office.js 取选区 → 开放 API 翻译 → 展示/插入）
// 认证走开放 API Key（与浏览器插件一致）；服务地址即当前站点，无需用户填写。
// 页面内嵌字符串发布（与 /docs/*、/pricing 同模式），无静态资源依赖。
// =============================================

import (
	"fmt"
	"net/http"
)

// officeOrigin Word 加载项（Office Add-in）的固定来源域，用于 CORS 放行与回跳校验。
const officeOrigin = "https://langcross.lexicorn.cn"

// handleOfficeManifest 返回 Word 加载项侧加载清单。
func (s *Server) handleOfficeManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	fmt.Fprint(w, officeManifestXML)
}

// handleOfficeTaskPane 返回任务窗格页面（Office.js + 划词翻译 UI）。
func (s *Server) handleOfficeTaskPane(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, officeTaskPaneHTML)
}

// officeManifestXML Word 任务窗格清单（SourceLocation 指向本站 taskpane）。
const officeManifestXML = `<?xml version="1.0" encoding="UTF-8"?>
<OfficeApp xmlns="http://schemas.microsoft.com/office/appforoffice/1.1"
           xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
           xmlns:bt="http://schemas.microsoft.com/office/officeappbasictypes/1.0"
           xmlns:ov="http://schemas.microsoft.com/office/taskpaneappversionoverrides"
           xsi:type="TaskPaneApp">
  <Id>7f3a2c64-9b1e-4e5d-8a77-53c2f0a91b10</Id>
  <Version>1.0.0.0</Version>
  <ProviderName>翻译助手</ProviderName>
  <DefaultLocale>zh-CN</DefaultLocale>
  <DisplayName DefaultValue="翻译助手 · 划译"/>
  <Description DefaultValue="选中文字一键翻译：调用企业自建翻译助手，术语与翻译记忆全量生效。"/>
  <IconUrl DefaultValue="` + officeOrigin + `/favicon.ico"/>
  <SupportUrl DefaultValue="` + officeOrigin + `/docs/terms"/>
  <AppDomains>
    <AppDomain>` + officeOrigin + `</AppDomain>
  </AppDomains>
  <Hosts>
    <Host Name="Document"/>
  </Hosts>
  <Requirements>
    <Sets><Set Name="WordApi" MinVersion="1.1"/></Sets>
  </Requirements>
  <DefaultSettings>
    <SourceLocation DefaultValue="` + officeOrigin + `/office/taskpane.html"/>
  </DefaultSettings>
  <Permissions>ReadWriteDocument</Permissions>
</OfficeApp>`

// officeTaskPaneHTML 任务窗格页面：配置区（API Key/目标语言）+ 选区译文展示 + 插入按钮。
const officeTaskPaneHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<script src="https://appsforoffice.microsoft.com/lib/1/hosted/office.js"></script>
<style>
body{font-family:system-ui,'PingFang SC','Microsoft YaHei',sans-serif;margin:0;padding:12px;color:#202124;background:#fff}
h3{margin:0 0 8px;font-size:14px;color:#1a237e}
input{width:100%;box-sizing:border-box;padding:7px 9px;border:1px solid #dadce0;border-radius:8px;font-size:12px;margin-bottom:8px}
button{width:100%;padding:8px;border:none;border-radius:8px;background:#1a73e8;color:#fff;font-size:13px;cursor:pointer;margin-bottom:8px}
button.sec{background:#e8f0fe;color:#1a73e8}
button:disabled{opacity:.5;cursor:not-allowed}
#result{white-space:pre-wrap;word-break:break-word;border:1px dashed #c6c6c6;border-radius:8px;padding:8px;min-height:48px;font-size:12px;line-height:1.6;color:#333}
.hint{color:#888;font-size:11px;line-height:1.5;margin-top:8px}
.ok{color:#2e7d32}
</style>
</head>
<body>
<h3>🌐 翻译助手 · 划译</h3>
<input id="apiKey" placeholder="开放 API Key（管理后台签发）"/>
<input id="langs" placeholder="目标语言，逗号分隔（默认 en）"/>
<button id="btnGo">翻译选中文本</button>
<button id="btnInsert" class="sec">将结果插入文档</button>
<div id="result">选中文字后点击「翻译选中文本」。</div>
<div class="hint">首次使用请填入 API Key 并保存。术语包 / 翻译记忆 / 质检与企业版控制台同源生效。</div>
<script>
var cfgKey='trz_office_cfg';
var cfg=JSON.parse(localStorage.getItem(cfgKey)||'{"apiKey":"","langs":"en"}');
document.getElementById('apiKey').value=cfg.apiKey;
document.getElementById('langs').value=cfg.langs;
function saveCfg(){cfg={apiKey:document.getElementById('apiKey').value.trim(),langs:document.getElementById('langs').value.trim()||'en'};localStorage.setItem(cfgKey,JSON.stringify(cfg));return cfg}
function setR(t,ok){var el=document.getElementById('result');el.textContent=t;el.className=ok?'ok':''}
var lastResult='';
document.getElementById('btnGo').onclick=function(){
  var b=this;b.disabled=true;setR('翻译中…',false);
  var cfg=saveCfg();
  if(!cfg.apiKey){setR('请先填入 API Key',false);b.disabled=false;return}
  Office.context.document.getSelectedDataAsync(Office.CoercionType.Text,function(res){
    if(res.status!==Office.AsyncResultStatus.Succeeded||!res.value.trim()){setR('未取到选中文本',false);b.disabled=false;return}
    fetch(location.origin+'/openapi/v1/translate',{method:'POST',
      headers:{'Content-Type':'application/json','Authorization':'Bearer '+cfg.apiKey},
      body:JSON.stringify({text:res.value.trim(),target_langs:cfg.langs.split(',')})})
    .then(function(r){return r.json()})
    .then(function(d){
      b.disabled=false;
      if(!d.success||!d.translations){setR('失败：'+(d.message||'未知错误'),false);return}
      lastResult=Object.keys(d.translations).map(function(lc){return lc+': '+d.translations[lc]}).join('\n');
      setR(lastResult,true);
    })
    .catch(function(e){b.disabled=false;setR('网络错误：'+e.message,false)});
  });
};
document.getElementById('btnInsert').onclick=function(){
  if(!lastResult){setR('请先翻译',false);return}
  Office.context.document.setSelectedDataAsync(lastResult,{coercionType:Office.CoercionType.Text},function(res){
    if(res.status===Office.AsyncResultStatus.Succeeded){setR('已插入文档 ✓',true)}
  });
};
</script>
</body>
</html>`
