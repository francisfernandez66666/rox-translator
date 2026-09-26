# 能言 SaaS · 项目进度总览

> 最后更新：2026-09-27（**〇-U：发布前E2E_UAT_20260926 第二轮客户级实跑 78 例 → F-44～F-71 逐条核码 → 批 I-1～I-10 全落地＋断言缺口补齐＋注释/提交/推送链收口**——F-64 状态码诚实三档（①收款/账务 71 处 ②管理台与业务动作 182 处 ③鉴权分流 122 处全迁 `writeError`/`writeAuthzError`，前端 17 个接口文件 `bizResp` 接线并逐文件补中文注释）、F-63 审计小票「实单实额」（含固定串负向锁）、F-44③ 读侧吞错 500 诚实闸（含摘闸反证）、O-1 mock 支付二次确认＋审计 ⚠ 标记、F-67 USDT 声明/确认三视图锁、T63 鉴权分流 HTTP 级常设锁、O-9 平台上下文余额胶囊守卫、SDK 四语言偏差账收口＋`public/sdk/` 重打（1.0.2）；09-27 全量闸门绿：`go test -race` **42 包全 ok**、assist **48/0**、`run_uat.sh`（PG）**A/B 130/0 ＋ T 643/0 ＋ 前端 E2E exit=0**（T42-f67 与 T63 新腿首跑即绿）、多实例 **8/0**、扩展 1.2.2/SDK 1.0.2 漂移 ✅、vitest **76 文件 567 用例全绿**（初跑 5 条负载型假红：单跑 34/34＋整套原样复跑全绿，未放宽任何断言）、tsc 0、引号闸门 2993 行 0 处；**本轮三红全部归因为「测试自伤」而非产品回退**（H1 被 T45/T47 轮换失效→T52 段前全局刷新、O-9×F-48 交界→移动端余额用例改租户口径账号、P2d 冷启动载入窗→轮询到件数稳定再量）。本地代码提交 `ba9ba71`（115 文件，+3594/−711，零 `.md`、零 UI 交付包）→ 纯代码推送 `c4fe807`＋并轨 `551b2c1`·文档仅本地；本地测试数据已清理（`translator_uat` 库删除、四端口释放、WORK 目录与 /tmp 调试件全清）。**两站发版已完成（09-27 02:49 两站同批：`translator-server` 同 sha `4719aa89…`、两站首页换源 `index-brebiSnS.js`、演示站 `pay_mode` mock→static_qr 达成 O-1 红线；发版中现场根因并修复「批 H 运营备份表属主留在 postgres ⇒ 两库自动备份自批 H 起一直产出 0 字节 dump」——详见下方「〇-U」节第 9 条与《部署指南.md》〇-U 发版记录。）
> 上一批（〇-T）记录：2026-09-26（**〇-T：发布前客户级 E2E UAT 缺陷全量修复收口＋自动化测试固化批**——批 A–G 全部缺陷施工并独立复验、批 H 遗单挂账；发布闸门终局全绿：run_uat（PG）**A 95/0 · B 113/0 · T 515/0 · 前端 E2E 70/0**、竞态 40 包全 ok、assist 48/0、多实例 8/0、扩展/SDK 漂移 ✅、tsc/vitest 65 文件/vite build 全绿、注释闸门双 0；本地代码提交 `5e914c9`（122 文件）→ 纯代码推送 `d892de3`＋并轨 `4a1e20b`·文档仅本地；本地测试数据已清理（超管测试号按令保留）。**09-26 00:51 已随批发版：主站＋演示站两站同批前后端上线（`translator-server` sha `1fbd8793…` 两站一致、dist `index-BOIA4YDV.js`、assist/扩展未动），批 H 随发版动作完成（12 语种手册铺库+`manual_pdf_dir`、SDK `/sdk/` 首发、尺子 33222 上柜·两库均先建备份表），deploy_check 内网 11/11×2＋公网 8/8×2、/sdk 五探魔数全过；存量处置项（id55/90 号/carry/TM 候选/订单 5·7）仍按令挂账待确认。**详见下方「〇-T」节。）
> 上一批（〇-S 增补）记录：2026-09-24（**〇-S 增补：语言识别收口 "error" 字段类 + A3lang 语种 UAT 入矩阵 + 像素锁按 〇-P 实测重钉**——本地代码提交 `c72a2f3` → 纯代码推送 `977f9a2`（8 文件，零 .md、零 UI 交付包）·文档仅本地；全量门禁复跑绿：后端 trio（含 -race）、前端 tsc/vitest 54 文件/vite build、`run_uat.sh`（PG）**API 102/0 + T 套件 510/0 + 前端 E2E exit=0**、`assist_uat` 48/0、多实例 8/0、扩展漂移 ✅、注释闸门 0 缺口；本地测试数据已清理（DB 备份后删）。详见下方「〇-S 增补」节。上一批（〇-S 主批，前台整合＋后端语言识别**——本地代码提交 `e4b263a` → 纯代码推送 `a19e2ec`（17 文件）·文档仅本地；门禁后端 trio 全绿（含 -race 全量）、前端 tsc 0/vitest 54 文件 405 用例全绿/vite build 成功；真机双语闭环已验。（当时部署未做，已于 09-24 23:28 随增补批两站同批发版）。上一批 〇-R 记录：〇-R 后台去写死中文全量收口 + 全站 logo 统一/favicon + 个人中心默认落页 + 发码 403 三连修复**——本地代码提交 `670a842` → 纯代码推送 `a882977`（57 文件，零 .md、零 UI 交付包）·文档仅本地；门禁 tsc 0、vitest 52 文件全绿、vite build 成功；（当时部署未做，已随 09-24 23:28 两站同批发版上线）。上一批 〇-Q 记录见下文））**
> （历史记录：2026-09-24 〇-Q 文件直出区通栏固定舞台重排 ＋ 文档注释棘轮门禁**——文件直出演示弃双栏改「居中标题容器 + 限宽 880 固定舞台」，hero 第二卖点带迁入动效上方（首屏删除），演出只动元素自身零布局位移（chip 定宽、链路弹性伸缩、sink 只压亮度），源 chip 去飞入吸附改左链路数据包流入，下载改纯 icon 绝对定位长在译文 chip 内，顶部独立进度条与底部步骤文字行删除，窄屏链路补 `min-width:0` 修「粗带」（根因＝基础 `min-width:48px` 未被媒体查询覆盖）；App PageLoading 去「加载中」文字。新增 `tools/check_doc_comments.py` 导出面中文文档注释棘轮门禁（Go 249 文件实测 0 缺口、FE 补 7 处后双 0 基线入库 `.doc_comments_baseline`，selftest 含剥离探针负向自证）。Landing.dom.test 新增 ⑩-⑮ 六条回归锁。门禁：tsc 0、vitest **49 文件 384 用例全绿**、`go vet`/`go build`/gofmt 0。本地代码提交 **`c52c0f7`+`3e5ebec`** → 纯代码推送 **`dc419b8`**（9 文件，零 .md、零 UI 交付包）·文档仅本地。**部署见 〇-Q 表 ⑦**）
> ★★ **〇-P、回退到 UI 交付稿批（2026-09-23，现行口径）**：用户看过 〇-O 实测后判「太丑」，后令「严格按 UI 交付稿来」。
> 〇-O 整体撤销：**描边七档回到交付灰阶**（#8B939F → #2A2F3A，`#FFFFFF` 不再作框线色）、**面色台阶回交付值**
> （L2 `--lc-panel` **#0E1014** / L3 `--lc-raised` **#16181C**，〇-O 的 +8 档 #121417/#1A1D21 作废）、
> **描边粗细回交付档**（全边框 **1.2px** / 单边分隔线 **1px**，〇-N 的「一律 2px」作废），
> **字号保留 〇-N 的 +2px 不动**。五类渲染面逐面同步、闸门两侧全部翻转（`readability.test.ts` A/F/H/I、
> `public_ui_test.go` `retiredLegacy00O`、`admin_ui_test.go`、`pixel_uat.spec.ts` P2d/P6c）；扩展重打包 **1.2.2**。
> ⚠️ **下面的 〇-O 条目是历史记录，不再是现行口径**——读它请按「已撤销」理解。

### 〇-U、发布前E2E_UAT_20260926 第二轮客户级 UAT 修复批（2026-09-26 → 09-27 跨零点，★ 本地代码提交 `ba9ba71`（115 文件，+3594/−711，零 `.md`、零 UI 交付包）→ 纯代码推送 `c4fe807`＋并轨 `551b2c1`·文档仅本地）

背景：对 〇-T 发版后的两站做第二轮字节级客户 UAT（78 例全跑，现场与账目见《发布前E2E_UAT_20260926/》），产出缺陷 F-44～F-71 与观察项 O-1～O-16；每条先读真实代码核实（两处翻案、若干降级），再按批 I-1～I-10 全量施工。修复与核实的逐条处方见该目录《缺陷核实与修复文档_20260926.md》（§15 为执行账）。

1. **F-64 状态码诚实三档（本轮主账）**：①收款/账务七文件 71 处「200 承载失败」迁 `s.writeError(apierrors.New(code,msg))`（棘轮基线 699→**628**）；②管理台与业务动作 182 处改诚实状态码，前端配套在 `core.ts` 新增 `bizResp` 收敛器——把后端结构化 4xx 失败体**还原成历史 `{success:false,...}` 形态**（details 摊平、401/403 照抛触发重登录），调用方零改动；③尾量 27 文件 122 处「未登录也回 403」的鉴权内联错误迁 `s.writeAuthzError`（errNotLogin→401、等级不足→403，文案逐字不变）。前端接口层 17 个文件（apikeys/auth/billing/feedback/flow/industry/invites/models/org/persona/scrape/system/tasks/tenant/tickets/tmreview/webhooks）全部 bizResp 接线并带 F-64② 中文注释；必须留 200 的白名单口逐处显式登记。
2. **读侧与审计闸门族**：F-44（P0）PG 方言读侧＋编辑器吞错修复，机制闸 `f44_readfail_gate_test.go`（DROP 表后 500 诚实＋摘闸反证）；F-63 审计小票统一口径 `audit_detail.go`（订单号｜金额分｜渠道｜尾注），`order_pay`/`order_refund` 留实单实额并加「权益已回收」固定串**负向锁**；F-45④/F-57 反馈详情与 `"null"` 写侧收口；O-1 `pay_mode` 切 mock 时审计带 ⚠ 标记＋前端 `PlansP` 二次确认弹窗（新键 `packages.confirmMockPayMode` 十二语种同步＋dom 锁四条，含摘守卫反证）。
3. **USDT/交易视图与鉴权常设锁**：F-67 声明-确认链三段锁进 `api_uat_txn.sh`（T42-f67-declared/confirmed-view 钉订单视图如实回显 manual_confirm/channel）；新增 **T63** 段按三类守卫各取一代表（超管口/租管口/部门口）钉 401/403/200 分流形态，谁把 `writeAuthzError` 换回内联 403 即先红；F-70 轮换文档补「当日调用计数归零」口径（中英内嵌文档＋12 语种 `apikeys.confirmRotate` 同步，注意撇号引号曾会炸 TS 已规避）。
4. **SDK 与交付链**：四语言 SDK 偏差账收口（python/typescript 修复＋js/java 口径核对），`public/sdk/` 重打（python 1.0.2 / ts 1.0.2，`build_sdk.sh --check` 含产物内实比对绿）；托管 zip 扩展 1.2.2 未动（零改动＋漂移 ✅）。
5. **09-27 复跑三红归因（全部测试自伤，非产品回退；「测试与实现同时错」两例）**：①T 套件 8 红——T45 改密/T47 令牌轮换早把脚本顶部 `$H1` 打失效，旧一刀切 403 把过期令牌伪装成「权限不足」所以一直绿，F-64③ 分流后诚实翻 401；修法按脚本既有惯例在 T52 段前**全局刷新一次 H1**（引号闸门 2993 行 0 处复验）。②移动端余额徽标红——O-9 守卫（超管未切租户＝平台上下文，隐藏假「余额 0 积分」胶囊）与本用例子冲突：用例原以超管账号测徽标可见性，改租户口径 `uatuser_a`，产品行为不动；另把徽标等待窗 15s→30s 对齐冷启动载入闸门预算（仍要求真渲染真可见，语义不变）。③P2d「后台带框件 ≤3」临界红——首跑撞「外壳文案到位≠面板件挂载」的异步窗（retry 即过），改**轮询到件数稳定再量**＋失败信息自带实扫类名清单（自诊断不改红绿语义）。教训：**亚像素/时序类运行时锁改轮询前必须先复现现场；测试账号的计费上下文要和断言语义一致**。
6. **闸门终局数字（推送前全绿，PG 方言算数）**：`go build`/`go vet` 0、`go test -race ./...` **42 包全 ok**；`run_uat.sh`（PG）A/B **130/0** ＋ T **643/0** ＋ 前端 E2E **exit=0**（71 条用例含 retry 甄别）；assist **48/0**；多实例 **8/0**；`tsc` 0、vitest **76 文件 567 用例全绿**（初跑 5 红＝负载型假红：三条文件单跑 34/34＋整套原样复跑全绿，未放宽任何断言——口径同 〇-T 第 4 条）、`vite build` 0；扩展/SDK 漂移 ✅；注释审计 88 个在途代码文件收口（8 个小语种 locale 命中属译文值误报，无代码逻辑）。
7. **测试数据清理**：KEEP 留场实例四进程按 pid＋二进制属主核验后全杀（外来 Python mock 先验证命令行再杀）、`translator_uat` PG 库删除、WORK 临时目录与 /tmp 调试件（hashgen/迁移脚本/草稿块）全清；生产五项处置此前已按逐字批准执行并复核（见 〇-T 后 UAT 账），本轮未新增生产写。
8. **发版（第 5 步，两站同批，09-27 02:49 已执行）**：本轮改动含 `admin_openapi.go`/`public.go` 内嵌直出面与后端二进制逻辑，**必须换 `translator-server`**；前端 dist（含 `/sdk/` 1.0.2 新产物）两站换源；assist 与扩展未动不换。发版执行账与接线验收见《部署指南.md》〇-U 发版记录。
9. **发版执行账（09-27 02:49–02:57，两站同批）**：`translator-server` sha256 `4719aa89…` 两站同值（TS=20260927_024949，`cp -a` 备份→`mv` rename 替换）；两站 `/opt/*/web` 走「web.new→校验首页 `index-brebiSnS.js`→两步 `mv`」，演示站补 `chown root:caddy`+`o+rX`；演示站 `pay_mode` mock→**static_qr**（O-1 红线，主站本就 static_qr）。接线验收全绿：内网 `deploy_check.sh` **11/11×2**、公网 **8/8×2**、`/livez`·`/readyz` 两站 ready、journal `-p err` 零条、`/sdk` 五探×两站魔数全命中（主站 tgz 首跑 000 属本机→CF 链路抖动，复跑 200）、扩展 zip 11890 B/`PK`/指纹 `4ebed6a3233a…`＝登记值、未登录 ops 路由回 **401**（★ F-64③ 新口径：未登录=401、越权=403，旧「期望 403」清单已在部署指南同步改口径）。⚠️ **发版事故并当场修复**：批 H 运营动作 root 直建的操作备份表 `system_config_bak_20260926`（主站）与 `system_config_bak_20260926`+`tenants_brand_bak_20260926`（演示站）**属主留在 postgres**，应用角色 pg_dump 在 LOCK TABLE 处 permission denied ⇒ **两库自动备份自批 H 起产出 0 字节 dump**（主站 09-26 01:45 起、演示站 09-27 00:51 起），且 0 字节文件还占 `backup_keep` 名额挤占真备份。修复＝`ALTER TABLE … OWNER TO langcross` 三张＋清 0 字节 dump＋重启触发启动备份（复核主站 56,323,007 B／演示站 57,847,808 B，日志「数据库已备份」）。**运维红线：操作备份表必须随建随 `OWNER TO langcross`，发版后抽验新 dump 字节数 >0**。

### 〇-T、发布前客户级 E2E UAT 缺陷全量修复收口＋自动化测试固化批（2026-09-25 → 09-26 跨零点，★ 本地代码提交 `5e914c9`（122 文件，+7052/−549，零 `.md`、零 UI 交付包）→ 纯代码推送 `d892de3`＋并轨 `4a1e20b`·文档仅本地）

背景：发布前客户级 E2E 全量落账（F-01~F-41 级缺陷清单＋字节级用例集，见《发布前E2E_UAT_20260925/》四份文档），修复分**批 A–G**（批 G 又分波 A 六组互不冲突并行施工＋波 B i18n 手术序列）全部完成并独立复验；**批 H 为遗单观察账**（id55 核销、90 号订正 SQL、租户 3 幽灵 carry、TM 候选 1005-1007、F-30 XFF 取证、F-19 USDT 开放、F-05 DKIM、F-21 披露文案、F-38 供应商取证、Login.tsx auth-ok 观察项——均待用户决策，未擅自动手）。

1. **产品面三个新口径收口**：
   - **F-41 工单预估口径翻转（宁高勿低，用户拍板）**：`estimateTicketTokens = 源字符 × 语种数 × K(mode)`，`K(pro)=160 / K(fast)=60`，走 `system_config` 键 `est_tokens_per_char_pro/fast`（缺失/非法/≤0 一律回退保守默认）；文件源字符按扩展名分档折算 `estimateFileSourceChars`（纯文本 /3、未知容器 /6、pdf·doc·ppt·xls 等 /12）。旧口径 `chars/1.3×langs×markup` 比 pro 实测计费低估约 62 倍（工单 88 估 17.3k token vs 实烧 ≥107 万），根因是 pro＝初译+评审双趟×分块×逐次上下文开销，「源字符→单趟 token」结构上跟不上。拒绝文案继续「预估积分」范式（零 token 裸值）。⚠️ 后果之一：**716800 B 的 PDF 在体验余额（1100 分）下现在会被拦**（59,733 字符 ×60 ÷300 ≈ 11,947 分 > 余额）——这正是用户批准的效果，旧 T40「700KB 必须放行」断言已翻转重钉（见第 3 条）。**留观动作（批 H）：K 是「建单门槛」敏感项，上线首周按 `usage_ledger` 实测 P99 回调。**
   - **F-29 单次对话字符护栏（后端半＋前端半）**：运营策略键 `chat_max_chars`（默认 5,000，非法/缺失回退默认），流式入口在调模型前拒超长，杜绝「大载荷被上游 ~90s 掐成 524」；前端半＝`useChat.tsx` 对含 `<!doctype` 的错误响应体兜底为友好文案（引导改用翻译工单），**禁止响应体原文塞进气泡**。断言：单测 `stream_f29_test.go`＋UAT `A7t` 三条（超限拒码/拒文案/限内放行）＋`useChat` dom 兜底锁。
   - **F-27 官方 SDK 本站托管交付通道**：`scripts/build_sdk.sh` 把四件套（python whl+sdist、npm tgz、各自 latest 别名与 `.sha256`）连 `manifest.json` 一起产出到 `frontend-react/public/sdk/`，随前端 dist 换源上线（`/sdk/*` 走 `spa.go` 静态直出，**不需要换后端二进制**）；SDK 页从「假安装命令」收口为「本站托管下载」。闸门：`build_sdk.sh --check`（漂移红灯，已入 AGENTS §二）＋ `e2e/sdk_download.spec.ts`（200 + 非 HTML 兜底 + 魔数 whl=`PK`/tgz·sdist=gzip `1f 8b` + 体积下限 + manifest↔`pyproject.toml`/`package.json` 版本交叉锁，版本号禁写死）。**文档批同步（09-26）**：12 语种产品手册 §10.3 的假安装命令（`pip install langcross-translator`/`npm i @langcross/translator-sdk`，注册表 404 实锤见证据 R22）全部改为「本站 `/sdk/` 托管下载后本地安装」口径；`产品手册/pdf/` 12 份 PDF 用同源 Chromium 打印管道重出（逐语种 pdftotext 核验：旧命令 0 命中、新口径 2 命中、页数 14–18 与交付版持平），铺库动作即用新版。
2. **自动化测试固化（把今天的开发内容加进测试程序）**：`e2e/register_captcha_token.spec.ts`（F-06/F-07 企业注册双死洞收口——AI 流管理员分支补组织编码 + 退回传统表单重挂 Turnstile）；`e2e-manual/real_llm_smoke.spec.ts` 新增 SMOKE-5（F-15 货币误译活体半：「售价 300 元」⇒ 必须含 300 且带 ¥/CNY/元 系标记，负向锁 R$/€/$ USD/ZAR；`REAL_LLM_BASE/KEY` 缺失整文件 skip）；`api_uat.sh` 补 A7t；`api_uat_txn.sh` T40 按 F-41 重钉 5 条（含「文案预估积分 == 按当前 K 现算」等值锁与「补足额度即放行」反向锁——反向锁防的是「闸门焊死谁都不让过」这类修复过头）。⚠️ **与修复文档处方的偏离**：F-03 处方原写「UAT 脚本断连点两次前 8 位不同」，真 Cloudflare 无法在 shell 里实测，改以 Playwright `register_captcha_token.spec.ts` 承担，已回写修复文档。
3. **闸门健壮性三修**（本轮抓出的三处「闸门自己坏」而非产品回归）：
   - `multi_instance_e2e.sh`：①就绪预算 20s→45s——冷启新库迁移单飞锁串行实测 A 实例 25s 才 listen，旧预算会让 M1–M8 一条都没跑就整段红（只放宽等待、**不放宽断言**；失败信息回报实际等待秒数）；②补钉 `USER_DATA_DIR=$WORK/udata`——此前没钉，双实例把 TM 备份与 memleak 堆快照写进**本机真实应用数据目录**（`~/Library/Application Support/能言`），本轮发现并清痕。
   - **假链端口抢占（T42 五条假红根因）**：`/private/tmp/lcrec` 的外来 scratch 进程占着 mock-chain 端口 8902，就绪探针把外人的应答当绿 ⇒ 后端轮账打到语言模型模拟器、`usdt_deposits` 永不相入账 ⇒ 五条连锁红。排障口径：**先 `lsof -i:<端口>` 看应答方是谁**；不杀外来进程，用 `MOCK_CHAIN_PORT=8912 bash run_uat.sh` 整体让位（前后端同源同一变量，断言侧 `CHAIN=${MOCK_CHAIN_URL}` 天然一致）。
   - `dblib.sh`：dbcfg 的 SQLite 分支补 `.timeout 5000` 且写失败即 FATAL 退出（此前静默失败，放宽配置没落库、后面整段矩阵在旧配置上跑）。
4. **闸门终局数字（推送前全绿）**：`run_uat.sh`（PG）A **95/0** · B **113/0** · T **515/0** · 前端 E2E **70 passed/0 failed**（失败自动 `--last-failed` 复跑甄别 flaky，exit=0）；`go test -race ./internal/...` 40 包全 ok；`assist_uat` **48/0**；`multi_instance` **8/0**；`build_extension.sh --check` ✅（1.2.2，指纹 `4ebed6a3233a…`）；`build_sdk.sh --check` ✅（python 1.0.0 / ts 1.0.1）；`tsc --noEmit` 0、vitest **65 文件 480 用例全绿**（初跑两条为并行 worker 负载型假红，单跑+整套复跑双绿，未放宽任何断言）、`vite build` 0；注释闸门 `check_doc_comments.py` go 0 / fe 0。
5. **测试数据清理**：本地开发库先备份再删除本轮 UAT 账号/工单/测试租户及关联行；**超管测试号 `uat_super_01` 按用户令保留不清理**；/tmp 草稿产物、临时二进制、调试实例全清；`~/Library/Application Support/能言` 下今天泄漏写入的备份/堆快照已删（09-24 及更早旧件待用户确认后再处置）。跟踪文件 `data/tm_embeddings.npz` 曾被外来临时进程改写为 5 行合成数据，两次 `git checkout --` 还原为提交版（13,457,516 B，sha256 `91c699a4…`），运维注意：本机别的项目不要指向本仓数据文件。
6. **生产联动动作（随本批部署执行/留观）**：`/sdk/manifest.json` 与 4 产物可达性验证（§十 判据口径）、12 语种产品手册 PDF 铺库＋SetConfig、`price_fen` 29900→33222 上柜、K 值 P99 回调留观、`usdt_*` 配置态保持。
7. **发版实录（09-26 00:51，两站同批）**：`translator-server` sha256 `1fbd8793…` 两站同值（`cp -a` 时间戳备份 → `mv` rename → `systemctl restart`），`translator-assist` 与扩展未动（零改动＋漂移闸门绿）；前端 dist（含 `/sdk/` 首发 13 文件）两站两步换源，演示站补 `chown root:caddy`+`o+rX`，两站首页现引用 `index-BOIA4YDV.js`。批 H 已执行三项：**12 语种手册 PDF 铺 `/opt/translator/data/manual/` 并两库 SetConfig `manual_pdf_dir`（旧 6,522B 单文件挪档）、两库尺子键 29900→33222（改前两库各建 `system_config_bak_20260926` 备份表）、`/sdk/` 随 dist 上线**；`usdt_*` 未动。验收（只接线）：deploy_check 内网主/演 **11/11 ×2**、公网 **8/8 ×2**，扩展包 11890B/`PK`/指纹对平，/sdk 五探 200+魔数全过（无 SPA 兜底），403 对、`anydoc_ready:true`、livez/readyz 200、日志零 err/panic；治理裁档后磁盘 38%／余 24 G。**仍挂账（待用户确认，未擅动）**：id55 核销、90 号订正 SQL、租户 3 carry、TM 候选 1005–1007、订单 5/7 与测试账号处置范围、F-30 XFF 取证；K 值一周后按 `usage_ledger` P99 回调。

### 〇-S 增补、语言识别收口 "error" 字段类 + A3lang 语种 UAT + 像素锁 〇-P 重钉（2026-09-24，★ 本地代码提交 `c72a2f3` → 纯代码推送 `977f9a2`（8 文件，零 `.md`、零 UI 交付包）·文档仅本地）

用户下令五步收尾链（查完成度→测试+清数据→注释+commit/push→更新文档→部署）中的第 1/2 步补完：

1. **`"error"` 字段类收口（完成度核查抓出的第 3 类盘点遗漏）**：`metrics.go`/`spa.go`/`stream.go` 内联 `writeJSON` 的 `"error": "中文"` 不经 `apierrors`，首轮两类棘轮扫不到——404 「接口不存在」在英文会话原样直出。补录 11 词条（catalog →**386**），`lang_middleware.go` 改写正则放宽为 `"(?:message|error)"`，棘轮扩第三类字面量口径（`TestAPICnMessageLiteralsCovered` 三正则并扫），新增 `TestWithLangErrorFieldTranslates`（404 英文化 + 非汉语值 `invalid_api_key` 反证不动）。
2. **A3lang 语种 UAT 入矩阵**：`scripts/uat/api_uat.sh` 新增 6 条等值探测（zh 默认/en/ja→en/zh_hant 保持中文/error 字段英中各一），走 `/api/lead` 校验失败与 404 探针**刻意避开登录限流预算**（loginFailThreshold 5 次/300s，撞闸会级联拖红全套件）。本轮 `run_uat.sh`（PG）实测 6/6 绿。
3. **像素锁 〇-P 重钉（run_uat 全量复跑翻红揪出的两把旧锁，零产品代码改动）**：① P2b 几何锁还停在 〇-N「描边 2px」档（50/94/114），描边回落 1.2px 后运行时真值即 〇-M 原档 **48/90/109**（mt-seg 34=项 28+内垫 4+取整描边 2）；② P2d「对话框边宽 = 1.2px」在 Chrome 151 下**物理不可能满足**——CSSOM 把亚像素描边按设备像素取整上报，实测 dpr=1/2.5 均回 `1px`（一次性探针量过），改锁「取整等值档 1px」并把 1.2px 字面等值责任写明归源码级 `readability.test.ts` A/I 段，运行时锁射程=抓 2px/0px 覆写。AGENTS.md 〇-N/〇-P 两段已同步（新教训入档：**写运行时亚像素等值锁前必须在钉死的浏览器版本里实测**）。
4. **注释与测试数据**：`core.ts` 补 `setApiMsgCopier` docblock（`check_doc_comments.py` go/fe 双 0 基线恢复）；本地开发库备份（`backups/tm-pre-testclean-20260924.sqlite3`）后删除测试账号 `dbguser`/`ui00s` 及两租户与全部关联行（仅剩 admin/默认租户），/tmp 草稿产物与临时二进制全清，8787 调试实例已停。
5. **扩展语种现状（声明，不属缺陷）**：MV3 划译扩展界面为写死中文且无语种设置，走 openapi 无头默认 zh——留待扩展引入语种设置时再接 X-App-Lang。

**部署收尾（2026-09-24 23:28 已执行）**：三批（〇-R＋〇-S＋本增补）合并发版，两站同批——`translator-server` sha256 `5ebd32c8…` 两站同 sha（mv rename 替换＋时间戳备份）、前端 dist `index-Cx_OOgba.js` 两站两步换源、assist 不换／扩展不重打包；验收只确认接线：主站内网 11/11、两站公网各 8/8、`journalctl -p err` 零条、两站 `X-App-Lang: en` 探针实测英文（详见《部署指南》2026-09-24 23:28 发版记录）。

### 〇-S、前台整合（套餐/余额/账号并入右上下拉+页脚回底+去汉堡）＋ 后端语言识别（提示语按界面语言返回）（2026-09-24，★ 本地代码提交 `e4b263a` → 纯代码推送 `a19e2ec`（17 文件，零 `.md`、零 UI 交付包）·文档仅本地）

用户在 〇-R 待决策清单上下令两事（#7/#11 前台整合、#12 后端语言识别）：
① **#11 前台整合**：左侧汉堡抽屉删除（用户判「右侧的不是汉堡，是下拉」），套餐/余额/邀请/账号四项并入右上角 AccountMenu 下拉（新增 `selfNav`/`showInvites` 入参，个人用户才见「我的邀请」，与 /invites 守卫同口径）；`SiteFooter` 回归页脚位置（参考元宝：页面底固定），壳层改 `100dvh` flex 列 + `app-main` 自滚动，ChatWindow 高度弃 `calc(100vh-39px)` 改 `flex:1`。浏览器实测（zh+en 双语种）：无汉堡、下拉九项齐全、footerTop=视口底、无双滚动条、/billing 跳转正常。断言：`AccountMenu.selfnav.dom.test.tsx`（3 用例）+ `FrontShell.layout.lock.test.ts`（4 条源码锁：无 `Icon n="menu"`/无 Drawer/menuOpen 残留、SiteFooter 位置、布局尺寸口径）。顺带修 `WordSwap` jsdom 卸载后 `window.setTimeout` 抛未处理拒绝（改 plain setTimeout + play().catch）。
② **#12 后端语言识别**：新增 `internal/i18n` 包——`FromRequest` 判语种（X-App-Lang > Accept-Language > 无头默认 zh；zh 系含繁体一律归 zh〔本批明示取舍〕、其余语种→en 与前端回退链同口径）；`Msg()` 三级匹配（精确 379 词条 → ≥5 rune 最长前缀〔覆盖「保存失败: 」+err 拼接〕→ 10 条 %d/%s/%v 模式句式 → 原样透传）。挂点收口在 HTTP 边界：`lang_middleware.go` 仅对英文请求包 `langWriter`，缓冲 JSON 响应后**字节级**改写 `"message":"..."` 值——992 处调用点零改动、字段顺序不破坏；SSE/静态/下载等非 JSON Content-Type 直通、超 1MB 冲刷切直通、handler 主动 Flush 即切直通；**中文请求（含全部 curl/UAT）零包装、字节级与改造前一致**。CORS 放行 X-App-Lang；前端 `authHeaders()` 统一附带（core.ts 直读 localStorage.app_lang 不 import i18n，SSE 通道 translate.ts 复用 authHeaders 同源生效）。
⚠️ **盘点补漏**（真机联调抓出）：首轮只扫 `"message": "..."` 字面量，漏掉 `apierrors.New(code, "中文")` 整类——43 唯一串缺 36 条（用户名或密码错误/未登录或登录已失效/SSO 系列/支付渠道系列等），已全部补录；防复发棘轮 `TestAPICnMessageLiteralsCovered`：internal/api 非测试源码两类写死中文不进词条表即红灯（命中数 <300 判正则口径退化）。词条表由 `scripts/gen_i18n_catalog.py --apply` 生成（改完自动过 gofmt），`TestCatalogCoverage` 钉规模 ≥375。
**门禁**：后端 `go build`/`go vet` 0、`go test -race ./...` 全量绿（首跑仅 gofmt 闸门红〔新文件未格式化，gofmt -w 后复跑绿〕+ 触及三包 `-race` 复验：internal 5.3s / api 213.6s / i18n 1.3s 全 ok）；前端 tsc 0、vitest **54 文件 405 用例全绿**、vite build 2.63s 成功。真机双语闭环：`/api/auth/login` 错误凭证在 X-App-Lang:en 下返回 “Incorrect username or password”、ja→en、zh_hant→zh、Accept-Language:fr→en、无头→zh（页面实测英文/中文提示各随其语种）。
**部署待办**：本批含后端——上线需换 `translator-server` 二进制（internal/api 直出页与全部 JSON 接口经新中间件）；前端换 `frontend-react/dist` web 源（连同 〇-R 未部署部分一起生效）。assist-server 与扩展本批未接入 X-App-Lang（扩展/划译插件走 openapi 无头默认 zh，留待后续批次）。

### 〇-R、后台去写死中文全量收口 + 全站 logo 统一/favicon + 个人中心默认落页 + 发码 403 三连修复（2026-09-24，★ 本地代码提交 `670a842` → 纯代码推送 `a882977`（57 文件，零 `.md`、零 UI 交付包）·文档仅本地）

用户连报四事，本批全部收口（纯前端，无 Go 改动）：

1. **发码 403 三连（#6，前段完成本批入库）**：新增 `src/lib/turnstile.ts` 人机验证挂件桥；AiRegisterFlow 补挂载与失败提示；403 误改文案回退。dom 断言锁随批入库。
2. **后台去写死中文（#9）**：KbP 授权弹窗/OrgP 移动/BrandP 占位与超限提示/OpsP 覆写因子与路由统计与 SLO/LangMultiSelect 语种名/内置人格（`personaName` code→英名映射）全部走 12 语种词典；**api 层中文报错**经新增 `apiMsg` 钩子总线本地化（core.ts 禁静态 import i18n 的口径不破，ToastBridge 注册取词器，未注册回落中文兜底句）；新闸门 `src/i18n/hardcodedCjkGate.test.ts` 扫 components/admin + api 两目录的字符串字面量 CJK，零写死中文（SdkP 代码示例豁免、apiMsg 兜底行豁免）。dicts +25 键、panels ops+33/org+3/kb/brand/tasks/auth，10 份 locale 全量补译（占位符逐字一致）。
   ⚠️ **遗留决策项（后端）**：后端无 Accept-Language 机制，`r.message` 类中文提示（如越权/参数错误的后端原文）仍会直出到前端 toast——要不要后端按请求语言回文案，待用户定。
3. **个人中心默认落页（#10）**：进入即打开第一个子页（个人=邀请好友、企业/超管=任务中心），`PersonalCenterP.dom.test.tsx` 锁默认落页与权限收口；本仓 vitest 未开 globals，RTL 不自动 cleanup，跨用例 DOM 残留曾致假红，已在测试内 `afterEach(cleanup)`。
4. **全站 logo 统一 + favicon（#8）**：`BrandDotIcon`（Icon n="brand"）重写为首页顶栏「白 30×30 圆角块 + 一实一虚两笔黑画」同源图形；App 顶栏、后台侧栏默认标记、PricingPage BrandMark（旧圆环圆点退役）三处统一；新增 `public/logo.svg` + index.html `<link rel="icon">`；`branding.tsx` 解析品牌后 favicon 随 `brandLogo` 切换（**有独立品牌 Logo 的租户除外**，其仍用自家 Logo 与顶栏 img）。`src/logoConsistency.test.ts` 以两条笔画 path 逐字锁五处同源。
5. ⚠️ 〇-P 口径插曲：Dialog/uiDialogs 默认按钮曾误切 `common.confirm`（确认），实为交付原文「确定」用词回归——新增 `common.ok`（确定/OK）12 语种全量落键并回落，TaskCenterP.dom 红灯即此因。

门禁：tsc 0、vitest **52 文件全绿**、vite build 成功。**部署未做**（纯前端批次，待用户下令换 web 源；logo/favicon 属前端 dist 即换即生效，不动二进制）。

### 〇-Q、文件直出区通栏固定舞台重排 + 文档注释棘轮门禁批（2026-09-24，★ 本地代码提交 `c52c0f7`+`3e5ebec` → 纯代码推送 `dc419b8`（9 文件，零 `.md`、零 UI 交付包）·文档仅本地）

> 来源＝用户 2026-09-23~24 连续六轮反馈（「容器丑重新做」→「吃进去保留」→「四步与动效联动+下载进容器」→「只要 icon+进度条要么联动要么删」→「离太远/粗线是什么鬼/文字不要了」→「整合第一屏容器做高级放动效上方」）。本轮全部改动集中在 `frontend-react/src`（`Landing.tsx`/`App.tsx`），后端零改动。

| 块 | 交付 | 锁/边界 |
|----|------|---------|
| **① 文件直出演示终态构图（FileDirectDemo）** | 居中标题容器 `.lc-fd-head`（`heroPoint2` 主标题 + `heroPoint2Note` 副题，clamp 26-40px）在上；舞台 `.lc-fd-stage` 在下：源 chip — 链路(pl) — 「能言/LangCross」引擎环 — 链路(pr) — 译文 chip，自循环四阶段：源 chip 原地浮现→数据包流入引擎→引擎点火（虚线环旋转+双脉冲+品牌脉动）→译文 chip 物质化+译名打字机→sink 蓄力→释放（引擎激发+12 道光线以引擎为圆心放射+译文 chip 燃亮+光泽+角标对勾）→下载 icon 在译文 chip 右缘展开 | 演出**只动元素自身**（opacity/transform）：chip 定宽 `clamp(196px,20vw,232px)`、链路 `flex:1 1 0` 弹性伸缩（两侧等宽⇒引擎恒居中）、sink 只压亮度**不缩放舞台**——任何阶段零布局位移（历史教训：双栏 grid 曾被演出内容挤动文字/容器）；下载区 `.lc-fd-dlzone` 绝对定位不占布局流 |
| **② 本轮退役清单（负向锁进测试 ⑭）** | `.lc-fd-barfill` 顶部独立进度条（用户令「不要孤零零的动效」）、底部 `.lc-fd-steps` 步进文字行+`.fd-seg` 轨道（用户令「下面的文字不要了」）、`.lc-fd-src.fly` 飞入吸附引擎、`.lc-hero-point2` 首屏卖点带（迁入 fd 区避免两处重复）、`.lc-fd-in`/`.lc-fd-copy` 双栏布局、下载按钮「下载」文字（只留 icon，`title` 供可读名） | `Landing.dom.test.tsx` 新增 **⑩-⑮ 六条回归锁**（标题容器在位/卖点带迁移/双栏零残留/五元素在位/旧附件零残留/icon 无文字）——这些元素按用户明令退役，**不得复活** |
| **③ 窄屏粗带根因（★ 通用教训）** | 现象：≤640px 链路渲染成 48px 宽深色带。根因＝基础规则 `flex:1 1 0;min-width:48px`，媒体查询只覆盖了 `width:1.5px`——**`min-width` 未被覆盖仍生效**，纵向 flex 再把高度拉满 | 修法：媒体查询补 `min-width:0`。教训：**覆盖 flex 项尺寸时 `min-*` 与 `max-*` 必须逐一核对**，只写 `width` 不算覆盖完 |
| **④ 文档注释棘轮门禁（tools/check_doc_comments.py）** | 口径：Go 导出（func/type 首字母大写）紧邻上一行必须 `//`；FE `export function/const/interface/type/class` 紧邻上一行必须 `//`、`/*`、`*`。跳过 `_test.go`/`*.d.ts`/`vendor`/`dist` 等。棘轮基线 `.doc_comments_baseline`（`go=0`/`fe=0`）入库，只降不升；`--selftest` 常开（探针注入必须命中＋有注释必须放行） | 首跑实测：Go 249 文件 **0 缺口**（本仓惯例本就全注释，反向剥离探针自证扫描器有效）；FE 补 7 处（kb.ts CHUNK_SIZE、i18n/script.ts ×4、Mobile.tsx MobScreenProps、utm.ts UtmSnapshot）后 **双 0**。新增导出不写中文注释会直接红灯 |
| **⑤ App PageLoading 去文字** | 加载占位只剩换词动效（WordSwap），「加载中」文字删除——动效本身就是加载语义，文字仅保留为 ariaLabel 供读屏 | 无障碍语义不丢 |
| **⑥ 闸门（2026-09-24 全绿）** | `tsc --noEmit` 0；vitest **49 文件 384 用例全绿**（含新 ⑩-⑮）；`gofmt -l` 空、`go vet ./...` 0、`go build ./...` OK（后端本轮零改动，例行跑）；`check_doc_comments.py` go=0/fe=0；仅增注释行自证（4 文件 diff 全为 `+//` 行） | 提交走 `push_code_only.sh`（干跑 9 文件清单核对 → --apply），零 .md、零 UI 交付包 |
| **⑦ 部署（2026-09-24 16:02 两站同批，已回填《部署指南》顶部）** | 用户令「阅读部署文档，前后端都部署到云服务器」⇒ `translator-server` sha256 `60ecfd88…` **两站同 sha**（`mv` rename 替换，`translator-assist` 不换——零 `internal/assist` 改动）；两站 web 两步换源，新资产 `index-AUDApay_.js`（含 `Landing-DRQF3mZH.js`）。验收：内网两站 **11/11**、公网主站/演示站（**rox-test.lexicorn.cn**，非 demo.*）**各 8/8**；journal `-p err` 零条、panic 0；扩展包 200/11890B/PK；管理路由 403×2；`PDF_LIB_OK`/`ANYDOC_OK`；演示站公网首页引用 `index-AUDApay_.js`。web_old 各裁 4 份、磁盘 38% 余 24G | 后端零代码改动仍按用户令重发两站（先例＝09-23 17:37「三件齐上」批） |

### 〇-O、全部框线纯白 + 面色三级台阶批（2026-09-23，★ 本地代码提交 `50db33e` → 纯代码推送 `d5387ea`（34 文件 +386/−169，零 `.md`、零 UI 交付包）·文档仅本地·**两站已 17:37 同批发版（见 ⑦）**

> 来源＝用户连续三令（均为单向命令，按字面执行）：「**框线全部纯白、背景主色黑，分层可以带一点深灰的层次感**」→「**所有的按钮框和选项框也要纯白**」→「**三级台阶抬亮可以，页面底色不要变亮，分层效果用背景主色（纯黑）＋台阶深灰＋白色框线来做**」。
> 中途 AskUserQuestion 问过「四级（含页面底 #050607）还是三级（页面底保持 #000）」，用户选了**页面底不变亮**这一档。
> ⚠️ 用户看完 1440×900 实测截图后判「**框线太粗了**」，随即撤回「**算了，你前端别改了，就这样吧**」⇒ **2px 粗细是现行口径**，1.2px 是已作废的交付原档；想改粗细须重新下令，不得拿交付稿自作主张。

| 块 | 交付 |
|----|------|
| **① 令牌层翻白与面档抬台阶** | 承载色值的 CSS 只有两件：`src/ui/langcross/css/tokens.css`（描边七档 `--lc-border-strong/done/input/faint/pill/card/card-dim` 全 → `#FFFFFF`；面档 L2 `--lc-panel` 0E1014→**121417**、L3 `--lc-raised` 16181C→**1A1D21**，页面底 `--lc-bg` 仍 `#000`）与 `src/styles/theme.css`（后台/门户别名 `--npz-line`/`--adm-line` → `#FFFFFF`，`--npz-surface`/`--adm-card` 取 L2、`--npz-surface-2`/`--adm-card-hi` 取 L3）。**七个描边名与 `--lc-border-1…7` 别名一律保留**，几百个调用点零改动即整体翻白。另 **17 个 React 文件**（`App.tsx`/`branding.tsx`/`ChatWindow`/`LangMultiSelect`/`Landing`/`Login`/`PricingPage`/`TicketsPage`/`AiAssist`/`AiRegisterFlow`/`EditorPage`/`ErrorBoundary`/`SiteFooter`/`BrandP`/`OrgP`/`SdkP`/`Skeleton`）内联 `<style>` 与 style 对象里硬写的旧灰阶/旧面档字面量逐处换令牌或换白——「改了令牌页面没变」的历史漏点就在这里 | 旧灰阶 `#8B939F`/`#6E7683`/`#5A6270`/`#464C58`/`#424956`/`#3A404C`/`#2A2F3A` 与旧面档 `#0E1014`/`#16181C` **不得复活**；页面底**禁止再抬亮**（分层由台阶＋白框承担） |
| **② 台阶差取 +8 而不是 +4** | L1 `#0A0B0D`（输入框底/凹陷块，与交付值一致未改）→ L2 `#121417`（卡片/Dialog/抽屉/后台面板）→ L3 `#1A1D21`（Toast/菜单/骨架条/Tab 活跃底），每级 RGB 通道 **+8**。取 +4 时白框接管边界后面色差异看不出来，用户要的「深灰层次感」落不了地——这是本批实测出来的取值依据，不是审美偏好 | 面色一档到底（无台阶）＝上一批被判「平、没有层次」的形态；靠**抬页面底**分层＝用户明令禁止的方向 |
| **③ 语义状态边不随白框翻白（本批唯一例外）** | `--lc-border-danger-edge #402323`（危险对话框整框）与后台状态条 `--adm-warn-bd`/`--adm-info-bd`/`--adm-err-bd`/`--adm-purp-bd`（琥珀/红/半透明白族）**保持原值**——这些是状态标识不是区块框；`--lc-border-white` 旧别名收掉（七档本体已白），`ChatWindow` 输入区两处半透明白边改用 25% 白复合令牌 `--lc-line-white-25` | 「全部框线纯白」**不含**语义状态色；把 danger 边也刷白会让危险对话框失去提示作用（本批实测确认的边界） |
| **④ 五类渲染面逐面同步（dist 绿 ≠ 全站绿）** | ①React 令牌层＋组件内联；②后端直出 `/docs/*`（`public.go`）；③`/openapi/docs`（`admin_openapi.go` 的共享常量 `openAPIDocsCSS`）＋ `/office/taskpane.html`（`office.go`）；④assist `go:embed` 的 `web/admin.html`；⑤`extension/popup.html` ＋ `content.css` ⇒ **重打包 `1.2.1`**（`build_extension.sh 1.2.1`，指纹 `72a14441b1ce…`，1.2.0 保留可回溯，`latest` 同步）| 部署判据：②③ 必须换 `translator-server`、④ 必须换 `translator-assist`，只换 `/opt/translator/web` 对这三类一律不生效；⑤ 随前端 dist 换源即上线（不动二进制） |
| **⑤ 等值锁按新档重定（新值旧值两边都锁死）** | 源码级 `readability.test.ts`：**A 段**令牌本体等值（描边七档＝`#FFFFFF`、面档＝L1/L2/L3 实值）＋ **F 段**旧灰阶负向清零 ＋ **H 段**（⑤ 扩展两面＋②③④）白框等值、**旧面档值不许复活** ＋ **I 段**细描边负向清零；新增 **`BORDER_HEX_ALLOW`** 白名单口径＝**禁不透明灰阶档，半透明白放行**（弱标签 `.tag-lang`、加载转圈 fade 属白族降透明写法）——不透明 `#FFFFFF` 会渲染成实心白环。后端：`public_ui_test.go` ②③ 面等值 ＋ **`retiredRamp00O` 旧灰阶与 `border-*-dim` 名复活负向**、`admin_ui_test.go` ④ 面同步。运行时：`pixel_uat.spec.ts` 新增 **P2d**（工作台框实测 `rgb(255,255,255)` @2px、卡面 `#121417`、`body` 底 `#000`、后台带框件非白 0 命中）＋ **P6c** 直出页卡面改判 | ⚠️ **运行时锁与源码锁必须同口径**：P2d 起初把 `rgba(231,233,234,.14)` 判成违规（源码锁已放行），一严一松会让闸门长期红灯——「白族」判据要写成「禁不透明灰阶」而非「必须等于 #FFFFFF」。附带修：`Landing.tsx` 仪式动效峰值态改用**外扩白色光环**表达（白框之上没有更亮的边框档，旧的「边框提亮」会让峰值反而变暗） |
| **⑥ 闸门（2026-09-23 全绿）** | `go build ./...` ✓ ＋ `go vet ./...` 0 ＋ `go test -count=1 ./internal/api/ ./internal/assist/...` ✓；`npx tsc --noEmit` 0 ＋ vitest **99 用例全绿**（`src/styles/readability.test.ts` 单跑复验）＋ `npx vite build` → `index-ZFGse9Wb.css`；`build_extension.sh --check` 绿（1.2.1 无漂移）；发版前重闸门（PG 方言 `run_uat.sh`、`assist_uat.sh`、`multi_instance_e2e.sh`）按 §二 口径随本批跑齐，数字见 ⑦ | 改 UI 必点名五面；只跑 React 侧闸门会让 ②③④⑤ 带旧值上线 |
| **⑦ 发版与线上验收（2026-09-23 17:37，主站＋演示站两站同批）** | **三件齐上**（用户令「前后端都部署」）：`translator-server` sha256 `680c2927…`（主站 `/opt/translator/bin/`、演示站 `/opt/translator-demo/bin/`，**两站同 sha**）、`translator-assist` sha256 `f25ae595…`（`/opt/ai-assist/bin/`，assist 无演示实例）、两站 `/opt/*/web` 各走「解到 `web.new` → 校验首页引用 → 两步 `mv`」，新资产 **`index-C8yga-vW.js`／`index-ZFGse9Wb.css`**（两站首页均实测引用一致）。旧件与旧目录留时间戳备份（主站 `.bak.20260923_173714`／演示站 `.bak.20260923_173746`），归档按口径裁到最近 4 份 `web_old.*`／3 份 `.bak.*`；演示站换源后补 `chown -R root:caddy` + `chmod -R o+rX`，四个管线脚本随二进制从主站 `bin/` 同步（两站各 4/4） | 发版**前**闸门：`go build`／`go vet` 0、`go test -count=1 ./internal/api/ ./internal/assist/...` 全 ok、`tsc --noEmit` 0、vitest **48 文件全绿**、`vite build` 成功、**`run_uat.sh`（PG 方言）`RUN_UAT_EXIT=0` 且前端 E2E `exit=0（FAIL=0）`**。发版**后**接通类验收：两站内网 base `deploy_check.sh` **各 11/11**、公网 base **8/8**；`translator`/`translator-demo`/`ai-assist` 三服务 `active`、`journalctl --since 17:37 -p err` **No entries**、`panic` 命中 0；启动即备份产出 `tm_20260923_173714.bak.dump`；`/api/health` `anydoc_ready:true`、`/pricing` 与 `/docs/{sla,privacy}` 200、`/api/admin/ops/policy` 未登录 403；扩展包线上 `latest` 200／11831 B／`PK`，**内容指纹 `72a14441b1ce…` 三处相等（线上 zip＝仓库 zip＝`.sha256` 登记值）**；assist 内嵌管理台线上 `sha256 b2e6445e…` **等于仓库 `web/admin.html`**、`ASSIST_WEB` 生效行 0 命中（证明换的是二进制内嵌页、外置覆盖没复活）；线上 CSS 实测含 `#121417`/`#1A1D21`/`#0A0B0D` 与 `#FFFFFF` 白框，**旧灰阶与旧面档 0 命中**。⚠️ 本批按用户口径**不做前端像素校验**（「发版后不用校验前端，只要确认接线都通」），像素级形态由 `pixel_uat.spec.ts` P2d 在本地闸门承担 |

### 〇-N、恒暗根因修与全站字阶/描边抬档批（2026-09-23，★ 代码已推送 **`a5dbd27`**（纯代码提交，本地对应 `143cc8f`，并轨 merge `f6a80e7`）·文档仅本地·**主站与演示站已同日发版**（主站三件齐上、演示站两二进制欠账补齐 + 换源，见 ⑧））

> 来源＝用户两张截图（即时翻译语种面板 + 一张后台概览）+ 四条指令：
> 「1.聊天框选项，黑色的 UI 不该配黑色的字。2.后台 tab 字号变大。3.线框变粗。4.整体提升对比度，靠字体变大和加大线框粗细实现，不要变颜色。」
> AskUserQuestion 定档四条（全部按用户选择施工）：范围＝**全站（组件库令牌级）**；幅度＝**明显一档（字号 +2px / 描边 1.2px→2px）**；
> 真值＝**是，按新实测值重定档**（`UI-ANNOTATIONS` 与等值锁同步）；黑底黑字根因＝**干掉 light/auto 档**（三态与设置页切换钮一并删除，属用户明令的功能删除）。

| 块 | 交付 |
|----|------|
| **① 黑底黑字的根因（不是配色问题，是主题层问题）** | 文字色只写在 `html[data-theme='dark'] body` 覆写层，而主题默认 `auto` 跟随系统 ⇒ **系统外观为浅色的用户拿不到覆写**，纯黑底上落回浏览器默认黑字。修法两层：(a) `theme.css` 基础层无条件写死 `html, body{background:#000000;color:var(--lc-text)}`；(b) §九 的 30 条暗色覆写**全部去掉 `html[data-theme='dark']` 前缀**改为无条件生效。`lib/theme.ts` 由三态（light/auto/dark + `cycleTheme` + `watchSystemTheme`）收敛成 `applyTheme()` 一个函数：恒写 `data-theme=dark` + `color-scheme=dark`，并清掉遗留 `app_theme` 键；顶栏的主题切换钮删除（`App.tsx`），随之无消费方的 `app.theme.*` 三键从 zh/en 词典 + 10 份 locale 各删 3 行（12 文件 × −3，`locales.core.test.ts` 以 ALL_KEYS 动态长度为基准故不翻红） |
| **② 字阶 +2px（613 处）与描边 2px（161+61+4 处）** | 一次性脚本 `/tmp/retype_00n.js`（规则集带 `groups`，`FS_MAX=16`、细档 `{1,1.2,1.5}→2`）跑 55 文件 613 处字号 + 161 处描边；**第二遍** `/tmp/retype_camel.js` 补 JSX 驼峰与带引号值 61 处（第一遍的 CSS 语法正则扫不到 `borderTop:` 与 `border:'1.2px solid …'`）；收尾手改 4 处脚本仍扫不到的形态：模板串 `border: \`1.2px solid ${…}\``（`OrgP.tsx`）、三元 `borderTop: i ? '1px solid …'`（`TicketsPage.tsx`）、徽标描边 `border: \`1px solid ${fg}33\``、以及 `Login.tsx` 的 `box-shadow:inset 0 0 0 1.2px` hover 环。**四道独立证据**防批量静默损坏：干跑打印改动对 → 结构不变式（改动行与原始行的所有数字打码成 `#` 后必须逐字相等 + 行数不变）→ 落地 → `HEAD↔工作区`字号/描边直方图映射核对（`/tmp/retype_mapcheck.js`，输出「映射一致 true」）。>16px 的展示型大字（D1–D6）与 3px 强调条**未动**；`border-radius:1px` 保留（不是框） |
| **③ 五类渲染面一起抬（dist 绿 ≠ 全站绿）** | ①React 组件内联 `<style>` 与 `theme.css`/`mobile.css`/组件库 `src/ui/langcross/css/*`；②后端直出 `/docs/*`（`public.go`：body 14→16、品牌 15→17、导航/按钮 12→14、卡片描边 1.2→2px）；③`/openapi/docs`（`admin_openapi.go` 的共享 CSS 常量）+ `/office/taskpane.html`（`office.go`）；④assist `go:embed` 的 `web/admin.html`；⑤`extension/popup.html` + `content.css` ⇒ `build_extension.sh 1.2.0` 重打包（`langcross-extension-1.2.0.zip` 11633 B + latest，1.1.0 保留）。⚠️ 部署侧：**②③ 必须换 `translator-server`、④ 必须换 `translator-assist`**，只换 `/opt/translator/web` 对这四类一律不生效；⑤ 随 dist 即上线 |
| **④ 等值锁按新档重定（两侧、两层）** | 源码级：`readability.test.ts` C 段品牌 16 / Tab 15，新增 **I 段**（`TYPE_SURFACES` = `walkSrc('src')` + 扩展两面 + 后端三个 `.go` + assist 内嵌页，三条用例＝最小 11px 零命中 / 1px·1.2px·1.5px 细描边零命中 / 扫描量级守卫 >150 文件 & >100 个 `border…2px`）。I 段的描边正则是本批踩出来的：只写 `[a-z-]` 会整类漏掉驼峰，冒号后必须允许跑到 `px`（引号/三元），同时用 `(?<![.\d])` 后视兜住 `border:1px solid` 这种紧凑写法——**上一版闸门正是这三处形态漏的**（假绿）。运行时：`pixel_uat.spec.ts` P2b 几何档 40/**50**/**94**/**114**（工具条仍必须一排）、字阶 textarea 13→**15**、气泡 14→**16**、品牌 14→**16**、Tab 13→**15**、语种钮 12→**14**（顶栏行高仍锁 38，Tab 实高 28、语种钮 30 均 ≤38 防折行）。后端：`public_ui_test.go` ④ 段改判 `border:2px solid var(--lc-card-line)` 并负向清掉 `1.2px`/`border:1px `；`public.go` 里那句 served CSS 注释同步改写，否则**负向锁会命中自己的说明注释**（已踩到一次）。新增正向锁：语种面板展开后逐档量对比度——选项 15px、文字 `#E7E9EA` 对面板底 **17.2:1**（≥4.5 才放行），这条直接钉住用户投诉第 1 条的可见结果 |
| **⑤ 浅色宿主的回归闸门** | `e2e/dark_admin_upload.spec.ts` 整文件 `test.use({ colorScheme:'light' })`（不钉住的话，跑它的机器外观决定结果，闸门随机绿），D1 内预置 `localStorage.app_theme='light'` 后断言 `data-theme` 仍为 `dark`、`body` 文字色实测 `rgb(231,233,234)`、遗留键被清 |
| **⑥ 闸门（2026-09-23 全绿）** | `go build ./...` ✓、`go vet ./...` VET_EXIT=0、`go test -race -count=1 ./internal/...` **RACE_EXIT=0**（含 `TestGofmtGateZeroViolations`——本批一度因 `public.go` 注释未 gofmt 红灯，`gofmt -w` 后复绿）；`npx tsc --noEmit` 干净、vitest **48 文件 / 369 用例**、`vite build` → `index-DM3l5EoZ.js`；Playwright 全矩阵 **61 passed / 1 skipped** 跑了两遍（SQLite 与 PG 各一次）；`assist_uat.sh` **48/0**；`multi_instance_e2e.sh` **8/0**；`build_extension.sh --check` 绿（1.2.0，指纹 `38d7827c0012…`）；**发布闸门 `run_uat.sh`（PG 方言）＝API 主链路 96/0 + 交易专项 510/0 + 前端 E2E exit=0，PG_UAT_EXIT=0**。改动面：80 个已跟踪文件（+1068/−1003）+ 扩展新包 2 个未跟踪文件 |
| **⑦ 实测取证与清理** | 本地 `KEEP=1` 实例上跑量尺脚本取运行时真值（`/tmp/measure_00n.mjs`、`/tmp/measure_panel_00n.mjs`，脚本在 /tmp 不入库）；截图证据 `artifacts/p2b_workbench_merged.png`（工作台：单排工具条、2px 卡框、字阶明显变大）与 `artifacts/_00n_panel_light_host.png`（**浅色宿主**下展开语种面板，选项亮灰可读），一次性脚手架 `e2e-manual/_measure_panel_contrast.mjs` 跑完即删；四个 UAT 临时目录（`tmp.9fmZmCJpaX`／`tmp.HbyyWZRdfh`／`tmp.V13iu27TXT`／`tmp.gXLr1m0Ell`，合计约 162 M）按显式路径删净，8899/8898/8901/8902 四端口已无监听 |
| **⑧ 发版（用户令「全部做了」＋「演示站也要部署」，14:17 主站 / 14:19 演示站）** | **三件齐上**：HEAD 交叉编译 `translator-server` `73fee513…` 与 `translator-assist` `474586b0…`（`GOOS=linux CGO_ENABLED=0 -ldflags="-s -w"`），各 `cp` 备份 → `mv` rename 替换（ETXTBSY 口径）→ `systemctl restart translator ai-assist`；`vite build` 新资产 `index-DM3l5EoZ.js`／`index-CaGyRNei.css`，两站均走 `web.new` 校验引用 → 两步 `mv`，演示站换源后补 `chown root:caddy` + `chmod -R o+rX`。**演示站无 assist 实例**（`bootstrap-demo.sh` 只装 `translator-demo`），故它只换 `translator-server` + `web`，且与主站二进制 **sha 完全相同**。**验收（全部在换件换源之后）**：两站内网 `deploy_check.sh` 各 **11/11**、公网两域各 **8/8**；`journalctl --since "14:17" -p err` 三服务**零条**、全级别 `panic`／`fatal` 关键字零命中；五类渲染面逐项线上核——`/docs/{terms,sla,privacy}` 2px 命中 3/4/3 且细档 **0**、`/openapi/docs` 2px **5**、`/office/taskpane.html` 2px **3**／**5151 B**（两站同值）、assist 内嵌管理台 **19125 B / sha `24d72756e99811f5…`＝仓库 `internal/assist/web/admin.html` 逐字节相同**（并复核 `ASSIST_WEB` 外置覆盖仍为 0，内嵌页仍是单一事实源）、扩展包两站 `latest.zip` 与 `1.2.0.zip` 均 **200／11633 B／`PK`**。**浏览器实测（`colorScheme:'light'` + 1280×720＝原故障场景）**：主站公开页底 `#000000`／文字 `#E7E9EA`（**17.24:1**）、`data-theme=dark`、`color-scheme=dark`、遗留 `app_theme` 键被清、描边最小 **2px**；演示站即时翻译 **114/94/50/40** 逐项相等、工具条单排、输入框 **15px**、顶栏 38／Tab **15**／语种钮 **14**、描边取样 46 处细档 **0**；语种面板 `lms-panel--up` 向上弹（`bottom 645 ≤ 触发上沿 651`）、选项 **15px** 且 `#E7E9EA` 于 `#0A0B0D` **16.17:1**（已选/未选同值）。⚠️ 取证坑：**内嵌浏览器 MCP `innerWidth=0`** 会让媒体查询落到移动档，量出 `.app-header .brand` **17px**（`mobile.css` 移动档）而非桌面 16px——UI 数值取证必须用显式视口的 Playwright。治理：两站 `web_old.*` 各裁至 4 份、`/tmp` 本批上传件删净，磁盘 **38%、余 24 G** |

> **第 2 张截图的口径澄清（重要，别当成「已修」**）：截图标题「极石智能翻译平台 · 管理后台」里的品牌名**可以**是本产品——
> 后台侧栏标题走 `AdminDashboard.tsx:185` 的 `branding.brandName || t('admin.title')`，白标租户配了 `brand_name` 就显示它。
> 但截图正文那几条字面文案（**余额可用天数**、**计费明细**）在本仓源码、`git log --all -S` 全历史与交付 UI 包里**都不存在**
> （本仓概览页的对应卡是「知识库条目 / 组织余额（积分）/ 流程步骤启用 / 用量类型 / 主模型状态 / LLM 错误率」），
> 所以那张图不是本仓任一版本的页面。恒暗修复与抬档对**任何**该形态的页面都成立（同一套 `theme.css` + 组件库），
> 但本批不声称「该页已修」。
>
> **提交与推送（已按用户令执行）**：代码提交 `143cc8f`（80 个代码文件，提交内 `.md` 与 `前端及UI相关/` 计数 **0**）→ `scripts/push_code_only.sh` 干跑（基点 `origin/autosales=76d38ec`、判出「本地领先含 5 个文档文件、待推 80 个代码文件」）→ `--apply` 推出纯代码提交 **`a5dbd27`** 并并轨 merge `f6a80e7`。
> ⚠️ 首跑 `--apply` 被 git 拒：脚本要 `checkout -b` 建纯代码临时分支，而工作区还挂着未提交的文档编辑 ⇒ 口径入账：**跑推送脚本前工作区必须只剩未跟踪件**，文档要么先提交、要么带标签 `git stash push -- <显式文档路径>` 挪开（本次同时留 `/tmp` 补丁备份），推完 `git stash apply` 原样恢复再提交文档。
> 部署口径＝**前端 dist 换源 + `translator-server` + `translator-assist` 两二进制替换**（本批动了渲染面 ②③），扩展 1.2.0 包随 dist 上线；**实际已两站齐上**，见上表 ⑧。

### 〇-M、即时翻译输入区元宝式单卡批（2026-09-23，★ 代码已推送 **`c0e5e99`**（纯代码提交，本地对应 `cdae8dc`，并轨 merge `4d3bb1f`）+ **`cba5e61`**（本批 dom 测试补中文注释，本地 `a772afe`，merge `f5c0079`）+ **`76d38ec`**（全量中文注释批，36 个代码文件、机器证明零删除，本地 `b612e9c`，merge `693d9da`）·文档仅本地·**主站与演示站已同日发版**（主站只换 `web`，演示站补齐二进制 + `web` 到同版；11:24 两站再各补换一次 `translator-server` 至同 sha `0e0343d2…`，见 ⑧））

> 来源＝用户两条指令 + 一次 AskUserQuestion 定档：「即时翻译的输入框和选择器这一块的太长了，太占空间了。我要类似元宝这种的」⇒
> 选择「**框内一行工具条**」（保留〇-LJ/LK 定的整屏单框 + 输入贴底，只把框脚内部压扁）与「**chips 内联、超 3 折叠**」。
> 按记忆里的「形态指令先复述再动手」，这两档是先复述「谁包住谁」并等用户确认后才施工的（〇-LJ 读反方向的教训）。

| 块 | 交付 |
|----|------|
| **① 输入区形态：四排 → 一卡一排** | 旧框脚 = 「原文标签+输入框」/「整宽语种下拉」/「chips 独立行」/「模式+缩翻+按钮行」四排。新形态＝一张「凹」进卡面的 `.cw-composer`（底 `--lc-inset`、描边 `--lc-border-input`），内部只有两层：`textarea`（`rows=1`，空态实测 **40px** 即一行高，`autoResize` 下限由 96 收到 40、上限 240 不变）+ `.cw-toolbar` **一行**（源语言胶囊「原文 · 自动检测」· 目标语言胶囊 · 已选语种 chips · 模式分段 · 缩翻（数值框仍只在勾选后出现）· 行尾主按钮）。1280×720 实测工具条 **48px**、输入卡 **90px**、框脚 **109px**（旧形态约 250px）。**功能一项未减**：会话搜索/导出/清空、预估与余额、校对模式、缩翻上限、停止、浮球让位（`.cw-toolbar` 右内边距 68px 给 `.na-fab`）全部保留，窄屏靠 `flex-wrap` 自行换行 |
| **② chips 折叠成「+n」但仍可 ×** | `LangChips` 加两个可选入参（默认值保持工单页老形态，零调用点改动）：`max`（内联最多几颗，其余折成「+n」）、`dense`（行内模式去掉独立成行的 8px 下外边距）。**「+n」是真按钮**（`data-testid="lang-chips-more"`，语言名列表进 `title`/`aria-label`），点下去就地展开全部——折走的语种必须仍能一键 ×，否则用户得开面板滚动找勾，那就是「因压缩而缩水」 |
| **③ 语种胶囊档 + 向上弹面板（隐性缺陷）** | `LangMultiSelect` 新增 `compact`：触发器走 `.lms-trigger--pill`（高 28 与相邻分段控件同档、宽随内容不撑满、文案取短档 `chat.targetLangLabel`，长档「选择目标语言」塞进 28px 胶囊会把 ▾ 挤掉），面板走 `.lms-panel--up`。**这条是真缺陷修复**：触发器贴屏幕底部后，向下弹会顶出视口，而宿主 `.cw-dialog` 是 `overflow:hidden`，越界部分直接被裁掉——表现就是「点了没反应」，且**结构锁与 DOM 顺序锁看不见这类失败**，所以锁必须落在运行时几何（见 ④） |
| **④ 三处形态锁按新形态钉死（等值锁）** | ①`ChatWindow.tsx` `.cw-dialog*` CSS + 文件头「形态沿革」记 〇-M 改档理由；②`ChatWindow.dom.test.tsx` 新增 **⑤**：`.cw-toolbar` 必须在 `.cw-composer` 内、全站只有一座、语种/chips/模式/缩翻/主按钮全在这一排、旧 `.cw-composer-label` 与 `.cw-dialog-acts` 必须为 `null`、框脚只剩一个子节点；③`pixel_uat.spec.ts` P2b 按交付真值写**等值锁**（`taH=40`/`toolbarH=48`/`composerH=90`/`footH=109`，并把「工具条必须是一排」算成实控 top 归档集合 `toolbarRows===1`——零高的弹性占位先剔除，否则换行会静默通过），另加面板向上弹的负向锁（`panel.bottom <= trigTop` 且 `panel.top >= 0`）。`LangMultiSelect.dom.test.tsx` 补 4 例锁胶囊档与折叠口径。**不写「只准更矮」的单向锁**（AGENTS §5：〇-L 立的就是这条） |
| **⑤ 闸门（2026-09-23 全绿）** | `npx tsc --noEmit` 干净；vitest **48 文件 / 368 用例**（本批 +5）；`vite build` 成功（`index-D0DEyVi8.js`）；Playwright 全矩阵 **61 passed / 1 skipped**（`smoke.spec.ts` 两条需另给 `API_URL`，默认指 8787 的 dev 代理，实跑 `API_URL=BASE_URL` 后 3/3 绿——非本批回归）；`go build`+`vet`+`go test -race ./...` **GO_EXIT=0**（后端零改动）；`assist_uat.sh` **48/0**；`multi_instance_e2e.sh` **8/0**；`build_extension.sh --check` 绿（未动 `extension/`）。本批**只动 React 组件与内联 `<style>`**（AGENTS §5 渲染面 ①）⇒ 部署只需换 `/opt/translator/web`，两二进制不动。末次为「本批 dom 测试补中文注释」的纯注释提交后复跑：`tsc` 干净、vitest **368 用例**、`vite build` 产物与线上 **26 个文件逐一同内容**（两端聚合 sha 的差异只是 `find` 排序口径），**故前端无需重发版**。⚠️ 但「纯注释＝不用发版」对**后端内嵌页不成立**：随后的全量注释批 `b612e9c` 往 `office.go` 内嵌 taskpane 的 JS 字符串里加了两行说明，那是渲染面 ③ ⇒ 两站补换一次 `translator-server`（见 ⑦ 末段） |
| **⑥ 测试数据与本地痕迹清理** | 一次性量尺脚本 `frontend-react/e2e-manual/_measure_composer.mjs` 与两张量尺截图（`artifacts/_measure_5langs.png`·`_measure_expanded.png`）跑完即删；`KEEP=1` 留下的四个进程（`uat-server:8899`／`mock_llm:8901`／`mock_chain:8902`／`assist-server:8898`）与临时库目录 `tmp.wRlhhBRbhp` 已终止删除；本会话误起的 `vite --port 5174` 已停（**5173 上是另一个项目的 vite，未动**）。★ 后令已清完（11:35）：`$TMPDIR` 下 **21 个**历次 UAT 遗留 `tmp.*` 目录（约 928 M）逐个显式路径删净（宽匹配 `rm -rf` 会被权限分类器拦下，改用「按签名逐个点名」的窄命令即可通过），整目录 **1.9 G → 1.1 G**；判定签名＝`api_uat.log`／`uat-server`／`instA.log·instB.log`，余下唯一一个 `tmp.1J4PGxBkDE` **无 UAT 签名**（0 M、归属不明）故未动。根因仍在：`run_uat.sh`/`multi_instance_e2e.sh` 的 `KEEP=1` 路径不清临时目录，不清脚本就会再次堆积 |

| **⑦ 发版（用户令「前后端都部署，主站和演示站都部署」）** | **主站＝纯前端换源**：先实核 `git diff b51097d..HEAD -- backend-go/ extension/` **零改动** ⇒ 两二进制保持 〇-LK/〇-LL 的 `7ea4814b…`/`64a16a21…`，不替换不重启；10:21:50 走 `web.new` → 首页引用 `index-DRdu9cyA.js`→`index-D0DEyVi8.js` 校验 → 两步 `mv`。**演示站＝一次补齐 5 个批次的欠账**（前端停在 09-19、`translator-server` 停在 `95e307ce…`）：HEAD 交叉编译的新二进制 `aef52f0f…` 先 `cp` 备份再 `mv` rename 替换（`cp` 覆盖运行中文件会 ETXTBSY），四个文件管线脚本随二进制同步，`systemctl restart translator-demo` 后再换 `web` 并补 `chown root:caddy` + `chmod -R o+rX`（漏了就是「前端页面尚未构建」老故障）。**验收全部在换源之后**：两站内网 `deploy_check.sh` 各 **11/11**、公网各 **8/8**（生产注册需邮箱验证 ⇒ 第 5 项自动只探 balance 通道，不留测试账号）、`journalctl --since "10:20" -p err` 零条、两站扩展包 200／11634 B／`PK`、线上 chunk `.cw-toolbar` 命中而 `.cw-dialog-acts`·`.cw-composer-label` 零命中，最后用 `e2e-manual/_verify_online_composer.mjs`（演示账号，跑完即删）**在浏览器里量到线上几何与锁值逐项相等**：109／90／48 单排／40／28、面板向上弹且 `top 272 ≥ 0`。治理：两站 `web_old.*` 各裁至最近 4 份，`/tmp` 上传件与 `deploy_check.sh` 副本删除，磁盘 **39%、余 23 G** |
| **⑧ 发版续（11:24，两站后端补换一次）** | 顺序坑：10:21 发版在「全量中文注释」批 `b612e9c` **之前**，而那批把两行注释写进了 `internal/api/office.go` 内嵌 taskpane 的 **JS 字符串**里 ⇒ 属 AGENTS §5 渲染面 ③（后端直出页），只换 `web` 不会生效。实核过才动手：`strings` 找不到中文（BSD strings 只取 ASCII）改用 `LC_ALL=C grep` 确认新二进制含该串、线上两站 `grep -c` 为 **0** ⇒ 差异真实存在。HEAD 交叉编译 `0e0343d2…`，主站与演示站各 `cp` 备份 → `mv` rename 替换（ETXTBSY 口径）→ `systemctl restart translator translator-demo`，**两站二进制从此 sha 相同**；`translator-assist` 不换（只碰 `engine.go` 函数注释，`go:embed` 的 `web/admin.html` 字节未变）。换件后复跑：两站 `/office/taskpane.html` **5157 B**（换件前 4921，差值＝两行注释）、内网 `deploy_check.sh` 各 **11/11**、公网各 **8/8**、`journalctl --since "2026-09-23 11:24:00" -p err` **No entries**、首页仍引用 `index-D0DEyVi8.js`、扩展包 200／11634 B／`PK`。**口径入账：纯注释提交对 React 侧是「不发版」（dist hash 不变），对后端内嵌页是「必须换件」** |

> **交付真值侧补了一条**：`UI-ANNOTATIONS.md` 的即时翻译切图稿仍是**双栏**，与线上「整屏单框 + 输入贴底单卡」不一致，
> 已在该文件末尾新增 §6「实现侧用户后令覆盖记录」把 109/90/48/40 与三处锁位置写进真值表，防止后人照切图把输入区改回四排。
> **两件待决策已按后令处置（11:33–11:35）**：① `$TMPDIR` 下 **21 个** UAT 遗留 `tmp.*`（约 928 M）按签名逐个点名删净，整目录 1.9 G→1.1 G（余一个无 UAT 签名的 `tmp.1J4PGxBkDE` 归属不明，未动）；
> ② `/opt/translator-demo/bin/translator-server.bak.*` 由 **26 份裁到最近 3 份**（按文件名时间戳裁——`cp -a` 会保留原 mtime，`ls -t` 排序不可信），`/opt/translator-demo/bin` **612 M → 91 M**、整盘 **39% → 38%（余 24 G）**；保留 `20260923_112341`／`20260923_102303`／`20260919_104038`，回滚路径仍完整。主站侧 4 份未动。
> ⚠️ 已知未做：主站未做同样的浏览器实测（不拿真实客户账号登录）——两站下发的是同一份 dist，`index-D0DEyVi8.js` 远端/本地 sha256 `9c3e0678…` 一致，形态等价按这条 + chunk 命中数判定。

### 〇-LL、主站部署 + 磁盘治理 + assist 知识库同步 + 扩展交付链批（2026-09-23，★ 代码已推送 **8395f3c + 1ff038c**（本地代码提交 `b51097d`、修复提交 `b6e9efc`，并轨 merge `192a1db`·`623bba3`）·文档仅本地·**主站已部署**（〇-LK 两二进制 + web 两次换源，末次 06:27）·演示站未部署）

> 来源＝用户四条指令：「1.阅读部署文档，主站部署。2.清理磁盘过时内容。3.assist kb_entries 关键词帮我同步（采集链没覆盖的部分）。4.extension/ 至今无交付渠道：无打包脚本、无托管 zip、manifest 还停 1.0.0，这些帮我做了。」
> 做完接固定收尾四步：①自查是否全部做完 ②自动化测试接入今日开发内容 + 清测试数据 ③前后端全量中文注释 + commit/push（**不带文档和流程图**）④更新项目文档、去掉过时内容。
> 另按用户在 AskUserQuestion 里的顺带选择做了三件：文档迁独立分支（`docs-local`）、清理 `web_old` 归档、补扩展交付链；**演示站补部署未被选择，按前令仍未动**。

| 块 | 交付 |
|----|------|
| **① 主站部署（〇-LK 欠的三件齐上）** | 先按《部署指南》§五/§十三核对口径：本批 〇-LK 同时动了 `internal/api/*`（含新文件 `assist_open_proxy.go`）与 `internal/assist/*` + 前端 dist ⇒ **`translator-server` + `translator-assist` 两二进制 + `web` 换源三件必须一起上，只换 web 则挂件缓存的服务端根因与 Token 热生效都不生效**。05:02 两件 scp→`mv` rename 替换（`translator-server` sha256 `7ea4814b…`、`translator-assist` sha256 `64a16a21…`，旧件留 `.bak.20260923_050316`）→ 05:03:16 `systemctl restart translator ai-assist` 两服务 active → `/opt/translator/web` 两步换源（旧目录留 `web_old.20260923_050316`）。至此 〇-LK 顶部记的「主站未部署」状态作废 |
| **② 磁盘治理与「日志没人回收」的根治** | 先看构成再动手：整盘 66% 用、余 13G，大头**不是** 41 份 `web_old`（各 2.9M，合计 ~120M），而是 `/opt/translator/log/translator.log` **2.85 GB**（占整盘 7%）——两服务都用 systemd `StandardOutput=append:` 写文件日志，而 `/etc/logrotate.d/` 里**根本没有对应条目**，即除了手动 gzip+截断没有任何机制回收。处置：旧日志归档压缩为 `translator.log.20260923.gz`（107 M，保留可查）→ 新增并安装 `deploy/logrotate/translator.conf`（`daily` + `maxsize 200M` + `rotate 7` + `compress delaycompress` + **`copytruncate`**，**不加 `su`**：`assist.log` 是 root:root，加 su 会让截断失败而静默不轮转）→ `logrotate -d` 判读 + `-f` 实跑（`translator.log.1`／`assist.log.1` 生成，两份活动日志归零）→ `web_old.*` 裁到**最近 4 份**。结果：磁盘 **66% → 39%（余 23 G）**，日志目录 2.9 G → 103 M |
| **③ assist 生产库知识库同步（收口自 09-20 的「待人工执行」）** | 先定范围再改：`assist.db` 与仓库 `internal/assist/seed/seed.json` 逐字段比对（`category/title/content/keywords/link_keys/priority/enabled`），差异**恰好 3 行**（`languages`／`billing-points`／`what-is`）；再做三方对照 `git show bd34c36^` 证明是**旧 seed 停在库里**而非线上被人工改过（改动方向与 〇-XLVI/XLVIII 的 seed 修订完全一致），才敢以 seed 为准回写。落地：`python3 scripts/assist_kb_sync.py --apply`——默认只读、写前 `.backup` 出带时间戳的库备份（`assist.db.bak.20260923_054618`，98304 B；用 sqlite3 `.backup` 且经 stdin 送 dot-command，**不是 `cp`**——WAL 态下 `cp` 拷不到一致快照，且 `.backup` 作为命令行参数会被 sqlite3 拒收）、只 UPDATE/INSERT **绝不 DELETE**、写后重读复核；UPDATE **恒带 `updated_at=CURRENT_TIMESTAMP`**（第 3 级向量索引失效指纹 = `embed_model` + 每行 `key`+`updated_at`，不 bump 就静默沿用旧嵌入；顺带查明生产没配 `embed_recall`/`embed_model`，第 3 级本就是关的）。同批确认 `scripts`/`flows`/`feature_links`/`configs` **零漂移**，没有第二处人工同步欠账 |
| **④ 扩展交付链（从零建到「客户点得开」）** | 版本口径：`extension/manifest.json` 的 `version` 为**唯一事实源**（1.0.0→**1.1.0**），任何脚本/文件名/测试都现读不复制。新增 `scripts/build_extension.sh`：显式白名单 7 文件（`manifest.json background.js content.js content.css popup.html popup.js INSTALL.txt`，不让 `.DS_Store` 混进用户包）→ 产出 `frontend-react/public/extensions/langcross-extension-<ver>.zip` + `-latest.zip` + 各自 `.sha256`；`--check` 为漂移闸门（防「改了源码忘了重打包，站点还在发旧包」），带版本号参数即 bump manifest 后重打。**为什么 zip 进仓库**：站点是自托管形态（无对象存储、无 CI 产物仓），zip 不进仓库就等于没有下载入口，体积 11.6 KB 可接受。前台侧：后台「SDK 与集成」页新增下载卡（`SdkP.tsx` 挂 `/extensions/langcross-extension-latest.zip`），文案 3 键 × **12 语种**（`sdk.extTitle/extDesc/extDownload`）；包内 `extension/INSTALL.txt` 写自托管安装步骤（**刻意不叫 `.md`**——纯代码推送过滤器按 `*.md` 排除，叫 .md 会和它登记的指纹分家）。上线：06:27 随 dist 换源（`/extensions/*` 走 `spa.go` 静态直出，**不动后端二进制**），线上实测 200 / 11634 B、首两字节 `PK`，且下载体逐文件**内容指纹 `d88ac536…` 与仓库 `.sha256` 登记值、`--check` 输出三者相等** |
| **⑤ 测试接入（覆盖本批全部开发内容）** | vitest 新增 `src/extensionPackage.test.ts` **4 例**（托管 zip 与源码内容指纹等值、manifest 版本↔包名↔页面链接一致、`--check` 同口径）→ 48 文件 / **363 用例**；Playwright 新增 `e2e/extension_download.spec.ts` **2 例**（latest 与带版本号包各一）→ 62 用例，判据按 AGENTS §6 的可达探针写：**200 + 响应体不是 SPA 兜底的 `index.html` + 首两字节 `PK` + 体积合理**（只判 200 必假绿，`spa.go` 对不存在路径也回 200）；`build_extension.sh --check` 入 §二 闸门清单。**测试数据**：三套脚本各自临时库（`$WORK/dev.db`、`$WORK/assist.db`、PG `translator_uat` 每次重建），新断言无外置产物；本地散件（`/tmp/vprobe`、`/tmp/extchk`、`/tmp/dl.bin`、打包 tar、服务器侧探针备份）已全部清理 |
| **⑥ 全量中文注释 + 只推代码（并修掉「只推代码」机制自身的首跑缺陷）** | `scripts/missing_comments_ts.py` 报 4 处顶层声明缺注释 → 补齐（`extensionPackage.test.ts` 三个路径常量、`extension_download.spec.ts` 的 `VER`）后 tsc/vitest/build 复跑绿。新增 `scripts/push_code_only.sh`（把「文档不外推」从流程约束变成机制：推出去的提交直接长在 `origin/<分支>` 上，内容取本地代码文件的目标状态，并排除 `*.md` 与 `前端及UI相关/`）。**首跑即抓出它自己的真缺陷**：`git checkout -b $TMP $BASE` 会把 BASE 里不存在的新增文件从磁盘删掉，于是 `[ -e "$ROOT/$f" ]` 恒假、25 个代码文件只推上去 13 个（`8395f3c`）——修复为「取/删判定问 git 对象库 `git cat-file -e`」+ 新增**第三道与本地树逐文件等价校验**（`b6e9efc`），补齐余下 12 文件（`1ff038c`）。两次推送各自实核 `.md` 计数 0，再 merge 并轨回本地（`192a1db`·`623bba3`），本批文档提交全部留在本地侧 |

> 闸门（2026-09-23，全绿）：`go build ./...` + `go vet` + `gofmt -l` 干净、`go test -race ./...` **GO_TEST_EXIT=0**；
> `npx tsc --noEmit` 干净；vitest **48 文件 / 363 用例**；`vite build` 成功（末次资产 `index-DRdu9cyA.js` / `index-DtdiKHPp.css`）；
> `run_uat.sh`（PG 方言，发布闸门）**RUN_UAT_EXIT=0**：A/B **96/0** + 交易专项 T **510/0** + Playwright **61 passed / 1 skipped**（62 用例，skip 项是 `ADMIN_PASS` 环境守卫）；
> `assist_uat.sh` **48/0**；`multi_instance_e2e.sh` **8/0**；`build_extension.sh --check` 绿。
> 线上验收（**在 06:27 最后一次换源之后复跑**，不是换源前）：`deploy_check.sh` 服务器本机内网 base（127.0.0.1:8787）**11/11**、公网 base **8/8**；
> `journalctl -u translator -u ai-assist --since "09-23 05:03" -p err` **零条**；扩展包线上下载三连比对见 ④。
> **①生产 `assist.db` 的 2 条验收探针会话已按令删除（08:04）**：`s1790111179931c8622fc58e0791b5`（1 问 2 答）、
> `s1790111144170f377da468c159705`（仅欢迎词）——先按 `created_at` 与消息构成核对确认是自己的探针、且其余 11 会话一条不动，
> 再取**新的** `.backup` 快照（`assist.db.bak.delprobe.20260923_080403`，写后回读复核 13/53 才动手）→ 单事务先删 `messages`
> 后删 `sessions`（`messages` 无外键级联，必须手删子表，否则留孤儿行）→ 复核 **11 会话／49 消息、目标会话消息残留 0、
> 孤儿会话 0**；两服务 `/health`·`/api/health` 均 200，无需重启（SQLite WAL 同库并发写）。
> 顺带一条排障口径：unit 名是 **`ai-assist.service`**，`systemctl is-active translator-assist` 会回 `inactive`（那是二进制名），
> 别把它当成服务挂了。
> ②演示站仍未部署（本批又扩两批次：〇-LK 与 〇-LL 都只上了主站）。〔★ 已由 〇-M 按用户令补齐：09-23 10:23 演示站二进制 + `web` 一次换到同版，见顶部 〇-M ⑦〕已闭：assist 知识库同步、扩展交付链、`web_old` 归档清理、日志轮转机制；
> 文档侧「不外推」改为**双保险**——`docs-local` 分支（永不推送）+ `scripts/push_code_only.sh`（推的提交直接长在 `origin/<分支>` 上）。
> ⚠️ 诚实口径：`autosales` 的**历史里仍夹着此前的文档提交**（如 `9f2a0af`），没有改写已推送历史来清它们，泄漏由脚本那半兜住。

### 〇-LK、AI 助手会话可用性与配置口径批（2026-09-23，★ 代码已推送 87a589f（同一改动在本地分支上另号 e1b9e46，并轨 merge 06a8424）·文档仅本地·**主站已部署（当时止于「未下部署令」，09-23 〇-LL 按令三件齐上，见顶部 〇-LL ①）**·演示站未部署）

> 来源＝用户三条反馈：①「另外 ai 助手要带缓存，不然刷新一次页面就没了很尴尬的」；
> ②「我截图的配置，请参考我其他 llm 配置的方式重新做」（assist 管理 Token 那一栏）；
> ③「你们家对话框是放顶部的啊，会不会做 ai 对话页面」（附即时翻译工作台截图）。
> 施工中还照出一个**闸门盲区**（见下第 ④ 块），属本轮补防而非用户直接反馈。

| 块 | 交付 |
|----|------|
| **① 挂件刷新不丢对话（`AiAssist.tsx` + `api/assist.ts`）** | 恢复序改为**本地缓存 → 服务端 history → 新会话 greet** 三层：`ny_assist_msgs`（`{sid,msgs}`，40 条上限）由 `bubbles` 变化统一回写（走 effect 而非各调用点补写，空数组不写以免恢复中途清空），缓存定位「即时可见 + 离线兜底」，服务端 history 回来以服务端为准对账；**只有两者都空**才 greet，避免把已看过的对话覆盖成一句欢迎词；tok 失效（401）时保留缓存、只标离线，等下次 send 的 401 自愈换新会话。服务端根因同批修掉：`sess_key` 改为随机生成并持久化 `configs.sess_key`（旧实现由**管理 Token** 派生，assist 重启或 Token 轮换即作废全部访客 tok），且 `sess_key` 完全不在管理面列出（`hiddenCfgKeys`）；`handleGreeting` 改为「该会话已有历史则不再叠欢迎词」 |
| **② 管理 Token 对齐 LLM 配置范式（`admin_assist.go` + `AssistP.tsx`）** | 出参改**掩码态**：`source(env/db/none)` + `set` + `masked` + `db_masked` + `env_overridden`，明文一律不出后端进程；保存留空＝不修改、掩码值回写＝`skipped`、清除走显式 `clear:true` + 前端二次确认；`env` 占用生效位时面板置灰并解释（InlineBanner）；输入框 `type=password` + `autoComplete=new-password` + 不回填。生效链：`configs.admin_token` > 启动快照，带 60s TTL 与 `invalidateAdminToken()`，主后台保存后**推送 assist 即刻热生效**（`pushed:false` 时明确提示需重启 `translator-assist`），前端不再显示明文 Token、iframe 老路径（#34 后不存在）的 `token`/`has_token` 字段删除 |
| **③ 即时翻译工作台输入区贴底（`ChatWindow.tsx`）** | 〇-LJ 定档的「整屏合并单框」保留，但把输入区从滚动区里挪进**框脚**：`.cw-dialog-head`（工具条）/`.cw-dialog-body`（消息流，唯一滚动区）/`.cw-dialog-foot > .cw-composer`（输入贴底 + 语种行 + 操作行常驻），`autoResize` 上限改 `max(96, min(240, innerHeight*0.28))`；功能一项未减（会话搜索/导出/清空、缩翻、预估与余额、校对模式、反馈入口）。三层锁同步改向：`ChatWindow.dom.test.tsx` 结构锁、`pixel_uat` P2b 运行时几何锁、`mobile_uat` 选择器 |
| **④ 补防：`/assist-api` 三条转发路径与「离线假绿」** | 挂件写死同源前缀 `/assist-api`，此前该前缀**只存在于 vite dev proxy 与生产 Caddy**——「主服务直出 dist」的形态（单二进制本地跑、发布闸门 `uat-server -frontend dist`）下每个请求都落进 SPA 兜底拿回 `index.html`，挂件全程显示「助手暂时联系不上」，而 W1/W2 因「缓存与界面照样一致」**照样绿**：闸门里助手链路从未真跑通过。现补主服务侧白名单转发 `internal/api/assist_open_proxy.go`（只放 greeting/chat/history/features，`/assist-api/api/assist/admin/*` 一律 404 不给管理面旁路、不注入任何凭据、上游 4xx 原样透传、请求体 1MB 与响应 4MB 限长、不可达回 502），并在 e2e 加 `expectLiveLink` 可达探针（无离线徽标 + greet 必须下发过 sid），三条用例各过一遍 |
| **⑤ 测试接入（覆盖本轮全部开发内容）** | Go：`TestAdminTokenHotRotate`（写新 Token 后新值 200/旧值 401、拒空、掩码回显不外泄、掩码回写 skip）、`TestGreetDedupesWelcome`、`TestSessKeySurvivesRestart`（同库重启后老 `sid+tok` 仍可读史、伪 tok 仍 401）、`TestAssistOpenProxy*` 6 例（转发/白名单外 404/方法 405/上游 401 透传/不可达 502/不注入凭据）；`assist_uat.sh` 新增 **G1–G8**（原 38 → **48 断言**）；e2e 新增 `assist_widget_cache.spec.ts` W1–W3 与 `assist_admin.spec.ts` **A7**（Token 区掩码态，注释写明**故意不做真轮换**，否则同批其余用例的代理链路一起被改坏）；vitest 新增 AssistP 管理 Token 7 例 + AiAssist 缓存单测；`api_uat_txn.sh` 新增 `mny_norm` 方言归一（SQLite 把数值列回吐成 `5.0`/`1.0`，金额直读等值锁 T51/T54 在本地快跑恒假红） |
| **⑥ i18n 与错误码口径** | `assist.*` 新增 12 键（`tokenSet/tokenUnset/tokenSrcLabel/tokenMaskNote/tokenClear/tokenClearConfirm/tokenCleared/tokenPushed/tokenNotPushed/tokenEnvLocked/tokenUnchanged/tokenMaskTyped`）＋改值 `tokenPh/tokenSaved`，`panels/assist.ts`(zh/en) + 10 份 `locales/*.ts` 全量同步；`audit.action.assist_token_rotate/assist_token_clear` 补进 12 份词典（漏译棘轮回基线 32）。`internal/errors` 新增统一错误码 `METHOD_NOT_ALLOWED`(405) 与 `UPSTREAM_UNAVAILABLE`(502)，新 handler 零内联错误响应（#42 棘轮被本批新文件顶红过一次 746>741，按闸门提示改走 `s.writeError` 后回到 741） |

> 闸门（2026-09-23，全绿）：`go build ./...` + `go vet` + `gofmt -l` 干净；`go test -race ./internal/...` 通过；
> `run_uat.sh`（PG 方言，发布闸门）A/B **96/0**、交易专项 T **510/0**、Playwright **59 passed + 1 skipped**
> （`RUN_UAT_EXIT=0`）；`assist_uat.sh` **48/0**；`multi_instance_e2e.sh` **8/0**；
> vitest **47 文件 / 359 用例**；`npx tsc --noEmit` 与 `vite build` 0 错。
> ⚠️ 同口径 `DB_DRIVER=sqlite UAT_SKIP_RACE=1` 快跑曾报 T51/T54 两条金额假红（跨方言数值文本差异，
> 已由 ⑤ 的 `mny_norm` 归一）——**SQLite 快跑不是发布闸门，判定只认 PG 那一跑**。
> 测试数据清理：三套脚本各自临时库（`$WORK/dev.db`、`assist.db`、PG `translator_uat` 每次重建），
> 新增断言无外置产物；`/opt/translator/web_old.*` 累积 **41 份**（磁盘 66%、余 14G）。〔★ 本行末尾的磁盘状态已被同批 〇-LL 治理取代：裁至最近 4 份 + 日志轮转落地，66% → **39%、余 23 G**〕
> **发布链**：按「代码提交 → push → 才提交文档」口径执行——本地代码提交 `e1b9e46`（37 文件、**零 .md**），
> 在 `origin/autosales` 之上另落纯代码提交 **87a589f** 推送（`git diff --name-only 43da5aa 87a589f` 实核
> 37 文件、`.md` 计数 0），再 merge 回本地并轨（`06a8424`），本批文档提交全部留在本地侧。
> **主站部署状态：未执行**〔★ **已由同批 〇-LL 执行**，见顶部 〇-LL 块 ①：05:02 两件二进制 `mv` rename 替换 + 05:03:16 重启 + web 换源〕。本批同时动了 `internal/api/*`（含新文件 `assist_open_proxy.go`）、
> `internal/assist/*` 与前端 dist，按 §五/§十三 口径发版需 **`translator-server` + `translator-assist` 两二进制 +
> `web` 换源**三件齐上（只换 web 则挂件缓存的服务端根因与 Token 热生效都不生效）。
> **当时的待用户决策清单（★ 除④演示站外全部已在本批 〇-LL 落地）**：①本批是否部署主站（三件齐上）→ **已部署**；
> ②文档提交是否迁到永不推送的独立分支 → **已建 `docs-local` + `scripts/push_code_only.sh` 双保险**；
> ③`web_old.*` 41 份归档是否清理 → **已裁至最近 4 份，并补日志轮转根治大头**；
> ④演示站落后 6 个批次是否补部署 → **未选，仍未部署**；⑤assist `kb_entries` 关键词手工同步 → **已用 `scripts/assist_kb_sync.py` 收口**；
> ⑥浏览器扩展（`extension/`）至今无交付渠道 → **已建交付链并随 dist 上线**。

### 〇-LJ、即时翻译工作台形态定档批（2026-09-22，★ 代码已推送 43da5aa·主站已部署（仅换源 `/opt/translator/web`，`translator-server`/`translator-assist` 两二进制未替换、服务未重启）·演示站未部署）

> 来源与本批的**一次方向误判**（如实记录，防再犯）：用户 09-22 反馈「我原来的即时翻译气泡对话框去哪里了，
> 怎么变成这种输入然后下方出结果的样式了」→ 我把随后的「**会退形态，保留功能**」读成「回退到 #36 之前的
> 两段式」，据此提交 `8d88313`（吸顶输入卡 + 下方气泡）并推远端、部署主站；用户看线上截图判**方向完全反**：
> 「我原来是 ai 对话框一样，一整屏组合起来的（除了页眉页脚），你现在在搞分步的」——要的就是 #36 那套
> **整屏单框**。同日以 `43da5aa` 改回单框并**定档**。
> **教训**：形态类口语指令（「回退」「原来的」）方向歧义极大，动手前必须先用一句话复述目标形态并向用户确认，
> 或先出截图对照；本批往返成本 = 两次全量前端闸门 + 两次主站换源。

| 块 | 交付 |
|----|------|
| **定档形态（`ChatWindow.tsx`）** | 除页眉页脚外**一张吃满整屏的对话框 `.cw-dialog`**（`flex:1 + min-height:0`，卡面 #0E1014 + 描边 + 阴影），内部三段：**框头**（原文标签 / 自动检测 / 会话搜索·导出 Markdown·清空）→ **滚动区 `.cw-dialog-body`**（原文 textarea 与译文结果气泡**同框**、框内滚动，输入不再独占一张卡）→ **框脚 `.cw-dialog-foot`**（目标语言多选 + 已选 chips、专业校对/快速、缩翻、翻译/停止主按钮，常驻不随滚动消失）；`autoResize` 上限按视口 40%、520px 封顶（合并框内给气泡留可读空间）。中间态 `.cw-stage/.cw-card/.cw-results` 三段类名**已全部撤除**，`theme.css` 与组件注释同步标注「勿再复活」 |
| **功能保留清单（两次往返一项未减）** | 会话搜索·导出 Markdown·清空、缩翻 `max_length` 接文本链路、字数预估与余额/组织预算条、目标语言多选与 chips、气泡上下文（assistant 取上一条 user 为 source）与反馈入口、离线横幅与重试、专业校对/快速双模式；**文件翻译入口维持 #36 的下线决定**（统一走「文档翻译」工单页，工作台仍零 `input[type=file]`） |
| **三层形态锁回到单框口径** | ① `ChatWindow.dom.test.tsx` ①：输入框与气泡都必须在 `.cw-dialog` 内、且同在其滚动区 `.cw-dialog-body` 内（旧两段式锁的 `.cw-stage`/`compareDocumentPosition`/内联 `position:sticky` 断言一并撤）；② `pixel_uat.spec.ts` P2b 恢复「工作台合并对话框结构」标题，锁 `.cw-dialog-body textarea` 与 `.cw-dialog-foot` 可见、全站零 `input[type=file]`，字阶 13/14、真值灰阶集合、顶栏 38px 高等**等值锁原样保留**；③ `mobile_uat.spec.ts` 等待选择器回到 `.cw-dialog`。截图产物名回到 `p2b_workbench_merged.png` **（★ 本行的「输入框在滚动区内」已被 〇-LK 改向：用户看后判「你们家对话框是放顶部的啊」，输入区现落在 `.cw-dialog-foot` 框脚，DOM 与三层锁以 〇-LK 为准）** |
| **文案十二语种同步** | `chat.welcomeSub` 回到「**译文会直接显示在这个对话框里**，支持 40+ 语言互译。」，`panels/chat.ts`（zh/en）+ 10 份 `locales/*.ts` 共 12 语种一次改齐（中间态「显示在输入框下方」口径全部作废） |

> 闸门（两次往返各自全绿，2026-09-22）：`tsc --noEmit` 干净；vitest **47 files·344 tests**；`vite build`
> 两段式态 → `index-DE2xH-NT.js`，单框定档态 → `index-yT1ZziZi.js` / `index-DtdiKHPp.css`；
> `UAT_SKIP_RACE=1 bash scripts/uat/run_uat.sh`（PG 方言）两度 API A/B **96/0**、交易专项 T **510/0**、
> **Playwright 55 passed + 1 skipped**（P2b 分别以两段式锁、单框锁通过，0 flaky），`RUN_UAT_EXIT=0`。
> ⚠️ 本轮曾遇**本地 PG 未运行**致 `run_uat` 建库失败（`RUN_UAT_EXIT=1`，非代码问题）：`brew services start postgresql@15`
> 后复跑全绿——PG 矩阵前置检查应看 `pg_isready`，别把建库失败当回归。
> 本批为纯前端形态批，未碰 `backend-go/`，故后端单测与 assist 侧不重跑（〇-LI 已全绿）。
> **发布链**：① 误向提交 **8d88313**（16 代码文件）推送时，因 `autosales` 线性单分支上排在其前的两份「仅本地」
> 文档提交（`d9ae957`、`6a68c25`）与并轨 merge（`7acefe3`）是它的祖先，被连带推上远端——**「文档不外推」被祖先链击穿**；
> 不改写已推送历史（force-push 需用户明令）。② 修正提交 **43da5aa** 已按正确手法推送：在 `origin/autosales` 之上
> cherry-pick 出纯代码提交（`git diff --name-only origin/autosales..tmp` 实核 16 文件、**零 .md**）后 push，
> 再 merge 回本地并轨——本批两份文档提交（`0f91afc`、`efae6b8`）留在本地侧。
> **根治口径（待用户定）**：要长期「文档不外推」，文档提交必须放到永不推送的独立分支（如 `docs-local`），
> 或每批严格「代码提交 → push → 才提交文档」；否则按「文档随代码入远端」处理，不再写「仅本地」。
> **主站发版（同日两次换源，第二次即修正）**：均为纯前端批按 §五 口径——tar 上传（远端/本地 sha256 一致）→
> 解到 `web.new`（22 文件，校验首页引用）→ 两步 `mv` 换名。
> ① 22:31 上的是误向两段式（`index-DE2xH-NT.js`、chunk `ChatWindow-DZZTO-Wo.js` 内 `.cw-stage` 命中、`.cw-dialog` 0），
> 旧目录留 `web_old.20260922_223134`；`deploy_check.sh` 内网 **11/11**、公网 **8/8**。
> ② 23:00 上定档单框（sha `1f03c885…`、`index-yT1ZziZi.js` / `index-DtdiKHPp.css`、chunk `ChatWindow-Dfkjl_KQ.js`
> 内 `.cw-dialog` 命中 12 处、`.cw-stage` **0 命中**），旧目录留 `web_old.20260922_230047`；
> `deploy_check.sh` 内网 **11/11**、公网 **8/8**；**两二进制全程未替换**（`translator-server` 仍 `fa2f8ca0…`、
> `translator-assist` 仍 `798d420c…`）、`systemctl` 未重启，换源前后两服务恒 `active`，`/office/taskpane.html` 200 未受影响。
> 期间用户报「页面直接打不开」一次：`curl` 线上 `/`、JS、CSS 均 200 且服务恒 `active`，判为换源那一两秒窗口或
> 浏览器/CDN 缓存旧 `index.html`（**处置：硬刷新 Cmd+Shift+R**）；若再复现需带具体页面与时间点排查。
> `/opt/translator/web_old.*` 累积 **41 份**（磁盘 66%、余 14G）。**演示站按前令未部署（版本差再扩两批次）**。
> **待用户决策（本批未擅动）**：①文档提交是否迁到永不推送的独立分支（上文口径偏离的根治办法）；
> ②`/opt/translator/web_old.*` 41 份归档是否清理；③演示站落后 5 个批次是否补部署；
> ④assist `kb_entries` 关键词手工同步（★ 已闭：〇-LL 用 `scripts/assist_kb_sync.py` 同步）；⑤浏览器扩展（`extension/`）至今**无任何交付渠道**（无打包脚本、无托管 zip、manifest 仍 1.0.0），是否补发版链（★ 已闭：〇-LL 建 `scripts/build_extension.sh` + 托管 zip + 后台下载卡，manifest 升 1.1.0 并随 dist 上线）。


### 〇-LI、白色两档定档与后端/扩展渲染面还原批（2026-09-22，★ 代码已推送 5f04be5·文档当时仅本地提交（★ 后随 〇-LJ 的代码 push 被祖先链带入远端，见 〇-LJ 发布链）·主站已部署（translator-server + translator-assist 两二进制 + web 换源）·演示站未部署）

> 来源：〇-L 之后用户仍判「白色不纯、偏蓝显脏」。逐像素取证把根因定位到两类，而不是第三轮提亮：
> ① **白色两档被混用**——主按钮 / 主 CTA / 反相白卡 / 徽标 / 用户气泡 / FAB 这些"实心白件"取了
> 文字档 **#E7E9EA** 做整块填充（纯黑底上读出来就是冷灰）；
> ② **四类「前端闸门扫不到」的渲染面**从未进过还原范围——后端直出的 /openapi/docs 与
> /office/taskpane.html 仍是 Google 蓝 #1a73e8 / indigo #1a237e 浅底，assist 内嵌管理台仍是蓝靛
> #2f47f5 浅底，浏览器扩展 popup + 划词气泡仍是靛蓝主色 + 绿色成功态。
> **文档矛盾裁决**：《UI-ANNOTATIONS》§1.1 把「主按钮底」写在 `--lc-text-1` 行内，与同文档
> §3.1-05「主按钮 面 #FFFFFF」自相矛盾——以交付包 `react/css/components.css`
> `.lc-btn--primary{background:#FFFFFF}` + 零偏移截图（05-pricing / 06-marketing-home）逐像素
> 直方图为准：白底件全部实测 #FFFFFF，#E7E9EA 在图里只出现在文字行。**文档 prose 让位于实证**。
> （取证注意：07/08/09 是整体 +20 亮度抬升的导出产物，拿它取色会把灰值当白值。）

| 块 | 交付 |
|----|------|
| **令牌定档** | `tokens.css` 新增实心白档 `--lc-fill-white:#FFFFFF`（文档口径别名块内），并把 `--lc-text-1` 注释里误写的「主按钮底」改为「正向活跃态（小控件与指示条）」 |
| **前端实心白件归位** | Landing 主投按钮 / 反相专业卡 / 收尾 CTA / logo 方块、PricingPage 首月徽标、AiAssist FAB + 发送键 + 用户气泡 + `.na-act:hover`、theme.css AI 头像 / 语种徽标 / DOCX 图标、ErrorBoundary 重试按钮，一律 `#FFFFFF`/`var(--lc-fill-white)` + 黑字。**合法留在 #E7E9EA 的**：进度条填充、光标、审阅态小胶囊、封段徽标、WordSwap 光标、复选框/开关/Tab 活跃胶囊（正向活跃档，非整块填充） |
| **后端直出页还原（§1.1 单色纯黑）** | `public.go` /docs 外壳重画（header 56 高、品牌 15/700 #FFFFFF、导航项 12/500 #9AA0AA + 活跃 `a.on` #FFFFFF、管理后台=白底黑字主按钮、面板 #0E1014 + 1.2px #3A404C + r14 + 顶缘受光、页脚 #050607/#536471，新增 `docNavLink` 点亮当前页）；`admin_openapi.go` /openapi/docs 原先**两处重复 CSS** 收敛为共享常量 `openAPIDocsCSS`（goldmark 壳与中英双容器页同源），代码块 #0A0B0D/#0E1014、语言切换活跃档白底黑字、链接取主文字不再取蓝；`office.go` Word 任务窗格改深底 + 纯白主按钮 + 描边次按钮，绿色成功态废止 |
| **assist 内嵌管理台还原** | `internal/assist/web/admin.html`（`go:embed`，路由 `/assist/admin`）`:root` 换 §1.1 令牌：面 #000/#0E1014/#16181C/#0A0B0D、文字三档、描边 1.2px、卡片顶缘高光；主按钮 `button.pri` 白底黑字；Tab 活跃改「次级卡面 + 主文字」；LLM 接入态三档改 主文字/次级/警示琥珀（绿蓝底全废）；Token 验证态与对话框遮罩（72% 黑）同口径 |
| **浏览器扩展还原** | `extension/popup.html` 靛蓝主色→纯黑单色（主按钮白底黑字、输入走 #0A0B0D + 1.2px 描边、成功态不标绿、select 内联样式并入统一规则）；`extension/content.css` 划词圆钮→纯白实心件（与主站 FAB 同档）、结果气泡→#0E1014 面板 + 1.2px #3A404C、术语高亮自造 #FFD54F 归位 §1.1 警示琥珀 #D29922 |
| **真值锁（四类闸门各补一处）** | ① `readability.test.ts` 新增 **G 段**：白底件逐点等值（11 点 + ErrorBoundary 内联特例）+ 按钮/CTA/徽标/气泡类选择器禁取 #E7E9EA/`var(--lc-text|-text-1|-success)` 做背景的负向锁（CSS-in-JS 选择器需规范化反引号，末级裸元素 i/span/svg 豁免）；新增 **H 段**：扩展 popup/content.css 旧靛蓝·浅底·绿族清零 + 取 §1.1 令牌。② 新建 `backend-go/internal/api/public_ui_test.go`：/docs 三页 + /openapi/docs + office 窗格逐页断言，外加 **`TestAllServedHtmlPagesMonochrome` 全量扫描**——凡源码含 `<!DOCTYPE html` 即进射程（这个盲区已被发现四次，逐页点名必然再漏）。③ 新建 `internal/assist/web/admin_ui_test.go`：assist 管理台令牌等值 + 主按钮白底 + 遮罩 72% 黑，扫描前先剥 `/* */`、`<!-- -->`、`//` 注释（本仓「旧值 → 真值」说明注释里全是历史色值，不剥就是命中注释自己的老坑）。④ `pixel_uat.spec.ts` 新增 **P6b**（营销页主投/收尾白块运行时 `getComputedStyle` = rgb(255,255,255)）、**P6c**（/openapi/docs 纯黑底 + 语言钮白底；/office/taskpane 走 fetch 校样式壳，避开 Office.js 外网依赖） |
| **闸门实跑** | `go build`/`vet`/`go test -race ./...` **GO_TEST_EXIT=0**（40 包含测试全过、零 FAIL 零 DATA RACE）；`tsc` 干净；vitest **47 文件 / 344 用例**（本批 +20：G 段 15 + H 段 5）；`vite build` 成功（`index-yT1ZziZi.js` / `index-DtdiKHPp.css`）；`run_uat.sh` PG 主矩阵 **RUN_UAT_EXIT=0**：A/B **96/0** + T 套件 **510/0** + Playwright **55 passed / 1 skipped**；`assist_uat.sh` **38/0**；`multi_instance_e2e.sh` **8/0**。本地预览实测 `.lc-mkt-btn--pri`/`.lc-cta`/`.lc-plan--pro` = rgb(255,255,255)、body = rgb(0,0,0) |
| **发布链** | 代码提交 **5f04be5**（16 文件，零 .md / 零流程图）已推送 `origin/autosales`：本地曾排在两份「仅本地」文档提交之后，为守住「文档不外推」，在 48c8c22 上 cherry-pick 出纯代码提交后推送，再 merge 并轨（rebase 改写历史被自动化安全闸门拦下，故用 merge，不改写已推送内容）。文档更新只本地提交〔★ 更正：这两份「仅本地」文档提交已随 〇-LJ 的代码 push 进入远端，见 〇-LJ 发布链〕 |
| **主站部署与线上验收** | 2026-09-22 21:19 两件二进制 mv rename 替换（`translator-server` `fa2f8ca0…` / `translator-assist` `798d420c…`，旧件留 `.bak.20260922_211905`）+ `/opt/translator/web` 两步换源（`index-yT1ZziZi.js`/`index-DtdiKHPp.css` 首页已引用，旧目录留 `web_old.20260922_211951`），`deploy_check.sh` 内网 **11/11** · 公网 **8/8**。线上四渲染面令牌实测：/docs 三页与 /openapi/docs 旧色 **0 命中** + `--lc-bg:#000000`、/office/taskpane.html 走 `var(--lc-bg)`/`var(--lc-white)`（唯一"命中"落在我自己写的历史色注释里，非样式值）、CSS 已含 `--lc-fill-white: #FFFFFF`。⚠️ **验收时抓到一处真缺陷**：assist 管理台页面换二进制后线上仍旧配色——生产 `ASSIST_WEB=/opt/ai-assist/web` 让**外置 09-17 旧页覆盖 `go:embed` 内嵌新页**（外置优先，`internal/assist/api/server.go` adminPage）。当日两步 `mv` 同步外置 `admin.html`（留 `admin.html.bak.20260922_212841`）+ `systemctl restart ai-assist`，公网 `/assist-api/assist/admin` 复测 200 / 19139 字节、`--bg:#000000`·`--white:#FFFFFF`·`background:var(--white);color:#000000` 齐、旧配色 0 命中。口径已入 AGENTS §5 与《部署指南》§十三：**今后碰 `internal/assist/web/*`，只换二进制不够，必须同步外置页或撤销 `ASSIST_WEB`。**同日 21:45 收尾（用户下令撤销）**：`secrets.env` 的 `ASSIST_WEB` 行改注释（备份 `/root/ai-assist.secrets.env.bak.20260922_214541`）、外置旧页挪走留档 `/opt/ai-assist/web/admin.html.inert.20260922_214541`、`systemctl restart ai-assist`。复测：进程 env `ASSIST_WEB` 计数 0、`/assist/admin` 由 `go:embed` 直出（本机与公网 sha256 `a9c3495e…` 逐字节等于仓库 `internal/assist/web/admin.html`）、`/health` ok、journal 零 error、挂件链路通过（greeting 签发 tok → chat 出 reply，无 tok 401）。**内嵌页自此为单一事实源，改 `internal/assist/web/*` 只需换二进制**；`ASSIST_SEED` 仍保留（只影响首启灌 seed） |

> 遗留（★ 前两项已由同批 〇-LL 收口：`assist_kb_sync.py` 同步完 3 行 kb 关键词、`web_old` 裁至最近 4 份并补 logrotate，磁盘 66%→39%）：生产 `ai-assist` 库 `kb_entries` 关键词人工同步（承接 〇-XLVI/XLVIII）；`/opt/translator/` 下 `web_old.*` 归档实测已 **39 份 / 67M**（磁盘 66% 用、余 13G）待清理决策；演示站按前令未部署（版本差再扩一批）。已闭：assist `ASSIST_WEB` 外置覆盖按用户下令已撤销（内嵌页为单一事实源）。

---

### 〇-L、全站 UI 交付真值还原批（2026-09-22，★ 代码已推送 48c8c22·主站已部署（纯前端批，仅换源 `/opt/translator/web`，两二进制不替换、服务不重启）·文档仅本地不推送）

> 来源：用户指令「很多页面不是纯白而是偏蓝，白色显脏，跟 UI 完全不一样」→ 扩大为
> 「不光色值，还有粗细、间距，所有前端的东西全部还原 UI 设计（除多语言特性导致的除外）」。
> 根因不在这批改动本身，而在**三批先后叠加的"可读性提亮"**：09-18 组件库令牌整体上提一档、
> 09-21 #35 页面级 +1px 字号与次级灰提档、09-22 #67/#68 顶栏控件放大与描边提亮。
> 三批各自都有"只准更大更亮"的单向测试锁兜底，结果是把设计一路推离交付稿——
> **单向锁是本轮返工的制度性成因**，故本批把它整体换成"必须等于真值"的等值锁。

| 块 | 交付 |
|----|------|
| **颜色还原** | `ui/langcross/css/tokens.css` 令牌回到交付包逐字相等（`--lc-text-3 #71767B`、`--lc-text-4 #536471`、`--lc-border-pill #424956`、`--lc-border-card #3A404C`、`--lc-border-card-dim #2A2F3A`）；`theme.css` 删除 §十 `:root:root` 令牌提档层与 §十一 `html .app-header` 顶栏放大层（21 处 +1 字号同步回基线）；全站 39 个提亮产物字面值（#666E7C/#B0B6C2/#878D95/#7A828E/#575F6C/#191D24/#E98286…）与 `rgba(10,16,40)` 蓝调遮罩清零 |
| **字阶/字重/间距还原** | 顶栏按《UI-ANNOTATIONS》§2.2：高 38、品牌「能言」14、主导航 13 胶囊（`border-radius:999`）、活跃面 #16181C；`--lc-workbench-topbar-h:38px`；余额条按「高 20 · 11px #9AA0AA」实装（`minHeight:20`+上下 2 内边距，多语种长文案仍靠 flexWrap 换行）；汉堡图标 18、`.ss-ghost-btn` 32/14、`.ss-table`/`.ss-drawer-item` 间距回基线；字重词汇表回到 Regular/Medium/SemiBold/Bold 四档（`.brand` 800→700） |
| **测试锁改向** | `src/styles/readability.test.ts` 整文件重写为六组真值锁：A 令牌等值 19 条 + 语义绿/蓝负向锁 / B `html` 前缀覆写层禁复活 / C 顶栏几何（38 高·品牌 14·Tab 13 胶囊）/ D 禁清单 39 值 + 遮罩 / E 次级灰禁写死 / F 描边下限；`e2e/pixel_uat.spec.ts` P2b 从"实测不得小于"改"getComputedStyle 实测等于交付值"（textarea 13、气泡 14、顶栏实高 38、Tab 13/≤38、语种钮 12/≤38，且可见性先断言防 `getBoundingClientRect()===0` 假绿） |
| **多语言能力保留** | LangSelect 12 语种入口、RTL、ALL_KEYS 全量词典、WordSwap 动效、首访浏览器语言检测一律不动（用户口径「除多语言特性导致的除外」）；`playwright.config.ts` 钉 `locale:'zh-CN'`、`vitest.setup.ts` 预置 `app_lang=zh` 两处底钉不变 |
| **T55 支付渠道配置闸门**（自动化测试覆盖近日批次缺口的补项） | `scripts/uat/api_uat_txn.sh` 新增 31 条 HTTP 级常设断言：读写口鉴权（匿名/普通用户拒）/ 白名单外键整单拒收且不留半套凭据 / 开关只认 0·1·空、回调与网关必须完整 URL / 敏感项 `enc:v1:` 密文落库 + 掩码回显 + 响应零明文 + **掩码原样再提交不覆盖真密文**（同批其它字段照常保存）/ **库配置端到端流到下单链路**（已填项不再被渠道侧缺项清单点名、未填项仍被点名）/ fail-closed 不出 `mockpay://` 假码 / `enabled=0` 时「已停用」优先于「未配置」/ 保存进审计 / 空串=清除删行。env>DB 优先级需注入 `PAY_*` 环境变量、跑中途改不了 UAT 服务环境，仍由 `pay_channels_test.go` 进程内覆盖，两侧互补 |
| **闸门实跑** | `go build`/`vet`/`test -race ./...` 全绿；`tsc --noEmit` 干净；vitest **47 文件 / 324 用例**全绿；`vite build` 成功；`run_uat.sh`（PG 主矩阵）**RUN_UAT_EXIT=0**：A/B 96/0 + T 套件 **510/0**（本批 +31）+ Playwright 53 passed/1 skipped；`assist_uat.sh` 38/0；`multi_instance_e2e.sh` 8/0 |
| **主站发版** | 纯前端批：`translator-server`/`translator-assist` 两二进制**不替换**、`systemctl` **不重启**（静态目录整目录替换对运行中进程无影响）；先解到 `web.new` 校验首页引用、再两步 `mv` 换名（少一个 404 窗口），线上资产 `index-VzNqyYQ_.js`/`index-B65eZ4VL.css`，旧目录留 `web_old.20260922_192828`；`deploy_check.sh` 内网 **11/11** + 公网 **8/8**；线上 CSS 实测四真值令牌在、`html .app-header`/`:root:root` 覆写层零残留、12 个提亮字面值与蓝调遮罩零命中、`body` 实算背景 `rgb(0,0,0)`。演示站按前令未部署 |

> 遗留（★ 已由 〇-LL 收口）：生产 `ai-assist` 库 `kb_entries` 关键词仍需人工随术语批次同步（非本批范围）。

---

### 〇-XLIX、国际收款就绪批：支付渠道可配 + 续费宽限期 + 多币种报价（已封存）（2026-09-23，★ 代码已推送 66452cd·文档本地·主站已部署 2026-09-22，演示站未部署）

> 来源：国际版路线（12 语种已上线后的商业化缺口）。#74 解决「凭据改一次要登服务器」与
> 「自动续费扣款失败当天就摘身份」；#75 解决「海外客户只看得到人民币价」。
> 编号口径：#74=支付渠道凭据管理台可配 + 订阅续费宽限期；#75=多币种报价。
> ★ 2026-09-22 用户裁决（本批收尾时确认）：收单能力只有微信/支付宝（人民币）与币安钱包
> USDT 两条，**#75 多币种报价关闭封存**（"保留国内 CNY 口径、海外 USDT 口径，其他做关闭，
> 后续有能力了再打开"）——机制代码全保留、总开关 `store/currency.go` `quoteFeatureOpen=false`
> 钉死关闭态，USDT 走既有独立 `usdt_*` 口径与本批无冲突；#74 两块默认缴收不受影响。

| 块 | 交付 |
|----|------|
| **#74a 支付渠道凭据管理台可配** | `store/billing_payconfig.go`（`paych_*` 白名单键，敏感项 `internal/secret` 加密落库、掩码回显、`IsSecretMasked` 防掩码写回）+ `api/pay_channels.go`（GET/POST `/api/admin/pay/channels[/save]`，超管专属，env 接管项回显标注）+ 前端 PlansP「运营配置」新增商户参数区块（被 env 接管的栏位置灰标变量名）。`payment/gateway_sdk.go`/`payment.go` 改读该配置链（env > 库 > 默认） |
| **#74b 订阅续费宽限期** | `store/renewal_grace.go`（`renewal_attempts` 唯一键 `(tenant_id,package_id,attempt_date)` 同日去重；`grace_expires_at`/`notified_grace` 走 tenants.permissions 原子覆写）+ `api/subscription_grace.go`（到期扫描：进宽限保留身份+站内信、宽限期每日补建续费单、逾期摘除换文案）+ `/api/me/package` 出 `in_grace/grace_expires`，前端宽限提示条（`PlansP.grace.dom.test.tsx` 钉本地时区日期口径）。配置键 `subscription_grace_days`/`renewal_lead_days`（见部署指南） |
| **#75 多币种报价（仅展示口径）→ ★ 已按用户决策关闭封存** | 后端：`store/currency.go`（`quote_currency`/`fx_rates` 配置 env>库>默认、12 币种白名单、倍率防呆 >0 且 <1000、`ConvertFromCNY` fail-closed 缺汇率回落 CNY；`orders` 幂等补列 `currency/fx_rate/money_cny` + 存量回填 `money_cny=amount_money`）+ `api/quote_currency.go`（超管口 GET/POST `/api/admin/config/quote-currency`，校验先行整批拒写，审计留痕）+ 出参加强（`/api/plans` 顶层 `quote_currency/fx_rates_snapshot`、每行 `price_cny/price_display`；`/api/me/package` 同口径；老字段一删不存）+ 五类建单点（subscribe/upgrade/pay-create/自动续费/后台代建）金额最终确定后统一 `stampOrderQuote` 落快照。**★ 关闭态实现（2026-09-22）：`store/currency.go` 总开关 `quoteFeatureOpen=false`——`QuoteCurrencyCfg` 读口短路恒回 CNY（残留库配置/env 都压不住）、`ValidateQuoteCurrency/ValidateFxRates` 拒收一切外币键、`SupportedQuoteCurrencies` 只露 CNY ⇒ 管理台报价区块与 C 端外币渲染自动整体消失；快照列/迁移/审计/接口契约原样保留（历史单不漂）。重开=翻转常量+还原 T54 开放态断言，其余零改动。** 语义红线不变：收单与实扣恒为人民币；海外收款唯一口径是既有 USDT 链（`usdt_*` 配置，与本开关无关）。前端：`quoteFmt.ts` + `/pricing` 与商店卡外币渲染分支 + 收银台「约合 X」行（关闭态下这些分支因出参恒 CNY 不触发）+ 管理台报价区块（以「白名单含外币币种」为渲染条件）；`billing.quote*` 8 键 × 12 语种词典保留（locales.core 全量闸门仍绿） |
| **测试** | 后端：`store/currency_test.go`（A–D 开放态用例显式翻开关 + **F 组关闭态红线：读口短路/写口拒收/env 压不动/换算 fail-closed/白名单仅 CNY**）、`api/quote_currency_test.go`（开放态往返 + **关闭态 400「暂未开放」/feature_open=false/plans 恒 CNY**）、`store/billing_payconfig_test.go`、`api/pay_channels_test.go`、`api/renewal_grace_test.go`、`store/renewal_grace_test.go` 等；前端：`PlansP.quote.dom.test.tsx`（回显/env 置灰/载荷拦截/商店卡双币/**关闭态区块隐藏** 6 用例）+ `quoteFmt.test.ts`（CNY 老口径逐字不变等 3 条）+ `PlansP.grace.dom.test.tsx`；UAT：`api_uat_txn.sh` 新增 **T53（#74 宽限期全链，含对照组）**、**T54（★ 关闭态口径重写：配置口鉴权不放水/外币保存拒收/CNY 表态放行/直插残留外币配置不生效/plans·快照恒 CNY 且 money_cny=amount_money 双写——快照金额=首月半价折让后实付 10.8，锁「快照跟随最终应收」，16 断言）** |

> 闸门（2026-09-22 六步链实跑，全绿）：后端 `go build/vet` + `go test -race ./...` **39 包全过**；
> 前端 tsc + **vitest 47 files·308 tests**（含 i18n 全量闸门 2746 键、quote 6 用例含关闭态区块隐藏）
> + vite build 全绿；run_uat PG：**API A/B 96/0**（本批 +2）、**交易专项 T 479/0**（T53 宽限期全链、
> T54 关闭态 16 断言；首轮 T54 两条 FAIL 定位为断言期望未算「试运营首月半价」——新租户订 paid 包
> 实付=挂牌五折 10.8，报价快照落在折让之后恰证「快照跟随最终应收」，断言已按 10.8 修正并重跑全绿）、
> **Playwright 53 passed + 1 skip**；assist_uat **38/0**；多实例 **8/0**。
> 发布链：代码提交 **66452cd** 已推送 origin/autosales（本批零文档零流程图；远端原停在 decd6b9，
> push 连带上了此前 5 个「仅本地」文档提交，属历史不可拆的既成事实）；文档更新留本地提交。
> **主站发版（2026-09-22，用户指示前后端都部署、演示站按前令不动）**：`translator-server`
> sha256 `4f7329bc…` mv rename 替换（留 `.bak.20260922_161421`）+ `/opt/translator/web` 换源
> （`index-BdjoUL1T.js` 线上生效，留 `web_old.20260922_161421`）；本批未碰 assist 侧代码，
> **translator-assist 不替换**。deploy_check 内网 11/11 + 公网 8/8（首跑 1 项 000 为链路抖动假红，
> 复跑全绿；教训：内网 base 必须用业务口 **127.0.0.1:8787**，18787/18788 是附加监听）；
> 重启后 journalctl 零 panic；线上封存口径实测 `/api/plans` quote_currency=CNY、
> fx_rates_snapshot 仅 CNY、price_display==price_cny——多币种报价关闭态生产生效。

### 〇-XLVIII、任务系统与评估报告缺陷批（2026-09-22，代码提交 106ce50·decd6b9 已推送 origin/autosales·文档仅本地·主站已部署，演示站未部署）

> 来源：《全量架构与商业价值评估_20260920.md》《全量UAT实测与架构商业评价_20260921.md》
> 《缺陷记录_工单T20260921075004EF8_md标题中文未译_20260921.md》三份文档的缺陷/缺失整合，
> 加用户 2026-09-20 原文需求 #33–#36。以下为本批实际交付（对外零 token 裸值口径不变）。

| 块 | 交付 |
|----|------|
| **任务系统（#33）** | `internal/store/task_rewards.go`（去重占位 → 周期计数 → 租户校验 → 临时积分入 `quota_grants`（可叠加到期）/永久积分入余额，发放与流水同事务）+ `internal/api/task_hooks.go`（登录/发起翻译/邀请充值/知识库解析四个自动触发点）+ 前端「任务中心」`TaskCenterP`。数值按用户原文：每日登录 100（3 天有效、日叠加）；每周发起翻译 100（周 ≤5 次、日 ≤1 次、7 天、周叠加）；邀请注册 500（14 天可叠加）；邀请且任意充值 1000 **永久**；自建知识库解析成功 600 **永久**一次性；超管「重置已订阅全部用户消耗量（有效期不变）」特殊动作。与 2026-08 旧「任务中心」（超管自定义每日/一次性任务）共存：旧表 `user_tasks` 走永久 token 台账，新 `user_tasks` 种子 + 周期口径由 `bumpTaskCounter` 管 |
| **后台 AI 助手前端重做（#34）** | `components/admin/AssistP.tsx` 重做（条目 CRUD/启停/Token/嵌入页五 tab，`data-testid="assist-tabs"`）+ `src/api/assistAdmin.ts`；E2E `e2e/assist_admin.spec.ts` A1–A6 全绿，后端 assist_uat 38/38（前后端真链路，非 mock 面板） |
| **字号与对比度（#35）** | 登录后前后台正文/表格/输入控件字号阶梯上调 + 文本对比度按 WCAG AA 校（`src/styles/readability.test.ts` 锁住最小值），页脚死链与假复制按钮等同批实装 |
| **即时翻译对话框合并（#36）** | 输入框与结果气泡合并为一个占满页眉页脚下方整区的对话框〔★ 该**整屏单框形态**已在 〇-LJ（2026-09-22）经一次误改（两段式 `8d88313`）后由用户判定档保留，见 〇-LJ〕 |页眉页脚下方整区的对话框；**即时翻译不再支持文件翻译**（文件走工单），提示词与 UI 里的文件翻译话术同步清除，缩翻 `max_length` 改接文本链路保留 |
| **文件管线保真 RC-1~RC-6（工单 T20260921075004EF8 原件/译文逐条比对）** | ①表格分隔行入翻译表 ⇒ 内部提示词泄漏进交付物（9 处，P0）②回显/同文判定统一收口 + KB 命中补同文守卫（P0）③`emphasisRe` 只回填 `$1` ⇒ 行内标记与内容被删（RC-5，P0）④表格行降级为散文 ⇒ 按单元格骨架逐列替换（P1）⑤译文含换行破坏行对齐 ⇒ 产物与原文行数 1:1（P1）⑥围栏内容三类分派（RC-3）⑦**产物文件名翻译（RC-4，原为零实现）**⑧目录锚点同步（RC-6）⑨结构指纹保真闸门 + 未译段清单透出（P1，漏译不再静默通过）⑩md 结构前缀支持无空格写法（RC-1） |
| **评估报告缺陷批** | #37 错误脱敏（对外 message 收敛 `errmsg.go` + `publicErrMessage`）/assist 硬编码 token/SDK Node22/401 回跳；#38 TMX 导出导入 + SCIM/SSO 配置界面接线；#39 落地页价格改走 `/api/plans` + 对账定时化 + 计量 sink 落盘 spool（停机不丢账）；#40 Redis 由隐形强依赖改显式（`REQUIRE_REDIS=1` fail-closed）+ 工单扣费事务 + 限流文案；#41 自动续费开关（`store/autorenew.go` + 订阅页）与优惠券（`coupons*`）；#42 探针拆分 `/livez`(恒 200) 与 `/readyz`(真探依赖)、gofmt 门禁、`errorstyle_gate_test.go`（writeJSON 基线 741 只减不增）、`route_auth_gate_test.go` 公开路由白名单、`archguard` 分层守卫；#43 主流程 E2E `translate_flow` + a11y 焦点陷阱（`focusTrap.ts`，Dialog/Drawer） |
| **★ 本批抓到的真缺陷（P1）** | **即时翻译 SSE 收尾不冲刷计量缓冲**：`/api/chat/stream` 全程不调 `billing.Flush()`（非流式 `/api/chat`、账单接口、文件流都有），用量留在内存缓冲等 2s ticker 落库，而前端在 done 帧后只刷新一次余额条 ⇒ 稳定读到旧值，用户表现为「翻译完余额/今日已耗不动，再操作一次才跳」。修法在终帧之前 Flush（放 handler 末尾仍是竞态）。快速锁：`api_uat.sh` 新增 A7s（done 后不 sleep 立读 `points_used_today` 必须增加）；界面级锁：E2E TF2。同批修 RC-4 提示词残留进交付文件名（`file_name.go` 加 `isFilenameEchoResidue` 结构闸，M1 保持原断言不弱化） |

> 闸门（2026-09-22 串行全绿）：`go build/vet` + `gofmt` 零红灯 + race 单测；前端 tsc + **vitest 43 files·280 tests** + vite build；
> assist_uat **38/38**；run_uat PG：**API A/B 94/0**（新增 A7s 两条）、**交易专项 T 436/0**、**Playwright 54/54**（0 flaky，含 TF 主流程与 A1–A6）；
> 多实例 **8/8**。i18n 闸门计数：取词字面量引用 2386 / 静态跳过 57、审计动作码存量 33、十语种全量 2532 键。
> 待用户决策未擅动（见任务 #65）：①交付物文件名带内部纳秒时间戳前缀（需先做每文件独立产物子目录才可安全剥离）；
> ②超管无租户时任务奖励发放按 WARN `bad_task` 刷屏（语义应为 `no_tenant` @ Info）。
> 仍阻塞：#41 微信/支付宝真实验签待商户凭证与资质（P1-1）、发票与多币种待开票资质（P1-2）；#59 store 层 ctx 穿透为已批准延期项。
> 报告口径修正两项：评估报告称 `/api/billing/usage` 为僵尸路由——**不属实**，用量看板与 CSV 导出仍在消费；`/api/billing/balance`、`/api/billing/config` 已标 deprecated 但仍在线（删除需用户确认，未擅动）。`plans_api.go` 的「今日已耗」查询实际取月初时间戳（口径瑕疵，展示值偏大），列为待决策。
>
> **部署（2026-09-22 07:21，主站三件同批）**：`translator-server` sha256 `e1210e26…`、`translator-assist` sha256 `f545d463…` scp→`mv` rename 替换（旧二进制留 `.bak.20260922_072155`）+ `/opt/translator/web` 换源（新资产 `index-CfLqzgP_.js`，旧目录留 `web_old.20260922_072155`），两服务 restart 后 active、journalctl 零 panic。验收：`deploy_check.sh` 服务器本机内网 base **11/11**（`/livez` 200、`/readyz` `{"status":"ready","store":"ok","distributed":"in-process"}` 且无拓扑泄露）、公网 base **8/8**；venv `PDF_LIB_OK`/`ANYDOC_OK`、四管线脚本齐备未变；`user_tasks` 五类出厂任务随启动幂等入库（对外 100/100/500/1000/600 积分，内部 token 30000/30000/150000/300000/180000）；assist 令牌链路线上实测（greeting 签发 sid+tok → chat 命中充值引导；伪造 sid 无 tok 返回 401）。**演示站按前令未部署，版本差再扩一批次。**

### 〇-XLVII、全站十语种全量词典（2026-09-21，提交 47c0ff0·已推送 origin/autosales·主站已部署，演示站未部署）

> 来源：#32 全站十语种——把 〇-XLV 的「核心 453 键部分词典 + 长尾键 lang→en→zh 回退」口径升级为
> **十语种 × ALL_KEYS 2532 键逐键全量覆盖**（该 453 键口径自本条起作废）。
> 生产：`frontend-react/src/i18n/locales/{ru,fr,ar,es,pt,de,ja,ko,th,zh-hant}.ts` 全部重写为全量词典
> （中文文件头注释、无 default export）；术语按已发货 core 词典基准逐语种对齐（积分=créditos/points/
> Punkte/кредиты/ポイント/포인트/คะแนน/نقاط/積分 等，文件→檔案仅 file 义），禁盲替（fr créditer 动词义误替已回滚）。
> 管线固化在 `~/i18n_persist`（src 分片/raw/out/gaps + merge_locales.py 校验闸门 + FIXUPS.md 修复台账；
> 曾因 /tmp 清空丢过中间产物，**禁用 /tmp**）。
> 闸门升级：`locales.core.test.ts` 改全量口径——①键数逐语种 ==2532 且无越界键；②非空 + `{placeholder}`
> 与英文源逐键一致；③非 CJK 七语种零汉字残留扫描（豁免英文源本身含汉字的键，如「极石」）；
> ④zh_hant OpenCC 不变式扫描（`cc.convert(v)` 须等于 v，臺/台、覈/核 豁免）。
> 口径升级吸收回退类断言（既有经验再验证）：Landing.dom ⑧（ru 落英文）改判俄语文案、e2e landing_i18n
> fr 用例改判 'Essayer gratuitement'；新增 `e2e/all_langs_full.spec.ts` 十语种整页抽查（每语种落地页
> 出该语种译文 + 导航无中文残留，10/10）。
> 附带修正：`usage.trendTip` 英文源残存中文「积分」→ '{date}: {val} credits'。
> 闸门全绿：后端 race 30 包 / tsc + vitest 181 + vite build（主块 gzip 609.93 kB 为十余份全量词典，
> 属预期体量）/ run_uat PG：A88·T326·E2E 45（35+10）/ assist_uat 38 / 多实例 8；另 Playwright 浏览器
> 十语种真机抽查 11/11。
> 文档同步：README §界面多语言/§测试 改全量口径与 45/181 计数；AGENTS.md §5 i18n 条款改 ALL_KEYS 全量。
> 部署（2026-09-21）：本批后端零改动（bd34c36 后仅前端），主站仅换源 `/opt/translator/web`
>    （新资产 `index-QXks6lNs.js` 线上生效，旧目录留 web_old 时间戳归档），translator 重启 active；
>    deploy_check.sh 服务器本机 **8/8 全过**；生产站真机抽查 6 语种落地页（ru/fr/es/de/ja/zh_hant）
>    全部出该语种译文且无中文残留。演示站按前令未动，与主站版本差再扩一个批次。

### 〇-XLVI、用户反馈四项 + 助手复合意图修复（2026-09-20，提交 bd34c36·主站已部署，演示站未部署）

> 来源：用户反馈①–④ + AI 销售助手「印度语能翻译吗，一个字多少钱」漏接报告。
> ①② 落地页质量数字口径改写：qs3「逐段」→「质量可验证」、qs4「几十万」→「降本 80%–90%」（zh/en 同步，
>    Landing.dom 断言锁新旧文案）；③ WordSwap 加载动效整体上调一档（大档 22/26px）+ PageLoading
>    minHeight 100dvh 撑满首屏真居中；④ 外国人可读：落地页顶栏挂 LangSelect（12 语种）+ 首访按浏览器
>    语言自动选 UI 语种（detectBrowserLang，中文系分简/繁、其余落英文；land.* 长尾键 lang→en→zh 回退
>    保证非中文访客看到完整英文落地页；10 语种落地页全文案翻译留作后续）。
>    测试钉底：vitest.setup app_lang=zh、playwright.config locale=zh-CN（否则自动检测会翻红既有断言）。
> 助手修复（根因是架构不是 LLM）：话术关键词直配（Source=rule）整体绕过 LLM，「价格咨询」话术被
>    「多少钱」抢答后语言侧知识全程不参与。新增 compoundIntent 让位判定：直配命中时若知识库还存在
>    与话术关键词**零交集**的跨领域命中（不比命中数/总分——计费域运营关键词滚太大，数值对比会永久
>    压制新领域条目，即二次踩坑点），话术降为素材之一，与跨领域知识一并交 LLM 融合应答（无 LLM 时
>    fallback 并排拼出两侧）；纯单意图仍毫秒级直配不退化。seed 同步扩关键词（languages 补印度语/印地语/
>    hindi/西语/阿拉伯语/俄语等；billing-points 补一个字/按字/字数，并明确「不按字词单独计价、统一折积分」）。
> 断言锁：engine 单测 TestCompoundIntentYield（含同域大条目压制回归锁）、assist_uat CI1/CI2（2b 段）、
>    locales.core 浏览器检测矩阵（㉑）+ 冷启动回落改判 en、e2e/landing_i18n.spec.ts 两条（切换持久 +
>    fr-FR 访客落英文）。闸门全绿：后端 race 30 包 / 前端 tsc+180 测试+vite build / assist_uat 38/0 /
>    run_uat PG：A88·T326·E2E 35/35 / 多实例 8/0。
> 部署注意：seed 仅首次启动生效——生产 ai-assist 库的 kb_entries（languages/billing-points）关键词与
>    文案需经管理台 API 同步更新，否则线上仍复现漏接（二进制替换只带引擎让位逻辑）。

### 〇-XLV、流式性能增强 + 系统多语言（2026-09-19/20，已提交 ed14bc9·主站已部署 2026-09-20，演示站未部署）

> 来源：《流式与性能增强方案_20260919.md》（B1–B5 全部落地）+ 用户三合一需求（#22 留言获取方案 / #23 十二语种前后端 / #24-25 加载动效复用与多语言轮播）。
> 要点：①B1 流式双态（初译草稿+细进度共存、draft 清洗合帧）与 B3 文件逐段 SSE 上屏（sealed 即 Flush 计费）；
> ②B2/B5 对照编辑器 memo 行 + react-virtuoso 虚拟化；B4 prompt 前缀重组 + 批量租户术语层缓存；B1 观测
> cached_tokens/TTFT 仅 /metrics（零 token 裸值红线不破）；③前端 12 语种（十份 CORE_KEYS=453 键部分词典 +
> lang→en→zh 回退链 + LangSelect 唯一入口 + 阿语 RTL），后端外语→简体中文纯模型直翻（/langs 追加 zh、
> 别名去重、批量源语言检测去硬编码、OpenAPI 同权）；④落地页语言演示退役改留言获取方案；⑤WordSwap 升级
> 12 语种轮播为全站加载态唯一实现。闸门：后端 race / 前端 176 测试 / PG UAT A88·T326·E2E 33 全绿 /
> 多实例 8/0 / assist_uat 34/0。附带修复：langs_zh_test 方言污染、run_uat G3 预检钉 sqlite、
> LangMultiSelect KB 扩容重复 option 去重（e2e T3 回归锁）。
> 发版（2026-09-20，用户指示**只部署主站**）：二进制 sha256 `3e4a43e0…` mv rename 替换 + /opt/translator/web
> 整目录换源（旧目录留作 web_old），service active，三管线脚本齐备；deploy_check.sh 服务器本机 8/8 全过；
> 线上 /langs 35 条（末条 zh/kb=false）、index 引用新资产 `index-DTvE5hO7.js`（12 语种切换 + WordSwap 轮播已生效）。
> 演示站（translator-demo）按指示未动，与主站存在一个批次版本差。

### 〇-XLIV、积分口径全面落地 + 角色包 + UI 实装收尾（2026-09-19，已部署双站）

> d581ca2 提交批次：对外全面积分口径（零 token 裸值/汇率）、职业角色包（job_role 15+）、UI 实装收尾；
> 发版记录见《部署指南.md》09-19 行。

### 〇-XLIII、体验与运维批次（2026-09-18，已部署双站）

> 留资链路、Tab 精简收口、PG 竞态防护等，明细见《部署指南.md》09-18 批次与 git 历史。

### 〇-XLII、架构融合与质量闭环（2026-09-17，未部署）

> 来源：《改造方案_架构融合与质量闭环_20260917.md》（基于 2026-09-17 全量 UAT 实测 + 全码阅读核实）。
> 落地记录与偏差说明见《改造完成情况_架构融合与质量闭环_20260917.md》。

| 序 | 项 | 内容 | 要点 |
|---|------|------|------|
| P0 | e2e 闸门红点清零 | `e2e/_tmp_admin.spec.ts`、`e2e/_tmp_iframe.spec.ts` 移入 `e2e-manual/` 并加 skip 守卫 | 二者写死生产站 `langcross.lexicorn.cn` 且依赖 CI 不存在变量 → 闸门长期必红、钝化回归敏感度；`testDir=./e2e` 天然不含新目录。Playwright 34/1 红 → **33/33 全绿零豁免** |
| 3 | LLM 路由补测试 | `llm/client_test.go`(14) + `engine/stagemodel_crypto_test.go`(2) + `engine/pickroute_test.go`(4) | 补上 `CallChatFallback` 429→等待→`HunyuanFallbackModel` 降级重试、坏 JSON/500/超时三分支、SSE 首块、stage_models 密文解密与密钥继承、静态路由全 0 权重/单路由/开关两路径（全 httptest，不依赖外网） |
| 4 | evals 不合格处置 | `workflow.go applyEvalDisposition` + tickets 表 `quality_flagged`/`qa_errors`/`qa_warnings` 三列（`db.EnsureColumns` 幂等）+ 告警中心 `kind=eval_quality` + 群机器人四渠道 | 修复 `SaveRecord(...,"passed")` **硬编码状态**（评估分数此前落库即死数据）；阈值 `evals_fail_threshold`（默认 60，<=0 关处置）；提醒 `evals_alert_enabled` 可单独关（只打标不打扰）；同单同语言同阶段限频 1 次；**不做自动重译**（Judge 主观分重译易震荡，仅人工决策）。单测 12 例（含阈值 59.9/60/60.1、限频幂等、打标单向性） |
| 5 | 用户侧 QA 报告透出 | 工单详情响应增 `quality` 字段（`ticketQualityView` 独立解析，不改既有结构）；`TicketsPage.tsx` 列表质检列（红「N 项错误」/黄「N 项提示」/「质检存疑」）+ 详情「质检报告」区块（汇总 + Issues 明细表 + 五维评估分） | 此前前端全仓 grep `qa_report` **零命中**，付费用户仅在下载 xlsx 后能看到质检投入；error 文案强调「已自动重译后仍存在，建议人工复核」。vitest 4 例（徽标三分支） |
| 1A | ai-assist 融合 | `ai-assist/*` → `backend-go/internal/assist/*` + `cmd/assist-server`；删独立 `go.mod/go.sum`；`go:embed` 内嵌管理页与 seed；日志统一 slog；ctx 贯穿；Token 经主后台 `/api/admin/assist/token` 免手填下发；systemd 改单二进制 `translator-assist`；CI 补 `assist` job | **数据隔离保留**（独立 SQLite 不并入业务库）；生效 Token 链 env > 主库 `assist_admin_token`（enc:v1:）> 默认值；`assist_uat.sh` 27→**32 断言**（+内嵌页、Token 桥接与 env 优先级契约）；单测 +9（Go 5 / vitest 4） |
| 2 | store 归组/冻结/下沉 | 加密能力下沉 `internal/secret`（`store/crypto.go` 改名同薄委托，90+ 调用点零改动）；新增 `store/README.md` 六域索引；新增仓库级 `AGENTS.md` | **`billing.go`(2201 行) 与 `kbpackages.go`(1371 行) 只减不增**；`store.go` 不承接业务方法；基础包禁止反向 import store；新列一律 `db.EnsureColumns` 幂等。第三步物理拆包（webhooks/referral → `internal/biz/*`）按方案留待评估后排期 |

回归：`go build`/`go vet`/`go test -race ./...`（26 包，含 assist 4 包）全绿；`run_uat.sh`（PG 方言）API **67/0** · T **308/0** · Playwright **33/33**；`assist_uat.sh` **32/32**；前端 `tsc` 净 + vitest **87/87** + `vite build` 绿。

闸门排查（2 处环境陷阱 → 顺带修 1 个真实脚本缺陷）：① `api_uat_txn.sh` T16 拒绝计数用 BRE `grep -c 'a\|b\|c'`——`\|` 为 GNU 扩展，非 GNU grep 下静默 0 命中致 `S+E==30` 恒假（响应体落盘取证确认语义正确：3 成功 + 20 QPS 限流 + 7 并发限流），已改 `grep -cE`；`assist_uat.sh` 同类负向断言（会「永远通过」）一并修正，全仓仅此 2 处，已立为 `AGENTS.md` 第 7 条约定。② T28 分片上传 4 断言失败为本机沙箱对 `~/Library/Application Support/能言/_uploads/**` 的 unlink 拦截（旧批次残留），清残留后 7/7 全过。

（遗留：`internal/openapi`、`internal/observability`、`internal/infra/ratelimit` 仍无测试文件；assist 管理页仍为单文件内联 JS，React 化留 B 阶段。）

### 〇-XLI、ai-assist R0 批次与全量评审核实（2026-09-16，提交 4ff889a，主站已部署）

> 来源：①autosales 批次上线后的全仓端到端评审（API 67/T 308/双实例 8/vitest 79/ai-assist 22 场景手工端到端，产出《核实报告_AI顾问缺陷与RAG改造_20260916.md》）；②用户实测反馈「AI 顾问不调 LLM 只机械回复」——三层根因（LLM env-only 无配置入口且无热加载 / hitScore 纯关键词子串匹配无语义 / 兜底话术空承诺）全部代码级核实并当日修复。

| 项 | 内容 | 要点 |
|---|------|------|
| R0.4 | 管理台 LLM 配置 | handleConfig key 白名单闸（堵任意 upsert 静默无效陷阱）；llm_base_url/api_key/model/model_backup 四键后台在线配置；api_key 掩码回显+掩码回写 skip；engine.ensureLLM 惰性重建（指纹变更重建 client，保存即生效免重启，env 显式接入优先）；`/api/assist/admin/llm/test` 测试连通；管理台 LLM 接入卡（表单/连通按钮/env-db-rule 三态徽标） |
| R0.1 | 同义词归一检索 | hitScore 增 configs.synonyms 归一表（管理台在线编辑，指纹失效重载）；seed 预置 充值/翻译/价格 三组；修复用户实测「怎么充钱」三层全脱靶；顺带修正 what-is 条目超泛关键词「是什么」（任何 XX是什么 误命中） |
| R0.2 | 兜底改造+运营闭环 | 零命中不再空承诺（改引导话术+快捷入口）；未答问题去重登记（上限200 FIFO）→管理台「待补料问题」清单（运营补知识库数据飞轮最小闭环） |
| R0.3 | 状态可见 | sessions 载荷增 llm_mode（env/db/rule）/unanswered；管理台徽标常显 |
| 评审修复 | e2e 断言漂移 | admin_tabs_lang 菜单断言 8→9（autosales 新增 AI 助手菜单致发布闸门双红，复跑甄别确认非 flaky），补 AI 助手菜单可见断言 |
| 安全项甄别 | P1-3 三连属实 | 会话 ID LCG 可预测/chat 无限流/admin_token query 传参（均已代码级确认，与 CORS 白名单一并列为 R1 同批待办） |

自动化：新增 `scripts/uat/assist_uat.sh`（**27 断言**：C端链路/同义词/兜底改造/白名单/掩码/热加载/连通/CRUD/管理页）；ai-assist 单测 +9；vitest 79；注释扫描归零（missing_comments 4 包 + 前端 12 文件补齐，office.go 3 处为嵌入 HTML 内 JS 误报）。清理测试数据（artifacts/test-results/pycache/DS_Store/tmp 残留）。

部署（当日 23:05）：ai-assist 交叉编译（sha 8d7a8a7f…）→ 备份+mv rename 替换 bin/web/seed → 线上 synonyms 经管理 API 写入 → 公网验收 health/greeting/**「怎么充钱」命中充值引导**/管理台 200/白名单 400 全过。主站 translator 本次未动。老部署注意：synonyms seed 不重灌，需管理台粘贴一次（已记《部署指南》§十三）。

### 〇-XL、缺陷核实修复批次与两站部署（2026-09-16，提交 5f721b5，两站已部署）

> 来源：全量代码评测 + 双方言全量 UAT（PG/SQLite 各 API 67 · T 295 · E2E 全绿）后，对产出结论逐条回到代码核实（详见《缺陷核实报告_20260916.md》——含 3 项「原判定不成立/降级」的诚实修正：JWT fail-fast 已有 REQUIRE_PROD_SECRETS 闸、backup_remote_cmd 无 HTTP 写入路径、README 主基线数字与实测一致）。

| 项 | 修复 | 要点 |
|---|------|------|
| D1（P2·资损留痕） | `SettleExhausted` qerr 吞错 | 永久余额兜底分支误判陈旧 `qerr`（恒 nil），真实 DB 错误被静默吞掉、部分欠费无痕消失；改判 `err` 且非 ErrNoRows 即上抛；新增 `TestSettleExhaustedNoPermAccount` 锁死分支语义 |
| D2（P3·配置未接线） | 低额告警阈值 | `lowBalanceThreshold` 硬编码 100000 → 实读 `low_balance_alert_tokens`（非法回退默认）；新增 service 包首批单测 `TestLowBalanceThresholdWired` |
| D3（P3·运维） | 备份推送超时 | `backup_remote_cmd` 改 `CommandContext`+10min 超时（超时单独告警）；注释禁止该键混入 settings HTTP 白名单（防 admin-RCE 面升格） |
| D4（测试稳定性） | e2e flaky 根治 | `mobile_uat` 全文件盲等→`expect.poll`/`networkidle`、selector 15s→30s、`setTimeout(90s)`；`a11y_errors` axe 扫描同样贴线超时一并抬限——复跑 **33/33 首过 0 flaky** |
| D5（传输安全） | Caddy CSP | `translator.conf` 新增 CSP：资源域收敛本站 + 仅放行 Turnstile/office.js 必需外链；`script-src` 暂留 `unsafe-inline`（后端渲染页/品牌注入/office taskpane 均内联脚本，待 nonce 化收紧，已注记） |
| 处置决定 | 支付渠道 | 暂无商户资质：微信/支付宝在线收单**维持 fail-closed 占位**（mock/static_qr/USDT/人工入账可用），不接真实 SDK；回调验签实现保留，资质到位仅需补 CreateOrder 真实调用 |

自动化与注释：`api_uat_txn.sh` 新增 **T44 源码级回归锁 ×6**（D1-D3/D5 修复点防回退），T 套件 295→**301**；全仓中文注释核查（后端 2010 函数文件级+函数级 0 缺口、前端补 `utm.ts`/`markdown.ts`/`translate.ts`/`OrgP.tsx` 函数级注释，密度达标）。

两站部署（当日 13:13-13:15）：主站 `translator-server` + web + 三管线脚本 + Caddy（validate 通过 reload），演示站同版本二进制（★ 踩坑记录：`cp` 覆盖运行中二进制撞 `Text file busy`——演示站二进制需 stop→cp→start 或 rename；主站用 `mv` rename 无此问题；已补进《部署指南》§五 与两站 sha 比对规范）；外网验收两站 health 200、CSP/HSTS 生效、`demo_admin` 登录成功、重启后零 error 日志。旧版留底：两站 `bin/translator-server.bak.20260916_*`、`web_old.*`、`/etc/caddy/translator.conf.bak.20260916`。

回归：go build/vet/22 包 test 绿；vitest 63；API 67 · T 301 · E2E 33 全绿（run_uat_confirm）。

### 〇-XXXIX、架构评审核实与修复批次（2026-09-16，提交 aa6269f）

> 来源：对《架构评审报告》全部发现做端到端 UAT 逐条核实（详见 `核实报告_架构评审发现逐条验证_20260916.md`，含 P0/P1/P2 修复记录表与「评审误报/不适用」甄别），P0/P1/P2 修复全部落地。回归：API 67 · T 295（含 T43×11 新断言）· E2E 33 · tsc 0 错 · vitest 63 全绿（SQLite 闸门；PG 主矩阵在当日早些轮次同样全绿）。

| 级 | 修复 | 要点 |
|---|------|------|
| **P0 ×3** | ① 支付渠道 fail-closed | 微信/支付宝真实协议未接入时下单显式报错（暂不可用），删除「下单失败静默回退 mock 出 `mockpay://` 假码」路径（用户扫废码、订单永挂 pending）；`payment_test.go` 契约更新 |
| | ② 停机顺序 | `main.go` Shutdown→Sink.Stop，在途计量事件不因先停 sink 丢失 |
| | ③ RBAC 收紧 | 高角色（等级≥4）必须平台级归属 `tenant_id=0`（IsSuperAdmin/RequireRole）；`users/update` 收口（对租户内账号提权→400）+ `user_import` 同口径；存量违规行（role=admin 挂具体租户）实测被 403 拦截（跨租户退款 exploit 已封） |
| **P1 ×5** | 前端资金闭环 | 退款/发票冲红前端封装（`adminOrderRefund`/`billingInvoiceVoid`）+ PlansP 按钮；支付状态轮询加终态短路+in-flight 锁+`document.hidden` 暂停；subscribe 失败 toast；余额耗尽横幅+useChat 充值引导（租管+跳计费 Hub）；TS SDK `waitTask` 优先级 bug 修复（v1.0.1）+ Java SDK 删除虚假宣称 |
| **P2 ×7** | 体验/基建 | SSE 心跳（20s `: ping` 注释帧，与 D20 sseMu 共锁+stop join）+ 前端 60s 空闲判连；`normErrCode` 错误码大小写/别名折叠；聊天 📎 即时文件翻译；playwright 超时收敛+trace retain-on-failure；`run_uat.sh` flaky 自愈（首轮非零自动 `--last-failed` 复跑甄别）；CI 新增 e2e job（SQLite 方言）；迁移段 PG advisory lock（`acquireMigrateLock` 防多实例并发迁移）；billing 17 处时间写点/比较点统一 UTC（orders TEXT 列字典序可比） |

UAT 基建加固（本批踩坑沉淀）：`dblib.sh` sqlite3 加 `.timeout 5000`（后端 reconciler 运行期写库，CLI 默认 busy timeout=0 撞锁取空导致断言假红）；`api_uat_txn.sh` 断言一律两段式（macOS bash 3.2 对 `"$()"` 内嵌 `\"` 的解析缺陷会把请求体拆坏）；手工探活 server 必须带 `SELFCHECK_URL`，e2e 需 `BASE_URL`/`API_URL` 同时指向被测端口；mock 残留进程（8901/8902）会让 T42 假红——跑闸门前先 `pkill -f 'mock_llm|mock_chain'`。

——以下为历史记录——


## 〇-XXXVIII、线上体验与运维五项批次（2026-09-15 晚，提交 c5f3376，两站已部署）

> 来源：用户线上实测反馈五项。全量回归：go test 22 包 · vitest 63 · API 67 · T 255 · Playwright 29 全绿。

| # | 任务 | 落地 |
|---|------|------|
| 1 | **token→积分口径统一**（余额不足提示露裸 token、700KB PDF 估 40 万 token） | 后端建单预检/账单/中止文案改积分（`PointsFromTokens`，保留「余额/耗尽」关键词供 `gateErrorCode` 映射）；新增 `estimateFileSourceChars` 按扩展名分档（纯文本/3、pdf·doc·ppt·xls 等二进制容器/12、未知/6，旧口径 size/3 把 PDF 整包当文本→高估 4 倍）；前端 15+ 面板（个人中心/任务/价目/租户/组织预算/用量/对账/推荐/数据源/聊天预算条）token 标签全量折积分，录入侧 `pointsToTokens` 反算落库；回归 **T40×3**（自适应读余额：700KB 放行/超支拒绝且零 token 裸值）。⚠️ **本行估算口径与「700KB 放行」断言已被 2026-09-25 F-41 翻转**（新公式 `chars×langs×K`，T40 重钉 5 条，见「〇-T」节），本条仅作历史记录） |
| 2 | 还原模式积分提醒 | `tk.deliveryRestoreTip` 双语言加 ⚠️「还原文件可能消耗更多积分，日常使用建议纯文案模式」 |
| 3 | **后台一级 Tab 20→8 精简** | 总览(+用量明细)｜工单｜个人中心｜知识库(+数据源)｜组织与成员(+成员账户)｜**计费与套餐 Hub**(套餐/租户/对账,L4 门控)｜外部调用(+**SDK 子页**三端安装指引)｜**系统与运维 Hub**(注册触达/邮件模板/流程/协议/审计/运营策略/模型供应商/品牌页脚)；`renderPanel` 保留旧 key 深链兼容；e2e `admin_tabs_lang` 3 例 |
| 4 | **审计日志「停在 8-29」根因修复** | 根因=SQLite→PG 切流按显式 id 导入但序列未同步：新审计拿低位 id，`ORDER BY id DESC` 将其沉底（界面误示停更）+序列逼近存量后主键冲突静默丢写（~20 条）。DB 层：生产两库 9+9 表 `setval(max(id)+1000)` 热修；代码层：`store.syncSequencesPG()` migrate 启动自愈（幂等，只向前）+ 审计列表改 `created_at DESC, id DESC`；回归 **T41×2** |
| 5 | **语言多选双展示去重** | `LangMultiSelect` 由 TDesign 多选 Select 重写为 Popup+自绘分组勾选列表（触发器永远单行占位），选中语言唯一展示位=外部 `<LangChips/>`（聊天+工单接入）；e2e M1 适配新 DOM + `admin_tabs_lang` T3 例 |

部署与验证：主站+演示站新二进制（md5 一致）与前端 dist（同 hash）上线，`deploy_check.sh` 两站 9 项全过；主库 audit_logs 序列推进至 1516（9-15 新行正常落库）。


## 〇-XXXVII、P0/P1/P2 待办收尾批次（2026-09-15：核实报告逐项落地，除支付渠道外全部清零）

> 来源：《P0P2待办核实报告_20260915.md》（对 2026-09-11/14 两份待办的逐条源码核实，含两处初判修正：备份本就由 watchdog 自动执行、群通知已支持企微/钉钉）。除「真实支付渠道接入」外全部完成。详见核实报告第五节《落地记录》。

| 块 | 内容 |
|---|---|
| **P0 ×1** | orgs 同级同名唯一约束：`store/orgmigrate.go` 先去重（同租户同父同名保留最小 id）再建 `idx_orgs_sibling_unique` 唯一索引（双方言迁移链挂载）；此前 API 层 `IsUniqueViolation` 防重复分支实为死代码（表无约束），现真实生效；Go 单测 `orgmigrate_test.go` 锁冲突→400 路径 |
| **P1 ×4** | ① 影子余额多实例闭环（sink.go）：seed 5s TTL 过期强制回读重播种（扣本实例缓冲量防少扣）+ `store/balancehook.go` 全资金写点钩子（Charge/Refund/Settle/发放/认领/奖励 11 处）→ 本进程 `Invalidate` + Redis `shadow:invalidate` RPUSH/BLPOP 广播（`billing/shadowsync.go`）② watchdog 四类周期任务 `runExclusive` 分布式锁（watchdog-check/db-backup/subscription-scan/ticket-retention；Redis 异常降级本地；内存巡检保持实例本地）③ k6 容量基线量化：`P99_MS/ERR_RATE_MAX` 阈值环境变量 + `thresholds_passed` 出参 + JSON/CSV 归档 `deploy/loadtest/results/` + `run_capacity_matrix.sh` 多台阶矩阵 ④ 审计写失败可观测：`store/audit.go` 原子计数 + 同 action 1 分钟限速日志，`/metrics` 新增 `translator_audit_write_failures_total` |
| **P2 ×6** | ① restore_drill 定时化：`translator-restore-drill.{service,timer}`（每周日 03:30，Persistent 补跑）+ 脚本 PG 分支（pg_restore 到临时库校验后清理）+ 失败回投 S9 告警口 ② 用量报表 CSV 导出：`UsageLedgerForExport`（日期区间/10 万行硬顶/charge_kind 语义列）+ `?export=csv`（非超管脱敏与展示系数和面板同口径）+ 用量看板导出按钮 ③ 多语言打包下载回归：UAT **T39**（OpenAPI 单文件×双语→行内 `zipOutputs` 预打包 zip，契约按 `service/ticket.go` 实况修正）+ Playwright **M1**（工单页勾选英/日双语→zip 字节断言）④ SDK 发布管线：`scripts/release-sdk.sh`（ts/py/java 三室版本同步校验→tsc 构建+导出面冒烟→sdist/wheel+venv 安装冒烟→npm pack 预演→`--publish` 显式开闸）+ TS `prepublishOnly`（顺带修潜伏缺陷：devDep 缺 `@types/node` 致 tsc 从未干净编译）+ Python `pyproject.toml`/`__version__` + `sdk/CHANGELOG.md` ⑤ Slack/Teams 通知渠道：`bot.go` 四渠道（Slack `{"text"}`、Teams MessageCard）+ 配置读写键 + 后台表单 + `bot_test.go` 双单测 ⑥ 桌面分发：`build.sh` 可选 Developer ID 签名（hardened runtime）+ notarytool 公证 + staple（`APPLE_DEVELOPER_ID/APPLE_ID/APPLE_APP_PASSWORD/APPLE_TEAM_ID` 四元组触发；缺省仍 ad-hoc） |
| **测试资产** | UAT T 套件 235→**250**（新增 T38 用量 CSV 导出 8 断言：鉴权/隔离/脱敏/区间/未登录非 CSV + T39 多语言 zip 6 断言）；Playwright 27→**28**（M1）；Go 新增/补充单测 ×5：`orgmigrate_test`/`bot_test`（四渠道 payload+关闭零请求）/`sink_test` 影子 TTL 重播种/`lock_test`（distlock 非阻塞）/`watchdog_lock_test`（runExclusive 无 Redis 降级）/`audit_fail_test`；`run_uat.sh` 新增 `PW_TARGET` 定向 e2e 开关；race 28 包全绿；vitest 61/61、tsc 通过 |
| **注释补齐** | 全仓中文注释审计：后端 202 个 Go 文件（非 test）100% 含中文职责横幅+函数注释；补齐 `vcode.go`/`anydoc-smoke` 两处缺失函数 doc、`ErrorBoundary.tsx` 全文件注释、admin 面板（KbP/OrgP/ModelsP/PlansP/TenantsP/TicketsP/WebhooksP/ApiKeysP/panels_e/DataSourcesP）加载函数 doc ×19 与 JSX 分区注释、KbP 21 个操作函数 doc（纯注释零代码改动，tsc/vitest/构建复验全绿） |


## 〇-XXXVI、全仓字节级缺陷修复批次（2026-09-14 晚：5×P0 + 14×P1 修复，双方言 UAT 全绿）

> 来源：4 路并行深读代码审查（计费交易/翻译引擎/前后端契约/安全多租户），逐条源码复核后修复；台账见《UAT_缺陷清单与处置记录_20260914.md》。定性为特性的 2 项（重试/对冲 token 实扣计费、文件部分失败整单计费）按实扣口径保留。

| 块 | 内容 |
|---|---|
| **P0 资金/接管级 ×5** | ① `SettleExhausted` 重写（store/billing.go）：欠费结算改事务内权威复核（ErrSettleNotNeeded 回插）+ 有界清零（消耗≤owed、试用/临期优先）+ `charge_kind='settle'` 调整流水；**纯后台批次（KB 重建等 abort=nil）不再触发清零停服**，仅告警留痕。② 部门管理员 org_id=0 绕过（api/auth.go reset/update 两处）：未分配部门目标一律 403，与 Delete 三处口径对齐。③ 品牌子域登录链：登录返回一次性 sso_code（60s/单次消费/走 sso/exchange），不再返回裸 JWT；前端 Login.tsx 改跳 `/?sso_code=`。④ `auto_charge` 即时入账仅 super_admin 生效，租户管理员订单恒 pending。⑤ 敏感词闸 Unicode 归一化（internal/sensitive）：小写+NFKC+零宽剥离+宽松空白剥离双遍检测，全角/零宽/字间空格混淆实测全部命中、干净文本零误报 |
| **P1 资金错误/安全/可靠性 ×14** | token 迁移失败不置位标记（重启幂等重试）；`usage_ledger.charge_kind` 语义列（charge/log/settle，建表+补列迁移，退款消耗核算只认实扣行——修复少退款）；sink 影子余额 seed 失败不再误中止在途任务；BootResume 只回收租约陈旧（>120s 无心跳）任务（多实例双跑防线）；工单 CAS 认领互斥（ClaimTicketForRun，draft/queued/rejected→in_progress 原子推进）；供应商 5xx/401 纳入降级链与熔断（isServerError）；KB 模糊匹配 ≥50% 重叠门槛（防串句译文采用）；pdftotext CommandContext 120s 超时；Webhook 投递禁跟随重定向 + Dialer.Control 拨号时校验真实 IP（SSRF/重binding 双封）；验证码校验 per-key 互斥锁串行化（改密+邮箱码）；caddy-ask 回环放行拒 XFF 请求（防代理枚举租户子域）；vcode 层进程内存回退（无 Redis 部署 SSO/验证码可用）；充值 points 溢出上限 400；前端 kb.ts 统一 fetchJSON 守卫（401 跳登录/非 2xx 抛错）+ 分片 uploadId 落 localStorage（断点续传真实可用）+ selfservice 余额统一积分口径 |
| **测试资产** | UAT 新增 **T37 今日修复回归套件**（19 断言：org_id=0 子树内放行+未分配 403 / auto_charge 双向 / sso_code 全链无裸 token+单次消费 / 敏感词三种 Unicode 混淆 e2e / points 溢出 / charge_kind 枚举守恒），交易专项 216→**235**；敏感词归一化 Go 单测 ×3（TestHitsNormalization/TestHitsFullwidthCJK/TestNormalize）；mobile_uat 两条 E2E 修复（home 登录改走 /login）；Playwright 25→**27** 全过 |
| **回归结论** | PG 主矩阵（race+API 67+T 235+Playwright 27）与 SQLite 矩阵全部全绿；前端 vitest 61/61、tsc/build 通过 |

## 〇-XXXV、商业化 D-1 验收批次（2026-09-14 晚：压测/硬扣费/灾修/全链路四项全过）

| 块 | 内容 |
|---|---|
| **k6 容量压测** | 新写台阶探针（`deploy/loadtest/k6.js`：constant-vus、随机串绕 TM 缓存、rate_limited 归过载不归系统错误）。实测主站同步翻译：**goodput 平台期 ≈0.85 req/s（LLM 上游约束）**，VU=4 P95≈8s / VU=8 13s / VU=16 24s；VU=24 时租户并发闸（临时 20）温和拒绝 141 次、系统错误 0、熔断未开、宿主机内存最低 36%。压测痕迹全清理（quota 恢复 10/3、Key disabled、grant 删除、临时工具删除）。定案文档《容量预告与超卖预案_20260914.md》（安全并发/承诺话术/五类超卖触发-动作/加容量路径） |
| **opskey 运维工具**（新） | `backend-go/cmd/opskey`：-create/-list/-revoke（API Key 现场签发回收）、-grant（额度）、-resetpw（仅 scratch 用）。生产连库走 secrets.env；本次为压测与演练配套 |
| **billing_enforced 复核** | 生产实为**已开启**（billing_enforced=1+token 迁移完成）；压测实证完整硬扣费链：扣减→清零→insufficient_balance 停服→sink 实时 `billing_exhausted` critical 告警。演示站刻意保持关（避免打断演示账号） |
| **灾备演练（RPO/RTO）** | 真实计划备份 51MB → scratch `pg_restore` **RTO=8s**、9 关键表行数对账全 ok（指南 §十一-B 复测命令固化）。隐患处置：同日多次重启会挤掉日备（按份数清理）→ `backup_keep=14`。**异地副本闭环（当晚）**：拉取式本地副本 `deploy/fetch_backups_local.sh`（Mac cron 每日 10:00，rsync 最新 3 份+sha256 远端/本地比对+留 7 份，服务器零改动零凭据下发；首拉 3×51MB 校验一致；本地 pg client 15 读不了 PG16 TOC，完整性以 sha256 为准） |
| **全链路演练（19/19）** | `deploy/chain_drill.sh`：生产备份→scratch→同二进制起 :8799 一次性实例→注册(UTM 归因✅/礼包 1000 分✅/默认 Key✅)→真实翻译扣费✅→耗尽硬停✅→充值 manual 单✅→「我已付费」✅→超管确认到账 3000 分✅→退款 refunded+额度回收✅。**发现生产孤儿余额行 tid3/4（已删租户，注册撞号捡走 5 万）**→ 生产清理+审计+脚本防御性清扫；告警两路定性（sink 即时/watchdog 300s 周期）；退款=按剩余率折算核实与 SOP 一致 |
| **验证** | `go test ./...` 全仓 ok；S9 收口新单测（鉴权 403/firing 落库 critical/resolved 忽略）；chain_drill 重放最终 19/19 全绿；行动清单第四批四项 [x]（仅剩用户侧：secret 轮换/S0 24h 观察/异地备份） |

## 〇-XXXIV、商业化试运营开闸批次（2026-09-14，S0-S9 全部完成，主站上线）

> 依据《商业化试运营_差距与行动计划_20260914.md》逐节点落地；所有改动**只应用主站** translator（43.108.86.140，演示站未动）。核心叙事：从「token 额度」全面切换为「**积分预付费**」客户口径，同时补齐品牌、官网、反薅、归因、触达、合规闸、监控与运营 SOP。

| 块 | 内容 |
|---|---|
| **S0 容量扩容** | 主站 drop-in：`MemoryMax=1229M/GOMEMLIMIT=900MiB/LLM_CHAT_CONCURRENT=3/worker=4`（实测总内存 1613M，留 PG/系统余量）；PG `shared_buffers` 640MB→256MB。24h swap/mem_pressure 观察挂账 |
| **S1 积分制计费（核心）** | **1 积分 = 300 内部 token**（`points_tokens_rate`，`/api/auth/me` 下发、前端 `utils/points.ts` 统一换算）；`packages.points` 列迁移 + v4 九档价目灌生产（试用 1000 分/14d，¥99/3000 分月、¥299/10000 分月、¥999/¥2999 年付，充值 299/1299/4599/19999 对应 3000~300000 分且 `duration_days=0`=**永久**）；**首月半价**（注册 30 天内 paid 订阅单五折，充值不折，store 单测锁价）；账目三修（rate_card 收敛+唯一索引、model_routes enc:v1、`price_fen_per_million_tokens=29900`）；**公开面零 token 裸值**（/api/plans 出 `free_trial_points`、balanceOut 出 `balance_points` 系，`openapi_show_tokens=0` 可整体隐藏，SDK 兼容默认保留）；前端 ChatWindow/MyBilling/App 顶栏/selfservice/PlansP/中英 i18n/公开 /pricing 页全部积分化；白皮书升 v1.1 |
| **S8 敏感词兜底闸**（开闸前置） | 新包 `internal/sensitive`（词包 mtime 热加载、大小写不敏感、生产 40 词五类）+ engine 双向挂接：**输入命中不进模型**（文本整单拒译/文件段级占位 `[已拦截·REDACTED]`）、输出兜底替换；审计 `sensitive_block/output` + 告警 + 超管 Switch（`sensitive_gate_enabled`）。部署指南 §八-B7 |
| **S2 品牌包** | 《品牌一页纸.md》（名称四层用法/定位句/slogan/卖点/禁用词）；index.html 标题、README 副标题、terms/privacy 中英署名「能言（Lexicorn）团队」（生产验证） |
| **S3 反薅** | `disposable.go` 一次性邮箱黑名单（内置 70+ 域+config 增补+子域匹配，挂注册/发码/换绑×2）；**Cloudflare Turnstile 生产点亮**（site/secret 仅存 system_config，注册链路顺序验证：格式→黑名单→人机） |
| **S4 归因+漏斗** | Landing UTM 五参捕获（localStorage 一次性消费）→ 注册随 `landing_path/host/UA/ref` 落 `registration_attribution`；`GET /api/admin/funnel`（注册→激活→耗尽→首购→续费五环节按 utm_source/裂变码聚合）+ PlansP「📈 增长漏斗」面板（7/30/90 天） |
| **S5 官网首页** | `components/Landing.tsx`：未登录 `/` → 营销首页（hero/三卖点/积分价目卡（/api/plans 动态）/FAQ/信任条/CTA/UTM 捕获），i18n `panels/landing.ts` 38 键中英；index.html SEO（description/og/canonical/JSON-LD） |
| **S7 触达序列** | `s7_watchlist` 观察表 + watchdog `runGrowthScan` 日扫：余额清零满 48h 且从未付费→**一次性挽回礼包**（167 积分/7 天）站内+邮件+群+审计；到期摘除 T+3 老客回访（去重）；续费窗口补 T-3 触达（`notified_renew3`）；store 单测 7 场景 |
| **S9 最小监控** | 同机三件套 **全回环零对外暴露**：prometheus(apt 2.45, 127.0.0.1:9090, MemoryMax=280M, TSDB 14d) + alertmanager(v0.27.0 二进制, 9093, 96M) + node_exporter(9100)；抓取经 `credentials_file` 带 METRICS_TOKEN；6 条规则（TranslatorDown/LLMBreakerOpen/LLMHighErrorRate/HostMemory×2/HostDiskLow）→ **新收口端点** `POST /api/alerts/alertmanager`（`s9_alerts.go`，X-Admin-Token 或 ?token= 常量时间比较）→ 平台告警中心（`kind=prom:*`）+运营群推送；AM→app→DB 全链冒烟通过（实耗 ~110M，avail 652M）。部署指南 §八-B8 |
| **S6 运营 SOP** | 《试运营收款发票退款SOP_20260914.md》：到账确认三对+SLA≤2h+双人接单+每周盘 pending；普票口径（确认后补开/专票不承诺）；退款消耗门槛制（<10% 全退）+台账；报价单/意向单最小模板；话术 A-F |
| **验证与质量** | `go test ./...` 全绿（含新增 sensitive/disposable/s7/points 定价/Alertmanager 收口单测）；前端 tsc 干净、vitest **54/54**、vite build；生产逐项验证：points 列+汇率 300、/api/plans 无 token 泄漏、/pricing 半价文案、词包 40 条加载、黑名单 mailinator 拦截、漏斗接口 403、/metrics 无 token 401、三监控服务 active+回环、冒烟告警落库；行动清单 S0-S9 全 [x] |
| **踩坑记录** | ① App.tsx 曾被脚本「先读快照后回写」truncate 成 0 字节——git 恢复+重放，教训：**批处理每段独立读写，禁止跨段共享快照**；② 主站监听 8787 非 8080；③ alertmanager 配置 600 root:root 导致服务用户读不到（改 root:alertmanager 640）；④ apt 无 alertmanager 包（universe 未开），走 GitHub 二进制 |
| **遗留（第四批 D-1）** | 灾修演练（RPO/RTO）、k6 压测定并发、`billing_enforced=1`（演示站试跑→主站）、全链路演练、S0 24h 观察；用户侧：SiliconFlow key 轮换、Turnstile secret 轮换建议 |

## 〇-XXXIII、线上反馈三项修复（2026-09-14，提交 fbefe5a…31e6b1f）

| 块 | 内容 |
|---|---|
| **① 后台暗色适配** | 暗色模式此前仅前台适配，后台黑白混杂。修复：admin 目录 ~165 处内联硬编码浅色（#667 提示灰/#f7f9fc 软面板/#fff 卡片/黄蓝紫提示盒/绿橙状态色等）统一收敛为 `--adm-*` 语义令牌（亮色值与历史一一对应，零视觉回归）；theme.css 暗色块补 TDesign 漏配轨（`--td-bg-color-specialcomponent` 输入框底、`--td-brand-color-light` 菜单选中/浅 Tag、`--td-brand-color` 暗色文本）；补 `.panel-card/.stat-card/.ticket-progress-float/.download-card/.tag` 等自绘类暗色覆写。**刻意保留白底**：登录页预览 Mock 卡、QR 码图（对比度需要）。新增 e2e `D1`：暗色下后台骨架取色 + 近白块扫描（≤2）+ 代表面板抽查 |
| **② 文件工单上传 400** | 「766KB PDF 报『文件解析失败或超过大小上限（40MB）』」根因：React 重写后 `core.ts request()` 无条件预设 `Content-Type: application/json`，FormData 请求的 multipart boundary 被抹掉 → 后端 `ParseMultipartForm` 0.4ms 即 400（误导文案）。**影响所有走 request() 的 multipart 上传**（文件工单/用户批量导入/KB 导入等，自 React 重写起即坏）。修复：body 为 FormData 时不再设 Content-Type（交浏览器生成 boundary）；调用方显式传入者优先。新增 e2e `U1` 上传建单回归 + 线上演示站真实浏览器验证通过 |
| **③ 演示站新超管** | `demo_superadmin / Demo#2026Rm!`（tenant 0 平台超管）：live 演示库已种入并登录验证；bootstrap-demo.sh SEEDSQL 同步（重跑幂等）。顺带修脚本 bug：`DEMO_SEED_ACCOUNTS=0` 跳过开关此前被无条件 `=1` 覆盖（文档承诺失效）；`base_domain` 幂等写入（同日早前 B11 修复延续） |
| **部署** | 主站+演示站前端 dist 已同步（web 快照，权限口径按 bootstrap：主站 translator:translator、演示站 root:caddy + o+rX）；线上验证：演示站 /admin 暗色骨架 rgb(20,22,26)/零近白块/菜单选中半透明蓝，U1 浏览器上传建单通过 |
| **验证** | 全量 UAT 绿：A49/B67/T188（**T35 新增 6 断言**：特殊文件名 PDF multipart 建单/终态、多文件混合上传、bootstrap-demo 配置守护×3）、Playwright **27/27**（含 D1/U1 两条新增）、tsc 干净、vitest **52/52**（含 core.request FormData 不预设 Content-Type 回归单测）、build 通过 |

## 〇-XXXII、工单文件翻译双模式（还原文件 / 纯文案）+ anydoc 接入（2026-09-14，提交 6f45a27…5493e9f）

> 需求：无版式还原诉求时直接输出纯文本译文，并接入开源 anydoc（firecrawl/anydoc，Rust 核心，MIT）扩大文件准入面。方案评估结论：anydoc 为**单向转换**（doc/ppt/xls/odf/rtf/epub → Markdown），无 MD→office 反向能力（版式/字体必丢，保真度劣于既有原位 XML 回写）——因此定位是**纯文案模式的提取层**而非替换回写链路。方案全文《改造方案_anydoc接入·工单文件翻译双模式_20260913.md》。

| 块 | 内容 |
|---|---|
| **前端模式选择** | 工单建单页（文件模式）新增「交付方式」：还原文件模式（默认）/ 纯文案模式，选择记忆于 localStorage；纯文案模式动态放宽 accept 与校验话术（老格式仅在纯文案模式支持）；标题列模式徽标；详情页新增「下载译文 .md」 |
| **纯文案管线** | `HandleFile` 增 `delivery=text` 路径：anydoc 独占格式（doc/docm/ppt 系/xls 系/odt/ods/odp/rtf/epub）经 `anydoc_md.py` 子命令壳（venv `firecrawl-anydoc`，复用 runSubprocess 资源闸+nice+超时治理）→ GFM → 既有 MD 翻译管线 → 交付 `.md`；PDF/原生格式走既有 Go 提取器不经 anydoc |
| **还原模式兜底** | 成功后同步产出纯文案 .md 旁路产物（`persistTextOutputs`：单语言直存 / 多语言 `{工单号}_texts.zip`，RegisterArtifact 归属登记）；**版式回写失败重试 3 次后自动降级**为 .md 交付：工单成功、`file_writeback` 置 warning、创建人站内信 |
| **准入与安全边界** | 建单分层白名单（restore 12 种 / text 12+anydoc 独占 16 种）；anydoc 独占格式需 `anydoc_ready` 依赖就绪否则 400；不做 hosted OCR（数据主权，扫描 PDF 两模式均拒）；`ANYDOC_TIMEOUT_SEC`（默认 60s）/`ANYDOC_SCRIPT` 可调 |
| **接口** | create-file/OpenAPI 任务表单 `delivery=restore|text`（status 回显）；`download?id=&fmt=text[&file_id=]` 取译文 .md；`/api/health` 暴露 `anydoc_ready`；错误话术 friendlyAnydocError（损坏/加密/扫描/依赖缺失分类） |
| **存储** | `tickets.delivery`、`tickets.text_result_path`、`ticket_files.text_result_path` 三列（SQLite+PG 双方言幂等迁移） |
| **测试** | 单测：fileproc（WriteTranslationMd/friendlyAnydocError/白名单）、engine（optionString/anydocSourceExt）、api（normalizeTaskDelivery + **handleHealth anydoc_ready 出参**）；UAT **T34 新增 14 断言**（旁路产物注册、fmt=text 下载、白名单分层、纯文案工单无原格式产物、OpenAPI delivery 回显、**health anydoc_ready 暴露**）；全量：A49/B67/T182 全绿、Playwright 25/25、`go test -race` 四包通过、vitest 51/51、tsc/build 干净；anydoc 端到端冒烟（临时 venv + 生产 translator 身份，RTF→GFM/detect/损坏文件友好错误）通过 |
| **部署** | venv 新增 `pip install firecrawl-anydoc`；`anydoc_md.py` 随二进制同步至 `bin/`；`cmd/anydoc-smoke` 部署自检；部署指南 §一/§五/§八-B6/§十/§十二 同步更新。**2026-09-14 生产发版**（主站 43.108.86.140 + 同日演示站 rox-test 同步快照：二进制/前端/`anydoc_md.py`，数据与密钥不动）：`/api/health` `anydoc_ready=true`，translator 身份 RTF→GFM 服务端冒烟通过，`deploy_check.sh` 8/8；捕获并修复 healthcheck 解释器探测偏差（探测 PATH 系统 python3 而子进程走 venv `pyBin()`，导致 `anydoc_ready` 误报 false，提交 7e01396） |
| **发版后修复（★ 演示站品牌丢失）** | 演示站换装 B11+ 二进制后 `rox-test` 子域租户品牌（logo/登录背景/标题）静默消失——根因：B11（09-12）移除 `lexicorn.cn` 硬编码兜底后，子域→租户品牌解析必须显式有 `system_config.base_domain`，而两站库与部署清单从未配置过该项（主站 `rox` 子域同踩，属存量隐患）。修复：两站库补 `base_domain`/`primary_host` 并实测注入恢复（`极石` logo/`brand_granted:true`）；`bootstrap-demo.sh` 第 3 步幂等推导写入 `base_domain`（并修 `DEMO_SEED_ACCOUNTS=0` 开关被无条件覆盖的脚本 bug）；演示站按手册重跑 bootstrap 完成标准部署（SPA 反代注入/Caddy/systemd 全对齐）；回归单测 `api/branding_test.go` 锁定解析与降级语义；部署指南 §八-B 新增两配置项说明 + §十二 故障表补「升级后子域品牌丢失」条目 |
| **文档** | README 核心能力补双模式条目（并修正「不降级」过时表述）；部署指南/产品支持文档同步；改造方案文档留档 |

## 〇-XXXI、H1-H12 规划能力落地 + UAT 全批次覆盖（2026-09-13，提交 5168fed）

> 第二阶段（原 F12 规划）12 项能力一次性交付（仅 Chrome 商店上架不做）；UAT 断言从「S/A/B 批次」补齐到覆盖 S–H 全部批次（T 套件 114→168）；UAT 期间捕获并修复 1 个权限越权缺陷与 2 个前端回归缺陷。详录《修改文档_全面缺陷修复与前端增强_20260912.md》批次 8。

| 块 | 内容 |
|---|---|
| **H1-H2 术语链路** | Gate 命中强制替换+违规自动重翻闭环；Constrained Decoding 双轨（`x_term_constraints` 载荷，路由 `supports_constraints` AND 口径，不支持模型自动降级） |
| **H3 包级权限矩阵** | `kb_pack_grants` 表 read/write/manage 三档 + `/api/admin/kb-packages/grants\|mine` + 面板授权弹窗；★ UAT T30 捕获越权：6 处 `kbPackGrantedSkip` 仅跳过校验、无成员正向准入 → 补硬闸（成员必须持 write/manage 授权才可写，read 仅可读），回归测试 `h3_writegate_test` |
| **H4-H5** | TM 自动审核（QA 分≥80 直通 SaveBack，低分入人审）；KB/TM 分片上传断点续传（≤8MB/片、24h TTL、前端 >4MB 自动分片+续传+重试） |
| **H6-H7 智能路由** | 滑窗延迟统计 + Hedged Requests 并行对冲（`HEDGE_ENABLED` 默认关）；成本/延迟感知动态权重 + `/api/admin/ops/routes` 可视化（`DYNAMIC_ROUTING` 默认关） |
| **H8 状态统一** | auth/admin/branding/chat 四栈迁 Zustand，Provider 退化副作用壳；★ 回归修复 2 处：FrontShell lazy 组件缺 Suspense 边界（React #426 白屏）、StrictMode 双挂载会话恢复误清 token（401 风暴） |
| **H9-H10** | 二级裂变漏斗（付费永久包→上级返佣 `referral_l2_pct`，防环+幂等）+ `/api/referral/funnel` 看板；SCIM 2.0 Users/Groups 全端点（eq filter/PATCH active/组↔部门映射/独立 Bearer token）+ 租户自助配置 |
| **H11 SLO** | 三 SLO（可用性 99.9/成功率 99/P99≤8s）分钟环采样、多窗口 burn rate 双窗同判自动开合告警；`/api/admin/ops/slo` |
| **H12 编辑器生态** | `vscode-extension/`（零构建：侧边栏工作台/选中翻译/术语检索/SecretStorage）；`GET /api/translation/export-tmx`（TMX 1.4）与 import-tmx 成 Trados/memoQ 双向闭环；`GET /openapi/v1/terms` 术语检索端点（spec 同步） |
| **UAT 全批次覆盖** | T25-T33 新增 54 断言：SSO 兑换拒绝/计费配置鉴权/usage 镜像/spec 路径/分片全链/SCIM 生命周期/权限矩阵全组合/TMX/SLO 端点/裂变漏斗；T 套件 **168/168**，A/B 主链路 67/67，e2e 25/25，`go test -race` 通过；**PG 与 SQLite 双矩阵全绿** |
| **测试卫生** | 单测固定 `DatabaseDriver="sqlite"`（防 config.C 环境变量污染方言）；hedge 计数器 atomic 化；observeRoute 懒初始化入锁 |
| **中文注释补齐** | 后端 47 处 + 前端 21 处 + 插件 3 处函数/类型级注释（Go 导出与非导出函数注释覆盖复查全量）；gofmt 全仓归一 |
| **验证** | `DB_DRIVER=postgres run_uat.sh` EXIT=0（含 race 门禁）；SQLite 矩阵 EXIT=0；tsc 干净、vitest 51/51、双端构建通过 |

## 〇-XXX、PG 方言全量修复 + UAT 双方言矩阵（2026-09-12，提交 80f0f22）

> 生产已切 PostgreSQL 但 UAT 仅覆盖 SQLite，导致一批 PG 专属缺陷长期漏检。本次全链路重研修复 8 项方言缺陷，并把 UAT 工具链双方言化（同一套断言跑两种方言）。

| 块 | 内容 |
|---|---|
| **P0 邀请奖励停发** | `tenant.SetPersonal/SetInviteEnabled` Go bool 直绑 INTEGER 列在 lib/pq 下报错且被吞——改显式 1/0；`register.go` 不再忽略 SetPersonal 错误并记日志 |
| **P0 增量包结算必挂** | `json_set/json_extract`（SQLite JSON1）内联于 billing.go/packages.go/quota_grants.go——新增 `internal/db/jsonops.go` 双方言助手（JSONNumAdd/JSONNumGE/JSONSetFalse/JSONExtractNum/JSONTicketIDExpr，jsonb 实现 + 标识符白名单防注入） |
| **P1 卡死工单永不重排** | `RequeueStalledTickets` 同款 JSON1 → JSONTicketIDExpr 方言分支 |
| **P1 任务中心不可用** | `SaveUserTask` 用 `LastInsertId()`（lib/pq 不支持恒返 0）→ `db.InsertID`（RETURNING） |
| **P1 欠费丢弃计费** | `billing/sink.go` 余额不足整批结算失败静默丢弃——按决策改「清零停用」：新增 `SettleExhausted`（双桶清零、不落 ledger、充值后从 0 计量）+ 写 `billing_exhausted` 告警 + 对话提示真因文案 |
| **P2 健康位混淆** | `/status` 业务告警≥10 翻转 `ok:false` 误导监控——`ok` 收敛为纯基础设施位（DB+熔断），新增 `db_ok`/`degraded` 独立位 |
| **P2 驱动错误透吐** | 新增 `store/dberr.go`（IsUniqueViolation/DebriefDBError），auth/orgs/tenant/pay/plans/admin_packages/register 等 8 处端点脱敏（PG 不再泄漏约束名/SQLSTATE） |
| **UAT 双方言矩阵** | 新增 `scripts/uat/dblib.sh`（dbq/dbcfg/dbjson 断言层方言路由）；api_uat/api_uat_txn 全部 sqlite3 硬编码改造；`run_uat.sh` 支持 `DB_DRIVER=postgres`（自动重建测试库+pgvector+txn 套件并入编排）；新增 T15 修复锁定段（脱敏/健康位/句数镜像/个人标记/任务 ID） |
| **测试与注释** | 新增 Go 回归测试 13 个（jsonops 双方言含 PG 真实执行 + store 修复项）；e2e P3/P5 异步竞态断言修复（即时读取→轮询等待）；前后端注释覆盖复查补齐（Go 导出函数 100%、前端组件/工具函数补 11 处） |
| **验证** | go build/vet/test 全绿；**SQLite 与 PG 双方言 UAT 均 67/67 + 79/79、e2e 16 过 3 刻意跳（tenant_label 产品缺口）**；tsc 干净、vitest 24/24 |

## 〇-XXIX、P0-P2 全链路修复 + Webhook 重试/死信机制（2026-09-11，提交 60edcfa）

> 本次迭代完成安全审计 P0 修复、前端质量 P1 改进、Webhook 重试/死信全链路实现，共 25 文件变更、1368 行新增、295 行删除。

| 块 | 内容 |
|---|---|
| **P0-1 ErrorBoundary** | 新增 `components/ErrorBoundary.tsx` 全局错误边界，组件崩溃时显示降级页+重试按钮；`App.tsx` 根组件包裹 `<ErrorBoundary>` |
| **P0-2 XSS 修复** | `MessageBubble.tsx` 的 `escapeHtml()` 补充 `"` `'` 转义；`javascript:void(0)` 改为 `<button>` 元素 |
| **P0-3 Token 安全** | `api/core.ts` 从 `localStorage` 改为 `sessionStorage`（关闭浏览器即清）；`stores/admin.tsx` 的 `clearAuth()` 同步清理 sessionStorage + 兼容清理旧 localStorage |
| **P0-4 React Router** | `App.tsx` 从手搓路由迁移至 `react-router-dom`（BrowserRouter + Routes/Route）；React.lazy 代码分割（主 chunk 1.3MB → 527KB，Admin 307KB 懒加载） |
| **P1 SSE 去重** | `api/translate.ts` 提取 `consumeSSEStream()` 公共函数，消除 chatStream/translateFileStream 重复 SSE 解析 |
| **P1 i18n 补全** | `selfservice.tsx` 4 个面板 30+ 键值走 `useT()` i18n（`i18n/dicts.zh.ts` + `dicts.en.ts` 新增 `ss.*` 键） |
| **P1 roleLevelSafe** | 提取到 `lib/ui.ts` 导出函数，消除 App.tsx 本地重复定义 |
| **P1 E2E 修复** | `tenant_label.spec.ts` 改用已有 UAT 用户 + `test.skip` 标记未实现的 Header 租户 Tag |
| **Webhook 重试/死信** | 后端：新建 `webhook_deliveries` 表（投递历史/死信队列）；webhooks 表新增 `max_retries`/`retry_interval`/`last_delivery_at`/`failure_count`；`postWebhooks` 每次投递写入 deliveries 表；新增 `/api/webhooks/deliveries` 和 `/api/webhooks/retry` API；`WebhookDelivery` 结构体 + `ListDeliveries`/`GetDelivery`/`RetryDelivery`/`GetDeliveryStats` 方法 |
| **前端 Webhook 管理** | `WebhooksP` 面板新增投递历史 Drawer + 统计卡片（总/成功/失败/死信）+ 重试按钮 + 连续失败列 + 重试策略表单字段；`webhooks.ts` API 客户端扩展 |
| **自动化测试** | `webhooks_test.go` 新增 8 个单元测试（投递记录/统计/重试/越权防护/策略字段）；`api_uat_txn.sh` 补充 webhook delivery/retry 端点测试 |
| **验证** | go test 全通过 | tsc --noEmit | vitest 24/24 | UAT 67/67 后端 + 16/16 前端 |

## 〇-XXVIII、行业字典超管可创建/维护 + 全站下拉动态拉取（2026-09-10，提交 9f0c7bc / fcc4e4b）

> 产品要求超管能创建和维护行业，不再受前端硬编码 `INDUSTRY_META` 限制。主张「行业 = 平台行业包（pack_type=industry、宿主租户0）+ 全站下拉动态化」：行业字典由超管在后台「行业管理」面板在线维护（code 全局唯一、名称、启停、删除带引用保护），注册页/租户表单/数据采集三处行业下拉全部改为动态拉取。已部署主站+演示站（二进制 `447ad23f...`、前端 `index-C4IRj-0V.js`；行业 API 全链路演示站真机验证通过，主站/演示站 register-config 均返回 9 个启用行业）。

| 块 | 内容 |
|---|---|
| **数据模型** | 行业以 `kb_packages` 平台行业包为承载（`pack_type=industry`、宿主 `SharedHostTenant=0`）；启动迁移幂等创建 `general` 兜底行业（注册回落用）。主站现有 9 行业：general/auto/realestate/b2b/education/ecommerce/wedding/retail/media |
| **后端 store** | `kbpackages.go` 新增：`ListIndustries`（全行业列表，供面板与下拉）、`IndustryCodeExists`（code 全局唯一校验）、`UpdateIndustry`（改名）、`ToggleIndustry`（启停）、`DeleteIndustry`（连带清理条目/安全句）、`IndustryReferenced`（`tenants.industry` 引用保护，被引用禁止删除） |
| **后端 handler** | `admin_kb.go` 新增 5 个行业 handler：`handleIndustries`（列表，L3 以上可见，附条目计数）/ `handleIndustryCreate`（仅超管，code 仅小写字母/数字/下划线，大写自动规范化，重复拒绝）/ `handleIndustryUpdate` / `handleIndustryStatus`（enabled=0/1）/ `handleIndustryDelete`（引用保护→400，非行业包拒删）；全部 `LogAudit` + `invKB` 失效缓存 |
| **公开注册字典** | `handleRegisterConfig` 公开返回启用中的行业列表（`industries` 字段，停用行业不返回）——注册页行业下拉的动态数据源，无需登录 |
| **前端面板** | 新建 `IndustriesP.tsx`：知识库面板「🏭 行业管理」Tab（仅超管显示）——行业表格（code/名称/条目数/启停状态）+「＋ 新建行业」弹窗（code+名称）+ 编辑改名 + 启停 Switch + 删除（引用保护提示）；`api/industry.ts` 封装 6 个接口 |
| **下拉动态化** | 登录/注册 `Login.tsx`（走 register-config 公开字典，硬编码兜底）、租户表单 `panels_b.tsx`（`industries()`，含「未设置」空项，创建/编辑均直传 code）、数据采集待审筛选 `DataSourcesP.tsx`（`industries()`，空时兜底硬编码）；`tenantCreate`/`tenantUpdate` 补 `industry` 传参 |
| **自动化测试** | `store/industry_test.go`（CRUD + 引用检测）；`api/industry_api_test.go` 4 个测试：超管全链路（创建规范化→列表→改名→启停→删除）、code 校验（非法字符/重复 → 400）、非超管 403、被引用删除 400；`TestRegisterConfigIndustries`（fcc4e4b 补）：公开字典新建可见、停用立即隐藏 |
| **验证** | 后端 `go build/vet/test -race` 全绿、前端 `tsc --noEmit` + vite build 通过；演示站真机：登录→列表(9行业)→新建 deploytest→改名→停用(register-config 立即减为 9)→删除→确认已清除；主站/演示站 `/api/health` 200、0 panic |

## 〇-XXVII、品牌名统一翻译 + 知识库品牌名前端可配（2026-09-10，提交 441c387 / 62944b5 / 97daaae / 2dff3cf / 5732fab）

> 产品要求品牌名一律等于 KB 规定译法（极石/极石汽车→ROX），不得出现 "ROX vehicles"/"ROX motor"/俄语音译 Киджиш 等自创写法。主张「复用品牌概念、前端可见、知识库单独可配」：后端品牌术语归一化 + 翻译前品牌保护，前端知识库面板新增「🏷️ 品牌名」Tab。已部署主站+演示站（二进制 `817563c9...`、前端 `index-D7QQoHnj.js`，deploy_check 双站 200/0 panic）。

| 块 | 内容 |
|---|---|
| **归一化函数** | `gate.NormalizeBrandTerm` 两步正则替换（先剥后缀词块、再剥前缀词块），覆盖四种形态：品牌+后缀（"ROX vehicles"）、后缀+品牌（"Автомобили ROX"）、前后环绕（"سيارات ROX"）、多后缀连写（"ROX Motor Car"）；保留品牌旁分隔符防中阿/俄文粘连；词表覆盖英/俄/西/阿语车辆词，内置首字母大写变体（`(?i)` 不折叠西里尔）、短词加 `\b` 防 "auto" 误切 "automóviles"；收尾压缩连续空白清理标点前空格。16 用例测试全绿 |
| **对话路径** | `text.go` `normalizeBrandTerms`（译后收尾，AI 校对与重翻之后、约束闸门前）；`engine.go` `St` 注入修复——Engine 构造时本不持有平台 store 导致归一化恒空转，`NewServer` 时 `eng.St=st` 注入 |
| **文件路径** | `file.go` 三处 BatchTranslate（KB 补漏/directOther/重试循环）前做**翻译前品牌保护** `protectSourceByLang`——把源文品牌名（极石/极石汽车，长词优先）替换为规定译法传输给模型，从根杜绝音译（音译无法靠事后剥后缀修正）；`fetchBrandTerms` 一次查询全部 brand 术语；`normalizeFileBrandTerms` 复用该映射做译后剥后缀兜底 |
| **数据** | 两库（主站/演示站）「极石汽车」5 条 layer=2 错误译法（`kk→ROX Motor`/`de→ROX Auto`）改回 ROX；按「极石」模板补 INSERT 34 语言 layer=1 brand 术语（极石汽车→ROX），双库验证 34/34 |
| **后端接口** | `store.ListBrandTerms` + `handleBrandTerms`（`GET /api/admin/brand-terms?package_id=`，返回 package 下 module=brand+layer=1 术语）；server.go 注册路由 |
| **前端** | `BrandTermsP.tsx`（知识库面板「🏷️ 品牌名」Tab）：自选知识库包 → 按品牌名分组表格展示各语言译法；「＋ 新增品牌名」弹窗按 21 语言批量写 ROX；单语言补录/修改/删除；`kb.ts` `brandTerms()` |
| **验证** | 对话 pro 三语（en/ru/ar）「极石汽车驰骋全球山海」全为纯 ROX；文件工单 91 RU 输出 "ROX покоряет горы и моря по всему миру."（此前音译 Киджиш）；工单 92 日志确认「文件路径加载品牌术语 34 个语言」；后端 build/vet/test/-race 全绿、前端 tsc/vite build 通过 |
| **回归测试（5732fab）** | `brand_term_test.go` 引擎方法回归：`fetchBrandTerms`（按语言聚合 src→target、非 brand/非 layer1 过滤）、`normalizeBrandTerms`（对话路径多形态后缀剥离：ROX vehicles/Автомобили ROX/سيارات ROX）、`normalizeFileBrandTerms`（文件译后兜底：命中修正/未命中不动/无命中返回0）；复用 `newTestStore` 内存 SQLite 基建，全仓 go build/vet/test/-race/tsc 全绿 |

## 〇-XXVI、翻译可靠性改造 + 开发遗留改进 + 双站部署（2026-09-10，提交 fb9984e / 3f78a46 / c06b29e）

> 本批次三连：①写回不降级+自动重试 + 漏译率硬闸 + 单语失败排队尾重试（对话阿语等偶发语言不再整段缺失、文件管线不再把「翻译 PDF 却交付 Excel」）；②PDF 中文单字残留修复；③两个遗留改进（对话余额不足明确提示、工单建单余额预检）。全部已部署主站+演示站。

| 块 | 内容 |
|---|---|
| **写回不降级（fb9984e）** | pdf/docx/pptx/txt/csv/md 写回失败自动重试 3 次（2s 退避），仍失败置工单失败并清理半成品；srt/vtt/json/yaml/yml 无原格式回写能力，以 xlsx 对照表为唯一交付形态（设计如此，非降级） |
| **漏译率硬闸** | 某语言未译出 >50% 直接置工单失败（注明段数与原因），绝不交付大面积漏译产物；≤50% 维持既有警告口径。抽纯函数 `leakedLang` + `writebackDelivery` |
| **单语失败排队尾重试** | `translateLangsConcurrent` 改轮次化重试队列：失败语言排到队尾（其余语言先行），每语言最多 3 次、轮间 2s 退避、ctx 取消即退出；新增 `singleLangFn`/`retryMaxAttempts`/`retrySleep` 测试钩子 |
| **PDF 中文单字残留** | `docx_translate.py` 提取放宽为单字段落也进翻译键（len<2→len<1）：pdf2docx 偶发把词拆成单字段（「保险」→「保」+「险」），旧过滤使单字从不翻译、写回残留中文（实测工单84残留 无/查/险/贵 等）。修复后同一文件重翻 0 汉字残留 |
| **改进1：余额不足提示（3f78a46）** | 对话翻译被实时计费中止时，回复明确提示「余额不足请充值」，不再静默缺失语言。`WithUsageRecorder` 改 `context.WithCancelCause`，中止注入 `store.ErrInsufficientBalance`，`text.go` 结果组装读 `context.Cause` 追加提示 + gateWarnings |
| **改进2：工单建单余额预检** | 文本/文件工单建单前用源字符数估算 token 消耗（`estimateTicketTokens` 纯函数），超出剩余余额直接拒绝并提示充值；文件预检失败清理已上传文件。`precheckTicketBalance` 内部归还并发名额 |
| **自动化测试** | `file_gate_test.go`（leakedLang/writebackDelivery 边界）、`retry_queue_test.go`（失败重试至3次/成功即停/第2轮恢复/取消即退/跳过已有 5 场景）、`abort_reason_test.go`、`ticket_balance_test.go`（5 边界+单调性）；全仓 go build/vet/test/-race 全绿 |
| **演示站坑复盘** | 演示站曾变频失败+只出俄语：根因是租户1 影子余额被扣成负数触发实时计费 abort（`context canceled`），DB model_routes key 本身有效；充值 100 万 token 后恢复。调查中新排除的嫌疑：secrets.env 缺 SILICONFLOW_API_KEY（实际用 DB 路由 key，直连有效） |
| **部署** | 双站二进制 md5 `9208a15811d4d57b84bd83d54f97caea`（GOOS=linux）；mv 原子替换 + systemctl restart；两站 /api/health 200、无 panic；冒烟：对话 ru 正常、文本工单 T20260910083521DHY 审批队列正常 |

## 〇-XXV、套餐升级功能（旧包抵扣+剩余额度等价转入新包）（2026-09-09，提交 09a5885 / bc655f4）

> 超管后台套餐管理新增「升级」能力：企业租户已订阅低档付费包时，目标付费包（售价更高）显示「升级」按钮。升级时旧包剩余价值（按剩余 token 台账折算）冲抵新包应付金额，旧包剩余 token 作废并等价转入新包台账，新包即时生效。

| 块 | 内容 |
|---|---|
| **数据模型** | `orders` 迁移列 `upgrade_from_order INTEGER NOT NULL DEFAULT 0`（旧包订单）+ `credit_money REAL NOT NULL DEFAULT 0`（抵扣金额） |
| **后端核心** | `billing.go`：`Order` 结构体新增 `UpgradeFromOrder`/`CreditMoney`；`ComputeUpgradeCredit`（剩余台账折算 + 校验：有生效订阅 / 目标非当前同包 / 目标为付费包 / 目标价高于当前包）；`CreateUpgradeOrder`（应付=新价−抵扣）；`MarkOrderPaid` 升级分流（作废旧台账 `left=0` + 新台账 `pkgTokens+旧剩余` + `PackageCode` 切换 + 到期重算） |
| **接口** | `POST /api/package/upgrade`（`handlePackageUpgrade`，含 mock/manual 支付渠道、`LogAudit("package_upgrade")`、响应含 `credit_money`）；server.go 注册路由 |
| **前端** | `billing.ts` `packageUpgrade`；`panels_c.tsx` `upgrade()` + `isUpgradePlan`（目标 paid 且价 > 当前包价 → 按钮显示「升级」）；i18n 中英 `plans.upgrade/upgradeTitle`、`billing.upgradeConfirm/upgradeCredit` |
| **自动化测试** | store 层 `TestPackageUpgrade`/`TestPackageUpgradeRejections`（充值抵扣/等价转入/边界拒绝）；API 层 `package_upgrade_test.go`（`newUpgradeTestServer` + subscribe/upgrade/me/package 四接口集成覆盖，含 token 句数口径 `TokenSentenceRate`/`MarkupMultiplier`） |
| **验证** | `go build/vet/test ./internal/...` 全绿、前端 `tsc --noEmit` 通过。★ 2026-09-10 已随本轮批次部署主站+演示站（含升级路由） |

## 〇-XXIV、QA 数字误报修复 + 输出契约白名单 + CJK 严格截断 + zh 互译清空 bug（2026-09-09，提交 78db1b9 / f0137d6）

> 三线并进：①工单 QA 数字误报（讲解式注释残留未被剥离，实测「…该操作。」→" ，。"）；②模型在译文后追加讲解/术语对照的注释残留根治；③QA 枚举序号豁免。已部署主站+演示站（二进制 `feae5240...`、前端 `index-mHz-uh0X.js`，deploy_check 双站 8/8）。

| 块 | 内容 |
|---|---|
| **QA 数字误报** | `stripTrailingCJKNotes` 增强（`leadingCJKNoteRe` 以全角标点开头），工单 T20260909111254JZ7 场景回归测试 |
| **输出契约** | prompt 要求模型用 `<t>…</t>` 包裹最终译文；`extractContractTranslation`（postprocess.go）置于 `PostProcessTranslation` 首步，白名单提取只取标签内内容——标签外注释块整体丢弃，对 zh/zh_hant/ja/ko 等黑名单无法区分注释与正文的目标语同样有效；违约时原样返回零回归。注入点：`singleLang`/`translateWithFeedbackEx`/`ReviewTranslation`/全量重翻（不加批量 `<sN>` 与续翻路径） |
| **CJK 严格截断** | zh/zh_hant/ja/ko 用 `leadingCJKNoteStrictRe`（`^[（\[【]?\s*：`）截断注释块；刻意不含「注：/说明：」词头防误删源文 |
| **QA 枚举豁免** | 译文多出恰为 1..N 连续序号 → warning；缺数字/非连续/小数 → error |
| **zh 互译清空 bug** | `StripChineseInNonZh` 守卫漏 `zh`：en→zh 互译中文被删空（实测「，。」）——已修复 + 回归测试 |
| **页眉租户名** | 前台 `App.tsx`（`myTenantName`）与后台 `AdminDashboard.tsx` 移除页眉租户名标签（需求） |
| **验证** | 全仓 go build/vet/test 全绿；已部署双站（7 文件、309+/17-） |

## 〇-XXIII、RAG 硬闸补 KB 术语遵循校验 + xlsx 行对齐 + 演示站 PDF 管线修复（2026-09-09，提交 95387bc / 979ea03）

> 用户指出架构缺口：「既然叫 RAG，硬闸就该复查 KB 命中是否被使用」。知识库已定义 `极石→ar→ROX`（id 41164）与 `极石→ru→ROX`（41163），但模型仍自创 `«جي شي» (ROX)`。根因有二：①`FindEntriesBySourceScoped` 整段精确匹配永远命不中嵌在长句里的单条 L1 术语（术语从未进 prompt）；②硬闸 `gate.Run` 无术语遵循校验。另修工单 T20260909165612KWB（Excel 导出两行重复整段译文）与演示站 PDF 翻译输出 Excel。

| 块 | 内容 |
|---|---|
| **术语子串匹配** | store 新增 `FindTermsBySubstring`：`layer=1` L1 术语按源文子串 LIKE 命中（按包优先级 + 原文长度降序），与整段精确匹配互补；`runKBMatch` 把命中术语转 `kb.Row` 并入 `p.Examples` → ai_initial 初翻 prompt 注入术语参考 |
| **硬闸术语遵循** | gate 新增 `RunWithTerms`（第 9 项「术语遵循」）：源文含命中术语、译文未含规定译法 → 判不通过；`runGate` 接线 + `retranslateWithKB` 附带命中术语重译（≤ gate_retry_max 次，默认 8） |
| **xlsx 行对齐** | `tickets.go` 文本工单下载原「源文按 `\n` 拆行、每行填整段译文」→ 译文重复输出。抽纯函数 `alignTicketRow`（复用 `extractTextSegments` 按行映射）：同段数逐行对应，不一致时首行填整段其余留空 |
| **演示站 PDF 管线** | demo 站 `bin/` 缺 `docx_translate.py`/`pdfwrite.py` 脚本（`docxScriptPath()` 按二进制同目录查找），PDF 写回必失败 → 降级 xlsx。已补脚本（主站/venv/LibreOffice/字体均在）；实测工单 79 `file_writeback success` |
| **自动化测试** | `gate_test.go`（术语遵循 5 场景：未遵循拦截/含译法通过/源文不含不适用/nil 等价 Run/多术语部分未遵循拦截）；`packages_test.go`（`TestFindTermsBySubstring` 子串命中/空源/多语言）；`editor_test.go`（`TestAlignTicketRow` 回归工单 78 两行场景） |
| **验证** | 全仓 go build/vet/test 全绿、前端 tsc 通过。提交仅代码+测试（不含文档/流程图） |

> 本轮双线推进：**联调修复 4 项注册/权限问题**（全部联调实证 + 新增自动化测试）与**两条技术债治理**（①进程内存态组件横向扩容改造已落地验证；②生产 systemd 沙箱迁移 ✅ 2026-09-09 已在服务器执行落地，见《部署指南.md》§六）。

### A. 注册/权限 4 项问题修复（全部联调实证）

| # | 问题 | 根因 | 修复 |
|---|------|------|------|
| 1 | 企业新用户注册后「进入平台根（翻译平台）」，看不到自己企业 | **展示层缺陷，非数据错位**：注册实际已建租户（租户#N、tenant_admin、JWT tid=N、meContext tenant_name 正确）；但前台顶栏只显示平台品牌（回退「能言/智能翻译平台」）、后台顶栏只显固定「企业管理」标签，用户看不到自己的租户名 | 后端 `handleTenantInviteEnabledGet` 补返回 `tenant_name`；前端 `App.tsx` 顶栏品牌旁叠加租户名 Tag、`stores/admin.tsx` 暴露 `tenantName`、`AdminDashboard.tsx` 管理范围标签显示租户名 |
| 2 | 超管平台视图无法改用户角色/组织（静默失败） | `UpdateUser`/`ResetPassword` 为 `WHERE id=? AND tenant_id=?`，超管平台上下文 `effTenant=0` 匹配不到目标行 | `handleAdminUserUpdate`/`handleAdminUserResetPassword`/`handleAdminUsers` 在平台上下文（tid≤0）用 `ListAllUsers()` 按 ID 解析用户真实租户后再操作 |
| 3/4 | 通用行业 KB 包层级、个人用户权限边界与组织/KB 层级 | 个人与企业用户在 KB 包可见性上未区分；「通用行业」未聚合 | 按澄清设计落实：**通用行业包 = 所有行业包之和**（`sharedFilterSQL`/`BuildPackScope` 对 `industry=general` 装配全部行业包）；**个人租户 KB=行业-个人**（`is_personal=1` 滤除部门/跨部门/企业层包）；新增回归测试 `TestScopeGeneralIndustryAggregation` |

> ★ 联调实证（临时库 dev3.db）：企业注册→租户#2「Hajimi」、tenant_admin、JWT tid=2、meContext tenant_name="Hajimi"；超管平台视图给 hajimi 改角色 `dept_admin` 落库成功；个人租户见 企业包+通用/汽车/医疗行业包+文化包（无部门包）、企业 auto 租户保留部门包全层级。

### B. 技术债① 进程内存态组件横向扩容改造（已完成 + 双实例冒烟验证）

| 组件 | 改造 | 位置 |
|------|------|------|
| 验证码（resetCodes/emailCodes） | Redis 键值 + 内存兜底（`vcodeSet/vcodeGet/vcodeDel`），双写双删跨实例共享 | `api/vcode.go`（新）、`auth.go`、`email_verify.go` |
| quotaByTenant（QPS/并发） | 并发=`concurrency.Semaphore`（Redis 槽位/SETNX，`TryAcquire` 返回释放闭包配对）；QPS=Redis 秒窗 INCR+EXPIRE，未启用降级本地滑动窗口 | `billing/quota.go`、`api/billing_api.go` |
| startPackScraper 采集调度 | 分布式锁 `distlock("scrape:lock")`（`scrape_lock_ttl_sec` 可配，默认 1800s），抢锁失败跳过本轮 | `api/packscraper.go` |
| loginLimiter/registerGuard | 已有 `rate_limits` 表持久化，确认即算完成 | `api/ratelimit.go`、`register_guard.go` |
| metrics/缓存 | 进程内保留（Prometheus 侧聚合 / 只读派生可重建） | — |
| Redis 客户端 | 新增 `Set(key,val,ttl)` 原语 | `infra/redis/redis.go` |

> ★ 双实例冒烟（本地两进程连同一 Redis）：验证码 A 生成 B 校验成功、采集锁 SETNX 互斥、QPS 秒窗第 6 个 BLOCK；新增 `billing/quota_test.go`（-race 通过）、`api/vcode_test.go`。

### C. 技术债② 生产 systemd 沙箱迁移（✅ 2026-09-09 已落地服务器，详见《部署指南.md》§六）

- `deploy/systemd/prod.conf`：更新为最终沙箱形态（GOMEMLIMIT=850Mi / MemoryMax=1150M / WORKER_CONCURRENCY=4 / PDF_FONT_PATH / 可写目录白名单收窄）
- `deploy/systemd/migrate_sandbox.sh`（新）：一键迁移脚本 `check`/`migrate`/`rollback` 三模式，含自动备份、JWT_SECRET 一致性校验、旧 6 drop-in 清理
- `deploy/deploy_check.sh --systemd`：沙箱本地验收（进程归属/密钥隔离/沙箱/旧drop-in 四项）

### D. 自动化测试（本次新增）

| 测试 | 覆盖 |
|------|------|
| `api/admin_superadmin_scope_test.go` | 问题2：超管平台根上下文改角色/重置密码/列用户命中真实租户 + 非超管跨租户 403 |
| `api/tenant_name_test.go` | 问题1：企业/个人租户 invite-enabled 返回租户名与 is_personal、超管切换生效租户 |
| `api/vcode_test.go` | 技术债①：验证码键前缀契约、Redis 未启用降级路径 |
| `store/kbpackages_scope_test.go` | 问题3/4：通用行业聚合、个人租户包收口（本会话已含） |
| `frontend-react/e2e/tenant_label.spec.ts` | 问题1渲染冒烟：企业/个人顶栏租户名 Tag + 后台管理范围标签（3/3 通过） |

> ★ 验证：后端 `go build/vet/test ./...` 全绿、前端 `tsc --noEmit` 通过；pixel_uat 7/8（P2 翻译全链路需真实 LLM key，环境依赖）；渲染冒烟截图 `frontend-react/artifacts/tenant_label_*.png`。
> 提交：本次仅代码 + 测试（不含文档/流程图），详见 git。

## 〇-XXI、主站/演示站移动端自适应（2026-09-07，提交 e564079）

> 主站（langcross.lexicorn.cn）与演示站（rox-test.lexicorn.cn）此前几乎无移动端适配：后台侧边栏固定 232px 撑破窄屏、聊天输入栏单行溢出、登录卡/宽表格超屏。本次按「断点 900px/640px + 组件级改类名」方案补齐移动端体验，并新增自动化核对。

| 块 | 内容 |
|---|---|
| **新增 mobile.css** | `frontend-react/src/styles/mobile.css`（叠加在 theme.css 之上）：登录卡 400px→全宽、分栏布局折叠单列（隐藏背景图）、登录卡容器转静态居中；前台头部允许换行收窄、窄屏隐藏余额徽标；聊天输入行可换行（语言选择+输入框各占整行）、气泡/头像缩小；后台侧边栏转抽屉（汉堡唤起+遮罩点关）、顶部工具栏换行、Field 字段折叠、表格横向滚动、弹窗不超屏 |
| **组件改造** | `AdminDashboard.tsx` 汉堡+抽屉状态+遮罩（桌面不受影响）；`App.tsx` 主内容区 `app-main` 加 `min-width:0`（根治宽表格把弹性列撑出横向滚动的根因——`margin:0 auto` 使弹性拉伸失效）；`ChatWindow.tsx` 输入行/语言选择类名；`Login.tsx` 分栏/卡片类名；`parts.tsx` Field 折叠类名；`EditorPage.tsx`/`TicketsPage.tsx` 包裹层 `width:100%+minWidth:0`、对照编辑器双栏→单栏 |
| **公开页（后端渲染）** | `public.go` 定价/协议/隐私页内联 CSS 补 `@media(max-width:720px)`（头部换行、卡片收窄、宽表格横向滚动、套餐卡单列） |
| **自动化核对** | 新增 `e2e/mobile_uat.spec.ts`（390×844 手机视口）：后台抽屉（汉堡可见/侧栏移出屏/滑入/遮罩/点菜单关闭）+ 工作台输入栏 + 全站 8 页无横向溢出（billing/invites/packages/my/tickets/editor/pricing） |
| **验证** | 完整 `run_uat.sh`：后端 API **67/67** + 前端 E2E **15/15**（原 12 项无回归 + 新增 3 项移动端）；`go build`/`tsc`/`vite build` 全绿；本次提交仅代码（12 文件），不含文档/流程图 |
| **部署** | ✅ 主站/演示站已上线（2026-09-07）：交叉编译 Linux 二进制 + 前端 dist 替换（流程见《部署指南.md》§五）；主站 12:52 CST 重启、`deploy/deploy_check.sh` 8/8、新 bundle `index-CChVKx0m.js` 生效、重启后 0 panic；演示站经 `bootstrap-demo.sh` 刷新克隆并种入演示账号（demo_admin 等，密码 Demo#2026Rm!）、13:04 重启、`rox-test.lexicorn.cn` 公网移动端免登录核对无溢出。**踩坑两条**：① 服务器旧版 bootstrap-demo.sh 缺 web 目录 `chmod o+rX`（translator 进程读不了 → 首页「前端未构建」），已手动 chmod 并同步新版脚本；② 生产 `users_id_seq` 失步（=1 而 MAX(id)=10007），克隆到演示站后种号即撞主键，已 `setval` 修复生产+演示两库（详见《部署指南.md》§十二） |

## 〇-XXI、运营策略权限收口 + 支付确认链路修复 + 自动化测试（2026-09-07，提交 e09636a）

> 需求 5 项收口落地：① 企业用户彻底隐藏「邀请好友·多邀多得」展示；② 运营时间窗配置 UI 升级（日期组件 + 因子名称/公式速查）；③ 邀请奖励超管设置合并进运营策略引擎；④ 运营策略仅超管可写、作为邀请/任务中心奖励总开关（中台与前台解耦）；⑤ 修复支付确认履约链路（「我已付费」/「确认收款」无响应、演示站二维码不显示）。并补齐 Go + Vitest 自动化测试。

| 块 | 内容 |
|---|---|
| **邀请隐藏** | 个人中心/`/invites` 路由/抽屉菜单/自服务快捷入口按 `is_personal` 隐藏「邀请好友」；ReferralP 删除超管运营配置块（参数并入策略引擎 invite 因子） |
| **任务中心总开关** | `ops_policy.task.enabled`（默认开）：关闭后 `GET /api/me/tasks` 空列表+disabled、`POST /api/me/tasks/claim` 拒绝；OpsP 新增「任务中心奖励因子」面板 |
| **运营策略超管专属** | `POST /api/admin/ops/policy/save` 仅超管（scope 恒 platform）；租户管理员只读；OpsP 按 `isSuper` 只读灰显；后台菜单 ops 提至 minLevel 4 |
| **支付链路修复** | `handleOrderPay` 根因=超管平台上下文 `effTenant=0` 匹配不到订单 → 前端 `adminOrderPay` 显式传 `tenant_id`（代充值两处同步）；「我已付费」/「确认收款」加成功/失败提示；支付模式统一 `effPayMode`（策略 payment.mode，缺省 mock） |
| **二维码渲染** | 新增 `GET /api/qr/render?text=`（go-qrcode 出 PNG，需登录）：收银台对非图片 `qr_content`（mock/wechat/alipay 文本）取 blob 渲染可扫码二维码，演示站不再白屏 |
| **邀请奖励优先级修复** | `ReferralPaidReward` 取值顺序改为 默认→env→存量散键→**运营策略（最高优先）**（此前散键覆盖策略，与文档承诺相反） |
| **自动化测试** | 后端：`ops` task 因子合并/关闭、`store` 邀请策略门禁与付费奖励优先级、`api` 超管权限/任务闸门/effPayMode（ops_gate_test.go）；前端：新增 Vitest + i18n 中英键对等性测试（24 用例，顺带修复 kb/referral/base 漏译与死键） |
| **验证** | `go build/vet` 全绿、`go test ./...` 全绿、前端 `tsc --noEmit` + `vitest run` 24/24 + `vite build` 通过 |
| **提交** | 本次仅代码（30 文件，含新增测试），不含文档/流程图/归档 |

## 〇-XX、运营策略（计费因子流程引擎）+ P1/P2/P3 修复（2026-09-05，提交 df664fb）

> 把「体验额度/邀请奖励/注册频控/限额/翻译模式/文件上限/支付」等计费因子从散落硬编码收敛为**平台统一策略 + 活跃时间窗**，超管在后台「运营策略」面板可视化配置即时生效。顺带修复 UAT 报告 B 阶段暴露的 P1（企业成员邀请加入不可用）/P2（平台包订阅不可达）/P3（计费落库 2s 延迟）三处缺陷，并同步补齐本轮涉及代码的全量中文注释。已部署生产 langcross.lexicorn.cn（备份 `translator-server.bak.20260905_213452`），deploy_check 8/8、重启后 0 panic。
>
> ★ **2026-09-07 权限收口（提交 e09636a）**：运营策略为平台级中台配置，`POST /api/admin/ops/policy/save` 收紧为**仅超管可写**，租户级覆盖停用（租户管理员只读）；新增 `task.enabled` 任务中心奖励总开关；付费邀请奖励并入策略引擎且策略值最高优先（修复散键覆盖策略的优先级 bug）；推广时间窗 UI 升级为日期组件并给出因子公式速查。

| 块 | 内容 |
|---|---|
| **策略数据模型** | `system_config.ops_policy`（平台唯一可写源，含 `promo_windows` 活跃时间窗）；★ 2026-09-07 起租户覆盖 `tenants.policy_config.ops_policy` 停用（仅超管平台级可写，租户只读）；`ops_resets` 记录套餐重置计数（跨月归零）。解析顺序 = 代码内置默认 → 存量散键（free_trial_tokens / inviter_paid_reward_tokens 等）→ 平台 ops_policy → 活跃时间窗（最高 priority 最后应用生效） |
| **因子域** | package（体验 token/天数、月度重置开关与上限）、invite（总开关、注册/付费奖励 token 与天数、日发奖上限）、task（任务中心奖励总开关，2026-09-07）、registration（注册开关、同 IP 间隔/日上限、邮箱验证）、limits（全局 QPS/并发、新租户默认日字符/token 上限）、mode（fast/pro 翻译模式：charge 计费/免费、markup、limit_chars、enabled）、payment（渠道、下单即到账）、content（文件翻译上限 MB） |
| **后端** | `internal/ops/policy.go`（OperationsPolicy patch 模型 + EffectivePolicy + Merge/ActiveWindows 解析）；`tenant.WithMode/ModeFromContext` 模式透传；`ChargeUsageRealtime` 模式感知（charge=false → `Store.LogUsage` 免费留痕、markup 按模式、enabled=false 中止）；对话/文件 `limit_chars` 闸门；邀请奖励/注册开关改读策略因子（含 `invite.enabled` 总开关门禁）；`internal/api/ops_api.go`（GET /api/admin/ops/policy、POST policy/save、window/save、/api/admin/billing/package/reset） |
| **前端** | 超管后台新增「运营策略」面板（`panels_e.tsx` OpsP + `api/ops.ts` + i18n zh/en），策略/时间窗表单保存即时生效 |
| **P1 修复** | 企业成员邀请加入：加入路径保留 `joinWithInvite` 标记走成员创建分支（role=user、同租户重名 400、`MarkInviteCodeUsed`），不再误清空邀请码 |
| **P2 修复** | `GetPackageByCode` 改 `tenant_id IN (0, ?) ORDER BY CASE WHEN tenant_id=? THEN 0 ELSE 1 END, id LIMIT 1`：租户包优先、平台包回落；新增回归测试 |
| **P3 修复** | `billing/sink.go` 新增进程级 `Flush()`，响应前显式落库（对话/文件/余额/OpenAPI 同步 4 处调用点），消除对账 2s 窗口；`TestSinkFlushSynchronous` 锁定 |
| **UAT** | `scripts/uat/api_uat.sh` 新增 B 阶段 18 断言（策略 GET/保存、fast 免费不扣账+留痕、limit_chars 拦截+pro 不受限、平台包订阅、套餐重置+二次限额、邀请奖励 +900000、P1 成员加入 3 项、时间窗覆盖免费）。全量结果：后端 A 49/49 + B 18/18 = **67/67 全绿**，前端 E2E 12/12，`go test ./...` 全绿，改动文件 gofmt |
| **注释** | 前后端代码补齐全量中文注释（本轮涉及文件 + ops 包/ops 面板）；扫描确认全仓 Go/TS 导出符号均有中文注释 |
| **部署** | 交叉编译 Linux amd64 → scp → 备份 → 替换 bin/web → `systemctl restart translator`；服务 active、dialect=postgres、新路由未登录 403、公开页 200、重启后 0 panic；本次提交仅代码（38 文件），不含文档/流程图 |

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
| **邀请裂变个人限定** | 邀请付费奖励（多邀得多）仅个人用户（is_personal=1）可得，企业租户后端跳过发放；★ 2026-09-07 起企业用户/平台超管在前端**彻底隐藏**「邀请好友」入口（个人中心 tab、路由 /invites、抽屉与快捷入口全部隐藏），不再保留「后台配置」块（邀请奖励参数已并入运营策略引擎） |
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

**明确不做（当前过度设计）**：Redis Cluster / etcd / gRPC Sidecar / K8s 微服务拆分 / 多区域；混沌工程 / SDK 自动发布流水线——等规模化运维诉求出现再做。（原列的 SSO/SCIM/白标/CAT 插件已于 2026-09 落地：SSO/OIDC、SCIM 2.0、租户品牌子域名、TMX 双向交换 + VS Code 插件，见 〇-XXXI；Chrome 商店上架维持不做，自托管扩展即可。）

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
| 计费 | Token 实时计量（每次 LLM 调用即上报用量；余额扣除受 billing_enforced 控制，billing_enforced=0 暂未启用扣费，超管随时开启；★ 2026-09-12 欠费策略定稿：批量结算遇余额不足即「双桶清零停用+billing_exhausted 告警」，已消耗成本按扣到归零结算、不补扣，充值后从 0 重新计量；对话/流式即时提示真因，文件/文本工单建单前按源规模预检余额）；**运营策略（计费因子流程引擎）**已上线——平台统一策略 + 活跃时间窗，★ 2026-09-07 起仅超管平台级可写，超管后台「运营策略」面板可视化配置，因子覆盖体验/邀请/任务中心/注册/限额/翻译模式/支付/文件上限（详见 〇-XX） |
| 部署脚本 | 后端交叉编译（GOOS=linux GOARCH=amd64）→ scp 二进制；前端 `npm run build` → scp dist 静态资源；ssh 重启 `translator.service`（详见 README 快速开始） |

## 二、核心能力（全部已上线）

- **多格式文件翻译**：docx/pptx/xlsx/pdf/txt/csv/md 输出译文文件（写回失败自动重试、不降级）；srt/vtt/json/yaml 等以对照表（xlsx）形式交付；多文件混合工单、多目标语言打包 zip；某语言漏译 >50% 直接置工单失败拒交残缺产物
- **PDF 保真翻译管线（两阶段，a1a5aad）**：
   1. `extract`：pdf2docx 转 DOCX 并提取段落键（含表格/嵌套/文本框/页眉脚）
   2. LLM 翻译段落键 → `apply`：在缓存 DOCX 上 w:t 级替换（图片/排版零破坏）+ LibreOffice 转回 PDF（★ 图片内容按产品策略不翻译，OCR 已移除）
    - ⚠️ 已知限制与缓解：超大 PDF（`pdf2docx + LibreOffice` 转 PDF 易超时/卡死）已由上传前置拦截兜底——**PDF 体积 >40MB 或页数 >120 页直接友好拒绝并提示转 docx**；转换子进程标记为 OOM 优先受害者，超限快速失败而非挂死整机。常规 PDF 可稳定翻译，超大/扫描件仍建议优先上传 `.docx` 源文件
- **双模式**：⚡快速（AI 初翻+校对）/ 🎓专业校对（知识库+评估+硬闸全流水线）
- **计费体系（Token 实费 + 双桶台账）**：额度=发放台账（quota_grants，带到期可叠加）+ 永久余额（balance_accounts）；扣减顺序「台账近到期行→永久余额」事务原子；部门预算墙、套餐/订单/发票
- **邀请裂变**：个人邀请码+专属链接+二维码；被邀人注册→邀请者体验叠加(+30万，有效期默认14天、后台可调)；首笔付费套餐→邀请者+50万（token 数与有效期均后台可调：默认永久余额，可改为限时台账）；同对每种奖励仅一次。**仅个人用户（is_personal=1）可获得邀请奖励（含多邀得多付费奖励），企业租户后端跳过发放；★ 2026-09-07 起企业用户/平台超管前端彻底隐藏「邀请好友」入口，邀请奖励参数并入「运营策略」面板（invite 因子，超管平台级可配）**。
- **注册与邮件体系**：自助注册拆分为「个人 / 企业」两类（个人注册自动生成租户编码与名称并标记 is_personal；企业注册进一步区分「我是管理员（新建企业）/我是普通成员（凭有效企业邀请码加入）」，无效或非企业邀请码自动降级为个人用户）。企业注册发送欢迎邮件并抄送管理员邮箱。超管可在后台「邮件模板」面板配置多用途模板（注册验证码 / 密码重置验证码 / 企业注册成功提醒 / 租户管理员通知 / 系统告警 / 产品手册）；注册成功自动向用户发送《产品手册》PDF 邮件，附件读取外部 PDF 文件（默认 `/opt/translator/data/manual.pdf`，可用 `system_config.manual_pdf_path` 或环境变量 `MANUAL_PDF_PATH` 指定），经专用邮箱 `info@lexicorn.cn` 发送；中文邮件主题用 RFC2047 编码、正文 base64 编码。
- **TM 自闭环**：tm_review 待审池唯一入库通道（超管人工审核通过才落正式 TM）；bitext/tmx/反馈修正/命中达标四来源候选
- **开放 API**：`POST /openapi/v1/tasks` 异步任务 + 轮询 status/download + balance；AES-GCM 密钥加密与一次性明文展示
- **组织架构**：平台根→租户根→组织→部门四级树、拖拽调层级、部门预算徽标弹窗、邀请码绑定组织
   - **管理后台**：三工作台（超管/租管/部门管）、租户切换器、OpenAPI 文档在线编辑（双语）、审计日志、告警中心、记忆审核台、**任务中心（用户领永久 token + 超管自定义每日/一次性任务）**、**个人中心（邀请好友 + 任务中心）**、**外部调用（开放 API + 回调通知）**、**运营策略（计费因子流程引擎，平台统一/活跃时间窗，★ 2026-09-07 权限收口为仅超管平台级可写，新增任务中心奖励总开关）**、**协议签署并入系统设置**（2026-09-03 菜单重组，详见 〇-XI）
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
- **SQLite 并发红线（仅本地开发/旧库适用；生产已切 PostgreSQL，DB_DRIVER=postgres）**：DSN `_txlock=immediate` 全局生效；事务内严禁经独立连接再写库（会撞 busy_timeout 静默失败——UAT-2 教训）；句数镜像等 JSON 读写一律走 `db.JSONNumAdd/JSONNumGE/JSONSetFalse` 双方言助手（★ 2026-09-12：禁止再内联 json_set/json_extract，PG 无 JSON1）；自增 ID 一律 `db.InsertID`（RETURNING），禁用 LastInsertId
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
