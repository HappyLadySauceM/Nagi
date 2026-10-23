// Package migrations embeds the versioned SQL schema files.
// migrations 包内嵌版本化 SQL 模式文件。
package migrations

import "embed"

// FS holds all *.sql migration files in this directory.
// FS 持有本目录下全部 *.sql 迁移文件。
//
//go:embed *.sql
var FS embed.FS
