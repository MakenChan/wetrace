"""
本地 SenseVoiceSmall 语音识别侧车服务
协议：OpenAI 兼容的 POST /v1/audio/transcriptions
供 wetrace 内置 TTS client 直接调用。

首次启动会从 ModelScope 下载 SenseVoiceSmall (~900 MB)
模型缓存在: ~/.cache/modelscope/hub/models/iic/SenseVoiceSmall

实现说明：
  直接用 funasr.AutoModel 跑 PyTorch 推理（非自回归，CPU RTF < 0.1），
  而不是 funasr-onnx 的运行时 ONNX 导出路径（那条路径依赖 onnxscript，坑多且启动慢）。
"""

from __future__ import annotations

import argparse
import io
import logging
import os
import re
import shutil
import sys
import tempfile
import time
from pathlib import Path
from typing import Optional

from fastapi import FastAPI, File, Form, HTTPException, UploadFile

HERE = Path(__file__).resolve().parent

# ---------- 准备 ffmpeg（funasr 内部用系统 ffmpeg 解码音频）----------
def _ensure_ffmpeg_on_path() -> None:
    """
    funasr.utils.load_utils._load_audio_ffmpeg 会 shell 出 `ffmpeg` 命令。
    Windows 下系统通常没装 ffmpeg，这里用 imageio-ffmpeg 携带的二进制兜底。
    """
    try:
        import imageio_ffmpeg  # type: ignore
    except ImportError:
        return  # 用户自己装了 ffmpeg 最好
    exe = imageio_ffmpeg.get_ffmpeg_exe()
    ffdir = os.path.dirname(exe)
    # 保证有一个名为 `ffmpeg.exe` 的文件（funasr 不带 .exe 后缀）
    target = os.path.join(ffdir, "ffmpeg.exe")
    if not os.path.exists(target):
        try:
            shutil.copy2(exe, target)
        except OSError:
            pass
    if ffdir not in os.environ.get("PATH", ""):
        os.environ["PATH"] = ffdir + os.pathsep + os.environ.get("PATH", "")


_ensure_ffmpeg_on_path()

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s  %(levelname)s  %(message)s",
)
log = logging.getLogger("sensevoiced")

# ---------- 延迟加载模型 ----------
_model = None


def get_model():
    global _model
    if _model is not None:
        return _model

    log.info("加载 SenseVoiceSmall（首次会下载约 900MB，请耐心）...")
    t0 = time.time()
    from funasr import AutoModel  # type: ignore

    _model = AutoModel(
        model="iic/SenseVoiceSmall",
        disable_update=True,   # 关闭启动检测更新
        device="cpu",
        disable_log=True,
    )
    log.info("模型加载完成，用时 %.2fs", time.time() - t0)
    return _model


# ---------- SenseVoice 输出 tag 清理 ----------
_TAG_RE = re.compile(r"<\|[^|]*\|>")


def strip_sensevoice_tags(s: str) -> str:
    """SenseVoice 输出会带 <|zh|><|HAPPY|><|Speech|><|withitn|> 之类的 tag。"""
    return _TAG_RE.sub("", s).strip()


# ---------- FastAPI ----------
app = FastAPI(title="wetrace-sensevoiced", version="0.1.0")


@app.get("/health")
def health():
    return {"status": "ok", "model_loaded": _model is not None}


@app.post("/v1/audio/transcriptions")
async def transcribe(
    file: UploadFile = File(...),
    model: Optional[str] = Form(None),        # 兼容 OpenAI 协议，忽略
    language: Optional[str] = Form("auto"),   # auto / zh / en / yue / ja / ko
    response_format: Optional[str] = Form("json"),  # 兼容
    prompt: Optional[str] = Form(None),       # 兼容
    temperature: Optional[float] = Form(None),  # 兼容
):
    raw = await file.read()
    if not raw:
        raise HTTPException(status_code=400, detail="empty audio")

    # 把上传内容落到临时文件，让 funasr 自己用 ffmpeg 解码
    suffix = Path(file.filename or "").suffix.lower() or ".bin"
    if suffix not in (".mp3", ".wav", ".m4a", ".ogg", ".flac", ".aac", ".opus"):
        suffix = ".bin"
    with tempfile.NamedTemporaryFile(suffix=suffix, delete=False) as f:
        f.write(raw)
        tmp_path = f.name

    m = get_model()
    lang = language or "auto"

    t0 = time.time()
    try:
        result = m.generate(
            input=tmp_path,
            language=lang,
            use_itn=True,
            batch_size_s=60,  # 60s 一个 batch（funasr 会自动切片）
        )
    except Exception as e:
        log.exception("推理失败")
        raise HTTPException(status_code=500, detail=f"inference failed: {e}")
    finally:
        try:
            os.unlink(tmp_path)
        except OSError:
            pass

    dur = time.time() - t0

    raw_text = result[0].get("text", "") if result else ""
    clean_text = strip_sensevoice_tags(raw_text)

    log.info(
        "ASR done  cost=%.3fs  text=%s",
        dur,
        clean_text[:40] + ("..." if len(clean_text) > 40 else ""),
    )

    return {"text": clean_text, "raw": raw_text}


# ---------- main ----------
def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=5210)
    ap.add_argument("--preload", action="store_true",
                    help="启动前加载模型（否则在第一次请求时加载）")
    args = ap.parse_args()

    if args.preload:
        get_model()

    import uvicorn
    uvicorn.run(app, host=args.host, port=args.port, log_level="info")


if __name__ == "__main__":
    main()
