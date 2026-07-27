# ============================================================================
# skill_registry.py — 技能注册器（自动扫描 + 智能路由）
# ============================================================================
# 【作用】启动时自动扫描 skills/ 目录，发现并注册所有技能；
#         收到用户输入时，自动匹配最合适的技能来处理
# 【★ 升级】扫描时自动为有 SKILL.md 的目录生成/升级 skill.py
# ============================================================================

import importlib
import os
import sys
from typing import Optional

from base_skill import BaseSkill


class SkillRegistry:
    """
    技能注册器 —— 管理所有可插拔技能的"注册表"
    """

    def __init__(self, skills_dir: str = None):
        self._skills: dict[str, BaseSkill] = {}
        self._skills_dir = skills_dir or os.path.join(
            os.path.dirname(os.path.abspath(__file__)), "skills"
        )

    def register(self, skill: BaseSkill):
        self._skills[skill.name] = skill
        print(f"  ✅ 技能已注册: {skill.name} — {skill.description}")

    def auto_discover(self):
        """
        自动扫描 skills/ 目录，发现并注册所有技能

        ★ 新增：扫描时自动为有 SKILL.md 但没有 skill.py 的目录生成包装器
        ★ 新增：自动升级旧版 SKILL.md 包装器（使用新的 _skill_md_runner）
        """
        print(f"🔍 扫描技能目录: {self._skills_dir}")

        if not os.path.isdir(self._skills_dir):
            print(f"  ⚠️ 技能目录不存在: {self._skills_dir}")
            return

        if self._skills_dir not in sys.path:
            sys.path.insert(0, self._skills_dir)

        # ★ 先扫描一遍，为没有 skill.py 的目录自动生成
        self._ensure_skill_py_files()

        # ★ 非技能目录黑名单（data/缓存/配置等，不要当技能扫描）
        _SKIP_DIRS = {"data", "__pycache__", "references", ".git", "node_modules"}

        # 遍历 skills/ 下的每个子目录，导入并注册
        for item in sorted(os.listdir(self._skills_dir)):
            skill_path = os.path.join(self._skills_dir, item)
            if not os.path.isdir(skill_path) or item.startswith("_") or item in _SKIP_DIRS:
                continue

            skill_file = os.path.join(skill_path, "skill.py")
            if not os.path.isfile(skill_file):
                print(f"  ⏭️ 跳过 {item}/（没有 skill.py）")
                continue

            try:
                # ★ 清除旧的模块缓存（确保重载时拿到最新代码）
                module_name = f"skills.{item}.skill"
                if module_name in sys.modules:
                    del sys.modules[module_name]

                module = importlib.import_module(module_name)

                found = False
                for attr_name in dir(module):
                    attr = getattr(module, attr_name)
                    if (
                        isinstance(attr, type)
                        and issubclass(attr, BaseSkill)
                        and attr is not BaseSkill
                    ):
                        skill_instance = attr()
                        self.register(skill_instance)
                        found = True

                if not found:
                    print(f"  ⚠️ {item}/skill.py 中没有找到 BaseSkill 子类")

            except Exception as e:
                import traceback
                detail = traceback.format_exc()
                print(f"  ❌ 加载技能 {item} 失败: {e}")
                print(f"     详情:\n{detail}")
                # ★ 记录失败原因，供前端查询
                if not hasattr(self, '_load_errors'):
                    self._load_errors = {}
                self._load_errors[item] = str(e)

        print(f"🎯 技能加载完成，已加载翻译引擎: {list(self._skills.keys())}")

    def _ensure_skill_py_files(self):
        """
        ★ 扫描 skills/ 目录，为有 SKILL.md 但没有 skill.py 的目录自动生成包装器
        ★ 检测旧版 SKILL.md 包装器（不继承 SkillMDRunner 的），自动升级
        """
        # 动态导入 _skill_md_wrapper
        wrapper_path = os.path.join(self._skills_dir, "_skill_md_wrapper.py")
        if not os.path.isfile(wrapper_path):
            wrapper_path = os.path.join(self._skills_dir, "skill_md_wrapper.py")
        if not os.path.isfile(wrapper_path):
            print("  ⚠️ _skill_md_wrapper.py 不存在，跳过自动生成")
            return

        try:
            import importlib.util as _ilu
            _spec = _ilu.spec_from_file_location("_skill_md_wrapper", wrapper_path)
            _mod = _ilu.module_from_spec(_spec)
            _spec.loader.exec_module(_mod)
            generate_skill_py = _mod.generate_skill_py
        except Exception as e:
            print(f"  ⚠️ 导入 _skill_md_wrapper 失败: {e}")
            return

        for item in sorted(os.listdir(self._skills_dir)):
            skill_path = os.path.join(self._skills_dir, item)
            if not os.path.isdir(skill_path) or item.startswith("_") or item in {"data", "__pycache__", "references", ".git", "node_modules"}:
                continue

            skill_md = os.path.join(skill_path, "SKILL.md")
            skill_py = os.path.join(skill_path, "skill.py")

            if not os.path.isfile(skill_md):
                continue  # 没有 SKILL.md，跳过

            need_generate = False

            if not os.path.isfile(skill_py):
                # 没有 skill.py，需要生成
                need_generate = True
                print(f"  📝 {item}/ 有 SKILL.md 但无 skill.py，自动生成包装器")
            else:
                # 有 skill.py，检查是否是旧版 SKILL.md 包装器
                try:
                    with open(skill_py, "r", encoding="utf-8") as f:
                        content = f.read()
                    # 旧版标志：有 SkillMDWrapper 类但没有 SkillMDRunner
                    if "SkillMDWrapper" in content and "SkillMDRunner" not in content:
                        need_generate = True
                        print(f"  🔄 {item}/ 旧版包装器，自动升级为新版 _skill_md_runner")
                except:
                    pass

            if need_generate:
                try:
                    generated = generate_skill_py(skill_path, item)
                    if generated:
                        print(f"  ✅ 已生成/升级 {item}/skill.py")
                except Exception as e:
                    print(f"  ⚠️ 生成 {item}/skill.py 失败: {e}")

    def route(self, user_input: str) -> Optional[BaseSkill]:
        if not user_input or not user_input.strip():
            return None

        scores = {}
        for name, skill in self._skills.items():
            score = skill.can_handle(user_input)
            if score > 0:
                scores[name] = score

        if not scores:
            return None

        best_name = max(scores, key=scores.get)
        best_score = scores[best_name]
        print(f"  🔀 路由结果: {best_name} (置信度={best_score:.2f})")
        return self._skills[best_name]

    def get(self, name: str) -> Optional[BaseSkill]:
        return self._skills.get(name)

    def get_all_info(self) -> list[dict]:
        return [skill.info() for skill in self._skills.values()]

    @property
    def skill_names(self) -> list[str]:
        return list(self._skills.keys())
