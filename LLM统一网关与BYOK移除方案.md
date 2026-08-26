# 技术方案 · LLM 统一平台网关与 BYOK 移除

> 版本 v1.1 ｜ 2026-08-26 ｜ 决策人：项目所有者 ｜ 状态：✅ 已全部落地
> **商业口径（定稿）**：所有 LLM 调用一律经平台网关，租户不得配置自有 Key；
> token 定价权 100% 归平台，计费按平台统一价执行。白皮书 §4.2「售价=上游成本×3」口径不变。
> 本方案同时修复浏览器插件 / Office 划译调用不存在端点的断链问题。

## 〇、实施记录（2026-08-26）

| 项 | 状态 | 落点 |
|---|---|---|
| engine BYOK 读取链删除 | ✅ | engine.go resolveModel 收敛两级、resolveTenantRoutes 删除、UsageModel 去 tenant 标识；顺带消除「降级链含主路由」「租户端点误配 Hunyuan 兜底」两处审计缺陷 |
| tenant 配置结构下线 | ✅ | ModelConfig/Route/GetModelConfig/SetModelConfig 删除（model_config 列保留，清理 SQL 见 §2.2） |
| 全局路由掩码回填修复 | ✅ | admin_models.go 改「位置对齐」回填，掩码串不再可能被当真实 Key 入库 |
| 同步翻译端点 | ✅ | POST /openapi/v1/translate（≤5000 字符、≤5 语言、复用 Key 鉴权/gateUsage/实费计量），插件与 Office taskpane 无需改码即恢复 |
| 前端清理 | ✅ | Models.vue 重写：BYOK 死区块删除，全局网关路由卡收归超管面板（修复了「超管反而无 UI 编辑 model_routes」的隐性缺口）；models.ts 死导出删除 |
| 文档同步 | ✅ | 部署指南 §八-C 替换为统一网关说明 |
| 回归 | ✅ | go build/vet/test 全绿（stagemodel 测试改写为全局路由用例）；vue-tsc + vite build 全绿 |


---

## 一、背景

- 现状引擎解析优先级：**租户 BYOK 多供应商路由 → 租户单模型 → 超管全局路由(model_routes) → 全局默认**
  （engine.go resolveModel :810-838、resolveTenantRoutes :840-848）。
- 计费侧 `chargeTokens` 对 BYOK 消耗照扣 token（×markup_multiplier）：租户自付上游费用又被扣平台
  token，商业逻辑矛盾；且定价权外溢，与本次定稿口径冲突。
- 实际使用面核查：租户级模型配置**无任何写入 API**（`SetModelConfig` 仅测试引用）；前端 Models 面板
  的 BYOK 区块 `v-if="!isSuper"` 但该面板仅超管可见 → 双重死 UI。移除零功能损失。
- 附带断链：extension/content.js:71 与 office.go:105 内嵌 taskpane 均调用 `POST /openapi/v1/translate`，
  该端点在现行路由表中不存在（server.go:336-342 仅 tasks 系列），请求落入 SPA 兜底返回 HTML，
  两代划译客户端实际全部失效。

## 二、改造内容

### 2.1 后端移除 BYOK 读取链（engine 层）

| 位置 | 处置 |
|---|---|
| engine.go `resolveModel` | 删除 `e.Ten.GetModelConfig` 整段分支；优先级收敛为「全局 ModelRoutes（按权重选主）→ 全局默认 Online*」 |
| engine.go `resolveTenantRoutes` | 函数整体删除 |
| engine.go `singleLang` 内 BYOK 降级链分支 | 删除 `resolveTenantRoutes` 分支；保留全局 `resolveRouteFallbacks`（该函数已正确排除主路由）。顺带修复审计发现的「BYOK 降级链含主路由自身」缺陷——删除后自然消失 |
| engine.go `UsageModel` :145-150 | 删除租户回退段（provider 不再出现 "tenant" 标识，成本核算口径统一为路由 provider/global） |
| api/billing_api.go `usageModel` :209-213 | 删除租户 GetModelConfig 回退 |

### 2.2 tenant 包配置结构下线

- 删除 `tenant.ModelConfig` / `tenant.Route` / `GetModelConfig` / `SetModelConfig`（tenant.go:241-282）。
- 数据库 `tenants.model_config` 列**保留不删**（避免迁移风险），代码不再读写；迁移说明：历史脏数据置 `'{}'`
  一次性清理语句写入部署手册（可选执行）。
- 受影响测试：engine/stagemodel_test.go 中 SetModelConfig 用例改写为纯全局路由用例。

### 2.3 admin_models.go 加固（超管侧保留并修缺陷）

- 全局路由能力**保留**（这是平台网关自身的多供应商调度，属定价权基础设施）。
- 修复掩码密钥回填脆弱性：`handleModelsSave` 掩码匹配由「api_base+model 双字段匹配」改为
  「合并列表按索引与旧路由对齐匹配」（同位置掩码=未修改则回填旧值），避免管理员只改 model 名导致
  掩码串被当真实 Key 入库、路由静默坏死。
- 注释明确：此面板为平台唯一 LLM 入口配置点，租户侧无任何模型配置入口。

### 2.4 新增同步翻译端点（修复插件/Office 断链）

`POST /openapi/v1/translate`（api_openapi_tasks.go 新增 handleOpenAPITranslateSync）：

```
请求头：Authorization: Bearer rk_xxx（开放 API Key）
请求体：{"text": "≤5000 字符", "target_langs": ["en"], "mode": "fast"|"pro"(缺省 fast)}
响应  ：{"success": true, "translations": {"en": "..."}, "tokens_used": N}
错误  ：401 无效Key / 403 权限不足 / 404 文本超限或为空 / 429 限流或配额 /
        402+insufficient_balance 余额不足（error_code 出参，与任务接口口径一致）
```

实现要点：
1. 鉴权复用 `authenticateAPIKey`（status=active + perms 含 translate/all + TouchAPIKey）。
2. 限额复用 Key 日配额检查（key_quota_exceeded 口径与 tasks 一致）。
3. 余额闸门：`Bill.Enabled() && Balance<=0 → insufficient_balance`（与创建任务同款）。
4. 直接调 `Engine.HandleText`（同步等待，划译场景文本短、延迟可控）；完成后
   `chargeTaskTokens` 同款实费扣减（复用现有 usage 收集器路径）。
5. server.go 注册路由于 `/openapi/v1/tasks` 组旁，注释标明「划译插件/Office taskpane 专用同步通道」。
6. extension/content.js 与 office.go taskpane **无需改动**（它们调用的正是该端点）。

### 2.5 前端清理（死 UI 移除）

- components/admin/Models.vue：删除「BYOK 单模型」「多供应商路由」两大区块及其状态/函数
  （本就不可达），面板聚焦五阶段模型 + 策略参数。全量中文注释随改随加。
- api/models.ts：删除死码导出 `modelRoutes/modelRoutesSave`。

### 2.6 文档同步

- 部署指南.md §八-C「租户 BYOK 模型配置」整节替换为「统一网关说明」：
  引擎解析优先级改为「超管全局路由(model_routes) → 全局默认」；分阶段模型仍超管维护。
- 商业化白皮书 FAQ Q2 表述不变（分阶段省钱口径仍成立）；§六安全承诺补一句
  「平台统一网关出站，租户无需也无法配置第三方模型凭据」。

## 三、兼容性与回滚

- 对外 OpenAPI 契约不变（新增端点为增量）；tasks 流程不受影响。
- 回滚：revert 本次提交即可；tenants.model_config 列数据仍在库中可恢复读取。

## 四、验收清单

1. `grep -rn "resolveTenantRoutes\|SetModelConfig\|GetModelConfig" backend-go/` 零业务代码命中（测试除外应同样为零）。
2. 超管 Models 面板保存多供应商路由 → 文本翻译走主路由，人为使主路由 429 → 按权重降级到备用路由成功返回。
3. 插件划词 → 气泡正常显示译文（不再「网络错误」）；Office taskpane 选区翻译同理。
4. 余额为 0 且 enforced=1 的租户 Key 调同步端点 → error_code=insufficient_balance。
5. `go build/vet/test` 与 `vue-tsc --noEmit && vite build` 全绿。
