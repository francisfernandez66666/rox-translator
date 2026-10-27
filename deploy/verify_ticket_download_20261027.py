#!/usr/bin/env python3
# ============================================================================
# deploy/verify_ticket_download_20261027.py — D-01 端到端字节取回闭环（**在部署机上跑**）
#
# 为什么要这么绕：2026-10-27 那批 UAT 只做到"库里那行指向的文件在盘上"＋"正确形态的端点回 401"，
#   端到端字节没取过 —— 因为公网发版机的跨境那一跳不稳（今天实测 SSL 重置／半截体），
#   而且取件要属主凭证。所以这一份**全程只在服务器本机跑**：
#   ① 用生产 JWT_SECRET 现签一条 6 小时短期 token（与 UAT 的 mint_token.sh 同一口径）；
#   ② 打回环 `127.0.0.1:8787/api/tickets/download?id=<id>`；
#   ③ 把响应体与库里 `ticket_files.result_path` 指向的那个文件做 **sha256 逐字节等值**＋魔数＋体积三判。
#   ★ 全程不把 token / JWT_SECRET / DSN 打印到 stdout，也不落盘：凭证只活在进程内存里。
#   ★ 只读：不写库、不改任何业务数据；GET 一个已存在的产物而已。
#
# 用法（在部署机上）：python3 verify_ticket_download_20261027.py 133
#   多单：python3 verify_ticket_download_202627.py 133 132 127
# 判绿只认末行 `TICKETDL_ALL_OK=1`。
# ============================================================================
import base64
import hashlib
import hmac
import json
import os
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request


def b64e(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).decode().rstrip("=")


def read_env() -> dict:
    """把 /etc/translator/secrets.env 读进内存（不外传、不打印值）。"""
    out = {}
    proc = subprocess.run(["sh", "-c", "set -a; . /etc/translator/secrets.env; env"],
                          capture_output=True, text=True)
    for line in proc.stdout.splitlines():
        if "=" in line:
            k, v = line.split("=", 1)
            out[k] = v
    return out


def sign_token(secret: str, uid: str) -> str:
    """按服务端 claims 口径现签一条短期 token（与 handlers 里校验的字段一致）。"""
    dsn = os.environ.get("DB_DSN") or ""
    res = subprocess.run(["psql", dsn, "-t", "-A", "-F", "|", "-c",
                          "select id,tenant_id,username,role,coalesce(token_version,0),"
                          "coalesce(must_change_pwd,0) from users where id=" + uid],
                         capture_output=True, text=True)
    rows = [r for r in res.stdout.strip().splitlines() if "|" in r]
    if not rows:
        raise SystemExit("NO_SUCH_USER " + uid)
    _id, tid, username, role, tv, mcp = rows[0].split("|")
    header = {"alg": "HS256", "typ": "JWT"}
    claims = {"uid": int(_id), "tid": int(tid), "username": username, "role": role,
              "exp": int(time.time()) + 6 * 3600, "tv": int(tv), "jti": secrets.token_hex(16),
              "mcp": mcp == "1"}
    signing = b64e(json.dumps(header, separators=(",", ":")).encode()) + "." + \
        b64e(json.dumps(claims, separators=(",", ":"), ensure_ascii=False).encode())
    return signing + "." + b64e(hmac.new(secret.encode(), signing.encode(), hashlib.sha256).digest())


def disk_expect(ticket_id: str) -> list:
    """现读库里那单该交付什么：每行 (file_name, result_path)。"""
    dsn = os.environ.get("DB_DSN") or ""
    res = subprocess.run(["psql", dsn, "-t", "-A", "-F", "\t", "-c",
                          "select coalesce(file_name,'-'), coalesce(result_path,'') "
                          "from ticket_files where ticket_id=" + ticket_id + " order by id;"],
                         capture_output=True, text=True)
    return [r.split("\t") for r in res.stdout.strip().splitlines() if r]


def pick_candidates() -> list:
    """挑现役、有产物且盘上还在的单，连"属主"与"另一租户的一个身份"一起带回来。

    ★ 为什么现读现挑而不是写死 id：2026-10-27 那批把 UAT 测试单（含 133）整段清掉了
      —— 写死 id 会让这条判据变成"查不到 ⇒ 判红"的空转，而红字完全指不到真正的原因。
    """
    dsn = os.environ.get("DB_DSN") or ""
    sql = ("select t.id, t.ticket_no, t.status, t.tenant_id, t.created_by, tf.result_path "
           "from tickets t join ticket_files tf on tf.ticket_id=t.id "
           "where t.status='completed' and coalesce(tf.result_path,'')<>'' "
           "order by t.id desc limit 25;")
    res = subprocess.run(["psql", dsn, "-t", "-A", "-F", "\t", "-c", sql], capture_output=True, text=True)
    out = []
    for line in res.stdout.strip().splitlines():
        parts = line.split("\t")
        if len(parts) == 6 and parts[5].startswith("/") and os.path.isfile(parts[5]):
            out.append(parts)
    return out[:3]


def pick_foreign_user(home_tenant: str) -> str:
    """找一个**不属于该租户**的启用账号，用来打隔离反向腿（期望拿不到字节）。"""
    dsn = os.environ.get("DB_DSN") or ""
    res = subprocess.run(["psql", dsn, "-t", "-A", "-c",
                          "select id from users where coalesce(tenant_id,0) <> " + home_tenant +
                          " and coalesce(status,'active')='active' and id <> 1 order by id desc limit 1;"],
                         capture_output=True, text=True)
    return res.stdout.strip().splitlines()[0] if res.stdout.strip() else ""


def dial(url: str, uid: str, secret: str) -> tuple:
    req = urllib.request.Request(url, headers={"Authorization": "Bearer " + sign_token(secret, uid)})
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            return r.status, r.read(), r.headers.get("Content-Disposition", "") or "-"
    except urllib.error.HTTPError as e:
        return e.code, e.read(), "-"
    except Exception as e:  # noqa: BLE001 — 链路失败如实报，不许吞成"通过"
        return 0, b"", type(e).__name__


def main() -> int:
    ids = sys.argv[1:]
    env = read_env()
    secret = env.get("JWT_SECRET", "")
    if not secret or not env.get("DB_DSN"):
        print("ABORT=1 读不到 JWT_SECRET/DB_DSN（先核 /etc/translator/secrets.env）")
        return 2
    os.environ["DB_DSN"] = env["DB_DSN"]
    port = os.environ.get("PORT", "8787")
    fails = 0

    if ids:  # 指定单号模式（只判属主腿）
        for tid in ids:
            code, body, disp = dial("http://127.0.0.1:%s/api/tickets/download?id=%s" % (port, tid), "10001", secret)
            rows = disk_expect(tid)
            paths = [x for _, x in rows if x]
            want = hashlib.sha256(open(paths[0], "rb").read()).hexdigest() if len(paths) == 1 and os.path.isfile(paths[0]) else ""
            got = hashlib.sha256(body).hexdigest()
            ok = code == 200 and want and want == got
            print("%s ticket=%s http=%d bytes=%d sha盘=%s sha回=%s disp=%s" % (
                "OK  " if ok else "BAD ", tid, code, len(body), (want or "-")[:16], got[:16], disp[:50]))
            fails += 0 if ok else 1
    else:
        cands = pick_candidates()
        if not cands:
            print("BAD  现役候选为空（库里没有 status=completed 且产物在盘上的单）⇒ 这条判据没东西可验，别当通过")
            return 1
        for tid, tno, _st, tenant, owner, path in cands:
            code, body, disp = dial("http://127.0.0.1:%s/api/tickets/download?id=%s" % (port, tid), owner, secret)
            want = hashlib.sha256(open(path, "rb").read()).hexdigest()
            got = hashlib.sha256(body).hexdigest()
            hit = code == 200 and want == got and len(body) > 200
            print("%s 属主腿 ticket=%s(%s) uid=%s http=%d bytes=%d/%d 魔数=%s disp=%s" % (
                "OK  " if hit else "BAD ", tid, tno, owner, code, len(body),
                os.path.getsize(path), body[:4].hex(), disp[:46]))
            fails += 0 if hit else 1
            # 隔离反向腿：换**别家租户**的身份打同一个 id，必须拿不到字节（也不能泄露存在性）
            other = pick_foreign_user(tenant)
            if other:
                c2, b2, _d2 = dial("http://127.0.0.1:%s/api/tickets/download?id=%s" % (port, tid), other, secret)
                blocked = (c2 in (403, 404)) and hashlib.sha256(b2).hexdigest() != want
                print("%s 隔离腿 ticket=%s 外来uid=%s http=%d bytes=%d" % (
                    "OK  " if blocked else "BAD ", tid, other, c2, len(b2)))
                fails += 0 if blocked else 1
            break  # 一条正向＋一条反向足够闭环，别整批打生产
    print("TICKETDL_FAIL=%d" % fails)
    if fails == 0:
        print("TICKETDL_ALL_OK=1")
        return 0
    return 1


if __name__ == "__main__":
    sys.exit(main())
