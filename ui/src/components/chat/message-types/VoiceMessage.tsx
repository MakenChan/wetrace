import { useState, useRef, useEffect } from "react"
import { mediaApi } from "@/api/media"
import { cn } from "@/lib/utils"
import { Loader2, Type } from "lucide-react"

/**
 * 从各种错误对象里提取对用户友好的短提示。
 * 特别处理：后端返回的底层 TCP 拒连信息（说明本地 SenseVoice 服务没启动）。
 */
function extractErrorMessage(err: unknown): string {
  const anyErr = err as {
    response?: { data?: { message?: string; error?: string } }
    message?: string
  } | null | undefined
  const raw: string =
    anyErr?.response?.data?.message ||
    anyErr?.response?.data?.error ||
    anyErr?.message ||
    String(err ?? "")
  const s = raw.trim()
  if (!s) return "未知错误"
  // 典型：... dial tcp 127.0.0.1:5210: connectex: No connection could be made ...
  if (/dial tcp.*(5210|refused|actively refused)/i.test(s) || /connectex/i.test(s)) {
    return "本地语音识别服务未启动（端口 5210）"
  }
  if (/timeout/i.test(s)) return "语音识别服务响应超时"
  // 太长的后端堆栈裁一下
  return s.length > 120 ? s.slice(0, 120) + "…" : s
}


interface VoiceMessageProps {
  id: string
  isSelf?: boolean
  duration?: number
  /**
   * 已有的语音转文字结果。可能来自：
   *  - 微信客户端 packed_info_data（textSource=wechat）
   *  - wetrace 本地 SenseVoice 缓存（textSource=local）
   * 若存在则组件挂载时直接展示，无需再点「转文字」按钮。
   */
  initialText?: string
  /** 已有文本的来源；首次由用户点击 mic 本地转写后，内部会置为 "local"。 */
  textSource?: "wechat" | "local" | string
}

export function VoiceMessage({ id, isSelf, duration, initialText, textSource }: VoiceMessageProps) {
  const [isPlaying, setIsPlaying] = useState(false)
  const [animationStep, setAnimationStep] = useState(3)
  const audioRef = useRef<HTMLAudioElement | null>(null)
  const animationIntervalRef = useRef<number | null>(null)
  const [transcribeText, setTranscribeText] = useState<string | null>(initialText ?? null)
  const [isTranscribing, setIsTranscribing] = useState(false)
  // 转写失败信息。与 transcribeText 分离，避免把错误当成识别结果展示、也避免按钮消失。
  const [transcribeError, setTranscribeError] = useState<string | null>(null)
  // 当前文本来源：优先使用 props 给的；若用户点击 mic 成功转写则变为 "local"。
  const [source, setSource] = useState<"wechat" | "local" | null>(
    initialText ? (textSource === "wechat" ? "wechat" : textSource === "local" ? "local" : "wechat") : null
  )

  const voiceUrl = mediaApi.getVoiceUrl(id)
  
  // Clean up duration for display
  // If duration is 0 or undefined, default to 1 for better UX? 
  // Or keep 0 if that's the data. The user said "shows 0 seconds", so data is likely 0.
  // We will display whatever is passed, but format it nicely.
  const displayDuration = duration ? Math.round(duration / 1000) : 0

  useEffect(() => {
    const audio = new Audio(voiceUrl)
    audio.onended = () => {
      setIsPlaying(false)
      stopAnimation()
    }
    audio.onpause = () => {
      setIsPlaying(false)
      stopAnimation()
    }
    audio.onplay = () => {
      setIsPlaying(true)
      startAnimation()
    }
    audio.onerror = () => {
        setIsPlaying(false)
        stopAnimation()
    }
    
    audioRef.current = audio

    return () => {
      audio.pause()
      audioRef.current = null
      stopAnimation()
    }
  }, [voiceUrl])

  const startAnimation = () => {
    if (animationIntervalRef.current) clearInterval(animationIntervalRef.current)
    setAnimationStep(1) 
    
    // Animation loop: 1 -> 2 -> 3 -> 1 ...
    // Step 0 is usually reserved for "off" or dot only, but standard wechat loop is:
    // Dot + 1 arc -> Dot + 2 arcs -> Dot + 3 arcs
    animationIntervalRef.current = setInterval(() => {
      setAnimationStep(prev => (prev % 3) + 1)
    }, 500)
  }

  const stopAnimation = () => {
    if (animationIntervalRef.current) clearInterval(animationIntervalRef.current)
    setAnimationStep(3) // Reset to full static
  }

  const togglePlay = async () => {
    if (!audioRef.current) return

    if (isPlaying) {
      audioRef.current.pause()
    } else {
      try {
        if (audioRef.current.ended) {
          audioRef.current.currentTime = 0
        }
        await audioRef.current.play()
      } catch (error) {
        console.error("Failed to play voice:", error)
      }
    }
  }

  const handleTranscribe = async (e: React.MouseEvent) => {
    e.stopPropagation()
    if (isTranscribing || transcribeText !== null) return
    setIsTranscribing(true)
    setTranscribeError(null)
    try {
      const res = await mediaApi.transcribeVoice(id)
      const text = (res.text || "").trim()
      if (!text) {
        setTranscribeError("未识别出文字内容")
        return
      }
      setTranscribeText(text)
      setSource("local")
    } catch (err) {
      // 错误不写入 transcribeText：保留 T 按钮，允许重试；错误独立红字展示。
      const msg = extractErrorMessage(err)
      setTranscribeError(msg)
    } finally {
      setIsTranscribing(false)
    }
  }

  // Calculate width. Base 60px, + px per second. Max 200px.
  // WeChat logic roughly: min 40px, max ~160px.
  const width = Math.min(200, Math.max(70, 70 + (displayDuration * 5)))

  return (
    <div className="flex flex-col gap-1">
      <div
        className={cn(
          "flex items-center gap-2 py-1 px-3 cursor-pointer select-none transition-all rounded-md hover:bg-black/5 dark:hover:bg-white/5",
          isSelf ? "flex-row-reverse" : "flex-row"
        )}
        style={{ width: duration ? `${width}px` : 'auto' }}
        onClick={(e) => {
          e.stopPropagation()
          togglePlay()
        }}
      >
        {/* Icon */}
        <div className={cn(
          "shrink-0 flex items-center justify-center w-5 h-5",
          isSelf ? "rotate-180" : ""
        )}>
          <VoiceIcon step={animationStep} />
        </div>

        {/* Duration */}
        <span className="text-sm text-foreground/80 min-w-[16px] text-center">
          {displayDuration}"
        </span>

        {/* Transcribe button */}
        {transcribeText === null && (
          <button
            className={cn(
              "shrink-0 ml-1 p-0.5 rounded hover:bg-black/10 dark:hover:bg-white/10 transition-colors",
              transcribeError
                ? "text-red-500 hover:text-red-600"
                : "text-muted-foreground hover:text-foreground"
            )}
            onClick={handleTranscribe}
            disabled={isTranscribing}
            title={transcribeError ? `转文字失败，点击重试：${transcribeError}` : "转文字"}
          >
            {isTranscribing ? (
              <Loader2 className="w-3.5 h-3.5 animate-spin" />
            ) : (
              <Type className="w-3.5 h-3.5" />
            )}
          </button>
        )}
      </div>

      {/* Error banner：只在无文本 + 有错误时显示；点击可重试 */}
      {transcribeText === null && transcribeError && (
        <div
          className={cn(
            "text-xs px-3 py-1 max-w-[260px] cursor-pointer select-text text-red-600 dark:text-red-400",
            isSelf ? "text-right" : "text-left"
          )}
          onClick={(e) => {
            e.stopPropagation()
            if (!isTranscribing) handleTranscribe(e)
          }}
          title="点击重试"
        >
          <span className="mr-1 align-middle text-[10px] opacity-80">[失败]</span>
          {transcribeError}
        </div>
      )}

      {/* Transcribed text */}
      {transcribeText !== null && (
        <div className={cn(
          "text-xs text-foreground/70 px-3 py-1 max-w-[240px]",
          isSelf ? "text-right" : "text-left"
        )}>
          {source === "wechat" && (
            <span className="mr-1 align-middle text-[10px] text-sky-600/80 dark:text-sky-400/80">[微信]</span>
          )}
          {source === "local" && (
            <span className="mr-1 align-middle text-[10px] text-emerald-600/80 dark:text-emerald-400/80">[识别]</span>
          )}
          {transcribeText}
        </div>
      )}
    </div>
  )
}

function VoiceIcon({ step }: { step: number }) {
  // WeChat style icon: Dot + 3 Arcs radiating to the RIGHT.
  // For sender, the parent container rotates it 180 deg.
  
  return (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
       {/* Core Dot - Always visible */}
       <circle cx="4" cy="12" r="2" fill="currentColor" />

       {/* Arc 1 */}
       <path 
         d="M9 8C10.5 9.5 10.5 14.5 9 16" 
         stroke="currentColor" 
         strokeWidth="2" 
         strokeLinecap="round" 
         strokeLinejoin="round"
         className="transition-opacity duration-150"
         style={{ opacity: step >= 1 ? 1 : 0.3 }}
       />

       {/* Arc 2 */}
       <path 
         d="M13 5C16 8 16 16 13 19" 
         stroke="currentColor" 
         strokeWidth="2" 
         strokeLinecap="round" 
         strokeLinejoin="round"
         className="transition-opacity duration-150"
         style={{ opacity: step >= 2 ? 1 : 0.3 }}
       />

       {/* Arc 3 */}
       <path 
         d="M17 2C22 7 22 17 17 22" 
         stroke="currentColor" 
         strokeWidth="2" 
         strokeLinecap="round" 
         strokeLinejoin="round"
         className="transition-opacity duration-150"
         style={{ opacity: step >= 3 ? 1 : 0.3 }}
       />
    </svg>
  )
}
