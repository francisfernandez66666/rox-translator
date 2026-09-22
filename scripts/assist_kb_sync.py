#!/usr/bin/env python3
# ============ scripts/assist_kb_sync.py · 职责说明 ============
# AI 顾问知识库（assist 侧 kb_entries）「seed → 生产库」差异同步工具。
#
# 为什么需要它：assist 的 seed.json 只在**首次启动且表为空**时灌库（见
# internal/assist/store/store.go 的 seed 装载与《部署指南》「seed 仅首启空表灌入，
# 不会覆盖线上编辑」）。因此仓库里后续对 kb 条目关键词/文案的修订（如 〇-XLVI
# 扩 languages 关键词、〇-XLVIII 给 billing-points 补「按字/字数/多少钱一个字」并
# 去掉 what-is 的超泛关键词「是什么」）**永远不会自动到达生产库**——线上仍是旧词，
# 访客问「多少钱一个字」就漏接。这一步此前一直被记为「待人工执行」的遗留项。
#
# 口径：
#   - 默认只读比对（不改任何东西），列出三类差异：seed 有/生产无、两边同 key 但字段不同、
#     生产有/seed 无。
#   - --apply 才写库；写之前必定远端 .backup 一份（WAL 安全的 sqlite3 .backup，不是 cp），
#     只 UPDATE 已有行 / INSERT 缺失行，**绝不 DELETE**（生产可能有管理台在线新增的条目）。
#   - 依赖 SSH 免密 + 服务器上的 sqlite3 CLI；SQL 在本机生成后通过 stdin 送进去执行，
#     不在服务器上落临时脚本文件。
#
# 用法：
#   python3 scripts/assist_kb_sync.py --host root@1.2.3.4                 # 只比对
#   python3 scripts/assist_kb_sync.py --host root@1.2.3.4 --apply         # 比对后同步
#   （host 也可用环境变量 LC_ASSIST_HOST 提供；--db 覆盖默认的 /opt/ai-assist/data/assist.db）
# =============================================
import argparse
import json
import os
import shlex
import subprocess
import sys
import time

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SEED_PATH = os.path.join(REPO_ROOT, "backend-go/internal/assist/seed/seed.json")
DEFAULT_DB = "/opt/ai-assist/data/assist.db"
# 比对口径：这些字段决定一条知识能否被关键词/向量检索命中，seed 为准
CMP_FIELDS = ["category", "title", "content", "keywords", "link_keys", "priority", "enabled"]


def run(cmd, *, stdin=None):
    """执行命令并返回 (returncode, stdout, stderr)；统一 text 模式，便于直接打印。"""
    p = subprocess.run(cmd, input=stdin, capture_output=True, text=True)
    return p.returncode, p.stdout, p.stderr


def fetch_remote_kb(host, db):
    """远端只读导出 kb_entries（-json 逐行数组），返回 {key: row}。"""
    # ★ 必须 shlex.quote：ssh 会把参数拼成一条命令交给远端 shell 解析，
    #   不加引号则 SQL 在第一个空格处被劈开，sqlite3 只收到 "select" → 「incomplete input」假错。
    sql = ("select " + ",".join(["key"] + CMP_FIELDS) + " from kb_entries order by id;")
    rc, out, err = run(["ssh", host, "sqlite3", "-readonly", "-json", db, shlex.quote(sql)])
    if rc != 0:
        sys.exit(f"❌ 读取生产 kb_entries 失败（rc={rc}）：{err.strip()}\n"
                 f"   先确认 SSH 免密可用、sqlite3 在 PATH、库路径 {db} 正确（WAL 态可直读）。")
    rows = json.loads(out or "[]")
    return {r["key"]: r for r in rows}


def q(v):
    """SQL 字面量：整数直出，字符串按 SQLite 规则翻倍单引号。"""
    if isinstance(v, bool):
        return "1" if v else "0"
    if isinstance(v, (int, float)):
        return str(int(v))
    return "'" + str(v).replace("'", "''") + "'"


def build_sql(seed_row, remote_row):
    """生成单条 kb 条目的同步语句：有则 UPDATE（并刷新 updated_at 以失效向量索引指纹），无则 INSERT。

    ★ updated_at 必须一起改：internal/assist/engine/vector.go 的向量索引指纹是
      「embed_model + 每行 key+updated_at」，不落时间戳则改了关键词也不会重建索引，
      线上会继续用旧向量——这正是「同步了却没生效」的隐藏坑。
    """
    cols = ["key"] + CMP_FIELDS
    if remote_row is None:
        return ("INSERT INTO kb_entries (key," + ",".join(CMP_FIELDS) + ",updated_at) VALUES ("
                + ",".join(q(seed_row.get(c, "")) for c in cols) + ",CURRENT_TIMESTAMP);")
    sets = ",".join(f"{c}={q(seed_row.get(c, ''))}" for c in CMP_FIELDS)
    return f"UPDATE kb_entries SET {sets},updated_at=CURRENT_TIMESTAMP WHERE key={q(seed_row['key'])};"


def main():
    ap = argparse.ArgumentParser(description="assist kb_entries：seed → 生产库差异同步")
    ap.add_argument("--host", default=os.environ.get("LC_ASSIST_HOST", ""),
                    help="SSH 目标，如 root@43.1.2.3（或环境变量 LC_ASSIST_HOST）")
    ap.add_argument("--db", default=DEFAULT_DB, help=f"生产 assist.db 路径（默认 {DEFAULT_DB}）")
    ap.add_argument("--apply", action="store_true", help="真正写库（默认只读比对）")
    a = ap.parse_args()
    if not a.host:
        sys.exit("❌ 缺 --host（或 LC_ASSIST_HOST）；本脚本只面向远端生产库，不做本地库操作。")

    with open(SEED_PATH, encoding="utf-8") as f:
        seed = {e["key"]: e for e in json.load(f)["kb"]}
    remote = fetch_remote_kb(a.host, a.db)

    todo, only_prod = [], sorted(set(remote) - set(seed))
    for key, s in seed.items():
        r = remote.get(key)
        if r is None:
            todo.append((key, ["<整条缺失>"], s, None))
            continue
        # 空白与引号差异不算漂移（管理台编辑器可能改动首尾空格）
        drift = [c for c in CMP_FIELDS if str(s.get(c, "")).strip() != str(r.get(c, "")).strip()]
        if drift:
            todo.append((key, drift, s, r))

    print(f"seed 条目 {len(seed)} / 生产条目 {len(remote)} / 需同步 {len(todo)}")
    for key, drift, s, r in todo:
        print(f"  · {key}: {', '.join(drift)}")
        for c in drift:
            if c == "content":
                print(f"      {c}: seed {len(str(s.get(c,'')))} 字 / 生产 {len(str(r.get(c,'')) if r else '')} 字（内容漂移，取 seed）")
            else:
                print(f"      {c}:\n        seed : {s.get(c,'')}\n        生产 : {(r or {}).get(c,'<无此条>')}")
    if only_prod:
        print(f"  ⚠️ 生产独有（本脚本不动、也不删）：{', '.join(only_prod)}")
    if not todo:
        print("✅ 无漂移，生产关键词已与 seed 一致。")
        return
    if not a.apply:
        print("（只读模式，未改动任何数据；加 --apply 执行同步）")
        return

    ts = time.strftime("%Y%m%d_%H%M%S")
    bak = f"{a.db}.bak.{ts}"
    # ★ dot-command（.backup）只能走 stdin 或 .read：把它当命令行 SQL 参数传给 sqlite3 CLI
    #   会报「missing FILENAME argument」，因为 CLI 不解析参数里的点命令。
    rc, _, err = run(["ssh", a.host, "sqlite3", a.db], stdin=f".backup '{bak}'\n")
    if rc != 0:
        sys.exit(f"❌ 备份失败，未做任何改动：{err.strip()}")
    # 备份必须真的落盘且非空，否则一旦同步出问题就无从回滚。
    # ★ 这里不能再套 shlex.quote：ssh 会把参数交给远端 shell 解析，整串再被引号包成一个词
    #   时 bash 会把它当「命令名」→ No such file or directory 假阴性。
    rc, out, _ = run(["ssh", a.host, f"test -s '{bak}' && echo BACKUP_OK"])
    if "BACKUP_OK" not in out:
        sys.exit(f"❌ 备份文件不可用（{bak} 不存在或为空），未做任何改动。")
    print(f"已备份 → {a.host}:{bak}")

    sql = "BEGIN;\n" + "".join(build_sql(s, r) + "\n" for _, _, s, r in todo) + "COMMIT;\n"
    rc, out, err = run(["ssh", a.host, "sqlite3", a.db], stdin=sql)
    if rc != 0:
        sys.exit(f"❌ 同步失败（事务已回滚，备份仍在 {bak}）：{err.strip()}")

    after = fetch_remote_kb(a.host, a.db)
    left = [k for k, _, s, _ in todo
            if any(str(s.get(c, "")).strip() != str((after.get(k) or {}).get(c, "")).strip() for c in CMP_FIELDS)]
    print(("✅ 同步完成并通过复查" if not left else f"❌ 仍有 {len(left)} 条未生效：{left}") +
          f"（向量索引会在下一次对话按 updated_at 指纹自动重建，无需重启 ai-assist）")
    sys.exit(0 if not left else 1)


if __name__ == "__main__":
    main()
