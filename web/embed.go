package web

import "embed"

//go:embed locales templates static
var FS embed.FS
