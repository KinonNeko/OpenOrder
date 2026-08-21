// Package web embeds the reference client (PROTOCOL §5). It is a development
// tool that exercises the public API — the real client lands in M1.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed index.html
var content embed.FS

func Handler() http.Handler {
	sub, _ := fs.Sub(content, ".")
	return http.FileServerFS(sub)
}
