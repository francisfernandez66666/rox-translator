#!/usr/bin/env bash
# ============================================================================
# fetch_sherpa_assets.sh — 部署期落盘 sherpa-onnx 浏览器 ASR 官方资产（P4 端侧实时识别）
#
# 拉取源：官方 k2-fsa ModelScope Space（web-assembly-asr-sherpa-onnx-zh-en，Apache-2.0）
#   ① 运行时三件套（glue/main/wasm，约 14MB）——缺失时补拉（git 内已有，通常跳过）
#   ② 流式模型包 sherpa-onnx-wasm-main-asr.data（199MB，zipformer 中英双语）
#      —— 超 GitHub 单文件 100MB 硬限不进 git，本脚本是它的唯一正规来源
# 每件资产按 manifest.json 记录的官方 sha256 校验，任何不符即退出非零。
#
# 用法：
#   ./scripts/fetch_sherpa_assets.sh                 # 全量校验 + 补缺
#   ./scripts/fetch_sherpa_assets.sh --force         # 无视本地存在，强制重拉
#   ASR_ASSET_BASE=<镜像URL> ./scripts/fetch_sherpa_assets.sh   # 换镜像源
# ============================================================================
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="${ROOT}/frontend-react/public/wasm/sherpa-onnx"
MANIFEST="${DEST}/manifest.json"
FORCE="${1:-}"

# 官方 Space 文件下载端点（modelscope）；可用 ASR_ASSET_BASE 覆盖（须为 ...&FilePath= 形态）
DEFAULT_BASE="https://modelscope.cn/api/v1/studio/k2-fsa/web-assembly-asr-sherpa-onnx-zh-en/repo?Revision=master&FilePath="
ASR_ASSET_BASE="${ASR_ASSET_BASE:-${DEFAULT_BASE}}"

if [ ! -f "${MANIFEST}" ]; then
  echo "✗ 找不到 ${MANIFEST}" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# 1) 从 manifest.json 读资产清单 + sha256
# ---------------------------------------------------------------------------
eval "$(/usr/bin/python3 - "${MANIFEST}" <<'PY'
import sys, json
m = json.load(open(sys.argv[1]))
files = []
rt = m["runtime"]
files.append(rt["glue"]); files.append(rt["main"]); files.append(rt["wasm"]); files.append(rt["modelData"])
# shell 安全：文件名全部来自仓内 manifest，均为白名单字符
print("ASSET_FILES=" + " ".join(files))
print("WASM_NAME=" + rt["wasm"])
print("DATA_NAME=" + rt["modelData"])
PY
)"

echo "→ 目标目录：${DEST}"
echo "→ 资产清单：${ASSET_FILES}"
echo ""

# ---------------------------------------------------------------------------
# 2) 逐件下载（缺失或 --force 时）并 sha256 校验
# ---------------------------------------------------------------------------
fail=0
for f in ${ASSET_FILES}; do
  path="${DEST}/${f}"
  want="$(/usr/bin/python3 - "${MANIFEST}" "${f}" <<'PY'
import sys, json
m = json.load(open(sys.argv[1]))
print(m["runtime"]["sha256"].get(sys.argv[2], ""))
PY
)"

  if [ "${FORCE}" != "--force" ] && [ -s "${path}" ] && [ -n "${want}" ]; then
    got="$(shasum -a 256 "${path}" | cut -d' ' -f1)"
    if [ "${got}" = "${want}" ]; then
      echo "  ✓ ${f}（已就位，sha256 一致）"
      continue
    fi
    echo "  ↻ ${f}（在位但 sha256 不符，重拉）"
  fi

  echo "  ↓ 拉取 ${f} ..."
  if ! curl -fSL --retry 3 --max-time 3600 "${ASR_ASSET_BASE}${f}" -o "${path}"; then
    echo "  ✗ 下载失败：${ASR_ASSET_BASE}${f}" >&2
    fail=1
    continue
  fi

  got="$(shasum -a 256 "${path}" | cut -d' ' -f1)"
  if [ -n "${want}" ] && [ "${got}" != "${want}" ]; then
    echo "  ✗ ${f} sha256 不符（got ${got:0:16}… want ${want:0:16}…）" >&2
    fail=1
    continue
  fi
  echo "  ✓ ${f}（$(wc -c < "${path}") bytes，sha256 一致）"
done

if [ "${fail}" != "0" ]; then
  echo "" >&2
  echo "⚠️ 部分资产未就位：WASM 引擎 isAvailable() 将保持 false，前端自动回落服务端（零回归）。" >&2
  echo "   可重跑本脚本，或换镜像：ASR_ASSET_BASE=<同形态URL> $0 --force" >&2
  exit 4
fi

echo ""
echo "✅ 官方 wasm 运行时 + 流式模型全部就位且校验通过。"
echo "   前端 WasmAsrEngine.isAvailable() 将返回 true，端侧实时识别已激活（auto 档优先端侧）。"
