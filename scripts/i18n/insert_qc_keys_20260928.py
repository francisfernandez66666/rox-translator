#!/usr/bin/env python3
# ============================================================================
# scripts/i18n/insert_qc_keys_20260928.py — 〇-Y #64：首页「快速算价」卡 7 键补进十份 locale
#
# 背景：panels/landing.ts 新增 `land.qc.*` 7 键（zh/en 成对）后，ALL_KEYS 长度 +7，
# 而 AGENTS §一·5 的 12 语种口径要求 locales/*.ts **逐键全量覆盖**
# （locales.core.test.ts 以 `Object.keys(dict).length === ALL_KEYS.length` 红灯拦截）。
# 本脚本负责剩下十语种那一半：把译文按锚点插入，**可重跑**（已存在的键跳过）。
#
# 插入位置：`"land.planEntF3"` 行之后——紧跟它所属的价格区块，与 zh/en 词典里的
# 分段顺序一致（乱序追加会让下一次 diff 完全看不出这是同一区块）。锚点缺失即拒绝盲插。
#
# 校验口径（与 insert_cmp_keys_20260928.py 同一套，任一不合格整语种不落盘）：
#   ① 键集与 panels/landing.ts 的 en 段新增键完全一致；
#   ② 每键占位符集合与 en 逐键相等（{points} {unit} {p} {pct}）——
#      漏掉就是界面上出现裸 {points}，多出来就是替换不上；
#   ③ 值内不得有 ASCII 双引号、反斜杠、换行（locale 用双引号成对，混进去直接 TS 语法崩）；
#   ④ 非 CJK 语种（ru/fr/de/es/pt/ar/th）值内不得出现汉字——那是 locales.core.test.ts
#      的「漏译扫描」判据，脚本先自己拦，不等测试红灯才发现是复制粘贴带进去的。
#
# 跑完必须验证（禁止放宽断言）：
#   cd frontend-react && npx tsc --noEmit && npx vitest run src/i18n/
# ============================================================================
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.abspath(os.path.join(HERE, "..", "..", "frontend-react", "src", "i18n"))
LOCALES = ["ru", "fr", "de", "es", "pt", "ar", "th", "ja", "ko", "zh-hant"]
ANCHOR = '"land.planEntF3"'
CJK_BAN = {"ru", "fr", "de", "es", "pt", "ar", "th"}
HAN = re.compile(r"[㐀-䶿一-鿿]")

# 占位符基准：与 panels/landing.ts 的 en 段逐键一致
EN_TOKENS = {
    "land.qc.title": [],
    "land.qc.sub": [],
    "land.qc.ours": [],
    "land.qc.points": ["points", "unit"],
    "land.qc.human": ["p"],
    "land.qc.save": ["pct"],
    "land.qc.detail": [],
}

# TR 每语种一份译文（顺序由 KEYS 固定）
TR = {
    "ru": [
        "Быстрый расчёт",
        "Укажите число символов оригинала и число целевых языков: применяется та же формула, что при оформлении заказа, а результат показывается рядом с минимальной публичной ставкой человеческого перевода. Формула, коэффициенты, источники ставки и причины отклонений — на странице «Тарифы и расчёт».",
        "Оценка LangCross",
        "около {points} {unit}",
        "Человеческий перевод · начальный ({p} юаня за символ оригинала)",
        "Примерно на {pct}% ниже начальной человеческой ставки",
        "Формула, коэффициенты и источники ставок",
    ],
    "fr": [
        "Estimation rapide",
        "Saisissez le nombre de caractères source et le nombre de langues cibles : la carte applique la même formule qu'au moment de la commande et affiche le résultat à côté du tarif humain d'entrée. La formule, les coefficients, les sources publiques du tarif humain et les raisons des variations sont sur la page « Tarifs et estimation ».",
        "Estimation LangCross",
        "environ {points} {unit}",
        "Traduction humaine · entrée ({p} CNY par caractère source)",
        "Environ {pct}% sous le tarif humain d'entrée",
        "Voir la formule, les coefficients et les sources",
    ],
    "de": [
        "Schnelle Kostenschätzung",
        "Geben Sie Quellzeichen und Anzahl der Zielsprachen ein – es dieselbe Formel wie bei der Bestellung angewandt, und das Ergebnis steht dem Einstiegspreis für menschliche Übersetzung gegenüber. Formel, Koeffizienten, öffentliche Quellen des Menschentarifs und Gründe für Abweichungen finden Sie auf der Seite „Preise & Kostenschätzung“.",
        "LangCross-Schätzung",
        "etwa {points} {unit}",
        "Menschliche Übersetzung · Einstieg ({p} CNY pro Quellzeichen)",
        "Rund {pct} % unter dem Einstiegspreis der menschlichen Übersetzung",
        "Formel, Koeffizienten und Tarifquellen ansehen",
    ],
    "es": [
        "Estimación rápida",
        "Introduce el número de caracteres fuente y de idiomas objetivo: se aplica la misma fórmula que al crear el pedido y el resultado aparece junto a la tarifa de entrada de la traducción humana. La fórmula, los coeficientes, las fuentes públicas de esa tarifa y las causas de las variaciones están en la página «Precios y estimación».",
        "Estimación de LangCross",
        "unos {points} {unit}",
        "Traducción humana · entrada ({p} CNY por carácter fuente)",
        "Aproximadamente {pct} % por debajo de la tarifa humana de entrada",
        "Ver la fórmula, los coeficientes y las fuentes",
    ],
    "pt": [
        "Estimativa rápida",
        "Informe o número de caracteres de origem e o número de idiomas de destino: aplicamos a mesma fórmula usada ao criar o pedido e mostramos o resultado ao lado da tarifa de entrada da tradução humana. A fórmula, os coeficientes, as fontes públicas dessa tarifa e as causas das variações estão na página «Preços e estimativa».",
        "Estimativa LangCross",
        "cerca de {points} {unit}",
        "Tradução humana · entrada ({p} CNY por caractere de origem)",
        "Cerca de {pct}% abaixo da tarifa humana de entrada",
        "Ver a fórmula, os coeficientes e as fontes",
    ],
    "ar": [
        "تقدير سريع",
        "أدخل عدد أحرف النص الأصلي وعدد اللغات الهدف: تُطبَّق الصيغة نفسها المستخدمة عند إنشاء الطلب، وتُعرض النتيجة بجانب السعر المعلن الأدنى للترجمة البشرية. الصيغة والمعاملات ومصادر السعر وأسباب التغيّر موجودة في صفحة «التسعير واحتساب التكلفة».",
        "تقدير LangCross",
        "حوالي {points} {unit}",
        "ترجمة بشرية · الحد الأدنى ({p} يوان لكل حرف أصلي)",
        "أقل بنحو {pct}% من الحد الأدنى لسعر الترجمة البشرية",
        "عرض الصيغة والمعاملات ومصادر السعر",
    ],
    "th": [
        "ประเมินราคาเร็ว",
        "กรอกจำนวนตัวอักษรต้นฉบับและจำนวนภาษาเป้าหมาย ระบบใช้สูตรเดียวกับตอนสร้างคำสั่งซื้อ และแสดงผลเคียงข้างราคาเริ่มต้นของงานแปลมนุษย์ที่ประกาศไว้สาธารณะ สูตร สัมประสิทธิ์ แหล่งที่มาของราคา และสาเหตุการคลาดเคลื่อนอยู่ในหน้า «เทียบราคาและคำนวณราคา»",
        "ประมาณการโดย LangCross",
        "ประมาณ {points} {unit}",
        "แปลมนุษย์ · อัตราเริ่มต้น ({p} หยวนต่อตัวอักษรต้นฉบับ)",
        "ถูกกว่าอัตราเริ่มต้นของงานแปลมนุษย์ราว {pct}%",
        "ดูสูตร สัมประสิทธิ์ และแหล่งที่มาของราคา",
    ],
    "ja": [
        "すばやい試算",
        "原文文字数と目標言語数を入力すると、注文時と同じ計算式で見積もり額をその場で算出し、同じ分量の手訳の入門価格と並べて表示します。計算式・係数・手訳単価の公開出所・変動の理由は「価格比較と試算」ページに記載しています。",
        "LangCross 見積もり",
        "約 {points} {unit}",
        "手訳 · 入門プラン（{p} 元／原文1文字）",
        "手訳の入門プランより約 {pct}% お得",
        "計算式・係数・手訳単価の出所を見る",
    ],
    "ko": [
        "빠른 견적",
        "원문 문자 수와 목표 언어 수를 입력하면 주문 때와 같은 계산식으로 비용을 즉시 산출하고, 같은 분량의 인번역 입문 단가와 나란히 보여 줍니다. 계산식과 계수, 인번역 단가의 공개 출처, 변동 이유는 「가격 비교 및 견적」 페이지에 있습니다.",
        "LangCross 예상",
        "약 {points} {unit}",
        "인번역 · 입문 단가({p}위안/원문 문자)",
        "인번역 입문 단가보다 약 {pct}% 절감",
        "계산식·계수·인번역 단가 출처 보기",
    ],
    "zh-hant": [
        "快速算價",
        "填入來源字元數與目標語種數，當場按下單時同一條公式估算費用，並與同批內容的人工筆譯入門價並列呈現。公式、係數、人工單價的公開出處與浮動說明都在「比價與算價」頁。",
        "LangCross 預估",
        "約 {points} {unit}",
        "人工筆譯 · 入門檔（{p} 元/來源字元）",
        "比人工入門檔再省約 {pct}%",
        "檢視公式、係數與人工單價出處",
    ],
}

KEYS = list(EN_TOKENS.keys())


def toks(v):
    return sorted(re.findall(r"\{(\w+)\}", v))


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "--dry":
        print("dry: 只打印将要插入的行，不写盘")
        apply = False
    else:
        apply = True
    total = 0
    for loc in LOCALES:
        path = os.path.join(SRC, "locales", f"{loc}.ts")
        if not os.path.exists(path):
            sys.exit(f"缺 locale 文件 {path}")
        lines = open(path, encoding="utf-8").read().split("\n")
        vals = TR[loc]
        if len(vals) != len(KEYS):
            sys.exit(f"{loc}: 译文条数 {len(vals)} != 键数 {len(KEYS)}")
        # ② 占位符逐键对齐 en 基准
        for k, v in zip(KEYS, vals):
            if toks(v) != EN_TOKENS[k]:
                sys.exit(f"{loc}/{k}: 占位符漂移 {toks(v)} != {EN_TOKENS[k]}")
            # ③ 形态红线：ASCII 双引号 / 反斜杠 / 换行都会炸掉 TS 字符串
            if '"' in v or "\\" in v or "\n" in v:
                sys.exit(f"{loc}/{k}: 值内含 ASCII 双引号/反斜杠/换行，会炸掉词典")
            # ④ 非 CJK 语种禁汉字（漏译扫描同判据，先自己拦）
            if loc in CJK_BAN and HAN.search(v):
                sys.exit(f"{loc}/{k}: 值内残留汉字 {HAN.search(v).group(0)}")
        anchor_i = next((i for i, l in enumerate(lines) if ANCHOR in l), None)
        if anchor_i is None:
            sys.exit(f"{loc}: 找不到锚点 {ANCHOR}，拒绝盲插")
        already = sum(1 for l in lines if '"land.qc.' in l)
        if already == len(KEYS):
            print(f"{loc}: 7 键已存在，跳过")
            continue
        if already:
            sys.exit(f"{loc}: 只找到 {already}/{len(KEYS)} 个 land.qc.* 键，状态不一致，先人工核")
        block = [f'  "{k}": "{v}",' for k, v in zip(KEYS, vals)]
        lines[anchor_i + 1:anchor_i + 1] = block
        if apply:
            open(path, "w", encoding="utf-8").write("\n".join(lines))
        print(f"{loc}: 在 land.planEntF3 之后插入 {len(block)} 行")
        total += len(block)
    print(f"TOTAL_INSERTED {total}")


if __name__ == "__main__":
    main()
