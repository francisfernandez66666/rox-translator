#!/usr/bin/env python3
# scripts/uat/lint_uat_quotes.py — UAT 脚本引号陷阱静态闸门（2026-09-21 新增）
#
# 要防的失效模式（当天实测踩到，会让断言「永远绿」或「永远红」而不报任何 shell 错误）：
#
#   ck X 'want' "$(post "$H" "{\"a\":1,\"b\":2}" /api/x)"   ← 错
#
# bash 在「双引号包裹的命令替换」内部会重新解析引号，此时 `{\"a\":1,\"b\":2}` 中的大括号
# 不再受引用保护，触发 **brace expansion**：一个参数被按逗号拆成两个（`"a":1` 与 `"b":2}`），
# curl 于是发出残缺 body，服务端只能回「参数格式错误」。若断言写的是宽松模式（如 '"success":false'），
# 这条闸门就永久命中残缺响应而无人察觉——正是 AGENTS.md 第七条禁止的「静默通过」。
#
# 正确写法（本仓 api_uat_txn.sh 第 72 行既定口径）：复杂 body 先存变量再断言。
#   R=$(post "$H" "{\"a\":1,\"b\":2}" /api/x); ck X 'want' "$R"
#
# 判定：任一 `"$(post …)"` / `"$(get …)"` / `"$(curl …)"` 调用点，其内部参数字符串同时含
# `\"`（内嵌转义引号）与位于 `{…}` 内的逗号 → 红灯。单引号 body（'{"a":1}'）安全，不报。
import re
import sys
from pathlib import Path

CALL = re.compile(r'"\$\((post|get|curl)\b')


def offending(line: str) -> bool:
    for m in CALL.finditer(line):
        span = line[m.start():]
        # 逐个候选参数：以 "{" 开头的双引号串（内嵌 \" 才可能被大括号展开）
        for am in re.finditer(r'"\{(?:\\.|[^"])*"', span):
            arg = am.group(0)
            if '\\"' in arg and ',' in arg:
                return True
    return False


def main() -> int:
    root = Path(sys.argv[1] if len(sys.argv) > 1 else 'scripts/uat')
    bad, checked = [], 0
    for f in sorted(root.glob('*.sh')):
        for i, line in enumerate(f.read_text(encoding='utf-8').splitlines(), 1):
            if line.strip().startswith('#'):
                continue
            checked += 1
            if offending(line):
                bad.append(f'{f}:{i}: {line.strip()[:120]}')
    if bad:
        print('❌ UAT 引号闸门：以下调用点的 body 会被 bash 大括号展开截断，必须改为「先存变量再断言」：')
        for b in bad:
            print('   ' + b)
        return 1
    print(f'OK|uat-quotes（扫描 {checked} 行，0 处内嵌双引号 body 受大括号展开影响）')
    return 0


if __name__ == '__main__':
    sys.exit(main())
