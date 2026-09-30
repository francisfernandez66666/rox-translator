#!/usr/bin/env bash
# ============ dispatch_preflight.sh · 职责说明 ============
# 派发**开闸前的一致性门禁**（改造方案 §10 的 G1～G4 ＋ 2026-09-29 补的 G5，P6 落地）。
#
# 它回答一个问题：**这台体验机跑出来的 PDF，会不会和主站跑出来的不一样，而且送得回来？**
# 这一问不能用"服务能起来"来答——脚本差一行、库差一个小版本、字体少一族，
# 两侧照样"成功"，只是客户拿到的版式/字形不一样，而这类差异不会在任何日志里报错。
# 所以这五项必须**现读现比**，不写死任何版本号与族数（写死就是一装一卸即假红）。
#
# 五项判据：
#   G1 脚本指纹等值：转换脚本 + 随包兜底字体，两侧 sha256 逐字相等（缺一即不派）；
#   G2 库版本等值：PyMuPDF / fpdf2 / pdf2docx 两侧版本串相等（importlib 现读，不写死）；
#   G3 字体族集合：远端 fc-list :lang=zh 的**族名集合** ⊇ 主站集合（多装不算漂移）；
#   G4 产物一致性抽验：同一份样张两侧各转一次，比对段数与逐段内容（★ 不比字节 sha——
#      PDF 内含 CreationDate 与子集字体序号，天然逐次不同，那是恒红锁）；
#   G5 传输腿全往返：put→run(apply)→stat→get 走**真机**一遍，上传与回拉各比一次 sha256
#      （★ 这一项必须给 FPD_SAMPLE，无样张即判红，理由见下面 ★★ 段）。
#
# ★ 远端数据只走 fpdexec.py 协议（体验机的 sshd 是 ForceCommand=fpd-bridge，
#   拿不到 shell），所以本脚本不 ssh 任何"命令"，只喂 stdin 上的一行 base64 header。
#   这与 Go 侧 DispatchRun 走的是**同一条协议**。
#
# ★★ 光有 G1~G4 会漏掉一整类故障，这是 2026-09-29 真踩出来的：
#   G1~G4 判的是**两侧内容一致性**（脚本指纹／库版本／字体集合／样张产物），
#   它们从没判过**传输通路本身**：主站当时用的是裸 `ssh 'mkdir -p'` 与 `scp`，
#   这两条腿在 ForceCommand 下是结构性死路（mkdir 实测退 78、scp 挂墙钟或静默非零），
#   而本脚本当时照样四项全绿——因为它自己一直走协议，主站却不走。
#   ⇒ 所以 09-29 把判据补成两条腿（09-30 开闸当天又加第三条凭据腿，见下面 ③）：
#     ① G5（本脚本）＝**真机**把"上传→转换→查尺寸→取回→逐字节等值"整条拨一遍，
#        并就地打印定档要用的体积/耗时读数；无 FPD_SAMPLE 时 G5 **判红不判跳过**，
#        因为"跳过还能全绿exit 0"正是上面那次事故的形状。
#     ② Go 侧单测（fileproc_remote_test.go）＝把同一套协议形状钉进接线：桩只认协议，
#        裸 mkdir／scp 调用一律照真机一样判红，上传与回拉都比 sha256。
#   开闸前两边都要绿，缺一边就是把"我编的远端"当成了真远端、或把"我编的脚本通路"当成了主站通路。
#
#   ③ ★ 2026-09-30 开闸当天再加一条硬前置（凭据面，见下面 [0/5] 段）：**服务账号真读得到那把私钥**，
#      且远端主机键有一个能落盘的 known_hosts。首开实测——钥匙在 /etc/translator（750 root:root）里，
#      服务账号 translator 连目录都进不去；systemd 的 EnvironmentFile 由 PID 1(root) 读，所以开关值读到了、
#      健康面只回 degraded，现象正是"预检全绿＋配置全对、但一单都不派、全被降级链兜回本地"。
#      这一腿过去只有 dispatch_apply.sh 会修，预检这把尺子量不到 ⇒ G1~G5 的绿灯会被读成"通路可用"，
#      和 §★★ 段当年那句"与 Go 侧同一条协议"是同一类错。现在它判得着：不读＝判红，不跳过。
#
# 用法（在**主站**执行，须为 root 或 translator，且能 BatchMode 拨通体验机）：
#   FPD_SSH=fpd@1.2.3.4 FPD_KEY=/etc/translator-dispatch/dispatch_ed25519 \
#   FPD_SAMPLE=/opt/translator/data/_uploads/sample.pdf \
#   bash scripts/dispatch_preflight.sh
#   （★ 私钥默认落点与 dispatch_apply.sh 同一个目录，两份脚本不许各写一个路径——
#     挪完钥匙再跑预检会直接报"私钥不存在"，那条绿灯也就没了。同源锁见
#     fileproc/dispatch_scripts_gate_test.go 的 TestPreflightAndApplyShareCredentialPaths。）
#
# 退出码：0 = 凭据面＋G1~G5 全过（可以开闸）；非 0 = 任一不过（**保持派发关闭**）。
#   ★ 口径：本脚本失败不修任何东西、不改任何配置，它只是一把尺子。
# =============================================================================
set -u

FPD_SSH="${FPD_SSH:-}"
# ★ 私钥默认落点＝/etc/translator-dispatch（与 dispatch_apply.sh 同值，2026-09-30 对齐）。
#   这里过去写的是 /etc/translator/dispatch_ed25519——那个目录 750 root:root，服务账号进不去，
#   apply 脚本已会把钥匙挪出到 translator 自有的目录；预检若还盯着旧路径，就会出现
#   "挪完钥匙 ⇒ 预检报私钥不存在 ⇒ 有人把预检跳过去 ⇒ 开闸开的是一把没人量过的闸"。
FPD_KEY="${FPD_KEY:-/etc/translator-dispatch/dispatch_ed25519}"
# 远端主机指纹文件：默认取 apply 脚本铺的那份；若主站已写过 dispatch.env，
# 就以 **dispatch.env 里的现值**为准（同一把锁只能有一个来源，见 [0/5] 段的凭据面判据）。
FPD_KNOWN_HOSTS="${FPD_KNOWN_HOSTS:-/etc/translator-dispatch/known_hosts}"
FPD_ENV_FILE="${FPD_ENV_FILE:-/etc/translator/dispatch.env}"
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

# ---------------------------------------------------------------- 凭据面（★ 2026-09-30 开闸当天加的第三条腿）
# 判的是"主站那个**跑业务的进程**能不能用这套凭据拨出去"，不是"我 root 手工能不能拨通"。
# 这两件事在过去是分开的，于是出现过：预检 17 项全绿、配置写下去了、服务也重启了，
# 健康面却永远回 degraded —— 因为钥匙在 750 root:root 的目录里，服务账号进不去。
# 所以这里的判据一律问"服务账号 test -r"，并且**判不到就判红**（跳过＝把下一次事故的形状原样留给开闸）。
SVC_USER=""
if command -v systemctl >/dev/null 2>&1; then
  SVC_USER="$(systemctl show -p User --value translator.service 2>/dev/null || true)"
fi
as_svc() {
  if command -v runuser >/dev/null 2>&1; then runuser -u "$SVC_USER" -- "$@" 2>/dev/null
  elif command -v sudo >/dev/null 2>&1; then sudo -n -u "$SVC_USER" "$@" 2>/dev/null
  else return 127; fi
}
if [ -z "$SVC_USER" ]; then
  bad "取不到 translator.service 的运行账号（systemctl 不可用或该 unit 不存在）⇒ 凭据面无法判读，不许开闸
  本脚本只在主站执行；换机器跑请连 G1~G5 一起重跑，别拿这里的旧绿灯开闸。"
elif [ "$SVC_USER" = "root" ]; then
  ok "凭据面：服务以 root 运行 ⇒ 私钥可读性按当前用户判（$FPD_KEY 本脚本已确认可读）"
elif ! as_svc test -r "$FPD_KEY"; then
  bad "私钥 $FPD_KEY 对服务账号 $SVC_USER 不可读 ⇒ 派发一单都拨不出去（会被降级链静默兜回本地，客户无感、健康面只见 degraded）
  目录口径：$(stat -c '%a %U:%G' "$(dirname "$FPD_KEY")" 2>/dev/null || echo 取不到)
  修法见 dispatch_apply.sh 的 ①b 段（把钥匙挪进 $SVC_USER 自有目录并 chmod 600）。"
else
  ok "凭据面：私钥对服务账号 $SVC_USER 实读通过"
fi

# 主机键文件：以 dispatch.env 现值为准（生产钉哪个文件，预检就用哪个文件），没有则用默认落点。
if [ -f "$FPD_ENV_FILE" ]; then
  ENV_KH="$(grep -E '^FILEPROC_DISPATCH_KNOWN_HOSTS=' "$FPD_ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d '\r' || true)"
  [ -n "$ENV_KH" ] && FPD_KNOWN_HOSTS="$ENV_KH"
fi
if [ -s "$FPD_KNOWN_HOSTS" ]; then
  if [ -n "$SVC_USER" ] && [ "$SVC_USER" != "root" ] && ! as_svc test -r "$FPD_KNOWN_HOSTS"; then
    bad "known_hosts $FPD_KNOWN_HOSTS 对服务账号 $SVC_USER 不可读 ⇒ 服务进程钉不住远端主机键"
  else
    ok "凭据面：远端主机键钉在 $FPD_KNOWN_HOSTS（$(grep -cvE '^[[:space:]]*$|^[[:space:]]*#' "$FPD_KNOWN_HOSTS" 2>/dev/null || echo 0) 条），ssh 与预检同用一个文件"
  fi
else
  warn "没有可用的 known_hosts（$FPD_KNOWN_HOSTS 不存在或为空）⇒ 本次预检走首见即信（accept-new）。"
  warn "  ⚠️ 服务账号（$SVC_USER）没有家目录且 unit 开了 ProtectHome=yes，accept-new 在生产上**记不下任何键**，"
  warn '     等于「谁给的主机键都认」。开闸前请先跑 dispatch_apply.sh 的 ①c 段把指纹铺好并带外核对。'
fi

# ssh 参数：有指纹文件就显式指过去（与 Go 侧 FILEPROC_DISPATCH_KNOWN_HOSTS 同一件事）。
#   刻意不用空数组展开——macOS bash 3.2 在 set -u 下会把它当未绑定变量打死。
if [ -s "$FPD_KNOWN_HOSTS" ]; then
  SSH=(ssh -i "$FPD_KEY" -o "UserKnownHostsFile=$FPD_KNOWN_HOSTS" -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=accept-new -p "$FPD_PORT" "$FPD_SSH")
else
  SSH=(ssh -i "$FPD_KEY" -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=accept-new -p "$FPD_PORT" "$FPD_SSH")
fi
if command -v shasum >/dev/null 2>&1; then DIGEST() { shasum -a 256 "$1" | awk '{print $1}'; }
else DIGEST() { sha256sum "$1" | awk '{print $1}'; }; fi

# now_ms：毫秒时戳。只有 G5 用它，而 G5 的耗时读数正是**定档输入**（09-29 实测：产物膨胀 3.4×、
#   回传 0.49MB/s，都是小数秒量级的事），所以不能用 bash 的 $SECONDS 整数秒。
#   又因 macOS 自带 date 没有 %N，统一走脚本已硬依赖的 python3。
now_ms() { python3 -c 'import time;print(int(time.time()*1000))'; }
# jf <python 表达式>：从 stdin 的**最后一行**解 JSON 后求值（远端把回执打在 stdout 末尾，
#   转换脚本可能在前面带 warn 行）。取不到就打印空串而不是抛栈——调用侧全部按"空串＝不合法"判红，
#   绝不给"解析失败被当成解析成功"留缝。
jf() {
  python3 -c 'import sys, json
raw = sys.stdin.read().strip()
try:
    d = json.loads(raw.splitlines()[-1])
except Exception:
    print("")
    sys.exit(0)
try:
    print(eval(sys.argv[1], {"d": d}))
except Exception:
    print("")' "$1"
}

TMP="$(mktemp -d "${TMPDIR:-/tmp}/fpd-preflight.XXXXXX")"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT

# ---------------------------------------------------------------- 取远端实测值（唯一入口 = fpdexec 协议）
log "[0/5] 拨通体验机并取回 probe（远端唯一入口 fpdexec.py）"
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
log "[1/5] G1 脚本指纹等值（两侧 sha256 逐字相等）"
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
log "[2/5] G2 库版本等值（现读现比，不写死版本号）"
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
  elif [ -z "$want" ] && [ -z "$got" ]; then
    # ★ 两侧都未装 ＝ **等值成立**（本项判据是"两侧会不会跑出不一样的产物"，不是"装没装齐"）：
    #   那一型转换在两侧都走同一条本地/降级路径，产物一致。把它判红会让门禁永远开不了闸，
    #   而真正的风险（一侧有一侧没有）由下一条分支管。
    ok "G2 $lib 两侧都未装 ⇒ 一致性成立（该型转换两侧同走降级路径，不是漂移）"
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
log "[3/5] G3 字体族集合（远端 ⊇ 主站，多装不算漂移）"
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
log "[4/5] G4 产物一致性抽验（同一份样张两侧各转一次）"
if [ -z "$FPD_SAMPLE" ] || [ ! -s "$FPD_SAMPLE" ]; then
  # ★ 这里只 warn 不 bad：G4 判的是"两侧产出一样"（内容一致性），缺样张时 G1~G3 仍然成立；
  #   而**传输通路**那一问由下面的 G5 硬判（缺样张 G5 直接红），所以"没样张还能 exit 0"这条路已经堵死。
  warn "  未给 FPD_SAMPLE ⇒ 跳过 G4（环境一致 ≠ 产出一致，开闸前仍建议补跑一次）"
else
  SAMPLE_BASENAME="$(basename "$FPD_SAMPLE")"
  # ① 主站侧 extract
  if ! "$FPD_LOCAL_PY" "$FPD_LOCAL_BIN/pdf_overlay.py" extract "$FPD_SAMPLE" >"$TMP/local_extract.json" 2>"$TMP/local_extract.err"; then
    bad "G4 主站 extract 失败：$(tail -3 "$TMP/local_extract.err" | tr '\n' ' ')"
  else
    # ② 远端侧 extract：走 fpdexec 的 put → run 两步（与主站 DispatchRun 同协议）
      REMOTE_REL="preflight/in_${SAMPLE_BASENAME}"
      REMOTE_IN="$FPD_REMOTE_ROOT/w/$REMOTE_REL"
      H1="$(printf '{"mode":"put","rel":"%s"}' "$REMOTE_REL" | base64 | tr -d '\n')"
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
  warn "     在本脚本里没做——它需要两侧各写回一次再比，放到开闸后第一单人工抽验（§9-A3/A4）里判，"
  warn "     判据是日志里必须出现「原地替换完成」而不是「降级版式重建」。"
fi

# ---------------------------------------------------------------- G5 传输腿全往返
log "[5/5] G5 传输腿全往返（上传→转换→查尺寸→取回→逐字节哈希等值）"
# ★★ 这一项是 2026-09-29 那次事故的直接产物：G1~G4 全绿的那几天，主站派发用的裸 mkdir/scp
#   在 ForceCommand 下一条都没走通过（客户件从没到过远端，全靠本地兜底在跑）。
#   一致性判据（内容一样）与传输判据（送得过去、取得回来）是两问，前者永远顶不了后者，
#   所以这里**必须真拨四条腿**，并且上传与回拉各比一次 sha256：
#     put  —— 远端回执 size+sha256  vs  本地实测（证明"远端存下来的就是这份"）；
#     run  —— 恒等译文写回（把 apply 这条**写路径**走一遍，extract 只读不写、证明不了产物能落盘）；
#     stat —— 产物在不在、多大、什么哈希（排障腿：分清"没做出来"与"做出来没搬回"）；
#     get  —— 原样字节流回主站，落盘后再比一次（网络中间任何一环节流/截断都在这里红）。
#   耗时读数一并打印：这三行就是**定档输入**（阈值按产物尺寸算，不是按输入尺寸）。
if [ -z "$FPD_SAMPLE" ] || [ ! -s "$FPD_SAMPLE" ]; then
  bad "G5 未给 FPD_SAMPLE ⇒ **判红而非跳过**：G1~G4 全绿也证明不了传输腿（那正是 09-29 的事故形态）。"
  bad "    给一份真实客户件重跑：FPD_SAMPLE=/opt/translator/data/_uploads/<某单>.pdf bash $0"
else
  G5="$TMP/g5"
  mkdir -p "$G5"
  # 远端 rel 用固定名，不嵌原始文件名：客户件名里可能有空格/中文/引号，
  # 让它们穿过 JSON+base64+远端文件系统三层是给自己造排障噪音（哈希等值才是本项判据）。
  G5_IN_REL="preflight_g5/in.pdf"
  G5_OUT_REL="preflight_g5/translated.pdf"
  G5_IN_ABS="$FPD_REMOTE_ROOT/w/$G5_IN_REL"
  G5_OUT_ABS="$FPD_REMOTE_ROOT/w/$G5_OUT_REL"
  LOCAL_SHA="$(DIGEST "$FPD_SAMPLE")"
  LOCAL_SIZE="$(wc -c <"$FPD_SAMPLE" | tr -d '[:space:]')"
  PUT_MS=0; RUN_MS=0; STAT_MS=0; GET_MS=0
  G5_PUT_OK=0; G5_RUN_OK=0

  # ① put：上传样张
  T0="$(now_ms)"
  H1="$(printf '{"mode":"put","rel":"%s"}' "$G5_IN_REL" | base64 | tr -d '\n')"
  { printf '%s\n' "$H1"; cat "$FPD_SAMPLE"; } >"$G5/put.in"
  if ! "${SSH[@]}" >"$G5/put.out" 2>"$G5/put.err" <"$G5/put.in"; then
    bad "G5① 上传失败：$(tail -3 "$G5/put.err" | tr '\n' ' ')"
  else
    PUT_MS=$(( $(now_ms) - T0 ))
    PUT_SHA="$(jf 'd.get("sha256","")' <"$G5/put.out")"
    PUT_SIZE="$(jf 'd.get("size",0)' <"$G5/put.out")"
    [ "$(jf 'd.get("ok") is True' <"$G5/put.out")" = "True" ] && G5_PUT_OK=1
    if [ "$G5_PUT_OK" = "1" ] && [ -n "$LOCAL_SHA" ] && [ "$PUT_SHA" = "$LOCAL_SHA" ] && [ "$PUT_SIZE" = "$LOCAL_SIZE" ]; then
      ok "G5① 上传 $LOCAL_SIZE B 用 ${PUT_MS}ms，远端回执 sha256 与本地**逐字节等值**"
    else
      bad "G5① 上传后远端回执与本地不等（本地 size=$LOCAL_SIZE sha=${LOCAL_SHA:0:12}… / 远端 size=$PUT_SIZE sha=${PUT_SHA:0:12}…，ok 回执=$G5_PUT_OK）"
      bad "    ⇒ 这一条不判就等于把客户的原件改坏了还交给客户（上传腿没有哈希比对是 09-29 前的形态）"
    fi
  fi

  # ② run(apply)：远端做一次**写回**，译文映射取"原文→原文"的恒等映射。
  #    为什么用恒等映射：本项要验的是"远端能把产物写到 w/ 之下并搬回来"，
  #    不是"翻得对不对"（那是 G4 与开闸后第一单的事）。恒等映射让 replaced/requested
  #    应当相等，顺带还证了一次"提取键与写回键同源"这条不变量。
  if [ "$G5_PUT_OK" != "1" ]; then
    warn "  G5②③④ 跳过（上传就没成，后面三条腿无从执行）⇒ 整项判红，先修 ①"
  else
    # 取键：优先用 G4 已在远端跑出的 extract 结果，退化到主站侧 extract（两者键应一致，G4 已比过）。
    KEYS="$G5/keys.json"
    if [ -s "$TMP/remote_extract.json" ]; then
      cp "$TMP/remote_extract.json" "$KEYS"
    elif [ -s "$TMP/local_extract.json" ]; then
      cp "$TMP/local_extract.json" "$KEYS"
    else
      HX="$(printf '{"mode":"run","script":"pdf_overlay.py","argv":["pdf_overlay.py","extract","%s"],"outputs":[]}' "$G5_IN_ABS" | base64 | tr -d '\n')"
      printf '%s\n' "$HX" >"$G5/extract.in"
      if "${SSH[@]}" >"$G5/extract.out" 2>"$G5/extract.err" <"$G5/extract.in"; then
        tail -1 "$G5/extract.out" >"$KEYS"
      else
        bad "G5② 远端 extract 取键失败：$(tail -3 "$G5/extract.err" | tr '\n' ' ')"
      fi
    fi
    NSEG=0
    if [ -s "$KEYS" ]; then
      NSEG="$(jf 'len(d.get("texts") or [])' <"$KEYS")"
      python3 - "$KEYS" "$G5/apply.payload" <<'PY' || bad "G5② 恒等译文映射构造失败"
import json, sys
d = json.load(open(sys.argv[1]))
texts = [t for t in (d.get("texts") or []) if isinstance(t, str) and t.strip()]
with open(sys.argv[2], "w", encoding="utf-8") as f:
    json.dump({"translations": {t: t for t in texts}}, f, ensure_ascii=False)
PY
    else
      bad "G5② 没拿到提取键（$KEYS 为空）⇒ 无法构造写回，产物落盘这条腿没验到"
    fi

    if [ -s "$G5/apply.payload" ]; then
      T0="$(now_ms)"
      # ★ 这一行刻意不折行（虽然丑）：闸门 TestDispatchHeaderProtocolIsBase64 的判据是
      #   "含 {\"mode\" 的那一行必须同一行出现 base64"，用 `\` 续行会把 base64 甩到下一行，
      #   于是这条锁会把自己判红——把它"美化"成两行＝让门禁对该 header 失明，不许改回去。
      HR="$(printf '{"mode":"run","script":"pdf_overlay.py","argv":["pdf_overlay.py","apply","%s","%s","zh"],"outputs":["%s"],"timeout_sec":%d}' "$G5_IN_ABS" "$G5_OUT_ABS" "$G5_OUT_ABS" "${FPD_G5_RUN_TIMEOUT:-300}" | base64 | tr -d '\n')"
      { printf '%s\n' "$HR"; cat "$G5/apply.payload"; } >"$G5/run.in"
      if ! "${SSH[@]}" >"$G5/run.out" 2>"$G5/run.err" <"$G5/run.in"; then
        bad "G5② 远端写回失败：$(tail -3 "$G5/run.err" | tr '\n' ' ')"
      else
        RUN_MS=$(( $(now_ms) - T0 ))
        APPLY_LINE="$(tail -1 "$G5/run.out")"
        G5_RUN_OK=1
        case "$APPLY_LINE" in
          *"replaced="*) ok "G5② 远端写回成功（${RUN_MS}ms，$APPLY_LINE）" ;;
          *) bad "G5② 远端写回退出码 0 但没吐统计行 ⇒ 「原地替换完成」这条判据在远端侧无从核对（日志：$APPLY_LINE）" ;;
        esac
        # 读写闭环判据（G4 只比了"读"，这条比"读出来的键能不能写回去"）：
        #   ★ 不能拿 replaced 去等**提取段数**——extract 的 texts 是**去重后**的键表
        #     （同一段跨页重复只翻一次，见 pdf_overlay.py 的 seen 集合），而 apply 的 replaced
        #     数的是**出现次数**，所以 replaced ≥ 段数是正常形态（本机 harness 实测：1 段 → replaced=2）。
        #     真正的不变量在回执自己那一行里：恒等映射下每条 requested 都必须被 replaced 命中。
        REPLACED="$(printf '%s' "$APPLY_LINE" | sed -n 's/.*replaced=\([0-9]*\).*/\1/p')"
        REQUESTED="$(printf '%s' "$APPLY_LINE" | sed -n 's/.*requested=\([0-9]*\).*/\1/p')"
        if [ -n "$REQUESTED" ] && [ "$REQUESTED" != "0" ] && [ "$REPLACED" != "$REQUESTED" ]; then
          bad "G5② 恒等映射下 replaced=$REPLACED ≠ requested=$REQUESTED ⇒ 提取键与写回键不同源，真译文会整段漏嵌"
        elif [ -z "$REPLACED" ] && [ -n "$NSEG" ] && [ "$NSEG" != "0" ]; then
          bad "G5② 写回执里读不到 replaced/requested 计数（$APPLY_LINE）⇒ 闭环判据无从执行"
        fi
      fi
    fi

    # ③ stat：产物在不在、多大
    if [ "$G5_RUN_OK" != "1" ]; then
      warn "  G5③④ 跳过（远端没产出，查尺寸/取回无从执行）"
    else
      T0="$(now_ms)"
      HS="$(printf '{"mode":"stat","rel":"%s"}' "$G5_OUT_REL" | base64 | tr -d '\n')"
      printf '%s\n' "$HS" >"$G5/stat.in"
      if ! "${SSH[@]}" >"$G5/stat.out" 2>"$G5/stat.err" <"$G5/stat.in"; then
        bad "G5③ 查远端产物尺寸失败（远端没做出来 / 路径不合规都会走到这里）：$(tail -3 "$G5/stat.err" | tr '\n' ' ')"
      else
        STAT_MS=$(( $(now_ms) - T0 ))
        WANT_SIZE="$(jf 'd.get("size",0)' <"$G5/stat.out")"
        WANT_SHA="$(jf 'd.get("sha256","")' <"$G5/stat.out")"
        if [ -z "$WANT_SHA" ] || [ "$WANT_SIZE" = "0" ]; then
          bad "G5③ stat 回执不合法（size=$WANT_SIZE sha=${WANT_SHA:0:12}…）"
        else
          ok "G5③ 远端产物 $WANT_SIZE B（sha=${WANT_SHA:0:12}…，查尺寸 ${STAT_MS}ms）"

          # ④ get：原样字节流回主站。★ stdout 只有文件字节，所以**不能**经过任何字符串变量，
          #    直接重定向落盘；收尾再 Sync 由文件系统保证（这里靠 shell 的重定向顺序）。
          T0="$(now_ms)"
          HG="$(printf '{"mode":"get","rel":"%s"}' "$G5_OUT_REL" | base64 | tr -d '\n')"
          printf '%s\n' "$HG" >"$G5/get.in"
          if ! "${SSH[@]}" >"$G5/artifact.pdf" 2>"$G5/get.err" <"$G5/get.in"; then
            bad "G5④ 取回失败：$(tail -3 "$G5/get.err" | tr '\n' ' ')"
          else
            GET_MS=$(( $(now_ms) - T0 ))
            GOT_SIZE="$(wc -c <"$G5/artifact.pdf" | tr -d '[:space:]')"
            GOT_SHA="$(DIGEST "$G5/artifact.pdf")"
            MAGIC="$(head -c 4 "$G5/artifact.pdf" | tr -d '[:space:]')"
            if [ "$GOT_SIZE" = "$WANT_SIZE" ] && [ "$GOT_SHA" = "$WANT_SHA" ] && [ "$MAGIC" = "%PDF" ]; then
              ok "G5④ 取回 $GOT_SIZE B 用 ${GET_MS}ms，sha256 与远端**逐字节等值**（PDF 魔数在位）"
            else
              bad "G5④ 取回件与远端不等（远端 size=$WANT_SIZE sha=${WANT_SHA:0:12}… / 主站 size=$GOT_SIZE sha=${GOT_SHA:0:12}… magic=$MAGIC）"
              bad "    ⇒ 客户会拿到坏文件；这一条不判就是把『传输被截断』当成上传成功（老实现 4MB 缓冲静默截断的同族）"
            fi
            # 定档读数（★ 阈值按**产物尺寸**算：09-29 实测产物可达输入 3.4 倍，按输入定档会低估回传）
            python3 - "$LOCAL_SIZE" "$PUT_MS" "$WANT_SIZE" "$GET_MS" "$RUN_MS" "$STAT_MS" <<'PY' || warn "  定档读数打印失败（不影响判据）"
import sys
in_b, put_ms, out_b, get_ms, run_ms, stat_ms = (float(x or 0) for x in sys.argv[1:7])
print("  ——— 定档读数（只读不判，供 dispatch_apply.sh 的阈值拍板）———")
print("    输入 %.2fMB / 产物 %.2fMB ⇒ 膨胀 %.1f×" % (in_b/1048576, out_b/1048576, (out_b/in_b) if in_b else 0))
print("    上传 %dms（%.2fMB/s）· 远端转换 %dms · 查尺寸 %dms · 取回 %dms（%.2fMB/s）"
      % (put_ms, (in_b/1048576)/(put_ms/1000) if put_ms else 0, run_ms, stat_ms,
         get_ms, (out_b/1048576)/(get_ms/1000) if get_ms else 0))
print("    \u26a0\ufe0f 单账号并发=1（远端 fpd.slice 只放一单）：派发吞吐按『串行 × 转换耗时』估，别按带宽估")
PY
            warn "  ℹ️ 本次在远端 w/preflight_g5/ 留了两份文件（in.pdf / translated.pdf），"
            warn "     fpdexec 没有删除指令，它们会由 fpd-sweep.timer 按 TTL 自动回收，不必手工清。"
          fi
        fi
      fi
    fi
  fi
fi

echo ""
if [ "$FAIL" -eq 0 ]; then
  echo "✅ preflight 全过（$PASS 项）：两侧环境一致 **且传输腿真机往返过一次**，可以走 scripts/dispatch_apply.sh 开闸"
  exit 0
fi
echo "❌ preflight 未过（通过 $PASS / 失败 $FAIL）⇒ **保持派发关闭**，先修再开"
exit 1
