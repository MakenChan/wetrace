import type { Message } from "@/types/message"
import { Phone, Video } from "lucide-react"

interface VoiceCallMessageProps {
  message: Message
  isSelf: boolean
}

/**
 * 语音 / 视频通话气泡（type=50）。
 *
 * 后端字段（来源： <voipmsg type="VoIPBubbleMsg"> ）：
 *   contents.calltype: "voice" | "video"            （来自 room_type，0=video, 1=voice）
 *   contents.text:     微信原文文案，可能为：
 *                        "通话时长 MM:SS" / "通话时长 HH:MM:SS"
 *                        "已取消" / "对方已取消"
 *                        "对方无应答" / "未应答"
 *                        "对方已拒绝"
 *                        "已在其它设备接听" / "已在其它设备拒绝"
 *   contents.duration: 接通秒数（仅在接通时存在）
 *   contents.msgtype:  100=主动落库, 101=被动落库（如"已在其它设备接听"）
 *   content:           人类可读兜底，如 "[语音通话] 通话时长 05:30"
 */
export function VoiceCallMessage({ message, isSelf }: VoiceCallMessageProps) {
  const callType = (message.contents?.calltype as string | undefined) || ""
  const isVideo = callType === "video"
  const Icon = isVideo ? Video : Phone

  // 标签
  const label = isVideo ? "视频通话" : (callType === "voice" ? "语音通话" : "通话")

  // 详情：优先 contents.text，否则从 content 中剥出方括号后的部分
  let detail = ""
  const rawText = (message.contents?.text as string | undefined) || ""
  if (rawText) {
    detail = rawText
  } else if (message.content) {
    // "[语音通话] 通话时长 0:42" -> "通话时长 0:42"
    const m = message.content.match(/^\[[^\]]+\]\s*(.*)$/)
    detail = m ? m[1].trim() : message.content
  }

  return (
    <div className="flex items-center gap-2 min-w-[120px]">
      <Icon
        className={isSelf ? "w-4 h-4 shrink-0 opacity-90" : "w-4 h-4 shrink-0 text-muted-foreground"}
      />
      <div className="flex flex-col leading-tight">
        <span className="text-sm">{label}</span>
        {detail && (
          <span className={"text-[11px] mt-0.5 " + (isSelf ? "opacity-80" : "text-muted-foreground")}>
            {detail}
          </span>
        )}
      </div>
    </div>
  )
}
