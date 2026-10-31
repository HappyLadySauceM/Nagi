package migrations

import (
	"embed"
	"io/fs"
)

//go:embed nagi/*.sql
var files embed.FS
var FS fs.FS

func init() {
	var err error
	FS, err = fs.Sub(files, "nagi")
	if err != nil {
		panic(err)
	}
}
