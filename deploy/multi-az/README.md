# 多 AZ 部署（阶段七）

翻译助手后端为**单二进制无状态进程**：所有持久状态在 PostgreSQL（业务 + KB + jobs 队列账本）+ Redis（锁/信号量/配额/唤醒）中，进程本身不落地状态。因此多实例水平扩展只需「跑多个进程 + 共享 PG/Redis + 前置负载均衡」。

## 1. 架构

```
                 ┌──────────── Caddy（TLS + 会话亲和 + /metrics 仅内网）────────────┐
                 │  upstream: instance-1 / instance-2 / instance-3（同 AZ 或跨 AZ）   │
                 └───────────────────────────┬──────────────────────────────────────┘
                                             │  （无状态，任意实例可服务任意请求）
        ┌────────────────────────────────────┼────────────────────────────────────┐
   PostgreSQL（主）◄──流复制──► PostgreSQL（备，可跨 AZ）      Redis（主）◄──哨兵/集群──► Redis（备）
        │ jobs 表 / tickets / tenants / audit / KB（pgvector）     锁 / 信号量 / 日配额 / 唤醒信号
```

- **无状态进程**：JWT 鉴权（前端 localStorage 托管 token），无服务端会话态 → 任意实例可处理任意请求。
- **任务队列多实例安全**：`jobs` 表 Claim 为原子 UPDATE（PG 下按行重判，跨实例不会双领），配合 `leased_at` 租约超时由 `RecoverStale` 回收。Redis 信号器（`translator:queue:signal`）仅作「有新活」的低延迟唤醒通道，不承载任务数据。
- **多实例 LLM 并发上限**：`concurrency.Semaphore` 经 Redis 全局令牌桶（per-slot SETNX + TTL 看门狗），跨实例合计不超过 `LLM_MAX_CONCURRENT`，进程内 `chatFast` 保留槽恒本地。
- **API Key 日配额**：`ratelimit.Daily` 经 Redis `INCR` + 过期至次日零点，跨实例聚合；未启用 Redis 时降级 SQLite。

## 2. 前置依赖
- PostgreSQL 12+，启用 `pgvector` 扩展（见 `scripts/cutover-to-pg.sh`）。
- Redis 6+（哨兵或集群；单实例亦可，但失去高可用）。
- 反向代理 Caddy（提供 TLS、限流、会话亲和、公网只暴露必要路由）。

## 3. PostgreSQL 流复制（跨 AZ 高可用，提纲）
1. 主库 `postgresql.conf`：`wal_level=replica`、`max_wal_senders=10`、`hot_standby=on`。
2. 备库 `pg_basebackup` 基线 + `primary_conninfo` 指向主库 + `standby.signal`。
3. 故障切换：repmgr / Patroni / 云托管托管（RDS 多 AZ 只读副本 + 自动故障转移）。
4. 应用侧 `DB_DRIVER=postgres` + `DB_DSN` 指向**当前主库**（故障转移由连接串切换或 Proxy 解决）。

## 4. Redis 高可用
- 单实例（开发/小流量）：`REDIS_ADDR=127.0.0.1:6379`。
- 生产：哨兵 `REDIS_ADDR=<sentinel-host>:26379` 三节点，或 Redis Cluster。`internal/infra/redis` 当前为单节点客户端；切哨兵/集群时扩展 `redis.New` 接入 `go-redis` 哨兵模式（离线环境已用自研零依赖客户端，生产可平滑替换实现，接口不变）。

## 5. Caddy 会话亲和（sticky cookie）
JWT 无状态本无需亲和，但**大文件翻译等长连接 + 本地 pprof/调试**场景下，开启粘性可提升缓存命中。示例：
```
translator.example.com {
    reverse_proxy {
        sticky_cookies translator_sess
        to instance-1:8787 instance-2:8787 instance-3:8787
    }
    # 公网仅暴露业务路由；/metrics、/debug 仅限内网
    @internal remote_ip 10.0.0.0/8 192.168.0.0/16
    basicauth /metrics /debug /* { <hash> }
}
```

## 6. systemd 多实例（同机多进程示例）
复制 `translator@.service` 为 `translator@1`、`translator@2`，实例间仅 `WORKER_CONCURRENCY` 与端口不同：
```
[Unit]
Description=翻译助手实例 %i
After=network.target postgresql.service redis.service
[Service]
User=translator
EnvironmentFile=/etc/translator/secrets.env
Environment=INSTANCE=%i
Environment=WORKER_CONCURRENCY=4
ExecStart=/opt/translator/translator -listen 127.0.0.1:878%i
LimitNOFILE=65535
MemoryMax=900M
Restart=on-failure
[Install]
WantedBy=multi-user.target
```

## 7. 启动顺序与探活
1. 依赖：PostgreSQL、Redis 先就绪。
2. 首次切流：`scripts/cutover-to-pg.sh`（备份 → init-db → migrate → rebuild 向量）。
3. 启多实例：`systemctl start translator@1 translator@2 translator@3`。
4. 探活：`curl https://translator.example.com/status` → `{"ok":true}`；`/metrics` 由 Prometheus 抓取（Bearer `METRICS_TOKEN`）。
5. 监控：`deploy/observability/` 下的 Grafana 仪表盘 + Alertmanager 规则（熔断/错误率/余额/掉线/goroutine 泄漏）。
