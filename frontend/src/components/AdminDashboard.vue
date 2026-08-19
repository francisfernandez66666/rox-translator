<template>
  <div class="ad-wrap">
    <aside class="ad-side">
      <div class="ad-brand">🏢 管理后台</div>
      <div v-if="isSuper && tenantList" class="ad-tenant-switch">
        <label>当前租户</label>
        <select :value="activeTenantId" class="ad-input" @change="switchTenant(Number(($event.target as HTMLSelectElement).value))">
          <option v-for="t in tenantList" :key="t.id" :value="t.id">[{{ t.id }}] {{ t.name }} ({{ t.code }})</option>
        </select>
      </div>
      <nav class="ad-nav">
        <button v-for="t in tabs" :key="t.key" :class="['ad-nav-item', active === t.key ? 'on' : '']" @click="active = t.key">
          {{ t.label }}
        </button>
      </nav>
      <div class="ad-side-foot">
        <div class="ad-user">{{ user?.display_name || user?.username }} ({{ roleName(user?.role) }})</div>
        <a href="/" class="ad-back">← 翻译工作台</a>
        <button class="ad-logout" @click="logout">退出登录</button>
      </div>
    </aside>

    <main class="ad-main">
      <div v-if="!isAdmin" class="ad-forbid">当前账号无管理权限，请使用管理员账号登录。</div>

      <!-- ===== 系统看板 ===== -->
      <section v-if="active === 'dash'" class="ad-section">
        <h2>系统看板</h2>
        <button class="ad-btn" @click="loadDash">刷新</button>
        <button class="ad-btn ad-btn-green" style="margin-left: 8px" @click="exportAuditCSV">导出审计 CSV</button>
        <button class="ad-btn" style="margin-left: 8px" @click="openMetrics">📊 Prometheus 指标</button>
        <div v-if="health" class="ad-cards">
          <div class="ad-card"><b>{{ health.kb_entries }}</b><span>知识库条目</span></div>
          <div class="ad-card"><b>{{ health.balance?.balance }}</b><span>租户余额 (token)</span></div>
          <div class="ad-card"><b>{{ health.flow_steps_enabled }}/{{ health.flow_steps_total }}</b><span>流程步骤启用</span></div>
          <div class="ad-card"><b>{{ health.usage ? Object.keys(health.usage).length : 0 }}</b><span>用量类型</span></div>
          <div class="ad-card"><b>{{ health.breaker_open ? '🔴 熔断' : '🟢 正常' }}</b><span>主模型状态</span></div>
          <div class="ad-card"><b>{{ health.llm_error_rate }}</b><span>LLM 错误率</span></div>
        </div>
        <div v-if="audit && audit.length" class="ad-audit">
          <h3>最近审计日志</h3>
          <table class="ad-table">
            <thead><tr><th>时间</th><th>操作</th><th>资源</th><th>详情</th><th>变更轨迹</th></tr></thead>
            <tbody>
              <tr v-for="l in audit" :key="l.id">
                <td>{{ fmtTime(l.created_at) }}</td><td>{{ l.action }}</td><td>{{ l.resource }}</td><td>{{ l.detail }}</td>
                <td class="ad-td">
                  <span v-if="l.before_val && l.after_val" class="ad-diff">旧 {{ shortJSON(l.before_val) }} → 新 {{ shortJSON(l.after_val) }}</span>
                  <span v-else>—</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- ===== 监控告警 ===== -->
      <section v-if="active === 'alerts'" class="ad-section">
        <h2>监控告警</h2>
        <div class="ad-row">
          <button class="ad-btn" @click="loadAlerts">刷新</button>
          <select v-model="alertStatus" class="ad-input" @change="loadAlerts">
            <option value="">全部</option><option value="open">未处理</option><option value="resolved">已解决</option>
          </select>
        </div>
        <table class="ad-table">
          <thead><tr><th>级别</th><th>类型</th><th>租户</th><th>内容</th><th>状态</th><th>时间</th><th></th></tr></thead>
          <tbody>
            <tr v-for="a in alerts" :key="a.id">
              <td>{{ a.level }}</td><td>{{ a.kind }}</td><td>#{{ a.tenant_id }}</td><td>{{ a.message }}</td>
              <td>{{ a.status === 'open' ? '未处理' : '已解决' }}</td><td>{{ fmtTime(a.created_at) }}</td>
              <td><button v-if="a.status === 'open'" class="ad-btn-sm" @click="resolveAlert(a)">关闭</button></td>
            </tr>
            <tr v-if="!alerts.length"><td colspan="7" style="text-align:center;color:#999">暂无告警</td></tr>
          </tbody>
        </table>
      </section>

      <!-- ===== 账户管理 ===== -->
      <section v-if="active === 'users'" class="ad-section">
        <h2>账户管理</h2>
        <div class="ad-row">
          <input v-model="uForm.username" placeholder="用户名" class="ad-input" />
          <input v-model="uForm.password" placeholder="初始密码" class="ad-input" />
          <input v-model="uForm.display_name" placeholder="显示名称" class="ad-input" />
          <select v-model="uForm.role" class="ad-input">
            <option v-for="r in roleOptions" :key="r" :value="r">{{ r }}</option>
          </select>
          <select v-if="isSuper" v-model="uForm.tenant_id" class="ad-input">
            <option v-for="t in tenantList" :key="t.id" :value="t.id">租户 {{ t.id }} ({{ t.code }})</option>
          </select>
          <button class="ad-btn" @click="createUser">创建</button>
        </div>
        <table class="ad-table">
          <thead><tr><th>ID</th><th>用户名</th><th>名称</th><th>角色</th><th>状态</th><th>最近登录</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="u in users" :key="u.id">
              <td>{{ u.id }}</td><td>{{ u.username }}</td>
              <td><input :value="u.display_name" class="ad-mini" @change="editUser(u, 'display_name', ($event.target as HTMLInputElement).value)" /></td>
              <td>
                <select :value="u.role" class="ad-mini" @change="editUser(u, 'role', ($event.target as HTMLSelectElement).value)">
                  <option v-for="r in roleOptions" :key="r" :value="r">{{ r }}</option>
                </select>
              </td>
              <td>{{ u.status }}</td>
              <td>{{ fmtTime(u.last_login_at) }}</td>
              <td class="ad-td">
                <button class="ad-btn-sm" @click="resetPwd(u)">重置密码</button>
                <button class="ad-btn-sm ad-btn-red" @click="toggleUser(u)">{{ u.status === 'active' ? '停用' : '启用' }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <!-- ===== 租户管理 ===== -->
      <section v-if="active === 'tenants'" class="ad-section">
        <h2>租户管理</h2>
        <div class="ad-row">
          <input v-model="tForm.code" placeholder="编码 (如 bmw)" class="ad-input" />
          <input v-model="tForm.name" placeholder="名称" class="ad-input" />
          <input v-model="tForm.expires" type="date" class="ad-input" />
          <button class="ad-btn" @click="createTenant">开通租户</button>
        </div>
        <div class="ad-row">
          <input v-model="tForm.adminUser" placeholder="租户管理员用户名" class="ad-input" />
          <input v-model="tForm.adminPass" type="password" placeholder="初始密码" class="ad-input" />
        </div>
        <div class="ad-hint">权限 JSON（langs 允许语言，max_daily_chars 日字符上限）</div>
        <input v-model="tForm.permissions" placeholder='{"langs":["de","en"],"max_daily_chars":100000}' class="ad-input ad-wide" />
        <table class="ad-table">
          <thead><tr><th>ID</th><th>编码</th><th>名称</th><th>状态</th><th>有效期</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="t in tenants" :key="t.id">
              <td>{{ t.id }}</td><td>{{ t.code }}</td><td>{{ t.name }}</td>
              <td>{{ statusLabel(t.status) }}</td>
              <td>{{ t.expires_at || '永久' }}</td>
              <td class="ad-td">
                <button class="ad-btn-sm" @click="toggleTenant(t)">{{ t.status === 'active' ? '停用' : '启用' }}</button>
                <button v-if="t.id !== 1" class="ad-btn-sm ad-btn-red" @click="removeTenant(t)">删除</button>
                <button class="ad-btn-sm" @click="chargeTenant(t)">充值</button>
                <button class="ad-btn-sm" @click="exportTenant(t)">导出数据</button>
                <button v-if="t.id !== 1" class="ad-btn-sm ad-btn-red" @click="eraseTenant(t)">清除数据</button>
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <!-- ===== 行业管理（行业包） ===== -->
      <section v-if="active === 'kb'" class="ad-section">
        <h2>行业管理（KB 包）</h2>
        <div class="ad-row">
          <input v-model="pForm.code" placeholder="编码 (如 auto)" class="ad-input" />
          <input v-model="pForm.name" placeholder="名称 (如 汽车行业包)" class="ad-input" />
          <select v-model="pForm.pack_type" class="ad-input">
            <option value="tenant">企业包</option><option value="industry">行业包</option><option value="locale">语言文化包</option>
          </select>
          <button class="ad-btn" @click="createPackage">创建包</button>
        </div>
        <div class="ad-hint">包内条目: 层 L1术语 / L2 TM / L3 安全句 / L4 碎片；源=中文，目标语言代码 (en/de/fr…)</div>
        <div v-for="p in packages" :key="p.id" class="ad-pkg">
          <div class="ad-pkg-head">
            <b>[{{ p.pack_type }}] {{ p.name }}</b>
            <span class="ad-pkg-role">{{ p.role }}</span>
            <button class="ad-btn-sm ad-btn-red" @click="removePackage(p)">删除包</button>
            <button class="ad-btn-sm" @click="loadEntries(p)">查看条目 ({{ entryCount(p.id) }})</button>
          </div>
          <div v-if="selectedPkg === p.id" class="ad-pkg-entries">
            <div class="ad-row">
              <input v-model="eForm.source_text" placeholder="中文源句" class="ad-input" />
              <select v-model="eForm.layer" class="ad-input">
                <option :value="1">L1 术语</option><option :value="2">L2 TM</option><option :value="3">L3 安全句</option><option :value="4">L4 碎片</option>
              </select>
              <input v-model="eForm.target_lang" placeholder="目标语言 (en)" class="ad-input ad-mini-w" />
              <input v-model="eForm.target_text" placeholder="译文" class="ad-input" />
              <button class="ad-btn" @click="addEntry(p.id)">添加</button>
            </div>
            <details class="ad-bulk">
              <summary>批量导入（每行一条：中文|语言|译文，可多行）</summary>
              <textarea v-model="bulkText" class="ad-input ad-wide ad-textarea" placeholder="制动系统|en|Brake system&#10;点火开关|en|Ignition switch" />
              <button class="ad-btn" @click="bulkImport(p.id)">批量导入</button>
              <span class="ad-hint" style="margin-left: 10px">{{ bulkMsg }}</span>
            </details>
            <table class="ad-table">
              <thead><tr><th>ID</th><th>层</th><th>源句</th><th>语言</th><th>译文</th><th></th></tr></thead>
              <tbody>
                <tr v-for="e in entries" :key="e.id">
                  <td>{{ e.id }}</td><td>L{{ e.layer }}</td><td>{{ e.source_text }}</td>
                  <td>{{ e.target_lang }}</td><td>{{ e.target_text }}</td>
                  <td><button class="ad-btn-sm ad-btn-red" @click="removeEntry(e)">删</button></td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </section>

      <!-- ===== 流程引擎设置 ===== -->
      <section v-if="active === 'flow'" class="ad-section">
        <h2>流程引擎设置</h2>
        <p class="ad-hint">工单翻译流程步骤启停。关闭某步则跳过（审批关闭后工单翻译完成后直接完成）。</p>
        <div v-for="st in flowSteps" :key="st.key" class="ad-flow-row">
          <label class="ad-switch">
            <input type="checkbox" v-model="st.enable" />
            <span></span>
          </label>
          <span class="ad-flow-name">{{ st.name }}</span>
          <code class="ad-flow-key">{{ st.key }}</code>
        </div>
        <button class="ad-btn" @click="saveFlow">保存流程配置</button>
      </section>

      <!-- ===== 模型配置 ===== -->
      <section v-if="active === 'models'" class="ad-section">
        <h2>模型配置</h2>
        <label class="ad-label">API 地址</label>
        <input v-model="mForm.api_base" class="ad-input ad-wide" />
        <label class="ad-label">API Key（留空不修改）</label>
        <input v-model="mForm.api_key" class="ad-input ad-wide" />
        <label class="ad-label">模型名</label>
        <input v-model="mForm.model" class="ad-input ad-wide" />
        <button class="ad-btn" @click="saveModels">保存模型</button>
      </section>

      <!-- ===== 模型路由策略 ===== -->
      <section v-if="active === 'routes'" class="ad-section">
        <h2>模型路由策略</h2>
        <div class="ad-hint">多供应商路由：按权重选主模型，失败后按权重降序依次降级。留空=使用单供应商 Online* 配置。</div>
        <div v-for="(r, i) in routeForm" :key="i" class="ad-route-row">
          <input v-model="r.provider" placeholder="供应商 (siliconflow/bigmodel…)" class="ad-input" />
          <input v-model="r.api_base" placeholder="API Base (…/v1)" class="ad-input" style="flex:1" />
          <input v-model="r.api_key" placeholder="API Key" class="ad-input" style="flex:1" />
          <input v-model="r.model" placeholder="模型名" class="ad-input" />
          <input v-model.number="r.weight" type="number" placeholder="权重" class="ad-input ad-mini-w" />
          <button class="ad-btn-sm ad-btn-red" @click="routeForm.splice(i, 1)">删</button>
        </div>
        <div class="ad-row">
          <button class="ad-btn" @click="routeForm.push({ provider: '', api_base: '', api_key: '', model: '', weight: 0 })">+ 添加路由</button>
          <button class="ad-btn ad-btn-green" @click="saveRoutes">保存路由</button>
        </div>
        <p class="ad-hint">当前生效：{{ routeForm.length ? routeForm.length + ' 条路由（主模型 ' + (routeForm.find(r => r.weight > 0)?.model || routeForm[0]?.model || '—') + '）' : '未配置，使用平台默认单供应商' }}</p>
      </section>

      <!-- ===== 策略参数 ===== -->
      <section v-if="active === 'policy'" class="ad-section">
        <h2>策略参数</h2>
        <label class="ad-label">相似度阈值 high_sim</label>
        <input v-model.number="pForm2.high_sim" type="number" step="0.01" class="ad-input" />
        <label class="ad-label">相似度阈值 med_sim</label>
        <input v-model.number="pForm2.med_sim" type="number" step="0.01" class="ad-input" />
        <label class="ad-label">evals 通过阈值</label>
        <input v-model.number="pForm2.evals_pass_threshold" type="number" class="ad-input" />
        <button class="ad-btn" @click="savePolicy">保存策略</button>
      </section>

      <!-- ===== evals 看板 ===== -->
      <section v-if="active === 'evals'" class="ad-section">
        <h2>evals 评估看板</h2>
        <button class="ad-btn" @click="loadEvals">刷新</button>
        <table class="ad-table">
          <thead><tr><th>ID</th><th>任务</th><th>语言</th><th>总分</th><th>状态</th><th>时间</th><th>译文</th></tr></thead>
          <tbody>
            <tr v-for="r in evals" :key="r.id">
              <td>{{ r.id }}</td><td>{{ r.task_type }}</td><td>{{ r.model }}</td>
              <td>{{ r.total?.toFixed ? r.total.toFixed(1) : r.total }}</td>
              <td>{{ r.status }}</td><td>{{ fmtTime(r.created_at) }}</td>
              <td class="ad-ellipsis">{{ r.output_text }}</td>
            </tr>
          </tbody>
        </table>
      </section>

      <!-- ===== 工单工作台 ===== -->
      <section v-if="active === 'tickets'" class="ad-section">
        <h2>工单工作台</h2>
        <div class="ad-row">
          <input v-model="tkForm.title" placeholder="标题" class="ad-input" />
          <textarea v-model="tkForm.source_text" placeholder="源文本（中文）" class="ad-input ad-textarea" />
          <input v-model="tkForm.target_langs" placeholder="目标语言，逗号分隔 (en,de)" class="ad-input" />
          <button class="ad-btn" @click="createTicket">创建工单</button>
        </div>
        <table class="ad-table">
          <thead><tr><th>单号</th><th>标题</th><th>状态</th><th>源文本</th><th>目标</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="t in tickets" :key="t.id">
              <td>{{ t.ticket_no }}</td><td>{{ t.title }}</td><td>{{ t.status }}</td>
              <td class="ad-ellipsis">{{ t.source_text }}</td><td>{{ t.target_langs }}</td>
              <td class="ad-td">
                <button class="ad-btn-sm" @click="runTicket(t)">运行流程</button>
                <button class="ad-btn-sm" @click="openTicket(t)">详情</button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="ticketDetail" class="ad-ticket-detail">
          <h3>工单 {{ ticketDetail.ticket_no }} 详情</h3>
          <pre>{{ prettyJSON(ticketDetail.final_result) }}</pre>
          <div v-if="ticketDetail.states && ticketDetail.states.length" class="ad-states">
            <div v-for="st in ticketDetail.states" :key="st.id" class="ad-state">
              {{ st.step }} → {{ st.status }} <span v-if="st.payload">{{ st.payload }}</span>
            </div>
          </div>
        </div>
      </section>

      <!-- ===== 审批台 ===== -->
      <section v-if="active === 'approval'" class="ad-section">
        <h2>审批台</h2>
        <button class="ad-btn" @click="loadApproval">刷新</button>
        <div v-for="t in approvalTickets" :key="t.id" class="ad-approval">
          <div class="ad-approval-head">
            <b>{{ t.ticket_no }} — {{ t.title }}</b>
            <span class="ad-pkg-role">{{ t.status }}</span>
          </div>
          <p class="ad-approval-src">{{ t.source_text }}</p>
          <textarea :value="t.final_result" class="ad-input ad-textarea" readonly />
          <div class="ad-row">
            <button class="ad-btn ad-btn-green" @click="doApprove(t, 'approve')">批准</button>
            <input v-model="t._reason" placeholder="驳回原因" class="ad-input" />
            <input v-model="t._suggestion" placeholder="改进建议" class="ad-input" />
            <button class="ad-btn ad-btn-red" @click="doApprove(t, 'reject')">驳回</button>
          </div>
        </div>
        <div v-if="!approvalTickets.length" class="ad-hint">暂无待审批工单</div>
      </section>

      <!-- ===== 用量看板 ===== -->
      <section v-if="active === 'usage'" class="ad-section">
        <h2>用量看板</h2>
        <button class="ad-btn" @click="loadUsage">刷新</button>
        <div v-if="usageData" class="ad-cards">
          <div class="ad-card"><b>{{ usageData.balance?.balance ?? '—' }}</b><span>当前余额 (token)</span></div>
          <div class="ad-card"><b>{{ usageData.total }}</b><span>累计用量 (token)</span></div>
          <div class="ad-card"><b>{{ usageData.provider_count }}</b><span>使用供应商数</span></div>
        </div>

        <div v-if="usageData && trendItems.length" class="ad-chart-card">
          <h3>近 7 日用量趋势</h3>
          <div class="ad-chart-bars">
            <div v-for="t in trendItems" :key="t.key" class="ad-chart-col" :title="`${t.key}: ${t.val} token`">
              <div class="ad-chart-bar" :style="{ height: barHeight(t.val, trendMax) }"></div>
              <span class="ad-chart-label">{{ t.key.slice(5) }}</span>
            </div>
          </div>
          <p class="ad-hint" style="text-align:center">峰值 {{ trendMax }} token</p>
        </div>

        <div v-if="usageData" class="ad-chart-grid">
          <div class="ad-chart-card">
            <h3>按任务类型</h3>
            <div v-for="item in usageItems" :key="item.key" class="ad-hbar">
              <span class="ad-hbar-label">{{ item.key }}</span>
              <div class="ad-hbar-track"><div class="ad-hbar-fill" :style="{ width: barHeight(item.val, usageMax) }"></div></div>
              <span class="ad-hbar-val">{{ item.val }}</span>
            </div>
            <p v-if="!usageItems.length" class="ad-hint">暂无用量</p>
          </div>

          <div class="ad-chart-card">
            <h3>按供应商/模型（成本核算）</h3>
            <div v-for="item in providerItems" :key="item.key" class="ad-hbar">
              <span class="ad-hbar-label" :title="item.key">{{ item.short }}</span>
              <div class="ad-hbar-track"><div class="ad-hbar-fill fill-2" :style="{ width: barHeight(item.val, providerMax) }"></div></div>
              <span class="ad-hbar-val">{{ item.val }}</span>
            </div>
            <p v-if="!providerItems.length" class="ad-hint">暂无供应商数据</p>
          </div>
        </div>

        <div class="ad-chart-card">
          <h3>用量明细</h3>
          <table class="ad-table">
            <thead><tr><th>时间</th><th>类型</th><th>供应商</th><th>模型</th><th>量</th><th>单价</th><th>消耗</th></tr></thead>
            <tbody>
              <tr v-for="l in usageData?.ledger || []" :key="l.id">
                <td>{{ fmtTime(l.created_at) }}</td><td>{{ l.task_type }}</td><td>{{ l.provider || '—' }}</td>
                <td>{{ l.model || '—' }}</td><td>{{ l.quantity }}</td><td>{{ l.unit_price }}</td><td>{{ l.cost }}</td>
              </tr>
              <tr v-if="!((usageData?.ledger || []).length)"><td colspan="7" style="color:#999">暂无明细</td></tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- ===== 充值/余额 ===== -->
      <section v-if="active === 'orders'" class="ad-section">
        <h2>充值管理</h2>
        <div class="ad-row">
          <input v-model="oForm.tokens" type="number" placeholder="token 数量" class="ad-input" />
          <input v-model.number="oForm.money" type="number" step="0.01" placeholder="金额 (元)" class="ad-input" />
          <button class="ad-btn" @click="createOrder">创建充值订单</button>
        </div>
        <table class="ad-table">
          <thead><tr><th>单号</th><th>tokens</th><th>金额</th><th>状态</th><th>时间</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="o in orders" :key="o.id">
              <td>{{ o.order_no }}</td><td>{{ o.amount_tokens }}</td><td>{{ o.amount_money }} 元</td>
              <td>{{ o.status }}</td><td>{{ fmtTime(o.created_at) }}</td>
              <td class="ad-td">
                <button v-if="o.status === 'pending'" class="ad-btn-sm ad-btn-green" @click="payOrder(o)">确认收款</button>
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <!-- ===== API Key ===== -->
      <section v-if="active === 'apikeys'" class="ad-section">
        <h2>开放 API Key</h2>
        <div class="ad-row">
          <a class="ad-btn" href="#" @click.prevent="openDocs">📄 查看 API 文档</a>
        </div>
        <div class="ad-row">
          <input v-model="kForm.name" placeholder="Key 名称" class="ad-input" />
          <select v-model="kForm.perms" class="ad-input">
            <option value="translate">translate</option><option value="kb">kb</option><option value="all">all</option>
          </select>
          <button class="ad-btn" @click="createKey">签发 Key</button>
        </div>
        <div v-if="newKey" class="ad-newkey">新 Key（仅显示一次）：<code>{{ newKey }}</code></div>
        <table class="ad-table">
          <thead><tr><th>ID</th><th>前缀</th><th>名称</th><th>权限</th><th>状态</th><th>调用次数</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="k in keys" :key="k.id">
              <td>{{ k.id }}</td><td>{{ k.key_prefix }}…</td><td>{{ k.name }}</td><td>{{ k.perms }}</td>
              <td>{{ k.status }}</td><td>{{ k.call_count }}</td>
              <td class="ad-td">
                <button class="ad-btn-sm" @click="toggleKey(k)">{{ k.status === 'active' ? '停用' : '启用' }}</button>
                <button class="ad-btn-sm" @click="rotateKey(k)">轮换</button>
                <button class="ad-btn-sm ad-btn-red" @click="deleteKey(k)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <!-- ===== 计费配置 ===== -->
      <section v-if="active === 'billing'" class="ad-section">
        <h2>计费配置</h2>
        <div class="ad-hint">开启强制计费后，所有翻译请求按用量扣减租户余额，余额不足将被拒绝服务。</div>
        <label class="ad-switch" style="margin-right: 14px">
          <input type="checkbox" v-model="billingEnforced" @change="saveBillingConfig" />
          <span></span>
        </label>
        <span :style="{ color: billingEnforced ? '#2e7d32' : '#888', fontWeight: 600 }">{{ billingEnforced ? '强制计费已开启' : '强制计费已关闭（仅记录用量不扣费）' }}</span>
      </section>

      <!-- ===== 配额设置 ===== -->
      <section v-if="active === 'quota'" class="ad-section">
        <h2>配额设置（当前租户）</h2>
        <div class="ad-hint">QPS 每秒请求上限、并发同时翻译任务数、每日累计字符上限（0=不限）。</div>
        <div class="ad-row">
          <input v-model.number="quotaForm.qps" type="number" placeholder="QPS" class="ad-input ad-mini-w" />
          <input v-model.number="quotaForm.concurrent" type="number" placeholder="并发" class="ad-input ad-mini-w" />
          <input v-model.number="quotaForm.max_daily_chars" type="number" placeholder="每日字符上限" class="ad-input" />
          <button class="ad-btn" @click="saveQuota">保存配额</button>
        </div>
        <p class="ad-hint">当前：QPS {{ quotaForm.qps }}，并发 {{ quotaForm.concurrent }}，每日上限 {{ quotaForm.max_daily_chars }} 字符</p>
      </section>

      <!-- ===== 发票管理 ===== -->
      <section v-if="active === 'invoices'" class="ad-section">
        <h2>发票管理</h2>
        <div class="ad-hint">仅可对本租户已支付订单开票。开票后金额不可修改。</div>
        <table class="ad-table">
          <thead><tr><th>发票号</th><th>关联订单</th><th>抬头</th><th>税号</th><th>金额(元)</th><th>开票时间</th></tr></thead>
          <tbody>
            <tr v-for="inv in invoices" :key="inv.id">
              <td>{{ inv.invoice_no }}</td><td>{{ inv.order_no }}</td><td>{{ inv.title }}</td><td>{{ inv.tax_no }}</td>
              <td>{{ inv.amount_money }}</td><td>{{ fmtTime(inv.created_at) }}</td>
            </tr>
            <tr v-if="!invoices.length"><td colspan="6" style="text-align:center;color:#999">暂无发票</td></tr>
          </tbody>
        </table>
      </section>

      <!-- ===== 邀请码管理 ===== -->
      <section v-if="active === 'invites'" class="ad-section">
        <h2>邀请码管理</h2>
        <div class="ad-hint">绑定租户的邀请码：受邀用户加入该租户（普通用户）；未绑定租户的邀请码：受邀用户自助新建租户。</div>
        <div class="ad-row">
          <input v-model="invForm.code" placeholder="邀请码" class="ad-input" />
          <select v-model.number="invForm.tenant_id" class="ad-input">
            <option :value="0">(新建租户)</option>
            <option v-for="t in tenantList" :key="t.id" :value="t.id">{{ t.name }} (#{{ t.id }})</option>
          </select>
          <button class="ad-btn" @click="createInvite">生成邀请码</button>
        </div>
        <table class="ad-table">
          <thead><tr><th>邀请码</th><th>绑定租户</th><th>状态</th><th>使用者</th><th>创建时间</th><th>使用时间</th></tr></thead>
          <tbody>
            <tr v-for="c in invites" :key="c.id">
              <td>{{ c.code }}</td><td>{{ c.tenant_id > 0 ? '#' + c.tenant_id : '新建租户' }}</td>
              <td>{{ c.used ? '已使用' : '未使用' }}</td><td>{{ c.used_by || '—' }}</td>
              <td>{{ fmtTime(c.created_at) }}</td><td>{{ fmtTime(c.used_at) }}</td>
            </tr>
            <tr v-if="!invites.length"><td colspan="6" style="text-align:center;color:#999">暂无邀请码</td></tr>
          </tbody>
        </table>
      </section>
    </main>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import {
  setAuthToken, getAuthToken, setActiveTenantId, getActiveTenantId, API_BASE,
  adminUsers, adminUserCreate, adminUserUpdate, adminUserResetPassword,
  tenantList, tenantCreate, tenantSetStatus, tenantDelete, tenantExport, tenantErase, adminOrderCreate, adminOrderPay,
  kbPackages, kbPackageCreate, kbPackageDelete, kbEntries, kbEntryAdd, kbEntryDelete, kbEntriesImport,
  flowConfig, flowSave,
  adminModels, adminModelsSave, modelRoutes, modelRoutesSave, adminPolicy, adminPolicySave,
  systemHealth, systemAudit, systemAlerts, alertResolve, evalsList,
  ticketList, ticketCreate, ticketRun, ticketDetail,
  approveList, approveAction,
  billingBalance, billingUsage, billingOrders,
  apiKeys, apiKeyCreate, apiKeyStatus, apiKeyDelete, apiKeyRotate, openAPIDocsUrl,
  billingConfig, billingConfigSave, billingQuota, billingQuotaSave,
  billingInvoices, billingInvoiceCreate,
  inviteCodes, inviteCodeCreate,
  type AuthUser, type Ticket, type FlowStepItem,
} from '@/api'

const emit = defineEmits<{ logout: [] }>()
const props = defineProps<{ user: AuthUser | null }>()

const active = ref('dash')
// 后台面板清单：min 为所需角色等级（2=租户管理员，3=超级管理员）
const allTabs = [
  { key: 'dash', label: '📊 系统看板', min: 2 },
  { key: 'alerts', label: '🚨 监控告警', min: 2 },
  { key: 'users', label: '👤 账户管理', min: 2 },
  { key: 'tenants', label: '🏢 租户管理', min: 3 },
  { key: 'kb', label: '📚 行业管理', min: 2 },
  { key: 'flow', label: '⚙️ 流程引擎', min: 2 },
  { key: 'models', label: '🧠 模型配置', min: 2 },
  { key: 'routes', label: '🔀 模型路由', min: 3 },
  { key: 'policy', label: '🎯 策略参数', min: 2 },
  { key: 'evals', label: '🧪 evals 看板', min: 2 },
  { key: 'tickets', label: '📝 工单工作台', min: 2 },
  { key: 'approval', label: '✅ 审批台', min: 2 },
  { key: 'orders', label: '💰 充值管理', min: 3 },
  { key: 'usage', label: '📈 用量看板', min: 2 },
  { key: 'apikeys', label: '🔑 API Key', min: 2 },
  { key: 'billing', label: '⚖️ 计费配置', min: 3 },
  { key: 'quota', label: '🚦 配额设置', min: 2 },
  { key: 'invoices', label: '🧾 发票管理', min: 2 },
  { key: 'invites', label: '🎟️ 邀请码', min: 3 },
]
// 当前用户角色等级，并据此过滤可见面板
const myLevel = computed(() => roleLevel(props.user?.role))
const tabs = computed(() => allTabs.filter(t => myLevel.value >= t.min))

const isAdmin = computed(() => myLevel.value >= 2)
const isSuper = computed(() => myLevel.value >= 3)
const user = computed(() => props.user)

// ---- 通用 ----
// 角色等级：普通用户(1) < 租户管理员(2) < 超级管理员(3)，兼容旧值 approver/admin
function roleLevel(r?: string): number {
  if (r === 'super_admin' || r === 'admin') return 3
  if (r === 'tenant_admin' || r === 'approver') return 2
  return 1
}
// 角色中文名（后台侧边栏展示）
function roleName(r?: string) {
  if (r === 'super_admin' || r === 'admin') return '超级管理员'
  if (r === 'tenant_admin' || r === 'approver') return '租户管理员'
  return '普通用户'
}
// 可选角色：超管可分配全部三级；租户管理员只能分配普通用户/租户管理员（防提权）
const roleOptions = computed(() =>
  isSuper.value ? ['user', 'tenant_admin', 'super_admin'] : ['user', 'tenant_admin']
)
function fmtTime(s?: string) { return s ? s.replace('T', ' ').slice(0, 19) : '—' }
function statusLabel(s: string) { return s === 'active' ? '启用' : s === 'disabled' ? '停用' : '已过期' }
function prettyJSON(s?: string) {
  if (!s) return ''
  try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s }
}
// 压缩 JSON 摘要（去掉引号与冒号，便于表格内阅读）
function shortJSON(s: string) {
  try { return JSON.stringify(JSON.parse(s)) } catch { return s }
}
function logout() {
  setAuthToken('')
  emit('logout')
}

// ---- 看板 ----
const health = ref<any>(null)
const audit = ref<any[]>([])
async function loadDash() {
  const h = await systemHealth()
  if (h.success) health.value = (h as any).health
  const a = await systemAudit()
  if (a.success) audit.value = (a as any).logs
}
async function loadAudit() { await loadDash() }

// ---- 监控告警 ----
const alerts = ref<any[]>([])
const alertStatus = ref('')
async function loadAlerts() {
  const r = await systemAlerts(alertStatus.value || undefined)
  if (r.success) alerts.value = (r as any).alerts || []
}
async function resolveAlert(a: any) {
  await alertResolve(a.id)
  await loadAlerts()
}

// 审计 CSV 导出：直接以当前租户身份下载（后端返回 UTF-8 BOM）
function exportAuditCSV() {
  const url = `${API_BASE}/api/system/audit?export=csv`
  const xhr = new XMLHttpRequest()
  xhr.open('GET', url, true)
  if (getAuthToken()) xhr.setRequestHeader('Authorization', `Bearer ${getAuthToken()}`)
  if (getActiveTenantId() > 0) xhr.setRequestHeader('X-Tenant-ID', String(getActiveTenantId()))
  xhr.responseType = 'blob'
  xhr.onload = () => {
    if (xhr.status !== 200) { alert('导出失败'); return }
    const a = document.createElement('a')
    a.href = URL.createObjectURL(xhr.response)
    a.download = `audit_${new Date().toISOString().slice(0, 10)}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
  }
  xhr.send()
}

// ---- 用户 ----
const users = ref<any[]>([])
const uForm = ref({ username: '', password: '', display_name: '', role: 'user', tenant_id: 1 })
async function loadUsers() {
  const r = await adminUsers()
  if (r.success) users.value = (r as any).users
}
async function createUser() {
  if (!uForm.value.username || !uForm.value.password) { alert('用户名和密码必填'); return }
  const r = await adminUserCreate({ ...uForm.value })
  if (!r.success) { alert(r.message); return }
  uForm.value = { username: '', password: '', display_name: '', role: 'user', tenant_id: activeTenantId || 1 }
  await loadUsers()
}
async function editUser(u: any, field: string, val: string) {
  const data = { display_name: u.display_name, role: u.role, status: u.status }
  ;(data as any)[field] = val
  const r = await adminUserUpdate(u.id, data)
  if (!r.success) alert(r.message)
  await loadUsers()
}
async function toggleUser(u: any) {
  const r = await adminUserUpdate(u.id, { display_name: u.display_name, role: u.role, status: u.status === 'active' ? 'disabled' : 'active' })
  if (!r.success) alert(r.message)
  await loadUsers()
}
async function resetPwd(u: any) {
  const pwd = prompt(`为 ${u.username} 设置新密码：`)
  if (!pwd) return
  const r = await adminUserResetPassword(u.id, pwd)
  if (!r.success) alert(r.message)
}

// ---- 租户 ----
// 超管通过 X-Tenant-ID 切换生效租户；租户管理员固定在自身租户
const tenants = ref<any[]>([])
const activeTenantId = ref(getActiveTenantId() || 1)
const tenantList = computed(() => tenants.value)
const tForm = ref({ code: '', name: '', expires: '', permissions: '{}', adminUser: '', adminPass: '' })
async function loadTenants() {
  const r = await tenantList()
  if (r.success) tenants.value = r.tenants || []
  // 超管首次进入：默认生效第一个租户
  if (isSuper.value && activeTenantId.value === 1 && !localStorage.getItem('active_tenant_id')) {
    if (tenants.value.length) setActiveTenantId(tenants.value[0].id)
  }
  // 若已选租户被删除，回退到第一个可用租户
  if (!tenants.value.some(t => t.id === activeTenantId.value) && tenants.value.length) {
    setActiveTenantId(tenants.value[0].id)
  }
}
// 切换生效租户：更新全局 X-Tenant-ID 并重载各面板数据
function switchTenant(tid: number) {
  setActiveTenantId(tid)
  activeTenantId.value = tid
  Promise.all([loadDash(), loadAlerts(), loadUsers(), loadPackages(), loadFlow(), loadModels(), loadRoutes(), loadPolicy(), loadEvals(), loadTickets(), loadApproval(), loadOrders(), loadUsage(), loadKeys(), loadQuota(), loadInvoices()])
}
async function createTenant() {
  if (!tForm.value.code) { alert('租户编码必填'); return }
  const r = await tenantCreate({
    code: tForm.value.code, name: tForm.value.name,
    expires_at: tForm.value.expires, permissions: tForm.value.permissions,
    admin_user: tForm.value.adminUser, admin_pass: tForm.value.adminPass,
  })
  if (!r.success) { alert(r.message); return }
  tForm.value = { code: '', name: '', expires: '', permissions: '{}', adminUser: '', adminPass: '' }
  await loadTenants()
}
async function toggleTenant(t: any) {
  const r = await tenantSetStatus(t.id, t.status === 'active' ? 'disabled' : 'active')
  if (!r.success) alert(r.message)
  await loadTenants()
}
async function removeTenant(t: any) {
  if (!confirm(`确认删除租户「${t.name}」？其数据一并删除。`)) return
  const r = await tenantDelete(t.id)
  if (!r.success) alert(r.message)
  await loadTenants()
}
async function chargeTenant(t: any) {
  const tokens = prompt(`为「${t.name}」充值 token 数量：`)
  if (!tokens || Number(tokens) <= 0) return
  const r = await adminOrderCreate({ tenant_id: t.id, tokens: Number(tokens), money: 0 })
  if (!r.success) { alert(r.message); return }
  const o = (r as any).order
  await adminOrderPay(o.id)
  alert(`已充值 ${tokens} token`)
}

// 租户数据主权：导出 / GDPR 清除
async function exportTenant(t: any) {
  if (!confirm(`导出租户「${t.name}」全部数据（JSON）？`)) return
  const url = `${API_BASE}/api/tenant/export`
  const xhr = new XMLHttpRequest()
  xhr.open('POST', url, true)
  xhr.setRequestHeader('Content-Type', 'application/json')
  if (getAuthToken()) xhr.setRequestHeader('Authorization', `Bearer ${getAuthToken()}`)
  xhr.responseType = 'blob'
  xhr.onload = () => {
    if (xhr.status !== 200) { alert('导出失败'); return }
    const a = document.createElement('a')
    a.href = URL.createObjectURL(xhr.response)
    a.download = `tenant_${t.id}_${t.code}.json`
    a.click()
    URL.revokeObjectURL(a.href)
  }
  xhr.send(JSON.stringify({ id: t.id }))
}
async function eraseTenant(t: any) {
  if (!confirm(`确认清除租户「${t.name}」的全部业务数据？此操作不可恢复，请先导出备份。`)) return
  if (!confirm(`再次确认：${t.name} 的用户/订单/用量/审计/KB 将全部删除。`)) return
  const r = await tenantErase(t.id)
  if (!r.success) { alert(r.message); return }
  alert('租户业务数据已清除')
  await loadTenants()
}

// ---- 行业包 ----
const packages = ref<any[]>([])
const entries = ref<any[]>([])
const selectedPkg = ref<number | null>(null)
const pForm = ref({ code: '', name: '', pack_type: 'industry' })
const eForm = ref({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' })
const entriesMap = ref<Record<number, number>>({})

function entryCount(id: number) { return entriesMap.value[id] || 0 }
async function loadPackages() {
  const r = await kbPackages()
  if (r.success) {
    packages.value = (r as any).packages
    entriesMap.value = {}
    for (const p of packages.value) {
      const e = await kbEntries(p.id)
      entriesMap.value[p.id] = (e as any).entries?.length || 0
    }
  }
}
async function createPackage() {
  if (!pForm.value.code || !pForm.value.name) { alert('编码和名称必填'); return }
  const r = await kbPackageCreate({ ...pForm.value, role: 'source' })
  if (!r.success) { alert(r.message); return }
  pForm.value = { code: '', name: '', pack_type: 'industry' }
  await loadPackages()
}
async function removePackage(p: any) {
  if (!confirm(`删除包「${p.name}」及全部条目？`)) return
  const r = await kbPackageDelete(p.id)
  if (!r.success) alert(r.message)
  await loadPackages()
}
async function loadEntries(p: any) {
  selectedPkg.value = selectedPkg.value === p.id ? null : p.id
  if (selectedPkg.value === p.id) {
    const r = await kbEntries(p.id)
    entries.value = (r as any).entries || []
  }
}
async function addEntry(pkgId: number) {
  if (!eForm.value.source_text) { alert('源句必填'); return }
  const r = await kbEntryAdd({ package_id: pkgId, ...eForm.value })
  if (!r.success) { alert(r.message); return }
  eForm.value = { source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' }
  await loadEntries({ id: pkgId })
  await loadPackages()
}
async function removeEntry(e: any) {
  const r = await kbEntryDelete(e.id)
  if (!r.success) alert(r.message)
  const p = packages.value.find((x) => x.id === selectedPkg.value)
  if (p) await loadEntries(p)
}

// 批量导入：每行 "中文|语言|译文"（可含层号：中文|en|译文|2）
const bulkText = ref('')
const bulkMsg = ref('')
async function bulkImport(pkgId: number) {
  const entries: any[] = []
  for (const line of bulkText.value.split('\n')) {
    const parts = line.split('|').map((s) => s.trim())
    if (parts.length < 3 || !parts[0]) continue
    entries.push({
      source_text: parts[0],
      target_lang: parts[1],
      target_text: parts[2],
      layer: parts.length >= 4 && Number(parts[3]) ? Number(parts[3]) : 2,
    })
  }
  if (!entries.length) { alert('没有有效行（每行：中文|语言|译文）'); return }
  const r = await kbEntriesImport({ package_id: pkgId, entries })
  if (!r.success) { alert(r.message); return }
  bulkMsg.value = `✅ 导入 ${r.added} 条，跳过 ${r.skipped} 条`
  bulkText.value = ''
  await loadEntries({ id: pkgId })
  await loadPackages()
}

// ---- 流程引擎 ----
const flowSteps = ref<FlowStepItem[]>([])
async function loadFlow() {
  const r = await flowConfig()
  if (r.success) flowSteps.value = (r as any).steps
}
async function saveFlow() {
  const r = await flowSave(flowSteps.value)
  if (!r.success) { alert(r.message); return }
  alert('流程配置已保存')
}

// ---- 模型/策略 ----
const mForm = ref({ api_base: '', api_key: '', model: '' })
const pForm2 = ref({ high_sim: 0.9, med_sim: 0.75, evals_pass_threshold: 75 })
async function loadModels() {
  const r = await adminModels()
  if (r.success) mForm.value = (r as any).model
}
async function saveModels() {
  const r = await adminModelsSave(mForm.value)
  if (!r.success) { alert(r.message); return }
  alert('模型配置已保存')
}
async function loadPolicy() {
  const r = await adminPolicy()
  if (r.success) pForm2.value = (r as any).policy
}
async function savePolicy() {
  const r = await adminPolicySave(pForm2.value)
  if (!r.success) { alert(r.message); return }
  alert('策略已保存')
}

// ---- 模型路由 ----
const routeForm = ref<any[]>([])
async function loadRoutes() {
  const r = await modelRoutes()
  if (r.success) routeForm.value = (r as any).routes || []
}
async function saveRoutes() {
  const valid = routeForm.value.filter((rt: any) => rt.api_base && rt.model)
  const r = await modelRoutesSave({ routes: valid })
  if (!r.success) { alert(r.message); return }
  alert('路由已保存并生效')
  await loadRoutes()
}

// ---- evals ----
const evals = ref<any[]>([])
async function loadEvals() {
  const r = await evalsList()
  if (r.success) evals.value = (r as any).records
}

// ---- 工单 ----
const tickets = ref<Ticket[]>([])
const ticketDetail = ref<any>(null)
const tkForm = ref({ title: '', source_text: '', target_langs: 'en' })
async function loadTickets() {
  const r = await ticketList(false)
  if (r.success) tickets.value = r.tickets || []
}
async function createTicket() {
  if (!tkForm.value.source_text) { alert('源文本必填'); return }
  const r = await ticketCreate(tkForm.value)
  if (!r.success) { alert(r.message); return }
  tkForm.value = { title: '', source_text: '', target_langs: 'en' }
  await loadTickets()
}
async function runTicket(t: Ticket) {
  const r = await ticketRun(t.id)
  if (!r.success) { alert(r.message); return }
  await loadTickets()
  alert(`工单 ${t.ticket_no} 流程执行完成`)
}
async function openTicket(t: Ticket) {
  const r = await ticketDetail(t.id)
  if (r.success) ticketDetail.value = { ...r.ticket, states: r.states }
}

// ---- 审批 ----
const approvalTickets = ref<any[]>([])
async function loadApproval() {
  const r = await approveList()
  if (r.success) approvalTickets.value = (r as any).tickets || []
}
async function doApprove(t: any, action: 'approve' | 'reject') {
  const r = await approveAction(t.id, action, t._reason || '', t._suggestion || '', '')
  if (!r.success) { alert(r.message); return }
  t._reason = ''
  t._suggestion = ''
  await loadApproval()
  await loadTickets()
}

// ---- 订单/余额 ----
const orders = ref<any[]>([])
const usageData = ref<any>(null)
const oForm = ref({ tokens: 1000, money: 0 })
async function loadOrders() {
  const r = await billingOrders()
  if (r.success) orders.value = (r as any).orders || []
}
async function loadUsage() {
  const [u, b] = await Promise.all([billingUsage(), billingBalance()])
  if (u.success) {
    const du = u as any
    usageData.value = {
      ...du,
      balance: b.success ? (b as any).balance : null,
      provider_count: Object.keys(du.provider_usage || {}).length,
    }
  }
}

// ---- 用量图表数据（纯 CSS 条形图，零依赖） ----
const trendItems = computed(() => {
  const trend = usageData.value?.trend || {}
  const keys = Object.keys(trend).sort()
  return keys.map((k) => ({ key: k, val: Number(trend[k]) || 0 }))
})
const trendMax = computed(() => Math.max(1, ...trendItems.value.map((t) => t.val)))
const usageItems = computed(() => {
  const u = usageData.value?.usage || {}
  return Object.keys(u).map((k) => ({ key: k, val: Number(u[k]) || 0 }))
})
const usageMax = computed(() => Math.max(1, ...usageItems.value.map((t) => t.val)))
const providerItems = computed(() => {
  const p = usageData.value?.provider_usage || {}
  return Object.keys(p).map((k) => ({
    key: k,
    short: k.length > 28 ? k.slice(0, 26) + '…' : k,
    val: Number(p[k]) || 0,
  }))
})
const providerMax = computed(() => Math.max(1, ...providerItems.value.map((t) => t.val)))
// 条形高度/宽度：取 8% 下限保证可见，最高 100%
function barHeight(val: number, max: number) {
  if (!val || !max) return '8%'
  const pct = Math.max(8, Math.round((val / max) * 100))
  return pct + '%'
}
async function createOrder() {
  const r = await adminOrderCreate({ tenant_id: 1, tokens: Number(oForm.value.tokens), money: oForm.value.money })
  if (!r.success) { alert(r.message); return }
  await loadOrders()
}
async function payOrder(o: any) {
  const r = await adminOrderPay(o.id)
  if (!r.success) { alert(r.message); return }
  await loadOrders()
  await loadDash()
}

// ---- API Key ----
const keys = ref<any[]>([])
const newKey = ref('')
const kForm = ref({ name: '', perms: 'translate' })
async function loadKeys() {
  const r = await apiKeys()
  if (r.success) keys.value = (r as any).keys || []
}
async function createKey() {
  if (!kForm.value.name) { alert('名称必填'); return }
  const r = await apiKeyCreate(kForm.value)
  if (!r.success) { alert(r.message); return }
  newKey.value = (r as any).api_key || ''
  kForm.value = { name: '', perms: 'translate' }
  await loadKeys()
}
async function toggleKey(k: any) {
  await apiKeyStatus(k.id, k.status === 'active' ? 'disabled' : 'active')
  await loadKeys()
}
async function deleteKey(k: any) {
  if (!confirm('删除该 API Key？')) return
  await apiKeyDelete(k.id)
  await loadKeys()
}
async function rotateKey(k: any) {
  if (!confirm(`确认轮换「${k.name}」？旧 Key 将立即失效。`)) return
  const r = await apiKeyRotate(k.id)
  if (!r.success) { alert(r.message); return }
  newKey.value = (r as any).api_key || ''
  await loadKeys()
}
function openDocs() {
  window.open(openAPIDocsUrl(), '_blank')
}
function openMetrics() {
  window.open(`${API_BASE}/metrics`, '_blank')
}

// ---- 计费配置 ----
const billingEnforced = ref(false)
async function loadBillingConfig() {
  const r = await billingConfig()
  if (r.success) billingEnforced.value = (r as any).billing_enforced === true
}
async function saveBillingConfig() {
  const r = await billingConfigSave({ billing_enforced: billingEnforced.value })
  if (!r.success) { alert(r.message); billingEnforced.value = !billingEnforced.value }
}

// ---- 配额设置 ----
const quotaForm = ref({ qps: 10, concurrent: 3, max_daily_chars: 0 })
async function loadQuota() {
  const r = await billingQuota()
  if (r.success) {
    quotaForm.value = {
      qps: (r as any).qps || 10,
      concurrent: (r as any).concurrent || 3,
      max_daily_chars: (r as any).max_daily_chars || 0,
    }
  }
}
async function saveQuota() {
  const r = await billingQuotaSave({
    qps: Math.max(1, quotaForm.value.qps),
    concurrent: Math.max(1, quotaForm.value.concurrent),
    max_daily_chars: Math.max(0, quotaForm.value.max_daily_chars),
  })
  if (!r.success) { alert(r.message); return }
  alert('配额已保存')
  await loadQuota()
}

// ---- 发票 ----
const invoices = ref<any[]>([])
async function loadInvoices() {
  const r = await billingInvoices()
  if (r.success) invoices.value = (r as any).invoices || []
}

// ---- 邀请码 ----
const invites = ref<any[]>([])
const invForm = ref({ code: '', tenant_id: 0 })
async function loadInvites() {
  const r = await inviteCodes()
  if (r.success) invites.value = (r as any).codes || []
}
async function createInvite() {
  if (!invForm.value.code) { alert('邀请码必填'); return }
  const r = await inviteCodeCreate(invForm.value)
  if (!r.success) { alert(r.message); return }
  invForm.value = { code: '', tenant_id: 0 }
  await loadInvites()
}

onMounted(async () => {
  if (myLevel.value >= 3) await loadTenants()
  const jobs: Promise<any>[] = [loadDash(), loadAlerts(), loadUsers(), loadPackages(), loadFlow(), loadModels(), loadRoutes(), loadPolicy(), loadEvals(), loadTickets(), loadApproval(), loadOrders(), loadUsage(), loadKeys(), loadBillingConfig(), loadQuota(), loadInvoices(), loadInvites()]
  await Promise.all(jobs)
})
</script>

<style scoped>
.ad-wrap { display: flex; min-height: 100vh; background: #f5f7fb; }
.ad-side {
  width: 230px; background: #1a237e; color: #fff; display: flex; flex-direction: column;
  flex-shrink: 0; position: sticky; top: 0; height: 100vh; overflow: auto;
}
.ad-brand { padding: 20px 18px; font-size: 18px; font-weight: 700; border-bottom: 1px solid rgba(255,255,255,0.12); }
.ad-nav { flex: 1; padding: 12px 10px; }
.ad-nav-item {
  display: block; width: 100%; text-align: left; padding: 10px 14px; margin-bottom: 4px;
  background: none; border: none; border-radius: 8px; color: rgba(255,255,255,0.8);
  font-size: 14px; cursor: pointer;
}
.ad-nav-item:hover { background: rgba(255,255,255,0.08); }
.ad-nav-item.on { background: #3949ab; color: #fff; font-weight: 600; }
.ad-side-foot { padding: 14px 16px; border-top: 1px solid rgba(255,255,255,0.12); font-size: 13px; }
.ad-user { color: rgba(255,255,255,0.9); margin-bottom: 8px; }
.ad-back { color: rgba(255,255,255,0.7); text-decoration: none; display: block; margin-bottom: 8px; }
.ad-back:hover { color: #fff; }
.ad-logout {
  background: rgba(255,255,255,0.15); border: none; color: #fff; padding: 6px 14px;
  border-radius: 6px; cursor: pointer; font-size: 13px;
}
.ad-main { flex: 1; padding: 28px 32px; overflow: auto; }
.ad-section h2 { font-size: 20px; margin-bottom: 18px; color: #1a237e; }
.ad-row { display: flex; gap: 10px; margin-bottom: 12px; flex-wrap: wrap; align-items: center; }
.ad-input { padding: 9px 12px; border: 1px solid #dcdcdc; border-radius: 8px; font-size: 13px; }
.ad-wide { width: 100%; box-sizing: border-box; }
.ad-mini-w { width: 100px; }
.ad-textarea { width: 100%; min-height: 60px; resize: vertical; }
.ad-mini { padding: 6px 8px; border: 1px solid #ddd; border-radius: 6px; font-size: 12px; }
.ad-btn {
  padding: 9px 16px; border: none; border-radius: 8px; background: #1a237e; color: #fff;
  font-size: 13px; cursor: pointer;
}
.ad-btn-green { background: #2e7d32; }
.ad-btn-red { background: #c62828; }
.ad-btn-sm { padding: 4px 10px; font-size: 12px; border: none; border-radius: 6px; background: #e8eaf6; color: #1a237e; cursor: pointer; margin-right: 4px; }
.ad-btn-red.ad-btn-sm { background: #fce4ec; color: #c62828; }
.ad-hint { font-size: 12px; color: #888; margin-bottom: 10px; }
.ad-label { display: block; font-size: 13px; color: #666; margin: 10px 0 4px; }
.ad-table { width: 100%; border-collapse: collapse; font-size: 13px; margin-top: 10px; }
.ad-table th, .ad-table td { border: 1px solid #eee; padding: 8px 10px; text-align: left; }
.ad-table th { background: #fafbfd; }
.ad-td { white-space: nowrap; }
.ad-ellipsis { max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ad-cards { display: flex; gap: 16px; margin-bottom: 20px; flex-wrap: wrap; }
.ad-card {
  background: #fff; border-radius: 12px; padding: 18px 22px; box-shadow: 0 2px 10px rgba(0,0,0,0.06);
  min-width: 150px;
}
.ad-card b { display: block; font-size: 24px; color: #1a237e; }
.ad-card span { font-size: 12px; color: #888; }
.ad-audit { margin-top: 20px; }
.ad-audit h3 { margin-bottom: 10px; font-size: 15px; }
.ad-pkg { background: #fff; border: 1px solid #e8e8e8; border-radius: 10px; margin-bottom: 12px; overflow: hidden; }
.ad-pkg-head { padding: 12px 16px; display: flex; align-items: center; gap: 10px; background: #fafbfd; }
.ad-pkg-role { padding: 2px 8px; background: #e8eaf6; border-radius: 12px; font-size: 11px; color: #1a237e; }
.ad-pkg-entries { padding: 14px; border-top: 1px solid #eee; }
.ad-flow-row { display: flex; align-items: center; gap: 14px; padding: 10px 0; border-bottom: 1px solid #f0f0f0; }
.ad-flow-name { font-size: 14px; }
.ad-flow-key { color: #999; font-size: 12px; }
.ad-switch { position: relative; display: inline-block; width: 42px; height: 22px; }
.ad-switch input { opacity: 0; width: 0; height: 0; }
.ad-switch span {
  position: absolute; cursor: pointer; top: 0; left: 0; right: 0; bottom: 0;
  background: #ccc; border-radius: 22px; transition: 0.3s;
}
.ad-switch span:before {
  content: ''; position: absolute; height: 16px; width: 16px; left: 3px; bottom: 3px;
  background: #fff; border-radius: 50%; transition: 0.3s;
}
.ad-switch input:checked + span { background: #1a237e; }
.ad-switch input:checked + span:before { transform: translateX(20px); }
.ad-ticket-detail { margin-top: 20px; background: #fff; border-radius: 10px; padding: 16px; border: 1px solid #eee; }
.ad-ticket-detail pre { font-size: 12px; white-space: pre-wrap; background: #f8f9fc; padding: 12px; border-radius: 8px; }
.ad-states { margin-top: 10px; }
.ad-state { font-size: 12px; padding: 4px 0; color: #555; }
.ad-approval { background: #fff; border: 1px solid #eee; border-radius: 10px; padding: 16px; margin-bottom: 14px; }
.ad-approval-head { display: flex; gap: 10px; align-items: center; margin-bottom: 8px; }
.ad-approval-src { font-size: 13px; color: #555; margin-bottom: 8px; }
.ad-newkey { background: #fff3e0; border: 1px solid #ffcc80; border-radius: 8px; padding: 10px 14px; margin-bottom: 12px; font-size: 13px; }
.ad-forbid { color: #c62828; font-size: 16px; padding: 40px; }
.ad-td .ad-btn-sm { margin-bottom: 2px; }
.ad-bulk { margin: 10px 0; background: #fafbfd; border: 1px solid #eee; border-radius: 8px; padding: 10px 12px; }
.ad-bulk summary { cursor: pointer; font-size: 13px; color: #1a237e; font-weight: 600; }
.ad-bulk textarea { margin: 8px 0; display: block; }
.ad-bulk .ad-btn { margin-right: 8px; }
.ad-route-row { display: flex; gap: 8px; align-items: center; margin-bottom: 8px; flex-wrap: wrap; }
.ad-diff { color: #2e7d32; font-size: 12px; background: #e8f5e9; padding: 2px 6px; border-radius: 4px; }
.ad-chart-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 16px; margin-top: 16px; }
.ad-chart-card { background: #fff; border: 1px solid #eee; border-radius: 10px; padding: 16px; margin-top: 16px; }
.ad-chart-card h3 { font-size: 14px; color: #1a237e; margin-bottom: 12px; }
.ad-chart-bars { display: flex; align-items: flex-end; gap: 8px; height: 140px; padding: 8px 4px 0; }
.ad-chart-col { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: flex-end; height: 100%; min-width: 0; }
.ad-chart-bar { width: 70%; max-width: 42px; min-height: 6px; background: linear-gradient(180deg, #3949ab, #1a237e); border-radius: 4px 4px 0 0; }
.ad-chart-label { font-size: 11px; color: #888; margin-top: 6px; }
.ad-hbar { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.ad-hbar-label { width: 120px; font-size: 12px; color: #555; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.ad-hbar-track { flex: 1; background: #f0f0f5; border-radius: 6px; height: 16px; overflow: hidden; }
.ad-hbar-fill { height: 100%; background: linear-gradient(90deg, #3949ab, #5c6bc0); border-radius: 6px; min-width: 6px; }
.ad-hbar-fill.fill-2 { background: linear-gradient(90deg, #2e7d32, #66bb6a); }
.ad-hbar-val { width: 64px; text-align: right; font-size: 12px; color: #333; }
</style>