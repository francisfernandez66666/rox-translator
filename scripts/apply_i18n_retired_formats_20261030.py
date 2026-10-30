#!/usr/bin/env python3
# ============ scripts/apply_i18n_retired_formats_20261030.py ============
# 组1（格式收缩）的 12 语种词典批：
#   ① 从 `tk.fileHint` 那句里摘掉已撤出准入的五种格式名（srt/vtt/json/yaml/yml）；
#   ② 在 `tr.fileFmtText` 之前插入新键 `tr.fileFmtPlainText`（12 语种各一份译文）。
#
# 为什么用脚本而不是手改 12 个文件：手改必漏一份，漏的那一份由 locales.core.test.ts
# 的 ALL_KEYS 全量口径当场红——但红的是"缺键"，不是"某个语种文案还承诺着 .srt"。
# 所以①这一半**必须**由脚本逐份核对（下面每条都会打印，任何一份不满足即整体中止不落盘）。
#
# 键序口径：插在 tr.fileFmtText 之前（与 dicts.zh/en 同一相对位置），
# 免得十份 locale 与 zh/en 的键序各自漂（parity 类锁按键集比，但按序比的那族会红）。
import re
import sys
import os

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'frontend-react', 'src', 'i18n')
RETIRED = ['srt', 'vtt', 'json', 'yaml', 'yml']
KEEP_MUST = ['docx', 'mp3']  # 摘完之后必须还在的名字（正向对照，防"整段被删空"式假绿）

FILES = ['dicts.zh.ts', 'dicts.en.ts'] + ['locales/%s.ts' % c for c in
        ['de', 'ar', 'es', 'fr', 'th', 'ru', 'zh-hant', 'pt', 'ko', 'ja']]

# 新键的 12 份完整文案（必须各含一个 {name} 占位符——缺了就等于把客户上传的文件名吞掉）。
# ★ 中文那一份的冒号之后那半句必须与 Go 侧 `fileproc.PlainTextContainerHint` **逐字相同**：
#   跨语言等值由 translateFormats.lock.test.ts 的 P7 现读比对（写死两份迟早打架）。
ZH_CORE = '这一类是纯文本容器，请直接粘贴内容到文本翻译'
VALUES = {
    'dicts.zh.ts': '{name}：' + ZH_CORE,
    'dicts.en.ts': '{name}: this is a plain-text container - please paste its content into text translation',
    'locales/zh-hant.ts': '{name}：這一類是純文本容器，請直接貼上內容到文本翻譯',
    'locales/ja.ts': '{name}：これはプレーンテキストのコンテナです。内容をそのままテキスト翻訳に貼り付けてください',
    'locales/ko.ts': '{name}: 일반 텍스트 컨테이너입니다. 내용을 텍스트 번역에 그대로 붙여넣어 주세요',
    'locales/de.ts': '{name}: dies ist ein reiner Textcontainer - bitte f\u00fcgen Sie den Inhalt direkt in die Text\u00fcbersetzung ein',
    'locales/fr.ts': '{name} : c\u2019est un conteneur de texte brut, veuillez coller son contenu directement dans la traduction de texte',
    'locales/es.ts': '{name}: es un contenedor de texto sin formato; pegue su contenido directamente en la traducci\u00f3n de texto',
    'locales/pt.ts': '{name}: \u00e9 um cont\u00eainer de texto simples; cole o conte\u00fado diretamente na tradu\u00e7\u00e3o de texto',
    'locales/ru.ts': '{name}: \u044d\u0442\u043e \u043e\u0431\u044b\u0447\u043d\u044b\u0439 \u0442\u0435\u043a\u0441\u0442\u043e\u0432\u044b\u0439 \u043a\u043e\u043d\u0442\u0435\u0439\u043d\u0435\u0440 \u2014 \u0432\u0441\u0442\u0430\u0432\u044c\u0442\u0435 \u0435\u0433\u043e \u0441\u043e\u0434\u0435\u0440\u0436\u0438\u043c\u043e\u0435 \u043f\u0440\u044f\u043c\u043e \u0432 \u0442\u0435\u043a\u0441\u0442\u043e\u0432\u044b\u0439 \u043f\u0435\u0440\u0435\u0432\u043e\u0434',
    'locales/th.ts': '{name}: \u0e1c\u0e39\u0e01\u0e2d\u0e32\u0e19\u0e02\u0e49\u0e2d\u0e19\u0e19\u0e35\u0e49\u0e40\u0e1b\u0e47\u0e19\u0e15\u0e31\u0e27\u0e02\u0e49\u0e2d\u0e04\u0e27\u0e32\u0e21\u0e2d\u0e32\u0e28\u0e32\u0e2d\u0e15\u0e30\u0e2d\u0e01 \u0e42\u0e1b\u0e01\u0e40\u0e19\u0e37\u0e49\u0e2d\u0e2b\u0e31\u0e14\u0e23\u0e32\u0e22\u0e02\u0e49\u0e2d\u0e04\u0e27\u0e32\u0e21\u0e40\u0e02\u0e49\u0e32\u0e43\u0e19\u0e01\u0e32\u0e23\u0e41\u0e1b\u0e25\u0e07\u0e2d\u0e31\u0e19\u0e04\u0e23\u0e30\u0e17\u0e31\u0e19\u0e40\u0e25\u0e22',
    'locales/ar.ts': '{name}: \u0647\u0630\u0627 \u062d\u0627\u0648\u064a\u0629 \u0646\u0635 \u0639\u0627\u062f\u064a\u060c \u064a\u0631\u062c\u0649 \u0644\u0635\u0642 \u0627\u0644\u0645\u062d\u062a\u0648\u0649 \u0645\u0628\u0627\u0634\u0631\u0629 \u0641\u064a \u062a\u0631\u062c\u0645\u0629 \u0627\u0644\u0646\u0635',
}

HINT_RE = re.compile(r"""(['"])tk\.fileHint\1\s*:\s*(['"])((?:\\.|(?!\2)[^\\])*)\2""")
NEIGHBOR_RE = re.compile(r'^(\s*)(["\'])tr\.fileFmtText\2(\s*):')


def strip_retired(line: str, label: str) -> str:
    m = HINT_RE.search(line)
    if not m:
        raise SystemExit('%s: 取不到 tk.fileHint，中止（不许把"没找到"当成"不用改"）' % label)
    quote, val = m.group(2), m.group(3)
    # 按 '/' 切token 时**不**先剥导语：各语种的导语标点不一（zh 是全角冒号、de 是 "Klicken ...:"），
    # 剥法错一种就会把"摘掉五个"算成"摘掉零个"。首 token 带着导语也无妨——前后两次用同一把尺子数。
    names = [t.strip() for t in val.split('/') if t.strip()]
    head_len = len(names)
    new_val = val
    for r in RETIRED:
        new_val, n = re.subn(r'\b%s\s*/\s*' % r, '', new_val, count=1)
        if n != 1:
            raise SystemExit('%s: 文案里没摘掉 %s（命中 %d 次）⇒ 写法与本脚本假设不同，中止' % (label, r, n))
    got = [t.strip() for t in new_val.split('/') if t.strip()]
    if len(got) != head_len - len(RETIRED):
        raise SystemExit('%s: 摘完剩 %d 个格式名，预期 %d（%d-%d）⇒ 分隔符假设不成立，中止'
                         % (label, len(got), head_len - len(RETIRED), head_len, len(RETIRED)))
    for r in RETIRED:
        if re.search(r'\b%s\b' % r, new_val):
            raise SystemExit('%s: 摘完仍剩 %s 出现在文案里：%s' % (label, r, new_val))
    for k in KEEP_MUST + ['pdf', 'md']:
        # 边界只认"非字母数字"（导语与第一个格式名之间可能是全角冒号，不是空格/斜杠）
        if not re.search(r'(?<![A-Za-z0-9])%s(?![A-Za-z0-9])' % k, new_val):
            raise SystemExit('%s: 摘除后连 %s 都没了（把整段删空了），中止' % (label, k))
    return line[:m.start()] + '%stk.fileHint%s: %s%s%s' % (m.group(1), m.group(1), quote, new_val, quote) + line[m.end():]


def insert_key(text: str, label: str, value: str) -> str:
    if 'tr.fileFmtPlainText' in text:
        return text  # 幂等：重跑不重复插
    if '{name}' not in value:
        raise SystemExit('%s: 新文案缺 {name} 占位符，中止' % label)
    lines = text.split('\n')
    out = []
    hit = 0
    for ln in lines:
        m = NEIGHBOR_RE.match(ln)
        if m:
            q = m.group(2)
            indent = m.group(1)
            out.append('%s%str.fileFmtPlainText%s: %s%s%s,' % (indent, q, q, q, value, q))
            hit += 1
        out.append(ln)
    if hit != 1:
        raise SystemExit('%s: 锚 tr.fileFmtText 命中 %d 次（应为 1），中止' % (label, hit))
    return '\n'.join(out)


# 自我前置校验：中文那半句必须与 Go 侧常量逐字相同（否则 P7 跨语言锁会在写完词典当场红，
# 而红的位置指不到这个脚本）。读不到文件/读不到常量一律中止，不许"读不到就当一致"。
gosrc = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'backend-go',
                     'internal', 'fileproc', 'formats.go')
if not os.path.isfile(gosrc):
    raise SystemExit('读不到 %s（等值自检无法进行），中止' % gosrc)
gm = re.search(r'const PlainTextContainerHint = "(.*?)"', open(gosrc, encoding='utf-8').read())
if not gm:
    raise SystemExit('Go 侧 PlainTextContainerHint 锚点取不到 ⇒ 两边写法已漂，中止')
if gm.group(1) != ZH_CORE:
    raise SystemExit('中文文案与 Go 常量不等值：\n  go=%s\n  py=%s' % (gm.group(1), ZH_CORE))


changed = []
for rel in FILES:
    p = os.path.join(ROOT, rel)
    if not os.path.isfile(p):
        raise SystemExit('找不到 %s，中止' % rel)
    text = open(p, encoding='utf-8').read()
    lines = text.split('\n')
    new_lines = []
    done_hint = 0
    for ln in lines:
        if 'tk.fileHint' in ln and 'tk.fileHintText' not in ln:
            ln2 = strip_retired(ln, rel)
            if ln2 != ln:
                done_hint += 1
            ln = ln2
        new_lines.append(ln)
    if done_hint != 1:
        raise SystemExit('%s: tk.fileHint 改了 %d 行（应为 1），中止' % (rel, done_hint))
    text = insert_key('\n'.join(new_lines), rel, VALUES[rel])
    changed.append(rel)
    open(p, 'w', encoding='utf-8').write(text)

print('OK: %d 份词典已改（tk.fileHint 摘五档 ＋ 新键 tr.fileFmtPlainText）' % len(changed))
for c in changed:
    print('   -', c)
