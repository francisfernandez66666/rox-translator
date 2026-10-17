#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
增长日报 —— 从管理台漏斗接口拉数据，出一份给创始人看的增长读数

为什么需要：作战手册里写死了「别用曝光量做决策，要看激活」。
这张表就是把「访客→上传→注册→首购→续费」拆出来，每天一眼看完。

数据源（已具备，零开发）：
    GET /api/admin/funnel?days=N   仅超管；返回五环节聚合
    （见 README 漏斗段：registration_attribution + 后台漏斗面板）

用法：
    # 真实数据（需要超管 token）
    python3 scripts/growth/daily_pulse.py --base https://langcross.lexicorn.cn --token <超管Token> --days 7
    # 没环境时先看长什么样
    python3 scripts/growth/daily_pulse.py --dry-run
    python3 scripts/growth/daily_pulse.py --selftest

依赖：标准库 + 运行环境能 curl 出站（本仓历史出现过沙箱拦截出站，连不上就走 --dry-run）
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from datetime import date

HERE = os.path.dirname(os.path.abspath(__file__))

# curl 不走本地代理（若本机有 HTTP_PROXY，对 127.0.0.1 自测会 502）
CURL = ["curl", "--noproxy", "127.0.0.1,localhost"]

# 北极星指标：周活跃翻译用户（WATU）—— 见作战手册 §四
NORTH_STAR = "周活跃翻译用户（WATU）：一周内至少完成 1 次翻译的去重用户"

# 示例数据（--dry-run / 接口字段未确认时的兜底）
SAMPLE = {
    "window_days": 7,
    "stages": {
        "visitors": 1840,
        "trial_uploads": 312,      # 访客上传了至少 1 个文件
        "registrations": 96,
        "first_purchase": 21,
        "renewals": 9,
    },
    "channels": {
        "seo": {"visitors": 612, "uploads": 138},
        "direct": {"visitors": 402, "uploads": 79},
        "x_social": {"visitors": 388, "uploads": 41},
        "referral": {"visitors": 214, "uploads": 30},
        "other": {"visitors": 224, "uploads": 24},
    },
    "watu": 287,
    "note": "示例数据（--dry-run）。换成真实 --token 后显示你的真实漏斗。",
}


def fetch_funnel(base: str, token: str, days: int) -> tuple:
    """返回 (data, error)。拿不到就走示例。"""
    if not base or not token:
        return SAMPLE, "no-credentials"
    url = f"{base.rstrip('/')}/api/admin/funnel?days={days}"
    try:
        out = subprocess.run(
            CURL + ["-sS", "--max-time", "25", "-w", "\n%{http_code}",
                    "-H", f"Authorization: Bearer {token}", url],
            capture_output=True, text=True, timeout=35)
    except Exception as e:  # noqa: BLE001
        return SAMPLE, f"curl-failed:{e}"
    body, _, code = out.stdout.rpartition("\n")
    if code.strip() != "200":
        return SAMPLE, f"http-{code.strip()}"
    try:
        return json.loads(body), None
    except json.JSONDecodeError:
        return SAMPLE, "bad-json"


def pct(n: float, d: float) -> str:
    return f"{(n / d * 100):.1f}%" if d else "—"


def render(data: dict, error: str | None) -> str:
    s = data.get("stages", {})
    win = data.get("window_days", 7)
    L = []
    L.append("# 能言 · 增长日报")
    L.append("")
    L.append(f"窗口：近 {win} 天")
    L.append(f"\n> 北极星：**{NORTH_STAR}**")
    if error:
        L.append(f"\n> ⚠️ 真实接口未取到（{error}），以下为示例数据，仅用于确认报告形态。")
    elif data is SAMPLE:
        L.append("\n> ⚠️ 接口返回了示例兜底（未配置凭据），部署后用 --token 拉真实数据。")
    L.append("")

    visitors = s.get("visitors", 0)
    uploads = s.get("trial_uploads", 0)
    regs = s.get("registrations", 0)
    fp = s.get("first_purchase", 0)
    ren = s.get("renewals", 0)

    L.append("## 漏斗（全局）")
    L.append("")
    L.append("| 环节 | 数量 | 上一环节转化 |")
    L.append("|---|---:|---:|")
    L.append(f"| 访客 | {visitors:,} | — |")
    L.append(f"| ★ 上传文件（激活） | {uploads:,} | {pct(uploads, visitors)} |")
    L.append(f"| 注册 | {regs:,} | {pct(regs, uploads)} |")
    L.append(f"| 首购 | {fp:,} | {pct(fp, regs)} |")
    L.append(f"| 续费 | {ren:,} | {pct(ren, fp)} |")
    L.append("")
    L.append("> ★ 最该盯的是「访客→上传文件」这一行：它直接反映首触点有没有验证卖点。")
    L.append("> 注册/首购的转化再高，如果上传数小，那只是少数人的事，不是增长。")
    L.append("")

    if "watu" in data:
        L.append(f"**WATU（周活跃翻译用户）：{data['watu']:,}**")
        L.append("")

    ch = data.get("channels")
    if ch:
        L.append("## 渠道（按「带来激活」而非曝光）")
        L.append("")
        L.append("| 渠道 | 访客 | 上传 | 激活率 |")
        L.append("|---|---:|---:|---:|")
        # 按「上传数」降序——这才是该看的排序，不是访客数
        rows = []
        for name, c in ch.items():
            v = c.get("visitors", 0)
            u = c.get("uploads", 0)
            rows.append((name, v, u))
        for name, v, u in sorted(rows, key=lambda x: -x[2]):
            L.append(f"| {name} | {v:,} | {u:,} | {pct(u, v)} |")
        L.append("")
        L.append("> 砍渠道看这一列「上传数」，不看访客数。一条 10 万曝光的推文"
                 "带来 0 上传，和一个 300 阅读带来 12 上传的帖子，留后者。")
        L.append("")

    L.append("## 下一步动作")
    L.append("")
    L.append("1. 今天就把「访客→上传文件」转化率记下来当基线——没有基线就没有增长。")
    L.append("2. 哪个渠道上传数最高，明天就加码它；最低的，先停。")
    L.append("3. 上传了但没注册的，去查免登录体验是不是卡在某一步（RC-4 文件名不翻译等）。")
    L.append("")
    return "\n".join(L)


def selftest() -> int:
    out = render(SAMPLE, "selftest")
    fail = []
    if "增长日报" not in out:
        fail.append("未生成标题")
    if "上传文件（激活）" not in out:
        fail.append("缺激活环节")
    if "%" not in out:
        fail.append("缺转化率计算")
    if "WATU" not in out:
        fail.append("缺北极星指标")
    doc = render(SAMPLE, None)
    if "⚠️" not in doc:
        fail.append("示例/错误态缺少明确提示，会让人误以为是真实数据")
    if fail:
        print("SELFTEST FAIL")
        for f in fail:
            print("  ✖ " + f)
        return 1
    print("SELFTEST PASS  (标题 / 激活环节 / 转化率 / 北极星 / 示例提示 全部就位)")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description="能言 增长日报")
    ap.add_argument("--base", default="https://langcross.lexicorn.cn")
    ap.add_argument("--token", default="")
    ap.add_argument("--days", type=int, default=7)
    ap.add_argument("--dry-run", action="store_true", help="用示例数据，不联网")
    ap.add_argument("--out", default="", help="写文件而非只打印")
    ap.add_argument("--selftest", action="store_true")
    a = ap.parse_args()
    if a.selftest:
        return selftest()

    if a.dry_run:
        data, err = SAMPLE, "dry-run"
    else:
        data, err = fetch_funnel(a.base, a.token, a.days)
    report = render(data, err)

    if a.out:
        with open(a.out, "w", encoding="utf-8") as f:
            f.write(report)
        print(f"日报已写入 → {a.out}")
    else:
        print(report)
    if err:
        print(f"\n[info] 真实数据未取到（{err}），以上为示例形态。", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
