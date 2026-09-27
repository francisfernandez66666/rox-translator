#!/usr/bin/env python3
# ============================================================================
# scripts/i18n/insert_cmp_keys_20260928.py — 〇-X #55：官网「比价与算价」页 39 键补进十份 locale
#
# 背景：新增 panels/cost.ts（`cmp.*` 39 键，zh/en 成对）后，ALL_KEYS 从 2931 涨到 2970，
# 而 AGENTS §一·5 的 12 语种口径要求 locales/*.ts **逐键全量覆盖**
# （locales.core.test.ts 以 `Object.keys(dict).length === ALL_KEYS.length` 红灯拦截）。
# 本脚本负责剩下十语种那一半：把译文按锚点整块插入，可重跑（已存在的键跳过）。
#
# 插入位置：第一个 `common.` 键所在行之前。为什么是这里而不是「按 ASCII 键序插」：
# 现 locale 文件是**分段近似有序**（admin/agreements/alerts/…/land/lang/login…，尾部还有
# 2026-09 批次追加的乱序块），add_gate_keys.py 的 insert_sorted 依赖全序、在这里会
# 「找不到插入位置」直接 sys.exit；整块插到 chat. 与 common. 之间既保持字母序观感，
# 也让一次批次的 39 行连续可读（diff 一屏看完）。锚点缺失即拒绝盲插。
#
# 译文来源：/tmp/cmp_<locale>.json（2026-09-28 由五个并行子代理各写两语种，
# 主代理逐键机器校验后才落盘）。校验口径三条，脚本末尾再跑一遍兜底：
#   ① 键集与 panels/cost.ts 的 en 完全一致；
#   ② 每键占位符集合与 en 逐键相等（{points} {unit} {pct} {mode} {v} {p} {lo} {hi} {d}）——
#      占位符漏掉就是运行时界面出现裸 {points}，多出来就是替换不上；
#   ③ 值内不得有 ASCII 双引号/反斜杠/换行（locale 用双引号成对，混进去直接 TS 语法崩，
#      这是本仓 locale 批量补译历史上真炸过的形态）。
#
# 跑完必须验证（禁止放宽断言）：
#   cd frontend-react && npx tsc --noEmit && npx vitest run src/i18n/
# ============================================================================
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.abspath(os.path.join(HERE, "..", "..", "frontend-react", "src", "i18n"))
LOCALES = ["ru", "fr", "de", "es", "pt", "ar", "th", "ja", "ko", "zh-hant"]


def key_of(line):
    m = re.match(r'^\s*"([^"]+)"\s*:', line) or re.match(r"^\s*'([^']+)'\s*:", line)
    return m.group(1) if m else None


def placeholders(v):
    return sorted(set(re.findall(r"\{(\w+)\}", v)))


def main():
    # ① 以词典为唯一基准读 en/zh 键集与占位符（不复制粘贴键名，避免脚本与词典脱钩）
    cost = open(os.path.join(SRC, "panels", "cost.ts"), encoding="utf-8").read()
    en_block = cost.split("export const en")[1]
    en = dict(re.findall(r"^\s{2}'([^']+)':\s*'((?:[^'\\]|\\.)*)',\s*$", en_block, re.M))
    if len(en) != 39:
        sys.exit(f"panels/cost.ts 的 en 段键数 {len(en)} != 39，词典形态与脚本假设不符")

    total_added = 0
    for loc in LOCALES:
        path_json = f"/tmp/cmp_{loc}.json"
        if not os.path.exists(path_json):
            sys.exit(f"缺译文文件 {path_json}")
        vals = json.load(open(path_json, encoding="utf-8"))
        # 逐键校验后才写盘（任一不合格整语种不落，避免半块译文进词典）
        if set(vals) != set(en):
            sys.exit(f"{loc}: 键集与 cost.ts 不一致 miss={set(en) - set(vals)} extra={set(vals) - set(en)}")
        for k, v in vals.items():
            if placeholders(v) != placeholders(en[k]):
                sys.exit(f"{loc}:{k} 占位符 {placeholders(v)} != 英文源 {placeholders(en[k])}")
            if '"' in v or "\\" in v or "\n" in v:
                sys.exit(f"{loc}:{k} 值内含 ASCII 双引号/反斜杠/换行，会炸 TS 语法")
            if not v.strip():
                sys.exit(f"{loc}:{k} 空值")

        f = os.path.join(SRC, "locales", f"{loc}.ts")
        lines = open(f, encoding="utf-8").read().split("\n")
        have = {key_of(ln) for ln in lines if key_of(ln)}
        todo = [(k, vals[k]) for k in en if k not in have]  # 按 en 键序，整块连续
        if not todo:
            print(f"  = {loc}.ts 39 键已在位，跳过（可重跑）")
            continue
        pos = next((i for i, ln in enumerate(lines)
                    if (key_of(ln) or "").startswith("common.")), None)
        if pos is None:
            sys.exit(f"{f} 找不到 common. 锚点，拒绝盲插")
        lines[pos:pos] = [f'  "{k}": "{v}",' for k, v in todo]
        open(f, "w", encoding="utf-8").write("\n".join(lines))
        total_added += len(todo)
        print(f"  +{len(todo)} locales/{loc}.ts @ 第 {pos + 1} 行前")

    # ② 落盘后复核：每语种键数必须等于 ALL_KEYS 长度（locales.core.test.ts 的同一条等式）
    for loc in LOCALES:
        f = os.path.join(SRC, "locales", f"{loc}.ts")
        n = sum(1 for ln in open(f, encoding="utf-8") if key_of(ln))
        print(f"  check {loc}.ts 键数 = {n}")
    print(f"完成：新增 {total_added} 行（期望 39 × 待补语种数）")


if __name__ == "__main__":
    main()
