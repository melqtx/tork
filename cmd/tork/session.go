package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/health"
	"github.com/melqtx/tork/internal/intake"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/session"
	"github.com/melqtx/tork/internal/state"
	"github.com/melqtx/tork/internal/tui"
)

func loadSessionConfig(dir string, readOnly bool) (*config.Config, error) {
	if dir == "" {
		if readOnly {
			return config.LoadReadOnly()
		}
		return config.Load()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if readOnly {
		return config.LoadReadOnlyFrom(abs)
	}
	return config.LoadFrom(abs)
}

func sessionUnavailable(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}

// Rebuilding a dev binary must not silently reconnect it to yesterday's UI.
// The running worker captures this identity once, before accepting terminals.
func sessionBuild() string {
	exe, err := os.Executable()
	if err != nil {
		return resolveVersion()
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return resolveVersion()
	}
	return resolveVersion() + ":" + strconv.FormatInt(fi.Size(), 10) + ":" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}

func ensureSession(cfg *config.Config, evil bool) error {
	if _, err := session.Control(cfg.Dir(), "status"); err == nil {
		return nil
	} else if !sessionUnavailable(err) {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"_session", "--config-dir", cfg.Dir(), "--download-dir", cfg.DownloadDir}
	if evil {
		args = append(args, "--evil")
	}
	if err := session.Start(exe, cfg.Dir(), args); err != nil {
		return err
	}
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := session.Control(cfg.Dir(), "status"); err == nil {
			return nil
		} else if !sessionUnavailable(err) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("could not start the background session; close any older foreground tork first and check %s", filepath.Join(cfg.Dir(), "session", "worker.log"))
}

func runSessionUI(cfg *config.Config, target *intake.Target, downloadOverride string, evil bool) error {
	if err := ensureSession(cfg, evil); err != nil {
		return err
	}
	w, h, _ := term.GetSize(os.Stdout.Fd())
	client, err := session.Attach(cfg.Dir(), session.Launch{
		Target: target, DownloadDir: downloadOverride, Evil: evil, Build: sessionBuild(),
		Width: w, Height: h, ColorProfile: int(lipgloss.ColorProfile()), Dark: lipgloss.HasDarkBackground(),
	})
	if err != nil {
		return err
	}
	info, err := client.Run(tea.WithAltScreen())
	if info != "" {
		fmt.Println(info)
	}
	return err
}

func runSessionDaemon(args []string) error {
	fs := flag.NewFlagSet("_session", flag.ContinueOnError)
	dir := fs.String("config-dir", "", "config directory")
	dlDir := fs.String("download-dir", "", "download directory")
	evil := fs.Bool("evil", false, "never seed new downloads")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || fs.NArg() > 0 {
		return errors.New("background session needs --config-dir")
	}
	cfg, err := loadSessionConfig(*dir, false)
	if err != nil {
		return err
	}
	ln, cleanup, err := session.Listen(cfg.Dir())
	if err != nil {
		return err
	}
	defer cleanup() // socket/lock remain owned until the engine has closed
	legacyDir := cfg.DownloadDir
	if *dlDir != "" {
		if err := cfg.OverrideDownloadDir(*dlDir); err != nil {
			return err
		}
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return err
	}
	defer eng.Close()
	st, err := state.Load(cfg.StatePath())
	if err != nil {
		return err
	}
	resumeAll(eng, st, cfg, legacyDir)
	if *evil {
		cfg.SeedAfterComplete = false
	}
	providers := enabledProviders(cfg)
	agg := aggregator.New(providers, cfg.SearchTimeout(), 2).WithFilter(provider.ContentFilter{HideNSFW: cfg.HideNSFW})
	store := health.Open(cfg.HealthPath())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stopHealth := maybeCheckHealth(ctx, cfg, store, providers, eng, healthWarmup)
	defer stopHealth()
	app := tui.New(cfg, eng, agg, st, store)
	build := sessionBuild()
	hooks := session.Hooks{
		Attach: func(launch session.Launch) (tea.Cmd, error) {
			if launch.Build != "" && launch.Build != build {
				return nil, errors.New("the background session uses an older build; run tork stop, then reopen tork")
			}
			if launch.DownloadDir != "" && filepath.Clean(launch.DownloadDir) != filepath.Clean(cfg.DownloadDir) {
				return nil, errors.New("the background session uses a different download directory; run tork stop before changing -d")
			}
			if launch.Evil && cfg.SeedAfterComplete {
				return nil, errors.New("run tork stop before starting a new --evil session")
			}
			return app.AttachSession(launch.Target)
		},
		Detach:     app.DetachSession,
		Checkpoint: app.CheckpointSession,
		Status:     app.SessionStatus,
	}
	return session.Serve(ctx, ln, app, hooks)
}

func runSessionControl(op string, args []string) error {
	fs := flag.NewFlagSet(op, flag.ContinueOnError)
	dir := fs.String("config-dir", "", "config directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: tork %s [--config-dir DIR]", op)
	}
	cfg, err := loadSessionConfig(*dir, true)
	if err != nil {
		return err
	}
	frame, err := session.Control(cfg.Dir(), op)
	if sessionUnavailable(err) {
		fmt.Println("No background session running.")
		return nil
	}
	if err != nil {
		return err
	}
	if op == "status" {
		fmt.Printf("Background session running (PID %d) · %s\n", frame.PID, frame.Info)
		return nil
	}
	// The listener can close before the engine releases its piece database.
	// The owner removes this socket only after that final shutdown finishes.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(filepath.Join(cfg.Dir(), "session", "ui.sock")); os.IsNotExist(err) {
			fmt.Println(frame.Info)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("session is still shutting down; wait before restarting")
}
