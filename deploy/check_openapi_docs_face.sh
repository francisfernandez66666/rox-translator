#!/usr/bin/env bash
# deploy/check_openapi_docs_face.sh —— 对外 API 文档面「生效来源」只读探针（★ 2026-10-04 〇-AR 第 2 波）
#
# 为什么要有这个脚本（现象 → 根因 → 为什么不能直接修）：
#   · 现象：`/openapi/docs` 页面上的「错误与 HTTP 状态码」整段**在现网看不见**，
#     客户读到的仍是 09-26 之前的旧口径「错误码（独立出参 error_code）」。
#   · 根因：`internal/api/admin_openapi.go` 的 `getDocsMD()` 优先级是
#     **system_config 里的 `openapi_docs_md_zh` / `openapi_docs_md_en` 现值 ＞ 代码内置默认**
#     （运营可在管理台「开放 API 文档」在线编辑，在线改的内容优先级高于 seed/默认，与挂件 seed 同一族坑）。
#     现网库里存的是一份**旧抄本** ⇒ 后端二进制再怎么换，文档面都纹丝不动。
#   · 为什么不在这里直接修：清掉/覆盖那两行 config 是**现网写操作**，需运营拍板
#     （要么管理台保存一次内置默认，要么删键回退默认）；本脚本只负责把「文档面生效的是哪一份」
#     变成一条可复读的只读读数，别把「换了二进制＝客户看到的文档也换了」当成默认成立。
#
# ★ 2026-10-28 补腿「这一跳收完整了没」（本批 openapi v3 换件后现网实证，两条都是判据自伤）：
#   旧形态把 `curl` 的返回体整个塞进 shell 变量就开判，**不验完整性**，于是：
#     ① 半截体被读成「库里旧抄本」——同一分钟内两次跑出两种红法（bytes=0／bytes=21,135），
#        而手工抓的完整件是 37,970 B 且中英标题齐备；更坏的是**失败文案会把人引去管理台改文档**
#        （改了没用：内置默认已在件里生效），属于「判据指向错误处置方向」。
#     ② 一次传输抖动＝一次红 ⇒ 闸门钝化（真回归反而没人看）。
#   修法与本批 `deploy/smoke_tenant_domain.sh` 同一把尺子：
#     · **完整性**：本页是后端直出的 chunked 响应（**没有 content-length**），
#       所以按字节数比对不可用，改用**文档页自己的收尾标记**（尾部 `</html>`）判「收完整了没」；
#       有 content-length 的响应仍走逐字节比对（两条腿并存，取先到的那个结论）。
#     · **只对传输失败重拨**：判据本身不合格的（内容不对）一律不重拨——摇绿灯就是把缺陷藏起来；
#       第二枪起锁 `--http1.1`（跨境这一跳的失败集中在 HTTP/2，实测 h2 连败／换 1.1 立刻 200）。
#     · **三态**：FAIL（内容真不对）／UNMEASURED（这一跳没走完，没测到）／PASS。
#       退出码 0＝全绿，**2＝有 N 项没测到**（链路抖动，复跑再判，不许当通过也不许当产品缺陷），1＝有真失败。
#
# 判据（全只读）：
#   D0 完整性：必须收完整（见上）；收不全 ⇒ 记 UNMEASURED，**不判内容**
#   D1 中英两段都必须出现「状态码」章节标题（现行内置默认里的两段标题，缺一即说明库里是旧抄本）
#   D2 状态码表里必须有 `insufficient_balance`/402 与 `task_failed`/409 两行（第 2 波对外契约的落点）
#   D3 负向：不许再出现旧标题「错误码（独立出参 error_code）」（库里旧抄本的特征串）
#   D4 反向对照：同一份判据打一个**必然不存在**的标记串必须回 0 命中，
#      证明 grep 真在跑、页面真取到了（防止 curl 失败时把空页判成"没命中旧标题＝通过"）
#   D5 正向（★ 2026-10-28 补）：计费口径 v3 段（中英各一）必须在页面上
#      ——这条直接守住「v3 内置文档确实在件里生效」；旧抄本缺这一段 ⇒ 与 D1 同时红。
#
# 用法：bash deploy/check_openapi_docs_face.sh [base_url ...]
#   缺省打主站与演示站两个 base；传参则只传参的那些。
#   bash deploy/check_openapi_docs_face.sh --selftest
#     用假 curl 喂四种形态自证本脚本的判据有射程（完整件／半截体／传输失败／旧抄本），
#     判绿只认末行 `OPENAPI_DOCS_FACE_SELFTEST_ALL_OK=1`。
set -u

MAX_ATTEMPTS=3
CURL_MAX_TIME=45
MIN_BYTES=2000

# ---------------------------------------------------------------- 完整性判据
# page_complete —— 「这一跳收完整了没」的唯一实现（探针与自证共用这一处）。
#   $1=响应体文件；回 0＝完整，回 1＝不完整。
#   为什么不用 content-length：该页由 Go 直出且 >2 KB，走 chunked（实测 `Transfer-Encoding: chunked`），
#   响应里**没有** content-length ⇒ 逐字节比对没有比对对象；改用文档页自己的收尾标记。
page_complete() {
  local f="$1" size=""
  [ -f "$f" ] || return 1
  size=$(wc -c < "$f" | tr -d ' ')
  [ "${size:-0}" -ge "$MIN_BYTES" ] || return 1
  tail -c 64 "$f" | grep -q '</html>' || return 1
  return 0
}

# fetch_page —— 抓一次并**验完整性**；只对「这一跳没走完」重拨。
#   $1=url，$2=落地文件；回 0＝拿到完整件（字节数写入 FETCH_SIZE，尝试次数写入 FETCH_ATTEMPTS），
#   回 1＝没拿到（原因写入 FETCH_REASON）。
#   ⚠️ 判据本身不合格（内容不对）**不重拨**：重拨只会把真缺陷摇成绿灯。
FETCH_SIZE=0
FETCH_ATTEMPTS=0
FETCH_REASON=""
fetch_page() {
  local url="$1" out="$2" attempt=0 rc=0 code="" size=0
  FETCH_REASON=""
  while [ "$attempt" -lt "$MAX_ATTEMPTS" ]; do
    attempt=$((attempt + 1))
    : > "$out"
    # 第二枪起锁 --http1.1：跨境这一跳的失败集中在 HTTP/2（实测 h2 连败／换 1.1 立刻 200）
    if [ "$attempt" -ge 2 ]; then
      code=$(curl -sS --http1.1 --max-time "$CURL_MAX_TIME" -L -o "$out" -w '%{http_code}' "$url" 2>/dev/null)
      rc=$?
    else
      code=$(curl -sS --max-time "$CURL_MAX_TIME" -L -o "$out" -w '%{http_code}' "$url" 2>/dev/null)
      rc=$?
    fi
    code=${code:-000}
    size=$(wc -c < "$out" | tr -d ' ')
    if [ "$rc" != "0" ] || [ "$code" = "000" ]; then
      FETCH_REASON="这一跳没走完（curl_rc=${rc} code=${code}）"
      continue
    fi
    if ! page_complete "$out"; then
      FETCH_REASON="半截体（bytes=${size}，收尾标记 </html> 缺失）"
      continue
    fi
    FETCH_SIZE="$size"
    FETCH_ATTEMPTS="$attempt"
    return 0
  done
  return 1
}

# ---------------------------------------------------------------- 自证夹具
# selftest —— 用假 curl 喂四种形态，证明「完整性腿」「三态」「重拨策略」都有射程。
#   判绿只认末行 OPENAPI_DOCS_FACE_SELFTEST_ALL_OK=1。
selftest() {
  local STUB ROOT OK=1
  STUB=$(mktemp -d)
  ROOT=$(mktemp -d)
  # 假 curl：从参数里取 -o 的落地路径与 url，按 $FAKE_CURL_MODE 吐不同形态
  cat > "$STUB/curl" <<'STUBEOF'
#!/usr/bin/env bash
out=""; url=""; prev=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then out="$a"; fi
  case "$a" in http*) url="$a";; esac
  prev="$a"
done
mode="${FAKE_CURL_MODE:-complete}"
case "$mode" in
  transport_fail)
    printf '000' ; exit 7 ;;
  truncated_always|truncated_then_complete)
    n=0
    if [ -f "$FAKE_CURL_COUNTER" ]; then n=$(cat "$FAKE_CURL_COUNTER" 2>/dev/null || echo 0); fi
    n=$((n + 1)); echo "$n" > "$FAKE_CURL_COUNTER"
    if [ "$mode" = "truncated_always" ] || [ "$n" = "1" ]; then
      # 半截体：够大（过体积下限）但没有收尾标记
      { i=0; while [ "$i" -lt 30 ]; do printf '%0.sX' $(seq 1 100); echo; i=$((i + 1)); done; } > "$out"
      printf '200' ; exit 0
    fi
    ;;
esac
# 完整件：含收尾标记；按 $FAKE_CURL_OLD=1 时改吐「旧抄本」形态
if [ "${FAKE_CURL_OLD:-0}" = "1" ]; then
  { printf '<html><body><div>错误码（独立出参 error_code）</div>'; i=0; while [ "$i" -lt 30 ]; do printf '%0.sY' $(seq 1 100); echo; i=$((i + 1)); done; printf '</body></html>'; } > "$out"
else
  { printf '<html><body><h1>错误与 HTTP 状态码</h1><h2>Errors &amp; HTTP status codes</h2><table>insufficient_balance 402 task_failed 409</table><h3>计费口径（★ 2026-10-28 起为 v3</h3><h3>Billing (v3 since 2026-10-28</h3>'; i=0; while [ "$i" -lt 30 ]; do printf '%0.sZ' $(seq 1 100); echo; i=$((i + 1)); done; printf '</body></html>'; } > "$out"
fi
printf '200'
exit 0
STUBEOF
  chmod +x "$STUB/curl"

  run_case() {
    # $1=模式 $2=旧抄本? $3=期望退出码 $4=说明
    local mode="$1" old="$2" want="$3" note="$4" rc=0 out=""
    export FAKE_CURL_MODE="$mode" FAKE_CURL_OLD="$old" FAKE_CURL_COUNTER="$ROOT/counter"
    rm -f "$ROOT/counter"
    out=$(PATH="$STUB:$PATH" bash "$0" "https://selftest.invalid" 2>&1)
    rc=$?
    if [ "$rc" != "$want" ]; then
      echo "  FAIL ${note}：期望退出码 ${want}，实得 ${rc}"
      printf '%s\n' "$out" | sed 's/^/        /'
      OK=0
    else
      echo "  ok  ${note}（exit=${rc}）"
    fi
  }

  echo "==> 自证夹具：四种形态"
  # ① 完整且内容正确 ⇒ PASS(0)
  run_case complete 0 0 "完整且内容正确 ⇒ PASS"
  # ② 永远半截体 ⇒ 收不完整 ⇒ UNMEASURED(2)，**不许**判成 FAIL(1)（那会把链路抖动读成产品缺陷）
  run_case truncated_always 0 2 "永远半截体 ⇒ UNMEASURED(2) 而非 FAIL(1)"
  # ③ 传输失败（curl 非零退出）⇒ UNMEASURED(2)
  run_case transport_fail 0 2 "传输失败 ⇒ UNMEASURED(2)"
  # ④ 旧抄本（完整但内容是旧口径）⇒ FAIL(1)，且文案必须指向「库里有抄本」而不是「没测到」
  run_case complete 1 1 "旧抄本 ⇒ FAIL(1)"

  # ⑤ 重拨腿的射程：第一次半截、第二次完整 ⇒ 必须重拨到 PASS，且尝试次数 ≥2
  export FAKE_CURL_MODE="truncated_then_complete" FAKE_CURL_OLD=0 FAKE_CURL_COUNTER="$ROOT/counter2"
  rm -f "$ROOT/counter2"
  out=$(PATH="$STUB:$PATH" bash "$0" "https://selftest.invalid" 2>&1)
  rc=$?
  if [ "$rc" = "0" ] && printf '%s' "$out" | grep -q '尝试 2 次'; then
    echo "  ok  半截体后重拨到完整件 ⇒ PASS 且尝试 2 次（重拨腿有射程）"
  else
    echo "  FAIL 重拨腿没生效（exit=${rc}）"
    printf '%s\n' "$out" | sed 's/^/        /'
    OK=0
  fi

  rm -rf "$STUB" "$ROOT"
  if [ "$OK" != "1" ]; then
    echo "❌ 自证未全绿"
    return 1
  fi
  echo "OPENAPI_DOCS_FACE_SELFTEST_ALL_OK=1"
  return 0
}

if [ "${1:-}" = "--selftest" ]; then
  selftest
  exit $?
fi

BASES=("$@")
if [ "${#BASES[@]}" = "0" ]; then
  BASES=("https://langcross.lexicorn.cn" "https://rox-test.lexicorn.cn")
fi

FAIL=0
UNMEASURED=0
TMPBODY=$(mktemp)
trap 'rm -f "$TMPBODY"' EXIT
for BASE in "${BASES[@]}"; do
  echo "==> 对外文档面探针：$BASE/openapi/docs"
  if ! fetch_page "$BASE/openapi/docs" "$TMPBODY"; then
    echo "  ⏳ UNMEASURED ${FETCH_REASON}"
    echo "     ⇒ 这一跳没测到：**不判通过、也不判产品缺陷**（复跑一次再判；退出码 2）"
    UNMEASURED=$((UNMEASURED + 1))
    continue
  fi
  echo "  读数：bytes=$FETCH_SIZE（尝试 ${FETCH_ATTEMPTS} 次，收完整了）"

  # D4 反向对照：先证明"命中/未命中"这套读法在本页是有分辨力的
  CONTRA=$(grep -c "该标记本仓任何版本都不存在_1004探针对照" "$TMPBODY" || true)
  if [ "${CONTRA:-0}" != "0" ]; then
    echo "  FAIL 反向对照命中不存在的标记串（count=${CONTRA}）⇒ 读法本身失效，判据不可信"
    FAIL=$((FAIL + 1))
    continue
  fi

  # D1 状态码章节标题（中／英任缺一份就是库里那份抄本只刷了一边）
  ZH=$(grep -cE '错误与 HTTP 状态码' "$TMPBODY" || true)
  EN=$(grep -cE 'Errors &amp; HTTP status codes|Errors & HTTP status codes' "$TMPBODY" || true)
  if [ "${ZH:-0}" = "0" ] || [ "${EN:-0}" = "0" ]; then
    echo "  FAIL 状态码章节标题缺失（中=$ZH 英=${EN}）⇒ 文档面生效的是**库里旧抄本**，不是代码内置默认"
    echo "       处置：管理台「开放 API 文档」把中英两段各保存一次（或删 system_config 的 openapi_docs_md_zh / _en 回退默认）"
    echo "       ⚠️ 只换 translator-server 二进制不解决这一条：getDocsMD 先读 config 现值"
    FAIL=$((FAIL + 1))
  else
    echo "  ok  状态码章节标题中英齐备（中=$ZH 英=${EN}）"
  fi

  # D2 对外契约两行：402 余额不足、409 处理失败（第 2 波把同步 /translate 归到这两档）
  C402=$(grep -cE 'insufficient_balance' "$TMPBODY" || true)
  C409=$(grep -cE 'task_failed' "$TMPBODY" || true)
  if [ "${C402:-0}" = "0" ] || [ "${C409:-0}" = "0" ]; then
    echo "  FAIL 状态码表里缺 insufficient_balance（=${C402}）或 task_failed（=${C409}）那一行"
    FAIL=$((FAIL + 1))
  else
    echo "  ok  状态码表含 insufficient_balance（$C402 处）与 task_failed（$C409 处）"
  fi

  # D3 旧口径标题负向锁：库里旧抄本的特征串，出现即说明文档还在教客户读 error_code 单键
  OLD=$(grep -cE '错误码（独立出参 error_code）|Error codes \(dedicated error_code field\)' "$TMPBODY" || true)
  if [ "${OLD:-0}" != "0" ]; then
    echo "  FAIL 页面仍带旧口径标题「错误码（独立出参 error_code）」（count=${OLD}）⇒ 客户会按 200＋error_code 写客户端"
    FAIL=$((FAIL + 1))
  else
    echo "  ok  旧口径标题已不在页面上"
  fi

  # D5 正向：计费口径 v3 段（中英各一）必须在页面上（★ 2026-10-28 补：守住 v3 内置文档确实在件里生效）
  V3ZH=$(grep -cF '计费口径（★ 2026-10-28 起为 v3' "$TMPBODY" || true)
  V3EN=$(grep -cF 'Billing (v3 since 2026-10-28' "$TMPBODY" || true)
  if [ "${V3ZH:-0}" = "0" ] || [ "${V3EN:-0}" = "0" ]; then
    echo "  FAIL 计费口径 v3 段缺失（中=$V3ZH 英=${V3EN}）⇒ 客户看到的仍不是 v3 口径（多半也是库里旧抄本）"
    FAIL=$((FAIL + 1))
  else
    echo "  ok  计费口径 v3 段中英齐备（中=$V3ZH 英=${V3EN}）"
  fi
done

if [ "$FAIL" != "0" ]; then
  echo "❌ 对外文档面判据未成立：失败 $FAIL 项（该页由后端直出，且优先级＝config 现值 ＞ 内置默认）"
  if [ "$UNMEASURED" != "0" ]; then
    echo "   另有 $UNMEASURED 项没测到（链路没走完）⇒ 实测覆盖不完整，复跑再判"
  fi
  exit 1
fi
if [ "$UNMEASURED" != "0" ]; then
  echo "⏳ 对外文档面有 $UNMEASURED 项没测到（这一跳没走完）⇒ 退出码 2"
  echo "   发版判据要求 UNMEASURED=0；拿到 2 就复跑一次再判（既不判绿、也不判成产品缺陷）"
  exit 2
fi
echo "✅ 对外文档面判据全部成立（状态码章节中英齐备／402·409 在表／旧口径标题已清／v3 段在位）"
exit 0
