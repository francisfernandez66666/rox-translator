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
load_dotenv()

# ★ API Key 硬编码（编译进二进制，不暴露给用户）
# 硅基流动 SiliconFlow（默认翻译模型）
if not os.getenv("SILICONFLOW_API_KEY"):
    os.environ["SILICONFLOW_API_KEY"] = "sk-ugkqobwooolhdmykmycxuxyizcsnysudgmvuhkpinetuuxvq"
if not os.getenv("SILICONFLOW_API_BASE"):
    os.environ["SILICONFLOW_API_BASE"] = "https://api.siliconflow.cn/v1"
# 智谱（embedding + 备用翻译模型）
if not os.getenv("ONLINE_API_KEY"):
    os.environ["ONLINE_API_KEY"] = "223ea45fe00b4bdcb465efc2b1ddd3aa.SVrMeynGY1tw2juA"
if not os.getenv("ONLINE_API_BASE"):
    os.environ["ONLINE_API_BASE"] = "https://open.bigmodel.cn/api/paas/v4"
if not os.getenv("ONLINE_MODEL"):
    os.environ["ONLINE_MODEL"] = "THUDM/GLM-4-9B-0414"

# ★ BUNDLE 模式下 Resources/ 不在 sys.path，补上（否则 import_module 找不到 skills/）
_MAIN_DIR = os.path.dirname(os.path.abspath(__file__))
_RESOURCES_DIR = os.path.join(os.path.dirname(_MAIN_DIR), 'Resources')
if os.path.isdir(_RESOURCES_DIR) and _RESOURCES_DIR not in sys.path:
    sys.path.insert(0, _RESOURCES_DIR)

# ---- 2. 导入技能注册器 ----
from skill_registry import SkillRegistry

# ---- 3. 创建 FastAPI 应用 ----
app = FastAPI(
    title="翻译助手",
    version="2.0.0",
    description="极石汽车多语言翻译助手",
)

# ---- 4. 跨域配置 ----
app.add_middleware(
    CORSMiddleware,
    # ★ 开发/演示阶段允许所有来源，ngrok等外网隧道可正常访问
    # 生产环境建议改为具体域名列表
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# ---- 5. 初始化技能注册器（异步加载，不阻塞 web 服务启动） ----
print("=" * 50)
print("🚀 翻译助手启动中...")

DATA_ROOT = Path(os.getenv("DATA_DIR", os.path.join(os.path.dirname(os.path.abspath(__file__)), "data")))
DATA_ROOT.mkdir(parents=True, exist_ok=True)
(DATA_ROOT / "_uploads").mkdir(parents=True, exist_ok=True)
(DATA_ROOT / "_output").mkdir(parents=True, exist_ok=True)

print("=" * 50)

registry = SkillRegistry()
_registry_ready = False

def _init_registry_bg():
    global _registry_ready
    try:
        registry.auto_discover()
    except Exception as e:
        print(f"  ⚠️ 技能加载出错: {e}（部分功能可能不可用）")
    _registry_ready = True
    print("=" * 50)
    print(f"✅ 启动完成！已加载 {len(registry.skill_names)} 个技能")
    print("=" * 50)

import threading
threading.Thread(target=_init_registry_bg, daemon=True).start()


# ==================== 数据模型 ====================

class ChatRequest(BaseModel):
    message: str
    skill: str = ""
    options: dict = {}


class ChatResponse(BaseModel):
    skill: str = ""
    reply: str = ""
    data: dict = {}
    files: list[str] = []
    error: str = ""


# ==================== 文件存储配置 ====================

_BACKEND_DIR = os.path.dirname(os.path.abspath(__file__))
UPLOAD_DIR = Path(os.getenv("UPLOAD_DIR", os.path.join(_BACKEND_DIR, "data", "_uploads")))
OUTPUT_DIR = Path(os.getenv("OUTPUT_DIR", os.path.join(_BACKEND_DIR, "data", "_output")))

UPLOAD_DIR.mkdir(parents=True, exist_ok=True)
OUTPUT_DIR.mkdir(parents=True, exist_ok=True)




# ==================== SSE 工具函数 ====================

def sse_event(event_type: str, data: dict) -> str:
    """
    构造一条 SSE 事件
    格式：data: {"type":"progress","step":"...","percent":50}\n\n
    """
    payload = {"type": event_type, **data}
    return f"data: {json.dumps(payload, ensure_ascii=False)}\n\n"


# ==================== API 接口 ====================

@app.get("/api/health")
async def health_check():
    result = {"status": "ok" if _registry_ready else "loading", "version": "2.0.0", "skills": registry.skill_names}
    if not _registry_ready:
        result["loading"] = True
    if hasattr(registry, '_load_errors') and registry._load_errors:
        result["load_errors"] = registry._load_errors
    return result


@app.get("/api/skills")
async def list_skills():
    """返回技能列表"""
    skills_info = registry.get_all_info()
    return {"skills": skills_info}


# ==================== SSE 流式聊天接口 ====================

@app.post("/api/chat/stream")
async def chat_stream(request: ChatRequest):
    """
    SSE 流式聊天接口
    翻译技能使用，实时推送翻译进度（知识库匹配/模型翻译/复查）
    """
    message = request.message.strip()
    requested_skill = request.skill.strip()
    options = request.options or {}

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

    # 技能路由
    skill = None
    if requested_skill:
        skill = registry.get(requested_skill)
    else:
        skill = registry.route(message)

    if not skill:
        result = ChatResponse(skill="chat", reply=_fallback_chat(message))
        return StreamingResponse(
            _sse_done(result),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )

    # ★ 翻译技能 → 流式进度推送
    if skill.name == "translation":
        return StreamingResponse(
            _stream_translation(skill, message, options),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )

    # 其他技能 → 直接执行，只返回 done 事件
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
    翻译技能的 SSE 流式生成器
    通过回调函数接收进度，转成 SSE 事件推送
    """
    progress_queue: asyncio.Queue = asyncio.Queue()

    def on_progress(step: str, done: int, total: int):
        """翻译引擎的进度回调，把进度塞入队列"""
        percent = int(done / total * 100) if total > 0 else 0
        progress_queue.put_nowait({"step": step, "done": done, "total": total, "percent": min(percent, 99)})

    # 在线程池中执行翻译（翻译是同步阻塞的，需要放到线程里）
    loop = asyncio.get_event_loop()
    result_future = loop.run_in_executor(
        None,
        lambda: skill.handle({
            "message": message,
            "options": options,
            "on_progress": on_progress,
        })
    )

    # 实时推送进度
    while not result_future.done():
        try:
            # 非阻塞取进度（100ms超时）
            prog = await asyncio.wait_for(progress_queue.get(), timeout=0.1)
            yield sse_event("progress", prog)
        except asyncio.TimeoutError:
            # 没有新进度，继续等
            await asyncio.sleep(0.05)

    # 排空剩余进度
    while not progress_queue.empty():
        prog = progress_queue.get_nowait()
        yield sse_event("progress", prog)

    # 获取最终结果
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
    SSE 流式文件翻译接口
    """
    # 保存上传文件
    file_id = uuid.uuid4().hex[:8]
    file_ext = Path(file.filename).suffix
    save_path = UPLOAD_DIR / f"{file_id}{file_ext}"

    with open(save_path, "wb") as f:
        content = await file.read()
        f.write(content)

    langs = [l.strip() for l in target_langs.split(",") if l.strip()]
    online = use_online.lower() in ("true", "1", "yes")

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
    文件翻译的 SSE 流式生成器
    ★ user_message: 用户输入的提示语，选"其他语言"时用于解析目标语言
    """
    progress_queue: asyncio.Queue = asyncio.Queue()

    def on_progress(step: str, done: int, total: int):
        percent = int(done / total * 100) if total > 0 else 0
        progress_queue.put_nowait({"step": step, "done": done, "total": total, "percent": min(percent, 99)})

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

    while not result_future.done():
        try:
            prog = await asyncio.wait_for(progress_queue.get(), timeout=0.1)
            yield sse_event("progress", prog)
        except asyncio.TimeoutError:
            await asyncio.sleep(0.05)

    while not progress_queue.empty():
        prog = progress_queue.get_nowait()
        yield sse_event("progress", prog)

    # 清理上传文件
    try:
        os.remove(filepath)
    except:
        pass

    try:
        result = result_future.result()
        yield sse_event("progress", {"step": "完成", "done": 1, "total": 1, "percent": 100})
        yield sse_event("done", {"result": result})
    except Exception as e:
        yield sse_event("error", {"error": str(e)})


# ==================== 辅助 SSE 生成器 ====================

async def _sse_done(result: ChatResponse) -> AsyncGenerator[str, None]:
    """直接返回 done 事件（无进度的技能用）"""
    yield sse_event("done", {"result": result.model_dump()})


async def _sse_error(error: str) -> AsyncGenerator[str, None]:
    """直接返回 error 事件"""
    yield sse_event("error", {"error": error})


# ==================== 普通聊天接口（兼容） ====================

@app.post("/api/chat", response_model=ChatResponse)
async def chat(request: ChatRequest):
    """
    普通聊天接口（非流式，PRD/出图等用）
    """
    message = request.message.strip()
    requested_skill = request.skill.strip()
    options = request.options or {}

    if not message:
        return ChatResponse(
            skill="system",
            reply="你好！我是ROX智能营销工厂，可以帮你：翻译、写PRD、出图。",
        )

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
    普通文件翻译接口（非流式，兼容旧前端）
    """
    file_id = uuid.uuid4().hex[:8]
    file_ext = Path(file.filename).suffix
    save_path = UPLOAD_DIR / f"{file_id}{file_ext}"

    with open(save_path, "wb") as f:
        content = await file.read()
        f.write(content)

    langs = [l.strip() for l in target_langs.split(",") if l.strip()]
    online = use_online.lower() in ("true", "1", "yes")

    translation_skill = registry.get("translation")
    if not translation_skill:
        return {"error": "翻译技能未加载"}

    result = translation_skill.handle({
        "message": message,     # ★ 传用户提示语，用于解析"其他语言"
        "files": [str(save_path)],
        "options": {"target_langs": langs, "use_online": online, "_prompt": message},
    })

    try:
        os.remove(save_path)
    except:
        pass

    return result


# ==================== 文件下载 ====================

@app.get("/api/download/{file_path:path}")
async def download_file(file_path: str):
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
    查询翻译技能支持的语言列表
    ★ 返回 KB 语言列表，前端再加一个统一的"其他语言"选项
    返回: { kb_langs: [{code, name, flag}] }
    """
    skill = registry.get("translation")
    if not skill:
        return {"kb_langs": []}

    # 国旗映射
    FLAGS = {
        "en": "🇬🇧", "ru": "🇷🇺", "ar": "🇸🇦", "es": "🇪🇸", "pt": "🇵🇹",
        "fr": "🇫🇷", "kk": "🇰🇿", "de": "🇩🇪", "zh_hant": "🇹🇼",
    }

    def _lang_info(code: str) -> dict:
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
    上传翻译知识库文件（CSV/Excel），解析后写入 SQLite 并重建向量索引
    ★ 保留旧接口兼容，一步到位
    """
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
        if save_path.exists():
            os.remove(save_path)


@app.post("/api/translation/recognize-kb")
async def recognize_translation_kb(file: UploadFile = File(...)):
    """
    ★ 第一步：上传并识别翻译知识库文件，返回预览数据（不写入数据库）
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
    ★ 第二步：将已识别的翻译数据导入知识库（写入SQLite + 向量化 + 建索引）
    body: {"temp_id": "xxx"}
    """
    temp_id = body.get("temp_id", "")
    if not temp_id:
        return JSONResponse(status_code=400, content={"success": False, "message": "❌ 缺少 temp_id"})

    skill = registry.get("translation")
    if not skill:
        return JSONResponse(status_code=400, content={"success": False, "message": "❌ 翻译技能未加载"})
    return skill.import_kb(temp_id)


# ==================== 兜底闲聊 ====================

def _fallback_chat(user_input: str) -> str:
    return (
        f"我暂时不太理解「{user_input}」的意思 😅\n\n"
        "我是翻译助手，支持多语言文本翻译和文件翻译。\n"
        "试试直接输入中文，或上传文档来翻译吧！"
    )


# ==================== 前端静态文件服务 ====================

if getattr(sys, 'frozen', False):
    _BASE_DIR = Path(sys._MEIPASS)
else:
    _BASE_DIR = Path(__file__).resolve().parent.parent
DIST = _BASE_DIR / "frontend" / "dist"

if DIST.is_dir():
    _assets_dir = DIST / "assets"
    if _assets_dir.is_dir():
        app.mount("/assets", StaticFiles(directory=str(_assets_dir)), name="assets")

    @app.get("/{full_path:path}")
    async def serve_spa(full_path: str):
        if full_path.startswith("api/"):
            raise HTTPException(status_code=404)
        fp = DIST / full_path
        if fp.is_file():
            return FileResponse(str(fp))
        return FileResponse(str(DIST / "index.html"))


# ==================== 启动入口 ====================

if __name__ == "__main__":
    import uvicorn, webbrowser, threading, socket
    PORT = 8000

    if getattr(sys, 'frozen', False):
        def _wait_and_open():
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
        uvicorn.run("main:app", host="127.0.0.1", port=PORT, reload=False)
