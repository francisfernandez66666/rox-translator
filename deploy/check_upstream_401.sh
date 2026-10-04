#!/usr/bin/env bash
# ============================================================================
# deploy/check_upstream_401.sh — 启动日志负向锁（★ R-1 的 A9，2026-10-04）
#
# 判据：自**本次启动**以来，translator.log 里「api key 无效 (401)」的计数必须为 0。
#
# 为什么这条只能活在发版验收里、单测与 UAT 矩阵都给不出等价读数：
#   R-1 的现网形态是「/api/health 回 status:ok、库里那条真 Key 却在启动水合时没被装进来」
#   （旧代码把水合套在 `for _, r := range cfg.ModelRoutes` 里，model_routes 为空 ⇒ 那条腿一次都没跑），
#   于是每条真翻译都拨到 config.Default() 生成的**随机占位 Key**上。这一形态下：
#     · Go 单测全绿（假上游对任何 Key 都回 200）；
#     · UAT 矩阵全绿（scripts/uat/mock_llm.py 同样不看凭据）；
#     · 健康检查全绿（它只探库与熔断器，不探"能不能译出一句"）。
#   只有「真进程 + 真上游 + 真时间窗」能看见它 ⇒ 本条锁的是"这次上线后有没有再出现 401"。
#
# 时间窗口径（关键，别顺手改成按日期过滤）：log.Printf 那一路的日志行**不带时间戳**
#   （只有 observability 的 JSON 行带），按时间比较会把窗口内的裸行全漏掉、把历史 401 全算进来。
#   唯一可靠的锚是服务自己打的那行「服务已启动」（cmd/server/main.go）——从**最后一条**往后数。
#   ⚠️ 锚点找不到一律**判红**而不是判 0：判 0 就是"日志被轮转掉了 ⇒ 这次上线没问题"的假绿。
#
# 可测性：日志路径留 env 覆盖口 TRANSLATOR_LOG（同 dispatch_revert.sh 的 ENV_FILE 口径），
#   Go 侧 backend-go/cmd/server/upstream_401_gate_test.go 用临时目录里的假日志**真跑四条分支**。
#
# 退出码：0＝判据成立；1＝出现 401／锚点缺失／日志不可读（三种都是"不许验收"）。
# 用法：bash deploy/check_upstream_401.sh [日志路径]   （位置参数优先于 env）
# ============================================================================
set -uo pipefail

LOG="${1:-${TRANSLATOR_LOG:-/opt/translator/log/translator.log}}"
# 锚点与 401 字样都是**代码里现存的字符串**，改这两处必须同日改产线代码：
#   锚点＝cmd/server/main.go 的「能言 v2.0.0-go 服务已启动」
#   401＝internal/llm/client.go 的 fmt.Errorf("api key 无效 (401)")
ANCHOR_TEXT="${UPSTREAM_401_ANCHOR:-服务已启动}"
BAD_TEXT="${UPSTREAM_401_BAD_TEXT:-api key 无效 (401)}"

say() { echo "$1"; }

if [ ! -f "$LOG" ]; then
  say "FAIL|upstream-401|日志文件不存在：$LOG（服务没起来或路径不对，本条判红不判过）"
  exit 1
fi
if [ ! -r "$LOG" ]; then
  say "FAIL|upstream-401|日志不可读：$LOG（判据无从谈起，绝不按 0 通过）"
  exit 1
fi

ANCHOR_LINE=$(grep -n -F "$ANCHOR_TEXT" "$LOG" | tail -1 | cut -d: -f1)
if [ -z "${ANCHOR_LINE:-}" ]; then
  say "FAIL|upstream-401|找不到「$ANCHOR_TEXT」锚点行 ⇒ 时间窗无法界定（日志被轮转/启动即失败/锚点文案被改），本条判红不判过"
  exit 1
fi

WINDOW=$(tail -n +"$ANCHOR_LINE" "$LOG")
# grep -c 在 0 命中时退 1：这里必须把退码吃掉再取数，否则 pipefail 下 N401 会拿到空串，
# 后面 [ "$N401" = 0 ] 变成 [ "" = 0 ] ⇒ **干净日志反而判红**（本仓反复踩过的形态）。
N401=$(printf '%s\n' "$WINDOW" | grep -c -F "$BAD_TEXT" || true)
LINES=$(printf '%s\n' "$WINDOW" | grep -c '' || true)
N401=${N401:-0}
LINES=${LINES:-0}

if [ "$N401" != "0" ]; then
  say "FAIL|upstream-401|本次启动（第 ${ANCHOR_LINE} 行起）后仍有 ${N401} 条「${BAD_TEXT}」⇒ 全局 Key 没水合进来：查 system_config.online_api_key 与 model_routes，并看 [init] 那条占位告警（修法见《修改文档》§一 R-1）"
  exit 1
fi

# 正面样本提示：窗口里一条上游调用都没有时，0 计数只证明"没出错"，不证明"跑通过"。
# 不把这一档判红（服务刚起、还没人用是完全正常态），但必须**说出来**，
# 否则读验收的人会把它当成"这条腿已验证"（AGENTS §一·6 链路型用例同一条纪律）。
if [ "$LINES" -lt 3 ]; then
  say "PASS|upstream-401|0 条（但窗口只有 ${LINES} 行＝启动后几乎没流量，本条尚无正面样本）"
else
  say "PASS|upstream-401|自第 ${ANCHOR_LINE} 行起 ${LINES} 行日志内 0 条「${BAD_TEXT}」"
fi
exit 0
