# 首尔服务器 PostgreSQL + pgvector 切流执行手册

> 适用范围：首尔机房 VPS（2G 内存 / 双核），Debian/Ubuntu 系。
> 目标：把翻译助手从 SQLite 切到 PostgreSQL + pgvector（解锁向量召回 / 多副本水平扩展）。

---

## 0. 为什么是「手动在首尔执行」而不是由助手直接部署

沙箱环境为**离线**、无云厂商凭证、无到首尔服务器的网络通道，无法代你创建 RDS / SSH 推包。
因此本仓库已交付：

- 可在本地通过 `DB_DRIVER=postgres` 完整启动并验证 PostgreSQL 路径（已在本机 PostgreSQL 15 + pgvector 0.8.0 跑通：`--init-db` 建表含 `tm_segments.embedding vector`、`/status` 返回 `dialect=postgres`、全量路由 200）。
- 可复用的切流脚本 `scripts/cutover-to-pg.sh`（建表 / 可选迁移 / 重建向量索引 / 起服务 / 校验）。
- 本手册 + `scripts/bootstrap-seoul.sh`（一键装 PG + pgvector + 2G 调优）。

你在首尔服务器上按顺序执行下面三步即可完成切流。

---

## 1. 准备（一次性）

```bash
# 1) 拿到代码（已包含 backend-go / scripts / frontend）
git clone <你的仓库> translator && cd translator

# 2) 装好 Go 1.22+ 与前端构建依赖（npm）
go version          # 需 >= 1.22
node -v && npm -v   # 需构建前端 dist（或复用已构建产物）
```

> 2G 内存机器 `go build` 会吃内存但能完成；如担心，可在本地 `CGO_ENABLED=1 go build -o translator ./cmd/server`
> 后把二进制拷到服务器（减少服务器编译压力）。

---

## 2. 初始化数据库底座

```bash
sudo bash scripts/bootstrap-seoul.sh
```

脚本会：安装 PostgreSQL + pgvector → 建角色/库/扩展 → 写入 `conf.d/02-seoul-2g.conf`
（2G 保守参数）→ 重启 → 打印 `DB_DSN`。**请记录打印出的 `PG_PASS` 与 `DB_DSN`**。

可选环境变量：`PG_DB` / `PG_USER` / `PG_PASS` / `PG_PORT` 覆盖默认值。

---

## 3. 执行切流

```bash
# 把 bootstrap 打印的 DSN 填进来（务必与本机监听一致）
export DB_DRIVER=postgres
export DB_DSN='postgres://langcross:<密码>@127.0.0.1:5432/langcross?sslmode=disable'

# 若从既有 SQLite 迁移（默认数据文件 ~/Library/.../tm.sqlite3 或部署包内 tm.sqlite3）：
export SQLITE_SRC='/path/to/tm.sqlite3'   # 无源则跳过迁移步骤

# 如需重建向量索引，必须提供可用的 LLM Key（rebuild-kb-index 会调用 Embedding）：
export ONLINE_API_KEY='sk-...' EMBED_API_KEY='sk-...'

bash scripts/cutover-to-pg.sh
```

脚本内部步骤：

1. `go build` 单二进制（CGO 启用，便于 pgx 静态链接）。
2. `server --init-db` 建表（含 pgvector 列、默认租户 rox）。
3. （可选）`migrate-sqlite-to-pg` 迁存量数据。
4. （可选）`rebuild-kb-index` 重算向量召回（需 LLM Key）。
5. 起服务并 `curl /status | grep dialect` 校验返回 `postgres`。

全部通过即切流成功。

---

## 4. 生产守护（systemd）

`cutover-to-pg.sh` 会尝试写 `/etc/systemd/system/translator.service`；如未自动生成，
用下方单元（记得填入真实 `DB_DSN` / 密钥）：

```ini
[Unit]
Description=翻译助手 langcross (PostgreSQL)
After=network.target postgresql.service

[Service]
User=translator
WorkingDirectory=/opt/translator
Environment=DB_DRIVER=postgres
Environment=DB_DSN=postgres://langcross:<密码>@127.0.0.1:5432/langcross?sslmode=disable
Environment=JWT_SECRET=<32+ 字节随机>
Environment=ADMIN_TOKEN=<强随机>
Environment=ADMIN_INIT_PASSWORD=<强密码>
Environment=REQUIRE_PROD_SECRETS=1
ExecStart=/opt/translator/translator -addr 127.0.0.1:8788
Restart=on-failure
MemoryMax=1536M          # 2G 机器给服务留 1.5G，余量给 PG/OS

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload && systemctl enable --now translator
```

---

## 5. 回滚

PG 与 SQLite 数据文件相互独立；若出现必须回退的情况：

```bash
systemctl stop translator
# 改 env：DB_DRIVER 置空（或 sqlite）+ 指向原 tm.sqlite3，重启即可回到 SQLite。
# PG 中已写入的数据不受影响，随时可再次切回。
```

---

## 6. 2G 调优要点（已在 bootstrap 写入）

| 参数 | 值 | 说明 |
|------|----|------|
| shared_buffers | 512MB | 物理内存 ~25%，防 OOM |
| effective_cache_size | 1GB | 告诉规划器 OS 页缓存约 1G |
| max_connections | 40 | 双核小机器，连接数压低 |
| work_mem | 16MB | 单查询排序/哈希上限 |
| maintenance_work_mem | 128MB | pgvector 建 IVFFlat 索引需要 |
| random_page_cost | 1.1 | SSD 盘，随机 IO 代价调低 |

---

## 7. 已在本机验证（离线沙箱）

- 本地 PostgreSQL 15.18 + pgvector 0.8.0 启动成功。
- `server --init-db` 在 PG 上建出完整 schema，含 `tm_segments.embedding vector` 列。
- `server`（DB_DRIVER=postgres）启动后：`/api/health`、`/metrics`、`/openapi/v1.json`、
  `/api/sso/providers`、`/status` 全部 200，`/status` 返回 `dialect=postgres`。
- 默认租户 `rox` 已就绪，向量召回路径可用。

> 唯一无法在沙箱完成：真实 RDS/首尔服务器的创建与推送（无网络/凭证），以及需要 LLM Key 的
> `rebuild-kb-index` 向量重算、真实 IdP 授权码交换、Grafana/Loki 实例拉起、k6/Playwright 实跑。
> 这些在首尔服务器具备网络与密钥后按本手册执行即可。
