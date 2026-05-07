package dllloader

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

type DllLoader struct {
	dll              *syscall.LazyDLL
	initializeHook   *syscall.LazyProc
	pollKeyData      *syscall.LazyProc
	getStatusMessage *syscall.LazyProc
	cleanupHook      *syscall.LazyProc
	getLastErrorMsg  *syscall.LazyProc
}

func NewDllLoader() *DllLoader {
	return &DllLoader{}
}

func (d *DllLoader) Load(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	d.dll = syscall.NewLazyDLL(absPath)

	// Pre-load procs to check for errors immediately (optional but good)
	d.initializeHook = d.dll.NewProc("InitializeHook")
	d.pollKeyData = d.dll.NewProc("PollKeyData")
	d.getStatusMessage = d.dll.NewProc("GetStatusMessage")
	d.cleanupHook = d.dll.NewProc("CleanupHook")
	d.getLastErrorMsg = d.dll.NewProc("GetLastErrorMsg")

	// Trigger a load to ensure DLL exists and is loadable
	err = d.dll.Load()
	if err != nil {
		return fmt.Errorf("failed to load DLL: %v", err)
	}

	return nil
}

func (d *DllLoader) InitializeHook(targetPid uint32) error {
	if d.initializeHook == nil {
		return fmt.Errorf("DLL not initialized")
	}

	ret, _, _ := d.initializeHook.Call(uintptr(targetPid))
	if ret == 0 {
		return fmt.Errorf("InitializeHook failed: %s", d.GetLastErrorMsg())
	}
	return nil
}

func (d *DllLoader) PollKeyData() (string, bool) {
	if d.pollKeyData == nil {
		return "", false
	}

	buffer := make([]byte, 65)
	ret, _, _ := d.pollKeyData.Call(
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
	)

	if ret != 0 {
		// Convert null-terminated byte slice to string
		n := 0
		for n < len(buffer) && buffer[n] != 0 {
			n++
		}
		// PollKeyData 返回的是 hex 字符串（ASCII），但保险起见仍走 ANSI 转换
		return ansiBytesToUTF8(buffer[:n]), true
	}

	return "", false
}

func (d *DllLoader) GetStatusMessage() (string, int, bool) {
	if d.getStatusMessage == nil {
		return "", 0, false
	}

	buffer := make([]byte, 256)
	var level int32

	ret, _, _ := d.getStatusMessage.Call(
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&level)),
	)

	if ret != 0 {
		n := 0
		for n < len(buffer) && buffer[n] != 0 {
			n++
		}
		// C++ DLL 返回的消息使用系统 ANSI 代码页（简中系统为 GBK），需要转换为 UTF-8
		return ansiBytesToUTF8(buffer[:n]), int(level), true
	}

	return "", 0, false
}

func (d *DllLoader) CleanupHook() error {
	if d.cleanupHook == nil {
		return fmt.Errorf("DLL not initialized")
	}
	ret, _, _ := d.cleanupHook.Call()
	if ret == 0 {
		return fmt.Errorf("CleanupHook failed")
	}
	return nil
}

func (d *DllLoader) GetLastErrorMsg() string {
	if d.getLastErrorMsg == nil {
		return "DLL not initialized"
	}
	ret, _, _ := d.getLastErrorMsg.Call()
	if ret == 0 {
		return "Unknown error"
	}

	// 从 C 返回的 char* 读取 null-terminated 字节流
	ptr := unsafe.Pointer(ret)
	var raw []byte
	for {
		b := *(*byte)(ptr)
		if b == 0 {
			break
		}
		raw = append(raw, b)
		ptr = unsafe.Pointer(uintptr(ptr) + 1)
	}
	// C++ DLL 返回的消息使用系统 ANSI 代码页（简中系统为 GBK），需要转换为 UTF-8
	return ansiBytesToUTF8(raw)
}
