//go:build !windows

package dllloader

// 非 Windows 平台下 DLL 相关功能不可用，这里只是为了让 package 在其他平台也能构建。
// 如果调用方在非 Windows 上真正调用了这些函数，会在运行时失败（dll.Load 返回错误），
// 与既有行为保持一致。
func ansiBytesToUTF8(b []byte) string {
	return string(b)
}
