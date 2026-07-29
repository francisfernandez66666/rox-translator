# ============================================================================
# base_skill.py — 所有技能的"父类模板"（含通用出图 + 文件保存能力）
# ============================================================================
# 【作用】定义一个技能必须有哪些能力（名字、描述、关键词、处理函数）
# 【通用能力】
#   self._generate_image(prompt, size) — 调用 CogView-3-Flash 生成图片
#   self._save_file(content, name, ext) — 保存文本到文件
#   self._save_docx(md_content, name) — Markdown 转 Word 文档
#   self._translate_prompt(chinese) — 中文提示词翻译成英文
# ============================================================================
# 【为什么】新增技能只需要"填表" + 调通用方法，不需要自己实现图片/文件逻辑
# ============================================================================

from abc import ABC, abstractmethod
from typing import Any


class BaseSkill(ABC):
    """
    技能基类 —— 所有可插拔技能的统一模板

    新增一个技能只需要3步：
    1. 在 backend/skills/ 下建一个文件夹（如 prd/）
    2. 在里面创建 skill.py，写一个类继承 BaseSkill
    3. 实现 name、description、keywords、handle 四个方法

    ★ 通用能力（所有子类自动拥有）：
    - _generate_image(prompt, size) → 生成图片，返回 {"success", "local_path", ...}
    - _save_file(content, name, ext) → 保存文本文件，返回文件路径
    - _save_docx(md_content, name) → Markdown 转 Word，返回文件路径
    - _translate_prompt(chinese) → 中文提示词翻译成英文
    """

    # ---- 以下4个方法是每个技能必须实现的（@abstractmethod 强制） ----

    @property
    @abstractmethod
    def name(self) -> str:
        """
        技能的唯一标识名（英文，小写，用短横线连接）
        例：translation / prd / image
        """
        ...

    @property
    @abstractmethod
    def description(self) -> str:
        """
        技能的一句话中文描述
        """
        ...

    @property
    @abstractmethod
    def keywords(self) -> list[str]:
        """
        触发这个技能的关键词列表（小写）

        用于路由匹配：用户输入中包含任一关键词时，该技能被触发。
        """
        ...

    @abstractmethod
    def handle(self, params: dict) -> dict:
        """
        技能的核心处理函数

        Args:
            params: 统一参数字典
                - message (str): 用户输入
                - files (list): 上传文件路径
                - options (dict): 前端选项

        Returns:
            统一返回字典
                - skill (str): 技能名
                - reply (str): 回复文本
                - data (dict): 结构化数据（可选）
                - files (list): 生成的文件路径列表（可选）
                - error (str): 错误信息（可选）
        """
        ...

    # ---- 以下方法是基类提供的默认实现，子类可以覆盖 ----

    def can_handle(self, user_input: str) -> float:
        """
        判断这个技能能否处理用户输入，返回置信度 0.0~1.0

        默认实现：如果用户输入包含任一关键词，返回 0.8 高置信度；
        否则返回 0.0。子类可覆盖此方法实现更精细的匹配逻辑。

        Args:
            user_input: 用户的输入文本

        Returns:
            置信度分数（0.0 ~ 1.0）
        """
        text_lower = user_input.lower()
        for kw in self.keywords:
            if kw in text_lower:
                return 0.8
        return 0.0

    def info(self) -> dict:
        """
        返回技能的元信息

        Returns:
            包含 name、description、keywords 的字典
        """
        return {
            "name": self.name,
            "description": self.description,
            "keywords": self.keywords,
        }

    # ==================== ★ 通用能力：文件保存 ====================

    def _save_file(self, content: str, name: str, ext: str = ".md", subdir: str = "") -> str:
        """
        保存文本内容到文件

        封装了 services.file_service.save_text_file，子类无需关心文件保存细节。

        Args:
            content: 文件内容
            name: 文件名（不含扩展名）
            ext: 扩展名（.md / .txt / .json 等）
            subdir: 子目录名（可选，如 "prd"）

        Returns:
            保存的文件绝对路径
        """
        from services.file_service import save_text_file
        return save_text_file(content, name, ext, subdir)

    def _save_docx(self, md_content: str, name: str, subdir: str = "") -> str:
        """
        Markdown 内容转 Word 文档并保存（三层保障：专业→简单→纯文本兜底）

        封装了 services.file_service.save_docx，子类无需关心 Word 转换细节。

        Args:
            md_content: Markdown 格式内容
            name: 文件名（不含扩展名）
            subdir: 子目录名（可选）

        Returns:
            保存的 .docx 文件绝对路径（兜底时返回 .md 路径）
        """
        from services.file_service import save_docx
        return save_docx(md_content, name, subdir)
