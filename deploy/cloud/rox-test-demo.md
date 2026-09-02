# ROX 演示镜像部署手册（rox-test.lexicorn.cn）

> 目标：把现有首尔服务器上的生产 **ROX 租户**「拆分」出一个**独立演示镜像**，
> 专门稳定给老板演示，**即使生产发版也不受影响**。
>
> 方案：**同机独立实例** —— 独立目录 / 独立端口 / 独立 PostgreSQL 库 / 独立 systemd /
> 独立 Caddy 站点，数据从生产库克隆。

---

## 0. 为什么这样做（背景）

- 生产：`langcross.lexicorn.cn` → Caddy → `127.0.0.1:8787`，systemd `translator.service`。
- 发版流程：替换 `/opt/translator/bin/translator-server` + `systemctl restart translator`。
- 若直接在生产上演示，发版重启会中断演示、数据被生产操作污染，无法保证「演示专用稳定」。

因此拆分出独立演示实例：**发版只碰 `translator.service`，演示实例 `translator-demo.service` 与之一刀两断**。

---

## 1. 部署前准备

| 项 | 说明 |
|----|------|
| 服务器 | 首尔生产服务器（root） |
| DNS | 确认 `rox-test.lexicorn.cn` A 记录指向服务器 IP（若有通配符 `*.lexicorn.cn → IP` 则无需处理） |
| 依赖 | PostgreSQL 客户端（`psql` / `pg_dump`）、Caddy、`openssl`（服务器通常已具备） |
| 脚本 | 本仓库 `scripts/bootstrap-demo.sh`（请先 `git pull` 拉到最新） |

---

## 2. 一键部署

```bash
# 以 root 执行（脚本会自动探测生产 DB_DSN / secrets / systemd 配置）
sudo bash scripts/bootstrap-demo.sh
```

若自动探测失败（例如生产 DSN 不在常见位置），手动指定：

```bash
sudo PROD_DSN='postgres://langcross:<密码>@127.0.0.1:5432/langcross?sslmode=disable' \
     bash scripts/bootstrap-demo.sh
```

脚本执行内容（幂等，可重复跑）：

1. **目录与快照**：建 `/opt/translator-demo/`，复制生产二进制与前端 dist（快照，发版不影响）；
   **并复制生产 npz 向量索引**（`/opt/translator/data/tm_embeddings.npz` → 演示数据目录，`PROD_NPZ` 可覆盖；
   缺失仅告警不阻断；演示库为 PostgreSQL，npz 缺失时 DB 内 pgvector 检索仍可兜底（见 §3.2 治本修复）。
2. **克隆数据库**：`pg_dump` 生产库 → 恢复为 `langcross_demo`（含 ROX 租户全部数据、用户、KB 语料、向量）。
3. **演示库微调**：`primary_host` 指向演示域；`pay_mode` 置 `mock`（演示下单自动到账，老板演示最顺）。
4. **演示密钥**：写 `/etc/translator-demo/secrets.env`（0600），`JWT_SECRET`/`ADMIN_TOKEN` 复用生产
   （保证库内 `enc:v1:` 加密密钥可解密、登录 token 可验证——镜像的本质）。
5. **systemd**：安装 `translator-demo.service`（端口 8789，独立 env、低并发、内存护栏；`ExecStart` 带 `-kb <演示数据目录>/tm_embeddings.npz`，见下方 §3.2）。
6. **Caddy**：生成 `/etc/caddy/translator-demo.conf`（`rox-test.lexicorn.cn` → 127.0.0.1:8789，`request_body max_size 40MB`；SPA 首页走后端 `handleSPA` 注入品牌，消除先主站后演示站的闪烁）并在主 Caddyfile 引入、reload。

---

## 3. 部署后验证

```bash
# ① 本机探活（应 200）
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8789/api/health

# ② 确认演示实例用的是演示库（dialect=postgres 即正确）
curl -s http://127.0.0.1:8789/status | grep -o '"dialect":"[^"]*"'

# ③ 公网访问（证书签发可能需要几十秒到几分钟，首次访问若失败稍后重试）
curl -s -o /dev/null -w "%{http_code}\n" --max-time 20 https://rox-test.lexicorn.cn/api/health

# ④ 用演示专用账号登录（脚本已种入，仅存在于演示库，密码统一 Demo#2026Rm!）
#    demo_admin（企业管理员）/ demo_youtube / demo_hr / demo_cs（普通用户）
curl -s -X POST https://rox-test.lexicorn.cn/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"demo_admin","password":"Demo#2026Rm!"}'

# ⑤ 服务状态
systemctl status translator-demo
journalctl -u translator-demo -n 30
```

---

## 4. 维护操作

| 场景 | 操作 |
|------|------|
| 重启演示 | `systemctl restart translator-demo` |
| 查看日志 | `journalctl -u translator-demo -f` |
| 刷新演示数据 | `sudo -u postgres dropdb langcross_demo && sudo bash scripts/bootstrap-demo.sh`（重新克隆 + 自动种入演示账号 + 品牌域修正） |
| 重置演示账号密码 | 直接用新 bcrypt 哈希替换 `scripts/bootstrap-demo.sh` 中 4.5 步骤的 `SEEDSQL` 用户行，重跑脚本即可（幂等） |
| 跳过演示账号种入 | 执行前 `export DEMO_SEED_ACCOUNTS=0` |
| 演示环境想单独改数据 | 直接在演示后台操作即可，不影响生产 |
| 停止演示 | `systemctl disable --now translator-demo` |
| 完全卸载 | `systemctl disable --now translator-demo; rm -rf /opt/translator-demo /etc/translator-demo /etc/caddy/translator-demo.conf`（并从主 Caddyfile 删除 import 行后 reload） |

> 注：脚本已自动将演示库租户1的品牌子域从 `rox` 改为 `rox-test`，避免登录响应 `brand_host=rox.lexicorn.cn` 导致前端强制跳回生产域名。
> 另注意：演示库 `primary_host` 必须保持主站 `langcross.lexicorn.cn`（**不可**改成演示域）。
> 若改成演示域，品牌接口会把 `rox-test` 判为主站前缀而返回平台品牌（空），租户1的品牌定制
> （logo/首页背景/网页标题）在演示站不展示——数据本身已随克隆带入，只是解析层被跳过。
> 邮件说明：演示库已同步 `alert_email=noreply@lexicorn.cn` / `alert_email_cc=575160894@qq.com`
> （供「我已付费」通知链路验证），但演示服务本身未配置 SMTP，邮件为 Noop（仅入 jobs 队列并打印日志，不真实外发）。

### 3.1 行业包/语言文化包自动采集演示（★ 2026-09-01 新增）

- 演示入口：用 `demo_admin`（超管 L4）登录 → 管理后台「🕷️ 数据采集」面板。
- 可演示链路：新增数据源（如官方 API / 受限抓取 / LLM 生成）→「立即采集一轮」→ 待审增量池出现
  tier 1/2/3 条目与安全句 → 批量「通过并热加载」→ 该包术语即时进入后续翻译参考（CJK 缓存已失效重建）。
- 采集为低占用后台任务（无排队工单 + LLM 错误率低 + RSS 低于阈值时才跑），演示高峰时段可能
  处于暂停态，点「立即采集一轮」仅触发一轮探测，不会挤占翻译；护栏与断点续传配置见《部署指南》§八-B4。
- 注意：演示库克隆自生产时 `kb_pack_sources`/`kb_staged_*` 表与数据随库带入；重复刷新克隆即回到初始态。

### 3.2 npz 向量索引与 `-kb` 参数（★ 2026-09-01 修复说明）

- **背景**：早期演示镜像只带 `-kbdb`、漏带 `-kb`（npz 向量索引），导致演示站**语义检索整层被跳过**，
  知识库术语（如 极石→ROX、车主→owner）在工单翻译时无法命中、直接丢给模型自由发挥。
- **修复**：
  1. 临时修复：脚本已改为复制生产 npz 并在 `ExecStart` 增加 `-kb ${DEMO_USER_DATA}/tm_embeddings.npz`
     （演示库是 PostgreSQL，npz 缺失时 `pgvector` 语义检索在旧代码里也被 `if idx != nil` 一并跳过）；
  2. 治本修复（主站+演示站同一代码）：`engine.go` 语义检索块重构——pgvector 不再依赖 npz 索引，
     任一源（pgvector 或 npz）可用即执行语义检索；`workflow.go` 把 kb_match 阶段收集的 KB 命中例句
     （`res.Examples`）持久化并注入 AI 初翻 prompt，KB「部分命中」的术语也会被模型沿用。
- **验证**：重跑 `sudo bash scripts/bootstrap-demo.sh` 后，用演示账号翻译含 极石/车主 的句子，
  应命中知识库译法（模式不再显示「纯模型翻译/无知识库」）；或在 `journalctl -u translator-demo`
  中确认启动日志出现 `知识库向量索引已加载: N 条`。

---

## 5. 隔离性与稳定性说明

- **发版隔离**：生产发版脚本只动 `/opt/translator/bin/`、`/opt/translator/web`、`translator.service`；
  演示实例全部在 `/opt/translator-demo/` + `translator-demo.service`，物理隔离，互不干扰。
- **数据隔离**：演示库 `langcross_demo` 独立；生产写操作不影响演示，演示写操作不影响生产。
- **密钥复用**：`JWT_SECRET` 复用生产，是为了克隆库内加密密钥可解；如担心安全可轮换，
  但需同时重写库内 `enc:v1:` 密文（成本高，演示场景不推荐）。
- **内存**：演示实例已降低并发与内存（`GOMEMLIMIT=450MiB`、`MemoryMax=700M`、worker=1）。
  若生产与演示同时高负载，请留意 `free -m`；极端情况可进一步压低演示参数或错峰演示。

---

## 6. 常见问题

| 问题 | 处理 |
|------|------|
| `bootstrap-demo.sh` 报「无法探测生产 DB_DSN」 | 用 `PROD_DSN=...` 环境变量手动传入（见 §2） |
| 演示服务起不来 | `journalctl -u translator-demo -n 50`；重点看 `REQUIRE_PROD_SECRETS=1` 是否缺 `ADMIN_INIT_PASSWORD`（脚本已自动补齐） |
| 公网访问 502 | Caddy reload 未生效或端口未监听；`systemctl status caddy`、`curl 127.0.0.1:8789/api/health` |
| 证书未签发 | 首次访问稍等再试；确认 DNS A 记录；不要用 root 跑 `caddy validate`（会污染日志权限） |
| 演示数据里混入生产最新改动 | 需要时重跑刷新流程（§4） |
| 想改演示支付方式为静态码/在线 | 演示后台「套餐中心」或直接改演示库 `system_config.pay_mode` 后重启演示服务 |

---

## 7. 与生产实例的参数对照

| 项 | 生产（langcross） | 演示（rox-test） |
|----|-------------------|------------------|
| 域名 | `langcross.lexicorn.cn` | `rox-test.lexicorn.cn` |
| 目录 | `/opt/translator/` | `/opt/translator-demo/` |
| 端口 | 127.0.0.1:8787 | 127.0.0.1:8789 |
| systemd | `translator.service` | `translator-demo.service` |
| 数据库 | `langcross` | `langcross_demo` |
| secrets | `/etc/translator/secrets.env` | `/etc/translator-demo/secrets.env` |
| Caddy conf | `/etc/caddy/translator.conf` | `/etc/caddy/translator-demo.conf` |
| 发版影响 | 是（发版目标） | **否（完全隔离）** |
