# 技术方案 · KB 组织继承链与部门隔离改造

> 版本 v1.1 ｜ 2026-08-26 ｜ 决策人：项目所有者 ｜ 状态：✅ 已全部落地（见 §八 实施记录）
> 需求拍板：①部门间隔离（兄弟分支不可见）②祖先链继承（就近覆盖）③跨部门降级检索（精确=采用+打标；模糊/语义=仅例句参考）④租户级开关默认开 ⑤部门包包级 opt-out（share_cross_dept）
> 附带修复：审计遗留 #9（FetchRowTenant 行业码不校验越权）、#10（共享包向量检索漏召回）

---

## 一、目标语义（定稿）

用户发起翻译时，术语命中范围与优先序：

```
第0..N层 链内部门包   本部门(org_id=我) → parent_id 祖先逐级上溯 → 租户根
                      同名术语距离近者胜（本部门覆盖父部门覆盖顶层）
第T层    企业包       pack_type='tenant'，全租户可见
第H层    历史无主行   tm_segments.pack_id=0 的 TM 沉淀/审核入库行，按企业层对待
第I层    行业包       平台共享，注册行业匹配（不变）
第L层    语言文化包   平台共享（不变）
───────────────────────────────────────────────
回退层   跨部门精确   仅当：链内精确零命中
                       且租户策略 kb_cross_dept_fallback=开（默认开）
                       且目标包 share_cross_dept=1（包级开关，默认共享）
                     → 直接采用 + Mode 打标「🌐跨部门命中（来自X）」
回退层   跨部门模糊/语义 → 绝不直接采用；仅作例句注入 system 提示词供 LLM 参考
                       （例句池规则：链内候选优先填满既有 ≤5条/短句限制，名额不满才补跨部门）
```

- **隔离**：不在上述范围的本租户其他分支部门包，对精确/模糊/语义三条路径均不可见
- **未挂组织用户 / 匿名请求**：chain 为空 → 只见 T/H/I/L 层（安全默认）
- **平台上下文（tid≤0）**：维持现状跳过全部知识库

## 二、数据模型变更（幂等迁移）

```sql
-- kb_packages 补列（migrateColumns 追加）
ALTER TABLE kb_packages ADD COLUMN share_cross_dept INTEGER NOT NULL DEFAULT 1;
-- 组织归属索引（VisiblePackScope 查询加速）
CREATE INDEX IF NOT EXISTS idx_kb_packages_org ON kb_packages(tenant_id, org_id);
```

无其他表结构变更；tm_segments 不加列（组织过滤经 pack_id JOIN kb_packages 动态判定，单一事实源）。

## 三、核心类型与算法

### 3.1 PackScope（定义在 kb 包，store 组装，engine 消费）

```go
// kb/scope.go
type PackScope struct {
    TenantID        int64
    ChainPacks      map[int64]int     // 包ID → 祖先距离(0=本部门,1=父,...)
    SharedPackIDs   map[int64]bool    // 行业匹配+文化包（含宿主租户1的行）
    AllowCrossDept  bool              // 租户策略开关
    CrossDeptPacks  map[int64]string  // 跨部门候选包ID → 展示名（打标用；开关关时空表）
}
func (s *PackScope) Distance(packID int64) (int, bool)          // 链内距离
func (s *PackScope) Rank(packID int64, rowTenant int64) int     // 全局排序秩：
//   链内=distance(0..89)；企业/pack_id=0=100；行业=200；文化=300；跨部门=400
func (s *PackScope) InChain(packID int64) bool                  // 例句分层用
```

### 3.2 store.BuildPackScope(tid, chain []int64) (*kb.PackScope, error)

一次 SQL 装配：本租户全部包分类（department 按 org_id∈chain 记距离 / tenant 归企业层 / department∉chain 且 share_cross_dept=1 归跨部门集）；共享层复用现有 sharedFilterSQL 口径（行业码匹配）。chain 为空时 ChainPacks/CrossDeptPacks 皆空。

### 3.3 检索三路径改造

| 路径 | 改造 |
|---|---|
| FindExactScoped(zh, scope) | 两段式：段一 WHERE 限定「链内∪企业∪pack_id=0∪共享」取候选 LIMIT 16 → Go 侧按 Rank 排序取最优；段二仅当段一零命中且 AllowCrossDept：只查 CrossDeptPacks 集。旧 FindExact(zh,tid) 保留并内部委托 scope=nil 兼容 |
| FuzzyHitsScoped(...) | 单查询覆盖全可见域，返回 `(chainHits, crossHits []*Row)`；Go 侧沿用长度差过滤；调用方仅可对 chainHits 整句采用，crossHits 只进例句池 |
| ScopedSearch(vec,k,langs,scope) | 过滤器由「租户相等」改为「tenant 匹配 ∪ 行 ∈SharedPackIDs」（修 #10），白名单改按 Scope 三集合判定；返回 SearchResult 增加 InChain 字段 |

### 3.4 引擎接线（engine/text/file）

- ctx 新增 `WithUserOrg/UserOrgFromContext`；`userScope(ctx)` = BuildPackScope(tid, OrgAncestorIDs(tid,userOrg))，带请求级 memo
- translateOneInner 四段替换为 Scoped 版本；Mode 打标追加；例句池组装「链内优先、跨部门补位」
- getCJKCache 键升级 `tenantID|sha1(chain)|switch`（结构体字段改 map[string]map[string]int64）；新增 `InvalidateKBCaches()`；失效挂点：SaveBack/TM 审核通过/KB 条目增删导/RebuildKBIndex/包启停/共享开关切换/组织移动删除
- FetchRowTenant(id, tid, scope)：共享判定收紧行业码校验（修 #9）；scope=nil 回退旧行为

### 3.5 入口注入 org（三路）

| 入口 | 取值 |
|---|---|
| api/stream.go 四个翻译接口 | authUser.OrgID（匿名→0）|
| service/ticket.go runTicket | 开跑时 GetUser(t.CreatedBy, t.TenantID).OrgID |
| api_openapi_tasks.go 任务创建 + 同步端点 | ak.UserID 反查用户 org |

## 四、管理与配置面

| 面 | 改动 |
|---|---|
| PolicyConfig | 增 `CrossDeptFallback *int`（nil=默认开/0=关/1=开）；handlePolicy 输出解析后的布尔；handlePolicySave 接收 |
| kb_packages API | 新增 POST /api/admin/kb-packages/share {id, share}（镜像启停接口，canManagePackType+deptKBScope 三重校验）；create/update 可选携带 share_cross_dept |
| KBPackage 结构 | ShareCrossDept 字段 + kbPkgCols/各 Scan 点同步 |
| 前端 Kb.vue | 部门包卡片加「跨部门共享」开关（镜像启用开关交互，调新接口）|
| 前端 Models.vue | 策略卡加租户级开关一行；api/models.ts、api/kb.ts、i18n 中英补齐 |

## 五、测试计划

store/kbpackages_scope_test.go + kb/db_scope_test.go（临时文件库直插种子数据）：

1. 三级组织 A>B>C：C 用户命中 C 包；删 C 包回落 B 包；再删回落企业包
2. 兄弟分支 D 包：C 用户精确/模糊/语义三路径零命中
3. 无组织用户：只见企业/历史行/行业/文化
4. pack_id=0 历史行：任意部门用户可见（企业层级）
5. 移动部门（改 parent_id）后继承随之变化
6. opt-out：share_cross_dept=0 的兄弟包——跨部门回退不可见；该包对本链用户仍正常可见
7. 开关关：CrossDeptPacks 为空，两段式第二段不发
8. 打标：跨部门精确命中 Mode 含「🌐跨部门命中（来自X）」
9. #9 回归：他行业包行 FetchRowTenant 拒绝；#10 回归：文化包行向量可召回

## 六、验收清单

- [x] go build/vet/test 全绿（新增 9 个 scope 场景用例）
- [x] vue-tsc --noEmit && vite build 全绿
- [x] 冒烟：`deploy/smoke_kb_scope.sh` 三段式（逻辑层九场景自动化 + 服务探活 + UI 四步人工清单）
- [x] 部署指南 §八-C、PROGRESS.md 同步更新

## 七、边界声明

- kb_packages.parent_id 维持闲置（组织归属单轴 org_id）
- 管理视角列表逻辑不动（本按子树）
- 前台"生效范围"提示、知识贡献统计 → 二期

---

## 八、实施记录（2026-08-26）

| 项 | 状态 | 落点 |
|---|---|---|
| OrgAncestorIDs 祖先链 | ✅ | iam/store.go（环检测+深度≤20）+ store/orgs.go 委托 |
| 数据迁移 | ✅ | store.go：share_cross_dept 列 + idx_kb_packages_org（migrateColumns 尾部，规避建表顺序问题） |
| PackScope 类型/装配 | ✅ | kb/scope.go（Rank/InChain/CrossName/ChainKey）；store.BuildPackScope（含空链守卫+share 过滤——测试中发现的真 bug 已修） |
| 检索三路径 | ✅ | db.go FindExactScoped 两段式/FuzzyHitsScoped 分层/FetchRowTenant(scope) 修#9；npz.go ScopedSearchScope 修#10 且 InChain 排序优先 |
| tm_segments 唯一键三元组 | ✅ | ensurePackScopeUnique 重建表方案（autoindex 不可 DROP 的先例路径；修复旧重建丢 priority/pack_id 列隐患）；4 个写入点 ON CONFLICT 同步升级 |
| 引擎接线 | ✅ | engine/scope.go ctx 注入+userScope+缓存键升级+InvalidateKBCaches；translateOneInner 四段重写（就近覆盖/跨部门打标「精确命中 \| 🌐跨部门（来自X）」保留徽标子串兼容/例句链内优先跨部门补位） |
| 入口注入 ×3 | ✅ | stream.go userOrgCtx 四接口；ticket.go runTicket 创建人/APIUserID 回退；api_openapi_tasks 同步端点 ak.UserID 反查 |
| 策略开关 | ✅ | PolicyConfig.CrossDeptFallback *int 三态(nil=默认开)；admin_models 读写；前端 Models.vue 策略卡 select |
| 包级 opt-out | ✅ | SetKBPackageCrossDeptShare + POST /api/admin/kb-packages/share（三重校验镜像启停）；Kb.vue 部门包卡片按钮；api/kb.ts + i18n 中英 |
| 测试 | ✅ | kbpackages_scope_test.go 九场景全绿（三级就近/祖先继承/兄弟隔离/opt-out/开关关/空链守卫/历史行与行业码/移动重继承/模糊分层+#9#10回归） |
| 回归 | ✅ | go build/vet/test 全部通过（9 包 ok）；vue-tsc --noEmit 零错误；vite build 成功 |

**实施中发现并修正的方案外问题**：
1. tm_segments 旧唯一键 (zh_hash,tenant_id) 会令同租户跨层同名术语写时互覆——扩为三元组后「写并存、读裁决」；
2. 旧 rebuildTableWithCompositeUnique 拷贝列清单缺 priority/pack_id，生产库若再触发将丢列数据——新重建逻辑已覆盖全列；
3. BuildPackScope 初版漏按 share_cross_dept 过滤跨部门集（被 opt-out 测试用例当场拦截）。
