package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

var (
	Version = "(dev)"
	// BuildTime 在编译时通过 -ldflags "-X 'github.com/afumu/wetrace/pkg/version.BuildTime=...'" 注入。
	// 推荐格式：本地时区 ISO 8601，例如 "2026-05-09T09:55:12+08:00"。
	// 未注入时为空字符串，前端可据此降级显示。
	BuildTime = ""
	buildInfo = debug.BuildInfo{}
)

func init() {
	if bi, ok := debug.ReadBuildInfo(); ok {
		buildInfo = *bi
		if len(bi.Main.Version) > 0 {
			Version = bi.Main.Version
		}
	}
}

func GetMore(mod bool) string {
	if mod {
		mod := buildInfo.String()
		if len(mod) > 0 {
			return fmt.Sprintf("\t%s\n", strings.ReplaceAll(mod[:len(mod)-1], "\n", "\n\t"))
		}
	}
	return fmt.Sprintf("version %s %s %s/%s\n", Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
