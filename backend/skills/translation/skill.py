# ============================================================================
# skills/translation/skill.py — 翻译技能（懒加载 + 5步文件翻译 + SSE 进度）
# ============================================================================
# 【升级点】
#   1. ★ 懒加载 lib.py：启动时不import，第一次调用时才加载（避免numpy拖慢启动）
#   2. ★ 缓存 DB 连接和向量索引：加载一次后常驻内存，不每次请求都重读
#   3. 文本翻译：知识库匹配 + 在线模型
#   4. 文件翻译：5步流程 + 修正排版
#   5. ★ "其他语言"统一为一个选项：用户在prompt中指定语言（如"翻译成泰语：你好"），
#      后端用LLM解析出目标语言，然后走纯模型翻译，文件翻译也是5步法
#   6. ★ KB语言动态查询：前端可获知哪些语言有KB支持
#   7. ★ KB上传+自动向量化：上传CSV/Excel扩充知识库，自动重建索引
# ============================================================================

# ---- 标准库导入 ----
import os
import sys
import re
import json
from typing import Optional

# ---- 路径配置 ----
# 将当前脚本所在目录加入 sys.path，以便导入同目录下的模块
_SCRIPTS_DIR = os.path.dirname(os.path.abspath(__file__))
if _SCRIPTS_DIR not in sys.path:
    sys.path.insert(0, _SCRIPTS_DIR)

# 将 backend 父目录加入 sys.path，以便导入 base_skill 等模块
_BACKEND_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if _BACKEND_DIR not in sys.path:
    sys.path.insert(0, _BACKEND_DIR)

# 导入基础技能类
from base_skill import BaseSkill

# ---- ★ 懒加载：启动时不 import lib，避免 numpy 等重型依赖拖慢启动 ----
_lib = None           # type: ignore  # lib 模块引用，首次调用时加载
_db_conn = None       # 数据库连接缓存
_index_ids = None     # 向量索引ID缓存
_index_vecs = None    # 向量索引缓存

# ★ 以下提示词常量已废弃（v2.15 统一翻译架构）：
# _OTHER_LANG_SYSTEM_PROMPT、_UNDERSTAND_PROMPT、_TRANSLATE_WITH_CONTEXT_PROMPT
# 所有翻译（KB语言+其他语言）统一走 lib.call_online_llm_single_lang()，
# 不再在 skill.py 里重复实现API调用逻辑
# 旧 system prompt 常量（SYSTEM_PROMPT_TEXT_SIMPLE 等）已全部从 lib.py 中删除，
# 改用极简 user message 格式，由 post_process_translation() 统一做风格后处理。


def _ensure_lib():
    """
    懒加载翻译引擎 lib.py

    ★ 直接从文件系统加载，不依赖 sys.path，兼容所有打包模式
    搜索顺序：同目录 → PyInstaller 打包路径 → 标准 import

    Returns:
        module | None: 加载成功返回 lib 模块，失败返回 None
    """
    global _lib
    if _lib is not None:
        return _lib

    import importlib.util as _ilu

    # ★ 构建所有可能的 lib.py 路径
    _candidates = []
    # 1) 同目录（skill.py 旁边）
    _candidates.append(os.path.join(_SCRIPTS_DIR, "lib.py"))
    # 2) PyInstaller _MEIPASS 下
    if getattr(sys, 'frozen', False):
        _meipass = getattr(sys, '_MEIPASS', '')
        if _meipass:
            _candidates.append(os.path.join(_meipass, "skills", "translation", "lib.py"))
            _candidates.append(os.path.join(_meipass, "_internal", "skills", "translation", "lib.py"))
            _candidates.append(os.path.join(_meipass, "Resources", "skills", "translation", "lib.py"))
        # 3) 可执行文件同级 _internal
        _exe_dir = os.path.dirname(sys.executable) if getattr(sys, 'executable', '') else ''
        if _exe_dir:
            _candidates.append(os.path.join(_exe_dir, "_internal", "skills", "translation", "lib.py"))
            _candidates.append(os.path.join(_exe_dir, "..", "_internal", "skills", "translation", "lib.py"))

    # 去重 + 验证：遍历候选路径，找到第一个有效文件并加载
    _seen = set()
    for p in _candidates:
        rp = os.path.realpath(p)
        if rp in _seen:
            continue
        _seen.add(rp)
        if not os.path.isfile(rp):
            continue
        try:
            _spec = _ilu.spec_from_file_location("translation_lib", rp)
            if _spec and _spec.loader:
                _mod = _ilu.module_from_spec(_spec)
                _spec.loader.exec_module(_mod)
                _lib = _mod
                print(f"  ✅ 翻译引擎 lib.py 已加载: {rp}")
                return _lib
        except Exception as e:
            print(f"  ⚠️ 加载 {rp} 失败: {e}")
            continue

    # 4) 最后尝试标准 import（可能 sys.path 里有）
    try:
        import importlib as _il
        _lib = _il.import_module('skills.translation.lib')
        print("  ✅ 翻译引擎 lib.py 已加载（import_module）")
        return _lib
    except Exception:
        pass

    # 所有路径均失败，打印诊断信息
    print(f"  ❌ 翻译引擎 lib.py 加载失败，搜索路径:")
    for p in _candidates:
        print(f"     {'✅' if os.path.isfile(os.path.realpath(p)) else '❌'} {p}")
    return None


def _get_cached_resources():
    """
    获取缓存的 DB 连接和向量索引

    首次调用时初始化 DB 连接和向量索引，后续调用复用缓存，
    避免每次请求都重新 load_index。

    Returns:
        tuple: (lib, conn, ids, vecs)
            - lib: lib 模块引用，加载失败为 None
            - conn: SQLite 数据库连接，无数据库文件时为 None
            - ids: 向量索引 ID 数组，无缓存时为 None
            - vecs: 向量索引向量数组，无缓存时为 None
    """
    global _db_conn, _index_ids, _index_vecs

    l = _ensure_lib()
    if l is None:
        return None, None, None, None

    # ---- 缓存 DB 连接 ----
    if _db_conn is None:
        if os.path.isfile(l.DB_PATH):
            _db_conn = l.get_db()
            print("  ✅ 翻译知识库 DB 已连接（缓存）")
        else:
            # 数据库文件不存在，仅返回 lib 模块
            return l, None, None, None

    # ---- 缓存向量索引 ----
    if _index_ids is None or _index_vecs is None:
        _index_ids, _index_vecs = l.load_index()
        print(f"  ✅ 向量索引已加载（缓存，{len(_index_ids)} 条）")

    return l, _db_conn, _index_ids, _index_vecs


class TranslationSkill(BaseSkill):
    """
    多语言翻译技能（懒加载版）

    文本翻译：知识库匹配 + 在线模型（精简提示词）
    文件翻译：5步流程（理解→总结补齐→翻译→简练→复查）+ 修正排版
    ★ "其他语言"：用户在prompt指定语言，后端解析后走纯模型翻译，文件翻译仍5步法
    """

    @property
    def name(self) -> str:
        """返回技能名称标识"""
        return "translation"

    @property
    def description(self) -> str:
        """返回技能描述文本，用于前端展示"""
        return "多语言翻译：支持9种知识库语言+任意其他语言AI翻译，支持文本和文件翻译"

    # ★ 知识库支持的语言列表（有翻译记忆库和向量索引的语言）
    @property
    def kb_langs(self) -> list[str]:
        """获取当前知识库支持的语言代码列表"""
        l = _ensure_lib()
        if l is None:
            return []
        return list(l.TRANSLATE_LANGS)

    # ★ 语言名称映射
    @property
    def lang_names(self) -> dict:
        """获取语言代码 → 中文名的映射字典"""
        l = _ensure_lib()
        if l is None:
            return {}
        return dict(l.LANG_NAMES)

    @property
    def keywords(self) -> list[str]:
        """返回触发该技能的关键词列表，用于意图识别"""
        return [
            "翻译", "译成", "翻成", "translate",
            "多语言", "九语", "9语", "本地化",
            "英文怎么说", "俄语怎么说", "法语怎么说",
        ]

    def can_handle(self, user_input: str) -> float:
        """
        判断是否能够处理用户输入

        规则：包含关键词返回 0.9；短文本且非闲聊内容返回 0.5；否则返回 0.0

        Args:
            user_input: 用户输入的文本

        Returns:
            float: 0.0 ~ 1.0 的置信度评分
        """
        text = user_input.strip()
        text_lower = text.lower()
        # 关键词匹配：高置信度
        for kw in self.keywords:
            if kw in text_lower:
                return 0.9
        # 短文本非闲聊兜底：含中文字符且不是常见聊天用语
        if len(text) <= 20:
            chat_words = ["你好", "在吗", "谢谢", "再见", "嗨", "hi", "hello", "你是谁"]
            if not any(w in text_lower for w in chat_words):
                if any("\u4e00" <= c <= "\u9fff" for c in text):
                    return 0.5
        return 0.0

    def handle(self, params: dict) -> dict:
        """
        技能入口：根据参数分派文本翻译或文件翻译

        Args:
            params: 请求参数字典，包含以下可选键：
                - message: 用户输入文本
                - files: 文件路径列表
                - options: 选项字典（target_langs 等）
                - on_progress: 进度回调函数

        Returns:
            dict: 翻译结果（含 skill 名称、reply 文本、data 数据、files 文件列表等）
        """
        l = _ensure_lib()
        if l is None:
            return {
                "skill": self.name,
                "reply": "❌ 翻译引擎未加载，请检查 lib.py 是否存在",
                "error": "lib.py import failed",
            }

        message = params.get("message", "")
        files = params.get("files", [])
        options = params.get("options", {})
        on_progress = params.get("on_progress")

        # ---- 文件翻译 ----
        if files:
            return self._handle_file_translate(files, options, on_progress)

        # ---- 文本翻译 ----
        return self._handle_text_translate(message, options, on_progress)

    # ==================== ★ 从 prompt 中解析"其他语言"的目标语言 ====================

    # 常见语言中文名 → 语言代码映射（覆盖大部分场景）
    _LANG_ALIAS_MAP = {
        # 东亚/东南亚
        "泰语": "th", "越南语": "vi", "马来语": "ms", "印尼语": "id", "印度尼西亚语": "id",
        "缅甸语": "my", "柬埔寨语": "km", "老挝语": "lo", "菲律宾语": "fil", "他加禄语": "fil",
        # 欧洲
        "意大利语": "it", "波兰语": "pl", "瑞典语": "sv", "荷兰语": "nl", "乌克兰语": "uk",
        "希腊语": "el", "捷克语": "cs", "罗马尼亚语": "ro", "匈牙利语": "hu", "芬兰语": "fi",
        "丹麦语": "da", "挪威语": "no", "葡萄牙语": "pt",
        # 中东/南亚
        "土耳其语": "tr", "希伯来语": "he", "波斯语": "fa", "印地语": "hi", "乌尔都语": "ur",
        "孟加拉语": "bn", "泰米尔语": "ta",
        # 其他
        "韩语": "ko", "日语": "ja", "蒙语": "mn", "蒙古语": "mn",
    }

    # 常见英文语言名 → 代码
    _LANG_EN_ALIAS = {
        "thai": "th", "vietnamese": "vi", "malay": "ms", "indonesian": "id",
        "italian": "it", "polish": "pl", "swedish": "sv", "dutch": "nl",
        "ukrainian": "uk", "greek": "el", "czech": "cs", "romanian": "ro",
        "hungarian": "hu", "finnish": "fi", "danish": "da", "norwegian": "no",
        "turkish": "tr", "hebrew": "he", "persian": "fa", "hindi": "hi",
        "korean": "ko", "japanese": "ja", "mongolian": "mn",
        "bengali": "bn", "tamil": "ta", "filipino": "fil", "tagalog": "fil",
    }

    def _parse_other_lang_from_prompt(self, text: str) -> tuple[list[str], str]:
        """
        ★ 从用户 prompt 中解析目标语言（支持多语言，如"翻译成日语、韩语"）

        匹配模式：
        1. "翻译成泰语：xxx" / "翻成日语、韩语xxx"
        2. "用泰语翻译：xxx"
        3. "translate to thai: xxx"
        4. "泰语：xxx"（语言名开头+冒号）
        5. 纯英文语言名+冒号

        Args:
            text: 用户输入的文本

        Returns:
            tuple: (lang_codes列表, cleaned_text)
                - lang_codes: 解析到的语言代码列表，为空列表表示未解析到
                - cleaned_text: 移除语言指令后的纯文本
        """
        # ---- 辅助：单个语言名 → 代码 ----
        def _resolve_one(hint: str) -> Optional[str]:
            """将单个语言名转换为语言代码，使用别名表+LLM兜底"""
            hint = hint.strip().rstrip("语") + "语" if hint.strip() in self._LANG_ALIAS_MAP and not hint.strip().endswith("语") else hint.strip()
            # 别名表
            code = self._LANG_ALIAS_MAP.get(hint)
            if code:
                return code
            # 英文别名
            code = self._LANG_EN_ALIAS.get(hint.lower())
            if code:
                return code
            # LANG_NAMES 反查
            l = _ensure_lib()
            if l:
                for lc, name in l.LANG_NAMES.items():
                    if name == hint or hint in name:
                        return lc
            # LLM 兜底
            return self._llm_parse_lang(hint)

        # 模式1：翻译成/翻成/译成 + 语言名列表（支持顿号/逗号分隔）
        pat_list = r'(?:翻译成|翻成|译成|翻译为|翻为|译为)\s*(.+?)\s*[：:]\s*'
        m = re.search(pat_list, text, re.IGNORECASE)
        if m:
            lang_part = m.group(1).strip()
            # 按顿号、逗号、空格拆分语言名
            lang_hints = re.split(r'[、，,\s]+', lang_part)
            codes = []
            for h in lang_hints:
                h = h.strip()
                if not h:
                    continue
                code = _resolve_one(h)
                if code:
                    codes.append(code)
            if codes:
                cleaned = re.sub(pat_list, '', text, count=1, flags=re.IGNORECASE).strip()
                return codes, cleaned

        # 模式1b：翻译成xxx（无冒号，语言名在句尾前）
        pat_no_colon = r'(?:翻译成|翻成|译成|翻译为|翻为|译为)\s*(.+?)$'
        m = re.search(pat_no_colon, text, re.IGNORECASE)
        if m:
            lang_part = m.group(1).strip()
            # 去掉可能的正文（语言名都是短词）
            lang_hints = re.split(r'[、，,\s]+', lang_part)
            codes = []
            for h in lang_hints:
                h = h.strip()
                if not h:
                    continue
                code = _resolve_one(h)
                if code:
                    codes.append(code)
            if codes:
                cleaned = re.sub(pat_no_colon, '', text, count=1, flags=re.IGNORECASE).strip()
                return codes, cleaned

        # 模式2：用xxx语翻译
        m = re.search(r'用\s*(.+?)\s*翻译\s*[：:，,]?\s*', text)
        if m:
            lang_part = m.group(1).strip()
            lang_hints = re.split(r'[、，,\s]+', lang_part)
            codes = []
            for h in lang_hints:
                code = _resolve_one(h)
                if code:
                    codes.append(code)
            if codes:
                cleaned = re.sub(r'用\s*.+?\s*翻译\s*[：:，,]?\s*', '', text, count=1).strip()
                return codes, cleaned

        # 模式3：translate to xxx
        m = re.search(r'(?:translate\s+to|translat(?:e|ion)\s+in)\s+(.+?)\s*[：:，,]?\s*', text, re.IGNORECASE)
        if m:
            lang_part = m.group(1).strip()
            lang_hints = re.split(r'[、，,\s]+', lang_part)
            codes = []
            for h in lang_hints:
                code = _resolve_one(h)
                if code:
                    codes.append(code)
            if codes:
                cleaned = re.sub(r'(?:translate\s+to|translat(?:e|ion)\s+in)\s+.+?\s*[：:，,]?\s*', '', text, count=1, flags=re.IGNORECASE).strip()
                return codes, cleaned

        # 模式4：语言名+冒号开头（如"泰语：你好"）
        m = re.match(r'^(\S+语)\s*[：:]\s*(.+)$', text.strip())
        if m:
            code = _resolve_one(m.group(1))
            if code:
                return [code], m.group(2).strip()

        # 模式5：纯英文语言名+冒号
        m = re.match(r'^(\w+)\s*[：:]\s*(.+)$', text.strip())
        if m:
            code = _resolve_one(m.group(1))
            if code:
                return [code], m.group(2).strip()

        # 都没匹配到
        return [], text

    def _llm_parse_lang(self, lang_hint: str) -> Optional[str]:
        """
        用LLM辅助识别语言名，返回ISO 639-1代码

        当别名表无法匹配时，调用在线LLM作为兜底方案。

        Args:
            lang_hint: 用户输入的语言名称（如"泰语"、"thai"等）

        Returns:
            Optional[str]: ISO 639-1 两字母语言代码，无法识别返回 None
        """
        l = _ensure_lib()
        if l is None or not l.online_api_is_configured():
            return None

        try:
            cfg = l._get_online_config()
            url = f"{cfg['base_url'].rstrip('/')}/chat/completions"
            headers = {
                "Authorization": f"Bearer {cfg['api_key']}",
                "Content-Type": "application/json",
            }
            payload = {
                "model": cfg["model"],
                "messages": [
                    {
                        "role": "system",
                        "content": "你是一个语言识别助手。用户输入一个语言名称，你只返回该语言的ISO 639-1两字母代码。如果无法识别，返回unknown。只返回代码，不要解释。"
                    },
                    {
                        "role": "user",
                        "content": lang_hint
                    }
                ],
                "temperature": 0.0,
                "max_tokens": 10,
                "enable_thinking": False,
            }

            r = l.requests.post(url, headers=headers, json=payload, timeout=10)
            r.raise_for_status()
            r_json = r.json()
            content = l._extract_content(r_json)
            if content and content.strip().lower() != "unknown" and len(content.strip()) <= 5:
                return content.strip().lower()
        except Exception as e:
            print(f"  [语言解析] LLM辅助失败: {e}")

        return None

    # ==================== ★ "其他语言"专用翻译方法 ====================

    def _translate_other_lang(self, l, zh_text: str, target_lang: str, lang_display: str) -> str:
        """
        ★ "其他语言"专用翻译——统一走 lib.call_online_llm_single_lang()

        ★ 不再重复实现API调用/重试/429降级/清洗逻辑，所有语言翻译归一
        ★ lib.py 内置了 ja/ko 的专属指令，也可通过 lang_instruction_override 覆盖

        Args:
            l: lib 模块引用
            zh_text: 待翻译的中文文本
            target_lang: 目标语言代码（如 "th", "vi" 等）
            lang_display: 语言显示名称（用于日志/报错）

        Returns:
            str: 翻译后的文本，失败时返回含 "[翻译失败" 的错误信息
        """
        # ★ 构造语言专属指令覆盖（lib.py 内置了 ja/ko 指令，这里只处理 lib 没覆盖的）
        lang_instruction_override = None
        # ★ ja/ko 的指令已在 lib.call_online_llm_single_lang 内置，无需重复构造
        # 如果未来新增语言需要专属指令，在这里添加 elif 分支即可
        # elif target_lang == "th":
        #     lang_instruction_override = "请将以下中文翻译为泰语。..."

        try:
            # ★ 统一走 lib 的单语翻译（含429降级、截断重试、清洗等完整逻辑）
            result = l.call_online_llm_single_lang(
                zh_text, target_lang, [],
                use_simple_prompt=True,  # 文本翻译429降级走两步法
                lang_instruction_override=lang_instruction_override,
            )
        except Exception as e:
            return f"[翻译失败: {str(e)}]"

        return result

    # ==================== 文本翻译 ====================

    def _handle_text_translate(self, text: str, options: dict, on_progress=None) -> dict:
        """
        文本翻译：知识库匹配 + 在线模型

        支持两种目标语言指定方式：
        - 方式1（前端子选单）：target_langs 含语言代码（如 ["en", "ja"]）
        - 方式2（手写兜底）：target_langs 含 "other"，从 prompt 中解析语言

        ★ target_langs 中的 "other" → 从 prompt 解析语言 → 纯模型翻译
        ★ 直接传入的非KB语言代码（如 ja, ko, th）→ 自动走纯模型翻译

        Args:
            text: 待翻译的文本
            options: 选项字典，包含 target_langs 等
            on_progress: 进度回调函数，格式为 (message, current, total)

        Returns:
            dict: 翻译结果，包含 reply、data（translations、translations_source 等）
        """
        if not text.strip():
            return {"skill": self.name, "reply": "请输入要翻译的中文文本"}

        l, conn, ids, vecs = _get_cached_resources()
        if l is None:
            return {
                "skill": self.name,
                "reply": "❌ 翻译引擎未加载，请检查 lib.py 是否存在",
                "error": "lib.py import failed",
            }

        # 解析目标语言
        target_langs = options.get("target_langs")
        if not target_langs:
            # 从文本中自动解析语言指令（如"翻译成英语：xxx"）
            clean_text, parsed_langs = l.strip_lang_instruction(text)
            if parsed_langs:
                target_langs = parsed_langs
                text = clean_text
            else:
                target_langs = ["en"]  # 默认英语

        # ★ 离线翻译已移除（v2.18），所有翻译统一走在线API

        # ★ 分离 KB 语言和"其他语言"（支持两种交互方式）
        # 方式1：target_langs 直接含非KB语言代码（如 ja, ko, th）→ 自动识别为其他语言（前端子选单勾选）
        # 方式2：target_langs 含 "other" → 从 prompt 解析语言（手写兜底）
        # 两种方式可同时使用
        has_other = "other" in target_langs
        kb_target = [lang for lang in target_langs if lang != "other" and lang in l.TRANSLATE_LANGS]
        # ★ 直接从前端传入的非KB语言代码（新方式：前端子选单直接选语言）
        direct_other_codes = [lang for lang in target_langs if lang != "other" and lang not in l.TRANSLATE_LANGS]
        other_lang_codes = []  # 最终的其他语言代码列表
        other_lang_names = {}  # 语言代码→中文名映射

        # ★ 辅助：获取语言中文名
        def _get_lang_cn(code: str) -> str:
            """反查语言中文名：别名表 → LANG_NAMES → 代码回退"""
            for cn_name, c in self._LANG_ALIAS_MAP.items():
                if c == code:
                    return cn_name
            return l.LANG_NAMES.get(code, code)

        # ★ 合并"其他语言"来源：直接传入 + prompt解析
        if direct_other_codes:
            # 新方式：前端直接传了语言代码（子选单勾选）
            other_lang_codes = list(direct_other_codes)
            for code in other_lang_codes:
                other_lang_names[code] = _get_lang_cn(code)

        if has_other:
            # 旧方式：从 prompt 解析语言（手写兜底）
            if on_progress:
                on_progress("识别目标语言...", 1, 4)

            parsed_codes, cleaned_text = self._parse_other_lang_from_prompt(text)
            if parsed_codes:
                text = cleaned_text
                for code in parsed_codes:
                    if code not in other_lang_codes:
                        other_lang_codes.append(code)
                    if code not in other_lang_names:
                        other_lang_names[code] = _get_lang_cn(code)

        if has_other and not other_lang_codes:
            # 选了"其他"但没解析到语言，提示用户
            return {
                "skill": self.name,
                "reply": "❓ 未识别到目标语言。请在输入框中指定，例如：\n• 翻译成泰语：你好\n• 翻译成日语、韩语：欢迎使用\n• translate to thai: hello",
            }

        try:
            # ---- KB 语言：走正常三级匹配 ----
            kb_result = {}
            kb_mode = ""
            kb_result_raw = None
            kb_need_model = []
            if kb_target:
                if on_progress:
                    on_progress("知识库匹配中...", 1, 4)

                if conn is not None:
                    kb_result_raw = l.translate_one(
                        conn, ids, vecs, text,
                        target_langs=kb_target,
                    )
                    kb_result = kb_result_raw.get("translations", {})
                    kb_mode = kb_result_raw.get("mode", "")
                    kb_need_model = kb_result_raw.get("need_model", [])
                else:
                    kb_mode = "模型翻译（无知识库）"
                    kb_need_model = kb_target

            # ---- 其他语言：纯模型翻译（★ 支持多语言，用专用提示词提高质量）----
            other_result = {}
            if other_lang_codes:
                for idx, code in enumerate(other_lang_codes):
                    lang_display = other_lang_names.get(code, code)
                    if on_progress:
                        on_progress(f"AI翻译{lang_display}...", 2 + idx, 4 + len(other_lang_codes))

                    if l.online_api_is_configured():
                        try:
                            other_result[code] = self._translate_other_lang(l, text, code, lang_display)
                        except Exception as e:
                            other_result[code] = f"[翻译失败: {str(e)}]"
                    else:
                        other_result[code] = "[翻译失败: 需要在线模型支持]"

            # 合并结果
            all_translations = {**kb_result, **other_result}

            if on_progress:
                on_progress("翻译完成", 4, 4)

            # ★ 构建每语言来源信息
            translations_source = {}
            for lang in kb_target:
                translations_source[lang] = "model" if lang in kb_need_model else "kb"
            for code in other_lang_codes:
                translations_source[code] = "model"

            # 构建输出
            lines = [f"📝 「{text}」翻译结果：\n"]
            for lang in kb_target:
                lang_name = l.LANG_NAMES.get(lang, lang)
                trans = all_translations.get(lang, "")
                lines.append(f"  {lang_name}：{trans}")

            if other_lang_codes:
                for code in other_lang_codes:
                    trans = all_translations.get(code, "")
                    lang_display = other_lang_names.get(code, code)
                    lines.append(f"  {lang_display}：{trans} 🤖")

            # 模式说明
            mode_parts = []
            if kb_target:
                mode_parts.append(f"KB语言({'+'.join(kb_target)}): {kb_mode}")
            if other_lang_codes:
                for code in other_lang_codes:
                    lang_display = other_lang_names.get(code, code)
                    mode_parts.append(f"{lang_display}({code}): 纯模型翻译")
            lines.append(f"\n📊 模式：{' | '.join(mode_parts)}")

            # ★ 构建语言中文名映射（供前端显示，不再用代码回退）
            all_lang_names = {}
            for lang in kb_target:
                all_lang_names[lang] = l.LANG_NAMES.get(lang, lang)
            if other_lang_codes:
                for code in other_lang_codes:
                    all_lang_names[code] = other_lang_names.get(code, code)

            return {
                "skill": self.name,
                "reply": "\n".join(lines),
                "data": {
                    "translations": all_translations,
                    "translations_source": translations_source,
                    "lang_names": all_lang_names,
                    "kb_langs": kb_target,
                    "other_langs": other_lang_codes if has_other else [],
                    "source_text": text,
                    "mode": kb_mode if kb_mode else "纯模型翻译",
                    "similarity": kb_result_raw.get("similarity") if kb_result_raw else None,
                    "matched_zh": kb_result_raw.get("matched_zh") if kb_result_raw else None,
                },
            }

        except Exception as e:
            import traceback
            traceback.print_exc()
            return {"skill": self.name, "reply": f"❌ 翻译失败: {str(e)}", "error": str(e)}

    # ==================== 文件翻译（5步流程） ====================

    def _handle_file_translate(self, files: list[str], options: dict, on_progress=None) -> dict:
        """
        文件翻译5步流程：

        1. 理解文件 — 提取文本，分析结构
        2. 翻译文件 — 逐条翻译（KB语言走知识库，其他语言走5步提示词+模型）
        3. 简练译文 — 去冗余，保持简洁

        ★ v2.16 合并了旧版5步为3步（理解→翻译→简练+复查）
        ★ v2.20 支持多语言并发翻译
        ★ target_langs 中的 "other" → 从 options 的 message 中解析语言

        Args:
            files: 文件路径列表（仅取第一个文件）
            options: 选项字典，包含 target_langs、message 等
            on_progress: 进度回调函数

        Returns:
            dict: 包含翻译结果、输出文件路径、统计信息等
        """
        if not files:
            return {"skill": self.name, "reply": "请上传文件"}

        filepath = files[0]
        if not os.path.isfile(filepath):
            return {"skill": self.name, "reply": f"❌ 文件不存在: {filepath}"}

        ext = os.path.splitext(filepath)[1].lower()
        supported = [".docx", ".pptx", ".xlsx"]
        if ext not in supported:
            return {"skill": self.name, "reply": f"❌ 不支持此文件格式: {ext}，支持: {', '.join(supported)}"}

        target_langs = options.get("target_langs", ["en"])
        # ★ 离线翻译已移除（v2.18），所有翻译统一走在线API

        l, conn, ids, vecs = _get_cached_resources()
        if l is None:
            return {"skill": self.name, "reply": "❌ 翻译引擎未加载", "error": "lib.py import failed"}

        # ★ 分离 KB 语言和"其他语言"（支持两种交互方式）
        # 方式1：target_langs 直接含非KB语言代码（如 ja, ko, th）→ 自动识别（前端子选单勾选）
        # 方式2：target_langs 含 "other" → 从 prompt 解析（手写兜底）
        has_other = "other" in target_langs
        kb_langs = [lang for lang in target_langs if lang != "other" and lang in l.TRANSLATE_LANGS]
        direct_other_codes = [lang for lang in target_langs if lang != "other" and lang not in l.TRANSLATE_LANGS]
        other_lang_codes = []
        other_lang_names = {}

        def _get_lang_cn(code: str) -> str:
            """反查语言中文名：别名表 → LANG_NAMES → 代码回退"""
            for cn_name, c in self._LANG_ALIAS_MAP.items():
                if c == code:
                    return cn_name
            return l.LANG_NAMES.get(code, code)

        # ★ 直接传入的非KB语言（前端子选单勾选）
        if direct_other_codes:
            other_lang_codes = list(direct_other_codes)
            for code in other_lang_codes:
                other_lang_names[code] = _get_lang_cn(code)

        # ★ "other" 从 prompt 解析（手写兜底）
        if has_other:
            message = options.get("message", "") or options.get("_prompt", "")
            if message:
                parsed_codes, _ = self._parse_other_lang_from_prompt(message)
                for code in parsed_codes:
                    if code not in other_lang_codes:
                        other_lang_codes.append(code)
                    if code not in other_lang_names:
                        other_lang_names[code] = _get_lang_cn(code)

        if has_other and not other_lang_codes:
            return {
                "skill": self.name,
                "reply": "❓ 文件翻译需指定目标语言。请在输入框中写明，例如：翻译成泰语 或 翻译成日语、韩语",
            }

        # 最终翻译语言列表
        final_langs = kb_langs[:]
        if other_lang_codes:  # ★ 不再限定 has_other，直接传入的语言代码也要加入
            for code in other_lang_codes:
                if code not in final_langs:
                    final_langs.append(code)

        if not final_langs:
            return {"skill": self.name, "reply": "❌ 请至少选择一种目标语言"}

        try:
            # =====================================================
            # 第1步：理解文件
            # =====================================================
            if on_progress:
                on_progress("第1步/3：理解文件结构...", 1, 3)

            # 调用 lib 层提取文件中的可翻译文本
            texts = l.file_extract_texts(filepath, ext)
            if not texts:
                return {"skill": self.name, "reply": "❌ 文件中没有可翻译的文本"}

            total_texts = len(texts)
            print(f"  [文件翻译] 提取到 {total_texts} 条文本")

            # ★ 总结文件语境，供返回数据使用
            try:
                file_context = self._summarize_file_context(texts)
            except Exception:
                file_context = ""

            # =====================================================
            # 第2步：翻译文件（v2.16 合并旧2+3步，4.7-flash直翻不需要先总结）
            # ★ KB 语言走知识库匹配，其他语言走模型直翻
            # =====================================================
            # ★ 动态步骤名：显示实际翻译的语言，而不是笼统的"其他语言"
            _step2_lang_names = []
            if kb_langs:
                _step2_lang_names.append(f"KB语言({'+'.join(kb_langs)})")
            if other_lang_codes:
                _step2_lang_names.append('+'.join(other_lang_names.get(c, c) for c in other_lang_codes))
            _step2_label = '、'.join(_step2_lang_names) if _step2_lang_names else '翻译'

            if on_progress:
                on_progress(f"第2步/3：翻译{_step2_label}（0/{total_texts}）...", 2, 3)

            final_translations = None
            kb_hits = 0
            model_hits = 0

            # ---- KB 语言翻译 ----
            if kb_langs and conn is not None:
                for progress_data in l.translate_file_texts(
                    texts=texts,
                    target_langs=kb_langs,
                    conn=conn, ids=ids, vecs=vecs,
                ):
                    all_trans, kb_h, model_h, done, total = progress_data
                    final_translations = all_trans
                    kb_hits = kb_h
                    model_hits = model_h

                    if on_progress:
                        pct = int(done / total * 100) if total > 0 else 0
                        on_progress(f"第2步/3：翻译{_step2_label}（{done}/{total}）{pct}%", 2, 3)

            # ---- 其他语言翻译（★ v2.20: 多语言并发 + 每语言内批内并发） ----
            if other_lang_codes and l.online_api_is_configured():
                if final_translations is None:
                    final_translations = {t: {} for t in texts}

                other_total = len(texts) * len(other_lang_codes)
                _lang_done_count = {}  # 每种语言已完成的文本数

                # ★ 单语言翻译函数（供并发调用）
                def _translate_lang(ocode, odisplay):
                    """翻译一种语言，返回 (ocode, batch_results)"""
                    # ★ 批量翻译回调：更新进度
                    def _on_batch_done(bidx, btotal, done, total):
                        _lang_done_count[ocode] = done
                        if on_progress:
                            # ★ 汇总所有语言的进度
                            total_done = sum(_lang_done_count.values())
                            pct = int(total_done / other_total * 100) if other_total > 0 else 0
                            on_progress(f"第2步/3：翻译{_step2_label}（{total_done}/{other_total}）{pct}%", 2, 3)

                    batch_results = l.call_online_llm_single_lang_batch(
                        zh_texts=texts,
                        target_lang=ocode,
                        examples=[],
                        batch_size=15,
                        on_batch_done=_on_batch_done,
                    )
                    return (ocode, batch_results)

                # ★ 多语言并发（ThreadPoolExecutor，最多2种语言同时翻译）
                from concurrent.futures import ThreadPoolExecutor, as_completed
                _lang_results = {}
                with ThreadPoolExecutor(max_workers=min(len(other_lang_codes), 2)) as executor:
                    futures = {}
                    for ocode in other_lang_codes:
                        odisplay = other_lang_names.get(ocode, ocode)
                        future = executor.submit(_translate_lang, ocode, odisplay)
                        futures[future] = ocode

                    for future in as_completed(futures):
                        ocode = futures[future]
                        try:
                            result_ocode, batch_results = future.result()
                        except Exception as e:
                            print(f"[文件翻译] {ocode} 语言翻译异常: {e}")
                            batch_results = ["[翻译失败]"] * len(texts)
                            result_ocode = ocode

                        # ★ 写入结果
                        for text_idx, zh_text in enumerate(texts):
                            if text_idx < len(batch_results):
                                trans = batch_results[text_idx]
                            else:
                                trans = "[翻译失败]"
                            if zh_text not in final_translations:
                                final_translations[zh_text] = {}
                            final_translations[zh_text][result_ocode] = trans
                            if trans and not trans.startswith("[翻译失败"):
                                model_hits += 1

            if not final_translations:
                return {"skill": self.name, "reply": "❌ 翻译结果为空"}

            # =====================================================
            # 第3步：简练译文 + 语法复查（v2.16 合并旧4+5步）
            # ★ 4.7-flash 直翻跳过复查——复查可能改坏正确译文
            # ★ translate_file_texts 内部的复查已按 is_47_direct 跳过，
            #   这里只处理其他语言（call_online_llm_single_lang 直翻的）
            # =====================================================
            if on_progress:
                on_progress("第3步/3：简练译文+语法复查...", 3, 3)

            # 先清洗语言名前缀（如"英文：xxx" → "xxx"）
            for text_key, trans_dict in final_translations.items():
                for lang, trans in trans_dict.items():
                    if trans and not trans.startswith("[翻译失败"):
                        trans = self._strip_lang_prefix(trans, lang)
                        final_translations[text_key][lang] = trans

            # =====================================================
            # 写回文件 + 修正排版
            # =====================================================
            if on_progress:
                on_progress("写回文件+修正排版...", 3, 3)

            output_files = []
            base_name = os.path.splitext(os.path.basename(filepath))[0]
            output_dir = os.path.join(os.path.dirname(filepath), "translated")

            # 为每种语言生成独立的翻译文件
            for lang in final_langs:
                lang_trans = {}
                for text_key, trans_dict in final_translations.items():
                    lang_trans[text_key] = trans_dict.get(lang, "")

                out_path = os.path.join(output_dir, f"{base_name}_{lang}{ext}")
                os.makedirs(output_dir, exist_ok=True)

                l.file_apply_translations(filepath, out_path, lang_trans, ext)
                output_files.append(out_path)

            # 构建语言名列表
            lang_name_list = []
            for lang in kb_langs:
                lang_name_list.append(l.LANG_NAMES.get(lang, lang))
            if other_lang_codes:
                for code in other_lang_codes:
                    lang_name_list.append(f"{other_lang_names.get(code, code)}(🤖)")

            lang_names = "、".join(lang_name_list)
            reply = (
                f"✅ 文件翻译完成！\n"
                f"  📄 共 {total_texts} 条文本\n"
                f"  🌐 {len(final_langs)} 种语言：{lang_names}\n"
                f"  ✅ 知识库命中 {kb_hits} 条，模型翻译 {model_hits} 条\n\n"
                f"点击下方链接下载："
            )

            # ★ 构建语言中文名映射（供前端显示，不用代码回退）
            all_lang_names = {}
            for lang in kb_langs:
                all_lang_names[lang] = l.LANG_NAMES.get(lang, lang)
            if other_lang_codes:
                for code in other_lang_codes:
                    all_lang_names[code] = other_lang_names.get(code, code)

            return {
                "skill": self.name,
                "reply": reply,
                "data": {
                    "total_texts": total_texts,
                    "target_langs": final_langs,
                    "lang_names": all_lang_names,
                    "kb_hits": kb_hits,
                    "model_hits": model_hits,
                    "file_context": file_context[:200] if file_context else "",
                },
                "files": output_files,
            }

        except Exception as e:
            import traceback
            traceback.print_exc()
            return {"skill": self.name, "reply": f"❌ 文件翻译失败: {str(e)}", "error": str(e)}

    # ==================== 辅助方法 ====================

    def _summarize_file_context(self, texts: list[str]) -> str:
        """
        用在线模型总结文件内容和语境

        将文件前50条文本拼接后发给LLM，获取1~2句话的核心主题总结，
        用于帮助用户了解文件整体语境。

        Args:
            texts: 文件中的文本片段列表

        Returns:
            str: 总结文本，失败时返回空字符串
        """
        if not texts:
            return ""
        l = _ensure_lib()
        if l is None:
            return ""

        all_text = "\n".join(texts[:50])
        if len(all_text) > 2000:
            all_text = all_text[:2000]

        try:
            cfg = l._get_online_config()
            url = f"{cfg['base_url'].rstrip('/')}/chat/completions"
            headers = {
                "Authorization": f"Bearer {cfg['api_key']}",
                "Content-Type": "application/json",
            }
            payload = {
                "model": cfg["model"],
                "messages": [
                    {
                        "role": "system",
                        "content": "你是一个文件内容分析助手。请用1-2句话总结以下文本的核心主题和语境，重点指出省略的主语、宾语等隐含信息。只输出总结，不要翻译。"
                    },
                    {
                        "role": "user",
                        "content": all_text
                    }
                ],
                "temperature": 0.1,
                "max_tokens": 256,
                "enable_thinking": False,
            }

            r = l.requests.post(url, headers=headers, json=payload, timeout=30)
            r.raise_for_status()
            r_json = r.json()
            content = l._extract_content(r_json)
            if content:
                print(f"  [文件摘要] {content[:100]}")
                return content.strip()
        except Exception as e:
            print(f"  [文件摘要] 生成失败: {e}")

        return ""

    def _strip_lang_prefix(self, text: str, lang: str) -> str:
        """
        去除译文前的语言名前缀

        有些LLM会在译文前自动加上语言名（如"英文：Hello"），
        此函数通过正则匹配去除这类前缀。

        Args:
            text: 译文文本
            lang: 语言代码

        Returns:
            str: 去除前缀后的文本
        """
        import re
        l = _ensure_lib()
        lang_name = l.LANG_NAMES.get(lang, "") if l else ""
        # ★ 也查别名表
        if not lang_name:
            for cn_name, code in self._LANG_ALIAS_MAP.items():
                if code == lang:
                    lang_name = cn_name
                    break

        # 构建中文语言名前缀匹配模式
        patterns = [
            rf'^{re.escape(lang_name)}\s*[：:]\s*',
            rf'^{re.escape(lang_name)}\s+',
        ]
        # 常见语言的本土名称/英文名称前缀
        en_names = {
            "en": ["English", "english", "Eng"],
            "ru": ["Russian", "russian", "Русский"],
            "ar": ["Arabic", "arabic", "العربية"],
            "es": ["Spanish", "spanish", "Español"],
            "pt": ["Portuguese", "portuguese", "Português"],
            "fr": ["French", "french", "Français"],
            "de": ["German", "german", "Deutsch"],
            "kk": ["Kazakh", "kazakh", "Қазақ"],
            "zh_hant": ["繁體中文", "Traditional Chinese"],
            # ★ 补充其他语言
            "th": ["Thai", "thai", "ไทย"],
            "vi": ["Vietnamese", "vietnamese", "Tiếng Việt"],
            "ms": ["Malay", "malay", "Bahasa Melayu"],
            "it": ["Italian", "italian", "Italiano"],
            "pl": ["Polish", "polish", "Polski"],
            "sv": ["Swedish", "swedish", "Svenska"],
            "nl": ["Dutch", "dutch", "Nederlands"],
            "ko": ["Korean", "korean", "한국어"],
            "ja": ["Japanese", "japanese", "日本語"],
        }
        for en_name in en_names.get(lang, []):
            patterns.append(rf'^{re.escape(en_name)}\s*[：:]\s*')
            patterns.append(rf'^{re.escape(en_name)}\s+')

        for pat in patterns:
            text = re.sub(pat, '', text)

        return text.strip()

    # ==================== KB 上传 + 自动向量化 ====================

    def upload_knowledge_base(self, filepath: str) -> dict:
        """
        上传翻译知识库文件（CSV/Excel），解析后写入 SQLite 并重建向量索引。

        ★ 保留旧接口兼容，内部拆为 recognize + import 两步

        Args:
            filepath: 知识库文件路径

        Returns:
            dict: 包含 success、message、added 等字段的结果字典
        """
        rec = self.recognize_kb(filepath)
        if not rec.get("success"):
            return rec
        return self.import_kb(rec["temp_id"])

    def recognize_kb(self, filepath: str) -> dict:
        """
        ★ 第一步：识别翻译知识库文件，解析预览但不写入数据库。

        解析文件内容，识别语言列，生成预览数据，并将元信息缓存到临时文件。

        Args:
            filepath: 知识库文件路径

        Returns:
            dict: 包含以下字段：
                - success: bool，是否识别成功
                - message: str，提示信息
                - total: int，数据行数
                - lang_cols: list，识别到的语言列名
                - new_langs: list，新增的语言（还不存在于 TRANSLATE_LANGS）
                - temp_id: str，临时缓存ID（供 import_kb 使用）
                - preview: list，预览数据（前5条）
        """
        l = _ensure_lib()
        if l is None:
            return {"success": False, "message": "❌ 翻译引擎未加载"}

        ext = os.path.splitext(filepath)[1].lower()
        if ext not in (".csv", ".xlsx", ".xls"):
            return {"success": False, "message": f"❌ 不支持的格式: {ext}，请上传 CSV 或 Excel"}

        try:
            rows = self._parse_kb_file(filepath, ext)
            if not rows:
                return {"success": False, "message": "❌ 文件中没有有效数据（请确认首行含 zh 列名）"}

            first_row = rows[0]
            # ★ 只保留语言列：2~3字母ISO 639代码 + zh_hant特殊代码，排除"模块"等非语言列
            lang_cols = [col for col in first_row.keys() if col != "zh" and col != "zh_hash" and self._is_language_col(col)]
            new_langs = [lang for lang in lang_cols if lang not in l.TRANSLATE_LANGS]

            # ★ 生成临时ID，缓存解析结果
            import uuid, json, tempfile
            temp_id = uuid.uuid4().hex[:12]
            temp_path = os.path.join(tempfile.gettempdir(), f"rox_kb_{temp_id}.json")
            with open(temp_path, "w", encoding="utf-8") as f:
                json.dump({"filepath": filepath, "rows_count": len(rows), "lang_cols": lang_cols}, f)

            # ★ 预览前5条
            preview = []
            for row in rows[:5]:
                zh = row.get("zh", "").strip()
                trans = {lang: row.get(lang, "").strip() for lang in lang_cols if row.get(lang, "").strip()}
                if zh or trans:
                    preview.append({"zh": zh, **trans})

            return {
                "success": True,
                "message": f"✅ 识别成功！共 {len(rows)} 条数据，{len(lang_cols)} 种语言",
                "total": len(rows),
                "lang_cols": lang_cols,
                "new_langs": new_langs,
                "temp_id": temp_id,
                "preview": preview,
            }

        except Exception as e:
            import traceback
            traceback.print_exc()
            return {"success": False, "message": f"❌ 识别失败: {str(e)}"}

    def import_kb(self, temp_id: str) -> dict:
        """
        ★ 第二步：将已识别的翻译数据导入知识库（写入SQLite + 向量化 + 建索引）。

        从临时缓存读取元信息，解析原始文件，将每行数据写入 SQLite，
        重建向量索引，并动态更新 TRANSLATE_LANGS。

        Args:
            temp_id: recognize_kb 返回的临时缓存 ID

        Returns:
            dict: 包含 success、message、added、kb_langs、new_langs、lang_cols 等字段
        """
        import json, tempfile
        l = _ensure_lib()
        if l is None:
            return {"success": False, "message": "❌ 翻译引擎未加载", "added": 0}

        # ★ 读取临时缓存
        temp_path = os.path.join(tempfile.gettempdir(), f"rox_kb_{temp_id}.json")
        if not os.path.isfile(temp_path):
            return {"success": False, "message": "❌ 识别数据已过期，请重新上传识别", "added": 0}

        try:
            with open(temp_path, "r", encoding="utf-8") as f:
                meta = json.load(f)
            filepath = meta["filepath"]
            lang_cols = meta["lang_cols"]

            ext = os.path.splitext(filepath)[1].lower()
            rows = self._parse_kb_file(filepath, ext)
            if not rows:
                return {"success": False, "message": "❌ 文件数据为空", "added": 0}

            new_langs = [lang for lang in lang_cols if lang not in l.TRANSLATE_LANGS]

            conn = _get_cached_resources()[1]
            if conn is None:
                conn = l.get_db()
                global _db_conn
                _db_conn = conn

            # ★ 写入数据库（逐步进度）
            added = 0
            total = len(rows)
            for i, row in enumerate(rows):
                zh_text = row.get("zh", "").strip()
                if not zh_text:
                    continue
                translations = {lang: row.get(lang, "").strip() for lang in lang_cols if row.get(lang, "").strip()}
                if translations:
                    l.save_back(conn, zh_text, translations)
                    added += 1

            # ★ 重建向量索引
            self._rebuild_index(l, conn)

            global _index_ids, _index_vecs
            _index_ids = None
            _index_vecs = None

            # 如果有新增语言，动态更新 TRANSLATE_LANGS
            if new_langs:
                updated_kb_langs = list(l.TRANSLATE_LANGS) + new_langs
                l.TRANSLATE_LANGS = updated_kb_langs
                l.ALL_LANGS = updated_kb_langs + [lang for lang in l.ALL_LANGS if lang not in updated_kb_langs]

            # ★ 清理临时文件
            try: os.remove(temp_path)
            except: pass

            kb_langs = list(l.TRANSLATE_LANGS)
            return {
                "success": True,
                "message": f"✅ 导入成功！写入 {added} 条翻译，{len(lang_cols)} 种语言",
                "added": added,
                "kb_langs": kb_langs,
                "new_langs": new_langs,
                "lang_cols": lang_cols,
            }

        except Exception as e:
            import traceback
            traceback.print_exc()
            return {"success": False, "message": f"❌ 导入失败: {str(e)}", "added": 0}

    # ★ 常见3字母非语言列名黑名单（在翻译表格中可能是其他含义，如 key=键值, ref=引用）
    _NON_LANG_3LETTER = frozenset({
        "key", "ref", "tag", "seq", "num", "idx", "cnt", "val", "txt", "str",
        "src", "url", "uri", "len", "max", "min", "app", "env", "cfg", "tmp",
        "log", "err", "msg", "cmd", "arg", "opt", "var", "fun", "obj", "res",
        "dat", "img", "vid", "doc", "pdf", "csv", "xml", "sql", "api", "lib",
        "pkg", "mod", "cls", "ext", "ver", "rev", "old", "new", "pri", "pub",
        "pro", "dev", "org", "usr", "grp", "asc", "desc", "pid", "uid", "gid",
    })

    @staticmethod
    def _is_language_col(col_name: str) -> bool:
        """
        ★ 判断标准化后的列名是否为语言代码。

        规则：
        - 2字母ASCII小写 = ISO 639-1 语言代码（en, ru, id 等），不限语种
        - 3字母ASCII小写 = ISO 639-2 语言代码，但排除 _NON_LANG_3LETTER 黑名单
        - zh_hant / zh-hant 等特殊变体代码
        - 其他非语言列名（如"模块"、"分类"、"备注"、"序号"等）返回 False

        Args:
            col_name: 列名字符串（可能含空格）

        Returns:
            bool: 是否为语言列
        """
        c = col_name.strip().lower()
        # 2字母ASCII小写 = ISO 639-1 语言代码（全部是真实语言，直接放行）
        if len(c) == 2 and c.isascii() and c.isalpha():
            return True
        # 3字母ASCII小写 = ISO 639-2 语言代码，但排除常见非语言缩写
        if len(c) == 3 and c.isascii() and c.isalpha() and c not in TranslationSkill._NON_LANG_3LETTER:
            return True
        # zh_hant 等特殊变体代码
        if c in ("zh_hant", "zh-hant", "zhhant"):
            return True
        return False

    # ★ 列名标准化映射：中文/英文别名 → 标准语言代码
    # 规则：单字母/双字母语言代码（如 en, ru）不转换，仅对中文别名和长英文名做映射
    _COL_ALIAS_MAP: dict[str, str] = {
        # 中文
        "中文": "zh", "中文原文": "zh", "原文": "zh", "源文": "zh", "源语言": "zh",
        "英文": "en", "英语": "en", "英式英语": "en", "美式英语": "en",
        "俄文": "ru", "俄语": "ru",
        "阿拉伯文": "ar", "阿拉伯语": "ar",
        "西班牙文": "es", "西班牙语": "es",
        "葡萄牙文": "pt", "葡萄牙语": "pt",
        "法文": "fr", "法语": "fr",
        "哈萨克文": "kk", "哈萨克语": "kk",
        "德文": "de", "德语": "de",
        "繁体中文": "zh_hant", "繁体": "zh_hant", "繁体中文": "zh_hant",
        "日文": "ja", "日语": "ja",
        "韩文": "ko", "韩语": "ko",
        "泰文": "th", "泰语": "th",
        "意大利文": "it", "意大利语": "it",
        "土耳其文": "tr", "土耳其语": "tr",
        "波兰文": "pl", "波兰语": "pl",
        "荷兰文": "nl", "荷兰语": "nl",
        "瑞典文": "sv", "瑞典语": "sv",
        "乌克兰文": "uk", "乌克兰语": "uk",
        "越南文": "vi", "越南语": "vi",
        "印尼文": "id", "印尼语": "id",
        "马来文": "ms", "马来语": "ms",
        "希伯来文": "he", "希伯来语": "he",
        "印地文": "hi", "印地语": "hi",
        "波斯文": "fa", "波斯语": "fa",
        "孟加拉文": "bn", "孟加拉语": "bn",
        "罗马尼亚文": "ro", "罗马尼亚语": "ro",
        "捷克文": "cs", "捷克语": "cs",
        "匈牙利文": "hu", "匈牙利语": "hu",
        "希腊文": "el", "希腊语": "el",
        "芬兰文": "fi", "芬兰语": "fi",
        "丹麦文": "da", "丹麦语": "da",
        "挪威文": "no", "挪威语": "no",
        # 英文别名（常见的全称）
        "english": "en", "chinese": "zh", "russian": "ru",
        "arabic": "ar", "spanish": "es", "portuguese": "pt",
        "french": "fr", "kazakh": "kk", "german": "de",
        "traditional chinese": "zh_hant", "japanese": "ja",
        "korean": "ko", "thai": "th", "italian": "it",
        "turkish": "tr", "polish": "pl", "dutch": "nl",
        "swedish": "sv", "ukrainian": "uk", "vietnamese": "vi",
        "indonesian": "id", "malay": "ms", "hebrew": "he",
        "hindi": "hi", "persian": "fa", "bengali": "bn",
        "romanian": "ro", "czech": "cs", "hungarian": "hu",
        "greek": "el", "finnish": "fi", "danish": "da", "norwegian": "no",
        # 大写语言代码
        "EN": "en", "RU": "ru", "AR": "ar", "ES": "es", "PT": "pt",
        "FR": "fr", "KK": "kk", "DE": "de", "JA": "ja", "KO": "ko",
        "TH": "th", "IT": "it", "TR": "tr", "ZH": "zh",
    }

    def _normalize_col_names(self, headers: list[str]) -> tuple[list[str], dict[str, str]]:
        """
        ★ 标准化列名：把中文别名、大写代码等映射为标准语言代码。

        处理步骤：
        1. 精确匹配 _COL_ALIAS_MAP
        2. 已是标准2~3字母小写代码，保留
        3. zh_hant 特殊变体处理
        4. 不匹配的，原样保留

        Args:
            headers: 原始列名列表

        Returns:
            tuple: (标准化后的列名列表, 映射记录 {原名: 标准名})
        """
        result = []
        mapping = {}
        for h in headers:
            h_stripped = h.strip()
            # 1. 精确匹配别名表
            if h_stripped in self._COL_ALIAS_MAP:
                std = self._COL_ALIAS_MAP[h_stripped]
                result.append(std)
                mapping[h_stripped] = std
            # 2. 已经是标准2~3字母代码（小写），保留
            elif len(h_stripped) in (2, 3) and h_stripped.isalpha() and h_stripped == h_stripped.lower():
                result.append(h_stripped)
            # 3. zh_hant 特殊处理
            elif h_stripped.lower() in ("zh_hant", "zh-hant", "zhhant"):
                result.append("zh_hant")
                mapping[h_stripped] = "zh_hant"
            # 4. 不匹配的，原样保留
            else:
                result.append(h_stripped)
        return result, mapping

    def _parse_kb_file(self, filepath: str, ext: str) -> list[dict]:
        """
        解析 CSV / Excel 文件，返回字典列表（列名自动标准化）

        Args:
            filepath: 文件路径
            ext: 文件扩展名（.csv / .xlsx / .xls）

        Returns:
            list[dict]: 解析后的行数据，每行为 {标准化列名: 值} 的字典
        """
        if ext == ".csv":
            import csv
            with open(filepath, "r", encoding="utf-8-sig") as f:
                reader = csv.DictReader(f)
                # ★ 读取原始行
                raw_rows = list(reader)
                if not raw_rows:
                    return []
                # ★ 标准化列名
                orig_headers = list(raw_rows[0].keys())
                std_headers, mapping = self._normalize_col_names(orig_headers)
                if mapping:
                    print(f"  [KB识别] 列名映射: {mapping}")
                # ★ 用标准化列名重建行
                result = []
                for row in raw_rows:
                    new_row = {}
                    for old_key, val in row.items():
                        std_key = mapping.get(old_key, old_key) if mapping else old_key
                        new_row[std_key] = val
                    if new_row.get("zh", "").strip():
                        result.append(new_row)
                return result
        else:
            try:
                import openpyxl
                wb = openpyxl.load_workbook(filepath, read_only=True)
                ws = wb.active
                rows_iter = ws.iter_rows(values_only=True)
                headers = next(rows_iter)
                headers = [str(h).strip() if h else "" for h in headers]
                # ★ 标准化列名
                std_headers, mapping = self._normalize_col_names(headers)
                if mapping:
                    print(f"  [KB识别] 列名映射: {mapping}")
                result = []
                for row in rows_iter:
                    d = {}
                    for i, val in enumerate(row):
                        if i < len(std_headers) and std_headers[i]:
                            d[std_headers[i]] = str(val) if val is not None else ""
                    if d.get("zh", "").strip():
                        result.append(d)
                wb.close()
                return result
            except ImportError:
                return []

    def _rebuild_index(self, l, conn):
        """
        重建向量索引

        从 tm_segments 表读取所有中文文本，调用 embedding 模型生成向量，
        保存到 .npz 文件并更新时间戳。需要 Ollama 运行；不可用时跳过，仅更新 DB。

        Args:
            l: lib 模块引用
            conn: SQLite 数据库连接
        """
        if not l.ollama_is_up():
            print("  [KB上传] Ollama 未运行，跳过向量索引重建（仅更新 DB）")
            return

        try:
            rows = conn.execute("SELECT id, zh FROM tm_segments").fetchall()
            if not rows:
                return

            ids_list = [r[0] for r in rows]
            texts = [r[1] for r in rows]

            vecs = l.embed_batch(texts, batch_size=8)
            ids_arr = __import__("numpy").array(ids_list, dtype=__import__("numpy").int64)

            __import__("numpy").savez(l.EMB_PATH, ids=ids_arr, vecs=vecs)
            l.INDEX_STAMP.touch()
            print(f"  [KB上传] 向量索引重建完成，共 {len(ids_list)} 条")

        except Exception as e:
            print(f"  [KB上传] 向量索引重建失败: {e}")

    # ==================== ★ 结构化知识库（segment_base）管理 ====================

    def build_segments(self) -> dict:
        """
        遍历 tm_segments，用 LLM 将长句拆解为独立短句并写入知识库。

        调用 lib.split_long_entries 对翻译记忆库中的长句进行拆分，
        将可拆解的句子拆为多个独立短句（含对应的多语言翻译），
        审计通过后写入 tm_segments，提高知识库复用率。

        Returns:
            dict: 包含 success、total_added、total_failed、message 字段的结果字典
        """
        l = _ensure_lib()
        conn = _get_cached_resources()[1]
        if l is None or conn is None:
            return {"success": False, "total_added": 0, "message": "❌ 翻译引擎或数据库未加载"}
        try:
            result = l.split_long_entries(conn)
            return result
        except Exception as e:
            import traceback
            traceback.print_exc()
            return {"success": False, "total_added": 0, "message": f"❌ 拆分失败: {str(e)}"}

    def kb_stats(self) -> dict:
        """
        获取知识库统计信息：TM 条目数、各语言条目数、segment_base 片段数。

        用于前端展示知识库的健康状态和数据量。

        Returns:
            dict: 包含 success、tm（各语言条目数详情）、segment_base（片段数）的统计字典
        """
        l = _ensure_lib()
        conn = _get_cached_resources()[1]
        if l is None or conn is None:
            return {"success": False, "message": "❌ 翻译引擎或数据库未加载"}

        stats = {"success": True}
        try:
            tm = l.tm_stats(conn)
            stats["tm"] = tm
        except Exception as e:
            stats["tm"] = {"error": str(e)}

        try:
            seg_count = conn.execute("SELECT COUNT(*) FROM segment_base").fetchone()[0]
            stats["segment_base"] = seg_count
        except Exception:
            stats["segment_base"] = 0

        return stats
