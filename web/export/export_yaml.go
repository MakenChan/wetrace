package export

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/afumu/wetrace/internal/model"
	"github.com/afumu/wetrace/store/types"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// yamlInstruction 顶部说明，喂给 Claude 用
const yamlInstruction = `本文件为微信聊天记录的 LLM 分析格式。
字段：i=日内消息序号；t=时间(HH:mm:ss)；u=发送者(我=本人，其余为对方昵称)；
内容字段（互斥，每条消息出现其中一个）：
  msg=文本；voice=语音转写文字；
  voip=语音/视频通话事件，格式 "<视频通话|语音通话> <状态文案>"，
       状态文案直接来自微信原文，例如：
         "通话时长 05:30"（接通）/ "已取消"（自己取消）/ "对方已取消" /
         "对方无应答" / "未应答" / "对方已拒绝" / "已在其它设备接听" 等；
  image/video/sticker/redpacket/transfer=true 表示该类型消息出现但无文本内容；
  location=地点描述；card=名片标题；share=公众号/链接/小程序/文件/视频号 标题；
  system=系统提示；other=未归类内容。
ref=被引用消息(发送者: 内容)。已剔除 URL、头像、ID 等噪音；保留微信表情码以保留情绪信号。
按日期分组(days)，i 在每日内从 1 递增，便于按天分析与定位。`

// urlPattern 匹配 http(s)/www 链接
var urlPattern = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)

// atPattern 匹配 "@昵称\u2005" 微信群提及
var atPattern = regexp.MustCompile(`@([^\s\x{2005}]+)\x{2005}`)

// yamlMessage 一条消息的精简结构
type yamlMessage struct {
	I        int    `yaml:"i"`
	T        string `yaml:"t"`
	U        string `yaml:"u"`
	Msg      string `yaml:"msg,omitempty"`
	Voice    string `yaml:"voice,omitempty"`
	Image    bool   `yaml:"image,omitempty"`
	Video    bool   `yaml:"video,omitempty"`
	VOIP     string `yaml:"voip,omitempty"`
	Sticker  bool   `yaml:"sticker,omitempty"`
	RedPack  bool   `yaml:"redpacket,omitempty"`
	Transfer bool   `yaml:"transfer,omitempty"`
	Location string `yaml:"location,omitempty"`
	Card     string `yaml:"card,omitempty"`
	Share    string `yaml:"share,omitempty"`
	System   string `yaml:"system,omitempty"`
	Other    string `yaml:"other,omitempty"`
	Ref      string `yaml:"ref,omitempty"`
}

type yamlDay struct {
	Date     string        `yaml:"date"`
	Messages []yamlMessage `yaml:"messages"`
}

type yamlRoot struct {
	Instruction string    `yaml:"instruction"`
	Talker      string    `yaml:"talker"`
	Range       string    `yaml:"range"`
	Days        []yamlDay `yaml:"days"`
}

// ExportChatYAML 导出聊天记录为 AI 分析友好的 YAML 格式
func (s *Service) ExportChatYAML(ctx context.Context, talker, talkerName string, startTime, endTime time.Time) ([]byte, error) {
	query := types.MessageQuery{
		Talker:    talker,
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     200000,
		Offset:    0,
	}
	messages, err := s.Store.GetMessages(ctx, query)
	if err != nil {
		return nil, err
	}

	log.Info().Int("count", len(messages)).Str("talker", talkerName).Msg("ExportYAML processing")

	// 按日期分组，使用本地时区
	loc := time.Local
	dayBuckets := make(map[string][]*model.Message)
	dayOrder := make([]string, 0)
	for _, msg := range messages {
		date := msg.Time.In(loc).Format("2006-01-02")
		if _, ok := dayBuckets[date]; !ok {
			dayOrder = append(dayOrder, date)
		}
		dayBuckets[date] = append(dayBuckets[date], msg)
	}

	root := yamlRoot{
		Instruction: yamlInstruction,
		Talker:      talkerName,
		Range:       fmt.Sprintf("%s ~ %s", startTime.In(loc).Format("2006-01-02"), endTime.In(loc).Format("2006-01-02")),
		Days:        make([]yamlDay, 0, len(dayOrder)),
	}

	for _, date := range dayOrder {
		dayMsgs := dayBuckets[date]
		day := yamlDay{
			Date:     date,
			Messages: make([]yamlMessage, 0, len(dayMsgs)),
		}
		for idx, msg := range dayMsgs {
			ym := buildYAMLMessage(msg, idx+1, loc)
			day.Messages = append(day.Messages, ym)
		}
		root.Days = append(root.Days, day)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("yaml 编码失败: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("yaml 关闭失败: %w", err)
	}
	return buf.Bytes(), nil
}

// buildYAMLMessage 把一条 Message 转成 yamlMessage
func buildYAMLMessage(msg *model.Message, idx int, loc *time.Location) yamlMessage {
	ym := yamlMessage{
		I: idx,
		T: msg.Time.In(loc).Format("15:04:05"),
		U: senderForYAML(msg),
	}

	switch msg.Type {
	case model.MessageTypeText:
		ym.Msg = cleanText(msg.Content)

	case model.MessageTypeImage:
		ym.Image = true

	case model.MessageTypeVoice:
		ym.Voice = pickVoiceText(msg)

	case model.MessageTypeCard:
		// 微信名片，尝试取昵称作为标题
		title := stringOf(msg.Contents, "nickname")
		if title == "" {
			title = stringOf(msg.Contents, "title")
		}
		if title == "" {
			ym.Card = "[名片]"
		} else {
			ym.Card = "[名片] " + cleanText(title)
		}

	case model.MessageTypeVideo:
		ym.Video = true

	case model.MessageTypeAnimation:
		ym.Sticker = true

	case model.MessageTypeLocation:
		ym.Location = formatLocation(msg)

	case model.MessageTypeShare:
		fillShareYAML(&ym, msg)

	case model.MessageTypeVOIP:
		ym.VOIP = formatVOIP(msg)

	case model.MessageTypeSystem:
		text := cleanText(msg.Content)
		if text == "" {
			text = "[系统消息]"
		}
		ym.System = text

	default:
		text := cleanText(msg.Content)
		if text == "" {
			text = fmt.Sprintf("[Type:%d]", msg.Type)
		}
		ym.Other = text
	}

	// 引用：省事派 —— 直接用 refer 里的发送者 + 截断后的纯文本
	if refer := pickRefer(msg); refer != "" {
		ym.Ref = refer
	}

	return ym
}

// senderForYAML 自己固定为 "我"，对方用昵称 → 备注 → wxid
func senderForYAML(msg *model.Message) string {
	if msg.IsSelf {
		return "我"
	}
	if msg.SenderName != "" {
		return msg.SenderName
	}
	if msg.Sender != "" {
		return msg.Sender
	}
	return "对方"
}

// pickVoiceText 优先级：微信识别 → 本地识别 → [未识别]
func pickVoiceText(msg *model.Message) string {
	if msg.Contents == nil {
		return "[未识别]"
	}
	source := stringOf(msg.Contents, "voiceTextSource")
	text := stringOf(msg.Contents, "voiceText")

	// 优先级 1: 微信
	if source == "wechat" && text != "" {
		return cleanText(text)
	}
	// 优先级 2: 本地识别（来源未明也归此处）
	if text != "" {
		return cleanText(text)
	}
	return "[未识别]"
}

// fillShareYAML 处理 Type 49 各种分享子类型
func fillShareYAML(ym *yamlMessage, msg *model.Message) {
	title := stringOf(msg.Contents, "title")
	switch msg.SubType {
	case model.MessageSubTypeText, model.MessageSubTypeLink, model.MessageSubTypeLink2:
		if title == "" {
			ym.Share = "[链接]"
		} else {
			ym.Share = "[链接] " + cleanText(title)
		}
	case model.MessageSubTypeFile:
		if title == "" {
			ym.Share = "[文件]"
		} else {
			ym.Share = "[文件] " + cleanText(title)
		}
	case model.MessageSubTypeGIF:
		ym.Sticker = true
	case model.MessageSubTypeMergeForward:
		if title == "" {
			ym.Share = "[合并转发]"
		} else {
			ym.Share = "[合并转发] " + cleanText(title)
		}
	case model.MessageSubTypeNote:
		if title == "" {
			ym.Share = "[笔记]"
		} else {
			ym.Share = "[笔记] " + cleanText(title)
		}
	case model.MessageSubTypeMiniProgram, model.MessageSubTypeMiniProgram2:
		if title == "" {
			ym.Share = "[小程序]"
		} else {
			ym.Share = "[小程序] " + cleanText(title)
		}
	case model.MessageSubTypeChannel:
		if title == "" {
			ym.Share = "[视频号]"
		} else {
			ym.Share = "[视频号] " + cleanText(title)
		}
	case model.MessageSubTypeQuote:
		// 引用消息，正文是文本
		ym.Msg = cleanText(msg.Content)
	case model.MessageSubTypePat:
		ym.System = cleanText(msg.Content)
	case model.MessageSubTypeChannelLive:
		if title == "" {
			ym.Share = "[视频号直播]"
		} else {
			ym.Share = "[视频号直播] " + cleanText(title)
		}
	case model.MessageSubTypeChatRoomNotice:
		if title == "" {
			ym.Share = "[群公告]"
		} else {
			ym.Share = "[群公告] " + cleanText(title)
		}
	case model.MessageSubTypeMusic:
		if title == "" {
			ym.Share = "[音乐]"
		} else {
			ym.Share = "[音乐] " + cleanText(title)
		}
	case model.MessageSubTypePay:
		ym.Transfer = true
		if msg.Content != "" {
			// msg.Content 形如 "[转账|发送 ¥10.00](备注)" —— 去掉前缀直接给数额备注更友好
			ym.Other = cleanText(msg.Content)
		}
	case model.MessageSubTypeRedEnvelope, model.MessageSubTypeRedEnvelopeCover:
		ym.RedPack = true
	default:
		if title != "" {
			ym.Share = "[分享] " + cleanText(title)
		} else {
			ym.Share = "[分享]"
		}
	}
}

// formatVOIP 把 type=50 通话消息整理成 "<视频通话|语音通话> <状态文案>"
//
// 数据来源（解析见 internal/model/message.go MessageTypeVOIP 分支）：
//
//	contents.calltype: "voice" | "video"   （来自 <room_type>，0=video, 1=voice）
//	contents.text:     微信原文文案，如 "通话时长 05:30" / "已取消" / "已在其它设备接听" 等
//
// 若两者均缺失，回退到 msg.Content（已带 "[语音通话]" 等前缀）或 "[通话]"。
func formatVOIP(msg *model.Message) string {
	callType := stringOf(msg.Contents, "calltype")
	label := "语音通话"
	if callType == "video" {
		label = "视频通话"
	}
	text := strings.TrimSpace(stringOf(msg.Contents, "text"))
	if text != "" {
		return label + " " + text
	}
	// 回退：msg.Content 形如 "[语音通话] 通话时长 05:30"
	if c := strings.TrimSpace(msg.Content); c != "" {
		return c
	}
	return "[" + label + "]"
}

// formatLocation 拼接位置文本：label + cityname
func formatLocation(msg *model.Message) string {
	label := stringOf(msg.Contents, "label")
	city := stringOf(msg.Contents, "cityname")
	parts := make([]string, 0, 2)
	if city != "" {
		parts = append(parts, city)
	}
	if label != "" {
		parts = append(parts, label)
	}
	if len(parts) == 0 {
		return "[位置]"
	}
	return strings.Join(parts, " ")
}

// pickRefer 提取被引用消息的省事描述："发送者: 截断内容"
func pickRefer(msg *model.Message) string {
	if msg.Contents == nil {
		return ""
	}
	raw, ok := msg.Contents["refer"]
	if !ok || raw == nil {
		return ""
	}
	refer, ok := raw.(*model.Message)
	if !ok || refer == nil {
		return ""
	}
	sender := senderForYAML(refer)

	// 直接用被引用消息的可读内容
	var content string
	switch refer.Type {
	case model.MessageTypeText:
		content = cleanText(refer.Content)
	case model.MessageTypeImage:
		content = "[图片]"
	case model.MessageTypeVideo:
		content = "[视频]"
	case model.MessageTypeVoice:
		content = "[语音] " + pickVoiceText(refer)
	case model.MessageTypeAnimation:
		content = "[表情]"
	case model.MessageTypeVOIP:
		content = formatVOIP(refer)
	case model.MessageTypeLocation:
		content = formatLocation(refer)
	case model.MessageTypeShare:
		title := stringOf(refer.Contents, "title")
		if title != "" {
			content = "[分享] " + cleanText(title)
		} else {
			content = "[分享]"
		}
	default:
		content = cleanText(refer.Content)
	}

	// 截断长引用内容到 80 字
	content = truncateRunes(content, 80)
	if content == "" {
		content = "[引用]"
	}
	return sender + ": " + content
}

// cleanText 降噪：去 URL、清理 @用户的 \u2005、压缩多余空白
func cleanText(s string) string {
	if s == "" {
		return ""
	}
	// URL → [链接]
	s = urlPattern.ReplaceAllString(s, "[链接]")
	// 去除 @用户名后的 \u2005
	s = atPattern.ReplaceAllString(s, "@$1 ")
	// 去掉残留的 \u2005
	s = strings.ReplaceAll(s, "\u2005", " ")
	// 折叠超过 2 个连续空格为 1 个
	s = strings.TrimRight(s, " \t\r\n")
	s = strings.TrimLeft(s, " \t")
	return s
}

// truncateRunes 按 rune 截断到 max
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max]) + "…"
}

// stringOf 从 Contents 里安全取 string
func stringOf(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
