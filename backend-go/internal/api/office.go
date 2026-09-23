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
	"strings"
)

// officeOrigin ★ B11（2026-09-12）：Word 加载项来源域不再代码明文，
// 取主站配置（system_config primary_host → env BRAND_DOMAIN_SUFFIX 拼装）。
// 未配置时返回空串，CORS 精确匹配自然不命中（加载项功能需显式配置后启用）。
func (s *Server) officeOrigin() string {
	if h := s.primaryHost(); h != "" {
		return "https://" + h
	}
	return ""
}

// handleOfficeManifest 返回 Word 加载项侧加载清单。
func (s *Server) handleOfficeManifest(w http.ResponseWriter, r *http.Request) {
	// 设置 XML 内容类型
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	// 返回预定义的 Word 加载项清单（★ B11：origin 占位符按主站配置注入）
	fmt.Fprint(w, strings.ReplaceAll(officeManifestXML, "__ORIGIN__", s.officeOrigin()))
}

// handleOfficeTaskPane 返回任务窗格页面（Office.js + 划词翻译 UI）。
func (s *Server) handleOfficeTaskPane(w http.ResponseWriter, r *http.Request) {
	// 设置 HTML 内容类型
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 返回预定义的任务窗格页面
	fmt.Fprint(w, officeTaskPaneHTML)
}

// officeManifestXML Word 任务窗格清单（SourceLocation 指向本站 taskpane；__ORIGIN__ 占位符运行时替换）。
const officeManifestXML = `<?xml version="1.0" encoding="UTF-8"?>
<OfficeApp xmlns="http://schemas.microsoft.com/office/appforoffice/1.1"
           xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
           xmlns:bt="http://schemas.microsoft.com/office/officeappbasictypes/1.0"
           xmlns:ov="http://schemas.microsoft.com/office/taskpaneappversionoverrides"
           xsi:type="TaskPaneApp">
  <Id>7f3a2c64-9b1e-4e5d-8a77-53c2f0a91b10</Id>
  <Version>1.0.0.0</Version>
  <ProviderName>能言</ProviderName>
  <DefaultLocale>zh-CN</DefaultLocale>
  <DisplayName DefaultValue="能言 · 划译"/>
  <Description DefaultValue="选中文字一键翻译：调用企业自建能言，术语与翻译记忆全量生效。"/>
  <IconUrl DefaultValue="__ORIGIN__/favicon.ico"/>
  <SupportUrl DefaultValue="__ORIGIN__/docs/terms"/>
  <AppDomains>
    <AppDomain>__ORIGIN__</AppDomain>
  </AppDomains>
  <Hosts>
    <Host Name="Document"/>
  </Hosts>
  <Requirements>
    <Sets><Set Name="WordApi" MinVersion="1.1"/></Sets>
  </Requirements>
  <DefaultSettings>
    <SourceLocation DefaultValue="__ORIGIN__/office/taskpane.html"/>
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
/* ★ 2026-09-22 全站 UI 还原批：Word 任务窗格原先是「Google 蓝 #1a73e8 + indigo 标题 + 白底」的
   独立浅底主题。本页由后端直出、不在前端构建产物里，前端令牌闸门扫不到（与 /docs/*、/openapi/docs、
   assist 管理台同一类盲区）。现按 UI-ANNOTATIONS §1.1 令牌走纯黑底，主按钮＝白底黑字实心件
   （交付真值 .lc-btn--primary{background:#FFFFFF}），次按钮走描边档；绿色成功态废止（全站无蓝无绿）。 */
:root{
  --lc-bg:#000000;--lc-panel:#0E1014;--lc-surface:#16181C;--lc-inset:#0A0B0D;
  --lc-text:#E7E9EA;--lc-text-2:#9AA0AA;--lc-text-3:#71767B;
  --lc-line:#464C58;--lc-pill:#424956;--lc-card-line:#3A404C;--lc-white:#FFFFFF;
  --lc-danger:#E5484D;
}
*{box-sizing:border-box}
body{font-family:'Noto Sans SC',system-ui,'PingFang SC','Microsoft YaHei',sans-serif;margin:0;padding:12px;color:var(--lc-text);background:var(--lc-bg);font-size:12px;line-height:1.6}
h3{margin:0 0 10px;font-size:14px;font-weight:700;color:var(--lc-white)}
input{width:100%;padding:7px 9px;background:var(--lc-inset);border:1.2px solid var(--lc-line);border-radius:8px;font-size:12px;color:var(--lc-text);margin-bottom:8px}
input::placeholder{color:var(--lc-text-3)}
input:focus{outline:none;border-color:var(--lc-pill)}
/* 主操作＝纯白底黑字（唯一实心白件，视觉强度最高）；次操作＝深底描边档 */
button{width:100%;padding:8px;border:none;border-radius:8px;background:var(--lc-white);color:#000000;font-size:13px;font-weight:500;cursor:pointer;margin-bottom:8px}
button.sec{background:var(--lc-panel);color:var(--lc-text);border:1.2px solid var(--lc-pill)}
button.sec:hover{border-color:var(--lc-line);background:var(--lc-surface)}
button:disabled{opacity:.42;cursor:not-allowed}
#result{white-space:pre-wrap;word-break:break-word;border:1.2px dashed var(--lc-card-line);border-radius:10px;padding:8px;min-height:48px;font-size:12px;line-height:1.6;color:var(--lc-text-2);background:var(--lc-panel)}
#result.ok{color:var(--lc-text);border-style:solid;border-color:var(--lc-line)}
.hint{color:var(--lc-text-3);font-size:11px;line-height:1.5;margin-top:8px}
</style>
</head>
<body>
<h3>🌐 能言 · 划译</h3>
<input id="apiKey" placeholder="开放 API Key（管理后台签发）"/>
<input id="langs" placeholder="目标语言，逗号分隔（默认 en）"/>
<button id="btnGo">翻译选中文本</button>
<button id="btnInsert" class="sec">将结果插入文档</button>
<div id="result">选中文字后点击「翻译选中文本」。</div>
<div class="hint">首次使用请填入 API Key 并保存。术语包 / 翻译记忆 / 质检与企业版控制台同源生效。</div>
<script>
var cfgKey='trz_office_cfg';
// 面板本地配置：API Key 与目标语言存在 localStorage（trz_office_cfg），刷新后不丢
var cfg=JSON.parse(localStorage.getItem(cfgKey)||'{"apiKey":"","langs":"en"}');
document.getElementById('apiKey').value=cfg.apiKey;
document.getElementById('langs').value=cfg.langs;
function saveCfg(){cfg={apiKey:document.getElementById('apiKey').value.trim(),langs:document.getElementById('langs').value.trim()||'en'};localStorage.setItem(cfgKey,JSON.stringify(cfg));return cfg}
function setR(t,ok){var el=document.getElementById('result');el.textContent=t;el.className=ok?'ok':''}
// 暂存最近一次译文，供「将结果插入文档」按钮取用（Office.js 的 insertText 需在用户手势里调用）
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
