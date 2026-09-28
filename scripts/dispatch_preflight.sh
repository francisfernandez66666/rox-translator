#!/usr/bin/env bash
# ============ dispatch_preflight.sh · 职责说明 ============
# 派发**开闸前的一致性门禁**（改造方案 §10 的 G1～G4，2026-09-28 P6 落地）。
#
# 它回答一个问题：**这台体验机跑出来的 PDF，会不会和主站跑出来的不一样？**
# 这一问不能用"服务能起来"来答——脚本差一行、库差一个小版本、字体少一族，
# 两侧照样"成功"，只是客户拿到的版式/字形不一样，而这类差异不会在任何日志里报错。
# 所以这四项必须**现读现比**，不写死任何版本号与族数（写死就是一装一卸即假红）。
#
# 四项判据：
#   G1 脚本指纹等值：转换脚本 + 随包兜底字体，两侧 sha256 逐字相等（缺一即不派）；
#   G2 库版本等值：PyMuPDF / fpdf2 / pdf2docx 两侧版本串相等（importlib 现读，不写死）；
#   G3 字体族集合：远端 fc-list :lang=zh 的**族名集合** ⊇ 主站集合（多装不算漂移）；
#   G4 产物一致性抽验：同一份样张两侧各转一次，比对段数与逐段内容（★ 不比字节 sha——
#      PDF 内含 CreationDate 与子集字体序号，天然逐次不同，那是恒红锁）。
#
# ★ 远端数据只走 fpdexec.py 协议（体验机的 sshd 是 ForceCommand=fpd-bridge，
#   拿不到 shell），所以本脚本不 ssh 任何"命令"，只喂 stdin 上的一行 base64 header。
#   这与 Go 侧 DispatchRun 走的是**同一条协议**，不存在"脚本能过、线上过不了"的分叉。
#
# 用法（在**主站**执行，须为 root 或 translator，且能 BatchMode 拨通体验机）：
#   FPD_SSH=fpd@1.2.3.4 FPD_KEY=/etc/translator/dispatch_ed25519 \
#   FPD_SAMPLE=/opt/translator/data/_uploads/sample.pdf \
#   bash scripts/dispatch_preflight.sh
#
# 退出码：0 = 四项全过（可以开闸）；非 0 = 任一不过（**保持派发关闭**）。
#   ★ 口径：本脚本失败不修任何东西、不改任何配置，它只是一把尺子。
# =============================================================================
set -u

FPD_SSH="${FPD_SSH:-}"
FPD_KEY="${FPD_KEY:-/etc/translator/dispatch_ed25519}"
FPD_PORT="${FPD_PORT:-22}"
FPD_REMOTE_ROOT="${FPD_REMOTE_ROOT:-/opt/fpdispatch}"
FPD_LOCAL_BIN="${FPD_LOCAL_BIN:-/opt/translator/bin}"
FPD_LOCAL_PY="${FPD_LOCAL_PY:-/opt/translator/.venv/bin/python3}"
FPD_SAMPLE="${FPD_SAMPLE:-}"

PASS=0; FAIL=0
log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  ✅ %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '\033[1;31m❌\033[0m %s\n' "$*" >&2; FAIL=$((FAIL+1)); }
warn() { printf '\033[1;33m⚠️\033[0m %s\n' "$*"; }

[ -n "$FPD_SSH" ] || { bad "必须给 FPD_SSH=fpd@<ip>（派发专用账号，不是管理员账号）"; exit 2; }
[ -f "$FPD_KEY" ] || { bad "私钥不存在：$FPD_KEY"; exit 2; }
[ -d "$FPD_LOCAL_BIN" ] || { bad "主站 bin 目录不存在：$FPD_LOCAL_BIN（本脚本须在主站执行）"; exit 2; }
[ -x "$FPD_LOCAL_PY" ] || FPD_LOCAL_PY="$(command -v python3 || true)"
[ -n "$FPD_LOCAL_PY" ] || { bad "主站找不到 python3（G2/G4 需要它现读版本与跑样张）"; exit 2; }

SSH=(ssh -i "$FPD_KEY" -o BatchMode=yes -o ConnectTimeout=15 -p "$FPD_PORT" "$FPD_SSH")
if command -v shasum >/dev/null 2>&1; then DIGEST() { shasum -a 256 "$1" | awk '{print $1}'; }
else DIGEST() { sha256sum "$1" | awk '{print $1}'; }; fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/fpd-preflight.XXXXXX")"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT

# ---------------------------------------------------------------- 取远端实测值（唯一入口 = fpdexec 协议）
log "[0/4] 拨通体验机并取回 probe（远端唯一入口 fpdexec.py）"
# ★ 协议修正（2026-09-29）：fpdexec.py 的 header 第一行是 base64(JSON)（见 fpdexec.py:563
#   `base64.b64decode(head_line)`），此前本脚本发的是裸 JSON ⇒ 远端报
#   "header 解析失败: Invalid base64-encoded string"、preflight 永远判不过、开不了闸。
#   此处与 G4 的 put/run 保持一致：先 base64 再发。
printf '%s\n' "$(printf '%s' '{"mode":"probe"}' | base64 | tr -d '\n')" >"$TMP/probe.in"
if ! "${SSH[@]}" >"$TMP/probe.out" 2>"$TMP/probe.err" <"$TMP/probe.in"; then
  bad "probe 失败（BatchMode 下多为私钥/kown_hosts 问题）：$(tail -3 "$TMP/probe.err" | tr '\n' ' ')"
  exit 3
fi
PROBE_JSON="$(tail -1 "$TMP/probe.out")"
if [ -z "$PROBE_JSON" ] || ! printf '%s' "$PROBE_JSON" | python3 -c 'import sys,json;json.load(sys.stdin)' 2>/dev/null; then
  bad "probe 返回的不是 JSON：$(tail -3 "$TMP/probe.out" | tr '\n' ' ')"
  exit 3
fi
# 之后所有远端取值都走这一个 helper：不重复 ssh，也不缓存（缓存会掩盖"刚同步完又漂了"）
rp() { printf '%s' "$PROBE_JSON" | python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

if [ "$(rp 'd.get("expired")')" = "True" ]; then
  bad "远端已标记 EXPIRED（到期日 $(rp 'd.get("expire_date")')，今天 $(rp 'd.get("today")')）⇒ 不许开闸"
  exit 4
fi
ok "远端在线 mem=$(rp 'round(d.get("mem_gb",0),1)')G cpu=$(rp 'd.get("cpu")') 到期日 $(rp 'd.get("expire_date")')"

# ---------------------------------------------------------------- G1 脚本指纹等值
log "[1/4] G1 脚本指纹等值（两侧 sha256 逐字相等）"
# ★ 比对清单与 Go 侧 DispatchRun 实际会用到的脚本保持一致。
#   兜底字体**不在这个循环里**：远端 probe 把它单独放在 fonts.fallback_ttf_sha（不在 script_sha 里），
#   拿脚本那把钥匙去开字体这把锁会恒等失败——它是 D5 那颗雷，下面单独判一次。
FINGERPRINTS="pdf_overlay.py pdfwrite.py anydoc_md.py docx_translate.py docx_table_selftest.py"
G1_FAIL=0
for rel in $FINGERPRINTS; do
  local_file="$FPD_LOCAL_BIN/$rel"
  if [ ! -s "$local_file" ]; then
    bad "G1 主站缺 $local_file ⇒ 主站自己就是坏的（先跑 scripts/sync_fileproc_assets.sh）"
    G1_FAIL=1
    continue
  fi
  want="$(DIGEST "$local_file")"
  got="$(rp 'd.get("script_sha",{}).get("'"$rel"'","")')"
  if [ "$want" = "$got" ] && [ -n "$got" ]; then
    ok "G1 $rel 等值 ${want:0:12}…"
  else
    bad "G1 $rel 不等值/远端缺失  主站=${want:0:12}  远端=${got:0:12} ⇒ 先跑 scripts/dispatch_sync.sh"
    G1_FAIL=1
  fi
done
# 兜底字体单独判（现网 D5 的同款死法就在这里：脚本齐全、字体没落盘 ⇒ apply 进页面循环必崩）
TTF_REL="assets/fonts/DroidSansFallbackFull.ttf"
if [ -s "$FPD_LOCAL_BIN/$TTF_REL" ]; then
  TTF_WANT="$(DIGEST "$FPD_LOCAL_BIN/$TTF_REL")"
  TTF_GOT="$(rp 'd.get("fonts",{}).get("fallback_ttf_sha","")')"
  if [ "$TTF_WANT" = "$TTF_GOT" ] && [ -n "$TTF_GOT" ]; then
    ok "G1 $TTF_REL 等值 ${TTF_WANT:0:12}…（D5 那颗雷已排）"
  else
    bad "G1 $TTF_REL 不等值/远端缺失  主站=${TTF_WANT:0:12}  远端=${TTF_GOT:0:12}"
    bad "    ⇒ 主站缺它就是现网 D5 的死法（原版式 100% 静默降级）；远端缺它则派出去的 apply 必崩。"
    G1_FAIL=1
  fi
else
  bad "G1 主站缺 $FPD_LOCAL_BIN/$TTF_REL ⇒ 主站自己还坏着，先跑 scripts/sync_fileproc_assets.sh"
  G1_FAIL=1
fi
[ "$G1_FAIL" -eq 0 ] || warn "  G1 不过 ⇒ Go 侧就绪判据会判不就绪，派发自动退回本地（产品不受影响，但也没有收益）"

# ---------------------------------------------------------------- G2 库版本等值
log "[2/4] G2 库版本等值（现读现比，不写死版本号）"
LOCAL_LIBS="$TMP/local_libs.json"
"$FPD_LOCAL_PY" - <<'PY' >"$LOCAL_LIBS"
import json, importlib.metadata as md
out = {}
for disp, imp in (("pymupdf", "pymupdf"), ("fpdf2", "fpdf2"), ("pdf2docx", "pdf2docx")):
    try:
        out[disp] = md.version(disp)
    except Exception:
        out[disp] = ""
print(json.dumps(out))
PY
G2_FAIL=0
for lib in pymupdf fpdf2 pdf2docx; do
  want="$(python3 -c "import json;print(json.load(open('$LOCAL_LIBS')).get('$lib',''))")"
  got="$(rp 'd.get("libs",{}).get("'"$lib"'","")')"
  if [ -n "$want" ] && [ "$want" = "$got" ]; then
    ok "G2 $lib 等值 $want"
  else
    bad "G2 $lib 不等值  主站=${want:-<未装>}  远端=${got:-<未装>}"
    G2_FAIL=1
  fi
done
# ★ 深判据：版本相等不等于能跑（现网"装了 pdf2docx 却没 pymupdf"就是被浅判据放过的形态）。
#   远端 selftest 真跑一次最小 PDF 的原版式 apply，退出码非 0 即判不就绪。
printf '%s\n' "$(printf '%s' '{"mode":"selftest"}' | base64 | tr -d '\n')" >"$TMP/st.in"
if "${SSH[@]}" >"$TMP/st.out" 2>"$TMP/st.err" <"$TMP/st.in"; then
  ok "G2 远端 selftest 退出码 0（真跑通了原版式链，含随包兜底字体）"
else
  bad "G2 远端 selftest 失败：$(tail -5 "$TMP/st.out" "$TMP/st.err" | tr '\n' ' ')"
  G2_FAIL=1
fi

# ---------------------------------------------------------------- G3 字体族集合
log "[3/4] G3 字体族集合（远端 ⊇ 主站，多装不算漂移）"
LOCAL_FAMS="$TMP/local_fams.txt"
if command -v fc-list >/dev/null 2>&1; then
  # 只取族名（fc-list 的第二个冒号分段），排序去重用 LC_ALL=C（★ BSD sort 在 UTF-8 下会折叠中文行）
  fc-list :lang=zh family 2>/dev/null | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' \
    | LC_ALL=C sort -u | grep -v '^$' >"$LOCAL_FAMS" || true
else
  warn "  主站没有 fc-list ⇒ 主站集合为空，G3 退化为恒真（只验远端非空）"
  : >"$LOCAL_FAMS"
fi
REMOTE_FAMS="$TMP/remote_fams.txt"
rp 'd.get("fonts",{}).get("zh_families",[])' \
  | python3 -c "import sys,ast;print('\n'.join(ast.literal_eval(sys.stdin.read()) or []))" \
  | LC_ALL=C sort -u >"$REMOTE_FAMS" || true
LOCAL_N="$(wc -l <"$LOCAL_FAMS" | tr -d ' ')"
REMOTE_N="$(wc -l <"$REMOTE_FAMS" | tr -d ' ')"
# 差集：主站有而远端没有的族（这才是会变成方框的那些）
MISSING="$(LC_ALL=C comm -23 "$LOCAL_FAMS" "$REMOTE_FAMS" | tr '\n' ' ')"
if [ -z "$MISSING" ]; then
  ok "G3 远端族集合 ⊇ 主站（主站 $LOCAL_N 族 / 远端 $REMOTE_N 族）"
else
  bad "G3 远端缺这些中文字体族：$MISSING"
  bad "    ⇒ 缺族的直接后果是译文变方框（西文变方框那类故障的同款形态），不许开闸。"
  bad "    修复：远端 apt-get install -y fonts-noto-cjk；阿里普惠体不是 apt 包，用 dispatch_sync.sh 的 FPD_FONTS_DIR 传。"
fi
if [ "$(rp 'd.get("fonts",{}).get("fallback_ttf")')" != "True" ]; then
  bad "G3 远端缺随包兜底字体 assets/fonts/DroidSansFallbackFull.ttf ⇒ 派出去的 apply 必崩（D5 同款）"
fi

# ---------------------------------------------------------------- G4 产物一致性抽验
log "[4/4] G4 产物一致性抽验（同一份样张两侧各转一次）"
if [ -z "$FPD_SAMPLE" ] || [ ! -s "$FPD_SAMPLE" ]; then
  warn "  未给 FPD_SAMPLE ⇒ 跳过 G4。**开闸前必须补跑一次**（G1~G3 全过也只能说明环境一致，"
  warn "  证明不了"两侧产出一样"；这一步不做，就是在用开闸后的第一单真实客户件做实验）。"
else
  SAMPLE_BASENAME="$(basename "$FPD_SAMPLE")"
  # ① 主站侧 extract
  if ! "$FPD_LOCAL_PY" "$FPD_LOCAL_BIN/pdf_overlay.py" extract "$FPD_SAMPLE" >"$TMP/local_extract.json" 2>"$TMP/local_extract.err"; then
    bad "G4 主站 extract 失败：$(tail -3 "$TMP/local_extract.err" | tr '\n' ' ')"
  else
    # ② 远端侧 extract：走 fpdexec 的 put → run 两步（与主站 DispatchRun 同协议）
    REMOTE_IN="$FPD_REMOTE_ROOT/w/preflight/in_${SAMPLE_BASENAME}"
    H1="$(printf '{"mode":"put","path":"%s"}' "$REMOTE_IN" | base64 | tr -d '\n')"
    { printf '%s\n' "$H1"; cat "$FPD_SAMPLE"; } >"$TMP/put.in"
    if ! "${SSH[@]}" >"$TMP/put.out" 2>"$TMP/put.err" <"$TMP/put.in"; then
      bad "G4 样张上传远端失败：$(tail -3 "$TMP/put.err" | tr '\n' ' ')"
    else
      H2="$(printf '{"mode":"run","script":"pdf_overlay.py","argv":["pdf_overlay.py","extract","%s"],"outputs":[]}' "$REMOTE_IN" | base64 | tr -d '\n')"
      printf '%s\n' "$H2" >"$TMP/run.in"
      if ! "${SSH[@]}" >"$TMP/run.out" 2>"$TMP/run.err" <"$TMP/run.in"; then
        bad "G4 远端 extract 失败：$(tail -3 "$TMP/run.err" | tr '\n' ' ')"
      else
        tail -1 "$TMP/run.out" >"$TMP/remote_extract.json"
        VERDICT="$(python3 - "$TMP/local_extract.json" "$TMP/remote_extract.json" <<'PY'
import json, sys
def texts(p):
    try:
        d = json.load(open(p))
    except Exception:
        return None
    if isinstance(d, dict):
        return d.get("texts") or []
    return None
a, b = texts(sys.argv[1]), texts(sys.argv[2])
if a is None or b is None:
    print("UNPARSEABLE")
    sys.exit(0)
if len(a) != len(b):
    print("SEGCOUNT_DIFF %d %d" % (len(a), len(b)))
    sys.exit(0)
for i, (x, y) in enumerate(zip(a, b)):
    if x != y:
        print("SEG_DIFF %d %r != %r" % (i, x[:40], y[:40]))
        sys.exit(0)
print("EQUAL %d" % len(a))
PY
)"
        case "$VERDICT" in
          EQUAL*) ok "G4 两侧 extract 段数与逐段内容相等（${VERDICT#EQUAL } 段）" ;;
          SEGCOUNT_DIFF*) bad "G4 两侧**段数不等**（$VERDICT）⇒ 版式切分口径已经漂了，产物必然不同" ;;
          SEG_DIFF*) bad "G4 两侧段内容不等（$VERDICT）⇒ 同一份件在不同机器上读出了不同的段" ;;
          *) bad "G4 两侧 extract 输出无法解析（$VERDICT）" ;;
        esac
      fi
    fi
  fi
  warn "  ⚠️ G4 的另外两小项（产物页数相等 / pdffonts 字体族一致 / 体积比 ±15%）属**写回**对比，"
  warn "     在本脚本里没做——它需要一次真实译文映射，放到开闸后第一单人工抽验（§9-A3/A4）里判，"
  warn "     判据是日志里必须出现「原地替换完成」而不是「降级版式重建」。"
fi

echo ""
if [ "$FAIL" -eq 0 ]; then
  echo "✅ preflight 全过（$PASS 项）：两侧环境一致，可以走 scripts/dispatch_apply.sh 开闸"
  exit 0
fi
echo "❌ preflight 未过（通过 $PASS / 失败 $FAIL）⇒ **保持派发关闭**，先修再开"
exit 1
