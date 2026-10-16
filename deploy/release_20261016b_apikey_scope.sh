#!/usr/bin/env bash
# release_20261016b_apikey_scope.sh — 修「API Key 后台删不掉」换件（**只换主站＋演示站两台 translator-server，不动前端/挂件/探针/脚本资产**）
#
# 缺陷（2026-10-16 现网实证，用户报「api key后台删不掉」）：
#   超管在**平台上下文**（不带 X-Tenant-ID ⇒ `effTenant()` 回 0）打 `/api/apikeys`，`ListAPIKeys(tid<=0)`
#   按设计回**全量** Key；而逐条操作（delete/status/limit/rotate/reveal）在 store 侧硬编码
#   `AND tenant_id=?` ⇒ `WHERE tenant_id=0` **恒 0 行影响、也不报错**，handler 于是回 `{"success":true}`。
#   现场读数：14:31:37 与 14:31:53 两次 `POST /api/apikeys/delete` 均 200、列表响应体 1617 B **一字不变**、
#   库里 `api_keys.id=56`（name=「临时」）原封不动；同刻库读 `count(id=56)=1` 而 `count(id=56 AND tenant_id=0)=0`。
#   即「看得见却动不了」＋「假装动过了」，比 403 难查。
#
# 修法（本件）：`store/apikeys.go` 收敛出唯一口径助手 `apiKeyIDScope(id,tid)`——
#   tid>0＝本租户（隔离不变）；tid<=0＝平台视角（跨租户，与 ListAPIKeys 同尺）。
#   GetAPIKey/GetAPIKeyPlain/SetAPIKeyStatus/SetAPIKeyDailyLimit/DeleteAPIKey 一律走它；
#   handler 侧补**存在性前置**（查不到＝404「Key 不存在」，跨租户也不泄露存在性），
#   并补 delete/status 的审计行（旧注释声称"写审计"而实际没写，同 §一·14「注释点名的锚点必须存在」）。
#   轮换另修一处口径：新 Key 必须继承 `old.TenantID`，否则超管在平台上下文轮换会把新 Key 落成 tenant_id=0 的孤儿。
#
# 本件**不抬 `cannedPromptRev`**（不在挂件 canned 射程）。
#
# 两道中止线（都在动手之前）：
#   ① 本机新件里特征串缺失 ⇒ 不上传（表现是"编译的是旧工作树"）；
#   ② 服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，磁盘上还没落位任何新件。
# 负向对照：落位**之前**先读线上旧件，确认 `apiKeyIDScope`/`apikey_set_status` 在旧件里是 0。
# 落位一律 `cp 到新名` ＋ 同目录 `mv -f` rename（`cp` 直接覆盖运行中二进制会 ETXTBSY）。
#
# 换件后现读三条（P5 自动读，读不到即 FAIL）：① 两站 `/api/health` 回 200；
#   ② 新件含本批两个特征串；③ 匿名打 `/api/apikeys/delete` 仍 401（路由在、鉴权闸没被顺手摘掉）。
# ⚠️ 端点级"真能删掉"的验收必须由**人在管理台点一次**（本脚本拿不到会话）：
#   删除后按 `deploy/check_apikey_delete_face.sh` 的两条库读复验。
#
# 用法：bash deploy/release_20261016b_apikey_scope.sh            # 干跑（编译＋特征串，不 scp、不碰服务器）
#       APPLY=1 bash deploy/release_20261016b_apikey_scope.sh    # 真换
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261016b"

SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 交叉编译（HEAD=$(git -C "$REPO" rev-parse --short HEAD)，工作树含 backend-go 未提交改动=$([ -n "$(git -C "$REPO" status --porcelain -- backend-go)" ] && echo 是 || echo 否)）====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) || { echo "ABORT=1 server 编译失败"; exit 2; }
echo "$SERVER_BIN size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN")"

echo "===== P0-B 特征串（中止线①：件里没这几条腿＝本批等于没上线）====="
W_SCOPE=$(grep -a -c -F -- "apiKeyIDScope" "$SERVER_BIN" || true)
W_AUD=$(grep -a -c -F -- "apikey_set_status" "$SERVER_BIN" || true)
# 承重：上一批的 TTS 多语档与更早的 OCR/ASR 三条腿必须还在（本批是叠加，不是替换）
CARRY_SUP=$(grep -a -c -F -- "supertonic" "$SERVER_BIN" || true)
CARRY_OCR=$(grep -a -c -F -- "ocr_recognize" "$SERVER_BIN" || true)
CARRY_ASR=$(grep -a -c -F -- "asr_transcribe" "$SERVER_BIN" || true)
echo "server: apiKeyIDScope=$W_SCOPE apikey_set_status=$W_AUD ； carry: supertonic=$CARRY_SUP ocr_recognize=$CARRY_OCR asr_transcribe=$CARRY_ASR"
for pair in "$W_SCOPE:本批口径助手 apiKeyIDScope" "$W_AUD:本批新增审计动作 apikey_set_status" \
            "$CARRY_SUP:承重·TTS 多语档" "$CARRY_OCR:承重·OCR 脚本名" "$CARRY_ASR:承重·ASR 脚本名"; do
  n="${pair%%:*}"; label="${pair#*:}"
  [ "$n" != "0" ] || { echo "ABORT=2 缺特征串 ⇒ ${label}（编译的不是含本批改动的树）"; exit 3; }
done
SERVER_SHA=$(sha_local "$SERVER_BIN")

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里全是本机动作（编译＋特征串）；未 scp、未碰服务器。加 APPLY=1 才执行 P1 起。"
  exit 0
fi

echo "===== P1 上传到 /tmp（此时仍未落位）====="
scp -P "$PORT" "$SERVER_BIN" "root@$HOST:/tmp/ts-new-$TAG" || { echo "ABORT=3 scp server 失败"; exit 4; }

echo "===== P2 服务器侧校验＋落位＋重启＋接线读数 ====="
ssh -p "$PORT" "root@$HOST" bash -s -- "$SERVER_SHA" "$TAG" <<'REMOTE_EOF'
set -uo pipefail
EXPECT_TS="$1"; TAG="$2"
TS=$(date +%Y%m%d_%H%M%S)
echo "远端时刻=$(date '+%F %T %Z') TS=$TS"

echo "----- P2 校验上传件（中止线②：不等即停，此时一件未落位）-----"
a=$(sha256sum /tmp/ts-new-$TAG | cut -d' ' -f1)
echo "server_sha=$a"
[ "$a" = "$EXPECT_TS" ] || { echo "ABORT=4 后端上传件 sha 不等，未落位即停"; exit 4; }

echo "----- P2-B2 特征串负向对照（★ 必须在落位之前读线上旧件）-----"
for pat in apiKeyIDScope apikey_set_status; do
  echo "旧件命中『${pat}』=$(grep -a -c -F -- "$pat" /opt/translator/bin/translator-server 2>/dev/null || true)（期望 **0**）"
done
echo "同一把尺子的正向对照：旧件命中『ocr_recognize』=$(grep -a -c -F -- ocr_recognize /opt/translator/bin/translator-server 2>/dev/null || true)（期望 ≥1＝上一批那条腿确实在旧件里）"
echo "旧件 size=$(stat -c %s /opt/translator/bin/translator-server) sha16=$(sha256sum /opt/translator/bin/translator-server | cut -c1-16)"

echo "----- P2-C 缺陷机制现读（库侧，只读）：平台上下文过滤恒 0 行 -----"
sudo -u postgres psql -d langcross -Atc "select '相同 id 不带租户过滤='||count(*) from api_keys where id=(select max(id) from api_keys);"
sudo -u postgres psql -d langcross -Atc "select '相同 id 带 tenant_id=0='||count(*) from api_keys where id=(select max(id) from api_keys) and tenant_id=0;"

place() { # $1 源 $2 目标目录 $3 目标文件名 $4 模式
  local src="$1" dir="$2" name="$3" mode="$4"
  if [ ! -f "$dir/$name" ]; then echo "FAIL $dir/$name <不存在>"; return 1; fi
  cp -a "$dir/$name" "$dir/$name.bak.$TS" || { echo "FAIL 备份 $dir/$name"; return 1; }
  cp "$src" "$dir/$name.new.$TS" || { echo "FAIL 复制到新名"; return 1; }
  chmod "$mode" "$dir/$name.new.$TS"
  chown root:root "$dir/$name.new.$TS"
  mv -f "$dir/$name.new.$TS" "$dir/$name" || { echo "FAIL rename 落位 $dir/$name"; return 1; }
  echo "PLACED $dir/$name size=$(stat -c %s "$dir/$name") sha16=$(sha256sum "$dir/$name" | cut -c1-16) 备份=$dir/$name.bak.$TS"
}
echo "----- P3 落位（只有两台后端）-----"
place /tmp/ts-new-$TAG /opt/translator/bin      translator-server 755 || exit 5
place /tmp/ts-new-$TAG /opt/translator-demo/bin translator-server 755 || exit 5

echo "----- P4 重启两台后端并等健康（挂件不重启：本波零改动）-----"
systemctl restart translator translator-demo
for p in 8787 8789; do
  ok=0
  for i in $(seq 1 15); do
    code=$(curl -s -o /dev/null -m 5 -w '%{http_code}' "http://127.0.0.1:$p/api/health" || true)
    if [ "$code" = "200" ]; then ok=1; break; fi
    sleep 2
  done
  [ "$ok" = 1 ] && echo "OK 端口 $p 健康（第 $i 次）" || { echo "ABORT=5 端口 $p 重启后没起来，请回滚件：$p"; exit 6; }
done
echo "单元读数：translator=$(systemctl is-active translator)/$(systemctl show -p NRestarts --value translator) ；translator-demo=$(systemctl is-active translator-demo)/$(systemctl show -p NRestarts --value translator-demo) ；ai-assist=$(systemctl is-active ai-assist)（本波没碰）"

echo "----- P5 接线读数（三条都读，缺一条即 FAIL）-----"
FAIL=0
for pat in apiKeyIDScope apikey_set_status; do
  n=$(grep -a -c -F -- "$pat" /opt/translator/bin/translator-server 2>/dev/null || true)
  echo "  新件命中『${pat}』=$n"
  # 读数必须是数字且 ≥1：先判形状再判大小（写成 `case … ;; *) … { … } ;; esac` 会在 bash 3.2 与
  # 远端 bash 5 上**解析期**报 `syntax error near ';;'`——2026-10-16 首跑真踩，脚本因此没跑到 P5 就退 2）。
  if printf '%s' "$n" | grep -qE '^[0-9]+$'; then
    if [ "$n" -lt 1 ]; then echo "  FAIL 新件缺特征串 ${pat}"; FAIL=$((FAIL+1)); fi
  else
    echo "  FAIL 特征串读数不是数字：'${n}'"; FAIL=$((FAIL+1))
  fi
done
for pair in "8787:主站" "8789:演示"; do
  port="${pair%%:*}"; site="${pair#*:}"
  h=$(curl -s -m 10 "http://127.0.0.1:$port/api/health" || true)
  case "$h" in *'"success"'*|*'"status"'*) echo "  OK ${site}(${port}) /api/health 有体" ;; *) echo "  FAIL ${site}(${port}) /api/health 读不到"; FAIL=$((FAIL+1)) ;; esac
  # 匿名删 Key 必须仍是 401（路由在、鉴权闸在；本批改的是租户口径，不是拆鉴权）
  code=$(curl -s -o /dev/null -m 10 -w '%{http_code}' -X POST "http://127.0.0.1:$port/api/apikeys/delete" -H 'Content-Type: application/json' -d '{"id":1}' || true)
  echo "  ${site} 匿名 POST /api/apikeys/delete → ${code}（期望 401）"
  [ "$code" = "401" ] || { echo "  FAIL ${site} 匿名调用不是 401（鉴权闸被动过？）"; FAIL=$((FAIL+1)); }
done
echo "  新件 size=$(stat -c %s /opt/translator/bin/translator-server) sha16=$(sha256sum /opt/translator/bin/translator-server | cut -c1-16)"
[ "$FAIL" -eq 0 ] && echo "RELEASE_APIKEY_SCOPE_OK=1" || echo "RELEASE_APIKEY_SCOPE_FAIL=$FAIL"
echo "REMINDER=1 端到端验收＝人在管理台删一把 Key；随后 bash deploy/check_apikey_delete_face.sh 复验库侧。"
REMOTE_EOF
rc=$?
echo "P2 远端退码=$rc"
exit "$rc"
