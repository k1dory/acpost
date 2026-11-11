package keychain

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/k1dory/acpost/internal/plat"
)

// Secrets kept in a file under a directory that was there, and hands everyone its access to what
// is made in it, are still this user's alone.
func TestSecretsFileIsPrivateInAnOpenDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	s := &Store{Service: "acpost-test", Dir: dir, File: true}
	if err := s.Set("a", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.Lookup("a"); err != nil || !ok || v != "s3cret" {
		t.Fatalf("%q %v %v", v, ok, err)
	}
	if err := plat.PrivateDir(s.file()); err != nil {
		t.Fatalf("the secrets file: %v", err)
	}
}
