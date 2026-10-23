package web

import "embed"

// Files contains only the public UI assets; no local configuration or secrets.
//
//go:embed *.html *.css *.js
var Files embed.FS
