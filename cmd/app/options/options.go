// Package options declares command-line flags and their help texts.
// Package options 声明命令行 Flags 与帮助文本。
package options

import "flag"

// Options holds flag values; empty means "fall back to config/env/defaults".
// Options 持有 Flag 取值；空值表示回退到配置文件、环境变量与默认值。
type Options struct {
	ConfigFile string
	Listen     string
	LogLevel   string
}

// Register registers application flags onto fs.
// Register 将应用 Flags 注册到 fs。
func Register(fs *flag.FlagSet) *Options {
	o := &Options{}
	fs.StringVar(&o.ConfigFile, "config", "",
		"config file path (defaults to configs/config.yaml, missing default file is tolerated)")
	fs.StringVar(&o.Listen, "listen", "", "override server.listen")
	fs.StringVar(&o.LogLevel, "log-level", "", "override log.level")
	return o
}
