#!/usr/bin/zsh
# scripts/xbot/_run_sim7d.sh — P3 模拟：拆分 7 天 → 逐条过闸门 → 渲染 21 张图 → 出预览页
# 只在本地跑，不接触 X API，不发任何真推。
set -euo pipefail

BASE="$(cd "$(dirname "$0")" && pwd)"
PY=/Users/zhangzifei/.workbuddy/binaries/python/versions/3.13.12/bin/python3
NODE=/Users/zhangzifei/.workbuddy/binaries/node/versions/22.22.2-6/bin/node
export NODE_PATH=/Users/zhangzifei/.workbuddy/binaries/node/workspace/node_modules
export PATH="$(dirname $NODE):$PATH"

cd "$BASE"
echo "== 1) 拆分 7 天 pending 队列 =="
$PY - <<'PYEOF'
import json, pathlib
base = pathlib.Path.cwd()          # stdin 执行的脚本里 __file__ 不存在，用 cwd（脚本开头已 cd）
src = json.loads((base / "out/sim7d.json").read_text(encoding="utf-8"))
for d in src["days"]:
    out = base / "out/pending" / f"{d['date']}.json"
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps({"date": d["date"], "day": d["day"], "items": d["items"]},
                              ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"  {d['date']}  {len(d['items'])} 条")
PYEOF

echo "== 2) 三道闸门全检 =="
$PY - <<'PYEOF'
import json, pathlib, sys
sys.path.insert(0, str(pathlib.Path.cwd()))   # 同上：stdin 执行无 __file__
import guard
base = pathlib.Path.cwd()
led, fails, total = [], 0, 0
for f in sorted((base / "out/pending").glob("*.json")):
    data = json.loads(f.read_text(encoding="utf-8"))
    for it in data["items"]:
        total += 1
        r = guard.check(it["text"], topic_id=it.get("topic_id"), ledger=led)
        it.setdefault("checks", {})
        it["checks"].update({"compliance": r.compliance,
                             "weighted_len": r.weighted_len,
                             "max_similarity": r.max_similarity,
                             "ok": r.ok})
        if r.ok:
            led.append({"ts": data["date"], "text": it["text"], "topic_id": it.get("topic_id")})
            print(f"  PASS {data['date']} #{it['seq']} [{it['topic_id']}] 加权 {r.weighted_len} 相似 {r.max_similarity}")
        else:
            fails += 1
            print(f"  FAIL {data['date']} #{it['seq']} [{it['topic_id']}] → {'; '.join(r.reasons)}")
    f.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
print(f"\n合计 {total} 条，通过 {total-fails}，拦下 {fails}")
PYEOF

echo "== 3) 渲染 21 张配图 =="
for f in out/pending/*.json; do
  d="$(basename "$f" .json)"
  $PY render.py --date "$d" | sed 's/^/  /'
done

echo "== 4) 生成预览页 =="
$PY _make_preview.py
echo "完成：品牌资产/xbot-模拟7天/预览.html"