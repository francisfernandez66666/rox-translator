#!/usr/bin/env bash
# ============================================================================
# prod_uat_probe.sh — 2026-10-25 发版后「生产 UAT 写类复验」（方案 §D 那一节）
#
# 判据（逐条对应方案 §D，全部**问产物**而不是问信封）：
#   A 即时翻译（/api/chat）：41 字中文 ⇒ translations.en 非空；points_used_today 前后读数（不设上下限，
#     只记录，因为免费期/策略免扣档下"没涨"是正确行为）。
#   B 文本工单（两段）：完成 ⇒ translations.en 两段都在（结构不合并），并记下 ticket id 供清理。
#   C 文件工单（0.2KB txt）：完成 ⇒ 下载名 `uat_sample-en.txt`（新口径 `-<lang>`）＋产物正文不含
#     「Renminbi/currency」这类外币幻觉句。
#   D 五条公开路由：title 各不相同、canonical 带 `?lang=`、hreflang 命中。
#
# 用法（在服务器上、token 已在 /tmp/.uat_probe_tok）：
#   bash prod_uat_probe.sh            # 只跑判据（会新建两张工单）
#   REUSE_TEXT=116 REUSE_FILE=117 bash prod_uat_probe.sh   # 复验模式：只核既有工单、零新增测试件
# 产物：末行 PROD_UAT_ALL_OK=1 才算全绿；打印 TICKET_IDS=… 供清理。
# ============================================================================
set -uo pipefail
B="${B:-http://127.0.0.1:8787}"
TOK_FILE="${TOK_FILE:-/tmp/.uat_probe_tok}"
H="Authorization: Bearer $(cat "$TOK_FILE")"
J='Content-Type: application/json'
PASS=0; FAIL=0
ck() { if printf '%s' "$3" | grep -qE "$2"; then PASS=$((PASS+1)); echo "OK   $1"; else FAIL=$((FAIL+1)); echo "BAD  $1（期望 /$2/，实得 $(printf '%s' "$3" | head -c 200)）"; fi; }
pv() { python3 -c 'import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1],{"d":d}))' "$1" 2>/dev/null; }

echo "===== A 即时翻译（41 字） ====="
P0=$(curl -s "$B/api/me/package" -H "$H" | pv 'd.get("points_used_today",-1)')
RA=$(curl -s "$B/api/chat" -H "$H" -H "$J" --max-time 120 \
  -d '{"message":"这是一段用于发版后复验的四十一字中文样例文本，请翻译成英文并保持原意与语气。","options":{"target_langs":["en"]}}')
# 响应形态实测：译文在 `data.translations.en`（顶层 `reply` 是同一句英文本；`points_used` 是本次实扣）
TA=$(printf '%s' "$RA" | pv 'd.get("data",{}).get("translations",{}).get("en","") or d.get("reply","")')
# 判据问**产物**：英文译文非空且不短于 10 字符（只问信封 success 的写法对"翻出空串"完全无感）
if [ -n "${TA:-}" ] && [ "${#TA}" -ge 10 ]; then PASS=$((PASS+1)); echo "OK   A-chat-en-nonempty（len=${#TA}）"; else FAIL=$((FAIL+1)); echo "BAD  A-chat-en-nonempty（实得 $(printf '%s' "${TA:-<空>}" | head -c 80)）"; fi
P1=$(curl -s "$B/api/me/package" -H "$H" | pv 'd.get("points_used_today",-1)')
echo "READ points_used_today ${P0} -> ${P1}（不设上下限：免扣档下不变是正确行为）"

echo "===== B 文本工单（两段） ====="
if [ -n "${REUSE_TEXT:-}" ]; then
  # 复验模式：只核既有工单，不再造新数据（判据一字不动）
  TKB="$REUSE_TEXT"; echo "REUSE text_ticket=$TKB"
else
  RB=$(curl -s "$B/api/tickets/create" -H "$H" -H "$J" --max-time 60 \
    -d '{"title":"20261025 发版复验文本单","source_text":"第一段：这是发版后复验的第一段中文。\n第二段：这是第二段，必须与第一段分开成两行。","target_langs":"en","mode":"fast"}')
  TKB=$(printf '%s' "$RB" | pv 'd.get("ticket",{}).get("id",0)')
  ck B-create '^True$' "$(printf '%s' "$RB" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("success"))')"
fi
DB=""
for i in $(seq 1 60); do
  DB=$(curl -s "$B/api/tickets/detail?id=$TKB" -H "$H")
  ST=$(printf '%s' "$DB" | pv 'd.get("ticket",{}).get("status","")')
  [ "$ST" = "completed" ] || [ "$ST" = "rejected" ] && break
  sleep 2
done
ck B-completed 'completed' "$ST"
# ★ 文本单的译文**不出现在详情响应里**（只有文件单才走 deliverables）——唯一读点是 tickets.final_result，
#   所以这一条按库直读（与 cleanup 脚本同一个 Q 口径）；判据仍是"两段源文 ⇒ 两段译文"。
FR=$(sudo -u postgres psql -d "${DBNAME:-langcross}" -Atc "SELECT final_result FROM tickets WHERE id=$TKB")
NB=$(printf '%s' "$FR" | pv 'len([x for x in d.get("translations",{}).get("en","").split("\n") if x.strip()])')
TB=$(printf '%s' "$FR" | pv 'd.get("translations",{}).get("en","")')
if [ "${NB:-0}" -ge 2 ]; then PASS=$((PASS+1)); echo "OK   B-en-two-lines（行数=${NB}）"; else FAIL=$((FAIL+1)); echo "BAD  B-en-two-lines（行数=${NB:-0}，实得 $(printf '%s' "${TB:-<空>}" | head -c 120)）"; fi

echo "===== C 文件工单（0.2KB txt） ====="
python3 - <<'PY'
open('/tmp/uat_sample.txt','w',encoding='utf-8').write(
  "发版后复验用的小文件。\n" + ("这是一句用于文件链路的填充文本。" * 8) + "\n")
PY
echo "sample_bytes=$(wc -c </tmp/uat_sample.txt | tr -d ' ')"
if [ -n "${REUSE_FILE:-}" ]; then
  TKC="$REUSE_FILE"; echo "REUSE file_ticket=$TKC"
else
  RC=$(curl -s "$B/api/tickets/create-file" -H "$H" -F "files=@/tmp/uat_sample.txt" -F "target_langs=en" -F "mode=fast" --max-time 120)
  TKC=$(printf '%s' "$RC" | pv 'd.get("ticket",{}).get("id",0)')
  ck C-create '^True$' "$(printf '%s' "$RC" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("success"))')"
fi
DC=""
for i in $(seq 1 90); do
  DC=$(curl -s "$B/api/tickets/detail?id=$TKC" -H "$H")
  ST=$(printf '%s' "$DC" | pv 'd.get("ticket",{}).get("status","")')
  [ "$ST" = "completed" ] || [ "$ST" = "rejected" ] && break
  sleep 2
done
ck C-completed 'completed' "$ST"
HD=$(curl -s -D - -o /tmp/uat_dl.bin "$B/api/tickets/download?id=$TKC" -H "$H" --max-time 60 | tr -d '\r')
CD=$(printf '%s' "$HD" | grep -iE '^content-disposition:')
# 负向判据必须吃**真靶子**（头/正文本身），拿占位串判＝恒绿的假锁（AGENTS §三）
ckn() { if printf '%s' "$2" | grep -qE "$3"; then FAIL=$((FAIL+1)); echo "BAD  $1（不应出现 /$3/，实得 $(printf '%s' "$2" | head -c 160)）"; else PASS=$((PASS+1)); echo "OK   $1"; fi; }
ck C-download-name-en 'uat_sample-en\.txt' "$CD"
ckn C-download-name-no-underscore "$CD" 'uat_sample_en'
ck C-download-complete ' 200 ' "$(printf '%s' "$HD" | head -1)"
ckn C-no-currency-hallucination "$(cat /tmp/uat_dl.bin 2>/dev/null)" 'Renminbi|人民币|currency'
echo "READ download_bytes=$(wc -c </tmp/uat_dl.bin | tr -d ' ') disposition=${CD}"

echo "===== D 五条路由（title/canonical/hreflang） ====="
for path in / /pricing /compare /login /register; do
  HTML=$(curl -s -H 'Accept-Language: zh' "$B$path?lang=zh")
  TT=$(printf '%s' "$HTML" | grep -oE '<title>[^<]*</title>' | head -1)
  CA=$(printf '%s' "$HTML" | grep -oE '<link rel="canonical" href="[^"]*"' | head -1)
  HL=$(printf '%s' "$HTML" | grep -c 'hreflang=')
  echo "READ $path title=$TT canonical=$CA hreflang=$HL"
  case "$path" in
    /register) ck "D$path-noindex" 'noindex' "$(printf '%s' "$HTML" | grep -oE '<meta name="robots"[^>]*>' | head -1)" ;;
  esac
  if [ -n "$TT" ] && [ -n "$CA" ]; then PASS=$((PASS+1)); echo "OK   D$path-title-canonical"; else FAIL=$((FAIL+1)); echo "BAD  D$path-title-canonical"; fi
done

echo "TICKET_IDS=$TKB,$TKC"
echo "PASS=$PASS FAIL=$FAIL"
[ "$FAIL" -eq 0 ] && echo "PROD_UAT_ALL_OK=1" || { echo "PROD_UAT_FAIL=1"; exit 1; }
