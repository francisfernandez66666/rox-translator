# langcross-translator（Python SDK）

翻译助手（能言 LangCross）开放 API 的零依赖 Python 客户端（单模块 `translator_sdk.py`）。

```bash
pip install langcross-translator
```

```python
from translator_sdk import TranslatorClient, __version__

cli = TranslatorClient(base_url="https://your-langcross-host", api_key="rk_xxx")
print(cli.translate_text("今天天气不错", ["en", "ja"]))   # {'en': ..., 'ja': ...}
print(cli.balance())                                       # 积分余额与 ≈句数
```

- 认证：管理后台「API Key」面板签发的 Bearer Key
- 任务模型：提交 → 自动轮询 → 返回译文；文件任务产物 `download_file(task_id, path)`（多语言缺省打 zip）
- 版本记录见 `sdk/CHANGELOG.md`；npm/PyPI/Maven 三端版本由 `scripts/release-sdk.sh` 统一同步
