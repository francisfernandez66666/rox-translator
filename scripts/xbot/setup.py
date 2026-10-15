#!/usr/bin/env python3
# ============================================================================
# scripts/xbot/setup.py — 交互式写 .env（2026-10-13）
#
# 为什么单独做这个：.env 以点开头是隐藏文件，Finder 双击打不开、编辑起来麻烦，
# 而手改文本文件最容易漏字段或存错位置。答几个问题比开编辑器稳。
#
#   python3 scripts/xbot/setup.py
# ============================================================================
from __future__ import annotations

import getpass
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import xauth  # noqa: E402

ENV = xauth.ENV

FIELDS = [
    ("X_CLIENT_ID", "步骤 4 的 OAuth 2.0 Client ID（或步骤 3 的 API Key，二者通常相同）"),
    ("X_CLIENT_SECRET", "步骤 4 的 OAuth 2.0 Client Secret", True),
    ("X_USER_ID", "你要发推的 X 用户名（不带 @，例如 lexi）—— 留空可跳过"),
]

TEMPLATE = """# scripts/xbot/.env —— X 凭据，禁止入库（.gitignore 已覆盖 .env）
X_CLIENT_ID=
X_CLIENT_SECRET=
X_REDIRECT_URI=http://127.0.0.1:8765/callback
X_REFRESH_TOKEN=
X_ACCESS_TOKEN=
X_TOKEN_EXPIRES_AT=
X_USER_ID=
"""


def read_env() -> dict[str, str]:
    """读现有 .env（不存在返回空表）。"""
    cur: dict[str, str] = {}
    if ENV.exists():
        for line in ENV.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, v = line.split("=", 1)
                cur[k.strip()] = v.strip()
    return cur


def save_env(cur: dict[str, str]) -> None:
    """写 .env：固定字段顺序 + 注释头，权限收紧到 600（凭据仅本人可读写）。"""
    lines = ["# scripts/xbot/.env —— X 凭据，禁止入库（.gitignore 已覆盖 .env）"]
    for k in xauth.ENV_KEYS:
        lines.append(f"{k}={cur.get(k, '')}")
    ENV.write_text("\n".join(lines) + "\n", encoding="utf-8")
    ENV.chmod(0o600)


def main() -> int:
    # 管道/自动化环境下 stdin 不是 TTY，input() 会 EOFError —— 给明确提示而不是抛栈
    if not sys.stdin.isatty() and "--check" not in sys.argv:
        print("检测到非交互式环境（stdin 不是终端）。\n"
              "请在【终端】里直接运行：python3 scripts/xbot/setup.py\n"
              "只想看当前配置：python3 scripts/xbot/setup.py --check")
        return 1

    if "--check" in sys.argv:
        cur = read_env()
        print("当前 .env 状态" + ("（文件不存在）" if not ENV.exists() else ""))
        for k in xauth.ENV_KEYS:
            v = cur.get(k, "")
            mark = "✔ 已填" if v else ("· 程序自动维护" if k in ("X_REFRESH_TOKEN", "X_ACCESS_TOKEN", "X_TOKEN_EXPIRES_AT") else "✘ 还缺")
            show = ("*" * len(v)) if (v and "SECRET" in k) else (v or "—")
            print(f"  {mark:<12} {k:<20} {show}")
        need = [k for k in ("X_CLIENT_ID", "X_CLIENT_SECRET", "X_USER_ID") if not cur.get(k)]
        print("\n还缺：" + ("、".join(need) if need else "无，可进行浏览器授权了"))
        return 0

    if not ENV.exists():
        ENV.write_text(TEMPLATE, encoding="utf-8")
        print(f"已按模板新建 {ENV}\n")

    cur = read_env()

    print("=" * 64)
    print("  X 凭据配置（Ctrl+C 可随时退出，不会写坏文件）")
    print("=" * 64)
    for item in FIELDS:
        key, desc = item[0], item[1]
        secret = len(item) > 2 and item[2]
        old = cur.get(key, "")
        shown = ("已填 " + old[:6] + "…" + old[-4:]) if (old and secret) else (old or "（空）")
        print(f"\n{key}")
        print(f"  {desc}")
        print(f"  当前：{shown}")
        if secret:
            v = getpass.getpass("  新值（直接回车＝不改）：").strip()
        else:
            v = input("  新值（直接回车＝不改）：").strip()
        if v:
            cur[key] = v

    print(f"\nX_REDIRECT_URI（回调地址，必须与门户里逐字符一致）")
    print(f"  当前：{cur.get('X_REDIRECT_URI', xauth.REDIRECT_URI)}")
    v = input("  新值（直接回车＝不改）：").strip()
    if v:
        cur["X_REDIRECT_URI"] = v
    cur.setdefault("X_REDIRECT_URI", xauth.REDIRECT_URI)
    save_env(cur)

    print("\n" + "=" * 64)
    print(f"  已写入 {ENV}（权限已收紧为 600，仅本人可读写）")
    print("=" * 64)
    missing = [k for k in ("X_CLIENT_ID", "X_USER_ID") if not cur.get(k)]
    if missing:
        print("\n还缺：" + "、".join(missing) + "（可重跑本脚本补上）")
    else:
        print("\n下一步：")
        print("  1) 浏览器授权拿 code（见 P0_凭据申请指南.md 步骤 6）")
        print("  2) python3 xbot.py auth --code 'XXXX' --verifier 'YYYY'")
        print("  3) python3 xbot.py probe   ← 四步全绿才准接定时任务")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        print("\n已取消，未写入任何内容。")
        raise SystemExit(130)