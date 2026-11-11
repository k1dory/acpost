package acptest

import "path/filepath"

// HomeEnv is the environment that makes dir a process's home on every system: HOME, and for
// Windows the profile and its two application-data directories, laid out as on Unix
// (dir/.config, dir/.local/state), so that a test finds the same files under dir everywhere.
func HomeEnv(dir string) []string {
	return []string{
		"HOME=" + dir,
		"USERPROFILE=" + dir,
		"APPDATA=" + filepath.Join(dir, ".config"),
		"LOCALAPPDATA=" + filepath.Join(dir, ".local", "state"),
	}
}

// SetHome makes dir this process's home (setenv is a test's t.Setenv).
func SetHome(setenv func(key, value string), dir string) {
	for _, kv := range HomeEnv(dir) {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				setenv(kv[:i], kv[i+1:])
				break
			}
		}
	}
}
