//go:build darwin

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/k1dory/acpost/desktop/preview"
)

// The IDE: VS Code's own web server (code serve-web, the VS Code installed on this Mac) on
// 127.0.0.1, behind a token of this run, shown in a native pane of the window. Extensions install
// as in VS Code; its server and data stay where VS Code keeps them.

type ideServer struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	base string // http://127.0.0.1:PORT/?tkn=…
}

var ide ideServer

var ideURL = regexp.MustCompile(`https?://(?:127\.0\.0\.1|localhost):\d+/?\S*`)

// vscodeCLI finds VS Code's command line: the app's code-tunnel (serve-web), or code on PATH.
func vscodeCLI() (string, error) {
	home, _ := os.UserHomeDir()
	for _, app := range []string{"/Applications/Visual Studio Code.app", filepath.Join(home, "Applications/Visual Studio Code.app"),
		"/Applications/Visual Studio Code - Insiders.app"} {
		for _, bin := range []string{"code-tunnel", "code-tunnel-insiders"} {
			p := filepath.Join(app, "Contents/Resources/app/bin", bin)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
	}
	if p, err := exec.LookPath("code"); err == nil {
		return p, nil
	}
	return "", errors.New("VS Code is not installed (Visual Studio Code.app in Applications)")
}

// IDEStart starts VS Code's server once (the first start fetches the server: a minute or so) and
// gives the address to open folder in.
func (n *Node) IDEStart(folder string) (string, error) {
	ide.mu.Lock()
	defer ide.mu.Unlock()
	if ide.base == "" || ide.cmd == nil || ide.cmd.ProcessState != nil {
		if err := ide.start(); err != nil {
			return "", err
		}
	}
	return ide.open(folder), nil
}

func (s *ideServer) start() error {
	cli, err := vscodeCLI()
	if err != nil {
		return err
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	tokFile := filepath.Join(os.TempDir(), "acpost-vscode-token")
	if err := os.WriteFile(tokFile, []byte(token), 0o600); err != nil {
		return err
	}
	args := []string{"serve-web", "--host", "127.0.0.1", "--port", "0", "--connection-token-file", tokFile,
		"--accept-server-license-terms", "--disable-telemetry"}
	if filepath.Base(cli) == "code" {
		args = append([]string{}, args...) // `code serve-web` takes the same
	}
	cmd := exec.Command(cli, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		sent := false
		for sc.Scan() {
			if m := ideURL.FindString(sc.Text()); m != "" && !sent {
				sent = true
				found <- m
			}
		}
		_ = cmd.Wait()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute) // the first start downloads the server
	defer cancel()
	select {
	case u := <-found:
		pu, err := url.Parse(u)
		if err != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			return err
		}
		q := pu.Query()
		q.Set("tkn", token)
		s.base = pu.Scheme + "://" + pu.Host + "/?" + q.Encode()
		s.cmd = cmd
		return nil
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		return errors.New("VS Code's server did not start in 3 minutes")
	}
}

func (s *ideServer) open(folder string) string {
	u, _ := url.Parse(s.base)
	q := u.Query()
	if folder != "" && filepath.IsAbs(folder) {
		q.Set("folder", folder)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// stop ends VS Code's server with the app.
func (s *ideServer) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)
	}
}

// IDEShow lays the IDE over the box at x, y, w × h (window points) and opens url in it when given.
func (n *Node) IDEShow(url string, x, y, w, h float64) error {
	if n.win == nil {
		return errPreview
	}
	if err := preview.IDEShow(n.win.NativeWindow(), x, y, w, h); err != nil {
		return err
	}
	if strings.TrimSpace(url) != "" {
		return preview.IDELoad(url)
	}
	return nil
}

// IDEHide hides it; VS Code keeps running.
func (n *Node) IDEHide() { preview.IDEHide() }

// IDEOpenApp opens folder in the VS Code app itself.
func (n *Node) IDEOpenApp(folder string) error {
	if !filepath.IsAbs(folder) {
		return errors.New("an absolute folder")
	}
	return exec.Command("/usr/bin/open", "-a", "Visual Studio Code", "--", folder).Run()
}
