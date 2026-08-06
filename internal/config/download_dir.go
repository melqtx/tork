package config

import (
	"os"
	"path/filepath"
	"strings"
)

// defaultDownloadDir resolves the OS Downloads folder and keeps tork's files
// in a dedicated subdirectory. On Linux the standard user-dirs.dirs entry has
// precedence, followed by the historical XDG_DOWNLOAD_DIR environment
// override on every platform, then ~/Downloads.
func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "downloads"
	}
	return resolveDefaultDownloadDir(home, os.Getenv("XDG_CONFIG_HOME"), os.Getenv("XDG_DOWNLOAD_DIR"))
}

func resolveDefaultDownloadDir(home, configHome, envDownload string) string {
	home = filepath.Clean(strings.TrimSpace(home))
	if home == "." || !filepath.IsAbs(home) {
		return "downloads"
	}
	if configured, ok := configuredDownloadDir(home, configHome); ok {
		return filepath.Join(configured, "tork")
	}
	if configured, ok := normalizeDownloadDir(envDownload, home, true); ok {
		return filepath.Join(configured, "tork")
	}
	return filepath.Join(home, "Downloads", "tork")
}

// normalizeDownloadDir expands only explicit leading home forms. It does not
// perform general environment or shell expansion.
func normalizeDownloadDir(raw, home string, allowTilde bool) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsRune(raw, 0) || strings.ContainsAny(raw, "\r\n`\";|&<>") {
		return "", false
	}

	switch {
	case raw == "$HOME":
		raw = home
	case strings.HasPrefix(raw, "$HOME/"):
		raw = filepath.Join(home, raw[len("$HOME/"):])
	case raw == "${HOME}":
		raw = home
	case strings.HasPrefix(raw, "${HOME}/"):
		raw = filepath.Join(home, raw[len("${HOME}/"):])
	case allowTilde && raw == "~":
		raw = home
	case allowTilde && strings.HasPrefix(raw, "~/"):
		raw = filepath.Join(home, raw[len("~/"):])
	case strings.ContainsRune(raw, '$'):
		return "", false
	}

	if !filepath.IsAbs(raw) {
		return "", false
	}
	return filepath.Clean(raw), true
}
