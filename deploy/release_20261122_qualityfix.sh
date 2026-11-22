#!/usr/bin/env bash
# release_20261122_qualityfix.sh — 真值验收 §7.8 七处译文层缺陷收口 上线
#
# 本批内容（2026-11-22，纯后端；前端 dist 与上一批同源，换源只为走同一套交付自检）：
#   ① 文件写回「数字尾巴」被当标点残渣剥掉（L23/L24/L137）：
#      `engine.stripTrailingSymbolGarbage` 新增数字族豁免（`numericTail`），
#      带正负号/百分号的数字（`-20%`、`-80%+`、`+150%`）不再被剥成一句没有数的句子。
#   ② 中文量级词错译十倍（L158/L160/L181）：
#      `engine.translateInstruction` 追加 `magnitudeInstructionNote`（万=10,000／亿=100,000,000），
#      只在中文源文下生效；`segmentHintNote("数字保持")` 同口径强化；
#      预估侧 `api.estimateTokens` 改传检测到的源语言（与真发出去的那一枪同源）。
#   ③ 标题编号骨架进翻译键导致标记堆叠＋编号丢失（L143/L190）：
#      `fileproc.mdParseProseLine` 把「强调标记起手 + 含数字 + 空白收尾」的编号块与
#      收尾标记移出翻译片段（`mdHeadingNumberBlock` / `mdHeadingTrailingMarkers`），
#      编号与标记写回时逐字节保留。
#   ④ 外来组合符（Mn）残片留在交付物里（L143 泰文）：
#      `engine.StripForeignScriptsInNonTarget` 把外来脚本的组合标记纳入清洗，
#      孤立无组合符的单字母仍保留（数学符号/品牌首字母误删代价更大）。
#   ⑤ 第八轮真值复跑（tv8）新暴露三处收口（同日第二批，仍纯后端）：
#      · L159 `25–50 万`→`250–500 אלף` 本身译对，gate 报「数字保持（缺失: 25, 50）」是假警
#        ⇒ `gate.numSuffixMults` 补希伯来语千档 `אלף`/`אלפים`（1e3）；
#      · L150 `65–120 万` 被交付成单个希伯来字母 `ו`（and）而判「可用」⇒
#        `engine.functionWordNoise` 补希伯来语单字母前缀（ו/ב/ל/ה/מ/ש/כ）；
#      · L45 `מפ춘` 单韩文音节夹在希伯来词中 ⇒ `StripForeignScriptsInNonTarget`
#        对「紧贴目标脚本字母」的单字母按词内残留删除（两侧为空白/标点/数字时仍保留）。
#
#   ⑥ 第九轮真值复跑（tv9）两处收口（同日第三批，仍纯后端）：
#      · L181 `年费 300 万+`→`300 שנק' ומעלה`（量级整个丢了）：产品闸门判对、这一段也确实进了
#        上下文重译，但提示词只有**通用示例**（"300 万+ means 3,000,000+"），模型照旧写 300。
#        ⇒ `gate.NumberExpectations` 复用 missingNumbers 同一套归一化，把**这一段**的换算结果
#        （`"300 万" = 3000000`）注入提示词（`WithSegmentNumberExpectations`）；
#        候选集同时改为**按严重度分档**（缺失 → 数字 → 脚本），撞上限时被截掉的是损失最小的末档。
#      · 脚本中立格假阳性：`gate.ScriptConsistent("he","650,000–1,200,000<br>")` = false
#        （拉丁字母进分母、拉丁不进目标分子）⇒ 三个**正确**的数字格白占重译预算，
#        且重译若丢掉 `<br>` 反而能过判据。⇒ 判据补第三档「两侧都没有非拉丁字母 ⇒ 不判」，
#        调用点签名改为 `ScriptConsistent(target, source, translation)`（源文入参是豁免的前提）。
#
# 中止线（都在动手之前）：
#   ① 本机 tar 过不了 `scripts/check_frontend_tar.sh` ⇒ 不上传；
#   ② 本机新件里特征串缺失 ⇒ 不上传（用**运行期真的会引用的字符串字面量**判，
#      不用函数名：`-ldflags="-s -w"` 下内联掉的函数名不进名字表，那会给假红）；
#   ③ 服务器侧 sha256 与本机现算值不等 ⇒ 立刻退出，磁盘上还没落位任何东西。
# 落位一律交给常设执行体 `swap_web_and_binary.sh`。
#
# 用法：bash deploy/release_20261122_qualityfix.sh            # 干跑（编译＋打包＋全部自检，不碰服务器）
#       APPLY=1 bash deploy/release_20261122_qualityfix.sh    # 真换（scp → swap → 复验）
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
APPLY="${APPLY:-0}"
TAG="20261122quality"

TAR="/tmp/fe-$TAG.tar.gz"
SERVER_BIN="${SERVER_BIN:-/tmp/translator-server-linux-$TAG}"
SWAP="$SCRIPT_DIR/swap_web_and_binary.sh"

SRC="$SCRIPT_DIR/deploy_to_production.sh"
HOST="$(sed -n 's/^REMOTE_HOST="${DEPLOY_HOST:-\(.*\)}"/\1/p' "$SRC")"
PORT="$(sed -n 's/^REMOTE_PORT="${DEPLOY_PORT:-\(.*\)}"/\1/p' "$SRC")"
[ -n "$HOST" ] && [ -n "$PORT" ] || { echo "ABORT=1 主机/端口现读失败（读 ${SRC}）"; exit 2; }

sha_local() { shasum -a 256 "$1" | awk '{print $1}'; }

# feat_count <文件> <字面量> —— 按**字节**数出现次数。
# ★ 不用 `grep -a -c -F`：macOS 的 grep 在当前 locale 下对含中文/破折号的字面量会**静默回 0**
#   （同一份二进制用 Python 按字节数是 1）。这类假 0 会把"编错了树"与"判据看不见"混成同一条红，
#   而真正该拦的"特征串被换成空口径"反而放过去。判据必须与 locale 无关。
feat_count() {
  python3 - "$1" "$2" <<'PY'
import sys
path, needle = sys.argv[1], sys.argv[2]
data = open(path, 'rb').read()
print(data.count(needle.encode('utf-8')))
PY
}

echo "===== P0 前端构建 → 侧车 → 打包（顺序只有一种：build → precompress → tar → 自检）====="
(cd "$REPO/frontend-react" && npx vite build) || { echo "ABORT=2 vite build 失败"; exit 2; }
# ★ 构建会把 `public/.DS_Store` 原样拷进 dist（Vite 复制 public 全目录）。
#   不在这里清掉，预压缩脚本会直接 ABORT=3，而"删一次 dist"只是治了这一次。
find "$REPO/frontend-react/dist" \( -name '.DS_Store' -o -name '._*' \) -delete
node "$REPO/scripts/precompress_dist.mjs" || { echo "ABORT=2 预压缩失败"; exit 2; }
rm -f "$TAR"
(
  cd "$REPO/frontend-react/dist" &&
    COPYFILE_DISABLE=1 tar --exclude='.DS_Store' --exclude='._*' --exclude='./wasm/sherpa-onnx/*.data' -czf "$TAR" .
) || { echo "ABORT=2 打包失败"; exit 2; }
echo "tar=$TAR size=$(wc -c <"$TAR" | tr -d ' ') sha=$(sha_local "$TAR" | cut -c1-16)"

echo "----- P0-T 交付包自检（与 deploy_to_production.sh、手工链共用同一把尺子）-----"
bash "$REPO/scripts/check_frontend_tar.sh" "$TAR" || { echo "ABORT=2 交付包自检没过 ⇒ 不许换源"; exit 2; }

echo "===== P0-B 后端件 ====="
(cd "$REPO/backend-go" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$SERVER_BIN" ./cmd/server) \
  || { echo "ABORT=3 server 编译失败"; exit 2; }
# 特征串一律取**运行期真会引用的字符串字面量**（提示词正文 / 配置键 / 日志前缀），不取函数名。
#   · 「数字量级口径」＝magnitudeInstructionNote 的中文条款首句（真拼进提示词）；
#   · 「80,000–1,200,000」＝同一条款的真值示例（改口径必改它，等于这一腿的指纹）；
#   · 「[file-ctx-retry]」＝上下文重译腿的日志前缀（上一批已有，留作同族回归哨兵）；
#   · 「instant_review_enabled」＝复核开关配置键（上一批已有，留作同族回归哨兵）；
#   · 「אלף」＝希伯来语千档量级词（第二批 `numSuffixMults` 的 map 键，运行期真被查表）。
W_MAG=$(feat_count "$SERVER_BIN" "数字量级口径")
W_SAMPLE=$(feat_count "$SERVER_BIN" "800,000–1,200,000")
W_CTX=$(feat_count "$SERVER_BIN" "[file-ctx-retry]")
W_INST=$(feat_count "$SERVER_BIN" "instant_review_enabled")
W_ALEF=$(feat_count "$SERVER_BIN" "אלף")
W_EXPECT=$(feat_count "$SERVER_BIN" "exact converted values")
W_BUDGET=$(feat_count "$SERVER_BIN" "上下文重译预算不足")
echo "server size=$(wc -c <"$SERVER_BIN" | tr -d ' ') sha=$(sha_local "$SERVER_BIN") ｜ 数字量级口径=${W_MAG} 真值示例=${W_SAMPLE} file-ctx-retry=${W_CTX} instant_review_enabled=${W_INST} אלף=${W_ALEF} 换算注入=${W_EXPECT} 预算点名=${W_BUDGET}（期望都 ≥1）"
[ "$W_MAG" != "0" ] || { echo "ABORT=3 缺特征串「数字量级口径」⇒ 编译的不是含本批量级条款的树"; exit 3; }
[ "$W_SAMPLE" != "0" ] || { echo "ABORT=3 缺特征串「800,000–1,200,000」⇒ 量级条款被换成了空口径"; exit 3; }
[ "$W_CTX" != "0" ] || { echo "ABORT=3 缺特征串 [file-ctx-retry]（上下文重译腿）"; exit 3; }
[ "$W_INST" != "0" ] || { echo "ABORT=3 缺特征串 instant_review_enabled（复核开关）"; exit 3; }
[ "$W_ALEF" != "0" ] || { echo "ABORT=3 缺特征串 אלף（希伯来语千档量级词）⇒ 编译的不是含 tv8 收口的树"; exit 3; }
[ "$W_EXPECT" != "0" ] || { echo "ABORT=3 缺特征串「exact converted values」⇒ 编译的不是含 tv9 换算注入的树"; exit 3; }
[ "$W_BUDGET" != "0" ] || { echo "ABORT=3 缺特征串「上下文重译预算不足」⇒ 编译的不是含 tv9 分档的树"; exit 3; }
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

echo "===== P2 服务器侧换源＋换件（常设执行体：两段式，两站全绿才落位）====="
ssh -p "$PORT" "root@$HOST" \
  "bash /tmp/swap_web_and_binary.sh /tmp/fe-$TAG.tar.gz $TAR_SHA /tmp/ts-new-$TAG $BIN_SHA"
rc=$?
echo "P2 远端退码=${rc}（0 且屏上有 SWAP_ALL_OK=1 才算换成功）"
exit "$rc"
