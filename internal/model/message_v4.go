package model

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/afumu/wetrace/internal/model/wxproto"
	"github.com/afumu/wetrace/pkg/util/zstd"
	"google.golang.org/protobuf/proto"
)

// CREATE TABLE Msg_md5(talker)(
// local_id INTEGER PRIMARY KEY AUTOINCREMENT,
// server_id INTEGER,
// local_type INTEGER,
// sort_seq INTEGER,
// real_sender_id INTEGER,
// create_time INTEGER,
// status INTEGER,
// upload_status INTEGER,
// download_status INTEGER,
// server_seq INTEGER,
// origin_source INTEGER,
// source TEXT,
// message_content TEXT,
// compress_content TEXT,
// packed_info_data BLOB,
// WCDB_CT_message_content INTEGER DEFAULT NULL,
// WCDB_CT_source INTEGER DEFAULT NULL
// )
type MessageV4 struct {
	SortSeq         int64  `json:"sort_seq"`         // 消息序号，10位时间戳 + 3位序号
	ServerID        int64  `json:"server_id"`        // 消息 ID，用于关联 voice
	LocalType       int64  `json:"local_type"`       // 消息类型
	UserName        string `json:"user_name"`        // 发送人，通过 Join Name2Id 表获得
	CreateTime      int64  `json:"create_time"`      // 消息创建时间，10位时间戳
	MessageContent  []byte `json:"message_content"`  // 消息内容，文字聊天内容 或 zstd 压缩内容
	CompressContent []byte `json:"compress_content"` // 非文字内容 (V4 中可能包含语音数据)
	PackedInfoData  []byte `json:"packed_info_data"` // 额外数据，类似 proto，格式与 v3 有差异
	Status          int    `json:"status"`           // 消息状态，2 是已发送，4 是已接收，可以用于判断 IsSender（FIXME 不准, 需要判断 UserName）
}

func (m *MessageV4) Wrap(talker string) *Message {

	_m := &Message{
		Seq:        m.SortSeq,
		Time:       time.Unix(m.CreateTime, 0),
		Talker:     talker,
		IsChatRoom: strings.HasSuffix(talker, "@chatroom"),
		Sender:     m.UserName,
		Type:       m.LocalType,
		Contents:   make(map[string]interface{}),
		Version:    WeChatV4,
	}

	// FIXME 后续通过 UserName 判断是否是自己发送的消息，目前可能不准确
	_m.IsSelf = m.Status == 2 || (!_m.IsChatRoom && talker != m.UserName)

	content := ""
	if bytes.HasPrefix(m.MessageContent, []byte{0x28, 0xb5, 0x2f, 0xfd}) {
		if b, err := zstd.Decompress(m.MessageContent); err == nil {
			content = string(b)
		}
	} else {
		content = string(m.MessageContent)
	}

	if _m.IsChatRoom {
		split := strings.SplitN(content, ":\n", 2)
		if len(split) == 2 {
			_m.Sender = split[0]
			content = split[1]
			_m.IsSelf = false
		} else {
			// 如果没有分割出发送者，且不是系统消息，说明是自己发送的
			// (群聊中自己发送的消息没有 wxid:\n 前缀)
			if _m.Type != 10000 {
				_m.IsSelf = true
			}
		}
	}

	_m.ParseMediaInfo(content)

	// 语音消息
	if _m.Type == 34 {
		_m.Contents["voice"] = fmt.Sprint(m.ServerID)
		if len(m.CompressContent) > 0 {
			_m.Contents["_raw_data"] = m.CompressContent
		}
		// 微信客户端将「语音转文字」结果（用户首次点过「转文字」后产生）落到了
		// V4 主消息表的 packed_info_data BLOB 中（field 5 → field 2，UTF-8）。
		// 这里直接做 wire-level 解析，避免修改 protoc 生成的 PackedInfo 结构。
		if voiceText := extractVoiceText(m.PackedInfoData); voiceText != "" {
			_m.Contents["voiceText"] = voiceText
		}
	}

	if len(m.PackedInfoData) != 0 {
		if packedInfo := ParsePackedInfo(m.PackedInfoData); packedInfo != nil {
			// FIXME 尝试解决 v4 版本 xml 数据无法匹配到 hardlink 记录的问题
			if _m.Type == 3 && packedInfo.Image != nil {
				_talkerMd5Bytes := md5.Sum([]byte(talker))
				talkerMd5 := hex.EncodeToString(_talkerMd5Bytes[:])
				_m.Contents["path"] = filepath.Join("msg", "attach", talkerMd5, _m.Time.Format("2006-01"), "Img", packedInfo.Image.Md5)
			}
			if _m.Type == 43 && packedInfo.Video != nil {
				_m.Contents["path"] = filepath.Join("msg", "video", _m.Time.Format("2006-01"), packedInfo.Video.Md5)
			}
		}
	}

	return _m
}

func ParsePackedInfo(b []byte) *wxproto.PackedInfo {
	var pbMsg wxproto.PackedInfo
	if err := proto.Unmarshal(b, &pbMsg); err != nil {
		return nil
	}
	return &pbMsg
}

// extractVoiceText 从 V4 主消息表的 packed_info_data BLOB 中提取微信客户端
// 已生成的「语音转文字」结果。
//
// 实测 protobuf 结构（仅 local_type=34 的语音消息出现 field 5）：
//
//	message PackedInfo {
//	  uint32          type    = 1;  // 上层已声明
//	  uint32          version = 2;  // 上层已声明
//	  ImageHash       image   = 3;
//	  VideoHash       video   = 4;
//	  VoiceTextWrap   voiceTextWrap = 5;   // ★ 仅语音消息出现
//	  uint32          field11 = 11;
//	}
//	message VoiceTextWrap {
//	  uint32 field1 = 1;   // 实测恒为 2，疑似 source（云端 ASR）
//	  string text   = 2;   // ★ UTF-8 转文字结果
//	}
//
// 这里走轻量 wire-level 扫描，避免动 protoc 生成的 PackedInfo / 引入新 proto。
// 返回空字符串表示该消息没有转文字数据。
func extractVoiceText(b []byte) string {
	wrap := findProtoSubField(b, 5)
	if len(wrap) == 0 {
		return ""
	}
	text := findProtoStringField(wrap, 2)
	return text
}

// findProtoSubField 在 protobuf wire 字节流中找 tag = fieldNum、wire type = 2
// (length-delimited) 的字段，返回其内容（length-delimited 的子字节流）。
// 若同 tag 出现多次，取最后一次。其他不关心的字段会被正确跳过。
func findProtoSubField(buf []byte, fieldNum uint64) []byte {
	var result []byte
	i := 0
	for i < len(buf) {
		tag, n := readVarint(buf[i:])
		if n <= 0 {
			return result
		}
		i += n
		field := tag >> 3
		wire := tag & 0x7
		switch wire {
		case 0: // varint
			_, n := readVarint(buf[i:])
			if n <= 0 {
				return result
			}
			i += n
		case 1: // 64-bit
			i += 8
		case 2: // length-delimited
			length, n := readVarint(buf[i:])
			if n <= 0 {
				return result
			}
			i += n
			if i+int(length) > len(buf) {
				return result
			}
			if field == fieldNum {
				result = buf[i : i+int(length)]
			}
			i += int(length)
		case 5: // 32-bit
			i += 4
		default:
			return result
		}
	}
	return result
}

// findProtoStringField 在 protobuf wire 字节流中找 tag = fieldNum、wire type = 2
// 的 UTF-8 字符串字段。
func findProtoStringField(buf []byte, fieldNum uint64) string {
	v := findProtoSubField(buf, fieldNum)
	if len(v) == 0 {
		return ""
	}
	return string(v)
}

// readVarint 解析 protobuf 变长整型，返回值与读取字节数；解析失败返回 (0, 0)。
func readVarint(buf []byte) (uint64, int) {
	var v uint64
	var shift uint
	for i := 0; i < len(buf) && i < 10; i++ {
		b := buf[i]
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, i + 1
		}
		shift += 7
	}
	return 0, 0
}
