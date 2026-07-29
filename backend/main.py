# ============================================================================
# main.py — ROX Agent 后端主入口（FastAPI + 技能路由 + SSE 流式进度）
# ============================================================================
# 【API一览】
#   POST /api/chat           — 普通聊天接口（PRD/出图等无进度技能）
#   POST /api/chat/stream    — SSE 流式聊天接口（翻译等有进度的技能）
#   POST /api/translate/stream — SSE 流式文件翻译
#   POST /api/translate      — 普通文件翻译（兼容）
#   GET  /api/skills         — 技能列表
#   GET  /api/health         — 健康检查
#   GET  /api/download/{f}   — 文件下载
# ============================================================================

import os
import sys
import json
import uuid
import asyncio
import traceback
from pathlib import Path
from typing import AsyncGenerator

from dotenv import load_dotenv
from fastapi import FastAPI, File, UploadFile, Form, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse, JSONResponse, StreamingResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel

# ---- 1. 加载环境变量 ----
# 从 .env 文件中读取环境变量（本地开发用）
# 在打包为二进制后，以下硬编码的 Key 会直接编译进程序
load_dotenv()

# ★ API Key 硬编码（编译进二进制，不暴露给用户）
# 硅基流动 SiliconFlow（默认翻译模型）
# 若环境变量未设置，使用内置默认值（编译打包后供用户直接使用）
if not os.getenv("SILICONFLOW_API_KEY"):
    os.environ["SILICONFLOW_API_KEY"] = "sk-ugkqobwooolhdmykmycxuxyizcsnysudgmvuhkpinetuuxvq"
if not os.getenv("SILICONFLOW_API_BASE"):
    os.environ["SILICONFLOW_API_BASE"] = "https://api.siliconflow.cn/v1"
# 智谱（embedding + 备用翻译模型）
if not os.getenv("ONLINE_API_KEY"):
    os.environ["ONLINE_API_KEY"] = "***REMOVED***"
if not os.getenv("ONLINE_API_BASE"):
    os.environ["ONLINE_API_BASE"] = "https://open.bigmodel.cn/api/paas/v4"
if not os.getenv("ONLINE_MODEL"):
    os.environ["ONLINE_MODEL"] = "tencent/Hunyuan-MT-7B"

# ★ BUNDLE 模式下 Resources/ 不在 sys.path，补上（否则 import_module 找不到 skills/）
# macOS .app 打包后，资源文件位于 Resources/ 目录，需手动加入模块搜索路径
_MAIN_DIR = os.path.dirname(os.path.abspath(__file__))
_RESOURCES_DIR = os.path.join(os.path.dirname(_MAIN_DIR), 'Resources')
if os.path.isdir(_RESOURCES_DIR) and _RESOURCES_DIR not in sys.path:
    sys.path.insert(0, _RESOURCES_DIR)

# ---- 2. 导入技能注册器 ----
# SkillRegistry 负责动态发现、加载和路由所有技能模块
from skill_registry import SkillRegistry

# ---- 3. 创建 FastAPI 应用 ----
# 初始化 FastAPI 应用实例，配置标题、版本和描述信息
app = FastAPI(
    title="翻译助手",
    version="2.0.0",
    description="极石汽车多语言翻译助手",
)

# ---- 4. 跨域配置 ----
# 添加 CORS 中间件，允许前端跨域请求
# ★ 开发/演示阶段允许所有来源，ngrok等外网隧道可正常访问
# 生产环境建议改为具体域名列表
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# ---- 5. 初始化技能注册器（异步加载，不阻塞 web 服务启动） ----
print("=" * 50)
print("🚀 翻译助手启动中...")

# 用户数据目录（避开 App Translocation 只读问题）
# macOS 会对下载的 .app 进行 translocation 保护，导致程序所在目录只读
# 因此将用户数据（知识库、上传文件等）统一存放在 ~/Library/Application Support/ 下
_USER_HOME = Path.home()
USER_DATA_DIR = Path(os.getenv("USER_DATA_DIR", str(_USER_HOME / "Library" / "Application Support" / "翻译助手")))
USER_DATA_DIR.mkdir(parents=True, exist_ok=True)
os.environ["USER_DATA_DIR"] = str(USER_DATA_DIR)

import shutil

# 首次启动：从 .app 内部复制知识库到用户目录
# 打包时内置的知识库文件（.sqlite3, .npz 等）需要复制到用户可写目录
_BUNDLE_DATA = Path(__file__).parent / "data"
if _BUNDLE_DATA.exists():
    for f in _BUNDLE_DATA.iterdir():
        if f.suffix in (".sqlite3", ".npz", ".sqlite3-shm", ".sqlite3-wal"):
            dst = USER_DATA_DIR / f.name
            if not dst.exists():
                shutil.copy2(f, dst)
                print(f"  📦 初始化知识库: {f.name}")

# 运行时目录（uploads / output）
# 每次启动时清空旧的运行时目录，确保临时文件不会累积
DATA_ROOT = USER_DATA_DIR
for _d in ["_uploads", "_output"]:
    p = DATA_ROOT / _d
    if p.exists():
        shutil.rmtree(p)
    p.mkdir(parents=True, exist_ok=True)

print("=" * 50)

# 创建技能注册器实例，用于管理所有已注册的技能模块
registry = SkillRegistry()
# 标记技能注册器是否已就绪（后台线程异步加载，启动时可能尚未完成）
_registry_ready = False


def _init_registry_bg():
    """
    后台线程初始化函数：异步发现并加载所有技能模块
    不阻塞 FastAPI 启动，技能加载完成后设置 _registry_ready 为 True
    """
    global _registry_ready
    try:
        # 自动发现 skills/ 目录下的所有技能模块并注册
        registry.auto_discover()
    except Exception as e:
        print(f"  ⚠️ 技能加载出错: {e}（部分功能可能不可用）")
    _registry_ready = True
    print("=" * 50)
    print(f"✅ 启动完成！已加载 {len(registry.skill_names)} 个技能")
    print("=" * 50)


import threading
# 在后台线程中启动技能加载，不阻塞主线程的 web 服务
threading.Thread(target=_init_registry_bg, daemon=True).start()


# ==================== 数据模型 ====================


class ChatRequest(BaseModel):
    """
    聊天请求数据模型

    属性:
        message (str): 用户输入的消息内容
        skill (str): 指定使用的技能名称，为空时自动路由
        options (dict): 可选参数，传递给技能模块的额外配置
    """
    message: str
    skill: str = ""
    options: dict = {}


class ChatResponse(BaseModel):
    """
    聊天响应数据模型

    属性:
        skill (str): 处理该请求的技能名称
        reply (str): 文本回复内容
        data (dict): 附加数据（如翻译的 JSON 结果等）
        files (list[str]): 生成的文件路径列表
        error (str): 错误信息（处理失败时填充）
    """
    skill: str = ""
    reply: str = ""
    data: dict = {}
    files: list[str] = []
    error: str = ""


# ==================== 文件存储配置 ====================

# 上传文件存储目录
UPLOAD_DIR = USER_DATA_DIR / "_uploads"
# 输出文件存储目录（翻译结果、生成的文件等）
OUTPUT_DIR = USER_DATA_DIR / "_output"




# ==================== SSE 工具函数 ====================


def sse_event(event_type: str, data: dict) -> str:
    """
    构造一条 SSE（Server-Sent Events）事件字符串

    SSE 协议格式：
        data: {"type":"<event_type>", "<key>": "<value>", ...}\n\n

    参数:
        event_type (str): 事件类型，如 "progress"、"done"、"error"
        data (dict): 事件数据，会自动合并 type 字段

    返回:
        str: 符合 SSE 协议的格式化事件字符串
    """
    payload = {"type": event_type, **data}
    return f"data: {json.dumps(payload, ensure_ascii=False)}\n\n"


# ==================== API 接口 ====================


@app.get("/api/health")
async def health_check():
    """
    健康检查接口（GET /api/health）

    用于监控和负载均衡检查，返回当前服务状态和技能加载情况。
    当技能尚未加载完成时，返回 "loading" 状态。

    返回:
        dict: {
            "status": "ok" | "loading",
            "version": "2.0.0",
            "skills": list[str],       -- 已加载的技能名称列表
            "loading": bool,           -- 是否仍在加载中
            "load_errors": list[str]   -- 加载过程中的错误信息（可选）
        }
    """
    result = {"status": "ok" if _registry_ready else "loading", "version": "2.0.0", "skills": registry.skill_names}
    if not _registry_ready:
        result["loading"] = True
    if hasattr(registry, '_load_errors') and registry._load_errors:
        result["load_errors"] = registry._load_errors
    return result


@app.get("/api/skills")
async def list_skills():
    """
    获取技能列表接口（GET /api/skills）

    返回所有已注册技能的详细信息，包括名称、描述、参数等。

    返回:
        dict: {"skills": list[dict]} -- 技能信息列表
    """
    skills_info = registry.get_all_info()
    return {"skills": skills_info}


# ==================== SSE 流式聊天接口 ====================


@app.post("/api/chat/stream")
async def chat_stream(request: ChatRequest):
    """
    SSE 流式聊天接口（POST /api/chat/stream）

    翻译技能使用该接口，通过 SSE 实时推送翻译进度：
    包括知识库匹配进度、模型翻译进度、复查进度等三个阶段。
    其他无进度技能则直接执行后返回 done 事件。

    参数:
        request (ChatRequest): {
            "message": str,   -- 用户输入
            "skill": str,     -- 指定技能（可选）
            "options": dict   -- 附加参数（可选）
        }

    返回:
        StreamingResponse: SSE 流式响应，事件类型包括:
            - progress: 进度更新事件
            - done: 处理完成事件
            - error: 处理出错事件
    """
    message = request.message.strip()
    requested_skill = request.skill.strip()
    options = request.options or {}

    # 空消息时返回欢迎提示
    if not message:
        result = ChatResponse(
            skill="system",
            reply="你好！我是ROX智能营销工厂，可以帮你：翻译、写PRD、出图。",
        )
        return StreamingResponse(
            _sse_done(result),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )

    # 技能路由：根据用户指定或自动匹配选择技能
    skill = None
    if requested_skill:
        # 用户明确指定了技能
        skill = registry.get(requested_skill)
    else:
        # 自动根据消息内容路由到合适的技能
        skill = registry.route(message)

    # 未匹配到技能时，使用兜底闲聊回复
    if not skill:
        result = ChatResponse(skill="chat", reply=_fallback_chat(message))
        return StreamingResponse(
            _sse_done(result),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )

    # ★ 翻译技能 → 流式进度推送
    # 翻译技能支持分阶段进度回调，需要 SSE 实时推送
    if skill.name == "translation":
        return StreamingResponse(
            _stream_translation(skill, message, options),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )

    # 其他技能 → 直接执行，只返回 done 事件
    # 如 PRD 写作、出图等技能，没有分阶段进度概念
    try:
        result = skill.handle({"message": message, "options": options})
        return StreamingResponse(
            _sse_done(ChatResponse(**result)),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )
    except Exception as e:
        traceback.print_exc()
        return StreamingResponse(
            _sse_error(str(e)),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )


async def _stream_translation(skill, message: str, options: dict) -> AsyncGenerator[str, None]:
    """
    翻译技能的 SSE 流式生成器（内部函数）

    通过回调函数接收翻译引擎的阶段性进度，实时转成 SSE 事件推送。
    翻译是同步阻塞操作，在后台线程池中执行以避免阻塞事件循环。

    参数:
        skill: 翻译技能实例
        message (str): 用户输入的待翻译文本
        options (dict): 翻译选项（目标语言等）

    返回:
        AsyncGenerator[str, None]: SSE 事件流，依次推送:
            - progress 事件（含 step/done/total/percent 字段）
            - 最终的 done 事件（含翻译结果）
            - 或 error 事件（处理异常时）
    """
    # 异步队列：翻译引擎的进度回调将进度数据放入队列
    progress_queue: asyncio.Queue = asyncio.Queue()

    def on_progress(step: str, done: int, total: int):
        """
        翻译引擎的进度回调函数

        由翻译引擎在每个阶段（知识库匹配/模型翻译/复查）调用。
        将进度数据（步骤名、已完成数、总数、百分比）放入异步队列。

        参数:
            step (str): 当前步骤名称
            done (int): 已完成的任务数
            total (int): 总任务数
        """
        percent = int(done / total * 100) if total > 0 else 0
        progress_queue.put_nowait({"step": step, "done": done, "total": total, "percent": min(percent, 99)})

    # 在线程池中执行翻译（翻译是同步阻塞的，需要放到线程里）
    # 避免长时间阻塞 FastAPI 的异步事件循环
    loop = asyncio.get_event_loop()
    result_future = loop.run_in_executor(
        None,
        lambda: skill.handle({
            "message": message,
            "options": options,
            "on_progress": on_progress,
        })
    )

    # 实时推送进度：轮询检查翻译线程是否完成，同时从队列中取进度
    while not result_future.done():
        try:
            # 非阻塞取进度（100ms超时）
            # 若队列为空则等待最多 100ms 后超时
            prog = await asyncio.wait_for(progress_queue.get(), timeout=0.1)
            yield sse_event("progress", prog)
        except asyncio.TimeoutError:
            # 没有新进度，继续等（短暂 sleep 避免 CPU 空转）
            await asyncio.sleep(0.05)

    # 排空剩余进度：翻译线程完成后队列中可能还有未取出的进度
    while not progress_queue.empty():
        prog = progress_queue.get_nowait()
        yield sse_event("progress", prog)

    # 获取最终结果并推送
    try:
        result = result_future.result()
        yield sse_event("progress", {"step": "完成", "done": 1, "total": 1, "percent": 100})
        yield sse_event("done", {"result": result})
    except Exception as e:
        yield sse_event("error", {"error": str(e)})


# ==================== SSE 流式文件翻译 ====================


@app.post("/api/translate/stream")
async def translate_file_stream(
    file: UploadFile = File(...),
    target_langs: str = Form("en"),
    use_online: str = Form("true"),
    message: str = Form(""),    # ★ 用户输入的提示语（选"其他语言"时用于解析目标语言）
):
    """
    SSE 流式文件翻译接口（POST /api/translate/stream）

    上传文件并通过 SSE 实时推送翻译进度。支持多目标语言翻译。
    翻译完成后清理临时上传文件。

    参数（表单）:
        file (UploadFile): 待翻译的文件（支持 CSV、Excel、Word 等格式）
        target_langs (str): 目标语言列表，逗号分隔，如 "en,ru,ar"
        use_online (str): 是否使用在线模型，"true" 或 "false"
        message (str): 用户提示语，选"其他语言"时用于解析自定义目标语言

    返回:
        StreamingResponse: SSE 流式响应，持续推送翻译进度和最终结果
    """
    # 保存上传文件到临时目录，用 UUID 前缀防重名
    file_id = uuid.uuid4().hex[:8]
    file_ext = Path(file.filename).suffix
    save_path = UPLOAD_DIR / f"{file_id}{file_ext}"

    with open(save_path, "wb") as f:
        content = await file.read()
        f.write(content)

    # 解析目标语言列表：逗号分隔、去除空白、过滤空串
    langs = [l.strip() for l in target_langs.split(",") if l.strip()]
    # 解析布尔值：支持 "true"/"1"/"yes" 等多种表示
    online = use_online.lower() in ("true", "1", "yes")

    # 获取翻译技能实例
    translation_skill = registry.get("translation")
    if not translation_skill:
        os.remove(save_path)
        return StreamingResponse(
            _sse_error("翻译技能未加载"),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )

    return StreamingResponse(
        _stream_file_translation(translation_skill, str(save_path), langs, online, message),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


async def _stream_file_translation(skill, filepath: str, langs: list, online: bool, user_message: str = "") -> AsyncGenerator[str, None]:
    """
    文件翻译的 SSE 流式生成器（内部函数）

    在后台线程池中执行文件翻译，通过回调实时推送进度。
    翻译完成后自动清理临时上传文件。

    参数:
        skill: 翻译技能实例
        filepath (str): 上传文件的完整路径
        langs (list): 目标语言代码列表，如 ["en", "ru"]
        online (bool): 是否使用在线模型
        user_message (str): 用户输入的提示语，选"其他语言"时用于解析目标语言

    返回:
        AsyncGenerator[str, None]: SSE 事件流，包含进度和最终结果
    """
    # 异步队列：用于接收翻译引擎的进度回调
    progress_queue: asyncio.Queue = asyncio.Queue()

    def on_progress(step: str, done: int, total: int):
        """
        文件翻译的进度回调函数

        将翻译引擎的进度信息放入异步队列供 SSE 推送。

        参数:
            step (str): 当前步骤名称
            done (int): 已完成的任务数
            total (int): 总任务数
        """
        percent = int(done / total * 100) if total > 0 else 0
        progress_queue.put_nowait({"step": step, "done": done, "total": total, "percent": min(percent, 99)})

    # 在线程池中执行同步的文件翻译任务
    loop = asyncio.get_event_loop()
    result_future = loop.run_in_executor(
        None,
        lambda: skill.handle({
            "message": user_message,     # ★ 传用户提示语，用于解析"其他语言"
            "files": [filepath],
            "options": {
                "target_langs": langs,
                "use_online": online,
                "_prompt": user_message,  # ★ 兼容：skill.py 通过 options._prompt 也能拿到
            },
            "on_progress": on_progress,
        })
    )

    # 轮询等待翻译完成，同时推送进度
    while not result_future.done():
        try:
            prog = await asyncio.wait_for(progress_queue.get(), timeout=0.1)
            yield sse_event("progress", prog)
        except asyncio.TimeoutError:
            await asyncio.sleep(0.05)

    # 排空剩余的进度事件
    while not progress_queue.empty():
        prog = progress_queue.get_nowait()
        yield sse_event("progress", prog)

    # 清理上传文件
    try:
        os.remove(filepath)
    except:
        pass

    # 获取翻译结果并推送
    try:
        result = result_future.result()
        yield sse_event("progress", {"step": "完成", "done": 1, "total": 1, "percent": 100})
        yield sse_event("done", {"result": result})
    except Exception as e:
        yield sse_event("error", {"error": str(e)})


# ==================== 辅助 SSE 生成器 ====================


async def _sse_done(result: ChatResponse) -> AsyncGenerator[str, None]:
    """
    SSE done 事件生成器（内部函数）

    直接返回一个带有完成结果的事件。
    用于无进度需求的技能，快速返回处理结果。

    参数:
        result (ChatResponse): 处理结果数据

    返回:
        AsyncGenerator[str, None]: 仅包含一个 done 事件的 SSE 流
    """
    yield sse_event("done", {"result": result.model_dump()})


async def _sse_error(error: str) -> AsyncGenerator[str, None]:
    """
    SSE error 事件生成器（内部函数）

    直接返回一个包含错误信息的事件。
    用于快速向客户端反馈异常情况。

    参数:
        error (str): 错误描述字符串

    返回:
        AsyncGenerator[str, None]: 仅包含一个 error 事件的 SSE 流
    """
    yield sse_event("error", {"error": error})


# ==================== 普通聊天接口（兼容） ====================


@app.post("/api/chat", response_model=ChatResponse)
async def chat(request: ChatRequest):
    """
    普通聊天接口（POST /api/chat，非流式）

    非流式版本，适用于 PRD 写作、出图等不需要实时进度的技能。
    前端无需 SSE 解析，直接获得 JSON 响应。

    参数:
        request (ChatRequest): 聊天请求，包含消息内容、技能选择和选项

    返回:
        ChatResponse: 处理结果，包含回复内容、数据、文件列表等
    """
    message = request.message.strip()
    requested_skill = request.skill.strip()
    options = request.options or {}

    # 空消息返回欢迎提示
    if not message:
        return ChatResponse(
            skill="system",
            reply="你好！我是ROX智能营销工厂，可以帮你：翻译、写PRD、出图。",
        )

    # 技能路由
    skill = None
    if requested_skill:
        skill = registry.get(requested_skill)
    else:
        skill = registry.route(message)

    if skill:
        try:
            result = skill.handle({"message": message, "options": options})
            return ChatResponse(**result)
        except Exception as e:
            traceback.print_exc()
            return ChatResponse(skill=skill.name, reply=f"❌ 处理出错: {str(e)}", error=str(e))
    else:
        # 无匹配技能时使用兜底闲聊回复
        return ChatResponse(skill="chat", reply=_fallback_chat(message))


# ==================== 普通文件翻译接口（兼容） ====================


@app.post("/api/translate")
async def translate_file(
    file: UploadFile = File(...),
    target_langs: str = Form("en"),
    use_online: str = Form("true"),
    message: str = Form(""),    # ★ 用户提示语
):
    """
    普通文件翻译接口（POST /api/translate，非流式，兼容旧前端）

    非流式版本的文件翻译接口，保持与旧前端兼容。
    上传文件后同步等待翻译完成，直接返回 JSON 结果。
    翻译完成后自动清理临时文件。

    参数（表单）:
        file (UploadFile): 待翻译的源文件
        target_langs (str): 目标语言列表，逗号分隔
        use_online (str): 是否使用在线模型
        message (str): 用户提示语

    返回:
        dict: 翻译结果，包含翻译后的文本、文件链接等
    """
    # 保存上传文件
    file_id = uuid.uuid4().hex[:8]
    file_ext = Path(file.filename).suffix
    save_path = UPLOAD_DIR / f"{file_id}{file_ext}"

    with open(save_path, "wb") as f:
        content = await file.read()
        f.write(content)

    # 解析参数
    langs = [l.strip() for l in target_langs.split(",") if l.strip()]
    online = use_online.lower() in ("true", "1", "yes")

    # 执行翻译（同步阻塞，但文件翻译通常在短时间内完成）
    translation_skill = registry.get("translation")
    if not translation_skill:
        return {"error": "翻译技能未加载"}

    result = translation_skill.handle({
        "message": message,     # ★ 传用户提示语，用于解析"其他语言"
        "files": [str(save_path)],
        "options": {"target_langs": langs, "use_online": online, "_prompt": message},
    })

    # 清理临时文件
    try:
        os.remove(save_path)
    except:
        pass

    return result


# ==================== 文件下载 ====================


@app.get("/api/download/{file_path:path}")
async def download_file(file_path: str):
    """
    文件下载接口（GET /api/download/{file_path}）

    根据文件路径提供下载服务。自动根据扩展名设置正确的 Content-Type，
    使图片等文件可直接在浏览器中展示。

    参数:
        file_path (str): 文件的完整路径（URL 编码）

    返回:
        FileResponse: 文件响应，包含正确的媒体类型和文件名

    错误:
        404: 文件不存在时返回
    """
    if not os.path.isfile(file_path):
        return JSONResponse(status_code=404, content={"error": "文件不存在"})
    filename = os.path.basename(file_path)
    # ★ 根据扩展名设置 Content-Type（图片需要正确类型才能在浏览器展示）
    ext = os.path.splitext(filename)[1].lower()
    content_types = {
        ".png": "image/png",
        ".jpg": "image/jpeg",
        ".jpeg": "image/jpeg",
        ".gif": "image/gif",
        ".webp": "image/webp",
        ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        ".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
        ".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        ".pdf": "application/pdf",
    }
    media_type = content_types.get(ext, "application/octet-stream")
    return FileResponse(path=file_path, filename=filename, media_type=media_type)


# ==================== 翻译 KB 语言查询 + 上传 API ====================


@app.get("/api/translation/langs")
async def translation_langs():
    """
    查询翻译技能支持的语言列表（GET /api/translation/langs）

    返回知识库中支持的目标语言列表，附带语言名称和国旗图标。
    前端在此基础上再加一个统一的"其他语言"选项。

    返回:
        dict: {
            "kb_langs": [
                {"code": "en", "name": "英语", "flag": "🇬🇧"},
                ...
            ]
        }
    """
    skill = registry.get("translation")
    if not skill:
        return {"kb_langs": []}

    # 国旗映射表：语言代码 → 对应国家的国旗 Emoji
    FLAGS = {
        "en": "🇬🇧", "ru": "🇷🇺", "ar": "🇸🇦", "es": "🇪🇸", "pt": "🇵🇹",
        "fr": "🇫🇷", "kk": "🇰🇿", "de": "🇩🇪", "zh_hant": "🇹🇼",
    }

    def _lang_info(code: str) -> dict:
        """
        构造单个语言的描述信息

        参数:
            code (str): 语言代码，如 "en"、"ru"

        返回:
            dict: {"code": str, "name": str, "flag": str}
        """
        return {
            "code": code,
            "name": skill.lang_names.get(code, code),
            "flag": FLAGS.get(code, "🌐"),
        }

    kb = [_lang_info(c) for c in skill.kb_langs]
    return {"kb_langs": kb}


@app.post("/api/translation/upload-kb")
async def upload_translation_kb(file: UploadFile = File(...)):
    """
    上传翻译知识库文件接口（POST /api/translation/upload-kb）

    上传 CSV 或 Excel 格式的翻译记忆（TM）文件，解析后写入 SQLite 数据库，
    并自动重建向量索引。处理完成后立即删除临时上传文件。

    ★ 保留旧接口兼容，一步到位：上传→解析→写入→建索引。

    参数（表单）:
        file (UploadFile): 知识库文件（CSV 或 Excel）

    返回:
        dict: {"success": bool, "message": str, ...} — 导入结果

    错误:
        400: 翻译技能未加载时返回
    """
    # 保存上传文件到临时目录
    file_id = uuid.uuid4().hex[:8]
    file_ext = Path(file.filename).suffix
    save_path = UPLOAD_DIR / f"kb_upload_{file_id}{file_ext}"

    with open(save_path, "wb") as f:
        content = await file.read()
        f.write(content)

    try:
        skill = registry.get("translation")
        if not skill:
            return JSONResponse(status_code=400, content={"success": False, "message": "❌ 翻译技能未加载"})
        result = skill.upload_knowledge_base(str(save_path))
        return result
    finally:
        # 无论成功失败，都清理临时文件
        if save_path.exists():
            os.remove(save_path)


@app.post("/api/translation/recognize-kb")
async def recognize_translation_kb(file: UploadFile = File(...)):
    """
    识别翻译知识库文件接口（POST /api/translation/recognize-kb，第一步）

    ★ 第一步：上传并识别翻译知识库文件，返回预览数据（不写入数据库）。
    用户可在前端预览识别的结果后，再决定是否导入。
    识别成功的临时文件保留以供后续 import-kb 接口读取。

    参数（表单）:
        file (UploadFile): 知识库文件（CSV 或 Excel）

    返回:
        dict: {
            "success": bool,
            "temp_id": str,      -- 临时文件标识，供 import-kb 使用
            "preview": list,     -- 预览数据
            "columns": list,     -- 列名
            "total_rows": int    -- 总行数
        }

    错误:
        500: 识别失败时返回错误信息
    """
    file_id = uuid.uuid4().hex[:8]
    file_ext = Path(file.filename).suffix
    save_path = UPLOAD_DIR / f"kb_recognize_{file_id}{file_ext}"

    with open(save_path, "wb") as f:
        content = await file.read()
        f.write(content)

    try:
        skill = registry.get("translation")
        if not skill:
            return JSONResponse(status_code=400, content={"success": False, "message": "❌ 翻译技能未加载"})
        result = skill.recognize_kb(str(save_path))
        return result
    except Exception as e:
        return JSONResponse(status_code=500, content={"success": False, "message": f"❌ 识别失败: {str(e)}"})
    # ★ 注意：识别成功时不删除临时文件，import_kb 需要读取


@app.post("/api/translation/import-kb")
async def import_translation_kb(body: dict):
    """
    导入翻译知识库接口（POST /api/translation/import-kb，第二步）

    ★ 第二步：将 recognize-kb 识别后的数据导入知识库。
    执行完整流程：写入 SQLite → 向量化 → 建立索引。
    使用 recognize-kb 返回的 temp_id 定位临时文件。

    参数（JSON Body）:
        body (dict): {
            "temp_id": str   -- recognize-kb 返回的临时文件标识
        }

    返回:
        dict: {"success": bool, "message": str, ...}

    错误:
        400: 缺少 temp_id 或翻译技能未加载时返回
    """
    temp_id = body.get("temp_id", "")
    if not temp_id:
        return JSONResponse(status_code=400, content={"success": False, "message": "❌ 缺少 temp_id"})

    skill = registry.get("translation")
    if not skill:
        return JSONResponse(status_code=400, content={"success": False, "message": "❌ 翻译技能未加载"})
    return skill.import_kb(temp_id)


@app.post("/api/translation/build-segments")
async def build_translation_segments():
    """
    构建结构化知识库接口（POST /api/translation/build-segments）

    遍历翻译记忆（TM）条目，使用 LLM 提取可复用的翻译片段。
    构建 segment_base，用于提高翻译匹配和上下文利用效率。

    返回:
        dict: {
            "success": bool,
            "total_segments": int,  -- 提取的片段总数
            "message": str          -- 处理结果描述
        }
    """
    skill = registry.get("translation")
    if not skill:
        return JSONResponse(status_code=400, content={"success": False, "message": "❌ 翻译技能未加载"})
    return skill.build_segments()


@app.get("/api/translation/kb-stats")
async def translation_kb_stats():
    """
    获取知识库统计信息接口（GET /api/translation/kb-stats）

    返回知识库的详细统计数据，包括：
        - TM（翻译记忆）条目总数
        - 各源语言的条目分布
        - segment_base 片段数

    返回:
        dict: {
            "success": bool,
            "total_tm_entries": int,
            "lang_stats": dict,         -- 各语言条目数
            "total_segments": int,      -- segment_base 片段数
            ...
        }
    """
    skill = registry.get("translation")
    if not skill:
        return {"success": False, "message": "❌ 翻译技能未加载"}
    return skill.kb_stats()


# ==================== 兜底闲聊 ====================


def _fallback_chat(user_input: str) -> str:
    """
    兜底闲聊回复函数（内部函数）

    当用户消息无法匹配任何技能时使用。
    返回友好的提示信息，引导用户使用翻译功能。

    参数:
        user_input (str): 用户输入的原始消息

    返回:
        str: 友好的提示回复文本
    """
    return (
        f"我暂时不太理解「{user_input}」的意思 😅\n\n"
        "我是翻译助手，支持多语言文本翻译和文件翻译。\n"
        "试试直接输入中文，或上传文档来翻译吧！"
    )


# ==================== 前端静态文件服务 ====================


# 判断是否处于 PyInstaller 打包状态
if getattr(sys, 'frozen', False):
    # 打包后，资源文件在 sys._MEIPASS 指向的临时目录中
    _BASE_DIR = Path(sys._MEIPASS)
else:
    # 开发模式下，项目根目录在 main.py 的上层目录
    _BASE_DIR = Path(__file__).resolve().parent.parent
DIST = _BASE_DIR / "frontend" / "dist"

# 如果前端构建产物存在，则挂载静态文件服务
if DIST.is_dir():
    # 挂载 assets 目录下的静态资源（JS/CSS/图片等）
    _assets_dir = DIST / "assets"
    if _assets_dir.is_dir():
        app.mount("/assets", StaticFiles(directory=str(_assets_dir)), name="assets")

    @app.get("/{full_path:path}")
    async def serve_spa(full_path: str):
        """
        SPA 静态文件兜底路由（内部路由）

        将所有非 API 路径的请求映射到前端 dist 目录。
        如果路径对应实际文件则直接返回，否则返回 index.html
        （实现 Vue/React SPA 的前端路由支持）。

        参数:
            full_path (str): URL 路径

        返回:
            FileResponse: 对应文件或 index.html

        错误:
            404: 路径以 api/ 开头时抛出 404（避免 API 路由被兜底）
        """
        # 防止 API 路由被静态文件兜底拦截
        if full_path.startswith("api/"):
            raise HTTPException(status_code=404)
        fp = DIST / full_path
        if fp.is_file():
            return FileResponse(str(fp))
        # SPA 约定：所有非文件路径返回 index.html 由前端路由处理
        return FileResponse(str(DIST / "index.html"))


# ==================== 启动入口 ====================


if __name__ == "__main__":
    """
    程序启动入口

    使用 uvicorn 运行 FastAPI 应用。
    打包模式下自动打开浏览器并等待端口就绪。
    开发模式下使用热重载（reload=False 已关闭，可按需开启）。
    """
    import uvicorn, webbrowser, threading, socket
    PORT = 8000

    if getattr(sys, 'frozen', False):
        # 打包模式：后台线程等待 uvicorn 端口就绪后自动打开浏览器
        def _wait_and_open():
            """
            等待 uvicorn 端口就绪后打开浏览器（后台线程）

            循环检测 127.0.0.1:8000 是否可连接，最多等待 7.5 秒。
            避免在 uvicorn 完全启动前打开浏览器导致白屏。
            """
            import time
            # 等 uvicorn 端口就绪，再开浏览器（避免看到连接失败白屏）
            for _ in range(15):
                time.sleep(0.5)
                try:
                    s = socket.create_connection(("127.0.0.1", PORT), timeout=0.5)
                    s.close()
                    break
                except:
                    pass
            webbrowser.open(f"http://localhost:{PORT}/")
        threading.Thread(target=_wait_and_open, daemon=True).start()
        uvicorn.run(app, host="127.0.0.1", port=PORT)
    else:
        # 开发模式：直接启动，不自动打开浏览器
        uvicorn.run("main:app", host="127.0.0.1", port=PORT, reload=False)
