package main

import (
	"os"
	"os/exec"
	"path/filepath"

	acpnode "github.com/k1dory/acpost/internal/node"
)

// nameCLI tells the app's node which acpost command its agents run. The app's own binary is a
// window, so the bundle carries the command (Contents/Helpers/acpost, build-app.sh): an agent told
// to `acpost send` finds it on a Mac where acpost was never installed. Outside a bundle (a
// development run), the acpost on PATH, if any.
func nameCLI() {
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		for _, name := range []string{"acpost", "acpost.exe"} {
			p := filepath.Join(filepath.Dir(exe), "..", "Helpers", name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				acpnode.CLI = filepath.Clean(p)
				return
			}
		}
	}
	if p, err := exec.LookPath("acpost"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			acpnode.CLI = abs
		}
	}
}
