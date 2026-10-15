# ============================================================================
# scripts/xbot/xbot.py — CLI 入口（2026-10-13）
#
# ★ 主路径：prep 备料（人工审）→ 手动发 → mark 回填。post 只在应急时用。
#
#   python3 scripts/xbot/prep.py --date 2026-10-20 --open   ★ 每天走这条
#   python3 scripts/xbot/xbot.py mark --date 2026-10-20 --seq 1 --tweet-id <id>
#   python3 scripts/xbot/xbot.py mark --date 2026-10-20 --seq 2 --skipped
#
# 备用（不作为每日路径）：
#   python3 xbot.py post --date 2026-10-20 --seq 1            打印将发内容（不发）
#   python3 xbot.py post --date 2026-10-20 --seq 1 --live     真发
#   python3 xbot.py del <tweet_id>                 删推补救（人工发也需要）
#   python3 xbot.py stats                          额度与连败
#   python3 xbot.py probe                          四步探针
#
# ★★ mark 是人工发方案的命门：发了（或毙了）必须回填台账，
#    否则 guard 的反重复闸门读不到历史，第二天 AI 会把今天毙掉的文案换个说法再生成一遍。
# ============================================================================
from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import guard  # noqa: E402
import xauth  # noqa: E402

BASE = Path(__file__).resolve().parent
LEDGER = BASE / "ledger.jsonl"
DAILY_LIMIT = 3        # 单日上限（硬编码，防生成器失控或重复触发）
MONTH_LIMIT = 500      # X Free 档月额度
WARN_AT = 450          # 到这条线开始提醒


# ── 台账 ──────────────────────────────────────────────────────────────────
def append_ledger(row: dict) -> None:
    with LEDGER.open("a", encoding="utf-8") as f:
        f.write(json.dumps(row, ensure_ascii=False) + "\n")


def month_count() -> int:
    ym = datetime.now().strftime("%Y-%m")
    n = 0
    for r in guard.load_ledger(LEDGER):
        if str(r.get("ts", "")).startswith(ym) and r.get("result") == "ok":
            n += 1
    return n


def consecutive_fail_days() -> int:
    """台账尾部连续有 failed 的天数；≥2 熔断。"""
    rows = guard.load_ledger(LEDGER)
    days = []
    for r in rows:
        if r.get("result") in ("ok", "failed"):
            days.append((str(r.get("ts", ""))[:10], r.get("result")))
    cnt, prev = 0, None
    for d, res in reversed(days):
        if res == "failed" and (prev is None or d == prev):
            cnt += 1
            prev = d
        elif res == "failed":
            break
        else:
            break
    return cnt


# ── X API ─────────────────────────────────────────────────────────────────
def _post_tweet(token: str, text: str, media_id: str | None) -> tuple[int, dict, dict]:
    payload: dict = {"text": text}
    if media_id:
        payload["media"] = {"media_ids": [media_id]}
    req = urllib.request.Request(
        "https://api.x.com/2/tweets",
        data=json.dumps(payload).encode(),
        headers={"Authorization": f"Bearer {token}",
                 "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            return r.status, json.loads(r.read().decode()), dict(r.headers)
    except urllib.error.HTTPError as e:
        hdrs = dict(e.headers or {})
        return e.code, {"detail": e.read().decode(errors="replace")[:400]}, hdrs
    except urllib.error.URLError as e:
        return 0, {"detail": f"网络不可达：{e.reason}"}, {}


def _upload_media(token: str, path: Path) -> str:
    """POST /2/media/upload：INIT → APPEND → FINALIZE（单图一次段，不必分片）。"""
    import base64
    boundary = "----xbotboundary1234567890"
    data = path.read_bytes()
    media_type = "image/png" if path.suffix == ".png" else "image/jpeg"

    def part(fields: dict, files: dict | None = None) -> bytes:
        buf = b""
        for k, v in fields.items():
            buf += (f"--{boundary}\r\nContent-Disposition: form-data; name=\"{k}\"\r\n\r\n{v}\r\n").encode()
        for k, (fn, content, ct) in (files or {}).items():
            buf += (f"--{boundary}\r\nContent-Disposition: form-data; name=\"{k}\"; filename=\"{fn}\"\r\n"
                    f"Content-Type: {ct}\r\n\r\n").encode() + content + b"\r\n"
        return buf + f"--{boundary}--\r\n".encode()

    def send(body: bytes):
        req = urllib.request.Request(
            "https://api.x.com/2/media/upload", data=body,
            headers={"Authorization": f"Bearer {token}",
                     "Content-Type": f"multipart/form-data; boundary={boundary}"})
        with urllib.request.urlopen(req, timeout=120) as r:
            return json.loads(r.read().decode())

    init = send(part({"command": "INIT", "media_type": media_type, "total_bytes": str(len(data))}))
    mid = init.get("data", {}).get("id")
    if not mid:
        raise RuntimeError(f"INIT 未返回 media_id：{init}")
    send(part({"command": "APPEND", "media_id": mid},
              {"media": (path.name, data, media_type)}))
    fin = send(part({"command": "FINALIZE", "media_id": mid}))
    # ★ 必须等 FINALIZE 成功再发帖：提前挂 media_id 会发出一条空图推文
    if fin.get("data", {}).get("processing_info"):
        raise RuntimeError("FINALIZE 返回 processing_info，视频类才需轮询；图片不应出现")
    return mid


def _err_code(status: int, body: dict) -> str:
    if status == 401:
        return "E401"
    if status == 403:
        return "E403M" if "media" in json.dumps(body).lower() else "E403"
    if status == 429:
        return "E429"
    if 400 <= status < 500:
        return "E4XX"
    if status >= 500:
        return "E5XX"
    return f"E{status}"


# ── 人工回填（mark）── ────────────────────────────────────────────────────
def cmd_mark(args) -> int:
    """人工发完/毙掉一条后回填台账。

    ★★ 这是「AI 备料 + 人工发」方案的命门：
       台账是 guard 反重复闸门的唯一输入（guard.load_ledger 只读 ledger.jsonl）。
       人工发不经过 post，台账就不会自己长出来 ⇒ 第二天 AI 生成时读不到今天的历史，
       会把今天刚毙掉的文案换个说法再生成一遍。回填就是补上这一段。
    ★ --skipped 也要回填：被毙掉的文案同样不该再生成，且必须记住「已审未发」。
    """
    p = BASE / "out" / "pending" / f"{args.date}.json"
    if not p.exists():
        print(f"FAIL  找不到 {p}，无法回填（连备料源都没有，refuse 记一条空的）")
        return 1
    data = json.loads(p.read_text(encoding="utf-8"))
    item = next((i for i in data.get("items", []) if i["seq"] == args.seq), None)
    if item is None:
        print(f"FAIL  {args.date} 没有 seq={args.seq}")
        return 1

    # ★ 幂等：同一天同一条不许重复回填。重复写会让台账虚增（额度统计失真），
    #   也让反重复池里塞进自己的副本，把相似度算高。
    if item.get("marked"):
        print(f"WARN  {args.date} 第 {item['seq']} 条已经回填过了，不重复写")
        print(f"      确实要再记一次的话，先把 pending/{args.date}.json 里的 marked 字段删掉")
        left = [i["seq"] for i in data["items"] if not i.get("marked")]
        print(f"      当日待回填：{left if left else '无，全部处理完了'}")
        return 1

    # 回填前重跑一次闸门：人工可能改过文案，改完超字数/踩禁用词要当场发现
    text = item["text"]
    wl = guard.weighted_len(text)
    print(f"{args.date} 第 {item['seq']} 条　{item.get('slot')}　{item.get('angle')}　{item.get('topic_id')}")
    print(f"加权字符 {wl}/280　闸门结论 {item.get('checks', {}).get('ok')}")
    if wl > 280:
        print(f"★ 警告：人工改过文案且已超字数 {wl} > 280，发出去会被 X 截断")
    if args.tweet_id and not args.tweet_id.isdigit():
        print(f"★ 警告：tweet_id「{args.tweet_id}」不是纯数字，多半是从地址栏粘错了")
    if args.skipped and args.tweet_id:
        print("FAIL  --skipped 与 --tweet-id 互斥：没发就不该有 tweet_id")
        return 1

    now = datetime.now().astimezone().isoformat(timespec="seconds")
    row = {
        "ts": now, "seq": item["seq"], "slot": item.get("slot"), "angle": item.get("angle"),
        "topic_id": item.get("topic_id"), "weighted_len": wl, "text": text,
        "image": item.get("image"),
        "result": "skipped" if args.skipped else "ok",
        "mode": "manual",
    }
    if args.tweet_id:
        row["tweet_id"] = args.tweet_id
    append_ledger(row)

    if args.skipped:
        print("OK    已记「审了不发」——它会进反重复池，明天 AI 不会拿它换皮重来")
    else:
        print(f"OK    已记已发布 https://x.com/i/web/status/{args.tweet_id}"
              if args.tweet_id else "OK    已记已发布（没给 tweet_id，删推时用不上）")
    # 顺带把 pending 标掉，避免同一天重复回填同一条
    item["marked"] = True
    p.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
    left = [i["seq"] for i in data.get("items", []) if not i.get("marked")]
    print(f"      当日待回填：{left if left else '无，全部处理完了'}")
    return 0


# ── 子命令 ────────────────────────────────────────────────────────────────
def cmd_probe(_args=None) -> int:
    print("== 探针 1/4：凭据与账号 ==")
    try:
        u = xauth.verify_user()
    except xauth.AuthError as e:
        print(f"FAIL  {e}")
        return 1
    print(f"OK    目标账号 @{u['username']}（{u.get('name','')}）")

    print("\n== 探针 2/4：发一条纯文字 ==")
    token = xauth.ensure_token()
    status, body, _ = _post_tweet(token, "xbot 探针，稍后删除", None)
    if status != 201:
        print(f"FAIL  {_err_code(status, body)} {body.get('detail', body)}")
        return 1
    tid = body["data"]["id"]
    print(f"OK    tweet_id={tid}")

    print("\n== 探针 3/4：发一条带图 ==")
    img = BASE.parents[1] / "品牌资产" / "logo-1024.png"
    if not img.exists():
        print(f"SKIP  找不到 {img}，跳过带图探针（发图探针未验证 ⇒ 不许接定时任务）")
    else:
        try:
            mid = _upload_media(token, img)
            status, body2, _ = _post_tweet(token, "xbot 配图探针，稍后删除", mid)
        except Exception as e:  # noqa: BLE001
            print(f"FAIL  上传异常 {e}")
            return 1
        if status != 201:
            print(f"FAIL  {_err_code(status, body2)} {body2.get('detail', body2)}")
            print("      403 on upload + 200 on tweets ⇒ scope 缺 media.write，重新走授权")
            return 1
        tid2 = body2["data"]["id"]
        print(f"OK    tweet_id={tid2}（media_id={mid}）")
        _delete(token, tid2)

    print("\n== 探针 4/4：删除测试推 ==")
    _delete(token, tid)
    print("\n四步全绿 ⇒ 允许接定时任务（手册 §9）")
    return 0


def _delete(token: str, tweet_id: str) -> None:
    req = urllib.request.Request(f"https://api.x.com/2/tweets/{tweet_id}",
                                 headers={"Authorization": f"Bearer {token}"}, method="DELETE")
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            print(f"OK    已删除 {tweet_id}（HTTP {r.status}）")
    except urllib.error.HTTPError as e:
        print(f"WARN  删除 {tweet_id} 失败 HTTP {e.code}：{e.read().decode(errors='replace')[:200]}")


def cmd_post(args) -> int:
    p = BASE / "out" / "pending" / f"{args.date}.json"
    if not p.exists():
        print(f"FAIL  找不到 {p}")
        return 1
    data = json.loads(p.read_text(encoding="utf-8"))
    item = next((i for i in data["items"] if i["seq"] == args.seq), None)
    if item is None:
        print(f"FAIL  {args.date} 没有 seq={args.seq}")
        return 1

    text = item["text"]
    wl = guard.weighted_len(text)
    print(f"日期 {args.date}  档 {item['slot']}  选题 {item.get('topic_id')}  加权 {wl}/280")
    print("─" * 60)
    print(text)
    print("─" * 60)

    if not args.live:
        print("DRY-RUN：未发送。加 --live 才真发。")
        return 0

    today = datetime.now().strftime("%Y-%m-%d")
    if today != args.date:
        print(f"WARN  待发日期({args.date})≠今天({today})")
    done_today = sum(1 for r in guard.load_ledger(LEDGER)
                     if str(r.get("ts", "")).startswith(today) and r.get("result") == "ok")
    if done_today >= DAILY_LIMIT:
        print(f"FAIL  今日已发 {done_today} 条，达单日上限 {DAILY_LIMIT}")
        return 1
    if consecutive_fail_days() >= 2:
        print("FAIL  连续 2 天失败，熔断。需人工处理后手动恢复。")
        return 1
    mc = month_count()
    if mc >= MONTH_LIMIT:
        print(f"FAIL  本月已发 {mc} 条，达 Free 档上限 {MONTH_LIMIT}")
        return 1

    token = xauth.ensure_token()
    media_id = None
    img = BASE / item.get("image", "")
    if item.get("image") and img.exists():
        try:
            media_id = _upload_media(token, img)
        except Exception as e:  # noqa: BLE001
            print(f"FAIL  上传失败：{e}")
            return 1

    status, body, hdrs = _post_tweet(token, text, media_id)
    now = datetime.now().astimezone().isoformat(timespec="seconds")
    if status == 201:
        tid = body["data"]["id"]
        append_ledger({"ts": now, "seq": item["seq"], "angle": item.get("angle"),
                       "topic_id": item.get("topic_id"), "tweet_id": tid,
                       "weighted_len": wl, "text": text, "image": item.get("image"),
                       "result": "ok"})
        print(f"OK    已发布 https://x.com/i/web/status/{tid}（tweet_id={tid}）")
        return 0

    code = _err_code(status, body)
    append_ledger({"ts": now, "seq": item["seq"], "angle": item.get("angle"),
                   "topic_id": item.get("topic_id"), "weighted_len": wl, "text": text,
                   "result": "failed", "err": code})
    print(f"FAIL  {code}  {body.get('detail', body)}")
    if code == "E429":
        rst = hdrs.get("x-rate-limit-reset") or hdrs.get("X-Rate-Limit-Reset")
        if rst:
            print(f"      限流恢复时刻 {time.strftime('%Y-%m-%d %H:%M:%S', time.localtime(int(rst)))}")
            print("      ★ 睡到该时刻再投，不要立即重试；当日剩余条目顺延次日")
    if code in ("E401", "E403", "E403M"):
        print("      ★ 立即熔断，不重试。凭据/scope 问题重投只会连续失败")
    return 1


def cmd_auth(args) -> int:
    """无参：打印授权链接。带 --code：换 token。两种用法共用一个命令。"""
    from xauth import _load_env, redirect_uri
    e = _load_env()
    cid = (e.get("X_CLIENT_ID") or "").strip()
    if not cid:
        print("还没填 X_CLIENT_ID。先跑：python3 scripts/xbot/setup.py")
        return 1
    ru = redirect_uri()
    loop = ("127.0.0.1" in ru) or ("localhost" in ru)
    print("=" * 68)
    print("  回调地址（必须与门户白名单逐字符一致）：")
    print(f"    {ru}")
    print()
    if loop:
        print("  ⚠ 本地回环地址：授权时那台机器必须开着且端口在监听。")
        print("    浏览器会停在「无法访问此网站」，但地址栏里有 code，复制出来即可。")
    else:
        print("  ✅ 指向自己站点的静态页，不需要任何本地服务，关机休眠都不影响。")
        print("    （需已部署 frontend-react/public/x-auth-callback.html）")
    print()
    print("  ① 复制下面整行到浏览器打开，点 Authorize：")
    print()
    print(xauth.build_authorize_url(cid))
    print()
    print("  ② 授权后页面会显示 code，复制它。")
    print("  ③ 回到终端执行：")
    print("     python3 xbot.py auth --code '<粘贴code>' --verifier '<第5步的CodeVerifier>'")
    print()
    print("  ★ 换到 token 之后，这个回调地址再也不需要了。")
    print("    之后的每日发布只用 refresh_token 自动续期，不依赖任何本地服务。")
    print("=" * 68)
    return 0


def cmd_auth_code(args) -> int:
    """用授权拿到的 code + code_verifier 换 token 并落 .env。"""
    try:
        data = xauth.exchange_code(args.code, args.verifier)
    except xauth.AuthError as e:
        print(f"FAIL  {e}")
        print("\n排查：")
        print("  · code 只能用一次，复制时别带多余空格，别等太久（有效期很短）")
        print("  · code_verifier 必须与门户 OAuth 2.0 PKCE 页生成的那一串一致")
        print("  · 授权链接里的 redirect_uri 与本次换 token 用的是同一个")
        return 1
    print(f"OK    token 已写入 {xauth.ENV.name}")
    print(f"      scope = {data.get('scope')}")
    print(f"      有效期 = {data.get('expires_in')} 秒（之后自动刷新，refresh_token 会轮换）")
    if "media.write" not in (data.get("scope") or ""):
        print("      ★ 缺 media.write ⇒ 配图发不出去，需重走授权")
    from xauth import _load_env
    if not _load_env().get("X_USER_ID"):
        print("\n提示：.env 里 X_USER_ID 还空着，填上你的 X 用户名可防发错号。")
    return 0


def cmd_stats(_args=None) -> int:
    today = datetime.now().strftime("%Y-%m-%d")
    rows = guard.load_ledger(LEDGER)
    t = sum(1 for r in rows if str(r.get("ts", "")).startswith(today) and r.get("result") == "ok")
    m = month_count()
    f = consecutive_fail_days()
    print(f"今日已发 {t}/{DAILY_LIMIT}　本月 {m}/{MONTH_LIMIT}"
          f"{'　⚠ 已过 ' + str(WARN_AT) + '，额度将尽' if m >= WARN_AT else ''}　连败 {f} 天"
          f"{'　★ 熔断' if f >= 2 else ''}")
    return 0


def cmd_del(args) -> int:
    token = xauth.ensure_token()
    _delete(token, args.tweet_id)
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(prog="xbot")
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("probe")
    sub.add_parser("stats")
    au = sub.add_parser("auth", help="无参打印授权链接；带 --code 换 token")
    au.add_argument("--code", help="授权回调 URL 里的 code 参数")
    au.add_argument("--verifier", help="门户 OAuth 2.0 PKCE 生成的 Code Verifier")
    p = sub.add_parser("post"); p.add_argument("--date", required=True); p.add_argument("--seq", type=int, required=True)
    p.add_argument("--live", action="store_true")
    mk = sub.add_parser("mark", help="人工发完/毙掉一条后回填台账（★ 人工发方案的命门）")
    mk.add_argument("--date", required=True)
    mk.add_argument("--seq", type=int, required=True)
    mk.add_argument("--tweet-id", help="已发布时的推文 ID（X 地址栏末尾数字），用于事后删推")
    mk.add_argument("--skipped", action="store_true", help="审了但没发（也要回填，否则明天换皮重来）")
    d = sub.add_parser("del"); d.add_argument("tweet_id")
    a = ap.parse_args()
    if a.cmd == "auth":
        if not (getattr(a, "code", None) and getattr(a, "verifier", None)):
            return cmd_auth(a)
        if not (a.code and a.verifier):
            print("--code 与 --verifier 必须同时给（见提示）")
            return 1
        return cmd_auth_code(a)
    return {"probe": cmd_probe, "post": cmd_post, "stats": cmd_stats,
            "del": cmd_del, "mark": cmd_mark}[a.cmd](a)


if __name__ == "__main__":
    raise SystemExit(main())