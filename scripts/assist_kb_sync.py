#!/usr/bin/env python3
# ============ scripts/assist_kb_sync.py · 职责说明 ============
# AI 顾问（assist 服务）seed 文案 → 生产库的差异同步工具。
#
# 为什么需要它：assist 的 seed.json 只在**首次启动且对应表为空**时灌库（见
# internal/assist/store/store.go 的 seed 装载与《部署指南》「seed 仅首启空表灌入，
# 不会覆盖线上编辑」）。因此仓库里后续对文案/关键词的修订（如 〇-XLVI 扩 languages
# 关键词、〇-XLVIII 给 billing-points 补「按字/字数/多少钱一个字」，★ 082x 把
# 「上下文审校/风格指令/积分永久有效」这类**系统根本没有的能力**从文案里摘掉）
# **永远不会自动到达生产库**——线上仍是旧词，访客问「多少钱一个字」就漏接，
# 而超承诺那条更糟：话术直配与流程**不经过模型**，库里那句假话会原样发给客户。
#
# ★ 082x 起同步面从 kb_entries 扩到四张表（scripts / flows / feature_links）：
#   只修 kb 是不够的——price-how 话术、onboard/recharge 流程、对话翻译功能卡的描述
#   同样带着「积分永久有效」「支持上下文与风格指令」，而这三条路径都不经模型，
#   【承诺边界】那段提示词管不到它们（判据见 internal/assist/seed/seed_gate_test.go 文件头）。
#
# 口径：
#   - 默认只读比对（不改任何东西），按表列出三类差异：seed 有/生产无、
#     两边同 key 但字段不同、生产有/seed 无。
#   - --apply 才写库；写之前必定远端 .backup 一份（WAL 安全的 sqlite3 .backup，不是 cp），
#     只 UPDATE 已有行 / INSERT 缺失行，**绝不 DELETE**（生产可能有管理台在线新增的条目）。
#   - 与 kb 同一条「seed 为准」的策略对四张表一致：线上若被管理台在线改过，
#     这里会用 seed 覆掉——差异清单会逐字段打印 seed/生产两个值，先看再 --apply，
#     确认没有想保留的在线修订。备份文件是最后一条退路。
#   - 依赖 SSH 免密 + 服务器上的 sqlite3 CLI；SQL 在本机生成后通过 stdin 送进去执行，
#     不在服务器上落临时脚本文件。
#
# 用法（★ 写库只有 --apply 一条路，其余一律只读）：
#   python3 scripts/assist_kb_sync.py --host root@1.2.3.4                    # 四张表只比对
#   python3 scripts/assist_kb_sync.py --host root@1.2.3.4 --dry-run          # 同上，显式声明只读
#   python3 scripts/assist_kb_sync.py --host root@1.2.3.4 --apply            # 比对后同步
#   python3 scripts/assist_kb_sync.py --host root@1.2.3.4 --only kb          # 只动 kb_entries
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

# 每张表的同步口径：seed 段落名 → (库表名, 比对/覆写字段, 是否要刷新 updated_at)
#
# ★ 为什么只有 kb_entries 刷 updated_at：internal/assist/engine/vector.go 的向量索引指纹是
#   「embed_model + 每行 key+updated_at」，不落时间戳则改了关键词也不会重建索引，
#   线上会继续用旧向量——这正是「同步了却没生效」的隐藏坑。另外三张表按 key/关键词**精确匹配**
#   直出，没有向量索引，表里也没有 updated_at 列（盲写会直接 no such column）。
TABLES = [
    {
        "name": "kb",
        "table": "kb_entries",
        "fields": ["category", "title", "content", "keywords", "link_keys", "priority", "enabled"],
        "touch_updated_at": True,
    },
    {
        "name": "scripts",
        "table": "scripts",
        "fields": ["stype", "title", "keywords", "link_keys", "content", "priority", "enabled"],
        "touch_updated_at": False,
    },
    {
        "name": "flows",
        "table": "flows",
        # steps_json 在 seed 里是**字符串**（与库列同形），不是数组；比对按整串比
        "fields": ["name", "description", "trigger_keywords", "steps_json", "enabled"],
        "touch_updated_at": False,
    },
    {
        "name": "features",
        "table": "feature_links",
        "fields": ["name", "description", "url", "ftype", "icon", "sort", "enabled"],
        "touch_updated_at": False,
    },
]


def run(cmd, *, stdin=None):
    """执行命令并返回 (returncode, stdout, stderr)；统一 text 模式，便于直接打印。"""
    p = subprocess.run(cmd, input=stdin, capture_output=True, text=True)
    return p.returncode, p.stdout, p.stderr


def fetch_remote(host, db, spec):
    """远端只读导出某张表（-json 逐行数组），返回 {key: row}。"""
    # ★ 必须 shlex.quote：ssh 会把参数拼成一条命令交给远端 shell 解析，
    #   不加引号则 SQL 在第一个空格处被劈开，sqlite3 只收到 "select" → 「incomplete input」假错。
    cols = spec["fields"]
    sql = "select " + ",".join(["key"] + cols) + f" from {spec['table']} order by id;"
    rc, out, err = run(["ssh", host, "sqlite3", "-readonly", "-json", db, shlex.quote(sql)])
    if rc != 0:
        sys.exit(f"❌ 读取生产 {spec['table']} 失败（rc={rc}）：{err.strip()}\n"
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


def build_sql(seed_row, remote_row, spec):
    """生成单条同步语句：有则 UPDATE（kb 顺带刷新 updated_at 以失效向量索引指纹），无则 INSERT。"""
    cols = ["key"] + spec["fields"]
    vals = ",".join(q(seed_row.get(c, "")) for c in cols)
    if remote_row is None:
        tail = ",updated_at" if spec["touch_updated_at"] else ""
        tv = ",CURRENT_TIMESTAMP" if spec["touch_updated_at"] else ""
        return f"INSERT INTO {spec['table']} (key,{','.join(spec['fields'])}{tail}) VALUES ({vals}{tv});"
    sets = ",".join(f"{c}={q(seed_row.get(c, ''))}" for c in spec["fields"])
    if spec["touch_updated_at"]:
        sets += ",updated_at=CURRENT_TIMESTAMP"
    return f"UPDATE {spec['table']} SET {sets} WHERE key={q(seed_row['key'])};"


def diff_table(spec, seed_rows, remote_rows):
    """按 key 比 seed 与生产，返回 (待同步 [(key, 漂移字段, seed, prod)], 生产独有 key 列表)。"""
    todo = []
    for key, s in seed_rows.items():
        r = remote_rows.get(key)
        if r is None:
            todo.append((key, ["<整条缺失>"], s, None))
            continue
        # 空白差异不算漂移（管理台编辑器可能改动首尾空格）；steps_json 这类 JSON 串
        # 还要求**语义等值**：库里可能是 compact 形态，按字符串比会天天报漂移。
        drift = []
        for c in spec["fields"]:
            sv, pv = s.get(c, ""), (r or {}).get(c, "")
            if json_equivalent(c, sv) and json_equivalent(c, pv):
                if json.loads(sv or "null") != json.loads(pv or "null"):
                    drift.append(c)
            elif str(sv).strip() != str(pv).strip():
                drift.append(c)
        if drift:
            todo.append((key, drift, s, r))
    only_prod = sorted(set(remote_rows) - set(seed_rows))
    return todo, only_prod


def json_equivalent(field, v):
    """该字段是不是「能按 JSON 解析的串」——只有 steps_json 会命中。"""
    if field != "steps_json" or not isinstance(v, str) or not v.strip():
        return False
    try:
        json.loads(v)
        return True
    except Exception:
        return False


def main():
    ap = argparse.ArgumentParser(description="assist seed 文案 → 生产库差异同步（kb/话术/流程/功能入口四张表）")
    ap.add_argument("--host", default=os.environ.get("LC_ASSIST_HOST", ""),
                    help="SSH 目标，如 root@43.1.2.3（或环境变量 LC_ASSIST_HOST）")
    ap.add_argument("--db", default=DEFAULT_DB, help=f"生产 assist.db 路径（默认 {DEFAULT_DB}）")
    ap.add_argument("--apply", action="store_true", help="真正写库（默认只读比对）")
    # ★ 与 --apply 互斥的显式只读档：默认行为本来就是只读，但「不带参数＝只读」这件事
    #   在命令行上看不出来，人和自动化审计都容易把一条干跑当成开写。写库必须显式 --apply。
    ap.add_argument("--dry-run", dest="dry_run", action="store_true",
                    help="显式只读比对（与 --apply 互斥；不加任何参数时同样只读）")
    ap.add_argument("--only", default="",
                    help="只处理指定段落，逗号分隔（kb,scripts,flows,features）；默认全四张表")
    a = ap.parse_args()
    if a.apply and a.dry_run:
        sys.exit("❌ --apply 与 --dry-run 互斥：要么只看差异，要么开写，别同时给。")
    if not a.host:
        sys.exit("❌ 缺 --host（或 LC_ASSIST_HOST）；本脚本只面向远端生产库，不做本地库操作。")

    picked = [s.strip() for s in a.only.split(",") if s.strip()]
    specs = [t for t in TABLES if not picked or t["name"] in picked]
    if picked and len(specs) != len(picked):
        sys.exit(f"❌ --only 里有不认识的段落：{picked}（可选 {[t['name'] for t in TABLES]}）")

    with open(SEED_PATH, encoding="utf-8") as f:
        seed_doc = json.load(f)

    plan = []  # [(spec, todo)]
    for spec in specs:
        seed_rows = {e["key"]: e for e in seed_doc[spec["name"]]}
        remote = fetch_remote(a.host, a.db, spec)
        todo, only_prod = diff_table(spec, seed_rows, remote)
        print(f"[{spec['table']}] seed {len(seed_rows)} / 生产 {len(remote)} / 需同步 {len(todo)}")
        for key, drift, s, r in todo:
            print(f"  · {key}: {', '.join(drift)}")
            for c in drift:
                if c == "content" or c == "steps_json":
                    print(f"      {c}: seed {len(str(s.get(c, '')))} 字 / 生产 "
                          f"{len(str(r.get(c, '')) if r else '')} 字（内容漂移，取 seed）")
                else:
                    print(f"      {c}:\n        seed : {s.get(c, '')}\n        生产 : {(r or {}).get(c, '<无此条>')}")
        if only_prod:
            print(f"  ⚠️ 生产独有（本脚本不动、也不删）：{', '.join(only_prod)}")
        plan.append((spec, todo))

    total = sum(len(t) for _, t in plan)
    if total == 0:
        print("✅ 无漂移，生产文案已与 seed 一致。")
        return
    if not a.apply:
        print(f"（只读模式，未改动任何数据；共 {total} 条待同步，加 --apply 执行）")
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

    sql = "BEGIN;\n"
    for spec, todo in plan:
        sql += "".join(build_sql(s, r, spec) + "\n" for _, _, s, r in todo)
    sql += "COMMIT;\n"
    rc, out, err = run(["ssh", a.host, "sqlite3", a.db], stdin=sql)
    if rc != 0:
        sys.exit(f"❌ 同步失败（事务已回滚，备份仍在 {bak}）：{err.strip()}")

    # 复查：逐表回读，漂移必须清零
    left_total = 0
    for spec, todo in plan:
        after = fetch_remote(a.host, a.db, spec)
        left = []
        for key, _, s, _ in todo:
            r = after.get(key) or {}
            for c in spec["fields"]:
                sv, pv = str(s.get(c, "")).strip(), str(r.get(c, "")).strip()
                if json_equivalent(c, sv) and json_equivalent(c, pv):
                    if json.loads(sv) != json.loads(pv or "null"):
                        left.append(f"{spec['table']}/{key}.{c}")
                elif sv != pv:
                    left.append(f"{spec['table']}/{key}.{c}")
        left_total += len(left)
        if left:
            print(f"  ❌ {spec['table']} 仍有 {len(left)} 处未生效：{', '.join(left[:8])}")
    if left_total:
        sys.exit(1)
    print("✅ 同步完成并通过复查"
          "（kb 的向量索引会在下一次对话按 updated_at 指纹自动重建，话术/流程/功能入口按库直出，均无需重启 ai-assist）")


if __name__ == "__main__":
    main()
