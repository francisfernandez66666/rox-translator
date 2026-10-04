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
# 判据（全只读，任何一条不成立即 exit 1）：
#   D1 中英两段都必须出现「状态码」章节标题（现行内置默认里的两段标题，缺一即说明库里是旧抄本）
#   D2 状态码表里必须有 `insufficient_balance`/402 与 `task_failed`/409 两行（第 2 波对外契约的落点）
#   D3 负向：不许再出现旧标题「错误码（独立出参 error_code）」（库里旧抄本的特征串）
#   D4 反向对照：同一份判据打一个**必然不存在**的标记串必须回 0 命中，
#      证明 grep 真在跑、页面真取到了（防止 curl 失败时把空页判成"没命中旧标题＝通过"）
#
# 用法：bash deploy/check_openapi_docs_face.sh [base_url ...]
#   缺省打主站与演示站两个 base；传参则只传参的那些。
set -u

BASES=("$@")
if [ "${#BASES[@]}" = "0" ]; then
  BASES=("https://langcross.lexicorn.cn" "https://rox-test.lexicorn.cn")
fi

FAIL=0
for BASE in "${BASES[@]}"; do
  echo "==> 对外文档面探针：$BASE/openapi/docs"
  BODY=$(curl -sS --max-time 25 -L "${BASE}/openapi/docs" 2>/dev/null || true)
  N=$(printf '%s' "$BODY" | wc -c | tr -d ' ')
  if [ "${N:-0}" -lt 2000 ]; then
    echo "  FAIL 页面没取到或过小（bytes=$N）⇒ 本条判据没跑，不许当成通过"
    FAIL=$((FAIL + 1))
    continue
  fi
  echo "  读数：bytes=$N"

  # D4 反向对照：先证明"命中/未命中"这套读法在本页是有分辨力的
  CONTRA=$(printf '%s' "$BODY" | grep -c "该标记本仓任何版本都不存在_1004探针对照" || true)
  if [ "${CONTRA:-0}" != "0" ]; then
    echo "  FAIL 反向对照命中不存在的标记串（count=$CONTRA）⇒ 读法本身失效，判据不可信"
    FAIL=$((FAIL + 1))
    continue
  fi

  # D1 状态码章节标题（中／英任缺一份就是库里那份抄本只刷了一边）
  ZH=$(printf '%s' "$BODY" | grep -cE '错误与 HTTP 状态码' || true)
  EN=$(printf '%s' "$BODY" | grep -cE 'Errors &amp; HTTP status codes|Errors & HTTP status codes' || true)
  if [ "${ZH:-0}" = "0" ] || [ "${EN:-0}" = "0" ]; then
    echo "  FAIL 状态码章节标题缺失（中=$ZH 英=$EN）⇒ 文档面生效的是**库里旧抄本**，不是代码内置默认"
    echo "       处置：管理台「开放 API 文档」把中英两段各保存一次（或删 system_config 的 openapi_docs_md_zh / _en 回退默认）"
    echo "       ⚠️ 只换 translator-server 二进制不解决这一条：getDocsMD 先读 config 现值"
    FAIL=$((FAIL + 1))
  else
    echo "  ok  状态码章节标题中英齐备（中=$ZH 英=$EN）"
  fi

  # D2 对外契约两行：402 余额不足、409 处理失败（第 2 波把同步 /translate 归到这两档）
  C402=$(printf '%s' "$BODY" | grep -cE 'insufficient_balance' || true)
  C409=$(printf '%s' "$BODY" | grep -cE 'task_failed' || true)
  if [ "${C402:-0}" = "0" ] || [ "${C409:-0}" = "0" ]; then
    echo "  FAIL 状态码表里缺 insufficient_balance（=$C402）或 task_failed（=$C409）那一行"
    FAIL=$((FAIL + 1))
  else
    echo "  ok  状态码表含 insufficient_balance（$C402 处）与 task_failed（$C409 处）"
  fi

  # D3 旧口径标题负向锁：库里旧抄本的特征串，出现即说明文档还在教客户读 error_code 单键
  OLD=$(printf '%s' "$BODY" | grep -cE '错误码（独立出参 error_code）|Error codes \(dedicated error_code field\)' || true)
  if [ "${OLD:-0}" != "0" ]; then
    echo "  FAIL 页面仍带旧口径标题「错误码（独立出参 error_code）」（count=$OLD）⇒ 客户会按 200＋error_code 写客户端"
    FAIL=$((FAIL + 1))
  else
    echo "  ok  旧口径标题已不在页面上"
  fi
done

if [ "$FAIL" != "0" ]; then
  echo "❌ 对外文档面判据未成立：失败 $FAIL 项（该页由后端直出，且优先级＝config 现值 ＞ 内置默认）"
  exit 1
fi
echo "✅ 对外文档面三段判据全部成立（状态码章节中英齐备／402·409 在表／旧口径标题已清）"
exit 0
