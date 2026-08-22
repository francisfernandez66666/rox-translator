// ============ 本文件职责中文说明 ============
// 公开商业页面：无需登录即可访问的产品介绍与合规文档。
//   - /api/pricing 公开单价数据（读 rate_card，供前端定价页渲染）
//   - /pricing 定价页（token 单价表 + 常见问题）
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

// handlePublicPricingAPI 公开单价数据接口（读 rate_card）。
// 无鉴权，供前端定价页 /pricing 动态渲染 token 单价表。
func (s *Server) handlePublicPricingAPI(w http.ResponseWriter, r *http.Request) {
	cards, err := s.Store.ListRateCards()
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "读取单价失败"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "rate_cards": cards})
}

// handlePublicPricing 定价页：token 单价表 + 充值说明。
func (s *Server) handlePublicPricing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, pricingHTML)
}

// handlePublicTerms 服务条款页。
func (s *Server) handlePublicTerms(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, publicDocPage("服务条款 / Terms of Service", termsBody))
}

// handlePublicSLA 服务等级协议页。
func (s *Server) handlePublicSLA(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, publicDocPage("服务等级协议 / SLA", slaBody))
}

// handlePublicPrivacy 数据保护条款（DPA）页。
func (s *Server) handlePublicPrivacy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, publicDocPage("数据保护条款 / Data Processing Agreement", privacyBody))
}

// publicLayout 公共页面外壳（品牌导航 + 内容区 + 页脚）。
func publicLayout(title, body string) string {
	return `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + title + ` - 翻译助手</title>
<style>
:root{--green:#2e7d32;--dark:#202124;--gray:#5f6368}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',sans-serif;color:var(--dark);background:#f5f7fb;line-height:1.7}
.header{background:var(--green);color:#fff;padding:14px 24px;display:flex;justify-content:space-between;align-items:center}
.header .brand{font-size:18px;font-weight:600}
.header a{color:#fff;text-decoration:none;margin-left:18px;font-size:14px}
.wrap{max-width:900px;margin:32px auto;padding:0 20px}
.card{background:#fff;border-radius:14px;padding:28px 32px;box-shadow:0 2px 12px rgba(0,0,0,.06)}
h1{font-size:22px;margin-bottom:8px;color:var(--green)}
h2{font-size:17px;margin:22px 0 8px;color:var(--green)}
p{margin:8px 0;color:#333}
.footer{text-align:center;color:var(--gray);font-size:13px;padding:28px}
table{width:100%;border-collapse:collapse;margin:14px 0}
th,td{border:1px solid #e0e0e0;padding:10px 12px;text-align:left;font-size:14px}
th{background:#e8f5e9;color:var(--green)}
</style></head><body>
<div class="header"><div class="brand">🌐 翻译助手</div><div><a href="/pricing">定价 Pricing</a><a href="/docs/terms">条款 Terms</a><a href="/docs/sla">SLA</a><a href="/status">状态 Status</a><a href="/docs/privacy">隐私 Privacy</a></div></div>
<div class="wrap"><div class="card">` + body + `</div></div>
<div class="footer">© 2026 翻译助手 · ROX 多语翻译知识库 · <a href="/status">服务状态</a> · <a href="/admin">管理后台</a></div>
</body></html>`
}

// publicDocPage 合规文档页包装（条款/SLA/DPA 共用外壳）。
func publicDocPage(title, body string) string {
	return publicLayout(title, `<h1>`+title+`</h1>`+body)
}

// pricingHTML 定价页正文（单价表由前端 fetch /api/pricing 动态渲染）。
const pricingHTML = `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>定价 - 翻译助手</title>
<style>
:root{--green:#2e7d32;--dark:#202124;--gray:#5f6368}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',sans-serif;color:var(--dark);background:#f5f7fb;line-height:1.7}
.header{background:var(--green);color:#fff;padding:14px 24px;display:flex;justify-content:space-between;align-items:center}
.header .brand{font-size:18px;font-weight:600}
.header a{color:#fff;text-decoration:none;margin-left:18px;font-size:14px}
.wrap{max-width:900px;margin:32px auto;padding:0 20px}
.card{background:#fff;border-radius:14px;padding:28px 32px;box-shadow:0 2px 12px rgba(0,0,0,.06)}
h1{font-size:22px;margin-bottom:8px;color:var(--green)}
h2{font-size:17px;margin:22px 0 8px;color:var(--green)}
p{margin:8px 0;color:#333}
.footer{text-align:center;color:var(--gray);font-size:13px;padding:28px}
table{width:100%;border-collapse:collapse;margin:14px 0}
th,td{border:1px solid #e0e0e0;padding:10px 12px;text-align:left;font-size:14px}
th{background:#e8f5e9;color:var(--green)}
.note{background:#fff8e1;border-left:4px solid #f9a825;padding:12px 16px;border-radius:6px;font-size:14px;color:#5f6368;margin:16px 0}
</style></head><body>
<div class="header"><div class="brand">🌐 翻译助手</div><div><a href="/pricing">定价 Pricing</a><a href="/docs/terms">条款 Terms</a><a href="/docs/sla">SLA</a><a href="/docs/privacy">隐私 Privacy</a></div></div>
<div class="wrap">
<div class="card">
<h1>定价 Pricing</h1>
<p>新用户注册即送免费体验句数；按套餐订阅或按 token 计量。充值 / 用量明细请在<a href="/admin">管理后台</a>查看。</p>
<h2>商业套餐 Plans</h2>
<div id="plansBox"><p>加载中…</p></div>
<div class="note">💡 句数口径：每源句 × 每个目标语言 = 消耗 1 句。句数用尽后可订阅付费包或购买增量包。</div>
<h2>Token 单价表</h2>
<div id="rateTable"><p>加载中…</p></div>
<div class="note">💡 计费说明：高膨胀语种（如日语/韩语）按倍率 × 基础单价计费；具体扣费以每次翻译明细为准。</div>
<h2>常见问题</h2>
<p><b>Q：token 如何计量？</b> 文本翻译按源文本字符数计量，文件翻译按提取段数计量。</p>
<p><b>Q：句数用完后怎么办？</b> 可订阅付费包（包月 X 句）或购买增量包，到账后立即恢复。</p>
<p><b>Q：支持哪些支付方式？</b> 支持微信 / 支付宝在线支付（对接中），静态二维码扫码 + 人工确认，当前可使用线下转账 + 管理员充值。</p>
</div></div>
<div class="footer">© 2026 翻译助手 · ROX 多语翻译知识库</div>
<script>
fetch('/api/plans').then(r=>r.json()).then(d=>{
  const types={free:'免费体验',paid:'付费包',increment:'增量包'};
  const cards=(d.plans||[]).map(p=>{
    return '<div style="border:1px solid #e0e0e0;border-radius:12px;padding:16px;margin:10px 10px 10px 0;display:inline-block;min-width:220px;vertical-align:top">'+
      '<b>'+p.name+'</b><br><span style="color:#5f6368">'+p.sentences+' 句 · '+(types[p.ptype]||p.ptype)+'</span><br>'+
      '<span style="color:#2e7d32;font-weight:600">¥'+p.price_money+'</span> / '+(p.duration_days||'—')+'天<br>'+
      (p.ptype==='free' ? '<span style="color:#5f6368">注册即送</span>' : '<span style="color:#5f6368">订阅后生效</span>')+
      '</div>';
  }).join('');
  document.getElementById('plansBox').innerHTML=cards || '<p>暂无上架套餐，请联系管理员</p>';
}).catch(()=>{document.getElementById('plansBox').innerHTML='<p>套餐加载失败</p>'});
fetch('/api/pricing').then(r=>r.json()).then(d=>{
  const rows=(d.rate_cards||[]).map(c=>{
    const task={translate:'翻译',review:'审校',evals:'评测',gate:'校验'}[c.task_type]||c.task_type;
    const prov=c.provider==='*'?'全部供应商':c.provider;
    const lang=c.lang==='*'?'所有语言':c.lang;
    const price=c.multiplier>1? c.unit_price+' ×'+c.multiplier : c.unit_price;
    return '<tr><td>'+task+'</td><td>'+prov+'</td><td>'+lang+'</td><td>'+price+' token/单位</td></tr>';
  }).join('');
  document.getElementById('rateTable').innerHTML='<table><thead><tr><th>任务</th><th>供应商</th><th>语言</th><th>单价</th></tr></thead><tbody>'+(rows||'<tr><td colspan="4">暂无单价配置</td></tr>')+'</tbody></table>';
}).catch(()=>{document.getElementById('rateTable').innerHTML='<p>单价加载失败</p>'});
</script>
</body></html>`

// termsBody 服务条款正文（中英双语）。
const termsBody = `
<p><b>中文</b>｜<i>English below</i></p>
<h2>1. 服务说明</h2>
<p>翻译助手（以下简称「本服务」）提供多语言文本/文件翻译、知识库管理等 SaaS 能力。使用本服务即视为同意本条款。</p>
<h2>2. 账户与使用</h2>
<p>用户须妥善保管账号密码；不得利用本服务从事违法活动、侵犯他人知识产权或滥用系统资源。余额为预付 token，不设有效期，不可退款（法律另有规定除外）。</p>
<h2>3. 服务变更与终止</h2>
<p>我们可能基于产品迭代调整功能与定价，重大变更将提前通知。用户违反条款时，我们有权暂停或终止其账户。</p>
<h2>4. 免责与责任限制</h2>
<p>翻译结果由 AI 模型生成，可能存在错误，不构成任何专业意见。在适用法律允许范围内，我们对间接损失不承担责任。</p>
<hr>
<h2>1. Service Description</h2>
<p>Translation Assistant provides multilingual text/file translation and knowledge base management as a SaaS. By using the service you agree to these terms.</p>
<h2>2. Accounts and Use</h2>
<p>Users must safeguard credentials; prohibited uses include unlawful activity, IP infringement, and resource abuse. Prepaid tokens carry no expiry and are non-refundable except as required by law.</p>
<h2>3. Changes and Termination</h2>
<p>We may evolve features and pricing with advance notice. Accounts may be suspended or terminated for breach of these terms.</p>
<h2>4. Disclaimers</h2>
<p>AI-generated translations may contain errors and do not constitute professional advice. To the extent permitted by law, we are not liable for indirect damages.</p>`

// slaBody 服务等级协议正文。
const slaBody = `
<p><b>中文</b>｜<i>English below</i></p>
<h2>可用性目标</h2>
<p>本服务月度可用性目标为 99.5%（不含计划维护与不可抗力）。可用性 =（当月总分钟数 − 不可用分钟数）÷ 当月总分钟数。</p>
<h2>补偿机制</h2>
<p>连续不可用超过 4 小时，或月度可用性低于目标，可申请等价 token 补偿，次月生效。</p>
<h2>响应时限</h2>
<p>工单支持响应时限：P1 严重故障 2 小时内响应，P2 一般问题 8 小时内响应，P3 咨询 24 小时内响应。</p>
<hr>
<h2>Availability Target</h2>
<p>Monthly availability target is 99.5% (excluding scheduled maintenance and force majeure). Availability = (total minutes − unavailable minutes) ÷ total minutes.</p>
<h2>Credits</h2>
<p>Credits in token equivalents may be claimed if availability falls below target or an outage exceeds 4 consecutive hours.</p>
<h2>Support Response</h2>
<p>P1 critical: 2h; P2 general: 8h; P3 inquiry: 24h.</p>`

// privacyBody 数据保护条款（DPA）正文。
const privacyBody = `
<p><b>中文</b>｜<i>English below</i></p>
<h2>我们收集的数据</h2>
<p>账户信息（用户名/邮箱/联系方式）、翻译内容（仅用于提供翻译服务与质量改进）、用量记录（计费与报表）。</p>
<h2>数据使用与共享</h2>
<p>数据仅用于提供服务，不向第三方出售。翻译内容调用大模型供应商 API 处理，供应商仅按委托处理且不得用于训练。</p>
<h2>数据保留与删除</h2>
<p>租户可随时在管理后台导出全部数据（GDPR 导出）或申请清除（删除级联清理）。我们按法律要求保留必要日志。</p>
<h2>安全措施</h2>
<p>传输加密（TLS）、数据落盘加密、访问审计日志、最小权限原则。</p>
<hr>
<h2>Data We Collect</h2>
<p>Account info, translation content (to provide service), and usage records (billing/reporting).</p>
<h2>Use and Sharing</h2>
<p>Data is used solely to provide the service and is never sold. Translation content is processed via LLM providers under data-processing terms; it is not used for their model training.</p>
<h2>Retention and Deletion</h2>
<p>Tenants may export (GDPR export) or erase all data at any time. Necessary logs are retained per legal requirements.</p>
<h2>Security</h2>
<p>TLS in transit, encryption at rest, audit logging, least-privilege access.</p>`

// 本文件不直接使用这些包，移除多余导入。
