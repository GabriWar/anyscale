package anyscale

import (
	"embed"
	"io/fs"
)

//go:embed LICENSE data/*.metainfo.xml po
var assetsFS embed.FS

func Assets() fs.FS {
	return assetsFS
}
