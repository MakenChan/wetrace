import { useMemo } from "react"
import { createPortal } from "react-dom"
import { format } from "date-fns"
import { X, Mic } from "lucide-react"
import { Button } from "../ui/button"
import { MessageType } from "@/types/message"

interface VoiceMissingModalProps {
  isOpen: boolean
  onClose: () => void
  /** 已按时间升序排序的全量消息（来自 useMessages） */
  messages: Array<any>
  /** 当点击某天时，把该天的目标消息索引（在 items 列表中的 index）抛出去做滚动 */
  dateMap: Record<string, number>
  onSelectDay: (index: number) => void
}

interface DayStat {
  dateKey: string          // yyyy-MM-dd
  total: number            // 当天语音总数
  missing: number          // 当天 voiceText 为空的语音数
}

export function VoiceMissingModal({
  isOpen,
  onClose,
  messages,
  dateMap,
  onSelectDay,
}: VoiceMissingModalProps) {
  // 按天聚合：仅统计语音消息（type=34）
  const { stats, totalVoice, totalMissing } = useMemo(() => {
    const byDay = new Map<string, DayStat>()
    let totalVoice = 0
    let totalMissing = 0

    for (const m of messages) {
      if (m.type !== MessageType.Voice) continue
      const d = new Date((m.createTime ?? 0) * 1000)
      if (isNaN(d.getTime())) continue
      const key = format(d, "yyyy-MM-dd")
      let s = byDay.get(key)
      if (!s) {
        s = { dateKey: key, total: 0, missing: 0 }
        byDay.set(key, s)
      }
      s.total += 1
      totalVoice += 1
      const hasText = !!(m.contents && (m.contents as any).voiceText)
      if (!hasText) {
        s.missing += 1
        totalMissing += 1
      }
    }

    // 仅展示有缺失的天，按日期倒序
    const stats = Array.from(byDay.values())
      .filter(s => s.missing > 0)
      .sort((a, b) => (a.dateKey < b.dateKey ? 1 : -1))
    return { stats, totalVoice, totalMissing }
  }, [messages])

  if (!isOpen) return null

  return createPortal(
    <div className="fixed inset-0 z-[150] flex items-center justify-center bg-black/50 backdrop-blur-sm animate-in fade-in duration-200">
      <div
        className="bg-background border shadow-2xl rounded-xl p-6 w-[420px] max-w-[92vw] animate-in zoom-in-95 duration-200 relative"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between mb-4 border-b pb-4">
          <div>
            <h3 className="text-lg font-bold flex items-center gap-2">
              <Mic className="w-5 h-5" />
              缺失语音转写
            </h3>
            <p className="text-xs text-muted-foreground mt-1">
              共 {totalVoice} 条语音，
              <span className="text-foreground font-medium"> {totalMissing} 条 </span>
              未生成转写
            </p>
          </div>
          <Button variant="ghost" size="icon" onClick={onClose} className="rounded-full">
            <X className="h-5 w-5" />
          </Button>
        </div>

        {stats.length === 0 ? (
          <div className="py-10 text-center text-sm text-muted-foreground">
            该会话所有语音都已有转写 🎉
          </div>
        ) : (
          <div className="max-h-[60vh] overflow-y-auto -mx-2 px-2">
            <ul className="flex flex-col gap-1.5">
              {stats.map(s => {
                const idx = dateMap[s.dateKey]
                const jumpable = idx !== undefined
                return (
                  <li key={s.dateKey}>
                    <button
                      type="button"
                      disabled={!jumpable}
                      onClick={() => jumpable && onSelectDay(idx)}
                      className="w-full flex items-center justify-between gap-3 px-3 py-2 rounded-md border border-transparent hover:border-primary/30 hover:bg-primary/5 disabled:opacity-50 disabled:cursor-not-allowed transition-colors text-left"
                      title={jumpable ? "跳到该天" : "未在当前列表中找到该天的位置"}
                    >
                      <span className="font-mono text-sm">{s.dateKey}</span>
                      <span className="text-xs">
                        <span className="text-foreground font-semibold">{s.missing}</span>
                        <span className="text-muted-foreground"> / {s.total} 条缺失</span>
                      </span>
                    </button>
                  </li>
                )
              })}
            </ul>
          </div>
        )}

        <div className="mt-4 pt-4 border-t text-xs text-muted-foreground leading-relaxed">
          点击日期跳到那天的第一条消息，再在缺失的语音上点 <Mic className="inline w-3 h-3 align-text-bottom" /> 旁边的 <span className="font-mono">Type</span> 图标手动转文字。
        </div>
      </div>
      <div className="absolute inset-0 -z-10" onClick={onClose} />
    </div>,
    document.body
  )
}
