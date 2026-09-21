#!/usr/bin/env python3
# ============================================================================
# scripts/i18n/add_gate_keys.py — 补齐 i18n 取词踩空闸门（missingKeyGate）发现的缺失键
#
# 背景：2026-09-22 建立 src/i18n/missingKeyGate.test.ts 静态闸门，实测 13 处引用踩在
# 词典里不存在的键上（界面直接显示裸键名、读屏软件逐字念出）。其中 1 处（auth.roleMember）
# 词典已有语义正确的 auth.roleStaff，改引用即可；其余 10 个键确属词典缺口，按 AGENTS.md 一.5
# 口径必须 zh + en + 十份 locales 同步补译（locales.core.test.ts 全量闸门逐键校验）。
#
# 插入策略（不做整文件重写，脚本可重跑）：
#  - dicts.zh.ts / dicts.en.ts：按锚点键所在行后插（同命名空间就地成组）；
#  - panels/auth.ts：一个文件里 zh 段 + en 段各有一份同名键，按「锚点第 n 次出现」分别插；
#  - locales/*.ts：现文件即 ASCII 键序，按序插（diff 最小）。
# 跑完必须验证：npx vitest run src/i18n/ && npx tsc --noEmit，任何红灯人工核对，禁止放宽断言。
# ============================================================================
import os
import re
import sys

SRC = os.path.join(os.path.dirname(__file__), "..", "..", "frontend-react", "src", "i18n")
SRC = os.path.abspath(SRC)
LOCALES = ["ru", "fr", "de", "es", "pt", "ar", "th", "ja", "ko", "zh-hant"]

# 键 → 12 份值（zh / en 为源语言，其余十语种为全量词典译文）
NEW = {
    "common.fail": {
        "zh": "操作失败", "en": "Operation failed",
        "ru": "Сбой операции", "fr": "Échec de l'opération", "de": "Vorgang fehlgeschlagen",
        "es": "Error en la operación", "pt": "Falha na operação", "ar": "فشل العملية",
        "th": "การดำเนินการล้มเหลว", "ja": "操作に失敗しました", "ko": "작업 실패",
        "zh-hant": "操作失敗",
    },
    "chat.searchClear": {
        "zh": "清除搜索", "en": "Clear search",
        "ru": "Очистить поиск", "fr": "Effacer la recherche", "de": "Suche löschen",
        "es": "Borrar búsqueda", "pt": "Limpar pesquisa", "ar": "مسح البحث",
        "th": "ล้างการค้นหา", "ja": "検索をクリア", "ko": "검색 지우기",
        "zh-hant": "清除搜尋",
    },
    "auth.orgInvite": {
        "zh": "组织邀请码", "en": "Organization invite code",
        "ru": "Код приглашения организации", "fr": "Code d'invitation de l'organisation",
        "de": "Einladungscode der Organisation", "es": "Código de invitación de la organización",
        "pt": "Código de convite da organização", "ar": "رمز دعوة المؤسسة",
        "th": "รหัสคำเชิญขององค์กร", "ja": "組織招待コード", "ko": "조직 초대 코드",
        "zh-hant": "組織邀請碼",
    },
    "auth.selectIndustry": {
        "zh": "请选择所属行业", "en": "Select an industry",
        "ru": "Выберите отрасль", "fr": "Sélectionnez un secteur", "de": "Branche auswählen",
        "es": "Seleccione un sector", "pt": "Selecione um setor", "ar": "اختر القطاع",
        "th": "เลือกอุตสาหกรรม", "ja": "業種を選択", "ko": "업종 선택",
        "zh-hant": "請選擇所屬行業",
    },
    "tk.edTitle": {
        "zh": "译文对照编辑", "en": "Translation editor",
        "ru": "Редактор перевода", "fr": "Éditeur de traduction", "de": "Übersetzungseditor",
        "es": "Editor de traducción", "pt": "Editor de tradução", "ar": "محرر الترجمة",
        "th": "ตัวแก้ไขคำแปล", "ja": "訳文編集", "ko": "번역 편집",
        "zh-hant": "譯文對照編輯",
    },
    "tk.edColLang": {
        "zh": "语种", "en": "Language",
        "ru": "Язык", "fr": "Langue", "de": "Sprache",
        "es": "Idioma", "pt": "Idioma", "ar": "اللغة",
        "th": "ภาษา", "ja": "言語", "ko": "언어",
        "zh-hant": "語種",
    },
    "tk.edLoad": {
        "zh": "加载", "en": "Load",
        "ru": "Загрузить", "fr": "Charger", "de": "Laden",
        "es": "Cargar", "pt": "Carregar", "ar": "تحميل",
        "th": "โหลด", "ja": "読み込み", "ko": "불러오기",
        "zh-hant": "載入",
    },
    "tk.edSave": {
        "zh": "保存", "en": "Save",
        "ru": "Сохранить", "fr": "Enregistrer", "de": "Speichern",
        "es": "Guardar", "pt": "Salvar", "ar": "حفظ",
        "th": "บันทึก", "ja": "保存", "ko": "저장",
        "zh-hant": "儲存",
    },
    "tk.edTermsHit": {
        "zh": "命中术语", "en": "Matched terms",
        "ru": "Найденные термины", "fr": "Termes correspondants", "de": "Übereinstimmende Termini",
        "es": "Términos coincidentes", "pt": "Termos correspondentes", "ar": "المصطلحات المطابقة",
        "th": "ศัพท์ที่ตรงกัน", "ja": "一致した用語", "ko": "일치 용어",
        "zh-hant": "命中術語",
    },
    "tk.edEmptyHint": {
        "zh": "输入工单 ID 或工单号后点「加载」，即可逐段编辑译文",
        "en": "Enter a ticket ID or ticket no., then click Load to edit segments",
        "ru": "Введите ID или номер задачи и нажмите «Загрузить», чтобы редактировать сегменты",
        "fr": "Saisissez un ID ou un numéro de ticket, puis cliquez sur Charger pour modifier les segments",
        "de": "Ticket-ID oder -Nr. eingeben und auf Laden klicken, um Segmente zu bearbeiten",
        "es": "Introduce un ID o número de ticket y pulsa Cargar para editar segmentos",
        "pt": "Insira um ID ou número de ticket e clique em Carregar para editar segmentos",
        "ar": "أدخل معرّف التذكرة أو رقمها ثم اضغط «تحميل» لتحرير المقاطع",
        "th": "ป้อน ID หรือหมายเลขงานแล้วกด โหลด เพื่อแก้ไขแต่ละท่อน",
        "ja": "チケットIDまたは番号を入力し、読み込みを押すとセグメントを編集できます",
        "ko": "티켓 ID 또는 번호를 입력하고 불러오기를 누르면 구문을 편집할 수 있습니다",
        "zh-hant": "輸入工單 ID 或工單號後點「載入」，即可逐段編輯譯文",
    },
}

KEY_RE = re.compile(r"""^\s*['"](?P<k>[^'"]+)['"]\s*:""")


def read(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


def write(path, text):
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)


def line_of(key, val, quote):
    if quote in val:
        sys.exit(f"值内含引号字符 {quote}，需另行转义：{key}={val}")
    return f"  {quote}{key}{quote}: {quote}{val}{quote},"


def insert_after_anchor(path, anchor_key, pairs, quote):
    """anchor_key 行之后插入 pairs=[(key, value)]；文件中已存在该键则跳过（可重跑）。"""
    lines = read(path).split("\n")
    have = {m.group("k") for ln in lines if (m := KEY_RE.match(ln))}
    todo = [(k, v) for k, v in pairs if k not in have]
    if not todo:
        return
    idx = next((i for i, ln in enumerate(lines)
                if (m := KEY_RE.match(ln)) and m.group("k") == anchor_key), None)
    if idx is None:
        sys.exit(f"锚点 {anchor_key} 不在 {path}，拒绝盲插")
    lines[idx + 1:idx + 1] = [line_of(k, v, quote) for k, v in todo]
    write(path, "\n".join(lines))
    print(f"  +{len(todo)} {os.path.relpath(path, SRC)} @ {anchor_key}")


def insert_after_nth(path, anchor_key, nth, key, val, quote):
    """同文件多段（zh 段 / en 段）各有 anchor_key：只在第 nth 次出现后插该行。"""
    lines = read(path).split("\n")
    hits = [i for i, ln in enumerate(lines)
            if (m := KEY_RE.match(ln)) and m.group("k") == anchor_key]
    if len(hits) < nth:
        sys.exit(f"{path} 里锚点 {anchor_key} 只出现 {len(hits)} 次，无法插第 {nth} 段")
    at = hits[nth - 1]
    nxt = lines[at + 1]
    m = KEY_RE.match(nxt)
    if m and m.group("k") == key:
        return  # 已插过（紧随锚点之后），重跑安全
    # 该段内是否已有同名键（避免锚点判断失灵时插重复）
    seg_end = next((i for i in range(at + 1, len(lines)) if lines[i].strip() == "}" or lines[i].strip() == "};"), len(lines))
    if any((mm := KEY_RE.match(ln)) and mm.group("k") == key for ln in lines[at:seg_end]):
        return
    lines.insert(at + 1, line_of(key, val, quote))
    write(path, "\n".join(lines))
    print(f"  +1 {os.path.relpath(path, SRC)} #{nth} @ {anchor_key} → {key}")


def insert_sorted(path, pairs, quote):
    """按 ASCII 键序插入（locales/*.ts 现文件即键序排列，插入后 diff 最小且仍可重跑）。"""
    lines = read(path).split("\n")
    have = {m.group("k") for ln in lines if (m := KEY_RE.match(ln))}
    added = 0
    for k, v in sorted(pairs):
        if k in have:
            continue
        pos = next((i for i, ln in enumerate(lines)
                    if (m := KEY_RE.match(ln)) and m.group("k") > k), None)
        if pos is None:
            sys.exit(f"{path} 找不到 {k} 的插入位置（文件形态与假设不符）")
        lines.insert(pos, line_of(k, v, quote))
        have.add(k)
        added += 1
    write(path, "\n".join(lines))
    print(f"  +{added} {os.path.relpath(path, SRC)}")


# ① dicts.zh.ts / dicts.en.ts：common. / chat. / tk. 三组就地插锚点后面
ANCHORS = [
    ("common.saveFail", ["common.fail"]),
    ("chat.searchPh", ["chat.searchClear"]),
    ("tk.edNotePlaceholder", ["tk.edTitle", "tk.edColLang", "tk.edLoad",
                              "tk.edSave", "tk.edTermsHit", "tk.edEmptyHint"]),
]
for lang, fname in (("zh", "dicts.zh.ts"), ("en", "dicts.en.ts")):
    p = os.path.join(SRC, fname)
    for anchor, keys in ANCHORS:
        insert_after_anchor(p, anchor, [(k, NEW[k][lang]) for k in keys], "'")

# ② panels/auth.ts：auth.* 两键（zh 段与 en 段各插一次）
pa = os.path.join(SRC, "panels", "auth.ts")
for key, anchor in (("auth.orgInvite", "auth.orgCodeRequired"),
                    ("auth.selectIndustry", "auth.orgCodeStaffPlaceholder")):
    insert_after_nth(pa, anchor, 1, key, NEW[key]["zh"], "'")
    insert_after_nth(pa, anchor, 2, key, NEW[key]["en"], "'")

# ③ 十份 locales：有序插入
for loc in LOCALES:
    variant = "zh-hant" if loc == "zh-hant" else loc
    insert_sorted(os.path.join(SRC, "locales", f"{loc}.ts"),
                  [(k, NEW[k][variant]) for k in NEW], '"')

print("完成：10 键 × (zh + en + 10 locales)")
