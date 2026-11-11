package node

import "errors"

// mkfifo: Windows has no FIFO in a directory.
func mkfifo(string) error { return errors.ErrUnsupported }
