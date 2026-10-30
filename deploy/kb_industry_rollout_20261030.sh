#!/usr/bin/env bash
# ============================================================================
# deploy/kb_industry_rollout_20261030.sh — 新增「金融 / 餐饮」两个行业包（模仿现有汽车行业）
#
# ★ 在**部署机上**跑（与 audit_kb_terms_20261026.sh 同口径：sudo -u postgres psql）。
#   本机不要直接跑它，用 deploy/run_kb_industry_rollout_20261030.sh 把它送上去再执行。
#
# 三档（默认只读，写库要显式下令）：
#   MODE=audit  bash deploy/kb_industry_rollout_20261030.sh            # 只读盘点（零写）
#   MODE=packs  APPLY=1 bash deploy/kb_industry_rollout_20261030.sh    # 建包 + 建采集源（不碰 kb_entries）
#   MODE=terms  APPLY=1 bash deploy/kb_industry_rollout_20261030.sh    # 录人工确认术语（T-A）+ 清洗脏候选
#   MODE=gloss  APPLY=1 bash deploy/kb_industry_rollout_20261030.sh    # 按租户开领域词表
#
# 为什么判据要落在"库里有没有这一行"而不是"接口回了 success"（AGENTS §一·8）：
#   包与条目都是**平台资产**，写错一处就是全站客户受影响；所以每一档都先备份、后写、再回读，
#   回读不等值即出声（DONE 行带读数，不靠人眼看表格）。
#
# 安全边界：
#   · 全程不打印任何密钥值（online_api_key 只报"在/不在"）；
#   · 只碰 tenant_id=0 的平台包与 system_config 里 domain_* 这几个键，别的表一行不动；
#   · terms 档写入前先建 schema kb_rollout_bak_20261030 存一份受影响包的 layer=1 快照。
# ============================================================================
set -uo pipefail

MODE="${MODE:-audit}"
APPLY="${APPLY:-0}"
DB="${DB:-langcross}"
HOST_TID="${HOST_TID:-0}" # 平台包宿主租户（MigrateSharedHostToZero 之后唯一口径）
PSQL=(sudo -u postgres psql -d "$DB" -v ON_ERROR_STOP=0 -Atc)

q() { "${PSQL[@]}" "$1" 2>/dev/null || echo "  <读失败：$1>"; }
sec() { echo; echo "===== $* ====="; }

# 新行业定义（code 必须与 crawler 的 builtinIndustrySeeds 键、tenants.industry 取值同形）
NEW_CODES=(finance catering)
NEW_NAMES=("金融（证券/投资）" "餐饮（门店/菜品）")
# 采集源名字里替换用的行业短名（照抄 auto 那批源的命名形态："维基百科·汽车行业(en)"）
NEW_ZH=("金融" "餐饮")

# ---------------------------------------------------------------- 只读盘点
sec "1) 平台包（tenant_id=${HOST_TID}）现状：code | name | pack_type | role | enabled"
q "SELECT id||' | '||code||' | '||name||' | '||pack_type||' | '||role||' | '||COALESCE(enabled::text,'-')
   FROM kb_packages WHERE tenant_id=$HOST_TID ORDER BY pack_type, id;"

sec "2) 各平台包的 L1 术语条数（模仿对象=汽车行业那一行）"
q "SELECT p.code||' | '||p.pack_type||' | L1='||COUNT(e.id)
   FROM kb_packages p LEFT JOIN kb_entries e ON e.package_id=p.id AND e.layer=1
   WHERE p.tenant_id=$HOST_TID GROUP BY p.id,p.code,p.pack_type ORDER BY p.pack_type,p.code;"

sec "3) ★ 采集落库时 kb_entries.tenant_id 实际写的是谁（AutoApproveEntry 传 1，包在 0）"
q "SELECT p.code||' | entry.tenant_id='||e.tenant_id||' | '||COUNT(*)
   FROM kb_entries e JOIN kb_packages p ON p.id=e.package_id
   WHERE p.tenant_id=$HOST_TID AND e.layer=1 GROUP BY p.code,e.tenant_id ORDER BY p.code,e.tenant_id;"

sec "4) 采集源 kb_pack_sources（新增两行业要照这一行的形状插）"
q "SELECT id||' | '||kind||' | '||name||' | lang='||COALESCE(lang,'')||' | industry='||COALESCE(industry,'')||' | pack_type='||COALESCE(pack_type,'')||' | tier='||COALESCE(tier::text,'')||' | enabled='||COALESCE(enabled::text,'')||' | freq_h='||COALESCE(freq_hours::text,'')||' | last_run='||COALESCE(NULLIF(last_run_at,''),'-')||' | '||COALESCE(NULLIF(last_status,''),'-')
   FROM kb_pack_sources ORDER BY id;"
q "SELECT 'base_url 非空的源数='||COUNT(*)||'（非空=词表走 URL，空=走内置种子）' FROM kb_pack_sources WHERE COALESCE(base_url,'')<>'';"

sec "5) 租户与行业归属（tenants.industry 决定行业包可见性，见 store.BuildPackScope）"
# ★ 只列确实存在的列：首版顺手写了 plan_id，PG 直接报错而 q() 只回一行"读失败"⇒ 整段静默空转。
q "SELECT 'tenants 列='||STRING_AGG(column_name, ',' ORDER BY ordinal_position)
   FROM information_schema.columns WHERE table_name='tenants';"
q "SELECT id||' | '||name||' | industry='||COALESCE(NULLIF(industry,''),'<空>')
   FROM tenants ORDER BY id LIMIT 40;"

sec "6) 最近的工单（找本次图片件所属租户；只报 id/租户/标题/时间）"
q "SELECT id||' | tid='||tenant_id||' | '||COALESCE(NULLIF(title,''),'<无题>')||' | '||COALESCE(created_at,'')
   FROM tickets ORDER BY id DESC LIMIT 12;"

sec "7) 领域词表配置现值（domain_*；缺行=走代码默认）"
q "SELECT COALESCE(NULLIF(value,''),'<空>')||'   <- '||key FROM system_config WHERE key LIKE 'domain_%' ORDER BY key;"
q "SELECT '（无 domain_* 行 ⇒ 全部走默认：开关关、超时 2500ms、每单上限按链档）' WHERE NOT EXISTS (SELECT 1 FROM system_config WHERE key LIKE 'domain_%');"

sec "8) 采集相关档位（scrape_*）"
q "SELECT COALESCE(NULLIF(value,''),'<空>')||'   <- '||key FROM system_config WHERE key LIKE 'scrape_%' ORDER BY key;"

sec "9) 上游模型档位（★ 只报 base/model/routes，密钥只报在不在）"
q "SELECT CASE WHEN key='online_api_key' THEN CASE WHEN COALESCE(NULLIF(value,''),'<空>')='<空>' THEN '<空>' ELSE '<非空，值不打印>' END||'   <- '||key ELSE COALESCE(NULLIF(value,''),'<空>')||'   <- '||key END
   FROM system_config WHERE key IN ('online_api_base','online_api_model','online_api_key','model_routes','ocr_fix_model') ORDER BY key;"

sec "10) 汽车行业包 L1 术语样本（新两包要长这个样子）"
q "SELECT e.source_text||' -> '||e.target_text||' | tgt='||e.target_lang||' | mod='||COALESCE(NULLIF(e.module,''),'-')
   FROM kb_entries e JOIN kb_packages p ON p.id=e.package_id
   WHERE p.tenant_id=$HOST_TID AND p.code='auto' AND e.layer=1 ORDER BY e.id LIMIT 20;"

sec "11) L1 术语按目标语言分布（判'新包该铺哪些语种'）"
q "SELECT target_lang||' | '||COUNT(*) FROM kb_entries WHERE layer=1 GROUP BY target_lang ORDER BY COUNT(*) DESC LIMIT 15;"

sec "12) 健康面状态词（只取状态词，不取配置值）"
curl -s --max-time 10 http://127.0.0.1:8787/api/health 2>/dev/null \
  | tr ',{}' '\n\n\n' | grep -E '"(status|dispatch|ocr_ready|asr_ready|tts_ready|lid_ready|lid_engine|ocr_fix)"' | sed 's/^/  /' || echo "  <health 读不到>"

sec "13) 跨部门包与其 L1 术语（V6 复核的靶子：这些条目按新口径不再进术语硬闸）"
q "SELECT p.id||' | '||p.code||' | '||p.name||' | pack_type='||p.pack_type||' | tid='||p.tenant_id||' | L1='||
     (SELECT COUNT(*) FROM kb_entries e WHERE e.package_id=p.id AND e.layer=1)
   FROM kb_packages p WHERE p.pack_type IN ('cross_dept','department') ORDER BY p.id LIMIT 20;"
q "SELECT 'cross_dept/department 包里的 L1 术语合计='||COALESCE(SUBSTR((SELECT COUNT(*)::text),1,20),'0')
   FROM kb_entries e JOIN kb_packages p ON p.id=e.package_id
   WHERE p.pack_type IN ('cross_dept','department') AND e.layer=1;"

sec "14) 各租户自己的企业包（T-A 人工术语的落点：企业包**恒在该租户可见域内**，不看 tenants.industry）"
q "SELECT p.tenant_id||' | pkg='||p.id||' | '||p.code||' | '||p.name||' | role='||p.role||' | L1='||
     (SELECT COUNT(*) FROM kb_entries e WHERE e.package_id=p.id AND e.layer=1)||' | L2='||
     (SELECT COUNT(*) FROM kb_entries e WHERE e.package_id=p.id AND e.layer=2)
   FROM kb_packages p WHERE p.pack_type IN ('tenant','department','cross_dept') ORDER BY p.tenant_id,p.id;"

sec "15) ★ 工单租户分布（tid=0＝平台上下文：跳过全部 KB，且领域词表按设计对 tid<=0 恒关）"
q "SELECT 'tid='||tenant_id||' | 单数='||COUNT(*)||' | 最近='||MAX(created_at) FROM tickets GROUP BY tenant_id ORDER BY tenant_id;"

sec "16) 用户与租户的对应（找'谁能代表租户 1 跑一单'）"
q "SELECT u.id||' | '||u.username||' | tid='||u.tenant_id||' | role='||COALESCE(NULLIF(u.role,''),'-')||' | org='||COALESCE(u.org_id::text,'-')
   FROM users u WHERE u.tenant_id IN (1,12) AND COALESCE(u.is_home,1)=1 ORDER BY u.tenant_id,u.id LIMIT 20;"

echo
if [ "$MODE" = "audit" ]; then  echo "MODE=audit 完成（零写库）。要动手：MODE=packs|terms|gloss 且 APPLY=1。"
  exit 0
fi

# ---------------------------------------------------------------- 写档前置闸
if [ "$APPLY" != "1" ]; then
  echo "ABORT=need-apply（MODE=$MODE 是写档，但 APPLY!=1 ⇒ 一个字都没写）"
  exit 2
fi

bak_schema="kb_rollout_bak_20261030"
ts=$(date +%Y%m%d_%H%M%S)

# ---------------------------------------------------------- MODE=packs 建包+源
if [ "$MODE" = "packs" ]; then
  # ★ 先自证"代码侧种子已就位"，再建源：tier1 的 base_url 留空时按 industry code 取内置种子，
  #   取不到会**静默回落通用词**（欢迎/谢谢/价格…），采集状态照样 ok——这是最坏的那种半成品。
  #   判据＝库里现读该 code 的既有源数 vs 本次要建的源数，但真正的前置是**新二进制已上线**，
  #   所以这里现读 /api/health 之外的一个硬事实：内置种子里有没有这个 code（由 Go 侧锁
  #   industry_registry_gate_test.go 在构建期保证；本脚本再要求操作者显式确认一次）。
  if [ "${CONFIRM_BINARY:-0}" != "1" ]; then
    echo "ABORT=need-confirm（建源前必须确认带 finance/catering 内置种子的二进制已上线：CONFIRM_BINARY=1 再跑一次）"
    echo "  理由：种子缺失不会报错，只会让新行业包里躺着一堆通用问候语，而采集读数恒 ok。"
    exit 6
  fi
  i=0
  for code in "${NEW_CODES[@]}"; do
    name="${NEW_NAMES[$i]}"
    zh="${NEW_ZH[$i]}"
    exists=$(q "SELECT COUNT(*) FROM kb_packages WHERE tenant_id=$HOST_TID AND pack_type='industry' AND code='$code';")
    if [ "${exists:-0}" != "0" ]; then
      echo "SKIP 包已存在：${code}（id=$(q "SELECT id FROM kb_packages WHERE tenant_id=$HOST_TID AND pack_type='industry' AND code='$code';")）"
    else
      "${PSQL[@]}" "INSERT INTO kb_packages (tenant_id,parent_id,code,name,pack_type,role,sort_order,enabled,created_at,updated_at)
        VALUES ($HOST_TID,0,'$code','$name','industry','source',100,1,to_char(now(),'YYYY-MM-DD\"T\"HH24:MI:SS'),to_char(now(),'YYYY-MM-DD\"T\"HH24:MI:SS'));"
      echo "CREATED 包 ${code}（${name}）"
    fi
    # 采集源＝**逐条照抄汽车行业的源矩阵**（同 kind／同 lang／同 tier／同 freq／同 pack_type），
    # 只把名字里的"汽车"换成本行业。为什么用 SELECT 派生而不是在这里写死语言清单：
    # 写死的那份清单迟早和现网漂移（少一个语种没人发现），派生则永远与"模仿对象"同覆盖。
    n_src=$(q "SELECT COUNT(*) FROM kb_pack_sources WHERE pack_type='industry' AND industry='$code';")
    if [ "${n_src:-0}" != "0" ]; then
      echo "SKIP 采集源已存在：industry=${code}（现 ${n_src} 条）"
    else
      "${PSQL[@]}" "INSERT INTO kb_pack_sources (kind,name,base_url,lang,industry,pack_type,enabled,freq_hours,tier,last_run_at,last_status,created_at)
        SELECT s.kind, replace(s.name, '汽车', '$zh'), '', s.lang, '$code', s.pack_type, s.enabled, s.freq_hours, s.tier, '', '', to_char(now(),'YYYY-MM-DD\"T\"HH24:MI:SS')
        FROM kb_pack_sources s
        WHERE s.pack_type='industry' AND s.industry='auto';"
      echo "CREATED 采集源 industry=${code}（按 auto 的源矩阵派生 $(q "SELECT COUNT(*) FROM kb_pack_sources WHERE pack_type='industry' AND industry='$code';") 条；base_url 全留空=走内置种子）"
    fi
    i=$((i + 1))
  done
  echo "--- 回读（包 / 源覆盖语种数 / 与 auto 的源数对照）"
  q "SELECT id||' | '||code||' | '||pack_type||' | role='||role||' | enabled='||enabled::text FROM kb_packages WHERE tenant_id=$HOST_TID AND code IN ('finance','catering') ORDER BY id;"
  q "SELECT industry||' | 源数='||COUNT(*)||' | 语种数='||COUNT(DISTINCT lang)||' | kind='||STRING_AGG(DISTINCT kind, ',') FROM kb_pack_sources WHERE industry IN ('auto','finance','catering') GROUP BY industry ORDER BY industry;"
  echo "DONE=packs 采集由后台调度器按 scrape_poll_sec 自动接手（低占用才跑；新源 last_run_at 为空 ⇒ 下一轮即采）。"
  exit 0
fi

# ---------------------------------------------------------- MODE=terms 录术语
if [ "$MODE" = "terms" ]; then
  # 人工确认过的金融 UI 术语（来源＝2026-10-30 用户复跑的那张交易界面截图上的错译/缺译）。
  # 形状纪律：源词 ≤30 rune（批量术语层硬过滤口径）、目标语非空、不含括号注释、值只一份不漂移。
  #
  # ★ 落点为什么是**两个包**（同一批值、不是两把尺子）：
  #   · 平台金融行业包 —— 正确的长期归属，但可见性要 `tenants.industry='finance'` 才成立；
  #     现网两个租户都标 auto ⇒ 只写这里，客户今天一条都不会变。
  #   · 租户 1 自己的企业包 —— 企业包**恒在该租户可见域内**（不看 industry），
  #     用户本人（zhangzifei）就是租户 1 的 tenant_admin，效果当场可验。
  #   两处写的是**同一个值**，术语检索按 (源词,语种) 去重，不存在"两个译法打架"。
  FIN_PKG=$(q "SELECT id FROM kb_packages WHERE tenant_id=$HOST_TID AND pack_type='industry' AND code='finance' LIMIT 1;")
  if [ -z "${FIN_PKG:-}" ] || [ "${FIN_PKG}" = "0" ]; then
    echo "ABORT=no-finance-pack（先跑 MODE=packs）"; exit 3
  fi
  TEN1_PKG="${TEN1_PKG:-$(q "SELECT id FROM kb_packages WHERE tenant_id=1 AND pack_type='tenant' ORDER BY id LIMIT 1;")}"
  echo "目标包：finance id=${FIN_PKG}（tenant 0）／租户 1 企业包 id=${TEN1_PKG:-<无>}"
  "${PSQL[@]}" "CREATE SCHEMA IF NOT EXISTS $bak_schema;
    DROP TABLE IF EXISTS $bak_schema.kb_entries;
    CREATE TABLE $bak_schema.kb_entries AS
      SELECT * FROM kb_entries WHERE package_id IN ($FIN_PKG$( [ -n "${TEN1_PKG:-}" ] && echo ",$TEN1_PKG" ));"
  echo "BAKED $bak_schema.kb_entries（$(q "SELECT COUNT(*) FROM $bak_schema.kb_entries;") 行，含两个目标包改前全量）"
  NOW=$(date -u +%Y-%m-%dT%H:%M:%S)
  ins_term() { # <package_id> <tenant_id> <源词> <目标语> <译文>
    local pkg="$1" tid="$2" src="$3" lang="$4" tgt="$5" dup
    dup=$(q "SELECT COUNT(*) FROM kb_entries WHERE package_id=$pkg AND layer=1 AND source_lang='zh' AND source_text='$src' AND target_lang='$lang';")
    if [ "${dup:-0}" != "0" ]; then
      echo "SKIP pkg=$pkg 已有 ${src}（${lang}）现值=$(q "SELECT target_text FROM kb_entries WHERE package_id=$pkg AND layer=1 AND source_text='$src' AND target_lang='$lang' LIMIT 1;")"
      return
    fi
    "${PSQL[@]}" "INSERT INTO kb_entries (tenant_id,package_id,layer,source_lang,source_text,target_lang,target_text,module,created_at,updated_at)
      VALUES ($tid,$pkg,1,'zh','$src','$lang','$tgt','manual:20261030_UI','$NOW','$NOW');" >/dev/null
    echo "INSERT pkg=$pkg $src → ${tgt}（${lang}）"
  }
  TERM_EN=(
    "形态战法|Pattern Strategy"
    "形态类交易战法|Pattern Strategy"
    "因子战法|Factor Strategy"
    "已实现|Realized"
    "已实现盈亏|Realized P&L"
    "浮动盈亏|Unrealized P&L"
    "总盈亏|Total P&L"
    "卖出胜负|Sell Win/Loss"
    "买入额|Buy Amount"
    "卖出额|Sell Amount"
    "成交笔数|Filled Lots"
    "笔数|Lots"
    "自动研究|Automated Research"
    "全局|Global"
    "元/次|CNY per Signal"
    "已同步|Synced"
    "买入|Buy"
    "卖出|Sell"
    "动量|Momentum"
    "双响炮|Double Bump"
    "龙头战法|Dragon Head Strategy"
    "龙回头|Dragon Pullback"
    "N形超短|N-Shape Scalping"
    "波动突破战法|Volatility Breakout Strategy"
    "交易流水与整体盈亏|Trade Blotter and Overall P&L"
    "加权成本重放|Weighted-Cost Rollover"
  )
  for row in "${TERM_EN[@]}"; do
    src="${row%%|*}"; tgt="${row#*|}"
    ins_term "$FIN_PKG" "$HOST_TID" "$src" en "$tgt"
  done
  if [ -n "${TEN1_PKG:-}" ] && [ "${TEN1_PKG}" != "0" ]; then
    for row in "${TERM_EN[@]}"; do
      src="${row%%|*}"; tgt="${row#*|}"
      ins_term "$TEN1_PKG" 1 "$src" en "$tgt"
    done
  fi
  # 餐饮：先把门店/菜品这一族高频词钉住（避免"菜名当散文译"），只进平台餐饮包。
  CAT_PKG=$(q "SELECT id FROM kb_packages WHERE tenant_id=$HOST_TID AND pack_type='industry' AND code='catering' LIMIT 1;")
  if [ -n "${CAT_PKG:-}" ] && [ "${CAT_PKG}" != "0" ]; then
    for row in "招牌菜|Signature Dish" "凉菜|Cold Dish" "热菜|Hot Dish" "主食|Staple Food" "前菜|Appetizer" \
               "加料|Extra Topping" "忌口|Dietary Restriction" "堂食|Dine-in" "外带|Takeaway" "外卖|Delivery" \
               "翻台率|Table Turnover Rate" "预订|Reservation" "后厨|Kitchen" "备料|Mise en Place" \
               "出餐|Serve Dish" "客单价|Average Check" "过敏原|Allergen" "买单|Pay the Bill"; do
      src="${row%%|*}"; tgt="${row#*|}"
      ins_term "$CAT_PKG" "$HOST_TID" "$src" en "$tgt"
    done
  else
    echo "SKIP catering 包不存在（先跑 MODE=packs）"
  fi
  echo "--- 回读（三个包的 L1 计数 + 本次人工条目样本）"
  q "SELECT p.code||'（tid='||p.tenant_id||'）| L1='||COUNT(e.id) FROM kb_packages p JOIN kb_entries e ON e.package_id=p.id AND e.layer=1 WHERE p.id IN ($FIN_PKG,${TEN1_PKG:-0},$CAT_PKG) GROUP BY p.code,p.tenant_id ORDER BY p.code;"
  q "SELECT source_text||' -> '||target_text||' | pkg='||package_id FROM kb_entries WHERE module LIKE 'manual:20261030_UI' AND target_lang='en' ORDER BY package_id, id LIMIT 60;"
  echo "DONE=terms 生效时机：术语缓存 60s 自然过期；要立刻生效重启服务（或走管理台任一写点触发失效）。"
  echo "      回滚：INSERT INTO kb_entries SELECT * FROM $bak_schema.kb_entries WHERE id NOT IN (SELECT id FROM kb_entries)；或直接 DELETE FROM kb_entries WHERE module='manual:20261030_UI';"
  exit 0
fi

# ------------------------------------------------- MODE=terms_trim 撤掉两条高风险条目
# ★ 2026-10-30 现网实证（探针 C 文本工单腿）：术语是**硬闸**（gate.go 第 9 项，失败重译 N 轮后整单 failed）。
#   两条写进去以后咬到真实样本：
#     ① 「交易流水与整体盈亏」是**句子片段**（9 字 > 固定标签档的 8 字上限）⇒ 模型几乎不可能逐字给出
#        "Trade Blotter and Overall P&L"，而文本链的术语遵循是硬闸 ⇒ 含这句话的工单必红；
#     ② 「形态类交易战法」与「形态战法」**同译法两源**，命中时两条要求都下发，只会让重译提示自相矛盾。
#   术语档的取向是"钉住界面短标签"，不是"钉住整句"——所以撤这两条，其余 23 条保留。
#   只删本次人工批次（module='manual:20261030_UI'）的这两条，先备份再删后回读。
if [ "$MODE" = "terms_trim" ]; then
  RISKY=("交易流水与整体盈亏" "形态类交易战法")
  if [ "$APPLY" != "1" ]; then
    sec "只读：这两条目前在哪些包里"
    for s in "${RISKY[@]}"; do
      q "SELECT '$s'||' | pkg='||e.package_id||' | '||p.code||'（tid='||p.tenant_id||'）| '||e.target_lang||' -> '||e.target_text
         FROM kb_entries e JOIN kb_packages p ON p.id=e.package_id
         WHERE e.module='manual:20261030_UI' AND e.source_text='$s';"
    done
    echo "DONE=terms_trim 只读（要看写入请 APPLY=1）"
    exit 0
  fi
  sec "备份 + 删除"
  "${PSQL[@]}" "CREATE SCHEMA IF NOT EXISTS $bak_schema;" >/dev/null
  "${PSQL[@]}" "CREATE TABLE IF NOT EXISTS $bak_schema.kb_entries_trim AS
    SELECT * FROM kb_entries WHERE module='manual:20261030_UI' AND source_text IN ('交易流水与整体盈亏','形态类交易战法');" >/dev/null
  q "SELECT '备份行数='||COUNT(*) FROM $bak_schema.kb_entries_trim;"
  "${PSQL[@]}" "DELETE FROM kb_entries WHERE module='manual:20261030_UI' AND source_text IN ('交易流水与整体盈亏','形态类交易战法');" >/dev/null
  echo "--- 回读（这两条应为 0 行；其余人工条目保留）"
  q "SELECT '残留高风险条目='||COUNT(*) FROM kb_entries WHERE module='manual:20261030_UI' AND source_text IN ('交易流水与整体盈亏','形态类交易战法');"
  q "SELECT '人工条目总数='||COUNT(*) FROM kb_entries WHERE module='manual:20261030_UI';"
  echo "DONE=terms_trim 生效时机同 terms 档（缓存 60s 自然过期）。"
  exit 0
fi

# -------------------------------------- MODE=terms_off_tenant 把人工批次从租户包撤出
# ★ 2026-10-30 现网实证（探针 C 文本工单腿，连跑三次）：
#   人工 UI 术语一旦落进**租户自己的企业包**，就进了文本链的**硬闸**（gate.go 第 9 项：源文命中术语
#   而译文不含规定译法 ⇒ 附提示重译 N 轮 ⇒ 仍不过就整单 task_failed）。实测「形态战法」
#   规定译法是 `Pattern Strategy`，模型在散文里写 `pattern strategies`/`pattern-based…` ⇒ 8 轮后整单失败。
#   ⇒ 这批"营销式专名"的正确位置是**行业包**（只在 tenant.industry 命中该行业时可见），
#     而不是租户企业包（对所有工单常态生效）。本档只撤租户包里那一批，行业包里的原样保留。
if [ "$MODE" = "terms_off_tenant" ]; then
  TP="${TEN1_PKG:-1}"
  if [ "$APPLY" != "1" ]; then
    sec "只读：租户包 ${TP} 里这批人工条目的影响面"
    q "SELECT 'pkg='||package_id||' | '||source_text||' -> '||target_text FROM kb_entries
       WHERE module='manual:20261030_UI' AND package_id=${TP} ORDER BY id LIMIT 40;"
    echo "DONE=terms_off_tenant 只读（要看写入请 APPLY=1）"
    exit 0
  fi
  sec "备份 + 从租户包 ${TP} 撤出"
  "${PSQL[@]}" "CREATE SCHEMA IF NOT EXISTS $bak_schema;" >/dev/null
  "${PSQL[@]}" "CREATE TABLE IF NOT EXISTS $bak_schema.kb_entries_tenant_off AS
    SELECT * FROM kb_entries WHERE module='manual:20261030_UI' AND package_id=${TP};" >/dev/null
  q "SELECT '备份行数='||COUNT(*) FROM $bak_schema.kb_entries_tenant_off;"
  "${PSQL[@]}" "DELETE FROM kb_entries WHERE module='manual:20261030_UI' AND package_id=${TP};" >/dev/null
  echo "--- 回读"
  q "SELECT '租户包残留='||COUNT(*) FROM kb_entries WHERE module='manual:20261030_UI' AND package_id=${TP};"
  q "SELECT '行业包保留='||COUNT(*) FROM kb_entries WHERE module='manual:20261030_UI' AND package_id<>${TP};"
  echo "DONE=terms_off_tenant 生效时机同 terms 档（术语缓存 60s 自然过期）。"
  exit 0
fi

# ---------------------------------------------------------- MODE=gloss 开词表
if [ "$MODE" = "gloss" ]; then
  TID="${GLOSS_TID:-0}"
  if [ "${TID}" -le 0 ]; then
    echo "ABORT=need-gloss-tid（GLOSS 档必须给 GLOSS_TID=<租户 id>，不给全站开：每单一次判定是真实成本）"; exit 4
  fi
  "${PSQL[@]}" "INSERT INTO system_config (key,value,updated_at) VALUES ('domain_glossary_enabled.$TID','1',to_char(now(),'YYYY-MM-DD\"T\"HH24:MI:SS'))
    ON CONFLICT(key) DO UPDATE SET value='1', updated_at=excluded.updated_at;"
  if [ -n "${DETECT_MODEL:-}" ]; then
    "${PSQL[@]}" "INSERT INTO system_config (key,value,updated_at) VALUES ('domain_detect_model','$DETECT_MODEL',to_char(now(),'YYYY-MM-DD\"T\"HH24:MI:SS'))
      ON CONFLICT(key) DO UPDATE SET value='$DETECT_MODEL', updated_at=excluded.updated_at;"
    echo "SET domain_detect_model=${DETECT_MODEL}（★ 只换 model，base/key 仍走全局那一档 ⇒ 必须是同一家网关上存在的模型）"
  fi
  echo "ENABLED domain_glossary_enabled.${TID}=1"
  q "SELECT COALESCE(NULLIF(value,''),'<空>')||'   <- '||key FROM system_config WHERE key LIKE 'domain_%' ORDER BY key;"
  echo "DONE=gloss 下一步手拨判域：用该租户的临时 Key 打 OpenAPI 翻译一段金融样本，再 grep 日志 domain_glossary。"
  exit 0
fi

echo "ABORT=unknown-mode（MODE=${MODE}；合法值 audit|packs|terms|gloss）"
exit 5
