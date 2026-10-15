# ============================================================================
# scripts/xbot/xauth.py — OAuth 2.0 PKCE（2026-10-13）
#
# ★ 为什么必须是 OAuth 2.0 PKCE：X 已于 2025-03-31 对自助档位弃用
#   upload.twitter.com/1.1/media/upload.json，老教程里的 requests_oauthlib OAuth1
#   方案在自助账号上直接失效。
# ★ 为什么必须断言 media.write：缺它 ⇒ 403 on upload + 200 on tweets
#   （能发文字、发不了图）。且 scope 在授权那一刻固定，事后在门户加的只对新 token 生效。
# ============================================================================
from __future__ import annotations

import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

BASE = Path(__file__).resolve().parent
ENV = BASE / ".env"
TOKEN_URL = "https://api.x.com/2/oauth2/token"
AUTHORIZE_URL = "https://x.com/i/oauth2/authorize"
REQUIRED_SCOPES = {"tweet.write", "media.write"}

# ★★ 回调地址：指向自己站点的静态页，不是本地服务。
#   为什么：定时任务是无人值守的，任何依赖「本机 8765 端口在监听」的设计都会在
#   关机 / 休眠 / 换机器时断掉。redirect_uri 只在【首次一次性授权】时用一次，
#   之后每天的发布只用 refresh_token 自动续期，根本不碰它。
#   为什么不用 localhost：X 对 localhost 只允许原生应用端口浮动，且回调会停在
#   「无法访问此网站」——code 虽在地址栏里，但体验像坏了。
#
#   前置依赖：x-auth-callback.html 必须已部署到线上（frontend-react/public/）。
#   若暂未部署，把 .env 的 X_REDIRECT_URI 改成 http://127.0.0.1:8765/callback，
#   并在门户里把该地址也加进白名单（白名单上限 10 个，两个都注册最省事）。
DEFAULT_REDIRECT_URI = "https://langcross.lexicorn.cn/x-auth-callback.html"

# 门户里要一并注册的另一个地址（静态页没部署时的兜底）
LOOPBACK_REDIRECT_URI = "http://127.0.0.1:8765/callback"

SCOPES = "tweet.read tweet.write users.read offline.access media.write"

ENV_KEYS = ["X_CLIENT_ID", "X_CLIENT_SECRET", "X_REDIRECT_URI", "X_REFRESH_TOKEN",
            "X_ACCESS_TOKEN", "X_TOKEN_EXPIRES_AT", "X_USER_ID"]


class AuthError(RuntimeError):
    pass


def _load_env() -> dict:
    """读 .env（不入库，.gitignore 已覆盖）。"""
    env = {}
    if ENV.exists():
        for line in ENV.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            env[k.strip()] = v.strip()
    # 真实环境变量优先（方便 CI 注入，不落盘）
    for k in ENV_KEYS:
        if os.environ.get(k):
            env[k] = os.environ[k]
    return env


def _save_env(env: dict) -> None:
    lines = ["# scripts/xbot/.env —— X 凭据，禁止入库（.gitignore 已覆盖 .env）"]
    for k in ENV_KEYS:
        if env.get(k):
            lines.append(f"{k}={env[k]}")
    ENV.write_text("\n".join(lines) + "\n", encoding="utf-8")


def _post_form(url: str, data: dict) -> dict:
    body = urllib.parse.urlencode(data).encode()
    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/x-www-form-urlencoded"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        detail = e.read().decode(errors="replace")[:300]
        raise AuthError(f"HTTP {e.code} token 端点：{detail}") from e
    except urllib.error.URLError as e:
        # ★ 出站被拦时这里表现为 URLError，不是权限问题；先怀疑网络再怀疑凭据
        raise AuthError(f"网络不可达（{e.reason}）——先确认能出站到 api.x.com") from e


def ensure_token() -> str:
    """返回可用 access_token；过期自动 refresh 并回写 .env。

    ★ refresh_token 每次使用都会被 X 轮换（rotate）——旧值立即失效。
      所以每次刷新都必须把返回的新 refresh_token 一起存回 .env，
      否则第一次刷新后第二次必然 401。（2026-10-13 补：不存 ⇒ 定时任务第二天必死）
    """
    env = _load_env()
    if not env.get("X_CLIENT_ID"):
        raise AuthError("缺 X_CLIENT_ID（见 P0 指南，先去 developer.x.com 申请）")
    if not env.get("X_REFRESH_TOKEN"):
        raise AuthError("缺 X_REFRESH_TOKEN（还没跑过 xbot.py auth）")

    exp = int(env.get("X_TOKEN_EXPIRES_AT", "0") or 0)
    if env.get("X_ACCESS_TOKEN") and exp > time.time() + 120:
        return env["X_ACCESS_TOKEN"]

    payload = {
        "grant_type": "refresh_token",
        "refresh_token": env["X_REFRESH_TOKEN"],
        "client_id": env["X_CLIENT_ID"],
    }
    if env.get("X_CLIENT_SECRET"):
        payload["client_secret"] = env["X_CLIENT_SECRET"]
    _absorb_token(_post_form(TOKEN_URL, payload), env)
    return env["X_ACCESS_TOKEN"]


def redirect_uri() -> str:
    """.env 里配的优先，没配就用站点静态页。"""
    return _load_env().get("X_REDIRECT_URI") or DEFAULT_REDIRECT_URI


def build_authorize_url(client_id: str, verifier: str | None = None) -> str:
    """拼授权链接。用户点一次即可换到长期 refresh_token。

    注意：链接里带的 redirect_uri 与 xbot.py auth --code 用的必须完全一致，
    否则 token 端点会拒（OAuth 要求授权请求与换 token 请求的 redirect_uri 相同）。
    """
    q = {
        "response_type": "code",
        "client_id": client_id,
        "redirect_uri": redirect_uri(),
        "scope": SCOPES,
        "state": "xbot",
    }
    return f"{AUTHORIZE_URL}?{urllib.parse.urlencode(q)}"


def exchange_code(code: str, verifier: str) -> dict:
    """用授权拿到的 code + 门户给的 code_verifier 换首对 token，并落盘 .env。"""
    env = _load_env()
    if not env.get("X_CLIENT_ID"):
        raise AuthError("缺 X_CLIENT_ID")
    payload = {
        "grant_type": "authorization_code",
        "code": code.strip(),
        "redirect_uri": redirect_uri(),
        "client_id": env["X_CLIENT_ID"],
        "code_verifier": verifier.strip(),
    }
    if env.get("X_CLIENT_SECRET"):
        payload["client_secret"] = env["X_CLIENT_SECRET"]
    data = _post_form(TOKEN_URL, payload)
    _absorb_token(data, env)
    return data


def _absorb_token(data: dict, env: dict) -> None:
    """校验 scope 并把 access/refresh/过期时间写回 env 并落盘。"""
    scope = set((data.get("scope") or "").split())
    missing = REQUIRED_SCOPES - scope
    if missing:
        raise AuthError(
            f"scope 缺 {sorted(missing)} ⇒ 发图文必 403。\n"
            f"  需重新走授权（scope 在授权那刻固定，门户后台后来加的只对新 token 生效）。\n"
            f"  当前 scope={data.get('scope')!r}"
        )
    env["X_ACCESS_TOKEN"] = data["access_token"]
    env["X_TOKEN_EXPIRES_AT"] = str(int(time.time()) + int(data.get("expires_in", 7200)))
    # ★ refresh_token 轮换：只有返回了新值才覆盖；返回空则保留旧值不动
    if data.get("refresh_token"):
        env["X_REFRESH_TOKEN"] = data["refresh_token"]
    _save_env(env)


def verify_user() -> dict:
    """GET /2/users/me：确认凭据对应的就是目标账号，防发错号。"""
    token = ensure_token()
    req = urllib.request.Request("https://api.x.com/2/users/me",
                                 headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            d = json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        raise AuthError(f"GET /2/users/me HTTP {e.code}：{e.read().decode(errors='replace')[:200]}") from e
    except urllib.error.URLError as e:
        raise AuthError(f"网络不可达（{e.reason}）") from e
    u = (d or {}).get("data") or {}
    if not u.get("username"):
        raise AuthError(f"users/me 未返回 username，响应异常：{d}")
    env = _load_env()
    want = env.get("X_USER_ID", "").strip().lstrip("@")
    if want and want.lower() != u["username"].lower():
        raise AuthError(f"凭据账号是 @{u['username']}，但 .env 写的是 @{want} —— 拒绝发送")
    return u