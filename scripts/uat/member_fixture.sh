#!/usr/bin/env bash
# ============================================================================
# scripts/uat/member_fixture.sh — 企业成员入职「邀请链接」工装（★ 2026-10-24 多身份方案 B3-c / R9）
#
# 为什么要有这一份：自 R9 起管理台建号**不再录入初始口令**——管理员提交后拿到的是一枚一次性
# 专属邀请链接（users 行一个都不建），由本人在注册口自设邮箱与口令，链接用一次即失效。
# 于是 UAT 里过去那句
#     post /api/admin/users/create（带 password）→ tok <用户名> <初始口令>
# 在新模型下**结构性不可能**：带 password 直接 400「企业成员建号不再录入初始口令」，
# 而且这一步根本不会产生可登录的账号。两份套件（api_uat.sh / api_uat_txn.sh）都要走这条链，
# 各写一份迟早漂成两套口径（AGENTS §一·5「同一件事写两遍迟早打架」），故收在这一个文件。
#
# 依赖：调用方在 source 本文件**之前**已定义 $B（服务地址）与 $J（Content-Type: application/json）。
#
# 三条工装纪律（改这一族前必读）：
#   ① **工装自己不计数**：PASS/FAIL 一律由调用方的 ck/ck3 承担。一条链坏掉时，
#      「发链接失败」「注册失败」「登录失败」是三个不同的病因，让它们各自显形比合并成一条红有用。
#   ② **原始响应体必须留在调用方手里**：mf_onboard 是**语句形态**调用（不是 $(...)），
#      所以 MF_CREATE/MF_REG 这类全局能带回父 shell——若把工装写成命令替换，
#      赋值发生在子 shell 里，调用方拿到的是空串（那是假绿的温床）。
#   ③ **登录按邮箱**：多身份模型下用户名同租户可重名（D3 邮箱才是标识），
#      按用户名登录在重名场景下会被「多租户重名拒绝」那一档挡掉；邮箱是唯一的稳定标识。
#
# 用法：source "$(dirname "$0")/member_fixture.sh"
# ============================================================================

# mf_field <点分路径> — 从 stdin 的 JSON 里取值（解析失败/取不到 ⇒ **空串**，绝不回字面量 None）。
# 空串是刻意的：调用方按「非空 = 拿到了」判，写成 None 会让「没拿到」看起来像拿到了。
mf_field(){ python3 -c 'import sys,json
try:
    d = json.load(sys.stdin)
except Exception:
    d = {}
v = d
for k in sys.argv[1].split("."):
    v = v.get(k) if isinstance(v, dict) else None
print("" if v is None else v)' "$1" 2>/dev/null; }

# mf_onboard <管理员头> <租户ID> <用户名> <邮箱> <口令> [角色] [部门ID]
# 走完整入职两段：① 管理员发一枚专属邀请链接 ② 本人凭该链接注册（自设邮箱＋口令）。
# 置四个全局（不退码；失败时对应值为空串）：
#   MF_CREATE / MF_LINK / MF_REG / MF_TOKEN
#   MF_LINK＝链接码（inv_<32hex>），注册时放进 invite 字段的就是它。
# 邮箱一律**必传**：链接的 bound_email 会锁死注册邮箱（§6.2「预绑邮箱是最强绑定」），
# 不传就只能靠人复制链接，UAT 里没法无人值守地走完注册。
mf_onboard(){
  local h="$1" tid="$2" uname="$3" email="$4" pass="$5" role="${6:-user}" org="${7:-0}" body
  MF_CREATE=""; MF_LINK=""; MF_REG=""; MF_TOKEN=""
  # body 用 printf + 单引号模板拼：内嵌 \" 的写法在「双引号内的命令替换」里会触发 bash
  # 大括号展开（本仓 lint_uat_quotes.py 判死的那条），单引号模板天然安全。
  body=$(printf '{"username":"%s","email":"%s","role":"%s","tenant_id":%s,"org_id":%s}' "$uname" "$email" "$role" "$tid" "$org")
  MF_CREATE=$(curl -s $B/api/admin/users/create -H "$h" -H "$J" -d "$body")
  MF_LINK=$(printf '%s' "$MF_CREATE" | mf_field invite.code)
  [ -z "$MF_LINK" ] && return 1
  # 注册形态 = 前端 ?invite=<code> 落地页提交的同一份载荷（type=enterprise + role_choice=member）。
  body=$(printf '{"username":"%s","password":"%s","email":"%s","type":"enterprise","role_choice":"member","invite":"%s","agreed":true}' "$uname" "$pass" "$email" "$MF_LINK")
  MF_REG=$(curl -s $B/api/auth/register -H "$J" -d "$body")
  MF_TOKEN=$(printf '%s' "$MF_REG" | mf_field token)
  [ -n "$MF_TOKEN" ] || return 1
  return 0
}

# mf_token <邮箱> <口令> → stdout 打 token（取不到打空串）。
# 用于「同一批里两处都要拿同一个成员的会话」：重拨一次比让调用方隔着几十行去复用 MF_TOKEN 安全
# （MF_TOKEN 会被下一次 mf_onboard 覆写，跨段复用等于把两段耦在不显式的顺序上）。
mf_token(){ local body r
  body=$(printf '{"email":"%s","password":"%s"}' "$1" "$2")
  r=$(curl -s $B/api/auth/login -H "$J" -d "$body")
  printf '%s' "$r" | mf_field token
}
