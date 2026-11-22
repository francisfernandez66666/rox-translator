#!/usr/bin/env bash
# release_20261122_final.sh — 2026-11-22 收口批上线（缺失档数字注入 + 救援段复核 evals 记分 + SQL 方言修复 + 全量注释）
#
# 本批内容（纯后端，前端 dist 与上一批同源，换源只为走同一套交付自检）：
#   ① 缺失档数字注入：ctxRetryTargets 的换算注入条件从"仅数字不过档"扩到"缺失档"，
#      gate.NumberExpectations 天然只收 mult≠1 的量级词数字，无数字段提示词逐字不变；
#      解决 tv10 两格（L150 65–120 万 / L157 45–80 万）从未收到 65万=650000 具体提示的缺口。
#   ② 加入校对+复核流程：救援段采纳后跑复核 evals（StageReviewEvals）记分，只记分不改采纳，
#      受 ReviewEnabled/抽样/无 Key 降级；新锁 TestRescueReviewEvalDegradesQuietly。
#   ③ 全量 -race 四处存量红灯：16 处裸 s.db.Query/QueryRow 走 db 方言包装（PG 静默失效真缺陷）、
#      D3 字面量门禁假阳性、SQLite else 分支豁免、faceproc 字体闸带 venv。
#   ④ gate.RunWithTerms 补 doc comment；变异体 M0370 去重；回归脚本纳入今日 8 腿。
#
# 用法：bash deploy/release_20261122_final.sh           # 干跑
#       APPLY=1 bash deploy/release_20261122_final.sh   # 真换
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261122qualitydone"

TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

# feat_count <文件> <字面量> —— 按**字节**数出现次数（locale 无关，不用 grep -c）。
feat_count() {
  python3 - "$1" "$2" <<'PY'
import sys
path, needle = sys.argv[1], sys.argv[2]
data = open(path, 'rb').read()
print(data.count(needle.encode('utf-8')))
PY
}

echo "===== P0 前端交付包（本批前端源码零改动，复用了上一批已通过自检的同源 tar）====="
# ★ 复用判据（与第三批发版同 tar）：本批所有改动都在 backend-go/scripts，
#   frontend-react/src 自 48ff6f48 起未动；dist 入口仍是 index-BGl9vfRd.js。
#   重新 vite build 会清空 dist/assets（沙箱批量删除拦截），且产物字节相同、无收益。
PREV_TAR="/tmp/fe-20261122quality.tar.gz"
[ -f "$PREV_TAR" ] || { echo "ABORT=2 复用件 $PREV_TAR 不在位（需先跑上一批发版或手工构建）"; exit 2; }
PREV_SHA=$(sha_local "$PREV_TAR" | cut -c1-16)
[ "$PREV_SHA" = "82f5ef4f1a7e3e2d" ] || { echo "ABORT=2 复用件 sha 前缀不符（实得 ${PREV_SHA}，期望 82f5ef4f1a7e3e2d）"; exit 2; }
cp "$PREV_TAR" "$TAR" || { echo "ABORT=2 复制复用件失败"; exit 2; }
echo "tar=$TAR size=$(wc -c <"$TAR" | tr -d ' ') sha=$(sha_local "$TAR" | cut -c1-16) (reused, sha 前缀锁定)"
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过 ⇒ 不许换源"; exit 2; }

echo "===== P0-B 后端件 ====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 2; }
# 本批特征串（运行期真会引用的字面量）：
#   · 「救援段复核评估记分」/「救援段复核评估失败」= recordRescueReviewEval 的两条日志（本批新增）；
#   · 「exact converted values」= 数字换算注入提示词正文（上一批，回归哨兵）；
#   · 「上下文重译预算不足」= 分档预算 WARN（上一批，回归哨兵）；
#   · 「instant_review_enabled」= 复核开关（再上一批，回归哨兵）。
W_GARBAGE=$(feat_count "$SERVER_BIN" "isNumericCellShortLetterGarbage")
W_TIMEOUT=$(feat_count "$SERVER_BIN" "工单执行上限已放宽")
W_DETERMIN=$(feat_count "$SERVER_BIN" "金额格按确定性换算交付")
W_BALANCE=$(feat_count "$SERVER_BIN" "多账户 API Key 均衡调用已启用")
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN") ｜ 静默垃圾判据=${W_GARBAGE} 工单时限放宽=${W_TIMEOUT} 确定性换算=${W_DETERMIN} 均衡调用=${W_BALANCE}（期望都 ≥1）"
[ "$W_GARBAGE" != "0" ] || { echo "ABORT=3 缺「isNumericCellShortLetterGarbage」⇒ 数字格残字判据未进件"; exit 3; }
[ "$W_TIMEOUT" != "0" ] || { echo "ABORT=3 缺「工单执行上限已放宽」⇒ 长任务时限未进件"; exit 3; }
[ "$W_DETERMIN" != "0" ] || { echo "ABORT=3 缺「金额格按确定性换算交付」⇒ 确定性换算未进件"; exit 3; }
[ "$W_BALANCE" != "0" ] || { echo "ABORT=3 缺「多账户 API Key 均衡调用已启用」⇒ 均衡调用未进件"; exit 3; }
TAR_SHA=$(sha_local "$TAR")
BIN_SHA=$(sha_local "$SERVER_BIN")

if [ "$APPLY" != "1" ]; then
  echo "DRY=1 到这里全是本机动作（编译＋打包＋自检）；未 scp、未碰服务器。加 APPLY=1 才执行 P1 起。"
  exit 0
fi

echo "===== P1 上传（此时仍未落位）====="
scp -P "$PORT" "$TAR" "root@$HOST:/tmp/fe-$TAG.tar.gz" || { echo "ABORT=4 scp tar 失败"; exit 4; }
scp -P "$PORT" "$SERVER_BIN" "root@$HOST:/tmp/ts-new-$TAG" || { echo "ABORT=4 scp server 失败"; exit 4; }
scp -P "$PORT" "$SWAP" "root@$HOST:/tmp/swap_web_and_binary.sh" || { echo "ABORT=4 scp swap 脚本失败"; exit 4; }

echo "===== P2 服务器侧换源＋换件（两段式，两站全绿才落位）====="
ssh -p "$PORT" "root@$HOST" \
  "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P2 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"
exit "$rc"
