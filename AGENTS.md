# AGENTS.md — 工程约定（本仓全体贡献者／AI 助手必读）

> 建立于 2026-09-17（改造 2 第二步）。这里只记**必须遵守的硬约定**，不重复产品文档。
> 详细架构与部署见 [README.md](README.md)、《部署指南.md》。

---

## 一、代码组织约定

### 1. store 冻结规则（★ 强制）

`backend-go/internal/store/` 是单一数据访问包（57 个非测试文件 / 约 16.5k 行；含 57 个单测文件为 114 文件 / 约 26.9k 行），
被 `internal/api/` 中 51 个非测试文件直接 import（2026-09-29 实测校准；api 包自身现有 93 个非测试文件）。
为控制复杂度增长，确立以下冻结规则：

1. **`billing.go`（2317 行）与 `kbpackages.go`（1371 行）只减不增**（行数口径＝含注释的非测试行数，规则本身指**不追加新方法**；2026-09-29 实测校准。billing.go 的行数增长全部来自原地改既有方法体与写约束注释——2026-09-29 D-1 并发透支修复即属此类，diff 里零个新增 `func`；确要加新方法就按下面的规则新建文件）。**
   新方法按域新建文件（`billing_refund.go` / `kbpackages_acl.go`），或归入已有窄域文件。
   **禁止**往这两个文件追加新方法。
2. **`store.go` 不承接业务方法**，只保留连接/迁移编排与真正的通用工具。
3. **通用能力下沉到基础包**（`internal/secret`、`internal/db` 等），store 用薄委托引用；
   **禁止基础包 import `internal/store`**（会产生循环依赖，且让「读一个配置」被迫拉起整个存储层）。
4. **新增列走幂等补列**：`db.EnsureColumns(...)`，仿 `store.TicketQualityFlaggedMigrate()`；
   禁止一次性 `ALTER TABLE` 直执行。
   迁移**唯一入口**即 `db.EnsureColumns`/`db.ExecDDL`——原 `internal/db/migrate.go` 的 Runner
   影子框架（版本表 `schema_migrations` + `RegisteredMigrations` 模板）生产零调用，
   已于 2026-09-18 P2-2 删除，禁止重建（`internal/db/guard_test.go` 的 `TestNoShadowMigrationRunner` 会红灯）。
5. 域索引与完整规则见 [backend-go/internal/store/README.md](backend-go/internal/store/README.md)。

### 2. 日志与观测

- 后端统一用 `internal/observability` 的 slog（JSON + trace_id），**不要用标准库 `log.Printf`**。
- 子服务（如 assist-server）也必须接同一口径，禁止自建日志格式。
- 存量 `log.Printf` 受棘轮闸门约束**只减不增**：`internal/observability/logratchet_test.go`
  （分根设基线 `internal` 176 / `cmd` 69；2026-09-29 实测 internal 真计数恰=176，即 D-2 批迁移 6 处后**回到基线而未下调**——
   想再降就要再迁一批并同步改 `logPrintfBaselines`，别只改数字。计数口径注意：闸门按「行」数且**跳过 // 注释行**，
   而 `grep -c log.Printf` 会把说明注释一起算进来（同文件实测 182 vs 176），拿 grep 读数去对基线必然误判。
   2026-09-18 P2-3 建立、2026-09-22 #42 纳入 `cmd/` 盲区并
  随迁移下调 `internal` 基线；总数不设基线，防「cmd 新增被 internal 下降掩盖」）。
  迁移优先级 engine → fileproc → orchestrator，按包随改动顺带清，
  每降一批同步下调该文件里的 `logPrintfBaseline`。

### 3. 密钥与配置

- 密文配置一律 `internal/secret.EncryptSecret` / `DecryptSecret`（`enc:v1:` 前缀，兼容历史明文）。
- 明文优先序：**环境变量 > 数据库配置**。密文 key 在管理台以掩码（含 `****`）回显，
  保存链路用 `IsSecretMasked` 判断并回填旧值，禁止把掩码写回库。
- ★ **运行期配置的「生效时刻」只有一份尺子**（★ 2026-10-05 〇-AR 第 5 波立为硬约定）：
  全局 LLM 配置（`online_api_base/key/model` ＋ `model_routes` 主路由）的一切读点一律走
  `internal/llmsource` 的 `Current(ctx, store, cfg)`，**禁止**新增直接读 `config.C.Online*`／
  `s.Cfg.ModelRoutes` 的运行期读点，也禁止在任何调用方再造第二套优先级（优先级顺序、掩码判定、
  占位 Key 判定、指纹口径都只在那一个包里）。
  写侧（管理台保存）**必须先落库、落成了才碰进程侧、再发布快照**；写库失败即返回错误且进程侧一字不动
  ——反过来（先改内存再写库）会把「保存失败」写成「这台已经生效了」，而另一台还在打旧 Key。
  启动期水合与运行期懒探共用同一个构造函数，不许出现「启动有一套合成逻辑、热加载有另一套」。
  懒探的三个口径必须同时成立：TTL 节流（`LLM_CONFIG_RELOAD_TTL_SEC`，默认 5s）＋
  **指纹比对**（库内原值与进程侧档位一起算，无变化不发布）＋**换库即视为指纹失效**
  （用假库/换库测，否则测试里换 store 永远探不到）。库读不到 ⇒ **沿用上一份快照**，不许按 cfg 重发
  （重发＝把一次网络抖动打成「Key 被洗回占位」的全局降级）。
  理由：这条链的缺陷从来不是「某处读错值」，而是**同一份库配置在六处对应六个时刻**——
  现网表现是「运营换了 Key，某条腿还在打旧 Key」，而评估这类静默腿坏了没人看见
  （〇-AR 第 1 波 R-1、第 5 波㊻两批真踩；跨实例那一层的实证＝`models/save` 只刷新写入进程自己的
  内存快照，第二台实例带启动期占位 Key 恒 401，旧的多实例闸门因此**结构性测不到**这一层，
  现由 `multi_instance_e2e.sh` 的 M5 段补上：第三台实例不带 LLM env 启动，须在 A 侧保存后自行翻可用）。
- ★ **「谁优先」的判据只能是构造期来源锚点，不是当前值的形状**（★ 2026-10-05 〇-AR 第 5 波补腿，现网演示单元实证）：
  「环境变量 ＞ 数据库」这条短路必须问 `config.Config.OnlineAPIKeyOrigin`（`OriginAPIKey*` 四档，构造期由
  `config.Default()` 钉一次），**禁止**再用「值非空＋`OnlineAPIKeyIsPlaceholder` 为假」这类**派生态**倒推来源——
  水合与管理台保存都会把这两条翻成"像 env"，于是那台被永久钉死在启动那一刻水合到的值上，库里再改也不跟，
  而热加载日志还写着 `from=env`（现网读数：演示单元启动日志明写「未配置 SILICONFLOW_API_KEY，已生成随机占位 Key」，
  同一台的热加载行却出 `from":"env"`）。三条配套口径：
  ① **值与来源必须在同一处同时回写**（唯一写点 `Snapshot.ApplyTo`），只写值＝标签与真来源脱钩，
     下一位排查的人顺着假标签找一整晚；来源字段还**不许留空串**（`Default()` 的 else 支显式钉 `none`，
     别让"没写过"和"没来源"同形）；
  ② 日志里的 `from` 档位是**对外排障契约**，与功能同等对待，新加这类字段要同步配一条"不许冒充"的断言；
  ③ 断言的射程要按**"有没有把水合值写回 cfg"**分派：只测"启动时库里没 Key、后来配上"的那一台对这条短路完全无感
     （那台的占位标记一直是真），必须另有一条"先水合、后改库"的锁——
     单测＝`cmd/server.TestHydratedKeyIsNotRecordedAsEnv`＋`llmsource.TestHydratedValueNeverMasqueradesAsEnv`，
     跨进程＝`multi_instance_e2e.sh` M5 段的 **D 实例**（启动时库里**已经有**可用配置那一形态，四条读数：
     D1 启动即水合可用／D1b 此刻快照仍是库里旧值／D2 第三次改动跟得上／D3 新增热加载行的档位不许是 env）。
  ④ **同一个接口的两个字段可以来自两个不同时刻**（★ 同批踩到，是 M5 段第一条假绿的机制）：
     `/api/admin/models` 的 `model` 对象读**这台此刻的快照**（懒探，默认 5s TTL），`routes` 数组读**库里原文**
     （编辑面必须显示"存着什么"，含被快照停用的坏路由——抹掉会让运营下一次整表保存把它永久删掉）。
     ⇒ 凡问"这台**在用**什么"的判据，必须**取那一个字段做等值**（脚本里的 `snap_model`），
     **禁止**拿整段响应体做子串匹配——运营一保存响应体就带新名字，于是在 TTL 窗口内**结构绿**
     （实测读数：D 启动后 0.8 秒 `body_has_new_name=1` 而 `snapshot_field=旧值`、热加载日志 0 条，那条弱判据判绿）。
  ⑤ **本机上一轮写回 `cfg` 的值同样不是一种来源**（★ 2026-10-05 〇-AR 第 6 波，现网演示单元实证）：
     「库里 `online_api_key` 非空」这一档的收尾判据若写成 `!placeholder && key != ""`，那个 `key` 很可能就是**这台自己**
     上一轮 `ApplyTo` 写回去的（第 5 波那条"水合值写回 cfg"的副产品），于是库里已经把凭证删干净了这台还在认它，
     档位读数永久停在 `db` 不动（现网读数：库里**没有** `online_api_key` 行，热加载行却出 `from":"db"`）。
     ⇒ 修法只有"在进 ②③ 档之前把非 env 锚点那份 Key 清零"这一种形态，**只清 Key**，
     `Placeholder`／base／model 一概不动（它们不是来源判据，清多了就把降级链打歪）；来源判据仍然只认
     env 锚点与启动期占位 base 这两档，唯一写点仍然只有 `Snapshot.ApplyTo`。四条配套口径：
     · **「改了跟得上」≠「删了洗得掉」**——第 5 波那批锁全测前者，所以对这条完全无感；撤销态必须用
       `UPDATE system_config SET value=''`（**不是删行**：「行不存在」与「行是空串」在库里是两种形态，
       删行造出来的是第三种、测不到这一档），并配一条"库真写了 Key ⇒ 档位必须翻回 `db`"的反向对照，
       否则清零块被摘掉也能绿灯。单测＝`llmsource.TestRouteHydratedValueNeverMasqueradesAsDB`，
       跨进程＝`multi_instance_e2e.sh` **M5-E 段**（E1 档位与库内容同刻等值／E2 清 Key 后新增热加载行的档位／E3 清 Key 后仍译得出）。
     · **期望值现读现推**：档位判据的期望值必须从"那一刻的库"用同一条三档 `CASE` 算出来，
       禁止在脚本顶部写死 `'db'`——库里值会被同段前面的断言改掉，写死就恒红或恒绿。
     · **懒探的观测腿必须自带读者**：跨进程锁若只 `grep -c` 等日志里新增一行热加载，而没人打任何一个读点，
       那行**永远不会出现**（实测：首跑 `HO0=1 HO1=1`、等满 24s 判红，把"没人来读"写成"没重探"）。
       等待循环里必须自己拨一次读点（`snap_model`），并把 FAIL 拆成「没有新增行」／「有行但取不到档位字段」／
       「档位不对」三档——三档混成一条就永远分不清是没探到还是探到了却撒谎。
     · 测试夹具里"声明了可用 Key 却忘了钉来源"会退化成"这台没有来源"（`evals.TestResolveJudgePriority` 那条红
       属于**暴露**不是回归，补 `cfg.OnlineAPIKeyOrigin = config.OriginAPIKeyEnv` 即可，判据一字不动）。
  ⑥ **现网探针写库前必须先问「产品读的是哪一把键」，且"必须回落"类判据要先确认该面的基线值**（★ 2026-10-05 〇-AR 第 6 波换件后现网实证，三条都是探针自伤、非产品缺陷）：
     · 往 `system_config` 写一把**不在 `llmsource.probeKeys` 四把清单里**的键（实测踩的是把 `online_model` 写成 `online_api_model`），
       恒不翻档是**正确行为**——不在清单里的键不进指纹、没有读点。第一遍因此等满 30 s 判成"热加载没跟上"，回滚重来。
       ⇒ 写库类探针的键名一律从 `internal/llmsource` 的常量抄（`KeyOnlineModel`＝`online_model`），禁止按字段名直觉拼。
     · **判"回落对不对"要先问那一个访问面按它自己的输入本来解析到什么**：`smoke_brand_homepage.sh` 的 G 段写的是
       "匿名 `?tenant_id=1` 必须回落 `tenant_id=0`"，这在主站成立，但演示域**按 Host 本身就解析到租户 1** ⇒
       无论 F-79 生不生效都恒回 1，属**结构性空转锁**。有判别力的写法＝点名一个库里不存在的号（`?tenant_id=999`），
       问"它有没有被采纳"；实测三域 `0→0`／`1→1`／`1→1`＝参数一律被忽略，F-79 才叫现网坐实。
     · `curl -w '%{http_code} %{content_type} %{size_download}'` 里 **`content_type` 本身含空格**
       （`text/html; charset=utf-8`），词分割后 size 那格读到的是字符串 `charset=utf-8` ⇒ 拿它和首页字节数做等值**恒假**
       （本轮两条 `/.DS_Store` 负向锁因此各假红一次）。多点判据要么**分格各取一次**，要么给字段套引号/换分隔符。
     · 同族一条：数"被调脚本的失败行"必须按**它实际写的字符**数（`smoke_tenant_domain.sh` 用 `✖`，不是 `✘`），
       抓错字符会把"反向对照有效"读成"判据射程可疑"。

### 4. 数据库方言

- 所有 SQL 必须同时支持 SQLite（本地/CI 快跑）与 PostgreSQL（生产）。
  用 `db.CurrentDialect()` 分支或 `db.Exec(s.db, db.CurrentDialect(), ...)`，禁止硬编码方言语法。
- 列迁移、`COALESCE` 取值、时间函数是历史高频踩坑点。
- **`DELETE`／`UPDATE` 一律不许带尾随 `LIMIT`（★ 2026-10-03 〇-AP 补腿立）**：挂件与本地用的
  `modernc.org/sqlite` 驱动**没开 `SQLITE_ENABLE_UPDATE_DELETE_LIMIT` 编译选项**，`DELETE … WHERE … LIMIT ?`
  在解析期就退 `SQL logic error: near "LIMIT": syntax error (1)`（PG 同样不认这种写法）。
  分批删除的唯一可移植形态＝**把 LIMIT 放进子查询**：
  `DELETE FROM t WHERE id IN (SELECT id FROM t WHERE … LIMIT ?)`（现网实证：匿名会话清理自上线起每小时恒失败一次，
  见《部署指南》〇-AP 那条）。判"某句 SQL 在两种方言都能跑"必须靠**真驱动的单测跑一次**，不靠语法直觉。
- **新增/改动后端单测必须自钉方言**：`config.Default()` 读 `DB_DRIVER` env 且**副作用写全局 `config.C`**，
  run_uat PG 模式下会泄漏方言给同包后续内存 SQLite 测试（`no such table: information_schema.tables` 假红，
  2026-09-19/20 两连败）。模板：`old := config.C; cfg := config.Default(); cfg.DatabaseDriver = "sqlite"; config.C = cfg; t.Cleanup(restore)`，
  并整包 `env DB_DRIVER=postgres DB_DSN=... go test -count=1 ./internal/<pkg>/` 自检一遍（单测 `-run` 不复现）。
  G3 预检另钉 `env DB_DRIVER=sqlite`，PG 方言覆盖由 UAT 矩阵承担。

### 5. 前端约定

- 风格计量单位统一**积分口径**，公开接口零 token 裸值。
- **对外报价只有一个事实源（★ 2026-09-28 〇-Y 立为硬约定）**：预估公式、人工对照单价与采集日期、输入上限、
  示例预置量一律从 `src/lib/priceCalc.ts` 取，`PriceComparePage.tsx`（全量页）与 `PriceQuickCalc.tsx`（首页快算卡）
  共同消费它；**禁止在组件里再抄一份公式或单价**——F-12「三口径打架」当年就是这么长出来的。
  系数数字本身仍只走匿名 `GET /api/pricing/meta` 现值，**取不到就渲染空态且零金额零输入**，不许兜底旧价
  （运营在管理台调档后官网报旧价＝对外错报，比"暂时取不到"严重）。公开面两条负向锁照旧：不含 `token` 字样、
  不含内部计量常数裸值（24917／33222），且负向锁必须配正向对照（同一句该出现的地方要在，否则负向恒真是空转）。
  首页任何带金额的卡片都走独立 `.lc-qc-*` 命名空间——#39「首页套餐卡不得出现金额」那条锁按类名判定，
  混用 `.lc-plan-price` 会把它直接判红；改这块时在 `Landing.dom.test.tsx` ⑰ 里重跑一次 #39 判据自证。
- **接口层失败必须经 `bizResp` 收敛（★ 2026-09-27 〇-U 批 I-10 立为硬约定，与 §一·8 配对）**：
  后端状态码诚实化（F-64①②③）之后，`src/api/*.ts` 里凡是返回 `{success,...}` 信封的调用一律写
  `bizResp(() => request(...))`，**禁止直返裸 `request`**——`bizResp` 把结构化 4xx 失败体还原成历史
  `{success:false,...}` 形态（details 摊平、401/403 照抛以触发重登录），调用方零改动；绕过它会让面板
  对着抛出的 ApiError 白屏。新增 api 文件须带 F-64② 文件级中文注释。
  ★ 这条的射程由 `src/api/bizRespGate.test.ts` 静态锁机械保证（2026-09-29 D-3 批建立，现存 33 个非测试 api 文件），四档口径：
  ① INFRA＝`core.ts`/`index.ts`/`types.ts` 不产生调用点；
  ② EXEMPT＝`lead.ts`（蜜罐「假成功」，失败也不许向提交方揭穿）与 `translate.ts`（预估已自判信封＋SSE/健康检查非信封）
     **刻意不接**，且源码里必须留有豁免理由标记（`bizResp 豁免`／`正当豁免`），只往白名单塞文件名不算；
  ③ READ_THROW_EXEMPT＝纯读取且调用点靠异常通道出文案的函数（mybilling 五个读取／referral 两个）**必须保持裸返**，
     包上 bizResp 会把「取数失败」伪装成「空数据」（批 #42 定性的静默失败形态）——判据含点位/理由/保鲜三腿；
  ④ LEGACY_UNWRAPPED＝存量裸返台账（16 文件 / 64 个宿主函数，只减不增；还清条目必须删，否则判红）。
  新增文件默认按主判据（凡返回 `{success,...}` 信封的调用一律包 `bizResp`）；想进 ②③④ 任一档都得在锁里写明理由。
- 组件测试用 vitest + jsdom（`*.dom.test.tsx`）。
- **i18n 为 12 语种口径**（★ 2026-09-20 全站十语种升级）：zh/en 全量词典（`panels/*.ts` 双语同步），
  其余十语种 `src/i18n/locales/*.ts` 为 **ALL_KEYS 全量词典**（2026-09-20 起建 2532 键，此后随批次增长，
  2026-09-29 实测 3013 键逐键覆盖（2026-09-22 曾为 2746），`locales.core.test.ts` 以 ALL_KEYS 动态长度为基准红灯拦截，不钉死数字；
  历史 CORE_KEYS 核心集口径已并入全量）——新增面板键必须十份 locale 同步补译，
  生产管线与术语基准见项目记忆「多语言批次」。lang→en→zh 回退链仅作全新键未同步时的临时兜底，
  不视为正常状态。
  语言切换唯一入口 `LangSelect`（禁再造 toggle 按钮），语种名统一 `langLabel()` 取中/英。
- **首访语言自动检测**（★ 2026-09-20）：无有效 `app_lang` 时按浏览器语言选语种（中文系分简/繁，
  命中语种表用该语种，其余回落 en）。测试必须钉底：vitest 在 `vitest.setup.ts` 预置 `app_lang=zh`、
  Playwright 在 `playwright.config.ts` 钉 `locale:'zh-CN'`，中文断言用例再自行覆盖，否则随机翻红。
- **界面规格 = UI 交付真值**（★ 2026-09-22 〇-L 立为硬约定）：颜色/字号/字重/间距一律取
  `前端及UI相关/UI-ANNOTATIONS.md` 与交付包 `langcross-handoff.zip` 的字面值，**禁止**再写
  「比 X 更大/更亮一档」这类**单向测试锁**——历史上三批提亮（09-18 令牌、#35、#67/#68）正是被
  单向锁一路推离交付稿，返工成本 = 全站 50 文件。新增界面规格断言必须写成**等值锁**
  （`readability.test.ts` 令牌等值 + 提亮产物负向清零；`pixel_uat.spec.ts` P2b 运行时实测等于交付值）。
  唯一例外是多语言能力（12 语种/RTL/全量词典/动效），按用户口径「除多语言特性导致的除外」保留。
  ★ **〇-N（2026-09-23）用户后令覆盖了交付原值**：字号在交付档上整体 **+2px**（≤16 的档，>16 的展示型
  大字不动）、所有 `border*` 描边一律 **2px**（含单向分隔线；`height:1px` 的独立分隔条不算框，仍 1px），
  **颜色一族逐字未改**（仅指 〇-N 这一批；描边与面色已被下面的 〇-O 覆盖）。换算表见 `UI-ANNOTATIONS.md` §1.2「〇-N 后档说明」与 §1.3；
  闸门＝`readability.test.ts` I 段（五类渲染面源码级：最小 11px + 细描边负向清零 + 扫描量级守卫）
  与 `pixel_uat.spec.ts` P2b（运行时实测 15/16/15/14 字阶、框脚 114 / 输入卡 94 / 工具条 50——
  ⚠️ 这组几何档是「描边 2px」时代的产物，**〇-P 回落 1.2px 后已作废**，现行实测见下方 〇-P 段「运行时锁细则」）。
  ⚠️ 抬档后**旧档不得复活**（本条仅指 〇-N 期间；**〇-P 已把描边粗细按要求改回交付档 1.2px/1px**，
     见下方 〇-P 段——那次不是「违规回改」，是用户重新下令，两段的锁已同步翻转）。
  ★ **〇-O（2026-09-23）用户后令又覆盖了「颜色未改」这一条**：**全部框线纯白**（描边七档
  `--lc-border-strong/done/input/faint/pill/card/card-dim` 值一律 = `#FFFFFF`，**名字与 `--lc-border-1…7`
  别名保留**，故几百个调用点零改动即整体翻白；旧灰阶 `#8B939F`/`#6E7683`/`#5A6270`/`#464C58`/`#424956`/
  `#3A404C`/`#2A2F3A` 一度作废）、**层级改由面色台阶承担**（L1 `--lc-inset` `#0A0B0D` / L2 `--lc-panel`
  `#121417` / L3 `--lc-raised` `#1A1D21`，台阶差 +8；**页面底 `--lc-bg` 仍 `#000`，用户明令不许抬亮**）。
  唯一例外：语义状态边（`--lc-border-danger-edge #402323`、后台 `--adm-warn-bd`/`--adm-info-bd`/
  `--adm-err-bd`/`--adm-purp-bd`）属状态标识不随白框翻白。〇-N 的 2px 粗细它没动（用户判「太粗」但明令「别改了」）。
  ★★ **〇-P（2026-09-23）——现行口径，覆盖 〇-O，并作废 〇-N 的 2px**：用户看过 〇-O 实测后判定
  「太丑」，后令「严格按 UI 交付稿来」。故：
    - **描边七档回到交付灰阶**（#8B939F / #6E7683 / #5A6270 / #464C58 / #424956 / #3A404C / #2A2F3A）；
      `#FFFFFF` **不再作框线色**（`--lc-fill-white` 仍是实心白填充档，只服务主按钮/徽标，不作描边）；
    - **面色台阶回到交付值**：`--lc-panel` `#0E1014` / `--lc-raised` `#16181C`（〇-O 的 +8 档作废）；
    - **描边粗细回到交付档**：全边框 **1.2px**、单边分隔线 **1px**（〇-N 的「一律 2px」作废；
      checkbox 对勾 glyph 的 `2px solid #000000` 是图形笔画不是框，保留）；
    - **字号保留 〇-N 的 +2px 不动**（用户本批明确保留）。
  换算表见 `UI-ANNOTATIONS.md` §1.1「〇-P 后档说明」与 §1.3；闸门＝`readability.test.ts`
  A/F/H/I 段 + `public_ui_test.go`（`retiredLegacy00O` 负向）+ `admin_ui_test.go` + `pixel_uat.spec.ts` P2d。
  ⚠️ 白族判据两侧必须同口径：〇-P **要求**框线落在不透明灰阶档，但 `rgba(255,255,255,.x)`/
  `rgba(231,233,234,.x)` 这类半透明白（弱标签、转圈 fade）与「描边跟着实心白填充走」的写法
  源码锁 `BORDER_HEX_ALLOW` 与运行时锁 P2d 一起放行，一严一松即长期红灯。
  ⚠️ **〇-P 运行时锁细则（2026-09-24 全量复跑重钉）**：① P2b 几何档随描边回落到
  `工具条 48 / 输入卡 90 / 框脚 109`（= 〇-M 原实测值；1.2px 档下 mt-seg 实测 34px + 上下内垫 14）；
  ② P2d 对话框边宽**只能锁「取整等值档 1px」**——Chrome 151 的 CSSOM 把 1.2px 亚像素描边按设备像素
  取整上报（dpr=1 与 dpr=2.5 实测均回 `1px`），运行时读到字面 `1.2px` 是**物理不可能**；
  1.2px 的字面等值由源码级 `readability.test.ts` A/I 段承担，P2d 这一锁的射程=抓 2px 抬档/0px 抹框类
  运行时覆写。写运行时亚像素等值锁前必须先在钉死的浏览器版本里实测一次，别拿源码值当运行时返回值。
- **主题只有暗色一套**（★ 2026-09-23 〇-N）：文字色必须落在无条件的 `html, body` 基础层，
  **禁止只挂在 `html[data-theme='dark'] …` 覆写层**——历史上文字色只在覆写层 + 主题默认 `auto` 跟随系统，
  浅色系统用户直接拿到「纯黑底 + 浏览器默认黑字」（用户投诉「黑色的 UI 不该配黑色的字」）。
  light/auto 三态与顶栏切换钮已删除，`lib/theme.ts` 的 `applyTheme()` 恒写 `data-theme=dark` +
  `color-scheme=dark` 并清理遗留 `app_theme` 键；新代码不得再引入 `prefers-color-scheme` 判定。
  锁见 `e2e/dark_admin_upload.spec.ts` D1（`colorScheme:'light'` 宿主 + 预置 `app_theme=light`
  下实测 body 文字色必须 = #E7E9EA 且遗留键被清）。
- **白色两档不许混用**（★ 2026-09-22 〇-LI）：实心白填充件（主按钮 / 主 CTA / 反相白卡 / 白底徽标 /
  用户气泡 / FAB / 发送键）= **#FFFFFF**（别名令牌 `--lc-fill-white`）；**#E7E9EA** 只用于主文字与
  正向/活跃态小控件（checkbox、switch、Tab 活跃胶囊、进度条、光标、状态点）。
  拿 `var(--lc-text-1)` 做整块填充就是用户判「白色显脏、偏蓝」的根因，锁见 `readability.test.ts` G 段。
- **五类「前端闸门扫不到」的渲染面**（改 UI 必须逐面点名，别默认 dist 绿 = 全站绿）：
  ① React 组件内联 `<style>` 模板串；② 后端直出 `/docs/*`（`internal/api/public.go`）；
  ③ 后端直出 `/openapi/docs`（`admin_openapi.go`，CSS 已收敛为共享常量 `openAPIDocsCSS`）与
  `/office/taskpane.html`（`office.go`）；④ assist-server `go:embed` 的 `internal/assist/web/admin.html`；
  ⑤ 浏览器扩展 `extension/`（popup + content.css）。
  ②③④⑤ 的锁分别在各侧自有闸门里：Go 侧 `public_ui_test.go`（含 `TestAllServedHtmlPagesMonochrome`
  全量扫描——源码含 `<!DOCTYPE html` 即进射程）、`assist/web/admin_ui_test.go`、
  `readability.test.ts` H 段；运行时侧由 `pixel_uat.spec.ts` P6/P6b/P6c 补。
  **部署口径**：改动落在 ②③ 必须换 `translator-server`，落在 ④ 必须换 `translator-assist`；
  落在 ⑤ **必须 `bash scripts/build_extension.sh [版本号]` 重打托管 zip**（★ 2026-09-23 起扩展有交付链：产物落
  `frontend-react/public/extensions/`，随前端 dist 换源即上线，`/extensions/*.zip` 走 `spa.go` 静态直出、不动后端二进制），
  **只改 `extension/` 源码而不重打包 = 线上仍是旧包**；`.sha256` 记的是「固定顺序 name+NUL+bytes+NUL 归一」的
  **内容指纹**而非 zip 字节哈希（zip 内含 mtime，同源码两次打包字节不同），比对排障按这个口径，
  漂移由 `build_extension.sh --check` 与 `src/extensionPackage.test.ts` 拦。
  **SDK 交付物与扩展同为「随源发布」通道**（★ 2026-09-25 〇-T）：改动落在 `sdk/python` / `sdk/typescript`
  **必须 `bash scripts/build_sdk.sh` 重打托管产物**（whl/sdist/tgz + latest 别名 + `.sha256` + `manifest.json`
  落 `frontend-react/public/sdk/`，随前端 dist 换源即上线，`/sdk/*` 静态直出、不动后端二进制）；
  **只改 SDK 源码而不重打包 = 线上仍是旧包**。漂移由 `build_sdk.sh --check` 与
  `e2e/sdk_download.spec.ts` 拦（可达性判据同 §6 托管物口径：200 + 非 HTML 兜底 + 魔数 whl=`PK`/tgz·sdist=gzip
  + 体积下限；manifest 与 `pyproject.toml`/`package.json` 版本交叉锁，禁写死版本号）。
  落在 ②③④ 而**只换 `/opt/translator/web`** 一律不生效（① 的组件内联样式在 dist 里，换前端即生效）。
  ⚠️ **「首页由谁直出」决定品牌注入在不在**（★ 09-27 F-74 实测、**09-28 已修并上线**）：`window.__BRANDING__` 由 `spa.go` 的
  `serveIndexHTML` **无条件**注入，所以只有**首页走后端**的域名拿得到它；首页若由 Caddy `file_server` 静态直出，
  品牌只剩前端异步兜底。现网**三域（主站／演示站／租户品牌域）现在都走后端**（主站兜底段 09-28 起为 `reverse_proxy`），
  ⇒ 判据翻转：**三域 `curl -s <站点>/ | grep -c __BRANDING__` 都应为 1**，且主站首页 sha **不该**等于仓库
  `frontend-react/dist/index.html`（实测 2,591 B 对 2,227 B）；**哪天测出命中 0 或 sha 相等，就是有人把兜底段写回了
  `file_server`**，去 Caddy 找，别在前端找「品牌不生效」。
  ★ **租户品牌域名（`*.lexicorn.cn` 通配块，〇-W/F-73）改动的三条硬口径**：① 通配块兜底段**必须 `reverse_proxy`，
  不许写回 `file_server`**（就是上面那条 F-74 教训的落地位置，`/assets/*` 才由 Caddy 落盘直出）；
  ② 动过 `/etc/caddy/*tenant*.conf` 或该 conf 的 import 行，**发版验收必须跑 `bash deploy/smoke_tenant_domain.sh`**
  （15 判据，全只读；本地单测/vitest 看不见反代路由表这一层，这正是 F-73 能藏这么久的原因）。
  该脚本自带反向对照：`TENANT_BASE=https://<主站域> bash deploy/smoke_tenant_domain.sh` **必须 FAIL≥2／exit 1**，
  若反而全绿说明判据失效，别把绿灯当"主站也通了"；⚠️ **两条红的内容随 F-74 修好而变**：旧＝T1 `hits=0 __no_injection__`
  （静态直出形态），新＝T1 `code=200 hits=1 tenant_id=0` ＋ T1b `brand_name` 空（首页走了后端、只是该域解析不到租户），
  **看到 `hits=1` 不是判据坏，是 F-74 生效的证据**；
  ③ **证书口径＝`tls internal`，公网可用只因为 Cloudflare 该 zone 是 Full 而非 Full(strict)**——切 strict 前必须先换
  Cloudflare Origin CA（`/etc/caddy/tls/tenant-origin.{crt,key}`，`chmod 600`，**Origin CA 私钥禁止进聊天/文档/git**），
  否则新租户子域全体 526。另注意**读侧匹配是 `WHERE domain=?` 的裸小写前缀精确等值**（★ F-76 已于 09-28 上线：
  **写侧**现在做 trim＋小写＋剥基础域＋剥路径与端口＋字符集白名单＋占用校验（撞其他租户的 `domain` **或 `code`** 都不行）
  ＋保留前缀名单（`langcross`／`rox-test`／`new` 等），非法即 400 带明确文案，管理台预览域与实际入库值同源显示；
  **存量行不做迁移**（现网 `rox`／`rox-test` 本就是规范裸前缀，没有要清洗的行），
  排查「品牌不生效」先核库里那一列的字面值，再核 ①②③。
  ★ **F-75 品牌展示付费闸（09-28 上线）三条读法**：展示判定＝企业根租户／超管授权／套餐在效／到期后 30 天品牌宽限
  （`BRAND_GRACE_DAYS` > `system_config.brand_grace_days` > 30，配 0 即关宽限），四条任一成立即展示；库里品牌字段
  **只改出栈视图绝不清空**，续费当天自动恢复。**超管与该租户管理员永远看真值**——后台品牌设置页是读写同源，
  把「被闸隐藏的空字段」当现值载回，客户点一次保存就覆盖掉真配置了（★ 改动这块前必须记住这条）。
  ★ **F-79 显式 `?tenant_id=` 身份闸（09-28 已修并上线）三条读法**：`brandingPayload` 的解析优先级仍是
  「显式 `?tenant_id=` ＞ 按访问域名 ＞ 平台默认」，但第一条现在带判定——判据只有一条导出面
  `internal/api/branding_scope.go` 的 `brandingExplicitIDAllowed`，**且必须继续收在 `brandingPayload` 这个咽喉上**：
  `/api/tenant/branding` 与 `spa.go` 首页注入两条面都从它走，改成两处各写一份＝迟早长出「接口堵了、首屏注入还漏」
  的第二形态（F-79 当初的线上复现恰好就是注入面）。放行两支＝平台超管（跨租户预览是这条参数**唯一**的设计用途）
  或该租户自己的成员；**其余一律忽略该参数、回落按访问域名解析，绝不返 403**——这是未登录可达的公开首屏，
  历史链接／收藏夹里就可能挂着 `?tenant_id=`，403 会把正常首屏打成错误页（F-64 那批"对外契约改动引发现网故障"的同形教训）。
  与 F-75 互不替代（那条管"有没有资格被展示"，这条管"有没有资格点名看"），所以**给第②支放宽不会捅开付费闸**。
  ⇒ 「品牌只在其品牌域生效」现在是**服务端保证的事实**；发版后按 `deploy/smoke_brand_homepage.sh` 判据 G 核对
  （线上匿名打首屏与接口两侧 `tenant_id` 都必须回 `0`），常设锁另有 Go 四态单测＋UAT **T65** 四腿。
  ⚠️ **「纯注释提交＝不用发版」只对 React 侧成立**：往 `public.go`/`office.go`/`admin_openapi.go` 的**内嵌 HTML/JS
  字符串里**加一行注释，dist hash 不变、`go build` 无任何行为差异，但**直出页的字节确实变了**，线上就是旧页
  （2026-09-23 〇-M 实测：注释批晚于发版批，两站 `taskpane.html` 与仓库差 2 行，只能 11:24 补换一次二进制）。
  判据：`git diff <线上二进制对应提交>..HEAD -- backend-go/` 里若命中上述文件的内嵌字符串区域，就必须换对应二进制。
  ⚠️ ④ 曾有一层坑：生产 `secrets.env` 一度留着 `ASSIST_WEB=/opt/ai-assist/web`，**外置文件优先于 `go:embed`**，
  换二进制仍是旧页——2026-09-22 〇-LI 收尾已**撤销该 env 并挪走外置页**，内嵌 `admin.html` 为单一事实源。
  若运维再显式配 `ASSIST_WEB`，同步外置文件的口径立即恢复生效（`internal/assist/api/server.go` adminPage）。

### 6. e2e 断言红线

- **禁止在 `frontend-react/e2e/` 写死外部/生产域名或依赖 CI 不存在的环境变量的用例。**
  这类用例会永久性把发布闸门拖红，钝化对真回归的敏感度（2026-09-17 已因此清理 `_tmp_admin.spec.ts` / `_tmp_iframe.spec.ts`）。
- 需要人工环境（生产探针、手工 Token 等）的用例一律放 `frontend-react/e2e-manual/`，
  并在文件头加 `test.skip(!process.env.XXX, '...')` 守卫，附「为何手工」的注释。
- 生产探针职责由 `deploy/` 下的冒烟脚本承担，不进 Playwright 矩阵。
- **链路型用例（依赖后端真实接口才有意义的 e2e，如助手挂件、流式对话）必须自带「可达探针」。**
  前端普遍有离线兜底（横幅 / 「暂时联系不上」空态），转发链断掉时**界面照样渲染**，断言会对着兜底态一路绿灯。
  写法：断言前先 `expect(离线标志元素).toBe(0)` + `expect(链路应下发的标识，如会话 sid).toBeTruthy()`，
  失败信息直接点明「⇒ /xxx-api 链路没通」，而不是留给后人去猜。
  （2026-09-23 实测：`/assist-api` 在主服务直出 dist 的形态下没有转发方，落进 SPA 兜底返回整页 `index.html`，
  `assist_widget_cache.spec.ts` W1/W2 全程在离线态假绿，直到 W3 才以「拿不到 sid」暴露——见 `e2e/assist_widget_cache.spec.ts` 的 `expectLiveLink`。）
- **同一兜底陷阱也污染「静态产物可下载性」用例。** `spa.go` 对**不存在的路径**会回退成 `index.html` 且**状态码仍是 200**，
  所以「文件在仓库里 ≠ 线上点得开」。凡断言托管物（`/extensions/*.zip` 等）的用例，判据必须是
  200 **+ 响应体不是 HTML 兜底 + 格式魔数（zip 为 `PK`）+ 体积合理**，只判 `status === 200` 一律视为无效断言
  （锁见 `e2e/extension_download.spec.ts`；版本号从 `extension/manifest.json` 现读，禁止写死后成为落后于版本的假绿源）。

### 7. Shell 脚本断言写法（UAT 脚本）

- **正则交替一律用 `grep -E 'a|b'`，禁止 BRE 的 `grep 'a\|b'`。** `\|` 是 GNU 扩展，
  BSD grep 之外的实现（如部分精简 shell 环境与容器基础镜像自带的 grep）会把它当字面量，
  **静默返回 0 命中**——断言会「永远通过/永远失败」而不报错，是最难发现的一类闸门失效。
  （2026-09-17 实测：`scripts/uat/api_uat_txn.sh` T16 因该写法恒判 refused=0 而误报失败。）
- 断言脚本避免依赖外部环境的行为差异；涉及金额/计数的断言优先用 `-E` + 明确锚点。
- **金额/数值「直读等值锁」必须先过方言归一 `mny_norm`（`scripts/uat/api_uat_txn.sh`）再比对。**
  同一列 SQLite 回吐 `5.0`/`1.0`，PostgreSQL numeric 回 `5`/`10.8`——直接字符串相等会在两种方言下各红一次，
  而这类红**不是**回归。（2026-09-23 实测：T51/T54 金额与 `fx_rate` 直读锁。）
- **`DB_DRIVER=sqlite` 的本地快跑不是发布闸门。** 它只用于快速自检；数值文本、时间函数、`RETURNING`
  与锁语义都有差异，计费/对账相关改动必须以 PG 方言跑 `run_uat.sh` 才算通过（见下方 §二）。

### 8. Handler 错误返回口径（★ 2026-09-23 〇-LK 立为硬约定）

- **新增/改动的 HTTP handler，错误响应一律走 `s.writeError(w, r, apierrors.New(code, msg))`，禁止新写内联
  `writeJSON(w, 4xx/5xx, map{...})`。** 全包棘轮 `TestErrorStyleRatchet`（基线 **485**，2026-09-29 实测校准；
  2026-09-26 批 I-7 由 699 降到 628：收款/账务七文件 71 处已迁 `writeError`）按**整包**计数，
  新文件里第 1 处内联错误响应就会顶红闸门——这不是形式问题，内联体缺统一错误码，
  前端与 SDK 无法按 code 分支处理。
- 白名单转发（如 `assist_open_proxy.go`）里「上游原样透传」的 4xx **不算**内联错误响应：那是上游状态码，
  不新造错误体；本层自己的失败（构造失败、上游不可达、读取中断）必须走 `writeError`。
- 缺错误码就在 `internal/errors/codes.go` 补（`ErrMethodNotAllowed`→405、`ErrUpstreamUnavailable`→502 即本批新增），
  同步登记 HTTP 映射，别在 handler 里手写状态码。

### 9. 提交与推送：文档不外推（★ 2026-09-23 〇-LL 立为硬约定）

- 远端（GitHub `origin`）**只放代码**：任意层级的 `*.md` 与 `前端及UI相关/`（UI 交付包、流程图）一律不进推送。
- **靠历史结构保证，不靠人记住命令。** `autosales` 是线性单分支，2026-09-22 〇-LJ 出过一次事故：排在纯代码提交
  前面的两份「仅本地」文档提交成了它的祖先，`git push` 按祖先链打包，文档被一起推上远端——
  **「文档不外推」是被历史结构击穿的**，而不是某条命令写错。
- 两半做法（①是流程，②是机制，②专门用来兜住忘记①的情况）：
  ① 文档提交放到**永不推送**的 `docs-local` 分支；正常批次 `autosales` 上只有代码提交，push 天然干净。
  ② 推送一律走 `scripts/push_code_only.sh`（先不带参数干跑看清单与判定，确认后再 `--apply`）：
  推出去的那个提交**永远直接从 `origin/<分支>` 长出来**，内容 = 本地代码文件的目标状态。
- **跑 `push_code_only.sh` 前工作区必须只剩未跟踪件**（★ 2026-09-23 〇-N 首跑真踩）：脚本要在 `origin/<分支>` 之上
  `git checkout -b` 建临时纯代码分支，未提交的文档编辑会让 checkout 直接失败
  （`Your local changes to the following files would be overwritten by checkout`），`--apply` 白跑一次。
  文档要么先提交，要么**带标签**挪开：`git stash push -m "<批次>-docs-aside" -- <显式文档路径…>`
  （别用裸 `git stash`，也别加 `-u`——`-u` 会把未跟踪的 `前端及UI相关/` 交付目录一并卷走）；
  推完 `git stash apply stash@{0}` 原样恢复，提交文档后再 `drop`。
- 改这个脚本前必读的两条（都是首跑真踩出来的）：
  - 判定「某路径该取还是该删」必须问 git 对象库（`git cat-file -e "$BR:$f"`），**不能问工作区**（`[ -e "$ROOT/$f" ]`）——
    `git checkout -b $TMP $BASE` 会把 BASE 里不存在的新增文件从磁盘删掉，于是 `[ -e ]` 恒假、新文件被当成「已删除」，
    首跑 25 个代码文件只推上去 13 个（`b6e9efc` 修）。
  - 三道校验缺一不可：纯代码提交内零 `.md`/零 UI 目录 → 推送后与本地树**逐文件等价**
    （`git diff --name-only "$BR" HEAD -- ':(exclude)*.md' ':(exclude)前端及UI相关/'` 必须为空）→ 任一红即停在本地不推。
- **force-push 与改写已推送历史需用户明令**；脚本只做普通 merge 并轨，冲突即停手交人工。
- 顺序口径仍然有效：一批工作 **代码提交 → push → 才提交文档**；顺序反了就是把文档送进了推送的祖先链。
- 文档里写「仅本地提交」时必须与实际一致——历史上那类偏离正是事故的来源。

### 10. 免费额度防薅与站点门面（★ 2026-09-28 〇-Z 立为硬约定）

- **设备号只有一份事实**：`frontend-react/src/lib/trialDevice.ts` 生成并持久化 `^[A-Za-z0-9_-]{8,64}$` 的设备号，
  免登录试用（`trial_dev`）与免费注册（`reg_dev`）**共用它**。新增任何"免费发放"类入口都必须带这个字段并按同一 scope 记账；
  **禁止**再造一个设备号或让某条链路各数各的账——否则"先刷试用再刷注册"这类连打路径在两侧各只吃掉一半格子，防薅形同虚设。
- **档位数字只有 env > `system_config` > 代码默认 三级**（实现＝`configIntTier`），**禁止在代码或前端写死档位**。
  ⚠️ 运维读档有两个地方，别查错：`system_config` 存**上限**（键不存在＝走默认，不是"上限 0"），
  `rate_limits` 存**已消耗**（该表只有 `count/window_start/lock_until`，**没有上限列**）。
- **占格必须原子、且必须有退格**：计数走 `store.RateReserve`（一条带条件的 UPDATE，判据与计数同事务），
  失败链路一律 `RateRelease`；**坐实点＝免费额度真发放出去那一刻**（`CreateQuotaGrant` 成功 → `markSpent()`），
  此前任何一次失败都不许扣掉用户当天格子。防薅表自身读写故障 **fail-open**（放行＋`observability.Error`）。
- **公开接口的新增防护一律软档**：`device_id` 缺失/格式不合**不返 400**（同 F-79 不许把公开首屏打成 403）——
  老缓存包、脚本客户端、代客注册都可能不带这个字段，**新增防护不拿可用性交押金**。
  受邀加入与专属域名注册**不占格**，且防薅判定必须发生在 `MarkInviteCodeUsed` **之前**（拒绝不许烧掉客户自己发的邀请码）。
- **门面开关是部署级**：`internal/api/site_flags.go` 解析 env `LANDING_DISABLED` > `ops_policy` 的 `front.landing_enabled` > 默认展示，
  **禁止**在前端按 hostname 判定"哪个站是体验机"（换域名要重构建、本地闸门看不见该分支）。
  注入纪律：**只在关闭态注入**那段 `<script id="__site_flags__">`，开放态首页必须**逐字节等于未注入**
  （§一·5 那条"主站首页 2,591 B / `__BRANDING__` 命中 1"判据靠这个成立）；只管 `/`，`/pricing`·`/compare` 访客仍须 200。
- **UAT 矩阵必须把防薅档开大**：`run_uat.sh`/`multi_instance_e2e.sh` 整轮注册上百个免费账号，按默认 3/500 会中途级联假红
  ⇒ 进矩阵前 `register_device_daily_limit`/`register_global_daily_limit` 开 100000，段内自压、跑完钉回；
  `rate_limits` 的滚动 24h 窗**不清就把下一轮判红**。

### 11. 计费三把尺子与平台承担成本（★ 2026-09-29 〇-AD 立为硬约定）

- **单一事实源**＝`backend-go/internal/store/billing_platform_cost.go` 里的四个常量
  （`RealDebitPred`／`PlatformTaskTypeExclPred`／`UnattributedTraceExclPred`／`CustomerUsagePred`）
  加带列前缀的 `CustomerUsagePredWith(prefix)`。**新增或改动任何「消耗量」读腿一律引用常量，禁止再抄字面量、禁止再造第四把尺子**
  ——这批 bug 的形态从来不是某条腿写错，而是五条腿各拿一把尺子各数各的账。
- 三把尺子的射程，不许互相顶替：
  ① `RealDebitPred`（`charge_kind IN ('','charge')`）**只管金钱语义**（退款消耗核算这类「到底扣了多少钱」）；
  ② `PlatformTaskTypeExclPred`（排 `kb_embed`/`system_task`/`pack_scrape`/`evals` 四个用途标签）**管额度侧**
  ＝日计数器 `usage_daily`、部门/组织月度预算墙、收银台「今日/本月已用」；
  ③ `CustomerUsagePred`＝②＋排无归因留痕（`NOT (charge_kind IN ('log','settle') AND COALESCE(user_id,0)=0)`）
  **管客户面显示**＝个人/组织看板、租户明细页与 CSV、自服务账单的日序列与流水分页、OpenAPI 用量、超管按用户聚合。
- 三条反证（合并尺子之前先读这三条，都是本批真踩）：
  拿 ① 当客户面尺子 ⇒ **免费期/策略免扣**（`ops_policy` 的 `mode_rules.<mode>.charge=false`）下客户自己下单的流水也是 `'log'`，
  「我的用量／明细／CSV」整段抹空（判据现场＝run_uat **T59**，`api_uat.sh` B8 段压的 `uat_promo` 窗口）；
  拿 ① 当额度尺子 ⇒ `billing_enforced=0` 的租户全部流水都是 `'log'`，日额与部门预算判成「一点没用」，**预算墙形同虚设**
  （那是拿「防超支」换「防误扣」，方向反了）；
  只用 ② 排平台行 ⇒ uid=0 的留痕/结算行（现网「系统/后台任务」一行 337,819 积分的形态，真实用户合计只 ≈725）回到客户账单。
- **平台承担的成本绝不进客户额度**：写点唯一入口 `llm.WithPlatformCost(ctx, reason)`（reason 名单＝`internal/llm/platform_cost.go`
  的 `Platform*` 常量），`ChargeUsageRealtime` 命中平台分支时**只记留痕行**——既不 `collector.AddBilled`、也不扣租户余额、也不撞预算墙。
  用途标签名单与 `store.PlatformTaskTypeExclPred` **跨包逐字同步**，锁在
  `internal/api/platform_cost_charge_test.go` 的 `TestPlatformCost08AD2_LabelListsAgree`（改名式破坏必须当场红）。
  自己已在别处记过账的平台任务（`engine.RebuildKBIndex`）要带 `llm.WithSelfLedgeredUsage(ctx)` 抑制出站回调，否则同一笔用量出两行。
- **平台承担这一档只在超管面出栈**：`/api/billing/usage/cost`＝`requireAdminUser`（level≥4）＋`tenant_id==0`，
  租户管理员 403、匿名 401；客户面任何页面／导出／对外接口**不得出现该字段**。改这块时先确认读点还在这一道闸后面。


### 12. 文件转换远端派发：只走 `fpdexec` 协议（★ 2026-09-30 〇-AE 立为硬约定；★ 2026-10-01 〇-AF 起**现网已开闸**，`/api/health` 回 `dispatch:"online"`，且 Go 腿用真件完整往返过一次——排障别再按「未开闸」假设）

- 体验机 `fpd` 账号被 sshd 的 `ForceCommand=fpd-bridge` 钉死，**只认「stdin 第一行 = base64(JSON header)」这一种协议**。
  因此 `backend-go/internal/fileproc/fileproc_remote.go` 里**禁止**出现裸 `ssh <host> <任意命令>` 与 `scp`：
  建目录／上传／查尺寸／取回一律走 `put`/`stat`/`get`（`put` 自己按协议建会话目录，**别再补一次 mkdir 往返**）。
  09-29 的现网事故就是这两条死腿：开关一开、第一次派发死在建目录，然后被降级链静默兜回本地，
  表现是「派发开了但从没真跑过一次」——**有自动降级的链路，"产物能打开"不算验收**。
- **上传与回拉必须各比一次 sha256 逐字节等值**才算成功（`TestDispatchRunByteEqualRoundTrip`），失败必须显式降级并记 WARN，
  **不许**把"远端没拨通"写成"这单已处理"。
- **就绪判据不许吃零值**：`selftest`／`expire_date`／`caps.rlimit_as_set` 任一缺字段即判未就绪
  （`TestProbeRejectsMissingSelftestField`——远端删掉字段＝主站静默放行，这是反向锁的存在理由）。
  `/api/health` 只出**状态词**（`dispatch`／`dispatch_expire`／`dispatch_mem_cap`／`dispatch_selftest`），
  **禁止**出现远端 IP／主机名／绝对路径。
- 探测缓存是 **TTL**（`FILEPROC_DISPATCH_PROBE_TTL_SEC`，默认 600s），**不是 `sync.Once`**：
  远端「到期即拒」当天，跑着的进程必须自己翻旧；失败侧只缓存一个短退避窗口（旧形态"失败一次永不翻身"）。
  ⚠️ 重探**不许**进 `procGate`/远端闸临界区（同 goroutine 重入 `sync.Once` 的死锁在这条链上踩过两次）。
- 桩只认协议：假 `ssh`/`scp` 桩对裸命令分别退 **78**／**255**（`TestFakeStubsThemselvesRejectDeadLegs`），
  谁把死腿写回来当场红——这条锁是"改完还会退化"的唯一机械防线。
- **`header.argv[0]` 只许出现一次脚本名（★ 2026-10-01 〇-AF 补丁五立为硬约定）**：
  远端 `fpdexec.py` 的 `cmd_run` 是「拼好 timeout/解释器/脚本名之后只接 `argv[1:]`」——**按位置**砍掉 argv[0]，
  不看内容。所以 `DispatchRun` 只能在调用方没写脚本名时补那一次（`args[0] != script` 才 prepend），
  **产品侧调用形态本来自带脚本名**（`pdf_overlay.go`／`pdfwrite.go` 把 `"xxx.py"` 放在 `args[0]`）。
  10-01 真机首跑抓到的就是双写：远端把第二个脚本名当子命令 ⇒ `未知子命令: pdf_overlay.py` 退 2
  ⇒ 被降级链静默兜回本地，而**三条读数当时全绿**（probe 腿没有 argv／G5 是 bash 自己拼的单份脚本名／
  假 ssh 桩当时压根不看 argv）⇒ 假 ssh 桩的 `run` 分支已同期改成按真机同一口径砍 argv[0] 并认子命令，
  等值锁＝`TestDispatchRunArgvHasScriptNameOnce`（反证：写回无条件 prepend ⇒ 三条用例一起红）。
- **页数只有一份事实源（★ 2026-10-01 〇-AF 补丁四）**：`fileproc.PdfPageCount`（解析器 ＞ 尾 `/Count` ＞
  流式页标记，优先级不可颠倒——字体描述符里的 `/Count 255` 会把尾 `/Count` 那条腿骗过）。
  `internal/api/upload.go` 的上传校验**必须薄委托**到它（锁＝`upload_pagecount_gate_test.go`，
  反证是把委托换回本地 ledong 解析器即红），**禁止**再在调用方各数一遍——派发资格是「体积 ∧ 页数」两条腿，
  页数读 0 会让 `TryDispatch` 直接判不合格、连一次远端都不拨，旧形态还**零日志**。
  体积够档而页数取不到 ⇒ 必须打一条 WARN（`页数读数取不到`）；`PdfPageCountDiagnostics` 出四腿读数给排障。
  ⚠️ 同族新缺陷：第三方解析器对「尾 100 字节全是换行」的畸形件 **panic（索引 -1）**，
  会击穿同步上传校验路径 ⇒ 两个入口各自 `recover` 收成 error，不许让它把请求打成 500。
- **真机往返探针＝`cmd/fpdprobe`**（`/api/health` 与预检都证明不了 Go 腿；用法见该文件头）：
  `extract` 那一腿的判据**不许**问「stdout 里有没有远端路径」——它的回执结构上只有文本段（实测 `grep -c` 远端根路径回 0），
  那样会把真派成功的一单判成红；extract 的证据是 `TryDispatch=true` 本身（降级一律 false，探针里没有本地兜底代码），
  路径串只作**不参与判定**的纯读数。`apply` 那一腿才问远端路径，且必须问**整段 stdout**（问 tail 会自己削掉路径）。
- **开闸判据**＝`scripts/dispatch_preflight.sh` 的 **G5 真机全往返**（无样张时 G5 **判红不判跳过**；
  G1~G4 全绿证明不了传输腿）＋**定档**：远端并发＝1（2G/2 核）；`FILEPROC_DISPATCH_MIN_MB` 必须按**产物尺寸**
  而不是输入尺寸抬档（代码默认已是 **20 MiB**＋`MIN_PAGES=30`，两条腿**同时成立**才派；旧文里那句「默认 8 MiB 偏低」自 09-30 定档起作废）。
  ⚠️ 传输速率与膨胀倍率的旧读数（回传 ≈0.49 MB/s、膨胀 ≈3.4×）已被 **10-01 真机复测覆盖**：
  一份 24,101,554 B／32 页真件——上传 ≈11 MB/s（2 s 内落 `w/`）、远端 extract 3.4 s、apply 55.4 s、
  **回传产物 24,101,170 B ⇒ inflation_x=1.00**（恒等映射写回几乎不涨；3.4× 那份是"小件＋嵌入字体"的另一形态）。
  ⇒ 别拿单一读数定档，探针每次都会出 `inflation_x=`，调档前先看当天那一行。
  凭据口径见《deploy/dispatch/README.md》「凭据面」：运维走 `fpdbox` 别名的 `ubuntu`，派发私钥不进本机 config。
- **"到期自动关闸"这只闹钟只能由脚本在"实测 `dispatch=off` 已坐实"之后退役（★ 2026-10-01 〇-AF 修法二）**：
  `translator-dispatch-expiry.timer` 每天 04:10 调 `scripts/dispatch_revert.sh --if-expired`。
  它的旧形态在 **unit** 里带一行无条件 `ExecStartPost=systemctl disable --now <本 timer>`，
  把"今天跑过一次"当成"到期已处理"——而 `--if-expired` 在**未到期**那天是正常退 0、什么都没关。
  现网实证（10-01 04:10:05）：journal 实读 `Removed ".../timers.target.wants/…"` ⇒ `is-enabled=disabled`、
  `is-active=inactive`，而到期日还差 **25 天** ⇒ 到期那天不会再有自动回滚，主站会持续朝一台已不存在的机器派发
  （每一单多付一次"上传→超时→回落本地"的双份时间，日志里只有回落计数在涨）。
  现在退役那一步在**脚本内部**、位置在 health 判定之后：未到期 ⇒ 一次 `systemctl` 都不碰；
  已到期且 `dispatch=off` ⇒ 先关闸再退役；**health≠off（含 `dispatch` 字段缺失）⇒ 退 4 且闹钟必须还在**，
  明天再试——**这一档的失败语义是"还会再来"，不是"今天试过一次就再也不试"**。
  ⚠️ 别把那行 `ExecStartPost` 写回 unit：它在 ServiceExit 之后无条件执行，脚本的三条提前退出（非 root 退 2／
  重启没起来退 3／health 不是 off 退 4）一概不看，等于把"失败也退役"重新装进环境层
  （静态锁＝`dispatch_expiry_timer_test.go` 的 `TestDispatchExpiryUnitHasNoUnconditionalExecStartPost`）。
  ⚠️ 这类判据**静态 grep 锁不住**（`disable` 那一行本来就该存在，该问的是**位置与条件**），
  所以 `dispatch_revert.sh` 的三个路径留了 env 覆盖口（`ENV_FILE`/`DROPIN`/`EXPIRY_FILE`），
  Go 侧用假 `systemctl`／假 `curl`／假 `id` 在临时目录里**真跑三条分支**，并各配一条反证
  （M1 装回无条件退役、M2 挪到 health 之前——两者都能在**退出码与正常态无法区分**的情况下被抓到）。

### 13. 挂件出栈守卫链：四道守卫只在 `Engine.Respond` 这一条咽喉上（★ 2026-09-30 〇-AE）

- 顺序钉死（`internal/assist/engine/engine.go` 的 `Respond` 尾部）：
  `repairReplyHanResidue`（判残补翻）→ `rehardenReplyRewrite`（**补翻产物再过一遍卫生**：旁白括号／裸方括号／回声）
  → `guardReplyInstructionEcho`（★ 〇-AP 引用重合度：正文逐句比对提示词指令语料，抄出来的指令句整句丢）
  → `guardReplyQuote`（报价：剥乘法算式、复算不出的总额一律不出口）→ `guardReplyBrand`（品牌名走不可译占位符）。
  新增守卫一律挂这条链，**不许**挂进四道回复分支或 `fallbackReply` 里各写一份。
- **`sanitizeVisitorText` 末道卫生里有一条「剥指令复述引导行」（★ 2026-10-02 〇-AN）**：
  `stripInstructionEchoLead` 专治模型把提示词方法论原样念成正文首句（现网「先肯定对比合理性，再分角度补充新细节：」）。
  判据刻意窄（首行＋冒号结尾＋≤48 字＋含「先」＋含 `instructionEchoLeadWords` 之一＋**必须有第二行正文**才剥），
  误剥的代价是吃掉客户要看的正常步骤句（「先注册，再上传…：」这类一律不剥，见反证单测 `TestSanitizeStripsInstructionEchoLead`）。
  这条与 persona/tone_rules 的提示词加固（方案 A/C：把「先肯定…再讲差异」从**流程指令**改成**心里的盘算**、并加【定界·最重要】）配对——
  ⚠️ 光靠提示词治不住 9B 模型的复述概率，清洗腿才是兜底；两者都要在。
- **★ 提示词泄漏的根治腿＝引用重合度过滤器（★ 2026-10-02 〇-AP，用户令「按p2做」）**：
  词表打地鼠（`selfNarrationMarkers`／`internalEchoMarkers`）天然追不上模型措辞，每轮都等用户截图再补几条。
  本腿把判据换成**比对提示词自身**：`reply_instr_echo.go` 的 `instructionCorpus` 把 `persona＋tone_rules＋promise_rules＋replyLangBlock`
  收成语料 I，出站时逐句与 I 比字符 4-gram 重合度——**连续 ≥14 字命中**（`instrEchoRunGrams=11`）或**整句包含度 ≥0.6**（`instrContainMin`）
  即判"抄出来的指令句"→ 整句丢弃＋WARN。命中即无需枚举新词，这才是「彻底」的落点。
  ⚠️ **语料 I 三条口径别踩**：① **不收【系统现值】/【报价纪律】**——合法报价本就该逐字引用那串价格系数与现算示例数字，
     收进 I＝把正确引用误杀成新缺陷（数字纪律另有 `guardReplyQuote` 专闸，见缺陷记录 §十二）；
  ② **只在 `Source=="llm"` 上跑**（话术直配／流程／兜底是人写文案，不经模型、不可能复述提示词）；
  ③ 需要每轮语料 ⇒ **挂 `Respond` 咽喉**（`guardReplyInstructionEcho`），**不许**塞进纯函数 `sanitizeVisitorText`
     （那个还服务历史回放与补翻，语料口径不同，塞进去会让那两条路误判）。
  与词面腿分工：`sanitizeVisitorText` 里新增的 `stripRuleSelfReference`（「第 N 条」×合规词自指）＋
  `stripTrailingNarrationTail`（专治**开头「（」被吃在半空、只剩收尾「）」**的尾部旁白，从尾部往回走、按 `coreNarrationMarkers`
  数到 ≥2 个不同内核词才判旁白，见第一个带字且标记 <2 的句即认正文边界停手、绝不清空）收的是**改写式**旁白（换词缩句、够不到重合阈值）；
  重合度腿收**逐字/近逐字复述**。两条互补，缺一不可。观测腿 `trailingNarrationCandidates`（`unstrippedAsides` 追加）
  对尾部单标记候选**只记 WARN 不改正文**，让下一族新形态先出现在日志里。
  ⚠️ 本腿改的是**对话正文出栈**、不碰 `localize()` 缓存 ⇒ **不需要抬 `cannedPromptRev`**（与那条「换件没修」的坑不同，换二进制即生效）。
  断言见 `reply_instr_echo_test.go`（逐字复述被丢＋合法改写保留＋规则源不动＋剥空回退不 Air＋尾部「）」旁白剥净且同屏正常报价一字不动＋≥2 误剥反证）。
- 守卫判据只问**作答语言**（`effectiveReplyLang`，见 §三 那条 〇-AC 口径），**不许**再直接读挂件送来的 `uiLang`。
- **确定性正字表（`applyJaResidueFixups`）的四道前置一个都不许放宽**：只动被判残点名的片段／`digitSeqOf` 数字序列逐字不变／
  `lineCountOf` 行数不变／残片数严格变少——任一不过就**保留原稿**并记 WARN。
  它是"补翻失败之后"的第二道防线（欢迎词/chips 那一路不做本地替换，因为有中文原文可退；对话正文没有原文可退）。
- **补翻拒绝原因分档常量是对外排障契约**：`no_upstream`/`no_leaks`/`upstream_error`/`truncated`/`empty_output`/`line_count`/`not_improved`，
  单测**逐字钉字面量**、adopt（成功）出口必须回空串。日志里的 `reason`/`before`/`after`/`fixed`/`leaks` 字段是现网定位的唯一抓手
  （094x 就是靠 `reason=line_count` 才把"补翻没生效"和"上游本来就不通"分开）。
- 改「canned 译文的最终形态」（提示词措辞／出栈卫生／判残与补翻口径）必须同步抬 `cannedPromptRev`，
  否则换件后老缓存继续投旧形态＝**换件没修**（本批真踩两次，见 §三 那条）。
- ✅ **canned 那一路的出栈闸已闭合（★ 2026-10-01 〇-AF 修法一落地；此条由「已知盲区」翻成硬约定）**：
  `localize()`（欢迎词／chips）的产物现在在 **`SetConfig` 之前**过一道 `cannedOutboundReject`
  （`internal/assist/engine/canned_guard.go`）：清洗后为空／还带着没翻的中文词（与补翻**同一把尺子** `hanResidueRuns`，
  不另立第二把）／原文没提品牌名而译文凭空多出品牌名——任一命中即**按中文原文出、一行缓存都不写**，
  并打 `reason=canned_empty`／`canned_han_residue`／`canned_brand_injected` 的 WARN（这三个分档名与那七个 reject*
  同为对外排障契约，逐字钉在 `canned_guard_test.go`）。**「翻译调用成功」不等于「产物可用」**：
  旧形态这三类在代码眼里全是成功译文，日志静默、坏形态常驻首屏（现网实证 en 的 `credits充值`、ja chips 各尾粘「能言」）。
  根因那一半收在**口径**上：`translateContract(lang, srcTopics)` 现在按源文投——原文提了品牌名才给「一律写作 X」，
  没提就**明确禁止**出现任何品牌名（沉默不等于许可被撤回，现网 ru 四条 chips 各前挂 `"LangCross: "` 就是模型自己补的）；
  计费单位那句没提就不拼。对话正文那一路**口径不变**（`replyRepairTopics()` 两条都拼：它的输入已是目标语言正文，
  没有中文原文可判"本来提没提"，拿"没提"去约束＝给模型一张删掉品牌名的许可证，会把 092x 红腿三修出来的还原反向拆掉）。
  ⚠️ 由此新增一条**同步义务**：口径段现在随源文内容而变 ⇒ 缓存指纹必须跟着"真发出去的那段口径"算
  （`localize` 与 `translateOnce` 各自 `srcTopicsOf(text)`，同函数同入参必然同值，等值锁在 `canned_guard_test.go`）。
  将来加第三个话题维度却忘了进指纹＝082x 那条「换件没修」第四次复发。
  ⚠️ **旧登记里「ja 欢迎词品牌名写作『能言』而非 LangCross」这一条判成缺陷是错的**（本轮复核翻案）：
  `brandKanjiLocales` 里 zh／zh_hant／ja 三个语种的品牌名**本来就该是汉字「能言」**（与该语种挂件标题同源），
  ja 那一档的真缺陷只有「四条 chips 每条尾部各粘一个能言」这种**注入**形态。别照旧文再"改"一遍。
  排障口径不变：换件后界面仍是坏形态，先读缓存行首指纹（`cannedPromptRev` 现为 **`096x-1`**）再怀疑模型。

---

## 二、提交前闸门（必须全绿）

```bash
cd backend-go && go build ./... && go vet ./... && go test -race ./...
cd ../frontend-react && npx tsc --noEmit && npm test && npx vite build
bash scripts/uat/assist_uat.sh                    # AI 顾问（自起临时实例，不碰生产）
bash scripts/uat/run_uat.sh                       # 全链路主矩阵（PG 方言，发布闸门）
bash scripts/uat/multi_instance_e2e.sh            # 多实例红线
bash scripts/build_extension.sh --check           # 扩展漂移闸门（动过 extension/ 却没重打 zip 时红灯；vitest 同口径）
# ★ fileproc 依赖说明（2026-09-29 真踩）：PDF 原版式（字形保护）用例必须有**真 pymupdf** 才跑得动，且它刻意 t.Fatal
#   不静默跳过（「PyMuPDF 认不认这个字体」除真构造 Font 外没有第二条路，跳过＝绿灯无效）。
#   CI 走 pip install，本机装在 ~/.venvs/langcross-fileproc —— run_uat.sh 已自动发现并导出 FILEPROC_PYTHON_BIN；
#   两处都没有时**只许补依赖，不许放宽或跳过该用例**（旧表现是整条 PG 矩阵在竞态预检段中止，后三段一行没跑）。
bash scripts/build_sdk.sh --check                 # SDK 托管产物漂移闸门（★ 2026-09-25 〇-T：动过 sdk/ 却没重打 public/sdk/ 时红灯；e2e/sdk_download.spec.ts 同口径）
```

★ **中文注释自查必须按「实文件」跑，禁止把目录当参数喂**（09-27 实测踩坑）：
`scripts/missing_comments.py` / `missing_comments_ts.py` 只接受**文件列表**，喂目录得到的是
「TOTAL 0 declarations in 0 files」——0 文件＝0 缺失＝**恒空假绿**，历史上被当成「注释闸双 0」记进过批次账。
正确口径：`find backend-go -name '*.go' -not -name '*_test.go' -print0 | xargs -0 python3 scripts/missing_comments.py`
（前端同理，`src` 下排除 `*.test.*`）。另注意两件事：① 内嵌 HTML/JS 字符串里的 JS 声明**不是** Go 声明，
给它们补注释＝改直出面字节、必须换二进制（§一·5），这类点位判为射程外；
② 改这两个检查器判据时必须配「剥掉一条注释立刻报缺」的反证，防止把真缺口一起抹掉。

改动全绿后**按 §一·9 的口径推送**（`scripts/push_code_only.sh` 干跑 → `--apply`），不要裸 `git push`。

改动触及计费/对账时，`run_uat.sh` **必须**跑 PG 方言（SQLite 快跑不能替代）。

---

## 三、改动原则

- 优先**薄委托 + 零改动调用点**的收敛手法（见 `store/crypto.go` 下沉 `internal/secret`），
  避免大爆炸式重构：改动面 >3000 行且无行为收益的重构不做。
- **并发回写共享 map 必须先快照**（★ 2026-09-28 〇-Z）：`for k, v := range m { go func(){ m[k]=… }() }` 是
  **runtime fatal error（concurrent map iteration and map write）**，不是 panic——`recover` 兜不住、进程直接挂，
  线上表现是"偶发 502／连接被重置"而不是一次请求失败。互斥锁只挡住写者彼此，挡不住"主线程还在 range"。
  正确形态：range 完把待处理项**收进切片**再并发（见 `engine/text.go` 的 `reviewJob`）。
  ⚠️ 这类 bug **真上游打不出来**（模型调用几百毫秒到几秒，range 早跑完），只在本地假上游"瞬间返回"时命中，
  所以配套断言必须带 `-race` 跑；不加 race 属于"运气好没被调度到"的假绿。
- 修复缺陷时同步补一条能复现的自动化断言（单测或 UAT 断言），否则视为未完成。
- **★ 修一条"恒失败的循环"前，必须先回答"它为什么一直没现形"（★ 2026-10-03 〇-AP 真踩）**：
  语法错把清理循环挡在第一次 `err != nil` 就 break，于是循环里那条**只在"没删到行"时才转不完**的判据
  （`for total == 0` 而不是"这一批删了 0 行即收敛"）在现网从没跑过一次——**只修 SQL＝把一条日志噪音换成把进程 CPU 钉死**。
  正确顺序：读整条循环体 → 把"收敛条件"按**本轮新增读数**（删了 0 行）判 → 给"无事可做的空库"写一条带**超时**的终止断言
  （见 `internal/assist/api/anonym_cleanup_test.go` 的 goroutine＋3s `select`，反证＝装回旧判据 3.01s 判红）。
  通用形态：**被前一道错误掩盖的分支，修掉那道错误时它就是新事故**；测试必须专门喂一次"什么都不必做"的输入。
- 涉及 DB schema 的改动必须写成幂等迁移，保证老库启动自动升级。
  ⚠️ **幂等迁移的守卫判据不许被同批建表语句先满足（★ 2026-10-03 现网抓到、待决）**：
  `CREATE TABLE IF NOT EXISTS <新表>` 若排在 `if 旧表存在 && !新表存在 { 迁数据; DROP 旧表 }` 之前，
  该判据**恒假** ⇒ 迁移在有历史的库上永远不跑，旧表变孤儿（现役代码不读写、清理也扫不到，现网实例：
  挂件库 `sessions`／`messages` 16／65 行 09-16~10-01 无人认领）。判据要问**数据层面**的事实
  （"新表里有没有对应行"），不是"表在不在"；写完必须造一个**存量库**（旧表有行）跑一遍迁移并断言行真的搬过去了。
- **改了 assist 的 `seed/seed.json` 不等于线上改了。** seed 只在**首启空表**时灌库，存量库不跟进，
  所以关键词/文案修订必须随批跑一次 `python3 scripts/assist_kb_sync.py --host <服务器>`（默认只读打差异，
  确认后 `--apply`，写前自动备份）——这一步此前长期挂「待人工执行」，2026-09-23 已由脚本收口，
  口径见《部署指南》§十三。同理「线上管理台在线改的内容」优先级高于 seed，脚本**绝不 DELETE**。
  ★ 2026-09-29 起同步面从 `kb_entries` 扩到**四张表**（`scripts` 话术 / `flows` 流程 / `feature_links` 功能卡）：
  只修 kb 不够——话术直配与流程步进**不经过模型**，库里那句超承诺文案会原样发给客户（判据见
  `internal/assist/seed/seed_gate_test.go`）。只有 kb 的 UPDATE 带 `updated_at` bump（向量索引指纹），
  另三张按 key/关键词精确直出、刷指纹无意义。`--only kb,scripts` 可分段跑。
- **★ 改「挂件 canned 译文的最终形态」必须同步抬 `cannedPromptRev`（★ 2026-09-29 〇-AB 立，本批真踩两次）**：
  欢迎词／chips 的译文按「原文 + 口径指纹」缓存在 `configs` 的 `i18n:<kind>:<lang>`，
  指纹＝`cannedPromptRev + translateContract(lang)`。射程不只是提示词措辞——**出栈卫生（剥回声／剥残渣）、
  判残与补翻口径**都算"最终形态"。不抬这一档，新二进制上线后老缓存继续把修前形态投给客户＝换件没修
  （第一次栽在改提示词口径，第二次栽在改译文出栈处理）。排障先读缓存键头 24 字符对指纹，别先怀疑模型。
- **★ 挂件「本轮用什么语言说」只有一个来源：`effectiveReplyLang(界面语言, 访客这一句)`（★ 2026-09-29 〇-AC 立）**：
  口径是用户 09-29 定稿的两半——**打开词／chips 按前台界面语言**（`LocalizeGreeting`／`LocalizeChips` 继续吃裸 `uiLang`），
  **后续每一条回复按访客这句输入的语言**，且**双向**接管（中文界面里的英文提问改口英文；英文界面里的中文提问改口中文）。
  因此 `backend-go/internal/assist/engine/` 里**任何新闸门都不许再直接读挂件送上来的 `uiLang` 来决定能不能出中文**：
  `visitorWantsChinese`／`replyLangBlock`／`enforceReplyLang`／`replyLangMismatch` 吃的必须是接管后的**作答语言**，
  而接管只在 `Engine.Respond` 这一个出站咽喉算（四道回复＋`fallbackReply` 兜底自动同享，加第五道也同享）。
  判据本身是刻意粗的：决定性字母（假名／谚文／西里尔／阿拉伯／泰文）一个即定性，拉丁要 **≥4 字母＋≥2 个不同英文虚词**，
  汉字 **≥4** 判中文（压过粘贴的英文材料），**判不出回 `""` 并回落界面语言**——猜错的后果只许是"这一轮没改口"，
  不许是"把法语提问硬拖成英文"（补翻只在汉字占主体时触发）。简／繁当一个**书写体系**处理（繁体界面继续出繁体，不因简体提问换档）。
  ⚠️ 历史形态：整套机制只看界面语言 ⇒ 中文界面里的英文提问既撞上中文 canned 话术直配（不经模型，会改口那一分支轮空），
  出站又判"中文界面不用翻" ⇒ 混排气泡原样送出（用户带截图报的缺陷本体）。
- **前端换源只走 tar，禁止 `scp -r dist`（★ 2026-10-03 〇-AP 收尾账：同一条现网泄漏第二次现形）**：
  macOS 的 `.DS_Store` 会跟着 `scp -r` 落进 web 根，而 Caddy／`spa.go` **对磁盘上真实存在的 dotfile 不拦**
  ⇒ 公网 `https://<站点>/.DS_Store` 直接 **200 可下载**（09-28 清过一次，10-01 那批用 scp 又把它带了回来）。
  硬口径三条：① 打包前 `rm -f dist/.DS_Store`（`tar` 前后各数一次 dirty 文件必须为 0）；
  ② 换源一律「scp tar → 服务器侧校验 tar 的 sha256 与本机一致 → 解到 `web.new` → 校验 `index.html` 入口与该入口引用的每个 asset 真落盘
  → 两步 `mv`（`web`→`web_old.<TS>`、`web.new`→`web`）→ `chown -R root:caddy`＋`chmod -R o+rX`」，
  ⚠️ 换完**按文件名时间戳**把 `web_old.*` 裁到 4 份（`ls -t` 会被 `cp -a` 继承的 mtime 骗到，见《部署指南》治理段）；
  ③ 换源后复验两件事：`find <两站 web 根> -name '.DS_Store' | wc -l` **回 0**，且公网打 `/.DS_Store` 只回 **SPA 兜底 HTML**
  （判据＝`content_type` 是 `text/html` 且字节数**等于该站首页大小**——回 `application/octet-stream` 或尺寸对不上就是磁盘上还有真文件）。
  ⚠️ 同一条判据**别只测主站**：演示站 `/opt/translator-demo/web` 与主站是两条独立换源链（本批实测演示站入口落后主站**两代**还在 09-30 那份）。

- **假成功不会让闸门红，只会让闸门钝（★ 2026-10-04 〇-AR 第 2 波立为硬约定）**：
  凡判据只问「信封里有没有 `"success":true`／状态码是不是 200」，上游真正坏掉时它照样绿灯——
  本批把译文出空收敛成失败之后**立刻红了三处**（双实例 M2/M4、M3 对账），三条都是**暴露**不是新引入的回归。
  两条写法必须同时成立：
  ① **成功类判据要同时问内容体非空**（`"success":true` ＋ `"translations":\{"en":"[^"]` 这类正锁），
     只有信封那条等于把「一次都没真翻出来」判成通过；
  ② **对账类判据必须在同一时刻取基线读数、只比增量**，禁止拿窗口消耗去比库里从建库攒到现在的流水合计
     （旧形态基线取在窗口之后，只要前面某条链路恒扣 0 就一直"对得上"，扣费一恢复当场假红）。
  ★ 第 5 波补腿又添第三条，形态不同但同族：③ **「某操作会打一行日志」这类判据锁的是文案与档位诚实，不是副作用真发生了**
     （把 `llmsource.Refresh` 里 `Publish(next)` 摘掉，那行「上游模型配置已热加载」照旧无条件打、档位读数完全合规，
     红的全在内容腿）。⇒ 判"生效"必须**由那一个消费方自己读出值来**（真拿这把 Key 发一次请求／读那一个快照字段），
     数日志次数只能用来抓"压根不走到这一行"那一族短路。
  另注意**测试脚手架的接线本身会造假绿**：`/api/admin/models/save` 只刷新写入那一个进程的 `s.Cfg`，
  第二台实例会带着启动期占位 Key 一直 401——要么启动时给两台都配好可用上游，要么断言里写清「这一条测的是共享库读写，不测跨实例热加载」。
  （★ 2026-10-05 第 5 波起产品侧已按「每台热加载」修掉这一缺口：见 §一·3 那条尺子，
  双实例闸门的 M5 段专门测它——**第三台实例 C 不带** LLM env 启动，必须自己在 A 侧保存后翻可用；
  第 5 波补腿又加**第四台实例 D**（启动时库里**已经有**可用配置＝现网演示单元那一台的形态），
  因为 C 那台对"水合值冒充 env"这条短路完全无感。）
