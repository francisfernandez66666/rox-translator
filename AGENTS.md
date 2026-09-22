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
  **部署口径**：改动落在 ②③ 必须换 `translator-server`，落在 ④ 必须换 `translator-assist`，
  落在 ⑤ 需重打扩展包——只换 `/opt/translator/web` 一律不生效。
  ⚠️ ④ 额外一层：生产 `secrets.env` 留着 `ASSIST_WEB=/opt/ai-assist/web`，**外置文件优先于 `go:embed`**，
  所以换二进制仍可能是旧页——改 `internal/assist/web/*` 必须同步外置 `admin.html`（2026-09-22 〇-LI 踩过）。

### 6. e2e 断言红线

- **禁止在 `frontend-react/e2e/` 写死外部/生产域名或依赖 CI 不存在的环境变量的用例。**
  这类用例会永久性把发布闸门拖红，钝化对真回归的敏感度（2026-09-17 已因此清理 `_tmp_admin.spec.ts` / `_tmp_iframe.spec.ts`）。
- 需要人工环境（生产探针、手工 Token 等）的用例一律放 `frontend-react/e2e-manual/`，
  并在文件头加 `test.skip(!process.env.XXX, '...')` 守卫，附「为何手工」的注释。
- 生产探针职责由 `deploy/` 下的冒烟脚本承担，不进 Playwright 矩阵。

### 7. Shell 脚本断言写法（UAT 脚本）

- **正则交替一律用 `grep -E 'a|b'`，禁止 BRE 的 `grep 'a\|b'`。** `\|` 是 GNU 扩展，
  BSD grep 之外的实现（如部分精简 shell 环境与容器基础镜像自带的 grep）会把它当字面量，
  **静默返回 0 命中**——断言会「永远通过/永远失败」而不报错，是最难发现的一类闸门失效。
  （2026-09-17 实测：`scripts/uat/api_uat_txn.sh` T16 因该写法恒判 refused=0 而误报失败。）
- 断言脚本避免依赖外部环境的行为差异；涉及金额/计数的断言优先用 `-E` + 明确锚点。

---

## 二、提交前闸门（必须全绿）

```bash
cd backend-go && go build ./... && go vet ./... && go test -race ./...
cd ../frontend-react && npx tsc --noEmit && npm test && npx vite build
bash scripts/uat/assist_uat.sh                    # AI 顾问（自起临时实例，不碰生产）
bash scripts/uat/run_uat.sh                       # 全链路主矩阵（PG 方言，发布闸门）
bash scripts/uat/multi_instance_e2e.sh            # 多实例红线
```

改动触及计费/对账时，`run_uat.sh` **必须**跑 PG 方言（SQLite 快跑不能替代）。

---

## 三、改动原则

- 优先**薄委托 + 零改动调用点**的收敛手法（见 `store/crypto.go` 下沉 `internal/secret`），
  避免大爆炸式重构：改动面 >3000 行且无行为收益的重构不做。
- 修复缺陷时同步补一条能复现的自动化断言（单测或 UAT 断言），否则视为未完成。
- 涉及 DB schema 的改动必须写成幂等迁移，保证老库启动自动升级。
