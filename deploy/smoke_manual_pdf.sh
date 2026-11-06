#!/usr/bin/env bash
# ============================================================================
# deploy/smoke_manual_pdf.sh — 12 语种《产品手册》PDF 公网下载面冒烟（★ F-69，2026-09-26 批 I-8）
#
# 为什么单独一个脚本（而不是塞进 api_uat.sh）：
#   F-69 的病根是「状态码 200 但内容不是那个东西」——线上 /docs/manual 实测回 2,591 B 的
#   SPA 兜底壳（index.html），只判 200 的探针会永远绿灯。AGENTS §一·6 已把这类判据写成硬口径：
#   **托管物必须 200 + 非 HTML 兜底 + 格式魔数 + 体积下限**，四件套缺一即视为无效断言。
#   本脚本对 12 个语种逐码跑这套判据，语种码参数化（不写死版本号/文件名）。
#
# 用法：
#   bash deploy/smoke_manual_pdf.sh                     # 默认打主站 langcross.lexicorn.cn
#   BASE=https://rox-test.lexicorn.cn bash deploy/smoke_manual_pdf.sh   # 打演示站
#   BASE=http://127.0.0.1:8787 bash deploy/smoke_manual_pdf.sh          # 打本地实例
#   MIN_KB=8 bash deploy/smoke_manual_pdf.sh            # 覆盖体积下限（默认 20 KB）
#
# 验收同时要求退出码 0 与 SMOKE_MANUAL_PDF_ALL_OK=1；1=红项，2=未测到。
# ============================================================================
set -euo pipefail

BASE="${BASE:-https://langcross.lexicorn.cn}"
# --selftest（或 SELFTEST=1）＝用假 curl 桩自证三态与重拨方向，见下面那一节的三条腿
SELFTEST_TARGET="${BASH_SOURCE[0]}"
SELFTEST="${SELFTEST:-0}"
if [ "${1:-}" = "--selftest" ]; then SELFTEST=1; fi
MIN_KB="${MIN_KB:-20}"                      # 体积下限（KB）：真手册十几页，兜底壳只有 2 KB 量级
LANGS="${LANGS:-zh zh-hant en ru fr ar es pt de ja ko th}"   # 12 语种（与后端 normalizeMailLang 白名单同口径）
RUN_NEG="${RUN_NEG:-1}"       # 负向探针开关：自检时打静态服务器（无 400 语义）可置 0
# ★ 单腿预算按**要搬多少字节**给（★ 2026-11-06 H 批发版实测改档，AGENTS §一·5 §6.2 同一条纪律）：
#   旧形态硬写 `-m 30`，那是 170–274 KB 那一代手册的预算；本批 PDF 带截图后**现读 642,472–825,893 B**，
#   而跨境冷缓存这一跳实测 ≈23 KB/s ⇒ 30 s 根本收不完，屏幕上给出的是**两种不同的假红**
#   （`HTTP 200000`＝curl 超时后把状态码与 000 拼在一起、以及负向腿的 `000000`），
#   而库里那一份是**逐字节等值**落盘的（铺库段已按 sha256 与仓库件核对过）——
#   把"这一跳没走完"读成"托管物坏了"，就会有人去动那条本来正确的判据。
MAX_TIME="${MAX_TIME:-180}"   # 单腿墙钟上限（秒）；跨境慢链要复跑而不是放宽内容判据
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fails=0
unmeasured=0
note() { printf '%s\n' "$*"; }
red()  { fails=$((fails + 1)); printf '  ✗ %s\n' "$*"; }
ok()   { printf '  ✓ %s\n' "$*"; }
# 三态里的第三态：**没测到**既不是通过也不是产品缺陷（发版判据要求本项为 0，拿到 2 就复跑一次再判）
unk()  { unmeasured=$((unmeasured + 1)); printf '  ⏳ %s\n' "$*"; }

# ============================================================================
# --selftest：用**假 curl 桩**证明三态与"只对没走完重拨"这两条真的成立（判绿只认末行
# SMOKE_MANUAL_PDF_SELFTEST_ALL_OK=1）。
#   为什么必须有这一节：把两态改成三态、给超时加重拨，都是**判据自己的改动**——
#   没有桩，"半截体现在算 ⏳ 而不是 ✗" 与 "HTML 壳仍然算 ✗" 这两句话都只是注释（AGENTS §三
#   「判据级断言代替不了守卫级」＋ §一·7「判据自身失效必须和判据命中分开报」）。
#   三例各治一病：
#     S1 半截体（content-length 5000、只给 1000、curl 退 28）⇒ 必须 ⏳ 且**整体退 2**，且桩被打了 ≥2 次（重拨真发生）
#     S2 收完整但 Content-Type=text/html（F-69 的原形态）    ⇒ 必须 ✗ 且**整体退 1**，且桩只被打 1 次（判据不合格不许重拨）
#     S3 404（路由没注册那一档）                            ⇒ 必须 ✗ 且**整体退 1**，且桩只被打 1 次
# ============================================================================
if [ "${SELFTEST:-0}" = "1" ]; then
  STUB="$(mktemp -d)"; CNT="$STUB/calls"
  cat > "$STUB/curl" <<STUB_SH
#!/bin/bash
# 假 curl：只认本脚本用到的那几个开关；把 body/hdr 写出来，并按 STUB_MODE 决定形状与退出码
echo "\$*" >> "$CNT"
o=""; d=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) shift; o="\${1:-}" ;;
    -D) shift; d="\${1:-}" ;;
  esac
  shift
done
[ -n "\$o" ] && : > "\$o"
[ -n "\$d" ] && : > "\$d"
case "\${STUB_MODE:-short}" in
  good)
    printf '%%PDF-1.7\\n' > "\$o"
    head -c 2048 /dev/zero | tr '\\0' 'A' >> "\$o"
    printf '\\n%%%%EOF\\n' >> "\$o"
    n=\$(wc -c < "\$o" | tr -d ' ')
    printf 'HTTP/1.1 200 OK\\r\\ncontent-type: application/pdf\\r\\ncontent-length: %s\\r\\n\\r\\n' "\$n" > "\$d"
    printf '200 %s' "\$n"; exit 0 ;;
  short)
    head -c 1000 /dev/zero | tr '\\0' 'A' > "\$o"
    printf 'HTTP/1.1 200 OK\\r\\ncontent-type: application/pdf\\r\\ncontent-length: 5000\\r\\n\\r\\n' > "\$d"
    printf '200 1000'; exit 28 ;;
  html)
    printf '<!DOCTYPE html><html><body>SPA fallback</body></html>' > "\$o"
    n=\$(wc -c < "\$o" | tr -d ' ')
    printf 'HTTP/1.1 200 OK\\r\\ncontent-type: text/html; charset=utf-8\\r\\ncontent-length: %s\\r\\n\\r\\n' "\$n" > "\$d"
    printf '200 %s' "\$n"; exit 0 ;;
  notfound)
    printf '{"success":false,"code":"NOT_FOUND"}' > "\$o"
    n=\$(wc -c < "\$o" | tr -d ' ')
    printf 'HTTP/1.1 404 Not Found\\r\\ncontent-type: application/json\\r\\ncontent-length: %s\\r\\n\\r\\n' "\$n" > "\$d"
    printf '404 %s' "\$n"; exit 0 ;;
esac
STUB_SH
  chmod +x "$STUB/curl"
  st_fail=0
  run_stub() { # $1=mode $2=期望退出码 $3=必须出现的串 $4=必须不出现的串 $5=期望桩次数
    local mode="$1" want_rc="$2" must="$3" mustnot="$4" want_calls="$5" out rc n
    : > "$CNT"
    set +e   # ★ 内层脚本**按设计**会退 1/2（那正是要判的三态）⇒ 不许让外层 set -e 在第一次红项就中止
    out="$(PATH="$STUB:$PATH" SELFTEST=0 STUB_MODE="$mode" BASE="http://stub" LANGS="zh" MIN_KB=1 RUN_NEG=0 \
            bash "$SELFTEST_TARGET" 2>&1)"; rc=$?
    set -e
    n=$(wc -l < "$CNT" | tr -d ' ')
    if [ "$rc" != "$want_rc" ]; then printf '  ✗ %s：退出码 %s（期望 %s）\n' "$mode" "$rc" "$want_rc"; st_fail=$((st_fail+1)); return; fi
    if [ -n "$must" ] && ! printf '%s' "$out" | grep -q "$must"; then
      printf '  ✗ %s：输出里找不到「%s」\n%s\n' "$mode" "$must" "$(printf '%s' "$out" | tail -6)"; st_fail=$((st_fail+1)); return; fi
    if [ -n "$mustnot" ] && printf '%s' "$out" | grep -q "$mustnot"; then
      printf '  ✗ %s：输出里出现了不该出现的「%s」＝把链路抖动读成缺陷（或反之）\n' "$mode" "$mustnot"; st_fail=$((st_fail+1)); return; fi
    case "$want_calls" in
      ge2) [ "$n" -ge 2 ] || { printf '  ✗ %s：桩只被打了 %s 次 ⇒ 重拨根本没发生\n' "$mode" "$n"; st_fail=$((st_fail+1)); return; } ;;
      eq1) [ "$n" -eq 1 ] || { printf '  ✗ %s：桩被打了 %s 次（期望 1）⇒ 判据本身不合格那一档也在重拨＝摇绿灯\n' "$mode" "$n"; st_fail=$((st_fail+1)); return; } ;;
    esac
    printf '  ✓ %s（rc=%s，桩 %s 次）\n' "$mode" "$rc" "$n"
  }
  # ★ 三条断言都问**那一腿自己的行**（前缀「  ⏳ zh」／「  ✗ zh」），不许问裸符号：
  #   红项那一段的排查提示里本来就写着"⏳"这个字（裸判据会被自己新写的文案喂饱＝假红，AGENTS §一·5 那一族真踩过两次）
  run_stub good     0 'SMOKE_MANUAL_PDF_ALL_OK=1' '  ✗ zh' eq1
  run_stub short    2 '  ⏳ zh' '  ✗ zh' ge2
  run_stub html     1 '  ✗ zh'  '  ⏳ zh' eq1
  run_stub notfound 1 '  ✗ zh'  '  ⏳ zh' eq1
  rm -rf "$STUB"
  if [ "$st_fail" != 0 ]; then echo "SELFTEST_FAIL=$st_fail"; exit 1; fi
  echo "SMOKE_MANUAL_PDF_SELFTEST_ALL_OK=1"
  exit 0
fi

# fetch_once $URL $BODY $HDR $MAXTIME → 打出 "<curl_rc> <http_code> <size>"（第二枪带 --http1.1）
# ⚠️ 这一腿**必须临时关掉 set -e**：curl 超时退 28 是本脚本要**读**的病因，不是要让脚本当场中止的理由
#   （本仓 `set -euo pipefail` 下，`x="$(curl …)"` 的赋值状态就是 curl 的退出码 ⇒ 不关就会在第一次抖动时整段退出，
#   屏幕上只剩"脚本跑了半截"，而那正是 §三 那条「判据自身失效必须和判据命中分开报」要避免的形态）。
fetch_once() {
  local url="$1" body="$2" hdr="$3" mt="$4" proto="" rc http
  [ "${5:-}" = "h1" ] && proto="--http1.1"
  set +e
  http="$(curl -sSL $proto --connect-timeout 20 -m "$mt" -o "$body" -D "$hdr" -w '%{http_code} %{size_download}' "$url" 2>/dev/null)"
  rc=$?
  set -e
  printf '%s %s' "$rc" "${http:-000 0}"
}

# cl_of $HDR → 取**最后一条** content-length（-L 跟重定向时标题块有多段，取第一条会把 302 的长度当终点）
cl_of() { grep -i -E '^content-length:' "$1" 2>/dev/null | tail -1 | tr -d '\r' | awk '{print $2}'; }

# complete_p $HDR $BYTES → 有 content-length 就逐字节比；没有（分块）就只问"curl 是否收完"
complete_p() {
  local cl bytes
  cl="$(cl_of "$1")"
  bytes="$2"
  [ -n "${cl:-}" ] || return 0
  [ "$cl" = "$bytes" ]
}

# get_handed $URL $BODY $HDR → 设全局 G_RC / G_CODE / G_BYTES；只对「这一跳没走完」重拨，
# 判据本身不合格的（4xx/5xx、内容不对）**一律不重拨**——摇绿灯就是把缺陷藏起来。
get_handed() {
  local url="$1" body="$2" hdr="$3" out rc code bytes
  rm -f "$body" "$hdr"; : > "$body"; : > "$hdr"
  out="$(fetch_once "$url" "$body" "$hdr" "$MAX_TIME")"; rc="${out%% *}"; code="$(printf '%s' "$out" | awk '{print $2}')"
  bytes=$(wc -c < "$body" | tr -d ' ')
  if { [ "$rc" != "0" ] || [ "$code" = "000" ] || ! complete_p "$hdr" "$bytes"; } && [ "$code" != "400" ] && [ "$code" != "404" ]; then
    note "    ↻ ${url##*/} 第一没收完（rc=$rc code=$code bytes=${bytes}）⇒ 换 --http1.1 重拨一次"
    out="$(fetch_once "$url" "$body" "$hdr" "$MAX_TIME" h1)"; rc="${out%% *}"; code="$(printf '%s' "$out" | awk '{print $2}')"
    bytes=$(wc -c < "$body" | tr -d ' ')
  fi
  G_RC="$rc"; G_CODE="$code"; G_BYTES="$bytes"; G_PROTO_LAST="${G_PROTO_LAST:-curl}"
}

note "手册 PDF 公网下载面冒烟：BASE=$BASE 体积下限=${MIN_KB}KB 单腿预算=${MAX_TIME}s 语种数=$(echo $LANGS | wc -w | tr -d ' ')"
note "判据（AGENTS §一·6 托管物四件套）：200 + Content-Type=application/pdf + 体首 %PDF- + 非 HTML 兜底 + 体积达标 ＋ ★ 收完整（content-length 逐字节）"

for code in $LANGS; do
  url="$BASE/docs/manual/${code}.pdf"
  body="$TMP/${code}.bin"
  hdr="$TMP/${code}.hdr"
  # -L 跟跳（Cloudflare/Caddy 偶发 308）；PDF 已压缩，--compressed 无关；预算见上面 MAX_TIME 那条
  get_handed "$url" "$body" "$hdr"
  http_code="$G_CODE"; bytes="$G_BYTES"
  # ★ 完整性腿排在所有**内容**判据之前：半截体同样可以是 200、魔数 %PDF- 也照样在（AGENTS §一·5 §6.2 ⑤）
  if [ "$G_RC" != "0" ] || [ "$http_code" = "000" ] || ! complete_p "$hdr" "$bytes"; then
    unk "${code}: 这一跳没走完（curl_rc=$G_RC code=$http_code 收到 ${bytes} B，标题里 content-length=$(cl_of "$hdr")）⇒ 复跑再判，既不读成通过也不读成缺陷"
    continue
  fi
  ct="$(grep -i -E '^content-type:' "$hdr" | tail -1 | tr -d '\r' | sed 's/^[Cc]ontent-[Tt]ype: *//')"
  magic="$(head -c 5 "$body" 2>/dev/null || true)"
  min_bytes=$((MIN_KB * 1024))

  if [ "$http_code" != "200" ]; then
    red "${code}: HTTP ${http_code}（期望 200）→ $url"
    continue
  fi
  # 判据①：体首魔数。%PDF- 不是 PDF 就是「200 但内容说谎」，与 F-69 同一形态。
  if [ "$magic" != "%PDF-" ]; then
    red "${code}: 体首=${magic:-<空>} 不是 %PDF- 魔数（前 5 字节），实际拿到的是别的字节流"
    continue
  fi
  # 判据②：非 HTML 兜底。SPA 兜底壳同样可以是 200，故必须按字节内容排除 HTML 特征。
  if head -c 2048 "$body" | grep -q -i -E '<!doctype html|<html'; then
    red "${code}: 响应体是 HTML 壳（SPA 兜底吃掉了托管物）——正是 F-69 的线上实测形态"
    continue
  fi
  # 判据③：Content-Type 必须是 application/pdf（前端据此直接下载，不能靠 nosniff 兜）。
  case "$ct" in
    application/pdf*) ;;
    *) red "${code}: Content-Type=${ct:-<缺失>}（期望 application/pdf）"; continue ;;
  esac
  # 判据④：体积下限（★ 方言无关的字节数比较；低于下限即疑似占位/兜底文件）
  if [ "$bytes" -lt "$min_bytes" ]; then
    red "${code}: 体积 ${bytes} B < 下限 ${min_bytes} B（疑似占位件）"
    continue
  fi
  # 判据⑤：尾部 %%EOF（PDF 结束符；截断/半截上传最常见就是它没了）
  if ! tail -c 1024 "$body" | grep -q -E '%%EOF'; then
    red "${code}: 尾部找不到 %%EOF（文件可能被截断）"
    continue
  fi
  ok "${code}: 200 + %PDF + application/pdf + ${bytes} B + %%EOF"
done

# —— 负向探针：这些路径**必须**是带错误码的 JSON，绝不能再回 200 整页 HTML ——
# 只判正向＝半个闸门：兜底壳之所以能骗过上一轮，就是因为没人问过「不存在的语种该长什么样」。
neg() { # $1=path $2=期望状态
  local path="$1" want="$2" url body hdr
  url="$BASE$path"
  body="$TMP/neg.bin"
  hdr="$TMP/neg.hdr"
  get_handed "$url" "$body" "$hdr"
  http_code="$G_CODE"
  if [ "$G_RC" != "0" ] || [ "$http_code" = "000" ]; then
    # 负向腿也一样：**链路抖动不许冒充"判据命中"**（那一行的处置是复跑，而不是去改期望状态码）
    unk "负向 $path → 这一跳没走完（curl_rc=${G_RC}），期望 $want 未测到"
    return
  fi
  if [ "$http_code" = "$want" ] && ! grep -q -i -E '<!doctype html' "$body"; then
    ok "负向 $path → $http_code 且非 HTML 兜底"
  else
    red "负向 $path → HTTP ${http_code}（期望 ${want}）且不得是 HTML 兜底壳；体首=$(head -c 60 "$body" | tr -d '\n')"
  fi
}
if [ "$RUN_NEG" != "1" ]; then
  note "负向探针已跳过（RUN_NEG=0：目标不是本后端时，400/404 语义不适用）"
else
neg "/docs/manual/" 404                 # 目录索引：如实 404
neg "/docs/manual/xx.pdf" 400           # 白名单外语种：400（不许静默回落中文）
neg "/docs/manual/zh-hans.pdf" 400      # 近亲码：400（12 码白名单外的写法一律拒）
neg "/docs/manual/.pdf" 400             # 空语种码：400
neg "/docs/manual/zh.pdf.pdf" 400       # 多后缀：400（防止把 "zh.pdf" 当码拼出目录外路径）
fi   # RUN_NEG 收尾
# ★ 路径穿越不在此列：Go ServeMux 会先把 URL 净化并重定向，公网侧看到的形状与处理方无关。
#   「处理方本身不吃穿越串」由 Go 单测直调 handler 证明（docs_manual_test.go 锁④第⑤条）。

if [ "$fails" -ne 0 ]; then
  note "★ 手册 PDF 冒烟失败 $fails 项（判据口径见文件头：200 + 非 HTML + %PDF + Content-Type + 体积 + %%EOF ＋ 收完整）"
  note "  排查顺序：① 二进制是否已换（改 spa.go/新增路由属后端直出面）② system_config.manual_pdf_dir"
  note "  是否指向含 12 份 {lang}.pdf 的目录（缺件语种会按精确→en→zh 回落，回落属实但会被本脚本点名为异常）"
  note "  ⚠️ 但**先分清红项与 ⏳**：红项＝内容不对（该有人去修），⏳＝这一跳没走完（复跑，别改判据）"
  exit 1
fi
if [ "$unmeasured" -ne 0 ]; then
  note "★ 手册 PDF 冒烟：**有 $unmeasured 项没测到**（跨境抖动／半截体），零红项但不算全绿 ⇒ 复跑一次再判"
  note "SMOKE_MANUAL_PDF_UNMEASURED=$unmeasured"
  exit 2
fi
note "手册 PDF 冒烟全绿：本轮语种逐码完成（LANGS=${LANGS}，RUN_NEG=${RUN_NEG}）"
note "SMOKE_MANUAL_PDF_ALL_OK=1"
