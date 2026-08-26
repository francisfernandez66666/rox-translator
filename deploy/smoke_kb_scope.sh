#!/usr/bin/env bash
# ============================================================================
# smoke_kb_scope.sh — KB 组织继承链与部门隔离 · 部署后冒烟脚本
# （2026-08-26《KB组织继承链与部门隔离改造方案》§六 验收第 3 条）
#
# 三段式：
#   [1/3] 逻辑层回归：跑 store 包九个 scope 场景用例（就近覆盖/兄弟隔离/opt-out/
#         空链守卫/历史行+行业码/移动重继承/模糊分层/#9/#10 回归）
#   [2/3] 服务健康：本地起 Go 服务（临时库）探活 /api/health 与 /status
#   [3/3] 人工清单：输出需在真实 UI 上点验的 4 步（涉及登录态与前端渲染，
#         自动化不覆盖——按清单逐项确认即可）
#
# 用法：./deploy/smoke_kb_scope.sh
# 退出码：0=全部通过；非 0=定位失败段落
# ============================================================================
set -uo pipefail
cd "$(dirname "$0")/.."

echo "==> [1/3] 逻辑层回归（九场景）"
if (cd backend-go && go test ./internal/store/ -run 'TestScope|TestFuzzyScoped|TestVectorScoped' -count=1); then
  echo "    ✅ 逻辑层通过"
else
  echo "    ❌ 逻辑层失败"; exit 1
fi

echo "==> [2/3] 服务启动与健康探活"
BIN=/tmp/translator-smoke-$$
(cd backend-go && go build -o "$BIN" ./cmd/server) || { echo "    ❌ 编译失败"; exit 1; }
PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
TMPDIR_SMOKE=$(mktemp -d)
"$BIN" -addr "127.0.0.1:$PORT" -kbdb "$TMPDIR_SMOKE/smoke.db" &
SRV=$!
trap 'kill $SRV 2>/dev/null; rm -rf "$TMPDIR_SMOKE"' EXIT

HEALTH_OK=0
for _ in $(seq 1 20); do
  if curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1; then HEALTH_OK=1; break; fi
  sleep 0.5
done
STATUS_OK=0
curl -sf "http://127.0.0.1:$PORT/status" | grep -q '"ok":true' && STATUS_OK=1
if [ "$HEALTH_OK" = "1" ] && [ "$STATUS_OK" = "1" ]; then
  echo "    ✅ /api/health 与 /status 探活通过"
else
  echo "    ❌ 探活失败 health=$HEALTH_OK status=$STATUS_OK"; exit 1
fi

echo "==> [3/3] UI 人工点验清单（部署窗口逐项确认）"
cat <<'CHECKLIST'
  □ 管理后台建三级组织 A>B>C，C 下建部门包并加一条术语：
      用 C 用户翻译该句 → 命中气泡应显示 C 包译法（精确命中）
  □ 删除 C 包内该术语 → 同句翻译回落 B/企业包译法（就近降级）
  □ 兄弟部门 D 建包加术语：C 用户翻译 → 默认显示「精确命中 | 🌐跨部门（来自D包）」
  □ Models 面板策略卡把「跨部门降级检索」切为关闭 → C 用户同句不再跨部门命中
CHECKLIST

echo ""
echo "✅ 冒烟完成：逻辑层与服务健康自动化通过；UI 清单请人工确认"
