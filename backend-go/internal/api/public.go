// ============ public.go · 职责说明 ============
// api 包内部实现文件。
// =============================================

// ============ 本文件职责中文说明 ============
// 公开商业页面：无需登录即可访问的产品介绍与合规文档。
//   - /pricing 定价页（商业套餐展示 + 常见问题；内部计价参数不对公网暴露）
//   - /docs/terms 服务条款、/docs/sla 服务等级协议、/docs/privacy 数据保护条款（DPA）
//
// 实现：直接由后端渲染内嵌 HTML（与 SPA 无关，public 无需登录），
//
//	文案中英双语内联，页面样式与整体品牌一致。
//
// ========================================
package api

import (
	"fmt"
	"net/http"
)

// handlePublicPricing 定价页：token 单价表 + 充值说明。
func (s *Server) handlePublicPricing(w http.ResponseWriter, r *http.Request) {
	// 设置 HTML 内容类型
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 返回预定义的定价页 HTML
	fmt.Fprint(w, pricingHTML)
}

// handlePublicTerms 服务条款页。
func (s *Server) handlePublicTerms(w http.ResponseWriter, r *http.Request) {
	// 设置 HTML 内容类型
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 返回服务条款页（中英双语）
	fmt.Fprint(w, publicDocPage("用户协议 / User Agreement", termsBody))
}

// handlePublicSLA 服务等级协议页（中英文切换）。
func (s *Server) handlePublicSLA(w http.ResponseWriter, r *http.Request) {
	// 设置 HTML 内容类型
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 返回服务等级协议页（支持中英文切换）
	fmt.Fprint(w, publicDocPageLang("服务等级协议 / SLA", slaBodyZh, slaBodyEn))
}

// publicDocPageLang 支持中英文切换的文档页（如 SLA）：顶栏提供「中文 / English」切换，
// 默认展示中文，切换后仅渲染对应语言段落，并记忆选择到 localStorage。
func publicDocPageLang(title, zh, en string) string {
	body := `<h1>` + title + `</h1>
<div class="lang-switch" style="margin:6px 0 18px;display:flex;gap:8px">
  <button id="btnZh" class="ls ls-on" onclick="setLang('zh')">中文</button>
  <button id="btnEn" class="ls" onclick="setLang('en')">English</button>
</div>
<div id="secZh">` + zh + `</div>
<div id="secEn" style="display:none">` + en + `</div>
<style>
.lang-switch .ls{border:1px solid var(--border);background:#fff;color:var(--text-2);font-size:13px;padding:6px 16px;border-radius:8px;cursor:pointer;transition:.2s}
.lang-switch .ls-on{background:var(--brand);border-color:var(--brand);color:#fff;font-weight:600}
</style>
<script>
function setLang(l){var z=document.getElementById('secZh'),e=document.getElementById('secEn'),bz=document.getElementById('btnZh'),be=document.getElementById('btnEn');
  if(l==='zh'){z.style.display='';e.style.display='none';bz.className='ls ls-on';be.className='ls'}else{z.style.display='none';e.style.display='';be.className='ls ls-on';bz.className='ls'}
  try{localStorage.setItem('doc_lang',l)}catch(_){}
}
try{var s=localStorage.getItem('doc_lang');if(s==='en')setLang('en')}catch(_){}
</script>`
	return publicLayout(title, body)
}

// handlePublicPrivacy 数据保护条款（DPA）页。
func (s *Server) handlePublicPrivacy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, publicDocPage("隐私协议 / Privacy Policy", privacyBody))
}

// publicLayout 公共页面外壳（品牌导航 + 内容区 + 页脚）。
// 视觉风格统一为「能言」TDesign 设计语言：蓝靛主色、统一圆角、浅底卡片，
// 与管理后台 / 登录页配色一致（替换原绿色主题）。
func publicLayout(title, body string) string {
	return `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + title + ` - 能言</title>
<style>
:root{--brand:#2b3ee8;--brand-hover:#4a5cf0;--brand-active:#1c2bd0;--brand-light:#e7ebff;--text:#1a2233;--text-2:#5a6478;--border:#e3e6ef;--bg:#f4f6fa}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',sans-serif;color:var(--text);background:var(--bg);line-height:1.75;font-size:15px}
.header{background:linear-gradient(135deg,#2b3ee8 0%,#1c2bd0 100%);color:#fff;padding:0 24px;display:flex;justify-content:space-between;align-items:center;height:60px;position:sticky;top:0;z-index:20}
.header .brand{font-size:18px;font-weight:700;letter-spacing:.3px;display:flex;align-items:center;gap:8px}
.header nav{display:flex;align-items:center;gap:6px}
.header nav a{color:#fff;text-decoration:none;font-size:14px;padding:6px 12px;border-radius:8px;transition:background .2s}
.header nav a:hover{background:rgba(255,255,255,.16)}
.header .btn{background:#fff;color:var(--brand);text-decoration:none;font-size:14px;font-weight:600;padding:7px 16px;border-radius:8px;margin-left:8px;transition:opacity .2s}
.header .btn:hover{opacity:.88}
.wrap{max-width:920px;margin:32px auto;padding:0 20px}
.card{background:#fff;border-radius:12px;padding:32px 36px;box-shadow:0 2px 12px rgba(26,35,126,.06);border:1px solid var(--border)}
h1{font-size:24px;font-weight:700;margin-bottom:6px;color:var(--brand)}
.doc-meta{color:var(--text-2);font-size:13px;margin-bottom:8px}
h2{font-size:18px;font-weight:600;margin:26px 0 10px;padding-left:11px;border-left:4px solid var(--brand);color:var(--text);line-height:1.4}
h3{font-size:15px;font-weight:600;margin:18px 0 8px;color:var(--text)}
p{margin:9px 0;color:#2b3145}
a{color:var(--brand);text-decoration:none}
a:hover{text-decoration:underline}
.footer{text-align:center;color:var(--text-2);font-size:13px;padding:28px;line-height:2}
.footer a{color:var(--text-2);margin:0 6px}
.footer a:hover{color:var(--brand)}
hr{border:none;border-top:1px solid var(--border);margin:24px 0}
table{width:100%;border-collapse:collapse;margin:14px 0;font-size:14px}
th,td{border:1px solid var(--border);padding:10px 12px;text-align:left}
th{background:var(--brand-light);color:var(--brand-active);font-weight:600}
.tag{display:inline-block;background:var(--brand-light);color:var(--brand-active);border-radius:999px;padding:2px 12px;font-size:12px;font-weight:500}
</style></head><body>
<div class="header"><div class="brand">🌐 能言 LangCross</div><nav><a href="/pricing">定价 Pricing</a><a href="/docs/terms">用户协议 Terms</a><a href="/docs/sla">SLA</a><a href="/docs/privacy">隐私协议 Privacy</a><a class="btn" href="/admin">管理后台</a></nav></div>
<div class="wrap"><div class="card">` + body + `</div></div>
<div class="footer">© 2026 能言 LangCross · 翻译平台<br><a href="/docs/terms">用户协议</a> · <a href="/docs/privacy">隐私协议</a> · <a href="/admin">管理后台</a></div>
</body></html>`
}

// publicDocPage 合规文档页包装（条款/SLA/DPA 共用外壳）。
func publicDocPage(title, body string) string {
	return publicLayout(title, `<h1>`+title+`</h1>`+body)
}

// pricingHTML 定价页正文（套餐卡片由前端 fetch /api/plans 动态渲染；样式统一品牌蓝）。
const pricingHTML = `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>定价 - 能言</title>
<style>
:root{--brand:#2b3ee8;--brand-hover:#4a5cf0;--brand-active:#1c2bd0;--brand-light:#e7ebff;--text:#1a2233;--text-2:#5a6478;--border:#e3e6ef;--bg:#f4f6fa}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',sans-serif;color:var(--text);background:var(--bg);line-height:1.75;font-size:15px}
.header{background:linear-gradient(135deg,#2b3ee8 0%,#1c2bd0 100%);color:#fff;padding:0 24px;display:flex;justify-content:space-between;align-items:center;height:60px;position:sticky;top:0;z-index:20}
.header .brand{font-size:18px;font-weight:700;letter-spacing:.3px}
.header a{color:#fff;text-decoration:none;margin-left:18px;font-size:14px}
.header a:hover{opacity:.85}
.wrap{max-width:920px;margin:32px auto;padding:0 20px}
.card{background:#fff;border-radius:12px;padding:32px 36px;box-shadow:0 2px 12px rgba(26,35,126,.06);border:1px solid var(--border)}
h1{font-size:24px;font-weight:700;margin-bottom:6px;color:var(--brand)}
h2{font-size:18px;font-weight:600;margin:26px 0 10px;padding-left:11px;border-left:4px solid var(--brand);color:var(--text);line-height:1.4}
p{margin:9px 0;color:#2b3145}
a{color:var(--brand);text-decoration:none}
a:hover{text-decoration:underline}
.footer{text-align:center;color:var(--text-2);font-size:13px;padding:28px;line-height:2}
.footer a{color:var(--text-2);margin:0 6px}
.footer a:hover{color:var(--brand)}
.note{background:var(--brand-light);border-left:4px solid var(--brand);padding:12px 16px;border-radius:8px;font-size:14px;color:var(--text-2);margin:16px 0}
#plansBox{display:flex;flex-wrap:wrap;gap:14px;margin:14px 0}
.plan{border:1px solid var(--border);border-radius:12px;padding:18px 20px;min-width:240px;flex:1 1 240px;background:#fff;transition:.2s}
.plan:hover{border-color:var(--brand);box-shadow:0 4px 16px rgba(43,62,232,.12)}
.plan b{font-size:16px;color:var(--text)}
.plan .meta{color:var(--text-2);font-size:13px;margin:6px 0}
.plan .price{color:var(--brand-active);font-weight:700;font-size:18px}
.plan .tag2{display:inline-block;margin-top:6px;font-size:12px;color:var(--brand-active);background:var(--brand-light);border-radius:999px;padding:2px 12px}
</style></head><body>
<div class="header"><div class="brand">🌐 能言 LangCross</div><div><a href="/pricing">定价 Pricing</a><a href="/docs/terms">用户协议 Terms</a><a href="/docs/sla">SLA</a><a href="/docs/privacy">隐私协议 Privacy</a><a href="/admin" style="background:#fff;color:var(--brand);padding:7px 16px;border-radius:8px;font-weight:600">管理后台</a></div></div>
<div class="wrap">
<div class="card">
<h1>定价 Pricing</h1>
<p>新用户注册即送免费体验句数；按套餐订阅或按 token 计量。充值 / 用量明细请在<a href="/admin">管理后台</a>查看。</p>
<h2>商业套餐 Plans</h2>
<div id="plansBox"><p>加载中…</p></div>
<div class="note">💡 套餐标注的句数为<b>单语言句数</b>：一次翻译译入 N 种语言将按 N 倍消耗；按实际用量从余额折算，具体以用量明细为准。</div>
<h2>常见问题 FAQ</h2>
<p><b>Q：如何计费？</b> 按每次翻译任务的实际消耗计费，专业校对模式包含知识库匹配与多轮质量保障环节。</p>
<p><b>Q：额度用完后怎么办？</b> 可订阅付费包或购买增量包，到账后立即恢复；也可联系管理员充值。</p>
<p><b>Q：支持哪些支付方式？</b> 支持微信 / 支付宝在线支付（对接中），静态二维码扫码 + 人工确认，当前可使用线下转账 + 管理员充值。</p>
</div></div>
<div class="footer">© 2026 能言 LangCross · 翻译平台 · <a href="/docs/terms">用户协议</a> · <a href="/docs/privacy">隐私协议</a></div>
<script>
fetch('/api/plans').then(r=>r.json()).then(d=>{
  const types={free:'免费体验',paid:'付费包',increment:'增量包'};
  const cards=(d.plans||[]).map(p=>{
    return '<div class="plan"><b>'+p.name+'</b><div class="meta">'+p.sentences+' 句 · '+(types[p.ptype]||p.ptype)+'</div>'+
      '<div class="price">¥'+p.price_money+'</div><div class="meta">/ '+(p.duration_days||'—')+' 天</div>'+
      (p.ptype==='free' ? '<span class="tag2">注册即送</span>' : '<span class="tag2">订阅后生效</span>')+
      '</div>';
  }).join('');
  document.getElementById('plansBox').innerHTML=cards || '<p>暂无上架套餐，请联系管理员</p>';
}).catch(()=>{document.getElementById('plansBox').innerHTML='<p>套餐加载失败</p>'});
</script>
</body></html>`

// termsBody 用户协议正文（中英双语，面向翻译平台）。
const termsBody = `
<p><b>能言 LangCross 用户协议</b>（以下简称「本协议」）</p>
<p>生效日期：2026 年 1 月 1 日</p>
<p>能言（LangCross，以下简称「本平台」或「我们」）是一个面向个人与企业的<b>翻译平台</b>，提供文本/文件翻译、多语知识库、术语管理与团队协作等 SaaS 能力。在使用本平台前，请您（以下简称「用户」）仔细阅读并充分理解本协议。您注册、登录或使用本平台任一功能，即视为已阅读并同意接受本协议全部条款。</p>

<h2>1. 协议的接受与变更</h2>
<p>1.1 您点击「同意」或实际使用本平台服务，即与本平台成立服务关系，本协议对双方均具有法律约束力。</p>
<p>1.2 我们可能根据法律法规变化或产品升级不时修订本协议，修订后的协议将在本页面及站内公告公示。若您继续使用服务，视为接受修订；若您不同意，应停止使用本平台。</p>

<h2>2. 服务说明</h2>
<p>2.1 本平台作为<b>翻译平台</b>，提供的核心能力包括但不限于：多语种文本与文件翻译、机器与人工结合的译文校对、多语知识库与术语库管理、翻译记忆、批量任务与团队协作等。</p>
<p>2.2 翻译结果由人工智能模型及/或人工服务生成，仅供您参考，不构成任何专业法律、医疗、财务或其他专业意见。</p>

<h2>3. 账户注册与安全</h2>
<p>3.1 您须提供真实、准确、完整的注册信息，并及时更新。</p>
<p>3.2 您应妥善保管账户名与密码，凡通过您账户发生的操作均视为您本人行为，由此产生的后果由您承担。</p>
<p>3.3 如发生账号盗用或安全漏洞，请立即通知我们；在接到通知前已产生的损失，除非法律另有规定，本平台不承担责任。</p>

<h2>4. 使用规则与禁止行为</h2>
<p>4.1 您承诺合法使用本平台，不得利用本平台从事以下行为：</p>
<p>（一）违反国家法律、法规或公序良俗；<br>（二）侵犯他人知识产权、商业秘密或个人隐私；<br>（三）上传含有病毒、木马或恶意代码的文件；<br>（四）对本平台进行反向工程、破解、滥用或超出授权范围的批量调用；<br>（五）利用本平台生成或传播违法、侵权或虚假信息。</p>

<h2>5. 知识产权</h2>
<p>5.1 本平台及其算法、软件、界面、文档、品牌标识等知识产权归本平台运营方所有，除本协议明示授权外，您不得复制、传播或用于商业目的。</p>
<p>5.2 您在使用本平台过程中提交的内容（含原文、术语、知识库）的知识产权归您或相关权利人所有；您授予本平台为提供服务所必需的存储、处理与展示权利。</p>

<h2>6. 费用与支付</h2>
<p>6.1 本平台采用免费体验与付费订阅/按量计费相结合的商业模式，具体套餐、价格与计费规则以站内公示及管理后台为准。</p>
<p>6.2 预付费余额（token/句数）不设有效期，除法律另有规定或本协议明确约定外不予退款；赠送额度不含退款权益。</p>

<h2>7. 服务变更、暂停与终止</h2>
<p>7.1 我们可能因系统维护、产品迭代或安全需要，调整、暂停或终止部分功能，重大变更将提前通知。</p>
<p>7.2 若您违反本协议，我们有权暂停或终止您的账户，并保留追究责任的权利。</p>

<h2>8. 免责声明</h2>
<p>8.1 本平台按「现状」提供服务，我们尽合理努力保障服务可用与结果质量，但不对翻译结果的准确性、适用性作出明示或默示担保。</p>
<p>8.2 因不可抗力、第三方服务故障或您自身操作导致的损失，本平台不承担责任。</p>

<h2>9. 责任限制</h2>
<p>在适用法律允许的最大范围内，本平台对因使用或无法使用本服务所导致的间接、附带、特殊或后果性损害不承担责任；累计赔偿责任不超过您在前 12 个月内实际支付的费用总额。</p>

<h2>10. 隐私与数据保护</h2>
<p>本平台高度重视用户数据与隐私，相关收集、使用、共享与删除规则详见《<a href="/docs/privacy">隐私协议</a>》，该协议为本协议不可分割的组成部分。</p>

<h2>11. 适用法律与争议解决</h2>
<p>本协议的订立、解释与争议解决适用中华人民共和国法律；双方因本协议产生争议的，应友好协商解决，协商不成的，提交本平台运营方所在地有管辖权的人民法院诉讼解决。</p>

<h2>12. 联系我们</h2>
<p>如您对本协议有任何疑问，可通过站内工单或管理后台公布的联系方式与我们联系。</p>

<hr>
<h2>User Agreement</h2>
<p><b>Effective date: January 1, 2026</b></p>
<p>LangCross ("the Platform", "we", or "us") is a <b>translation platform</b> offering multilingual text/file translation, multilingual knowledge bases, terminology management, and team collaboration as a SaaS. By registering, logging in, or using any feature of the Platform, you ("User") agree to be bound by this Agreement.</p>

<h2>1. Acceptance and Changes</h2>
<p>1.1 Clicking "Agree" or actually using the service creates a binding agreement between you and the Platform.</p>
<p>1.2 We may revise this Agreement from time to time. Continued use after notice constitutes acceptance of the revised terms.</p>

<h2>2. Service Description</h2>
<p>2.1 As a <b>translation platform</b>, LangCross provides multilingual text/file translation, AI- and human-assisted proofreading, knowledge-base and glossary management, translation memory, batch tasks, and team collaboration.</p>
<p>2.2 Translations are generated by AI models and/or human services for reference only and do not constitute professional advice.</p>

<h2>3. Account Registration and Security</h2>
<p>3.1 You must provide accurate registration information and keep it up to date.</p>
<p>3.2 You are responsible for safeguarding your credentials; activities under your account are deemed your own.</p>

<h2>4. Acceptable Use</h2>
<p>You agree not to: (a) violate laws or public morality; (b) infringe IP, trade secrets, or privacy; (c) upload malicious files; (d) reverse-engineer, abuse, or over-call the Platform; (e) generate or disseminate unlawful or infringing content.</p>

<h2>5. Intellectual Property</h2>
<p>5.1 The Platform's software, algorithms, interfaces, documentation, and trademarks are owned by the operator. No rights are granted except as expressly stated.</p>
<p>5.2 You retain rights to content you submit; you grant the Platform a license to store, process, and display it solely to provide the service.</p>

<h2>6. Fees and Payment</h2>
<p>6.1 The Platform combines free trials with paid subscriptions/usage-based billing; plans and prices are as published.</p>
<p>6.2 Prepaid balance has no expiry and is non-refundable except as required by law.</p>

<h2>7. Changes, Suspension, and Termination</h2>
<p>7.1 We may modify, suspend, or terminate features for maintenance, iteration, or security, with notice for material changes.</p>
<p>7.2 We may suspend or terminate accounts that breach this Agreement.</p>

<h2>8. Disclaimers</h2>
<p>The Platform is provided "as is"; we do not warrant the accuracy or fitness of translations.</p>

<h2>9. Limitation of Liability</h2>
<p>To the maximum extent permitted by law, the Platform is not liable for indirect or consequential damages; aggregate liability is capped at fees paid in the preceding 12 months.</p>

<h2>10. Privacy</h2>
<p>Data collection, use, and deletion are governed by our <a href="/docs/privacy">Privacy Policy</a>, which is incorporated into this Agreement.</p>

<h2>11. Governing Law</h2>
<p>This Agreement is governed by the laws of the People's Republic of China; disputes are resolved in the courts of the operator's location.</p>

<h2>12. Contact</h2>
<p>For questions, contact us via in-app tickets or the contact information published in the admin console.</p>`

// slaBodyZh / slaBodyEn 服务等级协议正文（拆分为中英文两段，由页面语言切换器控制显示）。
const slaBodyZh = `
<h2>可用性目标</h2>
<p>本服务月度可用性目标为 99.5%（不含计划维护与不可抗力）。可用性 =（当月总分钟数 − 不可用分钟数）÷ 当月总分钟数。</p>
<h2>补偿机制</h2>
<p>连续不可用超过 4 小时，或月度可用性低于目标，可申请等价 token 补偿，次月生效。</p>
<h2>响应时限</h2>
<p>工单支持响应时限：P1 严重故障 2 小时内响应，P2 一般问题 8 小时内响应，P3 咨询 24 小时内响应。</p>`

const slaBodyEn = `
<h2>Availability Target</h2>
<p>Monthly availability target is 99.5% (excluding scheduled maintenance and force majeure). Availability = (total minutes − unavailable minutes) ÷ total minutes.</p>
<h2>Credits</h2>
<p>Credits in token equivalents may be claimed if availability falls below target or an outage exceeds 4 consecutive hours.</p>
<h2>Support Response</h2>
<p>P1 critical: 2h; P2 general: 8h; P3 inquiry: 24h.</p>`

// privacyBody 隐私协议正文（中英双语，面向翻译平台）。
const privacyBody = `
<p><b>能言 LangCross 隐私协议</b></p>
<p>生效日期：2026 年 1 月 1 日</p>
<p>能言（LangCross，以下简称「本平台」）作为<b>翻译平台</b>的运营方，深知个人信息保护的重要性。本隐私协议说明我们如何收集、使用、共享、存储与保护您的信息，以及您所享有的权利。</p>

<h2>1. 我们收集的信息</h2>
<p>1.1 <b>账户信息</b>：注册时提供的用户名、邮箱、联系方式及组织信息。</p>
<p>1.2 <b>翻译内容</b>：您上传的待翻译文本、文件、术语与知识库内容，仅用于向您提供翻译及相关服务。</p>
<p>1.3 <b>用量与日志</b>：计费所需的任务记录、调用日志、设备与网络信息（IP、浏览器、访问时间）。</p>

<h2>2. 信息的使用</h2>
<p>2.1 提供、维护与改进翻译服务；</p>
<p>2.2 计费、对账与客服支持；</p>
<p>2.3 安全风控、欺诈防范与产品体验优化。</p>

<h2>3. 信息的共享</h2>
<p>3.1 我们<b>不出售</b>您的个人信息。</p>
<p>3.2 为实现翻译能力，您的翻译内容会调用大模型供应商 API 进行处理，供应商仅在委托范围内按数据处理协议处理，且不得将其用于自身模型训练。</p>
<p>3.3 在法律要求或为保护本平台及用户合法权益的必要范围内，我们可能向监管或司法机关提供相关信息。</p>

<h2>4. 数据存储与跨境传输</h2>
<p>4.1 您的数据存储于本平台运营方控制的服务器，传输采用 TLS 加密。</p>
<p>4.2 如涉及跨境传输，我们将依据适用法律采取合规措施（如标准合同条款）并征得必要同意。</p>

<h2>5. 数据保留与删除</h2>
<p>5.1 我们在实现服务目的所必需的最短时间内保留您的信息；法律法规要求更长期限的，从其规定。</p>
<p>5.2 您可随时在管理后台导出全部数据，或申请删除账户及关联数据，我们将在合理期限内完成删除（法律另行要求的除外）。</p>

<h2>6. 您的权利</h2>
<p>您对个人信息的查询、更正、导出、删除及撤回同意等权利，可通过管理后台或联系我们行使。</p>

<h2>7. 安全措施</h2>
<p>我们采取传输加密（TLS）、存储加密、访问审计日志与最小权限原则等技术与管理措施保护您的信息。</p>

<h2>8. 未成年人保护</h2>
<p>本平台主要面向成年人及企业用户；我们不直接收集未成年人个人信息，如误收集将及时删除。</p>

<h2>9. Cookie 与同类技术</h2>
<p>我们可能使用 Cookie 维持登录态与统计访问，您可通过浏览器设置管理 Cookie。</p>

<h2>10. 联系我们</h2>
<p>如您对个人信息处理有任何疑问或投诉，可通过管理后台公布的联系方式或隐私专用邮箱与我们联系。</p>

<h2>11. 协议更新</h2>
<p>本隐私协议将随产品与法规变化更新，更新后以本页面公示版本为准。</p>

<hr>
<h2>Privacy Policy</h2>
<p><b>Effective date: January 1, 2026</b></p>
<p>LangCross ("the Platform") operates a <b>translation platform</b>. This Privacy Policy explains how we collect, use, share, store, and protect your information.</p>

<h2>1. Information We Collect</h2>
<p>(a) Account info: username, email, contact, and organization details; (b) Translation content: texts, files, glossaries, and knowledge bases you upload, used solely to provide the service; (c) Usage and logs: task records, API logs, and device/network data (IP, browser, timestamps).</p>

<h2>2. How We Use</h2>
<p>To provide, maintain, and improve translation services; for billing and support; and for security, fraud prevention, and experience optimization.</p>

<h2>3. Sharing</h2>
<p>We do <b>not sell</b> personal information. Translation content is processed via LLM providers under data-processing agreements and is not used for their training. We may disclose information when required by law or to protect rights.</p>

<h2>4. Storage and Cross-Border Transfer</h2>
<p>Data is stored on our controlled servers with TLS in transit. Cross-border transfers follow applicable law (e.g., standard contractual clauses) with necessary consent.</p>

<h2>5. Retention and Deletion</h2>
<p>We retain data only as long as necessary; you may export or erase your data anytime via the admin console.</p>

<h2>6. Your Rights</h2>
<p>You may access, correct, export, delete, or withdraw consent regarding your personal data.</p>

<h2>7. Security</h2>
<p>We apply TLS, encryption at rest, audit logging, and least-privilege access.</p>

<h2>8. Minors</h2>
<p>The Platform is intended for adults and enterprises; we do not knowingly collect minors' data.</p>

<h2>9. Cookies</h2>
<p>We use cookies to maintain sessions and analytics; you can manage them in your browser.</p>

<h2>10. Contact</h2>
<p>For privacy questions, contact us via the admin console or our privacy email.</p>

<h2>11. Updates</h2>
<p>This Policy is updated as products and laws evolve; the latest version is published here.</p>`

// 本文件不直接使用这些包，移除多余导入。
