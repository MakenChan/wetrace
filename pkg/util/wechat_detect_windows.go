//go:build windows

package util

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// FindWeChatInstallPaths 查找微信安装路径
func FindWeChatInstallPaths() []string {
	paths := make(map[string]struct{})

	// Helper to add valid paths
	addIfValid := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}

		exes := []string{"WeChat.exe", "Weixin.exe"}

		// If path points to an exe
		base := filepath.Base(p)
		for _, exe := range exes {
			if strings.EqualFold(base, exe) {
				if _, err := os.Stat(p); err == nil {
					paths[p] = struct{}{}
					return
				}
			}
		}

		// Check for exes inside the dir
		for _, exe := range exes {
			exePath := filepath.Join(p, exe)
			if _, err := os.Stat(exePath); err == nil {
				paths[exePath] = struct{}{}
				return
			}
		}
	}

	// 1. Check HKLM (32-bit node)
	keys := []string{
		`SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\WeChat`,
		`SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\Weixin`,
	}
	for _, keyPath := range keys {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.QUERY_VALUE)
		if err == nil {
			if val, _, err := k.GetStringValue("InstallLocation"); err == nil {
				addIfValid(val)
			}
			k.Close()
		}
	}

	// 2. Check HKCU
	hkcuKeys := []string{
		`Software\Tencent\WeChat`,
		`Software\Tencent\Weixin`,
	}
	for _, keyPath := range hkcuKeys {
		k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
		if err == nil {
			if val, _, err := k.GetStringValue("InstallPath"); err == nil {
				addIfValid(val)
			}
			k.Close()
		}
	}

	// 3. Common Default Path
	addIfValid(`C:\Program Files (x86)\Tencent\WeChat`)
	addIfValid(`C:\Program Files\Tencent\WeChat`)

	result := make([]string, 0, len(paths))
	for p := range paths {
		result = append(result, p)
	}
	return result
}

// looksLikeAccountDir 判断一个目录是否像微信账号目录。
// 判定依据：目录内存在 db_storage / msg / FileStorage 三个典型子目录之一。
// 这样不依赖目录名前缀，能兼容自定义微信号命名。
func looksLikeAccountDir(dir string) bool {
	markers := []string{"db_storage", "msg", "FileStorage"}
	for _, m := range markers {
		if info, err := os.Stat(filepath.Join(dir, m)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// FindWeChatDataPaths 查找微信数据存储路径
func FindWeChatDataPaths() []string {
	basePaths := make(map[string]struct{})

	// Helper to add base xwechat_files paths
	addBaseIfValid := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}

		// Case 1: Path is the xwechat_files folder itself
		if strings.EqualFold(filepath.Base(p), "xwechat_files") {
			if _, err := os.Stat(p); err == nil {
				basePaths[p] = struct{}{}
			}
			return
		}

		// Case 2: Path contains xwechat_files
		sub := filepath.Join(p, "xwechat_files")
		if _, err := os.Stat(sub); err == nil {
			basePaths[sub] = struct{}{}
		}
	}

	// 1. Check HKCU FileSavePath
	for _, keyPath := range []string{`Software\Tencent\WeChat`, `Software\Tencent\Weixin`} {
		k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
		if err == nil {
			if val, _, err := k.GetStringValue("FileSavePath"); err == nil {
				if val != "MyDocument:" {
					addBaseIfValid(val)
				}
			}
			k.Close()
		}
	}

	// 2. Default Documents Folder
	home, err := os.UserHomeDir()
	if err == nil {
		addBaseIfValid(filepath.Join(home, "Documents"))
		addBaseIfValid(filepath.Join(home, "OneDrive", "Documents"))
	}

	// 3. Scan for account subdirectories
	// 不再要求必须是 wxid_ 前缀：只要子目录里存在 db_storage / msg / FileStorage
	// 中的任意一个，就视为账号目录。这样可以兼容自定义微信号命名。
	finalPaths := make(map[string]struct{})
	for base := range basePaths {
		entries, err := os.ReadDir(base)
		if err != nil {
			finalPaths[base] = struct{}{}
			continue
		}

		foundAccount := false
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			// 跳过已知的非账号目录
			lower := strings.ToLower(name)
			if lower == "all_users" ||
				strings.HasPrefix(lower, "applet") ||
				strings.HasPrefix(lower, "backup") ||
				strings.HasPrefix(lower, "wmpf") {
				continue
			}
			sub := filepath.Join(base, name)
			if looksLikeAccountDir(sub) {
				finalPaths[sub] = struct{}{}
				foundAccount = true
			}
		}

		// If no account folders were found in this base, keep the base itself
		if !foundAccount {
			finalPaths[base] = struct{}{}
		}
	}

	result := make([]string, 0, len(finalPaths))
	for p := range finalPaths {
		result = append(result, p)
	}
	return result
}
