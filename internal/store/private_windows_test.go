package store

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/k1dory/acpost/internal/plat"
)

// A database that was open to everyone in a private directory (there before, or moved in) is
// closed when the store opens it: a path that is known is opened past a closed directory, so the
// file's own access list is what keeps others out.
func TestExistingDatabaseOpenToOthersIsClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := plat.MkdirPrivate(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "store.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	if plat.PrivateDir(path) == nil {
		t.Fatal("the file is not open to everyone: nothing is tested")
	}
	s, err := Open("sqlite://"+path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm", path + ".lock"} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := plat.PrivateDir(p); err != nil {
			t.Errorf("%s remains open to other users: %v", p, err)
		}
	}
}
