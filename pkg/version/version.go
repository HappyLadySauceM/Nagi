// Package version provides build version information.
// Package version 提供构建版本信息。
package version

import (
	"fmt"
	"runtime"
)

// Build 信息可由构建流程通过 ldflags 注入，默认为开发态取值。
var (
	Version   = "0.1.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// String 返回单行版本描述。
func String() string {
	return fmt.Sprintf("nagi %s (commit %s, built %s, %s)", Version, Commit, BuildDate, runtime.Version())
}
