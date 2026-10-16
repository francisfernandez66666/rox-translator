#!/usr/bin/env bash
# release_20261016.sh — TTS 多语档扩档换件（**只换主站＋演示站两台 translator-server，不动前端、不动挂件、不动探针**）
#
# 本批动了什么（按 AGENTS §一·5「五类渲染面」点名，别默认 dist 绿＝全站绿）：
#   · `internal/fileproc/tts.go`（覆盖语种 2→10 档、`ttsMeloLangs`/`ttsSupertonicFiles` 分档、
#     `TtsCoveredLangs()` 改「有效档」、`ttsRoutesReady()` 任一路线即就绪、supertonic 资产探测）
#     ＋`internal/api/server.go`（`tts_langs` 注释口径）⇒ 都落在 **translator-server**，两台都要换。
#   · `tts_speak.py`＋`assets/tts/supertonic/*`（脚本资产）**不走本脚本**：已由
#     `scripts/sync_fileproc_assets.sh`（MODE=remote，两站各一次）落位并逐文件 sha 核对；
#     权重本体由 `scripts/fetch_tts_models.sh` 取（本批已在两站各投一次）。
#   · 前端只改注释（JSX 内注释构建即剥）⇒ 本机实测 dist 入口与线上**逐字节相同**
#     （`assets/index-1A-0R5X_.js` sha f32bfe30… 两侧一致、index.html 也一致）⇒ **不换源**。
#   · 挂件（translator-assist）／探针（fpdprobe）零改动 ⇒ 不换、不重启。
#
# ★ 本批**不抬 `cannedPromptRev`**（仍 `0AR-1`）：改的是 TTS 语种路由与能力出栈，
#   不在挂件 canned 射程 ⇒ 换二进制即生效。
#
# 两道中止线（都在动手之前）：
#   ① 本机新件里**特征串缺失** ⇒ 不上传（表现是"编译的是旧工作树"）；
#   ② 服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，磁盘上还没落位任何新件。
#   另有负向对照：**落位之前**先读旧件，确认那两条特征串在旧件里是 0——否则中止线① 是空转判据。
# 落位一律 `cp 到新名` ＋ 同目录 `mv -f` rename（`cp` 直接覆盖运行中二进制会 ETXTBSY）。
#
# 换件后**必须现读的三条**（本脚本 P5 自动读，读不到即 FAIL）：
#   ① 两站 `/api/health` 的 `tts_langs` 必须含 ja/ko/fr/de/es/pt/ru/ar（十档）；
#   ② 两站 `tts_ready` 仍为 true（任一路线资产齐即 true）；
#   ③ 配音真件另跑（`target_langs=ja`，见 P6 提示）——就绪位只回答"资产在不在"，证明不了整条腿。
#
# 用法：bash deploy/release_20261016.sh            # 干跑（编译＋特征串，不 scp、不碰服务器）
#       APPLY=1 bash deploy/release_20261016.sh    # 真换
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261016"

SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

echo "===== P0 交叉编译（HEAD=$(git -C "$REPO" rev-parse --short HEAD)，工作树含本波未提交改动=$([ -n "$(git -C "$REPO" status --porcelain -- backend-go)" ] && echo 是 || echo 否)）====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) || { echo "ABORT=1 server 编译失败"; exit 2; }
echo "$SERVER_BIN size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN")"

echo "===== P0-B 特征串（中止线①：件里没这几条腿＝本批等于没上线）====="
W_SUP=$(grep -a -c -F -- "supertonic" "$SERVER_BIN" || true)
W_DP=$(grep -a -c -F -- "duration_predictor.int8.onnx" "$SERVER_BIN" || true)
W_UE=$(grep -a -c -F -- "vector_estimator.int8.onnx" "$SERVER_BIN" || true)
# 承重：上一批的 OCR/ASR 三条腿必须还在（本批是叠加，不是替换；掉了就是构建树退回旧提交）
CARRY_OCR=$(grep -a -c -F -- "ocr_recognize" "$SERVER_BIN" || true)
CARRY_ASR=$(grep -a -c -F -- "asr_transcribe" "$SERVER_BIN" || true)
echo "server: supertonic=$W_SUP duration_predictor=$W_DP vector_estimator=$W_UE ； carry: ocr_recognize=$CARRY_OCR asr_transcribe=$CARRY_ASR"
for pair in "$W_SUP:多语档目录名 supertonic" "$W_DP:多语档权重清单 duration_predictor" "$W_UE:多语档权重清单 vector_estimator" "$CARRY_OCR:承重·OCR 脚本名" "$CARRY_ASR:承重·ASR 脚本名"; do
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
for pat in supertonic duration_predictor.int8.onnx; do
  n=$(grep -a -c -F -- "$pat" /opt/translator/bin/translator-server 2>/dev/null || true)
  [ -n "$n" ] || n='<文件读不到>'
  echo "旧件命中『${pat}』=${n}（期望 **0**）"
done
echo "同一把尺子的正向对照：旧件命中『ocr_recognize』=$(grep -a -c -F -- ocr_recognize /opt/translator/bin/translator-server 2>/dev/null || true)（期望 ≥1＝上一批那条腿确实在旧件里）"
echo "旧件 size=$(stat -c %s /opt/translator/bin/translator-server) sha16=$(sha256sum /opt/translator/bin/translator-server | cut -c1-16)"

echo "----- P2-B 换件前基线（TTS 就绪位与语种清单起点）-----"
echo "换件前 主站 tts_ready=$(curl -s -m 10 http://127.0.0.1:8787/api/health | grep -o '"tts_ready":[a-z]*' || echo '<无该字段>') tts_langs=$(curl -s -m 10 http://127.0.0.1:8787/api/health | grep -o '"tts_langs":\[[^]]*\]' || echo '<无该字段>')"
echo "换件前 演示 tts_ready=$(curl -s -m 10 http://127.0.0.1:8789/api/health | grep -o '"tts_ready":[a-z]*' || echo '<无该字段>') tts_langs=$(curl -s -m 10 http://127.0.0.1:8789/api/health | grep -o '"tts_langs":\[[^]]*\]' || echo '<无该字段>')"

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
echo "----- P3 落位（只有两台后端；探针、挂件与前端本波都不动）-----"
place /tmp/ts-new-$TAG /opt/translator/bin      translator-server 755 || exit 5
place /tmp/ts-new-$TAG /opt/translator-demo/bin translator-server 755 || exit 5

echo "----- P4 重启两台后端并等健康（挂件不重启：它本波零改动）-----"
# 窗口锚点与日志口径同形（%Y-%m-%dT%H:%M:%S，第 11 位是 T）：带空格的本地时刻做字典序比较会恒真。
RESTART_SINCE=$(date '+%Y-%m-%d %H:%M:%S')
RESTART_T=$(date -d "$RESTART_SINCE" '+%Y-%m-%dT%H:%M:%S')
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

echo "----- P5 就绪位与语种清单（本批的接线判据；三条都读，缺一条即 FAIL）-----"
FAIL=0
for pair in "8787:主站" "8789:演示"; do
  port="${pair%%:*}"; site="${pair#*:}"
  h=$(curl -s -m 10 "http://127.0.0.1:$port/api/health" || true)
  ready=$(printf '%s' "$h" | grep -o '"tts_ready":[a-z]*' || true)
  langs=$(printf '%s' "$h" | grep -o '"tts_langs":\[[^]]*\]' || true)
  echo "  ${site}(${port}) ${ready} ${langs}"
  case "$ready" in *true*) : ;; *) echo "  FAIL ${site} tts_ready 不是 true"; FAIL=$((FAIL+1)) ;; esac
  miss=""
  for L in zh en ja ko fr de es pt ru ar; do
    case "$langs" in *"\"$L\""*) : ;; *) miss="$miss $L" ;; esac
  done
  [ -z "$miss" ] && echo "  OK ${site} 十档齐" || { echo "  FAIL ${site} 缺语种:${miss}"; FAIL=$((FAIL+1)); }
done

echo "----- P5-B 件内自检（新件必须带本批那两条，且旧串不复活）-----"
for pat in supertonic duration_predictor.int8.onnx; do
  echo "    新件命中『${pat}』=$(grep -a -c -F -- "$pat" /opt/translator/bin/translator-server 2>/dev/null || true)"
done
echo "    新件 size=$(stat -c %s /opt/translator/bin/translator-server) sha16=$(sha256sum /opt/translator/bin/translator-server | cut -c1-16)"

echo "----- P5-C 落位后 tts_langs 十档复读（等就绪位翻档后 3 秒再读一次）-----"
sleep 3
for port in 8787 8789; do
  echo "    ${port} $(curl -s -m 10 "http://127.0.0.1:$port/api/health" | grep -o '"tts_langs":\[[^]]*\]' || echo '<读不到>')"
done

[ "$FAIL" -eq 0 ] && echo "RELEASE_TTS_LANGS_OK=1" || echo "RELEASE_TTS_LANGS_FAIL=$FAIL"
echo "REMINDER=1 配音真件（target_langs=ja）另跑一单；就绪位只回答『资产在不在』；跑完备份后清测试数据。"
REMOTE_EOF
rc=$?
echo "P2 远端退码=$rc"
exit "$rc"
