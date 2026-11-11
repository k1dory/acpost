//go:build unix

package plat

import "os"

func openToAll(dir string) error { return os.Chmod(dir, 0o755) }
