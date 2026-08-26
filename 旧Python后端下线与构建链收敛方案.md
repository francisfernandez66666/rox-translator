# 技术方案 · 旧 Python 后端下线与构建链收敛

> 版本 v1.1 ｜ 2026-08-26 ｜ 决策人：项目所有者（「老的 py 后端代码可以删除」）｜ 状态：✅ 已落地（除 §2.5 运维动作）
> 关联：《P0安全止血与并发原子性修复方案》P0-2 附带项（密钥泄漏治理）

## 〇、实施记录（2026-08-26）

| 项 | 状态 | 说明 |
|---|---|---|
| 删除清单执行 | ✅ | backend/、dist/、build/、build_app.sh、fix_damaged_app.sh、翻译助手.spec 已删除并移出 git 跟踪；.DS_Store/__pycache__ 同步清理 |
| 数据资产迁移 | ✅ | tm_embeddings.npz → data/（已入 .gitignore）；本地开发库改 data/dev.db（start.sh 自动建） |
| start.sh 重写 | ✅ | Go 开发链：编译→127.0.0.1:8787 启动，`-f` 起 vite 热更新、`-b` 强制重建前端 |
| build.sh 重写 | ✅ | Go 单二进制 .app 打包（launcher 起服务+开浏览器；日志入 ~/Library/Logs；无 UPX/xattr/kill 端口副作用）；bash -n 校验通过 |
| 文档同步 | ✅ | 部署指南 §一/§二 更新（fileproc 两个辅助 py 与旧栈的区分已注明）；PROGRESS.md 补架构决策记录 |
| §2.5 密钥吊销轮换 | 🔶 进行中 | **git 历史清洗 ✅ 已完成**（2026-08-26：filter-repo `--replace-text` 清文本 + `--blob-callback` 字节级清二进制 pyc，全历史/对象库双重复核 0 残留；filter-repo 会移除 origin，已重新添加；本地全量备份 bundle：`~/Desktop/翻译助手-git-backup-20260826.bundle`）；⏳ 待办：后台轮换两把 Key → GitHub 强推（`git push --force --all && git push --force --tags`）→ gitleaks 门禁 |

---

## 一、现状与风险

| 事实 | 证据 |
|---|---|
| backend/ 是桌面 .app 的唯一后端（PyInstaller 打包），Web/SaaS 早已由 backend-go 承接 | 翻译助手.spec:8 Analysis(['backend/main.py'])；build.sh:53 pyinstaller；vite 代理指向 Go 8787 |
| backend/main.py:38-44、clean_tm.py:45 **硬编码 SiliconFlow 与智谱真实 Key**，随 dist 安装包分发且已入 git 历史 | PyInstaller 产物可 pyinstxtractor 解包，等同明文张贴 |
| 双引擎并行必然行为漂移，维护双倍成本 | lib.py ≈4400 行 vs Go engine 两套实现 |
| 仓库卫生：tm.sqlite3 / tm_embeddings.npz / __pycache__/*.pyc 被 git 跟踪并持续变更；dist/ 打包产物（含泄漏 Key）在工作区 | `git status` 实测 |

## 二、改造内容

### 2.1 删除清单

- `backend/` 整目录（15 个源文件 + data 数据 + pycache）
- `dist/` 整目录（含泄漏 Key 的历史打包产物；重建流程见 §2.4）
- `build_app.sh`、`fix_damaged_app.sh`、`翻译助手.spec`（与 build.sh 平行的重复/补丁链，一并退役）

### 2.2 数据资产迁移

- `backend/data/tm_embeddings.npz` → `./data/tm_embeddings.npz`（Go 引擎 -kb 参数本地开发用）
- `backend/data/tm.sqlite3` → 不迁移（本地开发 KB 库按部署指南 `-kbdb` 参数现用临时库；
  若需保留历史本地 TM，手工拷贝到自选路径即可）。生产库在服务器 /opt/translator/data/，不受影响。
- `.gitignore` 追加：`data/`、`dist/`（构建产物与数据不入库）。
- 同步清理已被跟踪的垃圾文件：`.DS_Store`、`__pycache__`（git rm --cached）。

### 2.3 start.sh 重写（Go 开发链）

```bash
# 职责：一键启动本地开发环境
# 1) 编译 backend-go → /tmp/translator-server-dbg（无缓存直编）
# 2) frontend/dist 不存在时提示先 npm run build（或带 -b 参数代跑）
# 3) 启动 Go 服务 127.0.0.1:8787（-frontend frontend/dist -kb data/tm_embeddings.npz -kbdb data/dev.db）
# 4) 可选 -f 参数前台起 vite dev（代理已在 vite.config.ts 指向 8787）
```

不再出现 pip/venv/uvicorn/python 任何依赖；健康检查探 `/api/health`。

### 2.4 build.sh 重写（Go 单二进制桌面版）

桌面形态收敛为「Go 二进制 + 内嵌前端」：

```bash
# 职责：产出 macOS 翻译助手.app
# 1) npm run build 出 frontend/dist
# 2) go build -ldflags "-s -w" 出 backend-go 二进制
# 3) 组装 翻译助手.app/Contents/{MacOS,Resources}：
#    MacOS/translator   （Go 二进制，监听 127.0.0.1 随机端口）
#    Resources/frontend/dist（前端静态资源，-frontend 指向）
#    Info.plist（CFBundleExecutable=translator 等）
#    Resources/launcher：起服务后 open http://127.0.0.1:<port>
# 4) codesign ad-hoc 签名（正式分发需 Developer ID + 公证，脚本注释注明）
```

相对旧链路的收益：不再打 13MB npz 进包（KB 向量改为首次运行引导下载或按客户定制）、
无 UPX、无 xattr/sudo 补丁链、无 kill 端口副作用、无桌面日志污染（日志写 ~/Library/Logs）。

### 2.5 密钥泄漏治理（运维动作清单，非代码）

1. 【立即】SiliconFlow 控制台吊销 `sk-nzgxxx...`；智谱控制台轮换 `223ea45f...` 对应 Key。
2. backend-go 侧确认无硬编码（config.go 已是随机占位策略 ✅），新 Key 仅经环境变量注入。
3. git 历史清洗：`git filter-repo`（或 BFG）移除 backend/main.py、clean_tm.py 历史版本中的密钥串，
   强推后全体克隆重新拉取（单人仓库成本低，建议尽快执行）。
4. CI/本地预提交加 secret 扫描（gitleaks），防复发。

### 2.6 文档同步

- 部署指南.md「本地构建」「本地开发环境」章节：删除 Python venv/pip 相关行，
  本地启动命令改指 start.sh；依赖清单删 poppler 之外的 Python 段落说明保留（生产 PDF 管线仍需 venv：
  pdfwrite.py/docx_translate.py 属 backend-go/internal/fileproc 的辅助脚本，**不在本次删除范围**，
  注意区分：删除的是 backend/ 旧栈，fileproc 下两个 .py 是 Go 管线的子进程工具，必须保留）。
- PROGRESS.md 补一行架构决策记录：「2026-08-26 起 Web 与桌面统一 backend-go 单栈，Python 旧栈退役」。

## 三、边界与不做的事

- **保留** backend-go/internal/fileproc/*.py（pdfwrite.py、docx_translate.py）：PDF 两阶段管线的
  生产依赖，与被删除的旧栈无关。
- 不在本方案内实现 Apple Developer ID 签名/公证采购（运营动作，脚本已留注释位）。
- 不迁移任何旧 .app 用户数据（旧版本机数据目录独立，互不影响）。

## 四、验收清单

1. `ls backend dist build_app.sh fix_damaged_app.sh 翻译助手.spec` 全部不存在。
2. `./start.sh` 一键拉起 Go + 前端，登录/翻译全链路可用；`grep -rn "python\|pip\|uvicorn" start.sh build.sh` 零命中。
3. `./build.sh` 产出可双击打开的 翻译助手.app（本机 ad-hoc 签名），划词/翻译功能正常。
4. `git ls-files | grep -E "pycache|\.DS_Store|backend/"` 零命中；`data/`、`dist/` 已入 .gitignore。
5. 两把泄漏 Key 已在供应商控制台吊销/轮换（运维勾选）。
