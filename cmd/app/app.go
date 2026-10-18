// Command qqbot runs the QQ bot built on OneBot v11 over forward WebSocket.
// Command qqbot 运行基于 OneBot v11 正向 WebSocket 的 QQ 机器人。
package app

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/HappyLadySauceM/QQBot/cmd/app/options"
	"github.com/HappyLadySauceM/QQBot/pkg/version"
)

// RootCommand parses process arguments and runs the application.
// RootCommand 解析进程参数并运行应用。
type RootCommand struct {
	name string
}

// NewRootCommand builds the CLI without relying on process-wide mutable state.
// NewRootCommand 构建 CLI，不依赖进程级可变状态。
func NewRootCommand() *RootCommand {
	return &RootCommand{name: "qqbot"}
}

// ExecuteContext parses flags and runs; -version prints the version and returns.
// ExecuteContext 解析 Flags 并运行；-version 打印版本后返回。
func (c *RootCommand) ExecuteContext(ctx context.Context) error {
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	fs.Usage = func() {
		out := fs.Output()
		_, _ = fmt.Fprintf(out, "qqbot — OneBot v11 forward WebSocket QQ bot\n")
		_, _ = fmt.Fprintf(out, "qqbot — 基于 OneBot v11 正向 WebSocket 的 QQ 机器人\n\n")
		_, _ = fmt.Fprintf(out, "usage:\n  %s [flags]\n\nflags:\n", c.name)
		fs.PrintDefaults()
	}
	opts := options.Register(fs)
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version.String())
		return nil
	}
	return Run(ctx, *opts)
}
