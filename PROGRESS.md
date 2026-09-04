# 能言 SaaS · 项目进度总览

> 最后更新：2026-09-05（用量看板日期区间查询修复 + 腾讯 TDesign 日期范围组件；main 分支 **fe56876 / 2de90a4**，已推送已部署）

## 〇-XIX、用量看板日期筛选修复 + 自定义日期区间（2026-09-05）

> 用户反馈主站/演示站「选了日期数据都是 0」。排查发现两处叠加 bug：① 前端 `usage.orgTotal` 字典用 `{total}` 占位符而调用传 `{n}` → 模板不替换直接渲染字面量「本层累计：{total} token」；② 后端 `UsageByOrg`/`UsageByUser` 过滤 `l.user_id>0`，而全站批量 LLM 任务记账 `user_id=0`（系统/未登录），仅含此类任务的日期（如 2026-09-03 的 938848 token）按日查询恒为 0。同时顺带把「单日查询」升级为「任意日期区间」（1 天/3 天/自定义）。

| 块 | 内容 |
|---|---|
| **① 前端模板 bug** | `panels/usage.ts` `usage.orgTotal` 占位符 `{total}`→`{n}`（与 `panels_a.tsx` 的 `tpl(…,{n:…})` 对齐）；补 `usage.dateFrom/dateTo/dateClear/dateQuery` 中英键；清理与 `dicts.zh.ts` 重复键（`colUser/colName/colOrg/colCost` 由 panels 版覆盖生效） |
| **② 后端 user_id=0 口径** | store `UsageByOrg` 移除 `l.user_id>0` 过滤；API `handleUsageOrg` 超管/企业两分支把 `costByUser[0]` 并入 total 并追加一行「系统/后台任务」明细；`UsageAllByUser` 同样纳入 0 号用户 |
| **③ 自定义日期区间** | store 三函数（`UsageByUser/UsageByOrg/UsageAllByUser`）参数 `day`→`from,to`；新增 `usageDatePred` 生成 `created_at >= ? AND created_at < ?` 区间谓词（RFC3339 text 字典序可比；兼容非法日期回退 LIKE）；API 新增 `usageDateRange` 解析 `from/to` 并兼容旧 `date` 单日参数 |
| **④ 腾讯 TDesign 组件** | 前端用量看板用 `tdesign-react` 的 `DateRangePicker`（mode=date + valueType=YYYY-MM-DD）替换原生 `<input type="date">`，支持任选起止日期/清除；`api/billing.ts` `usageMe/usageOrg` 传 `from/to` |
| **验证** | 生产/演示库区间谓词与旧 LIKE 等价（2026-09-03 = 938848）；演示站 API：空日期累计 984997（含 system 968982）、单日 09-03 = 938848、区间 08-28~09-03 = 956337、平台视图区间 957602；go vet/test 全绿、vite build 通过 |
| **部署** | 主站与演示站分别停服→备份→替换 bin+web（MD5 `43ac7e7bda601aff79c030f1c72993b6`）→启动→health 200，前端构建 `index-BLX7noca.js` 两站一致 |

## 〇-XVIII、并行分支核查修正：KB 面板平台上下文宿主回归（2026-09-05）

> 多 session 并行推进同一分支后复核发现：`admin_kb.go` 在并行提交中被**回退为旧模型**——`kbTenant` 超管平台上下文仍返回 1、`handleKBPackages` 仍调用 `filterIndustryPackages`（按租户行业过滤行业包），与已上线的「共享包宿主=租户0」模型矛盾。后果：超管平台上下文（tid=0）被映射到 ROX 租户1，**看不到/管理不了租户0的行业包/语言文化包**；逐一核对了其余宿主文件（kbpackages.go/kb/db.go/engine.go/admin_scrape.go/auto-approve）均正确，仅 admin_kb.go 与 kbtenant_test.go 残留旧模型。

| 块 | 内容 |
|---|---|
| **① 修复** | `admin_kb.go` `kbTenant`：超管未显式切换企业租户（tid≤0）时返回 `0`（平台共享包宿主），已切换企业租户（tid>0）返回该租户；移除被并行分支倒退的 `filterIndustryPackages` 函数与其调用、移除旧的「非超管滤 locale」逻辑——行业包/文化包已迁至租户0，本租户 `kb_packages` 天然不包含，无需再按行业过滤 |
| **② 测试同步** | `kbtenant_test.go` 由「超管平台→租户1」更新为「超管平台→租户0（SharedHostTenant）」+ 新增超管切换企业租户用例；`go test ./...` 16 包全绿 |
| **③ 前端核对** | `authHeaders` 平台上下文（activeTenantId=0）不发 `X-Tenant-ID` → 后端 tid=0 → 平台行业包正常列出；企业租户发 `X-Tenant-ID` → 仅本租户包。`panels_d.tsx`/`KbUploadDialog` 数据驱动渲染，无硬编码冲突 |
| **④ 部署** | 重新交叉编译 Linux 二进制 → 替换主站与演示站 → 重启 → 验收（健康检查 + 迁移计数核对） |

## 〇-XVII、共享包宿主存量数据迁移（2026-09-04）

> 〇-XVI 已把读写路径统一到 `SharedHostTenant=0`，但**存量库**里租户1的行业包/语言文化包及其条目/安全句/检索层行仍是旧宿主租户1，导致 ROX（租户1）后台/检索仍能看到平台行业包。本次补上启动数据迁移，把存量一并搬到租户0。

| 块 | 内容 |
|---|---|
| **① 存量迁移** | `Store.MigrateSharedHostToZero`（启动自动执行，幂等）：找出 `tenant_id=1 AND pack_type IN ('industry','locale')` 的包，把 `kb_packages`/`kb_entries`/`kb_safety_phrases`/`tm_segments` 的 tenant_id 统一改为 `0`（企业包/部门包不受影响；pack_id 唯一不冲突） |
| **② 回归测试** | `TestMigrateSharedHostToZero` 锁定：旧宿主（租户1）行业包 + 条目 + 检索行 + 安全句迁移后全部落租户0、企业包（租户2）不受影响、二次调用幂等 |
| **验证** | go build/vet + go test ./internal/... ./cmd/... 全绿 |

## 〇-XVI、共享包宿主迁移补齐 + 测试夹具对齐（2026-09-04，提交 b3a9fc6）

> 〇-XV 后复核发现两处共享包宿主迁移遗漏：采集审批（admin_scrape.go）与一次性清洗审批工具（auto-approve）仍把平台共享行业包/语言文化包内容写入租户1，而生产代码已统一以 `SharedHostTenant=0` 装配共享包 → 审批落库租户与检索宿主不一致。本次补齐并让测试夹具随架构对齐。

| 块 | 内容 |
|---|---|
| **① 宿主租户补齐** | `handleKBScrapeApprove`/`handleKBScrapeRestore`（admin_scrape.go 两处）与 `cmd/auto-approve/main.go` 的 `tid` 由 `int64(1)` 改为 `store.SharedHostTenant`（=0），审批/清洗通过的行业包与语言文化包内容正确落到共享宿主，与 `BuildPackScope`/`sharedFilterSQL` 检索口径一致 |
| **② 测试夹具对齐** | `kbpackages_scope_test.go` 共享行业/文化包建包与条目宿主改为 `SharedHostTenant`；`packages_test.go` 行业包建在共享宿主（`FindIndustryByCode` 仅查宿主租户0）；`TestScopeHostTenantIndustryFilter` 注释同步为「企业租户按注册行业隔离」 |
| **验证** | go build/vet + go test ./internal/... ./cmd/... 全绿（TestScopeLegacyRowAndShared/TestVectorScopedSharedVisible/TestIndustryPackage/TestScopeHostTenantIndustryFilter 均通过）；仅提交代码（4 文件，不含文档/流程图） |

## 〇-XV、错误码体系 + OpenAPI 修复 + 队列租约心跳 + 扩展安全（2026-09-04，提交 309a126）

> 全仓审计后的修复批次，已部署生产 langcross.lexicorn.cn 并通过 deploy_check.sh 验收（9 项全过）。

| 块 | 内容 |
|---|---|
| **① 错误码体系** | `internal/errors/codes.go` 新增 OpenAPI snake_case 常量（invalid_api_key/key_quota_exceeded/forbidden/not_found/internal/bad_request/text_too_long/no_result/task_failed/insufficient/rate_limited/daily_quota/rejected）；api_openapi_tasks.go（37 处）与 admin_openapi.go（17 处）全部替换为 `string(errors.OpenAPIXXX)`（JSON 输出值不变）；前端 `api/core.ts` 新增 `ApiError` + `bizErrorCode` 透传 code/error_code |
| **② OpenAPI JSON 修复** | `openapi.v1.json` 原为损坏 JSON（/kb/stats 与 /keys/rotate 两路径对象各缺一个 `}` 致根对象提前闭合、components 沦为尾料）。已重写并补统一 `Error` schema + code 枚举 + error_code 别名；新增回归测试 `verify_embed_test.go`；线上 `/openapi/v1.json` 已验证合法 |
| **③ watchdog** | `watchdog_selfcheck_restart` 默认开启（显式 "0" 才关）；`SELFCHECK_URL` 可覆盖探活地址（默认 127.0.0.1:8787/status） |
| **④ 队列租约心跳** | `Queue` 接口新增 `Heartbeat`；`DirectQueue.Heartbeat` 按 `id+status='running'+leased_by` 刷新 leased_at；`RecoverStale` 改两步（先清租约再审是否回队）；worker 每 60s 续租；新增 TestHeartbeatKeepsLease 等测试 |
| **⑤ 扩展安全** | manifest.json 的 host_permissions → optional_host_permissions + activeTab/storage；popup.js 改 storage.local + API Key 掩码 + 按源授权；content.js 改 textContent 消毒 + 仅 http/https 受信任源 |
| **⑥ 部署上线** | 交叉编译 Linux 二进制 → scp → 备份 → 替换 bin/web → 重启；deploy_check.sh 9/9、/openapi/v1.json 合法、错误码出参正常、当日 0 panic；邮件确认已生效（[smtp] 发送成功 from=noreply@lexicorn.cn） |
| **⑦ 遗留待办** | backup_remote_cmd 异地备份未配、captcha_provider=turnstile 未启用（见《生产配置清单_20260904.md》） |

## 〇-XIV、品牌固定用法初始化进企业知识库 + 主站/演示站部署（2026-09-04，基于 75496f5）

> 线上实测：俄文翻译「极石汽车驾驶要领」品牌「极石」未被正确命中知识库译法 ROX，被模型音译为 **Jixi**（`Центр обслуживания автомобилей Jixi`）。根因：租户企业包无「极石→ROX」术语 → 模型自由发挥。本次把品牌固定用法在租户落地时初始化进企业包 L1 术语，并部署主站与演示站。

| 块 | 内容 |
|---|---|
| **① 租户模型扩展** | `tenants` 新增 `brand_names`（多语言名 JSON，如 `{"zh":"极石","en":"ROX"}`）与 `brand_name_en`（品牌英文名）两列（`EnsureColumns` 幂等补列，PG/SQLite 双兼容）；`Tenant` 结构体 + `tenantColumns`/`scanTenant` + `SetBrandNames`/`SetBrandNameEn` |
| **② 品牌术语种入** | `store.SeedBrandTerms(tid, names)`：品牌中文名→各目标语 L1 术语种入企业包（`code='tenant'`），语言兜底规则——显式语言名优先 > `zh_hant` 沿用中文名 > 其余语言用英文名；无可用译文跳过；复用 `SaveEntry` 幂等 upsert（kb_entries + tm_segments priority=1） |
| **③ 注册/建租户/品牌定制接线** | `api.seedTenantBrandTerms`（register.go）：注册（handleRegister）、超管建租户（handleTenantCreate）、后台品牌定制保存（handleTenantBrandingSet）三处统一种入 + 持久化 `brand_names`/`brand_name_en`；`brandingPayload` 返回新字段 |
| **④ 前端引导录入** | 注册表单「企业用户/管理员（新建企业）」新增「品牌中文名」「品牌英文名（选填，覆盖所有非中文语言固定用法）」录入（Login.tsx + auth.ts + i18n zh/en）；品牌面板（BrandP.tsx + api/branding.ts）新增品牌英文名编辑，保存即触发补种 |
| **⑤ 部署主站** | 交叉编译 Linux 二进制（MD5 `16c57b12`）→ scp → 备份旧版 → 替换 `/opt/translator/bin/translator-server` + `/opt/translator/web` → 重启 translator.service；`deploy_check.sh` 8 项全通过；`/api/health` 全 true、dialect=postgres、新列已补 |
| **⑥ 部署演示站** | 按 PROGRESS 记录流程：`systemctl stop translator-demo` → `dropdb langcross_demo` → `bash bootstrap-demo.sh`（克隆生产库/二进制/前端 + 种入 demo 账号）→ 起服；二进制 MD5 与生产一致、公网 `/api/health` 200 |
| **⑦ 生产补种 ROX 术语** | 生产租户1（ROX极石汽车）此前无 brand 术语 → SQL 补种：`tenants.brand_name='极石'/brand_name_en='ROX'/brand_names={"zh":"极石","en":"ROX"}` + `kb_entries` 34 条（全目标语）+ `tm_segments` 1 行（priority=1）；演示站经后台 API 保存品牌英文名触发同样种入 |
| **验证** | 演示站端到端回归：俄文「请尽快到极石汽车服务中心进行常规检查」→ 输出 `сервисный центр ROX`（不再 Jixi）；生产 `tm_segments` 精确命中 SQL 返回 `极石|1|ROX|ROX`；两站品牌字段/术语条数一致（34）；重启后无新增 panic |


## 〇-XIII、演示站知识库根治 + 行业包权限修复（2026-09-04，基于 75496f5）

> 演示站（rox-test.lexicorn.cn）运行旧二进制，知识库三个慢 SQL/交互问题未根治；且主站同样存在行业包权限问题。本次针对性修复 + 澄清演示站需以新二进制重发。

| 块 | 内容 |
|---|---|
| **① 行业包权限（主站同样存在）** | 行业包宿主在租户1，平台内全部行业包（auto/realestate/b2b/education/ecommerce 等）都在此 → ROX（汽车）在后台看到全部无关行业包。新增 `Server.filterIndustryPackages`：后台包列表按租户注册行业只保留 code=行业 的行业包，无注册行业回退通用行业包（general）；并对超管/宿主租户一视同仁。同时修 `BuildPackScope`（检索层）：移除 `tid==1 全放行` 旧规则，宿主租户同样只装配与本公司注册行业匹配的行业包，避免翻译时参考房产/教育等无关行业术语 |
| **② 安全句平铺（演示站旧二进制）** | 〇-XII 已实现安全句服务端分页（`ListSafetyPhrasesPage` + `applySafetyQuery` + 20/页跳页器），演示站因旧二进制未生效 → 重发即可 |
| **③ 查看目录慢/只显 20 条/翻页失效/翻译（演示站旧二进制）** | 〇-XII 已实现条目服务端分页（`ListEntriesPage` + `kbEntries` 分页参数 + 20/页跳页器 + 包列表 `entry_count` 消除 N+1 COUNT 卡顿），演示站旧二进制未生效 → 重发即可 |
| **演示站重发** | 演示站从生产克隆二进制/前端快照（`bootstrap-demo.sh`）。步骤：① 先部署本代码到生产（`/opt/translator/bin` + `web`）；② `systemctl stop translator-demo`；③ `sudo -u postgres dropdb langcross_demo && sudo bash scripts/bootstrap-demo.sh`（刷新克隆+种入演示账号+`-kb` 向量索引）；④ `systemctl start translator-demo`；⑤ 用 `demo_admin` 登录验证安全句/条目分页与仅汽车行业包 |
| **验证** | go build/vet/test（store+api 全绿）+ npm typecheck/vite build 通过 |

## 〇-XII、知识库性能优化 + 条目编辑 + 安全句服务端分页（2026-09-04，提交 75496f5）

> 线上反馈后台知识库卡顿：①安全句面板全量平铺渲染；②包列表每包一次 COUNT 的 N+1 请求；③「查看条目」列表缺搜索定位与编辑；④相关查询命中慢 SQL。本次从数据层索引到 API 到前端交互一并治理。

| 块 | 内容 |
|---|---|
| **N+1 卡顿根治** | 包列表条目数由前端逐包 `kbEntries(count:true)` 循环（每包一次请求）改为后端 `CountEntriesByPackages` 一次 `GROUP BY` 单查询，随 `handleKBPackages` 附带 `entry_count`（`attachEntryCounts`），前端 `loadPackages` 直接读角标，消除面板打开卡顿 |
| **慢 SQL 索引** | 新增 `idx_kb_entries_tid_pkg ON kb_entries(tenant_id, package_id, layer, target_lang)`（后台「查看条目」按租户+包过滤的 COUNT/LIKE 检索，原索引不含 tenant_id 走全表扫）与 `idx_kb_safety_tid_pkg ON kb_safety_phrases(tenant_id, package_id, lang)`（安全句过滤，原无索引）；安全句索引须在表创建后建立 |
| **安全句服务端分页** | `ListSafetyPhrasesPage`（store）：支持 包/语言/类型/状态 精确过滤 + phrase/replacement 关键词模糊搜索 + `LIMIT/OFFSET` 分页与真实总数；`handleSafetyPhrases` 接收 `package_id/lang/kind/status/q/page/page_size` 返回 `total`；前端安全句面板改为服务端分页（20/页 + 跳页器）+ 语言/类型下拉 + 搜索框 + 总数角标，替代原「全量平铺 + 客户端过滤」 |
| **条目编辑** | 查看条目对话框新增每行「编辑」按钮（回填表单→`saveEntry` 走更新），新增/保存按钮在编辑模式切换文案，支持「取消编辑」；后端新增 `/api/admin/kb-entries/update`（`handleKBEntryUpdate`）与 `UpdateEntry`/`GetEntryForUpdate`（store，租户隔离 + 不可改包归属），带部门/包类型权限校验 + 审计 + 失效 CJK 缓存 |
| **前端** | `api/kb.ts` 新增 `kbEntryUpdate`、`safetyPhrases` 支持分页/过滤参数；`panels_d.tsx` 新增 `querySafety`/`applySafetyQuery`（显式传参避免陈旧闭包）与 `reloadSafety`（增删改后沿当前过滤/页码回填）；i18n 新增编辑/搜索/安全句计数文案（zh/en） |
| **验证与发布** | go build/vet/test（store+api+culture+crawler+engine 全绿）+ npm typecheck/vite build 通过；已部署生产 langcross.lexicorn.cn（/api/health 全 true、`/status` dialect=postgres、bundle 含新功能、新路由鉴权正常、重启后无新增 panic）；本次提交不含文档/流程图 |

## 〇-XI、任务中心 + 后台菜单重组 + 行业筛选 + 计费审计修复（2026-09-03，提交 18c6857）

> 四线并行交付：①用户增长向的「任务中心」（每日/一次性任务领永久 token）与「个人中心」菜单；②后台菜单层级收敛（协议签署并入系统设置、开放 API+回调通知并入外部调用、邀请好友并入个人中心，流程引擎并入系统设置并修复跳转白板）；③待审池支持按行业筛选；④计费链路审计修复 + 中性词豁免 + 后处理增强。已本地构建/冒烟 + 生产部署验证通过。

| 块 | 内容 |
|---|---|
| **任务中心（新）** | `user_tasks`/`user_task_claims` 建表（启动迁移幂等）+ 后端 `internal/api/tasks.go`（超管增删改 `/api/admin/tasks*` + 用户 `/api/me/tasks*`）+ `internal/store/tasks.go`（`ListTasks` 按 enabled/sort_order 排序、`ClaimUserTask` 日频/一次频去重 + 事务发放永久 token）；前端 `TaskCenterP`（用户视图一键领取 + 超管增删改弹窗）+ `api/tasks.ts` + `panels/tasks.ts` 中英 i18n。**路由已注册**（server.go `routesTasks`） |
| **后台菜单重组** | 侧边栏收敛为 10 项：协议签署并入「系统设置」（SystemSettingsP 增加第 4 个 agreements tab）；开放 API+回调通知并入「外部调用」（ExternalCallsP 双 tab）；邀请好友+任务中心并入「个人中心」（PersonalCenterP 双 tab）；流程引擎并入「系统设置」（修复原菜单独立直达白板 bug）；stores/admin.tsx `PanelKey` 新增 `external`/`personal`、AdminDashboard `renderPanel` 补全 case，隐藏面板仍可跳转不白板 |
| **待审行业筛选** | `ListStagedMerged`/`CountStagedEntries` 支持 `industry` 参数（JOIN `kb_pack_sources.industry`，指定行业时安全句整体排除，与前端语义一致）；`handleKBScrapeStaged` 透传；前端待审面板新增行业下拉（`INDUSTRY_META` 本地化名）+ 行业列，请求带 `industry` |
| **计费链路审计修复** | `billing.go`/`quota_grants.go` 口径核对与修正；`admin_kb.go` 相关接口加固；`store.go` 迁移补充 |
| **中性词豁免 + 后处理增强** | `culture/culture.go` 中性词豁免策略；`engine/postprocess.go` 后处理增强（配合 `panels_d.tsx` 前端展示）；`tier3_llm.go` 相关容错 |
| **验证与发布** | go build/vet/test 全绿 + npm typecheck/vite build 通过；本地冒烟实测任务中心（建任务→领取+5000→重复领取被拒→已领取标记）与行业筛选 JOIN（health/finance 各取对应行）通过；已部署生产 langcross.lexicorn.cn（/api/health 全 true、/status dialect=postgres、新表 `user_tasks`/`user_task_claims` 已建、新路由 401/403 鉴权正常）；本次提交不含文档/流程图 |

## 〇-X、待审面板服务端分页 + LLM 输出容错（2026-09-03，提交 82ffccb）

> 线上反馈两类问题：①待审面板仅显示约 400 行（编号已到 4 千多、通知说有 1 万多待审，对齐不上）；②部分数据源报 `pq: column "tenant_id" does not exist` 与硅流超时。核查定位后针对性修复。

| 块 | 内容 |
|---|---|
| **面板数量失真根因** | 旧实现 `scrapeStaged` 对条目/安全句各取 `limit=200`（共 400 行）后纯前端分页，与真实库量（主库 approved 条目 15818 + 安全句 4787）严重不符。改 **服务端分页**：新增 `ListStagedMerged`（`kb_staged_entries`+`kb_staged_phrases` UNION ALL 统一行集，`key=kind:id` 复合键防两表自增撞车，`phrase_kind` 保留 style/forbidden/replace）与 `CountStagedEntries`/`CountStagedPhrases` 精确总数；`handleKBScrapeStaged` 返回 `rows/total/limit/offset`，前端面板按真实总数翻页（`待审增量（${total}）`，单页 20 条 + 跳页器，翻页清空选中）。SQL 已在生产库实测验证 |
| **tenant_id 报错为历史残留** | `kb_staged_entries` 列早已由启动迁移幂等补齐；面板所报 error 均为 **2026-09-02 旧二进制**遗留的 `last_status`（`last_run_at` 全为昨日）。清掉历史 error 源标记重跑后 22 个源全部转 `ok`（新增数据带 tenant_id 写入成功） |
| **LLM 输出解析容错** | tier3 低语种（ur/te）模型输出带**尾逗号**（`{"tgt":"...",}`）与 **Markdown 代码块围栏**（```json … ```），`extractJSON` 严格校验失败。新增 `cleanJSONFence`（剥围栏）与 `stripTrailingCommas`（字符串感知剔尾逗号，跳过引号内逗号与转义），预处理后再做括号平衡提取；`TestExtractJSONTrailingComma` 用线上真实失败样本锁定回归。ur/te 两源已实测重跑转 `ok` |
| **剩余观察项** | 579 源中 1 个仍 `error` 为 **LLM 输出截断**（JSON 未闭合），与尾逗号/围栏非同类；低语种冷门模型偶发超时属外部依赖，次日自动重跑自愈 |
| **验证与发布** | go build/vet/test（含 crawler 新回归）+ npm typecheck/vite build 通过；已部署生产与演示，/api/health 全 true；本次提交不含文档/流程图 |

## 〇-IX、采集自动审批改造 + 源语言清洗 + 待审还原编辑（2026-09-03，提交 657f7b3）

> 待审批数据流程由「人工审核」改为「自动清洗/修正/审批并通过」：采集即检测源语言并直接落正式库并留痕，人工只对已通过数据驳回/改正。

| 块 | 内容 |
|---|---|
| **流程改造** | 爬虫 `RunSource` 改为自动审批模式（`system_config.scrape_auto_approve`，默认开）：采集条目/安全句经 `AutoApproveEntry`/`AutoApprovePhrase` 直接落正式库（kb_entries/tm_segments + kb_safety_phrases）并在待审表留 `approved` 痕迹，人工事后可查看/驳回/改正；采集后由 SDK 调度自动 `invKB` + 异步重建向量索引 |
| **源语言自动清洗** | 新增 `crawler.DetectSourceLang` 按 Unicode 脚本检测源语言（CJK→zh/zh_hant 简繁细分、假名→ja、谚文→ko、西里尔→ru、阿拉伯→ar、泰文→th、天城文→hi、拉丁→en），采集时纠正 tier1/2/3 硬编码的 `SrcLang:"zh"`，英文源文本（如「blended learning」「auto parts」）不再误标 zh；`detect_test.go` 锁定回归 |
| **历史数据一键回填** | 新增 `cmd/auto-approve`：连接生产库批量清洗+审批历史 pending 待审（更正源语言→重算去重 hash→嵌入正式库→置 approved），含 `-dryrun` 预览与 hash 一致性修复（reconcile 删除 stale 重复行/更正孤儿 hash）。操作前 pg_dump 备份 |
| **还原为待审/编辑** | 新增 `POST /api/admin/kb-scrape/restore`：已通过/已驳回条目拉回待审池，支持还原前编辑内容（改译文/替换词），并回收正式库落库（kb_entries/tm_segments/kb_safety_phrases）+ 失效缓存 |
| **前端** | 待审面板「已通过/已驳回」筛选下显示「批量还原为待审」按钮 + 每行「还原/编辑」弹窗；语言列改中英文名展示（补充 `zh` 缺失映射）；新增「系统设置」合并面板（邮件模板/流程引擎/系统告警） |
| **修复事故** | `AutoApproveEntry` 原沿用内存 stale hash 致源语言更正行插入重复 approved 行（实测 15818→18841 +3023）；改为始终按当前字段重算 hash，回归测试 `TestAutoApproveEntryAfterSrcLangChange` 锁定 |
| **验证与发布** | go build/test（store+crawler+api+engine 全绿）+ npm typecheck/vite build 通过；已部署生产（langcross.lexicorn.cn）与演示（rox-test.lexicorn.cn），/api/health 全 true；主站 15818 条待审条目全部自动审批通过（更正源语言 3023）、4787 安全句通过；演示站 15626 条目 + 4787 安全句全部通过 |

## 〇-VIII、前端交互与前后端契约审计修复（2026-09-02，提交 0ee5b97）

> 全面审计 React 迁移后前端交互一致性/显示统一性/前后端契约：程序化比对前端 API 调用与后端路由（模板串插值经哈希键校验），并逐项核对弹窗/取消链路、字段口径。

| 块 | 内容 |
|---|---|
| **余额面板 403（bug1）** | `selfservice.tsx` BalancePanel 原调 `/api/billing/balance`（后端 `handleBalance` 需租户管理员 `requireTenantAdmin`），普通用户访问「我的余额」(/billing) 403 显示错误卡片。改走 `/api/me/package`（登录用户即可读），读 `permanent_balance`/`sub_grants_left`/`balance_tokens` 三字段 |
| **死代码路由（bug2）** | `api/feedback.ts` 删除 `adminFeedbacks()` 与无用 `FeedbackItem`（前端唯一指向不存在路由的调用——后端仅 `/api/admin/feedbacks/resolve`，管理台列表实际走 `/api/feedback/list`） |
| **字段缺口（bug3）** | ChatWindow 读后端不存在的 `estimate_rate` 字段，句数换算恒走 500 兜底；改按「可用 token ÷ ≈句数」（balance_tokens/balance_sentences_approx）反推实际换算率，无余额时兜底 500 句/token |
| **确认弹窗语义（ux4）** | 工单「✕取消」动作与确认弹窗「取消」按钮同名，造成「点了取消没反应、再点确定才取消」的交互歧义（链路本身正确，是文案混淆）。`confirmDialog` 增加 `confirmText`/`cancelText` 定制按钮文案；工单取消/删除确认按钮显示「确认取消」/「确认删除」（中英 i18n） |
| **审计确认无问题** | 登录强制改密/邮箱绑定/注销/密码/反馈弹窗、Bell 通知、AccountMenu、ModeToggle、KB 上传、审批弹窗、用量看板、支付/发票/订单（仅超管面板调用）等交互与契约均一致；159 个前端 API 调用 vs 后端路由无其他 URL 级不匹配 |
| **验证与发布** | npm typecheck + vite build 通过；已部署生产（langcross.lexicorn.cn）与演示（rox-test.lexicorn.cn），主页与 `/api/health` 全 200 |

## 〇-VII、符号残留根因修复 + 功能批量收尾发版（2026-09-02，提交 2dc29b0 / 7654cbc）

| 块 | 内容 |
|---|---|
| **★ 符号残留根因** | 翻译后处理 `stripReviewMarkers` 泛化：`markerBracketRe` 匹配单条审校模板标记（原文/译文/待审校译文/待審校譯文 及 `[]` 变体），最后一个标记后非空则取其后续译文、否则删除全部标记（不误删 `【贵宾】` 等正常内容）；`PostProcessTranslation` 在中文删除后追加空占位方括号二次清扫（原 `【原文】→【】` 残留）。实测 `【】Please perform…` 与 `【原文】Please perform…` 式残留根治；新增 `postprocess_test.go` 24 用例回归（en/zh_hant 端到端） |
| **工单轮询（功能0）** | TicketsPage 列表轮询改 `ref` 持有最新列表 + 一次性 effect（原 useCallback 依赖 tickets 重建 interval 致高频/无限请求）；kaijin.liu 登录403：邀请开关按角色等级只对租管及以上读取（stores/admin.tsx） |
| **计费豁免（功能6）** | `gateUsage` 与 `runTicket` 余额预检：超管 / tenant_id=0（平台上下文）跳过 QPS/并发/日额/余额/预算墙全闸门，避免平台视角被误判「余额不足」拒绝 |
| **哈萨克语全链路（功能3/4）** | 翻译指令限定「哈萨克斯坦国家语 · 西里尔字母」（Qazaq tili），禁止中国哈萨克族阿拉伯字母写法；config.LangNames / langNames.ts / 字典 / 下拉全链路名称统一「哈萨克语（哈萨克斯坦）」；繁体提示词禁止复述原文与【原文】【待審校譯文】标记 |
| **工单导出语言名（功能5）** | Excel 导出表头语言码→中文名（`config.LangNames`，无映射回退码）；工单列表 `target_langs` 列改 `langLabel` 显示中文名（此前显示原始码如 en,fr） |
| **控件行稳定+去重（功能1/2）** | 缩翻输入框 72px 定宽预留槽位——勾选只显隐输入框、不推移模式/发送/创建按钮；聊天页语言选择器改可收缩（minWidth:0）防窄窗口溢出；LangMultiSelect `valueDisplay` 去重 |
| **租户导入模板（功能4 收尾）** | 新增 `GET /api/admin/users/import-template`（表头+说明+示例行 xlsx）+ 前端「下载模板」按钮与中英 i18n |
| **其他修复** | xlsx 字号缩放样式 nil 判空、`kb_staged_entries` 幂等补 tenant_id 列（EnsureColumns）、bootstrap-demo `users_id_seq` 序列修正 |
| **验证与发布** | go build/vet/test 全绿 + npm typecheck/vite build 通过；已部署演示站（rox-test.lexicorn.cn，二进制 MD5 与生产一致 `637a749d`）与生产（langcross.lexicorn.cn，health/plans/register-config/translation-langs/tenant-branding 全 200）；7654cbc 全量中文注释收尾（crawler/extract_test.go 文件头） |

## 〇-VI、行业下拉双语自适应 + 全量中文注释补齐（2026-09-02，提交 ff48ee0）

| 块 | 内容 |
|---|---|
| **行业双语自适应** | 新增 `frontend-react/src/lib/industries.ts`：行业 code↔中英文名映射（汽车↔automobile 等 9 行业），登录注册/租户管理/数据源三类下拉统一按当前语言显示中文或英文名；值用中文名、提交时经 `industryCodeOf` 转回 code，后端接口契约不变 |
| **注释补齐** | 前端 api/core、branding、MessageBubble、AccountMenu、panels_b/d 及历史遗漏的顶层声明补充中文注释；后端 crawler/extract_test.go 测试函数注释补齐 |
| **验证** | `npm run typecheck` + `vite build` 通过；`go build ./...` + `go vet` 通过；已部署至生产（43.108.86.140）并跑 `deploy_check.sh` 8 项全通过 |

## 〇-V、行业包/语言文化包自动采集 + 硬闸护栏（2026-09-01，提交 ff7bf17）

| 块 | 内容 |
|---|---|
| **数据源三档** | tier1 官方 API（维基百科 langlinks 反查，源语言 zh）、tier2 受限抓取（robots.txt 遵从 + 每主机限速 1.2s + 术语表表格解析）、tier3 LLM 生成（词表批量翻译，kind 白名单 style/forbidden/replace），统一标注 tier 可信度进待审池 |
| **调度（低占用驱动）** | watchdog 内嵌 `startPackScraper`：每 `scrape_poll_sec` 探测，仅当「无排队/运行工单 + LLM 错误率 < 阈值 + RSS < 水位」三条件全满足才采集；占用提升即暂停，checkpoint 断点续传；`scrape_seed_once=1` 首日铺底 + 每日增量（`kb_scrape_daily_marker`）；新增待审通知全部超管（站内信） |
| **审批热加载** | 超管面板「🕷️ 数据采集」：数据源 CRUD/启停/手动采集一轮；待审池按类型/语言/状态筛选 + 批量通过/驳回；通过条目经 SaveEntry 落 `kb_entries`（宿主=平台共享包宿主租户0，`SharedHostTenant`）、安全句经 SaveSafetyPhraseEx 落 `kb_safety_phrases`，随后 invKB 失效缓存 + 异步重建向量索引即时生效（注：2026-09-04 行业包/语言文化包宿主已由租户1迁至租户0） |
| **硬闸护栏（gate_retry_max=8）** | Gate 8 项硬校验 / 语言文化闸门任一失败不再直接置 rejected：附 KB 参考（源文本命中标准译法）+ 失败原因，经 TranslateWithFeedbackEx 自动重译，循环至通过或达上限（默认 8 次）；重试次数与最近打回原因写入 payload（RetryCount/GateHints）供审批参考；`0`=关停自动重译直接打回 |
| **幂等与去重** | 待审去重键 `md5(src_lang\|src_text\|tgt_lang\|tgt_text)`（条目）/ `md5(lang\|kind\|phrase\|replacement)`（安全句），唯一索引 + INSERT OR IGNORE；断点续传键 `kb_scrape_checkpoint_<date>_<source_id>` 等存 system_config；store.KBScrapeMigrate 幂等建表（kb_pack_sources / kb_staged_entries / kb_staged_phrases） |
| **验证** | go build/vet/test（api+crawler+store+orchestrator 全绿）+ npm typecheck + vite build 通过；文档同步《部署指南》§八-B4（采集与护栏配置）并去掉过时/不存在的配置键表述 |

## 〇-IV、订单「我已付费」通知超管链路修复（2026-08-31，提交 ed1be6d）

| 块 | 内容 |
|---|---|
| **现象** | 静态码订单用户点「我已付费」后：超管铃铛无站内信、无告警邮件（alert 表其实已写入 id=55） |
| **根因1（站内信静默丢失）** | `notifications_id_seq` 序列失步（生产 seq=15 vs 实际 max(id)=92），`CreateNotification` 取序列主键撞已存在 id → insert 失败被 `_ =` 静默吞掉 → 站内信全丢。alerts 表序列正常故告警能写入 |
| **修复1** | 生产+演示 `notifications_id_seq` setval 对齐 max(id)；同时发现 `api_keys`(-22)/`kb_entries`(-23580) 同病，一并修复 |
| **根因2（无告警邮件）** | `alert_email` 系统配置为空 → `notifyAlert` 直接 return（不发送）。本次已配置 `alert_email=noreply@lexicorn.cn` + `alert_email_cc=575160894@qq.com` |
| **代码加固** | `handlePayManualConfirm` 站内信循环改为捕获错误打日志（`[pay-manual-confirm] 站内信通知超管(id=..)失败`）而非静默吞错；`notifyAlert` 支持 `alert_email_cc` 抄送 + 改走 `enqueueMail` 异步队列（Message.CC 由 SMTPSender 写入 Cc 头） |
| **演示脚本固化** | `bootstrap-demo.sh` 序列修复由仅 users 扩展到全核心表（users/notifications/api_keys/kb_entries/orders/tickets/alerts），防止 pg_dump 回放后新演示镜像再踩主键冲突 |
| **验证** | 演示环境端到端：demo_admin 下单(manual)→manual-confirm → `notifications` 新增 admin(id=1) pay_manual 站内信 + `jobs` mail_send 入队 `to=noreply@lexicorn.cn cc=575160894@qq.com` 主题「静态码支付待人工确认」+ alerts 写入；已清理演示测试订单/通知；生产+演示二进制同 MD5(708d557d) |

## 〇-III、演示镜像独立化 + 演示专用账号（2026-08-31，提交 2dff0bc / 55a148a）

| 块 | 内容 |
|---|---|
| **云端清理测试数据** | 删除生产库测试租户 5/7/8/9/10/11 及 user01/taadmin/t_member_bad/t_admin3 等测试账号并级联清理关联数据；生产库仅存唯一租户1（rox）+ 真实用户 |
| **演示专用账号种入** | `bootstrap-demo.sh` 新增 [4.5] 步骤：种入 4 个仅存在于 langcross_demo 的账号 demo_admin（企业管理员）/ demo_youtube / demo_hr / demo_cs，统一密码 Demo#2026Rm!；生产库无同名账号 → 跨库登录/数据彻底独立 |
| **bcrypt 哈希双坑修复** | ① shell/heredoc 变量展开破坏 `$2/$10/$408` → 引号 heredoc 写临时文件 + `psql -f`（`$` 保持字面量）；② psql 参数误用 `\"` 拼接 → 改函数封装 `psql "$DEMO_DSN" "$@"` |
| **品牌子域修复** | 租户1 Domain `rox`→`rox-test`：登录不再返回 `brand_host=rox.lexicorn.cn`，避免前端 `window.location.replace` 强跳生产域名破坏演示独立性 |
| **品牌定制展示修复（55a148a）** | 原脚本把演示库 `primary_host` 设为 `rox-test.lexicorn.cn`，导致 brandingPayload 将 rox-test 判为主站前缀 → 返回平台品牌（空），租户1的品牌定制（logo/首页背景/网页标题 brand_name）在演示站不展示（数据其实已随克隆）。修复：`primary_host` 保持主站 `langcross.lexicorn.cn`，rox-test 前缀走 `GetByDomain(rox-test)` 命中租户1 → 演示站正确展示 Rox极石汽车 logo/背景，网页标题自动变为「Rox极石汽车 智能翻译平台」 |
| **发版隔离验证** | 生产 translator 重启（模拟发版）期间演示 translator-demo 全程 200；两服务/两端口(8787/8789)/两库(PostgreSQL langcross/langcross_demo)物理隔离 |
| **验证** | 演示 4 账号公网登录 + 受保护接口（/api/billing/balance total_available=300000）+ 生产/演示主页 200 全通过 |

## 〇-II、任务2：体验额度统一 + KB 上传奖励 + 重新发放 + 到期提醒（2026-08-31，提交 33db59c）

| 块 | 内容 |
|---|---|
| **免费体验唯一口径（2.2）** | 新建/旧配置统一为 `free_trial_tokens`（300000）/ `free_trial_days`（14）；`registration_review` 与 `trial_sentences` 运行时读取清零下线；`/api/plans` 公开返回新口径；注册/`handleGrantTrial`/审核兜底全链路统一 |
| **KB 上传奖励（2.3）** | 新增 `kb_upload_rewards` 流水表 + `GrantKBReward` 事务发放永久 token（加入口增量×单条奖励，IMMEDIATE）；单租户日封顶 `kb_upload_reward_daily_cap`（UTC）；配置键 `kb_upload_reward_tokens_per_entry`=200；含数据层单测 |
| **企业重新发放（2.4）** | 超管「重新发放体验」对所有租户常显，叠加发放一份新体验（放开幂等；customFile/deduct API 等不受影响） |
| **到期提醒 + 耗尽引导（2.5）** | 台账 `NotifiedExp3` 标记 + watchog 每日扫描提前 3 天提醒；前端余额面板 / 套餐页额度用尽引导横幅（购买月租 / 充值永久 token，锚点侧滑） |
| **顺带修复** | `kb/db.go` nil 判空（测试环境 panic 根因）；KB 日封顶 UTC 口径跨时区修复 |
| **验证** | go build/vet/test（store+api 全绿）+ npm typecheck + vite build；已部署至生产（43.108.86.140）并回归 `/status`、`/api/plans`、`/api/auth/register-config` |

## 〇、全仓端到端评审整改 + 黑盒 UAT（第四批）

| 块 | 内容 |
|---|---|
| **P0 安全** | 跨租户安全句审核越权（补 tenant_id 条件）；KB 条目 target_lang 白名单（tm_segments 列名拼接位防标识符注入）；KB 导入元信息 temp_id 格式校验 + FilePath 落 UploadDir 白名单双闸；API Key 换 crypto/rand(160bit)；wordBoundaryCache 并发写加锁 |
| **资损/双跑** | OpenAPI 建任务余额预检改双桶合计（消除 A1 口径回潮误拒台账租户）；RequeueStalledTickets 删除「不看租约年龄」的第二段释放（认领窗口双跑双扣费根因）；句数镜像 json_set 单语句原子增减 + 发放流 IMMEDIATE 事务化（含 token 入账同事务）；legacy Deduct 改守卫式条件更新 |
| **引擎** | BatchTranslate 接入统一网关（resolveModel→stage_models.ai_initial），文件管线不再绕过 model_routes；硬闸补漏循环加墙钟预算(FILE_HARDGATE_MAX_SEC 默认600s)+连续2轮零进展熔断（「译出为止」语义不变） |
| **静默失效** | ListUsersByRole 补 deactivate_at 列（13列Scan14目标恒空→超管通知链复活）；QPS/并发配额落 system_config(tenant_quota_<tid>) 且启动回放；billing_config 审计 before 值先读后写；clientIP 支持 TRUST_PROXY_XFF 取真实IP（反代限流不再全员连坐）；GDPR 擦除补 12 表+工单磁盘产物清理(EraseTenantDataFull) |
| **UAT 实测追加修复** | 强制计费余额拒绝 error_code 映射 insufficient_balance（原 rejected 违约）；refund_revoke 告警移出 IMMEDIATE 事务（跨连接写被锁吞）；退款裸 no rows 友好化；取消与认领竞态（认领前查态防覆盖 + runTicket 3s 取消监视器联动 ctx）——详见《archive/全仓端到端评审·P0缺陷与交付收口方案.md》§六 |
| **新需求** | 邀请好友前台记录：ListReferrals 补 invitee_email/paid 标记，面板新增邮箱/邀请状态/是否已付费列（中英 i18n）；行业注册通用兜底（general 包幂等创建，缺选/错选不再拒绝注册） |
| **交付物** | Python SDK success 字段 P0 修复（对现网契约必失败→可用）+ JS 错误消息对齐 + 默认轮询按 type 15s/60s；前端五修（审批台 v-for 遮蔽 t 崩溃/Login roleLevel 归一四级/core.ts abort 监听泄漏/PlansPanel NaN+style 双开标签/Audit CSV 导出带鉴权头）；systemd 沙箱(User=translator+ProtectSystem 等)+密钥 EnvironmentFile(0600)；Caddy 安全头基线+回调凭证改环境变量引用；.gitignore 废除 /*.md（交付文档回归版本库） |

## 〇-B、前端重写与租户级唯一/KB 计费（2026-08-27）

| 块 | 内容 |
|---|---|
| **React + TDesign 重写** | 删除 Vue 旧栈（frontend/），新建 frontend-react/，start.sh/build.sh 指向 frontend-react |
| **中文注释补齐** | 全量中文注释补齐（React 新栈 + backend） |
| **租户级唯一约束** | output_artifacts.path / packages.code / orders.order_no / users.ref_code 改为租户级唯一 |
| **KB 嵌入计费** | 向量索引重建按包类型分摊 token 费用，行业/语言文化等全局包免费，租户/部门包按字符比例计费到对应租户 |

## 〇-C、最新需求交付（2026-08-28，提交 d9ea334）

| 块 | 内容 |
|---|---|
| **品牌子域直载 + 登录跳转** | 品牌信息由前端按访问 host 调 `/api/branding` 直接加载（无「根域配置再覆盖」）；登录成功后后端返回 `brand_host`，若用户所属租户配置了独立子域且与当前域不一致，前端带 `?token=` 跳转该子域（需求 1） |
| **企业注册角色区分** | 企业注册拆分为「我是管理员（新建企业）/我是普通成员（受邀加入）」；成员须凭有效企业邀请码加入，无效或非企业邀请码自动降级为个人用户（需求 2、7） |
| **邀请裂变个人限定** | 邀请付费奖励（多邀得多）仅个人用户（is_personal=1）可得，企业租户后端跳过发放；邀请面板按 is_personal 区分企业/个人，企业用户隐藏多邀得多奖励与后台配置（需求 5） |
| **公开文档优化** | `/docs/sla` 增加中/英切换（localStorage 记忆）；移除页脚 STATUS 按钮；定价页改为品牌蓝主题并卡片化（需求 6） |
| **主题与组件统一** | 统一 TDesign 品牌令牌（选中态加深、主色更饱和 `#2f47f5`）；修正 ChatWindow/TicketsPage/ModeToggle/AdminDashboard 等硬编码谷歌蓝，圆角对齐 TDesign（需求 3、4） |

## 〇-D、最新需求交付（2026-08-28，提交 6a868e6）

| 块 | 内容 |
|---|---|
| **实时计费（边工作边计费）** | llm.Client.OnUsage 每次 LLM 调用（对话/嵌入）即时上报用量；Bill.Meter 始终计量（billing_enforced=0 时仅记台账不计费），余额扣除仍受 billing_enforced 控制；余额不足经 ctx 中止整次翻译任务，避免供应商被免费翻译（白嫖）。移除原有的「任务结束后统一扣费」（chargeTaskTokens），改为逐调用计量 |
| **工单进度细粒度落库** | SetTicketState 改为同步骤 UPSERT（每步骤仅一行轨迹，避免每批进度撑爆 ticket_state）；新增 started_at/duration_ms 记录每步执行耗时；初翻/校对逐段进度经引擎回调归集为 file_translate 轨迹的 init/review done/total，前端可展示精确百分比与每步耗时 |
| **全量中文注释** | 前后端代码全量补充中文注释（文件职责说明 + 函数注释），无逻辑变更 |

---

## 〇-E、OpenAPI 全功能 UAT（2026-08-28，生产验收）

生产端点 `https://langcross.lexicorn.cn`，scope=all 测试 Key（测后已轮换，旧 Key 作废）。**7/7 全通过**：

| 端点 | 结果 |
|------|------|
| `GET /openapi/v1/balance` | 200，返回 token / ≈句数余额 |
| `GET /openapi/v1/kb/stats` | 200（`kb_entries:4013`） |
| `GET /openapi/v1/billing/usage` | 200，usage 随调用持续增长（**真实计量生效**） |
| `POST /openapi/v1/translate`（同步短文） | 200，en/ja 译文正确 |
| `POST /openapi/v1/tasks`（文本） | 202 入队 → `completed`；`status` 含完整译文，`tokens_used` 已计费 |
| `POST /openapi/v1/tasks`（文件 .txt） | 202 入队 → `completed`；`download` 返回正确译文内容 |
| `POST /openapi/v1/apikey/rotate` | 200，旧 Key 立即失效（balance 复测 401）/ 新 Key 可用（200） |

说明（非缺陷，已交叉验证不影响计费）：
- 文本任务结果在 `status.translations`；其 `download` 返回 `no_result` 为预期（download 仅用于文件产物）。
- 文件任务单文件 `download` 直接回传译文内容（多文件才打包 zip，与文档「缺省 zip」措辞略有出入，功能正常）。
- 同步 `translate` 响应体 `tokens_used` 现回填真实用量（引擎注入用量收集器后由 `UsageTokens` 汇总，与 balance/usage 一致）；此前回填 0 的展示字段不一致已修复（提交 91dc8c5，R-L1）。

## 〇-F、中低优整改 + 文件翻译修复（2026-08-28，提交 91dc8c5）

| 块 | 内容 |
|---|---|
| **文件翻译质量闸** | 文件交付物（含快速模式）强制硬约束闸重翻（`gates.go` / `applySegmentGates` 传 `retry=true`）：数字/格式/非源语言/乱码不过则带反馈重翻一次，避免错误直接落入成品 xlsx |
| **源语言全角误判（成本表漏译根因）** | `DetectSourceLang` 全角数字/标点不再稀释中文判定——成本表单元格「单价￥１２３．４５」原误判为 `en`→`en` 回显、段未译出；新增 `TestDetectSourceLangFullWidth` 回归测试 |
| **xlsx 单目标原地替换** | 单目标语言文件翻译改为原地替换单元格（产物即译文），多目标仍多 Sheet；修复「打开仍是中文原 Sheet」的误解 |
| **R-M1 入账口径** | 套餐 token 入账统一 × `MarkupMultiplier`（与扣费同单位）；修正 `phase4_test` 旧断言（50000→75000） |
| **R-M2 计费防丢** | `billing/sink.go` 非余额不足瞬时错误重入队（fail-open，带 50k 上限） |
| **R-M3 支付验真** | 微信 AES-256-GCM / 支付宝 RSA2 真实加解密；明文回调拒绝；仅 `mock` 渠道需 `X-Admin-Token` |
| **R-M4~M5** | 源语言识别补日/韩/阿/俄；阶段模型（校对/Judge/文化闸门）纳入多供应商 failover |
| **R-M6~M8** | Caddy on-demand 枚举 oracle 封禁（回环 + CIDR 白名单）；CORS 默认拒绝；登录/注册限流落库（`rate_limits` 表 + 内存兜底） |
| **R-M9 / R-L1~L4** | 前端品牌平台根哨兵 0→1；OpenAPI 同步翻译 `tokens_used` 回填；SDK 下载探测 JSON 错误体改抛错；扩展可配 fast/pro；`kb_entries` 四层（术语/TM/安全句/碎片）可达 |
| **全量中文注释** | 前后端代码（go/ts/tsx/js/py）全量补/对齐中文注释（本次新增 `gates.go`、`ratelimit.go` 包注释与若干前端 i18n 注释） |

## 〇-G、优化方案修订与落地决策（2026-08-30）

> 评审《系统优化方案.md》v1.0 后纠偏：文档方向（为规模化做准备）成立，但将**已实现的 PG 双方言层、pgvector 双写、jobs 表队列**误列为"待从零开发"，导致 P0 工时/优先级失真。落地口径改为"激活既有能力 + 补齐真实缺口"，详见《系统优化方案.md》§〇。

| 工作流 | 内容 | 状态 |
|---|---|---|
| **A. PostgreSQL 切换** | 连接池配置 env（`DB_MAX_OPEN_CONNS` 等）+ 一次性迁移工具 `cmd/migrate-sqlite-to-pg` 已就绪；PG 驱动此前已 blank-import。`DB_DRIVER=postgres`+`DB_DSN` 部署切换与切流后 `RebuildKBIndex` 回填 pgvector 已落地 ✅（服务器同机自建 PG 16 + pgvector 0.6.0，非托管 RDS） | ✅ 已落地（2026-08-30） |
| **B. 邮件异步** | 复用 `internal/queue` 把同步 `mail.Sender.Send` 改为入队 + worker 发送 + 重试/死信；不引 Redis | ✅ 已落地（commit 76f4410） |
| **C. 统一错误码+结构化日志** | 新增 `internal/errors` 枚举 + `log/slog` + `X-Trace-ID` 中间件；auth 关键路径已迁移，其余渐进 | ✅ 已落地（commit 76f4410） |
| **D. 对照编辑器（新 feature）** | `translation_edits` 表 + `GET/POST /api/tickets/segments` + 前端双栏编辑器（术语高亮+逐段通过/驳回批注）；文本+文件（MVP 先 xlsx/csv/对照表，docx/pdf 二期） | ✅ 已落地（commit 76f4410，见 〇-H） |

**明确不做（当前过度设计）**：Redis Cluster / etcd / gRPC Sidecar / K8s 微服务拆分 / 多区域；SSO/SCIM/白标/CAT 插件/混沌工程/SDK 自动发布流水线——等具体企业客户或规模化运维诉求出现再做。

## 〇-H、部署验证 + 全量注释 + 安全修复（2026-08-30）

| 块 | 内容 |
|---|---|
| **生产部署验证** | 交叉编译 Linux 二进制 → scp → 备份旧版 → 替换 → 重启；验证通过（健康检查/翻译/OpenAPI/管理后台/前端加载/PostgreSQL 连接均正常） |
| **支付回调安全修复** | `handlePayNotify` X-Admin-Token 校验从仅 mock 渠道改为所有渠道统一校验（修复前 wechat/alipay 无凭证可探测订单存在性） |
| **tickets_pkey 序列修复** | PostgreSQL 序列与 tickets 表最大 ID 不同步导致异步任务创建失败，`setval` 修复 |
| **全量中文注释** | Go 后端 146 个文件 + 前端 75 个文件全量添加/标准化中文注释（文件职责说明块 + 导出函数注释 + 行内注释） |
| **UAT 全场景测试** | 42 项测试覆盖认证/翻译/KB/计费/OpenAPI/管理后台/前端/安全，核心链路全通 |

## 〇-I、SDK 鉴权统一 + 后端 Bug 修复（2026-08-30，提交 f2c0e98）

| 块 | 内容 |
|---|---|
| **go.mod 版本修复** | `go 1.26.5`（无效版本号）→ `go 1.22`，编译验证通过 |
| **config.go 环境变量 Bug** | `ONLINE_API_BASE` 环境变量被错误赋值给 `EmbedAPIBase`，修正为 `OnlineAPIBase` |
| **TypeScript SDK 统一** | 鉴权头 `X-API-Key` → `Authorization: Bearer`；裸路径 → `/openapi/v1/` 前缀；轮询间隔对齐（文本15s/文件60s）；kbStats/usage 改 POST；rotateApiKey 路径修正 |
| **Java SDK 统一** | 同 TypeScript SDK 修复项 |
| **全量中文注释** | TypeScript/Java SDK 补充完整的函数级中文注释 |

## 〇-A、历史批次索引（详情见对应方案文档）

  - **第三批（并发优化+商业化收口）**：LLM 三路信号量/Embed 批处理缓存/卡死巡检正确性/FILEPROC 子进程闸；双桶余额贯通/定价单一事实源/payments 实收/退款权益回收/download 归属/metrics 死锁修复/模型Key加密/oneid 邮箱唯一+自助注销 → 《archive/评审整改·余额贯通与商业化收口方案.md》《archive/翻译引擎并发瓶颈诊断与优化方案.md》
  - **第二批（KB 组织继承链）**：祖先链就近覆盖/兄弟隔离/跨部门降级检索/tm_segments 三元组唯一键 → 《archive/KB组织继承链与部门隔离改造方案.md》
  - **首批（架构决策与止血）**：BYOK 移除统一网关/P0 八项止血/P1 七项/Python 栈退役/git 历史清洗 → 《archive/LLM统一网关与BYOK移除方案.md》《archive/P0安全止血与并发原子性修复方案.md》《archive/旧Python后端下线与构建链收敛方案.md》
  - **更早（商业化四连等）**：双桶台账/参数化巡检/订单分流/邀请裂变/TM 自闭环/OCR 移除/PDF 两阶段管线 → 《archive/TOKEN双桶改造实施方案.md》《archive/TM自闭环与OCR移除方案.md》

## 一、当前生产状态

| 项 | 值 |
|----|-----|
| 生产域名 | **https://langcross.lexicorn.cn**（2026-08-24 起，旧域名已下线） |
| 服务器 | 43.108.86.140（阿里云；内存紧张，按 **≤1G 有效可用** 调优：GOMEMLIMIT=850MiB、MemoryMax=1150M、worker=4） |
| 服务 | `translator.service`（Go 单二进制，/status 返回 v3, ok:true）；前端已切换为 React + TDesign（frontend/ 旧 Vue 栈已下线） |
| 反代 | Caddy（自动 HTTPS），配置片段 `/etc/caddy/translator.conf` |
| 数据库 | PostgreSQL 16 + pgvector 0.6.0（同机自建，非托管 RDS）；历史 SQLite 保留于 `/opt/translator/data/backups/` |
| 计费 | Token 实时计量（每次 LLM 调用即上报用量；余额扣除受 billing_enforced 控制，billing_enforced=0 暂未启用扣费，超管随时开启；余额不足中止整次任务避免白嫖） |
| 部署脚本 | 后端交叉编译（GOOS=linux GOARCH=amd64）→ scp 二进制；前端 `npm run build` → scp dist 静态资源；ssh 重启 `translator.service`（详见 README 快速开始） |

## 二、核心能力（全部已上线）

- **多格式文件翻译**：docx/pptx/xlsx/pdf/txt/csv/md 输出译文文件；srt/vtt/json/yaml 等以对照表（xlsx）形式交付；多文件混合工单、多目标语言打包 zip
- **PDF 保真翻译管线（两阶段，a1a5aad）**：
   1. `extract`：pdf2docx 转 DOCX 并提取段落键（含表格/嵌套/文本框/页眉脚）
   2. LLM 翻译段落键 → `apply`：在缓存 DOCX 上 w:t 级替换（图片/排版零破坏）+ LibreOffice 转回 PDF（★ 图片内容按产品策略不翻译，OCR 已移除）
    - ⚠️ 已知限制与缓解：超大 PDF（`pdf2docx + LibreOffice` 转 PDF 易超时/卡死）已由上传前置拦截兜底——**PDF 体积 >40MB 或页数 >120 页直接友好拒绝并提示转 docx**；转换子进程标记为 OOM 优先受害者，超限快速失败而非挂死整机。常规 PDF 可稳定翻译，超大/扫描件仍建议优先上传 `.docx` 源文件
- **双模式**：⚡快速（AI 初翻+校对）/ 🎓专业校对（知识库+评估+硬闸全流水线）
- **计费体系（Token 实费 + 双桶台账）**：额度=发放台账（quota_grants，带到期可叠加）+ 永久余额（balance_accounts）；扣减顺序「台账近到期行→永久余额」事务原子；部门预算墙、套餐/订单/发票
- **邀请裂变**：个人邀请码+专属链接+二维码；被邀人注册→邀请者体验叠加(+30万，有效期默认14天、后台可调)；首笔付费套餐→邀请者+50万（token 数与有效期均后台可调：默认永久余额，可改为限时台账）；同对每种奖励仅一次。**仅个人用户（is_personal=1）可获得邀请奖励（含多邀得多付费奖励），企业租户后端跳过发放；邀请面板按 is_personal 区分企业/个人，企业用户隐藏多邀得多奖励与后台配置**。
- **注册与邮件体系**：自助注册拆分为「个人 / 企业」两类（个人注册自动生成租户编码与名称并标记 is_personal；企业注册进一步区分「我是管理员（新建企业）/我是普通成员（凭有效企业邀请码加入）」，无效或非企业邀请码自动降级为个人用户）。企业注册发送欢迎邮件并抄送管理员邮箱。超管可在后台「邮件模板」面板配置多用途模板（注册验证码 / 密码重置验证码 / 企业注册成功提醒 / 租户管理员通知 / 系统告警 / 产品手册）；注册成功自动向用户发送《产品手册》PDF 邮件，附件读取外部 PDF 文件（默认 `/opt/translator/data/manual.pdf`，可用 `system_config.manual_pdf_path` 或环境变量 `MANUAL_PDF_PATH` 指定），经专用邮箱 `info@lexicorn.cn` 发送；中文邮件主题用 RFC2047 编码、正文 base64 编码。
- **TM 自闭环**：tm_review 待审池唯一入库通道（超管人工审核通过才落正式 TM）；bitext/tmx/反馈修正/命中达标四来源候选
- **开放 API**：`POST /openapi/v1/tasks` 异步任务 + 轮询 status/download + balance；AES-GCM 密钥加密与一次性明文展示
- **组织架构**：平台根→租户根→组织→部门四级树、拖拽调层级、部门预算徽标弹窗、邀请码绑定组织
  - **管理后台**：三工作台（超管/租管/部门管）、租户切换器、OpenAPI 文档在线编辑（双语）、审计日志、告警中心、记忆审核台、**任务中心（用户领永久 token + 超管自定义每日/一次性任务）**、**个人中心（邀请好友 + 任务中心）**、**外部调用（开放 API + 回调通知）**、**协议签署并入系统设置**（2026-09-03 菜单重组，详见 〇-XI）
   - **品牌定制与子域名**：按子域名前缀解析租户品牌（名称/Logo/子域）；Caddy on-demand TLS 自动签发证书（需 DNS 通配符 A 记录 `*.lexicorn.cn → 服务器 IP`）；品牌信息前端按 host 直接调 `/api/tenant/branding` 加载（无根域覆盖）；登录成功后自动跳转至所属品牌子域（后端返回 `brand_host`）。品牌定制为付费套餐功能（有效付费套餐或超管授权方可编辑，未满足仅可查看）；登录页支持两种布局——① 全屏背景（登录卡片浮于其上，无遮罩）② 左右分栏（容器可在左/右，另一侧为图片）；登录卡片与背景图位置均可在品牌管理页拖拽定位并保存；语言切换（中文/EN）为全局设计，内嵌于登录容器右上角
- **Office 划译插件**：Word 侧加载 taskpane，选区翻译插回文档
- **运维护栏（1G 内存红线，2026-08-28 优化，2026-09-04 复核）**：`GOMEMLIMIT=850MiB`、`MemoryMax=1150M`、worker=4、LLM 并发 2（禁 HTTP/2 治流挂起）；**文件翻译防卡死**：PDF 体积>40MB 或页数>120 前置拦截 + 友好提示；转换子进程 OOM 优先受害者 + 可选 `FILEPROC_RLIMIT_AS_MB` 硬上限；**并发写零 SQLITE_BUSY**：实时用量计量改为内存累积 + 周期(2s/200条)按租户单事务批量落库（写事务从每秒 N 个降到每周期每租户 1 个），并用 `usage_daily` 计数器表替代每次请求的 ledger `LIKE` 全扫；产物留存 14 天+到期提醒、pending 订单 15min 自动关闭、低额提醒巡检

## 三、近期关键修复（2026-08-24~27）

| 提交 | 内容 |
|------|------|
| 0ea5dac | KB 五档可见范围模型 + 跨部门包(cross_dept)独立类型（cross_orgs/cross_all 部门集合，维护与使用权限按涵盖部门收窄，导入/写条目/删除均经 deptKBScope）；embedding 供应商切 SiliconFlow BAAI/bge-m3(1024维) 移除硬编码 embedding-2；pgvector 后端(UpsertEmbedding/VectorSearch/RebuildKBIndex 双写，语义检索优先向量、回退 ScopedSearchScope)；前后端全量中文注释随本次提交补齐（覆盖整个代码库） |
| c5bbdc0 | 后端全量中文注释（39 文件头 + 31 函数文档）+ gofmt |
| a1a5aad | PDF 两阶段翻译重构：修表格不译/图片丢失/图后内容丢失三大缺陷（w:t 级替换、含图 run 保护、lxml id 去重陷阱） |
| e435a5f | python-docx runs 代理对象复用修复；零宽字符归一化；域名切换 |
| 7a7d459..17422d7 | 工单删除按钮+后端级联删除；气泡式进度面板（智能上下定位）；i18n 补齐 |
| 3b7047d..e883d8a | TM 自闭环全量落地（待审池/审核台/计数钩子）；OCR 全量移除；OpenAPI 文档口径统一；文本任务单引号 JSON 宽松解析 |
| d4b9d6e..9b4dd59 | 文件管线回显检测+硬闸重试（每段独立重翻最多 2 轮）；弹窗 Teleport 兼容加固；前台菜单受控下拉修复；LLM 客户端禁 HTTP/2 治 siliconflow 流挂起 |
| 1200dce..0ae3694 | 前台汉堡菜单双事件保险；改密/改邮弹窗与后台对齐、双验证码、邮箱必填全局唯一 |
| 614fe8f | CommitA 双桶台账：quota_grants + DeductWithGrants 顺序扣减；注册礼包 30w/14d 入台账 |
| bcdf7b6 | CommitB 商业化参数默认值落库（面板可调）；pending 订单 15min 自动关闭；低额提醒巡检（24h 去重） |
| 765d21d | CommitC 订单确认按 ptype 分流：paid→t+30 台账 / increment→永久余额 / free 维持旧句数通道 |
| 194211c+7225bfa | CommitD 邀请裂变全量：存储层首绑闸门/叠加发放/付费去重 + my/qrcode 接口 + 注册绑定与付费奖励钩子 + 前端邀请面板；修复 RewardPaidPermanent 租户取错、套餐订单 amount_tokens=0 致入账 0、QuotaGrantMigrate/ReferralMigrate 未挂载三处存量缺陷 |
| 1e37128 | 今日改动范围全量中文注释补全（后端 32 文件 + 前端 10 文件，无逻辑变更） |
| 6d85e1b | React + TDesign 前端重写（frontend/ Vue 旧栈下线，frontend-react/ 新建，start.sh/build.sh 切到新栈）；React 新栈与 backend 全量中文注释补齐；租户级唯一约束（output_artifacts.path / packages.code / orders.order_no / users.ref_code）；KB 嵌入向量索引重建按包类型分摊 token 费用，全局包免费、租户/部门包按字符比例计费 |
| 9fa17af | 品牌定制按子域名前缀解析租户；Caddy on-demand TLS 自动签发证书（配合 DNS 通配符 A 记录）；品牌定制改为套餐付费功能（有效付费套餐或超管授权方可编辑，未满足仅可查看并提示）；新增超管为指定租户开通品牌定制接口 POST /api/admin/tenant/brand-grant；前后端代码补充全量中文注释 |
| 709a0e9 | 登录页双布局（全屏背景 / 左右分栏，容器左右可切换）；背景图样式（缩放/位置/充满-适应）与登录卡片位置均可在品牌管理页拖拽保存；登录/注册/忘记密码三视图互斥；语言切换按钮内嵌登录容器；前后端补充全量中文注释 |
| ffe9312 | 注册拆分为个人/企业用户（个人 is_personal 可获邀请奖励，企业注册发欢迎邮件并抄送）；新增超管可配邮件模板（6 类）与后台「邮件模板」面板；注册成功自动发送产品手册 PDF 邮件（附件读取外部 PDF，info 专用邮箱发送）；修复中文邮件编码（RFC2047 主题 + base64 正文），mail 支持 CC 与 multipart/mixed 附件；前后端补充全量中文注释 |
| d9ea334 | 品牌子域登录跳转（brand_host 跨域带 token）；企业注册区分管理员/普通成员，成员须凭有效企业邀请码、无效码降级个人；邀请付费奖励仅个人用户可得；/docs/sla 中英切换 + 移除 STATUS 按钮 + 定价页品牌化；主题统一品牌蓝、选中态加深、修正硬编码谷歌蓝 |
| 6a868e6 | 实时计费（边工作边计费）：OnUsage 逐调用计量、余额不足中止任务；工单进度细粒度落库（UPSERT + started_at/duration_ms + 初翻/校对逐段进度）；前后端全量中文注释 |
| bfc982b | **不换库性能优化**：根治大 PDF 卡死（15MB/120页前置拦截 + 子进程 OOM 优先受害者 + GOMEMLIMIT=650Mi + FreeOSMemory）与多人并发 SQLITE_BUSY（实时计量批量落库 + usage_daily 计数器 + 进度/TM 批量写 + 缓存容量上限）；**修双重计费资损**（移除 chargeTokens 二次扣费，实时钩子为唯一扣费源）与用量看板 user_id 归属失真（ctx 透传） |
| 91dc8c5 | **中低优整改 + 文件翻译修复**：文件交付物强制硬闸重翻；DetectSourceLang 全角误判修复（成本表漏译根因）；xlsx 单目标原地替换；R-M1 入账×markup / R-M2 sink 重入队 / R-M3 支付真实验签解密 / R-M4 日韩阿俄识别 / R-M5 阶段模型 failover / R-M6 Caddy 枚举封禁 / R-M7 CORS 默认拒绝 / R-M8 限流落库 / R-M9 品牌根哨兵 / R-L1 tokens_used 回填 / R-L2 SDK 下载探测 / R-L3 扩展可配 fast·pro / R-L4 kb 四层可达；前后端全量中文注释 |

## 四、技术要点备忘

- **PDF 字体**：服务器装 `fonts-noto-cjk`（NotoSansCJK-Regular.ttc，拉丁+CJK 全覆盖）；`PDF_FONT_PATH` 指向它。旧 DroidSansFallbackFull.ttf 无拉丁字形（渲染为框框），勿再使用
- **Python 依赖**：`/opt/translator/.venv` 内 fpdf2/pdf2docx/python-docx/Pillow/fonttools；系统需 poppler-utils、libreoffice-writer/impress/calc。★ tesseract/pytesseract 已随 OCR 移除卸载，勿再装回
- **双桶台账**：额度唯一扣减入口 DeductWithGrants；paid 订单按「包内句数×estimate_tokens_per_sentence(默认500)」折算入台账（订单 amount_tokens 恒为 0，不可直接用）
- **前端弹窗规范**：应用内弹窗统一走 TDesign `Dialog`/`DialogPlugin`（`confirmDialog`/`promptText` 封装于 `frontend-react/src/components/uiDialogs.tsx`，取消按钮触发 `onClose` 保证 resolve(false)）；禁用浏览器 alert/confirm 于关键交互
- **lxml 陷阱**：元素代理对象回收后 id() 复用，严禁按 id() 去重节点
- **python-docx 陷阱**：`para.runs` 每次访问返回新代理列表；`run.text=` 会删除该 run 的 drawing/pict 子元素
- **SQLite 并发红线（仅本地开发/旧库适用；生产已切 PostgreSQL，DB_DRIVER=postgres）**：DSN `_txlock=immediate` 全局生效；事务内严禁经独立连接再写库（会撞 busy_timeout 静默失败——UAT-2 教训）；句数镜像一律 json_set 原子语句或 IMMEDIATE 事务
- **新增环境变量（第四批）**：`TRUST_PROXY_XFF=1`（反代取真实IP，直连勿开）；`FILE_HARDGATE_MAX_SEC`（硬闸补漏墙钟预算，默认600s）
- **邮件相关环境变量**：`MAIL_ENABLED` / `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS`（默认发信箱 `noreply@lexicorn.cn`，SMTP 端口 465）；`INFO_SMTP_ENABLED` / `INFO_SMTP_USER` / `INFO_SMTP_PASS`（产品手册等专用发信箱 `info@lexicorn.cn`，默认 `smtp.mxhichina.com:465`）。均在 systemd `translator.service` 的 `Environment` 中配置。
- **注册行业口径**：缺选/错选行业→通用行业(general)兜底不再拒绝；通用包由 EnsureDefaultPackages 在租户1幂等创建

## 〇-H、对照编辑器（工作流 D，2026-08-30 新 feature 落地）
- **后端**：`translation_edits` 表（store.go 迁移 + store/edits.go 读写方法，按 ticket_id+lang+seg_index 唯一）；新增 `internal/api/editor.go`：GET/POST `/api/tickets/segments`（?id=&lang=），租户隔离（超管可跨租户）。
- **段落提取**：文本工单解析 `FinalResult.translations` 按行对齐；文件工单解析 xlsx/csv 对照表产物（docx/pdf 等二进制为 `unsupported`，二期）。
- **术语高亮**：GET 响应带回租户术语表 `terms`，前端 `<mark>` 高亮命中串。
- **写入语义**：逐段 upsert edited_text/status(pending/approved/rejected)/note；approve→TM 回写默认关闭（MVP 仅落库，二期可配置）。
- **前端**：`src/components/EditorPage.tsx`（TDesign 双栏：源文只读+术语高亮 / 译文可编辑+状态+批注），`App.tsx` 新增「✍️ 对照编辑」Tab；`api/tickets.ts` 加 `getSegments`/`saveSegments`。
- **验证**：`go build ./...` 通过；`internal/api` 单测（splitLines/locateColumns/extractTextSegments）通过；`npm run typecheck` 与 `vite build` 通过。
- **状态**：已于 commit `76f4410` 落地并 push 至 `origin/main`（不含文档/流程图）。

## 六、解锁 PG + Redis 及路线图落地（2026-08-30）

> 详见《改造方案_解锁PG与Redis及路线图.md》§18/§19 联调验证记录。**路线图 13 阶段已全部交付**，PG + Redis 已在 Seoul 服务器部署落地。

- **阶段一 PostgreSQL 切流**：✅ 已完成。Seoul 服务器 PG 16 + pgvector 0.6.0（同机自建），`migrate-sqlite-to-pg` 迁移 + `backfill-embeddings` 写入 3,347 条向量，`/status` 返回 `dialect:"postgres"`。
- **阶段二 Redis**：✅ 已完成。`internal/infra/{redis,distlock,ratelimit,concurrency}` 自研落地；LLM 信号量/API Key 日配额/工单巡检锁接 Redis；`infra_integration_test.go` 联调全 PASS；运行时 `[init] Redis 已启用` + `/status` ok。
- **阶段三/四 监控/日志**：`deploy/observability/{prometheus,alertmanager,grafana,promtail}` 配置交付；需 Grafana/Loki/Prometheus 实例点亮（runbook 见该目录）。
- **阶段五 对照编辑器 docx/pdf**：`internal/doc/office.go` 纯 Go docx 段落抽取+回写；pdf 经 python venv pdf2docx 桥接；`editor.go` 抽取+`/api/tickets/segments/export` 回写修订稿。单测 PASS。
- **阶段六 SSO/OIDC**：`internal/auth/sso`（OIDC 发现 + 飞书/钉钉 OAuth2）+ `internal/api/sso.go`（`/api/sso/login|callback|providers`）+ config `SSO_PROVIDERS`/`SSO_FRONTEND_URL`。单测 PASS；运行时 providers 列表验证通过。
- **阶段七 多 AZ**：`internal/queue/notifier.go` + `internal/infra/redis/notify.go` Redis 唤醒跨实例 worker + `deploy/multi-az/README.md`（PG 流复制/Redis 高可用/Caddy 亲和/systemd 多实例）。
- **阶段八/九 压测/E2E**：`deploy/loadtest/k6.js` + `frontend-react/{playwright.config.ts,e2e/smoke.spec.ts}` 配置交付。
- **阶段十 API 版本化**：`withAPIVersion` 中间件（`Accept: application/vnd.langcross.v1+json` / `X-API-Version`，默认 v1，v2 预留）。
- **阶段十一 OpenAPI Spec**：`internal/api/openapi.v1.json`（go:embed）+ `/openapi/v1.json` 端点（与 Python SDK 契约一致）。
- **阶段十二 SDK 多语言**：`sdk/{python,typescript,java}` 三语言客户端，同一 OpenAPI 契约。
- **阶段十三 审计留存**：`Store.PruneAuditLogs` + `AuditRetentionDays`（默认 365，system_config 覆盖）+ 每 6h 定时 prune。
- **离线未端到端验证项**（代码/契约已就绪，接入环境即启用）：真实 IdP 授权码交换、PDF 抽取（需 venv pdf2docx）、多实例跨机分发实测、Grafana/Loki 实例点亮、k6/Playwright 实跑。

## 五、文档索引

- [部署指南.md](部署指南.md) — 构建/部署/systemd/Caddy/依赖清单
  - [未完成项目.md](archive/未完成项目.md) — 待办与外部依赖项
  - [待解决问题.md](archive/待解决问题.md) — 问题跟踪（含已解决归档）
  - [权限关系.md](权限关系.md) — 角色层级与数据可见性矩阵
  - [全仓端到端评审·P0缺陷与交付收口方案.md](archive/全仓端到端评审·P0缺陷与交付收口方案.md) — 第四批整改设计+UAT 实测记录（含 4 个 UAT 缺陷修复）
  - [TOKEN双桶改造实施方案.md](archive/TOKEN双桶改造实施方案.md) — 双桶台账数据模型/扣减算法/参数（已全部落地）
  - [TM自闭环与OCR移除方案.md](archive/TM自闭环与OCR移除方案.md) — TM 唯一入库通道与 OCR 移除决策记录
  - [评审整改·余额贯通与商业化收口方案.md](archive/评审整改·余额贯通与商业化收口方案.md) — 双桶余额贯通/插件CORS/财务口径/产物归属/安全加固二期（含硬闸重试特性确认）
   - [翻译引擎并发瓶颈诊断与优化方案.md](archive/翻译引擎并发瓶颈诊断与优化方案.md) — LLM 三路信号量/Embed 批处理缓存/卡死巡检正确性/子进程资源闸/QoS 车道

## 九、Bug 修复记录（2026-08-30）

### 9.1 Admin 账号无邮箱导致绑定弹窗死循环
- **根因**：`EnsureAdmin` 调用 `CreateUser` 时未传入邮箱，admin 账号 `email` 字段为空
- **现象**：admin 登录后前端检测到空邮箱，弹出不可关闭的 `EmailBindModal`，无论是否输入邮箱都无法正常使用
- **修复**：
  - `EnsureAdmin` 新增 `email` 参数，创建/更新时同步设置邮箱
  - `main.go` 新增 `ADMIN_EMAIL` 环境变量，传入 `EnsureAdmin`
  - 登录响应增加 `email` 字段，前端可直接检测
- **文件**：`backend-go/internal/iam/store.go`、`backend-go/cmd/server/main.go`、`backend-go/internal/api/auth.go`、`backend-go/internal/store/users.go`

### 9.2 验证码收不到（NoopSender 模式）
- **根因**：Seoul 服务器未配置 `MAIL_ENABLED=1` 和 SMTP 凭据，`mailer()` 返回 `NoopSender`，验证码仅打印到服务端日志，不会真正发送到邮箱
- **修复**：
  - `sendEmailCode` 在 `noop=true` 时返回明确提示："验证码已生成（测试模式，请查看服务端日志）"
  - 清理 `sendEmailCode` 中未使用的死代码 `sender` 变量
  - 修复 `pwd.codeNoop` 翻译（之前错误显示"请先发送验证码"，现在正确显示测试模式提示）
- **真正收信需配置**：`MAIL_ENABLED=1` + `SMTP_HOST/PORT/USER/PASS`（Seoul 已配置 SMTP，代码已更新并部署）
- **文件**：`backend-go/internal/api/email_verify.go`、`frontend-react/src/i18n/dicts.zh.ts`、`frontend-react/src/i18n/dicts.en.ts`

### 9.3 平台根 admin 账号无法发放试用
- **根因**：`handleGrantTrial` 拒绝 `tenant_id <= 0`，且 `main.go` 仅初始化 `tid=1` 不初始化 `tid=0`（平台根账号）。平台根 admin 无余额账户、无试用额度，且后台无法发放
- **修复**：
  - `main.go`：增加 `EnsureBalance(0)` 和 `EnsureDefaultPackages(0)` 初始化平台根账号
  - `handleGrantTrial`：`req.TenantID <= 0` 改为 `< 0`，新增 `tenant_id=0` 特殊分支直接发放试用额度（无需 tenants 表记录）
- **文件**：`backend-go/cmd/server/main.go`、`backend-go/internal/api/register.go`

### 9.4 超管被 `registration_review` 闸住无法翻译
- **根因**：`billing_api.go` 的 `gateUsage` 在 `registration_review=1` 时要求当前租户存在有效套餐/试用，但超管 `currentTenant` 返回 `1`，同样被闸
- **修复**：`gateUsage` 中 `registration_review` 检查增加超管豁免：`auth.IsSuperAdmin(u)` 为真时直接跳过
- **文件**：`backend-go/internal/api/billing_api.go`

### 9.5 验证码邮件仍无法送达（已解决）
- **当前状态**：✅ 已解决。Seoul 服务器 `MAIL_ENABLED=1`、SMTP 凭据已配置；Python 直连 `smtp.mxhichina.com:465` 发信成功（认证+发送均 OK）。**邮件已可真实送达**：生产 `jobs` 表可见注册验证码/试用额度发放等 `mail_send` 任务全部 `done`，收件人含 `noreply@lexicorn.cn`、`info@lexicorn.cn` 系列真实邮箱。
- **已做**：
  - 已部署邮件流程日志（`enqueueMail`/`syncSendMail`/`SMTPSender.Send` 均加 `[mail]`/`[smtp]` 日志）
  - 已确认进程环境变量 `MAIL_ENABLED=1`、SMTP 参数正确
  - ★ 2026-08-31：「我已付费」等关键告警邮件链路补齐——`notifyAlert` 支持 `alert_email_cc` 抄送 + `enqueueMail` 异步队列；`alert_email=noreply@lexicorn.cn`、`alert_email_cc=575160894@qq.com` 已落库，端到端验证邮件入队含抄送
- **备注**：若个别收件域仍不进信，检查发件域 `lexicorn.cn` 的 SPF/DKIM/DMARC 与垃圾箱。
