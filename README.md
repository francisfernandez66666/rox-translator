# 能言 SaaS

> 面向企业与团队的 AI 翻译协作平台，文件翻译、对话翻译、知识库一体化，支持 Token 预充值商业化。

---

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 单二进制，PostgreSQL + pgvector，REST API |
| 前端 | React + TDesign（Vite 构建） |
| 桌面端 | `./build.sh` 构建 macOS `.app` |
| 生产部署 | systemd + Caddy（反向代理 + 自动 TLS） |
| SDK | Python / TypeScript / Java |

---

## 快速开始

```bash
git clone <仓库地址>
cd 能言
./start.sh        # 编译 Go 后端并启动 http://127.0.0.1:8787
# ./start.sh -f   # 同时启动 vite 热更新前端（:5174）
```

首次运行会自动初始化数据库。启动后请访问控制台并按提示修改默认管理员密码。

手动构建示例：

```bash
cd frontend-react && npm install && npm run build
cd ../backend-go && go build -o translator-server ./cmd/server
./translator-server \
  -addr 127.0.0.1:8787 \
  -frontend ../frontend-react/dist \
  -kbdb data/dev.db
```

---

## 目录结构

```
能言/
├── backend-go/        # Go 后端源码（单二进制）
├── frontend-react/    # React + TDesign 前端
├── deploy/            # systemd、Caddy、冒烟/压测/演练脚本
├── scripts/           # 数据库初始化与切流脚本
├── extension/         # 浏览器插件
├── sdk/               # OpenAPI SDK（Python/TypeScript/Java）
├── data/              # 运行时数据（SQLite/PostgreSQL 数据与向量文件）
├── start.sh           # 本地开发一键启动
└── build.sh           # macOS .app 构建
```

---

## 核心能力

- **多格式文件翻译**：docx/pptx/xlsx/pdf/txt/csv/md 输出译文文件；srt/vtt/json/yaml 等以对照表（xlsx）形式交付；支持多目标语言打包下载
- **对话式翻译**：聊天交互、上下文审校、风格指令
- **知识库与翻译记忆（KB/TM）**：pgvector 向量检索；五档可见范围（部门 > 跨部门 > 企业 > 行业 > 通用语言习惯包）按优先级/相似度排序；组织继承链与部门级隔离
- **租户与组织隔离**：多租户、角色权限、部门预算
- **品牌定制与登录页布局**：按子域名解析租户品牌（名称/Logo/背景图）；登录页支持全屏背景或左右分栏
- **Token 计费**：双桶台账（发放额度 + 永久余额）、预充值、订阅与发票
- **邀请裂变**：推荐注册、邀请奖励
- **注册与邮件模板**：自助注册分个人/企业两类；超管可配置多用途邮件模板
- **OpenAPI**：API Key、异步任务模型（创建/轮询/下载）、同步短文翻译
- **管理后台**：仪表盘、审计日志、系统配置、模型路由、告警

---

## 部署要点

- 生产形态为单个 Go 二进制 + `frontend-react/dist` 静态资源
- 数据库：PostgreSQL 16 + pgvector（同机自建）
- Go 服务通过 `-frontend` 参数托管前端 dist
- `deploy/` 目录提供：
  - systemd 服务模板与 drop-in 配置
  - Caddy 反向代理与安全头配置
  - 部署检查、冒烟、负载测试、灾难恢复演练脚本
- 详细步骤见《部署指南.md》

---

## 文档索引

| 文档 | 说明 |
|---|---|
| [部署指南.md](部署指南.md) | 生产环境安装、systemd/Caddy 配置、依赖与目录 |
| [PROGRESS.md](PROGRESS.md) | 项目进度、当前生产状态 |
| [权限关系.md](权限关系.md) | 租户-组织-部门-角色权限模型 |
