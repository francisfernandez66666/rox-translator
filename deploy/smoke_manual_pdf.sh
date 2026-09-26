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
# 退出码：0=全绿；1=有红项（红项逐条点名，不静默）。
# ============================================================================
set -euo pipefail

BASE="${BASE:-https://langcross.lexicorn.cn}"
MIN_KB="${MIN_KB:-20}"                      # 体积下限（KB）：真手册十几页，兜底壳只有 2 KB 量级
LANGS="${LANGS:-zh zh-hant en ru fr ar es pt de ja ko th}"   # 12 语种（与后端 normalizeMailLang 白名单同口径）
RUN_NEG="${RUN_NEG:-1}"       # 负向探针开关：自检时打静态服务器（无 400 语义）可置 0
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fails=0
note() { printf '%s\n' "$*"; }
red()  { fails=$((fails + 1)); printf '  ✗ %s\n' "$*"; }
ok()   { printf '  ✓ %s\n' "$*"; }

note "手册 PDF 公网下载面冒烟：BASE=$BASE 体积下限=${MIN_KB}KB 语种数=$(echo $LANGS | wc -w | tr -d ' ')"
note "判据（AGENTS §一·6 托管物四件套）：200 + Content-Type=application/pdf + 体首 %PDF- + 非 HTML 兜底 + 体积达标"

for code in $LANGS; do
  url="$BASE/docs/manual/${code}.pdf"
  body="$TMP/${code}.bin"
  # -L 跟跳（Cloudflare/Caddy 偶发 308），--compressed 无关（PDF 已压缩）；-m 30 防挂
  http_code="$(curl -sSL -m 30 -o "$body" -D "$TMP/${code}.hdr" -w '%{http_code}' "$url" || echo 000)"
  bytes=$(wc -c < "$body" | tr -d ' ')
  ct="$(grep -i -E '^content-type:' "$TMP/${code}.hdr" | tail -1 | tr -d '\r' | sed 's/^[Cc]ontent-[Tt]ype: *//')"
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
  local path="$1" want="$2" url body
  url="$BASE$path"
  body="$TMP/neg.bin"
  http_code="$(curl -sS -m 30 -o "$body" -w '%{http_code}' "$url" || echo 000)"
  if [ "$http_code" = "$want" ] && ! grep -q -i -E '<!doctype html' "$body"; then
    ok "负向 $path → $http_code 且非 HTML 兜底"
  else
    red "负向 $path → HTTP $http_code（期望 $want）且不得是 HTML 兜底壳；体首=$(head -c 60 "$body" | tr -d '\n')"
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
  note "★ 手册 PDF 冒烟失败 $fails 项（判据口径见文件头：200 + 非 HTML + %PDF + Content-Type + 体积 + %%EOF）"
  note "  排查顺序：① 二进制是否已换（改 spa.go/新增路由属后端直出面）② system_config.manual_pdf_dir"
  note "  是否指向含 12 份 {lang}.pdf 的目录（缺件语种会按精确→en→zh 回落，回落属实但会被本脚本点名为异常）"
  exit 1
fi
note "手册 PDF 冒烟全绿：12 语种逐码 + 5 条负向路径"
