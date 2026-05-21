//go:build windows

package dllloader

import (
	"syscall"
	"unicode/utf8"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procMultiByteToWideCh = kernel32.NewProc("MultiByteToWideChar")
)

const cpACP = 0 // CP_ACP：系统当前 ANSI 代码页（简中系统 = 936 / GBK）

// ansiBytesToUTF8 将 C++ DLL 返回的 ANSI（系统代码页）字节流转换为合法的 UTF-8 字符串。
// 如果输入本身已经是合法 UTF-8（纯 ASCII 也算），则直接返回，避免误转换。
func ansiBytesToUTF8(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	// 已经是合法 UTF-8（纯 ASCII / 原本就是 UTF-8）时无需转换
	if utf8.Valid(b) {
		return string(b)
	}

	// 第一次调用：查询需要的 UTF-16 字符数
	n, _, _ := procMultiByteToWideCh.Call(
		uintptr(cpACP),
		0,
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(int32(len(b))),
		0,
		0,
	)
	if n == 0 {
		// 转换失败，退回原样（避免丢数据）
		return string(b)
	}

	buf := make([]uint16, n)
	ret, _, _ := procMultiByteToWideCh.Call(
		uintptr(cpACP),
		0,
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(int32(len(b))),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(int32(n)),
	)
	if ret == 0 {
		return string(b)
	}

	return syscall.UTF16ToString(buf)
}
