package dat2img

import (
	"bytes"
	"crypto/aes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Format defines the header and extension for different image types
type Format struct {
	Header []byte
	AesKey []byte
	Ext    string
}

// ErrNotImage 表示数据本身解密成功，但内容不是已知的图片格式
// （例如 iPhone 实况照片的 .dat 解出来是 mp4/CMAF 视频）。
// 上层调用方可以据此跳过当前候选文件，继续 fallback 下一个变体。
var ErrNotImage = errors.New("decrypted content is not a recognized image format")

var (
	// Common image format definitions
	JPG     = Format{Header: []byte{0xFF, 0xD8, 0xFF}, Ext: "jpg"}
	PNG     = Format{Header: []byte{0x89, 0x50, 0x4E, 0x47}, Ext: "png"}
	GIF     = Format{Header: []byte{0x47, 0x49, 0x46, 0x38}, Ext: "gif"}
	TIFF    = Format{Header: []byte{0x49, 0x49, 0x2A, 0x00}, Ext: "tiff"}
	BMP     = Format{Header: []byte{0x42, 0x4D}, Ext: "bmp"}
	WEBP    = Format{Header: []byte{0x52, 0x49, 0x46, 0x46}, Ext: "webp"} // "RIFF"，需进一步校验 WEBP
	WXGF    = Format{Header: []byte{0x77, 0x78, 0x67, 0x66}, Ext: "wxgf"}
	Formats = []Format{JPG, PNG, GIF, TIFF, BMP, WXGF}

	// Updated V4 definitions to match Dart implementation (6 bytes signature)
	// V4 Type 1: 0x07 0x08 0x56 0x31 0x08 0x07
	V4Format1 = Format{Header: []byte{0x07, 0x08, 0x56, 0x31, 0x08, 0x07}, AesKey: []byte("cfcd208495d565ef")}
	// V4 Type 2: 0x07 0x08 0x56 0x32 0x08 0x07
	V4Format2 = Format{Header: []byte{0x07, 0x08, 0x56, 0x32, 0x08, 0x07}, AesKey: []byte("0000000000000000")} // User needs to provide key
	V4Formats = []*Format{&V4Format1, &V4Format2}

	// WeChat v4 related constants
	V4XorKey byte = 0x37               // Default XOR key for WeChat v4 dat files
	JpgTail       = []byte{0xFF, 0xD9} // JPG file tail marker
)

// Dat2Image converts WeChat dat file data to image data
func Dat2Image(data []byte) ([]byte, string, error) {
	if len(data) < 4 {
		return nil, "", fmt.Errorf("data length is too short: %d", len(data))
	}

	// Check if this is a WeChat v4 dat file (Check first 6 bytes)
	if len(data) >= 6 {
		for _, format := range V4Formats {
			if bytes.Equal(data[:6], format.Header) {
				return Dat2ImageV4(data, format.AesKey)
			}
		}
	}

	// For older WeChat versions (V3), use XOR decryption
	findFormat := func(data []byte, header []byte) bool {
		xorBit := data[0] ^ header[0]
		for i := 0; i < len(header); i++ {
			if data[i]^header[i] != xorBit {
				return false
			}
		}
		return true
	}

	var xorBit byte
	var found bool
	var ext string
	for _, format := range Formats {
		if found = findFormat(data, format.Header); found {
			xorBit = data[0] ^ format.Header[0]
			ext = format.Ext
			break
		}
	}

	if !found {
		// Fallback check: if no known header found, verify if it's a V4 file with only 4 bytes matching (loose check)
		// This handles cases where the file might be truncated or slightly different, but it's risky.
		// Original code checked 4 bytes, let's keep strict 6 bytes above, and if failed, maybe return error.
		return nil, "", fmt.Errorf("unknown image type: %x %x", data[0], data[1])
	}

	// Apply XOR decryption (V3)
	out := make([]byte, len(data))
	for i := range data {
		out[i] = data[i] ^ xorBit
	}

	return out, ext, nil
}

// calculateXorKeyV4 calculates the XOR key for WeChat v4 dat files
func calculateXorKeyV4(data []byte) (byte, error) {
	if len(data) < 2 {
		return 0, fmt.Errorf("data too short to calculate XOR key")
	}
	fileTail := data[len(data)-2:]
	xorKeys := make([]byte, 2)
	for i := 0; i < 2; i++ {
		xorKeys[i] = fileTail[i] ^ JpgTail[i]
	}
	if xorKeys[0] == xorKeys[1] {
		return xorKeys[0], nil
	}
	return xorKeys[0], fmt.Errorf("inconsistent XOR key, using first byte: 0x%x", xorKeys[0])
}

func SetV4XorKey(hexStr string) error {
	// 这里用标准库简单解析一下，确保字符串是 2 位 16 进制
	v, err := strconv.ParseUint(hexStr, 16, 8)
	if err != nil {
		return err
	}
	V4XorKey = byte(v)
	return nil
}

// ScanAndSetXorKey scans a directory to calculate and set the global XOR key
func ScanAndSetXorKey(dirPath string) (byte, error) {
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), "_t.dat") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		// Check header (looser check for scan is acceptable, or use exact)
		isV4 := false
		if len(data) >= 4 {
			if bytes.Equal(data[:4], V4Format1.Header[:4]) || bytes.Equal(data[:4], V4Format2.Header[:4]) {
				isV4 = true
			}
		}
		if !isV4 {
			return nil
		}

		if len(data) < 15 {
			return nil
		}

		xorEncryptLen := binary.LittleEndian.Uint32(data[10:14])
		fileData := data[15:]

		if xorEncryptLen == 0 || uint32(len(fileData)) <= uint32(len(fileData))-xorEncryptLen {
			return nil
		}

		xorData := fileData[uint32(len(fileData))-xorEncryptLen:]
		key, err := calculateXorKeyV4(xorData)
		if err != nil {
			return nil
		}

		V4XorKey = key
		fmt.Println("V4XorKey:", V4XorKey)
		return filepath.SkipAll
	})

	if err != nil && err != filepath.SkipAll {
		return V4XorKey, fmt.Errorf("error scanning directory: %v", err)
	}
	return V4XorKey, nil
}

func SetAesKey(key string) {
	if key == "" {
		return
	}
	// Dart implementation uses asciiKey16 (taking first 16 chars/bytes)
	// If the input is hex, decoding is fine, but if it's a raw string like "cfcd...",
	// we should handle it carefully. Assuming input is hex string here as per original Go.
	decoded, err := hex.DecodeString(key)
	if err != nil {
		// Fallback: if hex decode fails, use raw bytes if length is 16 (matching Dart logic partially)
		if len(key) == 16 {
			V4Format2.AesKey = []byte(key)
			return
		}
		log.Println("invalid aes key")
		return
	}
	V4Format2.AesKey = decoded
}

// Dat2ImageV4 processes WeChat v4 dat image files
// Refactored to match Dart implementation logic
func Dat2ImageV4(data []byte, aesKey []byte) ([]byte, string, error) {
	if len(data) < 15 {
		return nil, "", fmt.Errorf("data length is too short for WeChat v4 format")
	}

	// 1. Parse Headers (Little Endian)
	// Offset 6-10: AES Encryption Length
	aesSize := binary.LittleEndian.Uint32(data[6:10])
	// Offset 10-14: XOR Encryption Length
	xorSize := binary.LittleEndian.Uint32(data[10:14])

	// Skip header (15 bytes)
	fileData := data[15:]

	// 2. AES Decryption Logic
	// Calculate aligned size: size + (16 - size % 16)
	// This ensures we read the full PKCS7 padded block
	alignedAesSize := aesSize + (16 - (aesSize % 16))

	if uint32(len(fileData)) < alignedAesSize {
		return nil, "", fmt.Errorf("file data too short for declared AES length")
	}

	// Split data: [AES Part] [Middle Raw Part] [XOR Part]
	aesPart := fileData[:alignedAesSize]
	remainingPart := fileData[alignedAesSize:]

	var unpaddedAesData []byte
	var err error

	// Decrypt AES part
	if len(aesPart) > 0 {
		unpaddedAesData, err = decryptAESECBStrict(aesPart, aesKey)
		if err != nil {
			return nil, "", fmt.Errorf("AES decryption failed: %v", err)
		}
	}

	// 3. Handle Middle and XOR Parts
	// XOR size validation
	if uint32(len(remainingPart)) < xorSize {
		return nil, "", fmt.Errorf("file data too short for declared XOR length")
	}

	rawLen := uint32(len(remainingPart)) - xorSize
	rawMiddleData := remainingPart[:rawLen]
	xorTailData := remainingPart[rawLen:]

	// Decrypt XOR part
	decryptedXorData := make([]byte, len(xorTailData))
	for i := range xorTailData {
		decryptedXorData[i] = xorTailData[i] ^ V4XorKey
	}

	// 4. Reassemble: [Unpadded AES] + [Raw Middle] + [Decrypted XOR]
	// Pre-allocate exact size
	totalLen := len(unpaddedAesData) + len(rawMiddleData) + len(decryptedXorData)
	result := make([]byte, 0, totalLen)

	result = append(result, unpaddedAesData...)
	result = append(result, rawMiddleData...)
	result = append(result, decryptedXorData...)

	// Identify image type
	imgType := ""
	for _, format := range Formats {
		// Only check headers for image types, not V4 types
		if format.Ext == "wxgf" || format.Ext == "jpg" || format.Ext == "png" || format.Ext == "gif" || format.Ext == "tiff" || format.Ext == "bmp" {
			if len(result) >= len(format.Header) && bytes.Equal(result[:len(format.Header)], format.Header) {
				imgType = format.Ext
				break
			}
		}
	}

	if imgType == "wxgf" {
		return Wxam2pic(result)
	}

	if imgType == "" {
		// 解密成功但 magic 不在已知图片格式里，进一步识别 ISO BMFF 容器（HEIC/HEIF/AVIF/MP4 等）。
		// ISO BMFF 文件的前 4 字节是 box size（big-endian），紧接着是 4 字节 box type "ftyp"，
		// 然后从 offset 8 开始是 4 字节的 major brand。
		if isoBrand := detectISOBMFFBrand(result); isoBrand != "" {
			switch isoBrand {
			// 静态图片类 brand：HEIC（iPhone 默认）、HEIF、AVIF 及其变体
			case "heic", "heix", "heim", "heis", "hevc", "hevx",
				"mif1", "msf1", "heif",
				"avif", "avis":
				// Windows 浏览器（Chrome/Edge）原生不支持 HEIC，统一用 ffmpeg 转成 JPEG 再吐回前端。
				jpegData, convErr := Convert2JPG(result)
				if convErr != nil {
					return nil, "", fmt.Errorf("convert HEIC/AVIF (%s) to JPEG failed: %w", isoBrand, convErr)
				}
				return jpegData, "jpg", nil

			// 视频/动态影像类 brand：iPhone 实况照片（Live Photo）的视频部分常见为 cmfc/qt/mp4 系
			default:
				// 明确告知上层："解密是成功的，但这玩意儿是 mp4/cmaf 视频，不是图片"
				// 上层 fallback 应该跳过当前候选，继续试 _h.dat（实况照片的高清图）
				return nil, "", fmt.Errorf("%w: ISO BMFF brand=%s", ErrNotImage, isoBrand)
			}
		}

		// Fallback detection (check first bytes if header match failed)
		if len(result) > 2 {
			return nil, "", fmt.Errorf("%w: unknown header %x %x", ErrNotImage, result[0], result[1])
		}
		return nil, "", fmt.Errorf("%w: empty content", ErrNotImage)
	}

	return result, imgType, nil
}

// detectISOBMFFBrand 检查数据是否是 ISO Base Media File Format 容器（HEIC/HEIF/AVIF/MP4 等），
// 如果是则返回 4 字节的 major brand 字符串（小写），否则返回空字符串。
//
// ISO BMFF 文件结构（前 12 字节）：
//
//	[4 bytes big-endian box size][4 bytes "ftyp"][4 bytes major brand]
//
// 参考: ISO/IEC 14496-12
func detectISOBMFFBrand(data []byte) string {
	if len(data) < 12 {
		return ""
	}
	// offset 4-8 必须是 "ftyp"
	if !bytes.Equal(data[4:8], []byte{'f', 't', 'y', 'p'}) {
		return ""
	}
	// offset 8-12 是 major brand
	brand := strings.ToLower(strings.TrimSpace(string(data[8:12])))
	return brand
}

// decryptAESECBStrict decrypts data using AES in ECB mode and strictly removes PKCS7 padding
// This matches Dart's _strictRemovePadding logic
func decryptAESECBStrict(data, key []byte) ([]byte, error) {
	if len(data) == 0 {
		return []byte{}, nil
	}

	cipher, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("data length %d is not a multiple of block size", len(data))
	}

	decrypted := make([]byte, len(data))
	for bs, be := 0, aes.BlockSize; bs < len(data); bs, be = bs+aes.BlockSize, be+aes.BlockSize {
		cipher.Decrypt(decrypted[bs:be], data[bs:be])
	}

	// Strict PKCS7 Unpadding
	length := len(decrypted)
	if length == 0 {
		return nil, errors.New("decrypted data is empty")
	}

	paddingLen := int(decrypted[length-1])
	if paddingLen == 0 || paddingLen > aes.BlockSize || paddingLen > length {
		return nil, fmt.Errorf("invalid PKCS7 padding length: %d", paddingLen)
	}

	// Verify all padding bytes
	for i := length - paddingLen; i < length; i++ {
		if decrypted[i] != byte(paddingLen) {
			return nil, errors.New("invalid PKCS7 padding content")
		}
	}

	return decrypted[:length-paddingLen], nil
}
