//go:build darwin

package main

import (
	"bufio"
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
	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{} // closed when the server process has ended
	ready   chan struct{} // closed when a start has finished (base or err set)
	base    string        // http://127.0.0.1:PORT/?tkn=…
	err     error
	tokFile string
	app     string // the VS Code app the CLI belongs to (to open folders in it)
	shown   string // the address the pane has open
}

var ide ideServer

var ideURL = regexp.MustCompile(`https?://(?:127\.0\.0\.1|localhost):\d+/?\S*`)

// vscodeCLI finds VS Code's command line (the app's code-tunnel, for serve-web) and its app; or
// code on PATH.
func vscodeCLI() (cli, app string, err error) {
	home, _ := os.UserHomeDir()
	for _, a := range []string{"/Applications/Visual Studio Code.app", filepath.Join(home, "Applications/Visual Studio Code.app"),
		"/Applications/Visual Studio Code - Insiders.app", filepath.Join(home, "Applications/Visual Studio Code - Insiders.app")} {
		for _, bin := range []string{"code-tunnel", "code-tunnel-insiders"} {
			p := filepath.Join(a, "Contents/Resources/app/bin", bin)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, a, nil
			}
		}
	}
	if p, err := exec.LookPath("code"); err == nil {
		return p, "Visual Studio Code", nil
	}
	return "", "", errors.New("VS Code is not installed (Visual Studio Code.app in Applications)")
}

// IDEStart gives the address to open folder in, starting VS Code's server once (the first start
// fetches it: a minute or two). Calls meanwhile wait for that start; a server that ended is
// started again.
func (n *Node) IDEStart(folder string) (string, error) {
	ide.mu.Lock()
	running := ide.done != nil && !closed(ide.done) && ide.base != ""
	if running {
		u := ide.open(folder)
		ide.mu.Unlock()
		return u, nil
	}
	ready := ide.ready
	if ready == nil || (closed(ready) && (ide.base == "" || closed(ide.done))) {
		ready = make(chan struct{})
		ide.ready, ide.base, ide.err = ready, "", nil
		go ide.start(ready)
	}
	ide.mu.Unlock()
	select {
	case <-ready:
	case <-time.After(4 * time.Minute):
		return "", errors.New("VS Code's server did not start in time")
	}
	ide.mu.Lock()
	defer ide.mu.Unlock()
	if ide.err != nil {
		return "", ide.err
	}
	return ide.open(folder), nil
}

func closed(c chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// start runs the server and waits — not holding the lock — for its address, its end, or 3 min.
func (s *ideServer) start(ready chan struct{}) {
	fail := func(err error) {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(ready)
	}
	cli, app, err := vscodeCLI()
	if err != nil {
		fail(err)
		return
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	// A token file of this server's own: created new (never another's, never a link), 0600, removed
	// when the server stops.
	f, err := os.CreateTemp("", "acpost-vscode-token-*")
	if err != nil {
		fail(err)
		return
	}
	_, werr := f.WriteString(token)
	f.Close()
	if werr != nil {
		os.Remove(f.Name())
		fail(werr)
		return
	}
	cmd := exec.Command(cli, "serve-web", "--host", "127.0.0.1", "--port", "0", "--connection-token-file", f.Name(),
		"--accept-server-license-terms", "--disable-telemetry")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		os.Remove(f.Name())
		fail(err)
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		os.Remove(f.Name())
		fail(err)
		return
	}
	done := make(chan struct{})
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if m := ideURL.FindString(sc.Text()); m != "" {
				select {
				case found <- m:
				default:
				}
			}
		}
		_ = cmd.Wait()
		close(done)
	}()
	s.mu.Lock()
	s.cmd, s.done, s.tokFile, s.app = cmd, done, f.Name(), app
	s.mu.Unlock()
	select {
	case u := <-found:
		pu, err := url.Parse(u)
		if err != nil {
			s.stop()
			fail(err)
			return
		}
		q := pu.Query()
		q.Set("tkn", token)
		s.mu.Lock()
		s.base = pu.Scheme + "://" + pu.Host + "/?" + q.Encode()
		s.mu.Unlock()
		close(ready)
	case <-done:
		os.Remove(f.Name())
		fail(errors.New("VS Code's server stopped as it started"))
	case <-time.After(3 * time.Minute):
		s.stop()
		fail(errors.New("VS Code's server did not start in 3 minutes"))
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

// stop ends VS Code's server (with the app) and removes its token file.
func (s *ideServer) stop() {
	s.mu.Lock()
	cmd, tok := s.cmd, s.tokFile
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	if tok != "" {
		_ = os.Remove(tok)
	}
}

// IDEShow lays the IDE over the box at x, y, w × h (window points) and opens url in it when given.
func (n *Node) IDEShow(url string, x, y, w, h float64) error {
	if n.win == nil {
		return errPreview
	}
	// Focus goes into VS Code when it is opened (a url), not each time the pane is placed.
	if err := preview.IDEShow(n.win.NativeWindow(), x, y, w, h, strings.TrimSpace(url) != "" && strings.TrimSpace(url) != ide.loaded()); err != nil {
		return err
	}
	// The same address again (the IDE shown once more): VS Code stays as it is, not reloaded.
	if u := strings.TrimSpace(url); u != "" && u != ide.loaded() {
		if err := preview.IDELoad(u); err != nil {
			return err
		}
		ide.mu.Lock()
		ide.shown = u
		ide.mu.Unlock()
	}
	return nil
}

func (s *ideServer) loaded() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shown
}

// IDEHide hides it; VS Code keeps running.
func (n *Node) IDEHide() { preview.IDEHide() }

// IDEOpenApp opens folder in the VS Code app itself.
func (n *Node) IDEOpenApp(folder string) error {
	if !filepath.IsAbs(folder) {
		return errors.New("an absolute folder")
	}
	ide.mu.Lock()
	app := ide.app
	ide.mu.Unlock()
	if app == "" {
		if _, a, err := vscodeCLI(); err == nil {
			app = a
		}
	}
	return exec.Command("/usr/bin/open", "-a", cmpOr(app, "Visual Studio Code"), "--", folder).Run()
}
