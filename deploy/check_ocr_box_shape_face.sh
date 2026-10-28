#!/usr/bin/env bash
# ============================================================================
# check_ocr_box_shape_face.sh — 图片版式回退的**现网只读探针**（★ 2026-10-28 慢链缓解批）
#
# 问的问题只有一个：**回退后的识别脚本在真权重上吐的框，是不是回到「逐视觉元素一框」**。
# 为什么必须有这一支（而不是"图能打开就算过"）：
#   本批摘掉的是「同行并框」与「双档重跑」，它的坏形态是**静默**的——
#   换件不带 .py（AGENTS §一·17），所以"二进制是新的、磁盘上还是旧识别脚本"完全可能；
#   而框并错了在界面上只是"图又花了"，日志零红。只有把同一张图喂给**两份脚本**各跑一次、
#   数框、比坐标，才能在客户报障之前抓住它。
#
# 三条判据（全只读，不写库、不建单、不碰 caddy、不重启任何单元）：
#   F1 落盘指纹：`/opt/translator/bin/ocr_recognize.py` 的 sha256 必须**等于**仓库那份（换件不带 .py 那一族）；
#   F2 框粒度：同一张图跑「现役脚本」与「回退前脚本（`9a45bcbd^` 那份，随本探针上传）」，
#      现役框数必须 **≥** 回退前框数（并框会把数压小；反过来就是回归复发）；
#   F3 出声：两份都退 0 且都吐出可解析 JSON（识别挂了不能读成"框数相等"）。
#
# 用法（在服务器本机跑，或经 ssh 包一层）：
#   bash check_ocr_box_shape_face.sh <图片路径> [回退前脚本路径]
#   判绿只认末行 `OCRBOXFACE_ALL_OK=1`；三态：0 全绿／1 有红项／2 有项**没测到**（链路或权重问题，复跑再判）。
# ⚠️ 参照图是**用户私有交易截图**：本探针只读它、不复制进仓库、不进 git；跑完留在 /tmp 由人清理。
# ============================================================================
set -uo pipefail

IMG=${1:?用法: bash check_ocr_box_shape_face.sh <图片路径> [回退前脚本路径]}
PRE=${2:-/tmp/ocr_recognize_pre_9a45bcbd.py}
ROOT=${ROOT:-/opt/translator}
PY=${FILEPROC_PYTHON_BIN:-python3}
CUR="$ROOT/bin/ocr_recognize.py"
REPO_SHA_FILE=${REPO_SHA_FILE:-}   # 可选：把本机现读的仓库 sha 传进来核 F1（不传则只出现代 sha）

FAIL=0
UNMEASURED=0
ok()  { echo "OK   $*"; }
bad() { echo "BAD  $*"; FAIL=$((FAIL + 1)); }
skip(){ echo "⏳   $*"; UNMEASURED=$((UNMEASURED + 1)); }

# ---- F1 落盘指纹：换件不带 .py，这一条是"新识别真落地"的唯一物证 ----
if [ ! -f "$CUR" ]; then
  bad "现役识别脚本不在位：$CUR"
else
  CUR_SHA=$(sha256sum "$CUR" | cut -d' ' -f1)
  echo "F1 现役 ocr_recognize.py sha=${CUR_SHA}"
  if [ -n "$REPO_SHA_FILE" ] && [ -s "$REPO_SHA_FILE" ]; then
    WANT=$(head -1 "$REPO_SHA_FILE" | tr -d ' \n')
    [ "$CUR_SHA" = "$WANT" ] && ok "F1 与仓库那份逐字节等值（新识别已落盘）" \
      || bad "F1 与仓库不等值（这台还在跑旧识别！期望 ${WANT}）⇒ 跑 sync_fileproc_assets.sh"
  else
    skip "F1 未传 REPO_SHA_FILE ⇒ 只出现代 sha，没做等值判定（别把没核到读成核过了）"
  fi
fi

# 框数：从 stdout JSON 里数 `boxes` 数组的元素个数（纯标准库，不依赖 jq）。
box_count() {
  python3 - <<'PY' "$1"
import json, sys
try:
    d = json.loads(sys.argv[1])
except Exception:
    print("PARSE_FAIL")
    sys.exit(0)
b = d.get("boxes") or []
print(len(b))
PY
}

# ---- 跑一份脚本，回 "退码|框数"（000 档交给调用方判没测到）----
run_one() {
  local script="$1" out rc json
  if [ ! -f "$script" ]; then echo "MISSING|-"; return; fi
  out=$("$PY" "$script" image "$IMG" 2>/tmp/ocrboxface.err); rc=$?
  if [ "$rc" -ne 0 ]; then echo "RC$rc|-"; return; fi
  json=$(printf '%s' "$out" | tail -1)
  echo "RC0|$(box_count "$json")"
}

# ---- F2/F3 框粒度对照 ----
if [ ! -f "$PRE" ]; then
  skip "F2 没有回退前那份脚本（${PRE}）⇒ 只出现代框数，做不了对照"
fi
CUR_R=$(run_one "$CUR")
PRE_R=$(run_one "$PRE")
echo "F2 现役=${CUR_R} 回退前=${PRE_R}"

cur_rc=${CUR_R%%|*}; cur_n=${CUR_R##*|}
pre_rc=${PRE_R%%|*}; pre_n=${PRE_R##*|}

if [ "$cur_rc" != "RC0" ]; then
  if [ "$cur_rc" = "MISSING" ]; then bad "F3 现役脚本不在位，跑不了"; else
    skip "F3 现役识别没跑通（${cur_rc}，stderr 末行：$(tail -1 /tmp/ocrboxface.err 2>/dev/null)）⇒ 判没测到，不判产品缺陷"
  fi
elif [ "$pre_rc" = "RC0" ]; then
  if [ "$cur_n" -ge "$pre_n" ] && [ "$pre_n" -gt 0 ]; then
    ok "F2 框数 ${cur_n} ≥ 回退前 ${pre_n}（逐元素一框回来了；并框会把数压小）"
  elif [ "$pre_n" -eq 0 ]; then
    skip "F2 回退前那版吐 0 框（对照靶子无效）⇒ 换一张有字的图再判"
  else
    bad "F2 框数 ${cur_n} < 回退前 ${pre_n} ⇒ 并框形态还在/又长回来了，按 AGENTS §一·21 那条查 `_merge_fragments` 是否被写回"
  fi
else
  skip "F3 回退前对照件没跑通（${pre_rc}）⇒ F2 无从判，别拿单边读数当通过"
fi

echo "OCRBOXFACE_FAIL=$FAIL UNMEASURED=$UNMEASURED"
if [ "$FAIL" -gt 0 ]; then echo "OCRBOXFACE_EXIT=1"; exit 1; fi
if [ "$UNMEASURED" -gt 0 ]; then echo "OCRBOXFACE_EXIT=2"; exit 2; fi
echo "OCRBOXFACE_ALL_OK=1"; exit 0
