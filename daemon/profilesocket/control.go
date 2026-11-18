package profilesocket

import "path/filepath"

const ControlSocketName = "anyscaled.sock"

func ControlPath(dir string) string {
	return filepath.Join(dir, ControlSocketName)
}
