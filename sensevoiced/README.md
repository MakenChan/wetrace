# sensevoiced - wetrace 本地语音识别侧车

基于 [FunAudioLLM/SenseVoiceSmall](https://huggingface.co/FunAudioLLM/SenseVoiceSmall) 的本地 ASR 服务，
暴露 **OpenAI 兼容** 的 `/v1/audio/transcriptions` 接口，供 wetrace 的内置 `internal/tts` client 直接调用。

## 为什么用它

- 微信语音转写：**完全本地、不外泄、零 API 费用**
- 对中文/粤语/中英混讲的识别质量显著优于 Whisper-Small
- 模型 ~900MB，加载后内存约 1.5~2GB
- 单段 10s 音频在普通笔记本 CPU 上推理约 0.5~0.7s（RTF ≈ 0.07）
- 非自回归模型，**一次前向出全文**，无 Whisper 那种逐 token 解码

## 目录结构

```
sensevoiced/
├── python/              # 项目私有 Python 3.12.7（nuget embed 版，独立不污染系统）
├── server.py            # FastAPI 服务端
├── start.ps1            # 启动脚本
├── stop.ps1             # 停止脚本
└── README.md
```

模型下载到用户级缓存：`%USERPROFILE%\.cache\modelscope\hub\models\iic\SenseVoiceSmall`（~900MB，仅首次）。

## 依赖

已通过 pip 安装在项目私有 `python/` 中，无需重装。如果需要从零搭建（例如新机器）：

```powershell
# 1) 下载并解压私有 Python（参考工程根目录构建说明，nuget 上的 python 3.12.7）
# 2) 安装依赖
./python/python.exe -m pip install -i https://mirrors.tencent.com/pypi/simple `
    funasr funasr-onnx modelscope jieba `
    fastapi "uvicorn[standard]" python-multipart `
    imageio-ffmpeg

# 3) 安装 PyTorch CPU 版（torch / torchaudio）
./python/python.exe -m pip install --index-url https://download.pytorch.org/whl/cpu torch torchaudio
```

## 启动

```powershell
# 前台启动，默认 127.0.0.1:5210，首次请求再加载模型
./start.ps1

# 后台启动（推荐），日志写到 server.log / server.err.log
./start.ps1 -Background

# 启动时即加载模型（首次请求 0 等待）
./start.ps1 -Background -Preload

# 自定义端口
./start.ps1 -Background -Port 5310
```

停止：

```powershell
./stop.ps1               # 默认 5210
./stop.ps1 -Port 5310
```

## 对接 wetrace

在工程根 `.env` 中：

```env
TTS_ENABLED=true
TTS_BASE_URL=http://127.0.0.1:5210/v1
TTS_API_KEY=local            # 任意非空字符串
TTS_MODEL=sensevoice-small   # 协议字段，本地服务会忽略
```

然后重启 wetrace，前端"缺失语音转写"弹窗 / `POST /api/v1/media/voice/transcribe` 即可走本地识别。

## 接口

### `GET /health`

返回 `{"status": "ok", "model_loaded": bool}`。

### `POST /v1/audio/transcriptions`

`multipart/form-data`：

| 字段 | 必填 | 说明 |
|---|---|---|
| `file` | ✓ | 音频二进制（支持 mp3 / wav / m4a / flac / ogg / opus 等 ffmpeg 支持的格式） |
| `language` | | `auto`(默认) / `zh` / `en` / `yue` / `ja` / `ko` |
| `model` | | 忽略，兼容 OpenAI 协议 |
| `response_format` | | 忽略 |
| `prompt` | | 忽略 |
| `temperature` | | 忽略 |

返回：

```json
{
  "text": "识别后的纯文本",
  "raw": "<|zh|><|HAPPY|><|Speech|><|withitn|>识别后的纯文本"
}
```

`raw` 里 `<|...|>` 是 SenseVoice 的特殊 token：语种 / 情感（HAPPY/SAD/ANGRY/NEUTRAL...） / 音频事件（Speech/Laughter/Applause...） / 是否启用 ITN。

## 成本预估

- 进程内存：加载完约 1.5~2GB（PyTorch + 模型权重）
- 磁盘：私有 Python ~30MB + torch + 依赖 ~2GB + 模型 ~900MB ≈ 3GB
- 启动加载：~9s（含从磁盘读 894MB 权重）
- 推理：10s 音频约 0.5~0.7s（RTF ≈ 0.07）

## 常见问题

**Q: 想要更小磁盘占用？**
A: 用 `funasr-onnx` 的 `model_quant.onnx`（INT8，~230MB）能省到 ~500MB 总占用，但需要先把 `model.pt` 用 `funasr.AutoModel.export(quantize=True)` 离线导出一次（依赖 onnxscript 等），后续可以 `pip uninstall torch torchaudio funasr` 把 torch 链路全部移除。这个改造留待后续。

**Q: 端口被占用？**
A: `./stop.ps1` 或 `./start.ps1 -Port 其他端口` + 改 `.env` 里 `TTS_BASE_URL`。

**Q: 第一次请求超时？**
A: 首次请求会触发约 900MB 的模型下载，慢的话十几分钟。建议用 `-Preload` 启动，先把模型在后台拉好。
