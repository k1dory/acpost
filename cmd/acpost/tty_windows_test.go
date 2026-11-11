package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestVTMode(t *testing.T) {
	const all = windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN
	for _, mode := range []uint32{0, windows.ENABLE_PROCESSED_OUTPUT, windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_WRAP_AT_EOL_OUTPUT, all} {
		got := vtMode(mode)
		if got&all != all || got&^all != mode&^all {
			t.Errorf("vtMode(%#x) = %#x", mode, got)
		}
	}
	if vtMode(all) != all {
		t.Error("a console that is a terminal already is changed")
	}
}
