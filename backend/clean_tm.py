"""
TM知识库清洗脚本

流程：
  1. 混合污染清洗：对中外文夹杂的翻译，LLM判断可删的中文部分，清洗。
  2. 纯中文条目处理：三层对照（同行→KB提取→语言识别），归位/清除。
  3. 翻译列移动：根据阶段2结果执行实际列移动。
  4. 全量KB验证：对每条的每列做LLM语言+翻译核对。
  5. 修复执行：根据验证结果修复。

全程LLM只做分类/判断/校对，不生成翻译。
"""

import sqlite3
import json
import re
import os
import sys
import time
import requests
from datetime import datetime
from pathlib import Path

# ---- 数据库与常量配置 ----
_USER_DATA_DIR = os.getenv("USER_DATA_DIR", str(Path.home() / "Library" / "Application Support" / "翻译助手"))
DB_PATH = os.path.join(_USER_DATA_DIR, "tm.sqlite3")  # TM数据库文件路径
# 全部支持的语言列代码列表
LANG_COLS = ['en', 'ru', 'ar', 'es', 'pt', 'fr', 'kk', 'de', 'zh_hant',
             'ms', 'id_lang', 'th', 'tr', 'it', 'pl', 'sv']
# 语言列代码 → 中文名称映射
LANG_NAMES = {
    'en': '英语', 'ru': '俄语', 'ar': '阿拉伯语', 'es': '西班牙语',
    'pt': '葡萄牙语', 'fr': '法语', 'kk': '哈萨克语', 'de': '德语',
    'zh_hant': '繁体中文', 'ms': '马来语', 'id_lang': '印尼语',
    'th': '泰语', 'tr': '土耳其语', 'it': '意大利语', 'pl': '波兰语', 'sv': '瑞典语'
}
# 反向映射：中文名称→列代码
NAME_TO_COL = {v: k for k, v in LANG_NAMES.items()}
# 非翻译语言列（0填充的列不计入核验）—— 实际活跃可用的语言列
ACTIVE_COLS = ['en', 'ru', 'ar', 'es', 'pt', 'fr', 'kk', 'de', 'zh_hant', 'id_lang']

# ─── API配置（与main.py保持一致） ───
# 如果没有设置环境变量，使用默认值
if not os.getenv("SILICONFLOW_API_KEY"):
    os.environ["SILICONFLOW_API_KEY"] = "sk-ugkqobwooolhdmykmycxuxyizcsnysudgmvuhkpinetuuxvq"
if not os.getenv("SILICONFLOW_API_BASE"):
    os.environ["SILICONFLOW_API_BASE"] = "https://api.siliconflow.cn/v1"
if not os.getenv("ONLINE_MODEL"):
    os.environ["ONLINE_MODEL"] = "tencent/Hunyuan-MT-7B"

API_BASE = os.getenv("SILICONFLOW_API_BASE")  # API 基础地址
API_KEY = os.getenv("SILICONFLOW_API_KEY")     # API 密钥
# 清洗核对用指令遵循更好的模型（非翻译模型）
CLEAN_MODEL = os.getenv("CLEAN_MODEL", "Qwen/Qwen2.5-32B-Instruct")
TRANSLATE_MODEL = os.getenv("ONLINE_MODEL", "tencent/Hunyuan-MT-7B")  # 翻译模型
API_TIMEOUT = int(os.getenv("ONLINE_TIMEOUT", "180"))  # API 调用超时时间（秒）


def log(msg):
    """
    带时间戳的日志输出，立即刷新缓冲区确保实时显示
    """
    t = datetime.now().strftime("%H:%M:%S")
    print(f"[{t}] {msg}")
    sys.stdout.flush()


def call_llm(messages, max_tokens=1024, temperature=0.0, retries=3, is_translation=False):
    """
    调用在线LLM（OpenAI兼容格式），返回content字符串

    Args:
        messages: 对话消息列表
        max_tokens: 最大输出token数
        temperature: 温度参数（0=确定性输出）
        retries: 失败重试次数
        is_translation: 是否使用翻译模型（否则使用清洗模型）

    Returns:
        LLM返回的文本内容（已strip）

    Raises:
        所有重试都失败时抛出最后的异常
    """
    model = TRANSLATE_MODEL if is_translation else CLEAN_MODEL
    url = f"{API_BASE.rstrip('/')}/chat/completions"
    headers = {
        "Authorization": f"Bearer {API_KEY}",
        "Content-Type": "application/json",
    }
    payload = {
        "model": model,
        "messages": messages,
        "temperature": temperature,
        "max_tokens": max_tokens,
    }
    # 重试延迟策略：逐次增加等待时间
    delays = [2, 5, 15]
    last_err = None
    for attempt in range(retries + 1):
        try:
            r = requests.post(url, headers=headers, json=payload, timeout=API_TIMEOUT)
            r.raise_for_status()
            data = r.json()
            content = data.get("choices", [{}])[0].get("message", {}).get("content", "")
            if content and content.strip():
                return content.strip()
            log(f"[warn] LLM返回空content: {json.dumps(data, ensure_ascii=False)[:200]}")
        except Exception as e:
            last_err = e
            log(f"[warn] LLM调用尝试{attempt+1}失败: {e}")
            if attempt < retries:
                delay = delays[attempt] if attempt < len(delays) else 30
                log(f"  等待{delay}秒后重试...")
                time.sleep(delay)
    raise last_err or Exception("LLM调用失败")


def call_llm_json(messages, max_tokens=1024, temperature=0.0, retries=3, is_translation=False):
    """
    调用LLM并解析JSON返回

    支持直接从响应内容或Markdown代码块 ```json ``` 中提取JSON，
    兼容单对象和数组格式的返回。

    Args:
        参数同 call_llm

    Returns:
        解析后的Python对象(dict/list)，解析失败返回None
    """
    content = call_llm(messages, max_tokens, temperature, retries, is_translation)
    # 尝试直接解析
    content = content.strip()
    # 尝试从 markdown 代码块中提取 JSON
    m = re.search(r'```(?:json)?\s*\n?([\s\S]*?)```', content)
    if m:
        content = m.group(1).strip()
    try:
        return json.loads(content)
    except json.JSONDecodeError:
        # 尝试从花括号中提取 JSON 对象
        m = re.search(r'\{[\s\S]*\}', content)
        if m:
            try:
                return json.loads(m.group())
            except:
                pass
        # 尝试从中括号中提取 JSON 数组
        m2 = re.search(r'\[[\s\S]*\]', content)
        if m2:
            try:
                return json.loads(m2.group())
            except:
                pass
        log(f"[error] JSON解析失败: {content[:300]}")
        return None


def has_chinese(s):
    """
    判断字符串是否包含中文字符（CJK统一表意文字及扩展区）
    """
    return bool(re.search(r'[\u4e00-\u9fff\u3400-\u4dbf\uf900-\ufaff]', s))


def is_pure_chinese(s):
    """
    判断字符串是否"纯中文"（去除标点/空格/数字后，中文字符占比≥50%）

    用于检测翻译列中错误复制的中文文本。
    """
    if not s or not s.strip():
        return False
    text = s.strip().replace('\u200b', '').replace('\xa0', ' ').replace('\n', ' ').replace('\r', ' ')
    # 移除标点、空格、数字、特殊符号等非文字字符
    cleaned = re.sub(r'[\s.,;:!?\'\"\-()\[\]{}<>/\\@#\$%^&*+=|~`，。；：！？""''（）【】《》/、&·\d]', '', text)
    if not cleaned:
        return False
    chinese_chars = re.findall(r'[\u4e00-\u9fff\u3400-\u4dbf\uf900-\ufaff]', cleaned)
    return len(chinese_chars) >= len(cleaned) * 0.5


def has_mixed_chinese(s):
    """
    判断字符串是否中英文混合（同时包含CJK汉字和拉丁字母）

    用于检测翻译列中的"污染"条目。
    """
    if not s or not s.strip():
        return False
    text = s.strip()
    latin_count = len(re.findall(r'[a-zA-Z]', text))
    cjk_count = len(re.findall(r'[\u4e00-\u9fff]', text))
    return cjk_count > 0 and latin_count > 0


class KBCleaner:
    """
    TM知识库清洗器

    分多阶段对翻译记忆库进行清洗，包括：
    - 混合污染清洗
    - 纯中文条目处理（同行对照→KB提取→语言识别）
    - 全量KB验证
    - 最终审计

    全程由LLM辅助判断，不自动生成翻译。
    """

    def __init__(self, db_path):
        """初始化数据库连接，设置WAL模式以支持并发放"""
        self.db_path = db_path
        self.conn = sqlite3.connect(db_path)
        self.conn.row_factory = sqlite3.Row  # 按列名访问结果
        self.conn.execute("PRAGMA journal_mode=WAL")

    def close(self):
        """关闭数据库连接"""
        self.conn.close()

    def get_total(self):
        """获取tm_segments表的总条目数"""
        cur = self.conn.execute("SELECT COUNT(*) FROM tm_segments")
        return cur.fetchone()[0]

    def get_row(self, row_id):
        """按ID获取单行数据"""
        cur = self.conn.execute("SELECT * FROM tm_segments WHERE id=?", (row_id,))
        return cur.fetchone()

    def get_rows_by_zh(self, zh_text):
        """
        按zh精确匹配查找行

        用于KB提取：根据中文文本找到其他行中对应的翻译
        """
        cur = self.conn.execute("SELECT * FROM tm_segments WHERE zh=?", (zh_text,))
        return cur.fetchall()

    def update_cell(self, row_id, col, value):
        """更新指定行列的单元格值"""
        self.conn.execute(f"UPDATE tm_segments SET {col}=? WHERE id=?", (value, row_id))

    def clear_cell(self, row_id, col):
        """清空指定行列的单元格（设为NULL）"""
        self.conn.execute(f"UPDATE tm_segments SET {col}=NULL WHERE id=?", (row_id,))

    def commit(self):
        """提交事务"""
        self.conn.commit()

    # ═══════════════════════════════════════
    #  阶段1: 混合污染清洗
    # ═══════════════════════════════════════
    def phase1_clean_mixed_entries(self):
        """
        对每语言列中中外文夹杂的翻译，用LLM判断：
        - 哪些中文段是错误的插入 → 删除
        - 哪些是合理的专有名词/品牌名 → 保留

        分批处理，每批15条，逐批提交以降低LLM调用压力。
        """
        log("=" * 60)
        log("阶段1: 混合污染清洗")
        total_cleaned = 0
        batch = []

        # 遍历所有活跃列，找出中英文混合的条目
        for col in ACTIVE_COLS:
            cur = self.conn.execute(
                f"SELECT id, zh, {col} FROM tm_segments WHERE {col} IS NOT NULL AND {col} != ''"
            )
            rows = cur.fetchall()
            for r in rows:
                val = r[col]
                if has_mixed_chinese(val):
                    batch.append({
                        'id': r['id'],
                        'col': col,
                        'zh': r['zh'],
                        'current': val,
                    })

        log(f"发现 {len(batch)} 条混合污染条目")

        # 分批处理，每批15条
        BATCH_SIZE = 15
        for i in range(0, len(batch), BATCH_SIZE):
            sub = batch[i:i + BATCH_SIZE]
            log(f"  处理批次 {i // BATCH_SIZE + 1}/{(len(batch) - 1) // BATCH_SIZE + 1}")
            try:
                result = self._llm_batch_judge_mixed(sub)
                if result:
                    for item in result:
                        try:
                            self._apply_mixed_judgment(item)
                            total_cleaned += 1
                        except Exception as e:
                            log(f"    [error] 处理条目失败: {e}, item={item}")
            except Exception as e:
                log(f"    [error] 批次处理失败: {e}")
            self.commit()

        log(f"阶段1完成，处理 {total_cleaned} 条")
        return total_cleaned

    def _normalize_col(self, col_input):
        """
        将列名标准化为列代码

        接受格式：列代码（en/ru等）、中文名称（英语/俄语等）、模糊名称
        """
        col_input = str(col_input).strip()
        # 直接匹配
        if col_input in LANG_COLS:
            return col_input
        if col_input in NAME_TO_COL:
            return NAME_TO_COL[col_input]
        # 模糊匹配：找包含关系
        for code, name in LANG_NAMES.items():
            if col_input in name or name in col_input:
                return code
        log(f"    [warn] 无法识别列名: {col_input}，使用原始值")
        return col_input

    def _llm_batch_judge_mixed(self, batch):
        """
        LLM批量判断混合条目

        构造详细提示词，让LLM对每条混合条目判断：
        - 混入的中文是否可以删除
        - 哪些是专有名词/品牌名应该保留

        Returns:
            LLM返回的JSON列表（包含每条的处理意见），或None
        """
        items_text = []
        for item in batch:
            items_text.append(
                f'  id={item["id"]} col={item["col"]} ({LANG_NAMES.get(item["col"], item["col"])}): "{item["current"]}"'
            )

        prompt = (
            "你是一个翻译质量监察员。以下是翻译记忆库中的条目，某些语言列的翻译中混入了中文。\n"
            "请判断每条翻译中混入的中文部分是否可以删除，还是应该作为专有名词/品牌名保留。\n"
            "注意：英文中的专有名词不应被视为中文，如'Face ID'、'Wi-Fi'等本身是英文，不要删除。\n\n"
            "只输出JSON数组，每项格式严格如下（不要输出任何其他内容）：\n"
            "[\n"
            "  {\n"
            '    "id": 行号（数字）,\n'
            '    "col": "列代码（en/ru/ar/es/pt/fr/kk/de/zh_hant/id_lang）",\n'
            '    "should_clean": true,\n'
            '    "cleaned": "清洗后的文本",\n'
            '    "reason": "原因"\n'
            "  }\n"
            "]\n\n"
            "条目列表：\n" + "\n".join(items_text)
        )

        log(f"    LLM请求({len(batch)}条)...")
        messages = [
            {"role": "system", "content": "你是一个严谨的翻译质量检查员。必须严格按指定JSON格式输出。"},
            {"role": "user", "content": prompt},
        ]
        try:
            result = call_llm_json(messages, max_tokens=8192, retries=2)
        except Exception as e:
            log(f"    [error] LLM调用失败: {e}")
            return None
        if not result:
            log(f"    [error] LLM未返回有效JSON")
            return None
        # 统一结果格式：确保是列表
        if isinstance(result, dict):
            # 可能返回了单个对象而不是数组
            if 'id' in result:
                result = [result]
            else:
                # 可能包在某个key里
                for v in result.values():
                    if isinstance(v, list):
                        result = v
                        break
        if not isinstance(result, list):
            log(f"    [error] 返回值不是数组: {str(result)[:200]}")
            return None
        # 标准化列名
        for item in result:
            if 'col' in item:
                item['col'] = self._normalize_col(item['col'])
        return result

    def _apply_mixed_judgment(self, item):
        """
        执行混合清洗判决

        根据LLM判断结果执行实际操作：
        - should_clean=True → 用清洗后文本替换（或清空）
        - 如果清洗后仍是纯中文 → 清空（说明没有有效翻译）
        """
        row_id = item['id']
        col = item.get('col', '')
        # 标准化列名
        col = self._normalize_col(col)
        if col not in LANG_COLS:
            log(f"    [跳过] id={row_id} 未知列: {col}")
            return
        should_clean = item.get('should_clean', False)
        if should_clean:
            cleaned = (item.get('cleaned', '') or '').strip()
            if cleaned:
                # 检查清洗后是否仍是纯中文
                if is_pure_chinese(cleaned):
                    self.clear_cell(row_id, col)
                    log(f"      清洗后仍是中文，清空 id={row_id} ({col})")
                else:
                    self.update_cell(row_id, col, cleaned)
                    log(f"      清洗 id={row_id} ({col}): {cleaned[:60]}")
            else:
                self.clear_cell(row_id, col)
                log(f"      清空 id={row_id} ({col})")

    # ═══════════════════════════════════════
    #  阶段2: 纯中文条目处理
    # ═══════════════════════════════════════
    def phase2_pure_chinese_entries(self):
        """
        对非zh列中的纯中文条目，三层对照：
        2a. 同行对照：与同一行的zh比较
        2b. KB跨行提取：在zh列精确搜索该文本，提取对应翻译
        2c. LLM语言识别

        返回值包含各步骤的处理统计信息。
        """
        log("=" * 60)
        log("阶段2: 纯中文条目处理")
        results_2a = []
        results_2b = []
        results_2c = []

        # 扫描所有活跃列，找出其中的纯中文条目
        for col in ACTIVE_COLS:
            cur = self.conn.execute(
                f"SELECT id, zh, {col} FROM tm_segments WHERE {col} IS NOT NULL AND {col} != ''"
            )
            rows = cur.fetchall()
            for r in rows:
                val = r[col]
                if is_pure_chinese(val):
                    results_2a.append({
                        'id': r['id'],
                        'col': col,
                        'zh': r['zh'],
                        'val': val.strip(),
                    })

        log(f"发现 {len(results_2a)} 条纯中文条目")

        # 2a: 同行对照 — 与同一行的zh原文比较
        log("--- 2a: 同行对照 ---")
        same_as_zh = [r for r in results_2a if r['val'] == r['zh']]
        diff_from_zh = [r for r in results_2a if r['val'] != r['zh']]
        log(f"  同行一致: {len(same_as_zh)} 条（清空）")
        log(f"  同行不一致: {len(diff_from_zh)} 条（进入KB提取）")

        # 同行一致的说明是zh的复制，直接清空
        for r in same_as_zh:
            log(f"    清空 id={r['id']} ({r['col']}) ← 复制了zh")
            self.clear_cell(r['id'], r['col'])
        self.commit()

        # 2b: KB提取 — 在知识库中搜索该中文文本在其他行对应的翻译
        log("--- 2b: KB跨行提取 ---")
        kb_found = []
        kb_not_found = []
        for r in diff_from_zh:
            matches = self.get_rows_by_zh(r['val'])
            matches = [m for m in matches if m['id'] != r['id']]  # 排除自身
            if matches:
                # 收集匹配行中该列的翻译（排除纯中文的）
                candidates = []
                for m in matches:
                    if m[r['col']] and m[r['col']].strip() and not is_pure_chinese(m[r['col']]):
                        candidates.append({
                            'source_id': m['id'],
                            'translation': m[r['col']],
                        })
                if candidates:
                    kb_found.append({
                        'id': r['id'],
                        'col': r['col'],
                        'zh_text': r['val'],
                        'candidates': candidates,
                    })
                else:
                    kb_not_found.append(r)
            else:
                kb_not_found.append(r)

        log(f"  KB找到候选: {len(kb_found)} 条（进入LLM校对）")
        log(f"  KB未找到: {len(kb_not_found)} 条（进入语言识别）")

        # LLM校对KB提取结果
        for i in range(0, len(kb_found), 15):
            sub = kb_found[i:i + 15]
            log(f"  校对批次 {i // 15 + 1}/{(len(kb_found) - 1) // 15 + 1}")
            verdicts = self._llm_verify_kb_extract(sub)
            if verdicts:
                for v in verdicts:
                    self._apply_kb_verdict(v)
            self.commit()

        # 2c: 语言识别 — 对于KB中找不到的行，用LLM识别文本实际语言
        log("--- 2c: LLM语言识别 ---")
        for i in range(0, len(kb_not_found), 20):
            sub = kb_not_found[i:i + 20]
            log(f"  语言识别批次 {i // 20 + 1}/{(len(kb_not_found) - 1) // 20 + 1}")
            results = self._llm_identify_language(sub)
            if results:
                # 将LLM结果与原始子条目配对
                id_to_item = {r['id']: r for r in sub}
                for r in results:
                    rid = r['id']
                    if rid in id_to_item:
                        self._apply_language_id(r, id_to_item[rid])
            self.commit()

        log(f"阶段2完成")
        return {
            'same_as_zh': len(same_as_zh),
            'kb_found': len(kb_found),
            'kb_not_found': len(kb_not_found),
        }

    def _llm_verify_kb_extract(self, batch):
        """
        LLM校对KB提取结果

        将KB中找到的候选翻译提交给LLM审核，确认其是否是该中文文本的正确翻译。
        """
        items = []
        for r in batch:
            cands = "; ".join([f"行{c['source_id']}: {c['translation'][:50]}" for c in r['candidates'][:3]])
            items.append(
                f'  id={r["id"]} ({r["col"]}) zh="{r["zh_text"][:50]}" 候选翻译: [{cands}]'
            )

        prompt = (
            "你是一个翻译质量监察员。以下条目中，某语言列的当前值是纯中文文本。\n"
            "我们已在知识库中按该中文文本精确匹配到其他条目，并提取了候选翻译。\n"
            "请判断：候选翻译是否是该中文文本的最正确翻译？\n\n"
            "输出JSON数组，每项：\n"
            "{\n"
            '  "id": 行号,\n'
            '  "col": "语言列",\n'
            '  "approved": true/false,  // 候选翻译是否正确\n'
            '  "best_translation": "认可的最佳翻译",  // 如果是，填写它\n'
            '  "reason": "说明"\n'
            "}\n\n"
            "条目：\n" + "\n".join(items)
        )

        messages = [
            {"role": "system", "content": "你是一个严谨的翻译质量检查员。"},
            {"role": "user", "content": prompt},
        ]
        result = call_llm_json(messages, max_tokens=4096)
        if not result:
            return None
        if isinstance(result, dict) and 'id' in result:
            result = [result]
        if not isinstance(result, list):
            return None
        return result

    def _apply_kb_verdict(self, verdict):
        """
        执行KB校对的判决结果

        - 如果LLM认可候选翻译 → 写入该翻译
        - 如果候选翻译仍是纯中文 → 清空
        - 如果不认可 → 清空
        """
        row_id = verdict['id']
        col = verdict['col']
        if verdict.get('approved') and verdict.get('best_translation'):
            t = verdict['best_translation'].strip()
            if t and not is_pure_chinese(t):
                self.update_cell(row_id, col, t)
                log(f"    KB写入 id={row_id} ({col}): {t[:60]}")
            else:
                self.clear_cell(row_id, col)
                log(f"    KB候选仍含中文，清空 id={row_id} ({col})")
        else:
            self.clear_cell(row_id, col)
            log(f"    KB未通过，清空 id={row_id} ({col})")

    def _llm_identify_language(self, batch):
        """
        LLM识别文本的实际语言

        对无法通过KB提取解决的纯中文条目，让LLM判断：
        - 该文本是否确实是中文（可能是错误复制）→ 清除
        - 是否其他语言的中文字（繁体中文/日文/韩文等）→ 指明目标列
        """
        items = []
        for r in batch:
            items.append(
                f'  id={r["id"]} col={r["col"]} zh原文="{str(r["zh"])[:40]}" 当前值="{str(r["val"])[:60]}"'
            )

        prompt = (
            "你是一个语言识别专家。以下是翻译记忆库中的条目，某些语言列（col）的值是纯中文文本，\n"
            "但该中文文本实际上可能是另一种语言（如繁体中文、日语、韩语等）。\n\n"
            "请判断每个条目的\"当前值\"实际属于以下哪种情况：\n"
            "1. 它就是中文，但与zh原文不同（可能是错误复制）→ 标记为应该清除\n"
            "2. 它是其他语言的中文字（如繁体中文、日文=含汉字）→ 指明目标语言列\n\n"
            "注意：col列名不需要你识别，只需要关注当前值文本本身。\n\n"
            "输出JSON数组，每项：\n"
            "{\n"
            '  "id": 行号（数字），\n'
            '  "identified_lang": null 或 "目标语言代码（如 zh_hant ja ko）",\n'
            '  "should_clear": true/false,\n'
            '  "reason": "说明"\n'
            "}\n\n"
            "条目：\n" + "\n".join(items)
        )

        messages = [
            {"role": "system", "content": "你是一个语言识别专家。"},
            {"role": "user", "content": prompt},
        ]
        result = call_llm_json(messages, max_tokens=4096)
        if not result:
            return None
        if isinstance(result, dict) and 'id' in result:
            result = [result]
        if not isinstance(result, list):
            return None
        return result

    def _apply_language_id(self, result, batch_item):
        """
        执行语言识别结果

        - should_clear=True → 清空（该文本是错误的中文复制）
        - identified_lang 有效 → 将当前值移到目标语言列
        - 无法识别 → 清空
        """
        row_id = result['id']
        col = batch_item['col']  # 使用原始条目的列名，而非LLM返回的
        if result.get('should_clear'):
            self.clear_cell(row_id, col)
            log(f"    清空（错误中文） id={row_id} ({col})")
        elif result.get('identified_lang') and result['identified_lang'] in LANG_COLS:
            target = result['identified_lang']
            # 先读当前值
            r = self.get_row(row_id)
            val = r[col]
            # 移到目标列
            self.update_cell(row_id, target, val)
            self.clear_cell(row_id, col)
            log(f"    移动 id={row_id} ({col}→{target}): {str(val)[:40]}")
        else:
            self.clear_cell(row_id, col)
            log(f"    无法识别，清空 id={row_id} ({col})")

    # ═══════════════════════════════════════
    #  阶段4: 全量KB验证
    # ═══════════════════════════════════════
    def phase4_full_verification(self):
        """
        对每条的每个语言列做LLM语言+翻译核对。
        按行批量（15条/批 × 9列 = 135对），LLM一次判断。

        只修复语言不匹配的问题（可以自动移动列），
        翻译不对应的问题仅标记不做修改。
        """
        log("=" * 60)
        log("阶段4: 全量KB验证")

        cols_str = ", ".join(ACTIVE_COLS)
        cur = self.conn.execute(f"SELECT id, zh, {cols_str} FROM tm_segments")
        rows = cur.fetchall()
        total = len(rows)
        log(f"共 {total} 条，准备全量核对")

        all_issues = []

        # 每15条一批进行验证
        for i in range(0, total, 15):
            sub = rows[i:i + 15]
            log(f"  核对批次 {i // 15 + 1}/{(total - 1) // 15 + 1} ({i}-{i+len(sub)-1})")
            issues = self._llm_verify_batch(sub)
            if issues:
                all_issues.extend(issues)
                # 立即修复已确认的问题
                for issue in issues:
                    self._apply_verification_fix(issue)
                self.commit()

        log(f"阶段4完成，共发现 {len(all_issues)} 个问题")
        return all_issues

    def _llm_verify_batch(self, batch):
        """
        LLM批量验证翻译条目

        对每条记录的每列翻译，检查：
        1. 语言是否正确（如en列是否真的是英语）
        2. 翻译是否与中文原文基本对应

        Returns:
            问题列表，每项包含行号、列名、问题类型和说明
        """
        items = []
        for r in batch:
            row_parts = [f'id={r["id"]} zh="{str(r["zh"])[:60]}"']
            for col in ACTIVE_COLS:
                val = r[col]
                if val and str(val).strip():
                    row_parts.append(f'{col}="{str(val)[:60]}"')
                else:
                    row_parts.append(f'{col}=""')
            items.append("  " + " | ".join(row_parts))

        prompt = (
            "你是一个翻译质量监察员。以下是翻译记忆库中的条目，每条包含中文原文和各语言的翻译。\n"
            "请检查每条中每个非空翻译是否满足：\n"
            "1. 语言正确（en列是英语，ru列是俄语，ar列是阿拉伯语，es列是西班牙语，pt列是葡萄牙语，\n"
            "   fr列是法语，kk列是哈萨克语，de列是德语，zh_hant列是繁体中文，id_lang列是印尼语）\n"
            "2. 翻译内容与中文原文基本对应（可以接受意译，不要求完全逐字）\n\n"
            "只标记有问题的情况。不要生成新翻译。\n"
            "输出JSON数组，每项格式严格如下：\n"
            "[\n"
            "  {\n"
            '    "id": 行号（数字）,\n'
            '    "col": "问题列的代码（en/ru/ar/es/pt/fr/kk/de/zh_hant/id_lang）",\n'
            '    "problem": "language_mismatch" | "translation_mismatch",\n'
            '    "comment": "说明问题所在（如：该列文本实际是俄语；或翻译与中文原文不对应）"\n'
            "  }\n"
            "]\n"
            "没有问题的条目不要输出。\n\n"
            "条目：\n" + "\n".join(items)
        )

        messages = [
            {"role": "system", "content": "你是一个严谨的翻译质量检查员。只检查不修改，严格按JSON格式输出。"},
            {"role": "user", "content": prompt},
        ]
        result = call_llm_json(messages, max_tokens=16384, temperature=0.0, retries=3)
        if not result:
            log("    [warn] LLM未返回有效结果，跳过批次")
            return []
        if isinstance(result, dict) and 'id' in result:
            result = [result]
        if not isinstance(result, list):
            log(f"    [warn] 结果格式异常: {str(result)[:200]}")
            return []
        return result

    def _apply_verification_fix(self, issue):
        """
        根据验证结果执行修复（只处理语言不匹配，可自动移动列）

        - language_mismatch：尝试识别实际语言，将值移动到正确列
        - translation_mismatch：仅标记，不做修改（无法自动修复）
        """
        row_id = issue['id']
        col = self._normalize_col(issue.get('col', ''))
        if col not in LANG_COLS:
            log(f"    [跳过] id={row_id} 未知列: {col}")
            return
        problem = issue.get('problem', '')

        if problem == 'language_mismatch':
            # 尝试识别实际语言，移动到正确列
            actual_lang = self._normalize_col(issue.get('actual_lang', ''))
            if actual_lang and actual_lang in LANG_COLS and actual_lang != col:
                r = self.get_row(row_id)
                val = r[col]
                if val and str(val).strip():
                    # 检查目标列是否已有值，有则跳过避免覆盖
                    existing = r[actual_lang]
                    if existing and str(existing).strip():
                        log(f"    [跳过-目标非空] id={row_id} ({col}→{actual_lang}): 目标列已有值")
                    else:
                        self.update_cell(row_id, actual_lang, val)
                        self.clear_cell(row_id, col)
                        log(f"    [移动] id={row_id} ({col}→{actual_lang}): {str(val)[:40]}")
                else:
                    log(f"    [跳过-空值] id={row_id} ({col})")
            else:
                log(f"    [标记-语言不匹配] id={row_id} ({col}): {issue.get('comment','')[:80]}")

        elif problem == 'translation_mismatch':
            # 仅标记，不修改
            log(f"    [标记-翻译不对应] id={row_id} ({col}): {issue.get('comment','')[:80]}")

    # ═══════════════════════════════════════
    #  阶段3: 翻译归位（由阶段2驱动）
    # ═══════════════════════════════════════
    # 阶段3的逻辑已在 phase2 中通过 _apply_language_id 和 _apply_kb_verdict 实现
    # 即：将放错语言列的值移动到正确的列

    # ═══════════════════════════════════════
    #  阶段5: 最终LLM对照
    # ═══════════════════════════════════════
    def phase5_final_audit(self):
        """
        清洗完成后，再次用LLM全量对照，验证修复效果。
        只出报告，不做修改。
        """
        log("=" * 60)
        log("阶段5: 最终LLM对照审计")

        cols_str = ", ".join(ACTIVE_COLS)
        cur = self.conn.execute(f"SELECT id, zh, {cols_str} FROM tm_segments")
        rows = cur.fetchall()
        total = len(rows)

        total_issues = 0
        for i in range(0, total, 15):
            sub = rows[i:i + 15]
            log(f"  审计批次 {i // 15 + 1}/{(total - 1) // 15 + 1}")
            issues = self._llm_verify_batch(sub)
            if issues:
                total_issues += len(issues)
                for iss in issues:
                    log(f"    [残留] id={iss['id']} ({iss['col']}): {iss.get('comment','')[:80]}")
            # 注意：审计不做修改

        log(f"最终审计完成，残留问题数: {total_issues}")
        return total_issues


def main():
    """
    主入口：按顺序执行所有清洗阶段
    1. 混合污染清洗
    2. 纯中文条目处理（含归位）
    4. 全量KB验证
    5. 最终审计
    """
    log("=" * 60)
    log("TM知识库清洗开始")
    log(f"数据库: {DB_PATH}")
    log(f"API: {API_BASE} / 清洗模型={CLEAN_MODEL} / 翻译模型={TRANSLATE_MODEL}")
    log(f"API_KEY: {'已设置' if API_KEY else '未设置！'}")

    if not API_KEY:
        log("[fatal] 未设置 SILICONFLOW_API_KEY 环境变量")
        sys.exit(1)

    cleaner = KBCleaner(DB_PATH)
    total_before = cleaner.get_total()
    log(f"清洗前条目数: {total_before}")

    # 阶段1: 混合清洗
    cleaner.phase1_clean_mixed_entries()

    # 阶段2: 纯中文条目处理（含阶段3归位）
    cleaner.phase2_pure_chinese_entries()

    # 阶段4: 全量KB验证
    cleaner.phase4_full_verification()

    # 提交所有更改
    cleaner.commit()

    # 阶段5: 最终审计
    remaining = cleaner.phase5_final_audit()

    cleaner.close()

    log("=" * 60)
    log(f"清洗完成！最终残留问题: {remaining}")
    log("备份文件: tm.sqlite3.bak / tm_embeddings.npz.bak")
    log("如需回滚: cp tm.sqlite3.bak tm.sqlite3 && cp tm_embeddings.npz.bak tm_embeddings.npz")


if __name__ == "__main__":
    main()
