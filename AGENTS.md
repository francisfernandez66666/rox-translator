# AGENTS.md — 工程约定（本仓全体贡献者／AI 助手必读）

> 建立于 2026-09-17（改造 2 第二步）。这里只记**必须遵守的硬约定**，不重复产品文档。
> 详细架构与部署见 [README.md](README.md)、《部署指南.md》。

---

## 一、代码组织约定

### 1. store 冻结规则（★ 强制）

`backend-go/internal/store/` 是单一数据访问包（52 个非测试文件 / 约 15.2k 行；含 44 个单测文件为 96 文件 / 约 22.6k 行），
被 `internal/api/` 中 46 个非测试文件直接 import（2026-09-22 实测校准，#74/#75 批次新增 6 个域文件）。
为控制复杂度增长，确立以下冻结规则：

1. **`billing.go`（2206 行）与 `kbpackages.go`（1371 行）只减不增**（行数口径＝含注释的非测试行数，规则本身指「不追加新方法」；2026-09-22 实测校准）。**
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
  （分根设基线 `internal` 176 / `cmd` 69，2026-09-18 P2-3 建立、2026-09-22 #42 纳入 `cmd/` 盲区并
  随迁移下调 `internal` 基线；总数不设基线，防「cmd 新增被 internal 下降掩盖」）。
  迁移优先级 engine → fileproc → orchestrator，按包随改动顺带清，
  每降一批同步下调该文件里的 `logPrintfBaseline`。

### 3. 密钥与配置

- 密文配置一律 `internal/secret.EncryptSecret` / `DecryptSecret`（`enc:v1:` 前缀，兼容历史明文）。
- 明文优先序：**环境变量 > 数据库配置**。密文 key 在管理台以掩码（含 `****`）回显，
  保存链路用 `IsSecretMasked` 判断并回填旧值，禁止把掩码写回库。

### 4. 数据库方言

- 所有 SQL 必须同时支持 SQLite（本地/CI 快跑）与 PostgreSQL（生产）。
  用 `db.CurrentDialect()` 分支或 `db.Exec(s.db, db.CurrentDialect(), ...)`，禁止硬编码方言语法。
- 列迁移、`COALESCE` 取值、时间函数是历史高频踩坑点。
- **新增/改动后端单测必须自钉方言**：`config.Default()` 读 `DB_DRIVER` env 且**副作用写全局 `config.C`**，
  run_uat PG 模式下会泄漏方言给同包后续内存 SQLite 测试（`no such table: information_schema.tables` 假红，
  2026-09-19/20 两连败）。模板：`old := config.C; cfg := config.Default(); cfg.DatabaseDriver = "sqlite"; config.C = cfg; t.Cleanup(restore)`，
  并整包 `env DB_DRIVER=postgres DB_DSN=... go test -count=1 ./internal/<pkg>/` 自检一遍（单测 `-run` 不复现）。
  G3 预检另钉 `env DB_DRIVER=sqlite`，PG 方言覆盖由 UAT 矩阵承担。

### 5. 前端约定

- 风格计量单位统一**积分口径**，公开接口零 token 裸值。
- **接口层失败必须经 `bizResp` 收敛（★ 2026-09-27 〇-U 批 I-10 立为硬约定，与 §一·8 配对）**：
  后端状态码诚实化（F-64①②③）之后，`src/api/*.ts` 里凡是返回 `{success,...}` 信封的调用一律写
  `bizResp(() => request(...))`，**禁止直返裸 `request`**——`bizResp` 把结构化 4xx 失败体还原成历史
  `{success:false,...}` 形态（details 摊平、401/403 照抛以触发重登录），调用方零改动；绕过它会让面板
  对着抛出的 ApiError 白屏。新增 api 文件须带 F-64② 文件级中文注释（现存 17 个文件已全部接线）。
- 组件测试用 vitest + jsdom（`*.dom.test.tsx`）。
- **i18n 为 12 语种口径**（★ 2026-09-20 全站十语种升级）：zh/en 全量词典（`panels/*.ts` 双语同步），
  其余十语种 `src/i18n/locales/*.ts` 为 **ALL_KEYS 全量词典**（2026-09-20 起建 2532 键，此后随批次增长，
  2026-09-22 实测 2746 键逐键覆盖，`locales.core.test.ts` 以 ALL_KEYS 动态长度为基准红灯拦截，不钉死数字；
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
  ⚠️ **品牌注入还要看「首页由谁直出」**（★ 09-27 F-74 实测）：`window.__BRANDING__` 由 `spa.go` 的 `serveIndexHTML`
  **无条件**注入，所以只有**首页走后端**的域名拿得到它；首页若由 Caddy `file_server` 静态直出（现网主站即此形态），
  品牌只剩前端异步兜底。判据一条：`curl -s <站点>/ | grep -c __BRANDING__`，同时看首页 sha 是否**等于**仓库
  `frontend-react/dist/index.html`——相等就说明是静态直出，别去前端找「品牌不生效」。
  ★ **租户品牌域名（`*.lexicorn.cn` 通配块，〇-W/F-73）改动的三条硬口径**：① 通配块兜底段**必须 `reverse_proxy`，
  不许写回 `file_server`**（就是上面那条 F-74 教训的落地位置，`/assets/*` 才由 Caddy 落盘直出）；
  ② 动过 `/etc/caddy/*tenant*.conf` 或该 conf 的 import 行，**发版验收必须跑 `bash deploy/smoke_tenant_domain.sh`**
  （15 判据，全只读；本地单测/vitest 看不见反代路由表这一层，这正是 F-73 能藏这么久的原因）。
  该脚本自带反向对照：`TENANT_BASE=https://<主站域> bash deploy/smoke_tenant_domain.sh` **必须 FAIL≥2／exit 1**，
  若反而全绿说明判据失效，别把绿灯当"主站也通了"；
  ③ **证书口径＝`tls internal`，公网可用只因为 Cloudflare 该 zone 是 Full 而非 Full(strict)**——切 strict 前必须先换
  Cloudflare Origin CA（`/etc/caddy/tls/tenant-origin.{crt,key}`，`chmod 600`，**Origin CA 私钥禁止进聊天/文档/git**），
  否则新租户子域全体 526。另注意**读侧匹配是 `WHERE domain=?` 的裸小写前缀精确等值**（F-76 未修：填 `ROX`/整域名/带斜杠
  会保存成功却永不生效），排查「品牌不生效」先核库里那一列的字面值，再核 ①②③。
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
  `writeJSON(w, 4xx/5xx, map{...})`。** 全包棘轮 `TestErrorStyleRatchet`（基线 628，
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

---

## 二、提交前闸门（必须全绿）

```bash
cd backend-go && go build ./... && go vet ./... && go test -race ./...
cd ../frontend-react && npx tsc --noEmit && npm test && npx vite build
bash scripts/uat/assist_uat.sh                    # AI 顾问（自起临时实例，不碰生产）
bash scripts/uat/run_uat.sh                       # 全链路主矩阵（PG 方言，发布闸门）
bash scripts/uat/multi_instance_e2e.sh            # 多实例红线
bash scripts/build_extension.sh --check           # 扩展漂移闸门（动过 extension/ 却没重打 zip 时红灯；vitest 同口径）
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
- 修复缺陷时同步补一条能复现的自动化断言（单测或 UAT 断言），否则视为未完成。
- 涉及 DB schema 的改动必须写成幂等迁移，保证老库启动自动升级。
- **改了 assist 的 `seed/seed.json` 不等于线上改了。** seed 只在**首启空表**时灌库，存量库不跟进，
  所以关键词/文案修订必须随批跑一次 `python3 scripts/assist_kb_sync.py --host <服务器>`（默认只读打差异，
  确认后 `--apply`，写前自动备份）——这一步此前长期挂「待人工执行」，2026-09-23 已由脚本收口，
  口径见《部署指南》§十三。同理「线上管理台在线改的内容」优先级高于 seed，脚本**绝不 DELETE**。
