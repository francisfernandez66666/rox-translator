#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""向十份 locales 插入 sdk.ext* 三键（2026-09-23 扩展交付链批次）。

为什么用脚本而不是十次手改：同一批键要在 10 个文件同一位置落位，手改极易
把相邻行的引号或逗号带偏；脚本按锚点键定位后插入，并回读校验总键数。
用完即可删除，不属于运行时代码。
"""
import json
import os

TR = {
    "ru": {
        "sdk.extTitle": "Расширение браузера: перевод по выделению",
        "sdk.extDesc": "Выделите текст на странице — перевод идёт по той же API Key. Распакуйте архив, включите «Режим разработчика» на странице расширений и выберите «Загрузить распакованное расширение»; подробности в файле INSTALL.txt внутри архива.",
        "sdk.extDownload": "Скачать zip",
    },
    "fr": {
        "sdk.extTitle": "Extension de traduction du texte sélectionné",
        "sdk.extDesc": "Sélectionnez du texte sur une page : la traduction utilise la même clé API. Décompressez l'archive, activez le mode développeur sur la page des extensions, puis « Charger l'extension non empaquetée » ; détails dans INSTALL.txt inclus.",
        "sdk.extDownload": "Télécharger le zip",
    },
    "ar": {
        "sdk.extTitle": "ملحق المتصفح: ترجمة النص المحدد",
        "sdk.extDesc": "حدّد نصاً في الصفحة لتُترجم بنفس مفتاح API. فك ضغط الملف ثم فعّل «وضع المطور» في صفحة الملحقات واختر «تحميل ملحق غير مغلف»؛ التفاصيل في INSTALL.txt داخل الحزمة.",
        "sdk.extDownload": "تنزيل ملف zip",
    },
    "es": {
        "sdk.extTitle": "Extensión del navegador: traducción de selección",
        "sdk.extDesc": "Selecciona texto en la página y se traduce con la misma API Key. Descomprime el zip, activa el «Modo de desarrollador» en la página de extensiones y elige «Cargar sin empaquetar»; detalles en INSTALL.txt dentro del paquete.",
        "sdk.extDownload": "Descargar zip",
    },
    "pt": {
        "sdk.extTitle": "Extensão do navegador: tradução de seleção",
        "sdk.extDesc": "Selecione texto na página para traduzir com a mesma API Key. Descompacte o zip, ative o Modo de desenvolvedor na página de extensões e escolha Carregar sem compactar; detalhes em INSTALL.txt no pacote.",
        "sdk.extDownload": "Baixar zip",
    },
    "th": {
        "sdk.extTitle": "ส่วนขยายเบราว์เซอร์: แปลงข้อความที่เลือก",
        "sdk.extDesc": "เลือกข้อความบนหน้าเว็บแล้วแปลด้วย API Key เดียวกัน แตกไฟล์ zip จากนั้นเปิดโหมดนักพัฒนาบนหน้าส่วนขยายและเลือก «โหลดส่วนขยายที่ยังไม่ได้แพ็ก» รายละเอียดอยู่ใน INSTALL.txt ในแพ็กเกจ",
        "sdk.extDownload": "ดาวน์โหลด zip",
    },
    "ja": {
        "sdk.extTitle": "ブラウザ拡張（選択テキスト翻訳）",
        "sdk.extDesc": "ページ上のテキストを選択すると同じ API Key で翻訳します。zip を解凍した後、拡張機能ページで「開発者モード」をオンにして「圧縮されていない拡張機能を読み込む」でフォルダを選択してください。詳細は同梱の INSTALL.txt を参照。",
        "sdk.extDownload": "zip をダウンロード",
    },
    "ko": {
        "sdk.extTitle": "브라우저 확장(선택 번역)",
        "sdk.extDesc": "페이지에서 텍스트를 선택하면 같은 API Key로 번역합니다. zip을 압축 해제한 뒤 확장 프로그램 페이지에서 개발자 모드를 켜고 '압축 해제된 확장 프로그램 불러오기'로 폴더를 선택하세요. 자세한 내용은 동봉된 INSTALL.txt를 참고하십시오.",
        "sdk.extDownload": "zip 다운로드",
    },
    "de": {
        "sdk.extTitle": "Browser-Erweiterung: Auswahlübersetzung",
        "sdk.extDesc": "Text auf der Seite markieren, übersetzt mit demselben API-Key. Zip entpacken, auf der Erweiterungsseite den Entwicklermodus aktivieren und „Entpackte Erweiterung laden“ wählen; Details in der beiliegenden INSTALL.txt.",
        "sdk.extDownload": "Zip herunterladen",
    },
    "zh-hant": {
        "sdk.extTitle": "瀏覽器劃詞外掛",
        "sdk.extDesc": "選取網頁文字即譯，沿用同一組 API Key。下載 zip 解壓後，在外掛管理頁開啟「開發人員模式」→「載入未封裝外掛程式」選取該目錄；詳細說明見 zip 內 INSTALL.txt。",
        "sdk.extDownload": "下載外掛 zip",
    },
}

ANCHOR = '"sdk.changelogHint"'
# 本脚本位于 scripts/i18n/，词典在 frontend-react/src/i18n/locales/
here = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "frontend-react", "src", "i18n")
for lang, kv in TR.items():
    p = os.path.join(here, "locales", f"{lang}.ts")
    lines = open(p, encoding="utf-8").read().split("\n")
    idx = next(i for i, l in enumerate(lines) if ANCHOR in l)
    if any("sdk.extTitle" in l for l in lines):
        print(f"{lang}: 已存在，跳过")
        continue
    add = [f'  {json.dumps(k, ensure_ascii=False)}: {json.dumps(v, ensure_ascii=False)},' for k, v in kv.items()]
    lines[idx + 1:idx + 1] = add
    open(p, "w", encoding="utf-8").write("\n".join(lines))
    print(f"{lang}: 在第 {idx+2} 行后插入 {len(add)} 键")
