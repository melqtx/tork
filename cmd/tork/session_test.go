package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/session"
	"github.com/melqtx/tork/internal/state"
	"gopkg.in/yaml.v3"
)

// Launch the real persistent engine/event loop in a separate process. The
// parent test only acts as a terminal client and a local, gated HTTP source.
func TestSessionWorkerProcess(t *testing.T) {
	if os.Getenv("TORK_SESSION_TEST_WORKER") != "1" {
		return
	}
	if err := runSessionDaemon([]string{"--config-dir", os.Getenv("TORK_SESSION_TEST_DIR")}); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, timeout time.Duration, check func() bool, failure string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(failure)
}

func TestDownloadContinuesWithUIClosedAndSurvivesWorkerRestart(t *testing.T) {
	if !session.Supported {
		t.Skip("persistent sessions require Unix sockets")
	}
	dir, err := os.MkdirTemp("/tmp", "tork-worker-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	payload := bytes.Repeat([]byte("a transfer that outlives its terminal\n"), 32768)
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	gate := make(chan struct{})
	var release sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		if r.Method == http.MethodHead {
			return
		}
		if _, err := w.Write(payload[:65536]); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write(payload[65536:])
	}))
	t.Cleanup(func() { release.Do(func() { close(gate) }); srv.Close() })
	cfg := config.Default(dir)
	cfg.DownloadDir = filepath.Join(dir, "downloads")
	cfg.Health.Enabled = false
	cfg.Direct.EnableChunking = false
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ConfigPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	name := "payload.bin"
	st := &state.State{Entries: []state.Entry{{Magnet: srv.URL + "/payload.bin", Name: name, DownloadDir: cfg.DownloadDir, DataPath: filepath.Join(cfg.DownloadDir, name), SHA256: digest, ExpectedSize: int64(len(payload)), AddedAt: time.Now().UTC(), Seed: state.Bool(false)}}}
	if err := st.Save(cfg.StatePath()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TORK_SESSION_TEST_WORKER", "1")
	t.Setenv("TORK_SESSION_TEST_DIR", dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := func() {
		t.Helper()
		if err := session.Start(exe, dir, []string{"-test.run=^TestSessionWorkerProcess$"}); err != nil {
			t.Fatal(err)
		}
		eventually(t, 10*time.Second, func() bool { _, err := session.Control(dir, "status"); return err == nil }, "worker never became ready")
	}
	stop := func() {
		_, _ = session.Control(dir, "stop")
		eventually(t, 10*time.Second, func() bool { _, err := os.Lstat(filepath.Join(dir, "session", "ui.sock")); return os.IsNotExist(err) }, "worker failed to release engine ownership")
	}
	t.Cleanup(stop)
	start()
	path := filepath.Join(cfg.DownloadDir, name)
	eventually(t, 5*time.Second, func() bool { fi, err := os.Stat(path + ".part"); return err == nil && fi.Size() >= 65536 }, "transfer never started")
	launch := session.Launch{Width: 100, Height: 30, ColorProfile: 3, Dark: true}
	c, err := session.Attach(dir, launch)
	if err != nil {
		t.Fatal(err)
	}
	if c.Initial().PID == os.Getpid() {
		t.Fatal("worker shares the terminal process")
	}
	if strings.Contains(c.Initial().View, "tork  ·  home") {
		t.Fatal("home still has duplicate header")
	}
	if err := c.SendKey(tea.KeyMsg{Type: tea.KeyCtrlC}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("UI quit failed")
		}
		f, err := c.Read()
		if err != nil {
			t.Fatal(err)
		}
		if f.Quit {
			break
		}
	}
	c.Close()
	status, err := session.Control(dir, "status")
	if err != nil || !strings.Contains(status.Info, "1 active") || !strings.Contains(status.Info, "UI closed") {
		t.Fatalf("worker stopped with UI: %+v %v", status, err)
	}
	release.Do(func() { close(gate) })
	eventually(t, 5*time.Second, func() bool { data, err := os.ReadFile(path); return err == nil && bytes.Equal(data, payload) }, "download did not finish without a UI")
	c, err = session.Attach(dir, launch)
	if err != nil {
		t.Fatal(err)
	}
	if c.Initial().PID != status.PID {
		t.Fatal("reattach restarted the engine")
	}
	c.Close() // abrupt terminal closure also leaves the worker alive
	stop()
	saved, err := state.Load(cfg.StatePath())
	if err != nil || len(saved.Entries) != 1 || !saved.Entries[0].Done {
		t.Fatalf("completed download not persisted: %+v %v", saved, err)
	}
	start()
	c, err = session.Attach(dir, launch)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !strings.Contains(c.Initial().View, "saved download") {
		t.Fatal("worker restart lost saved downloads")
	}
	if data, err := os.ReadFile(path); err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
		t.Fatal("worker restart changed the payload")
	}
}
