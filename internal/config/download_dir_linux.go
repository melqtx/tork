//go:build linux

package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

func configuredDownloadDir(home, configHome string) (string, bool) {
	configHome = strings.TrimSpace(configHome)
	if configHome == "" || !filepath.IsAbs(configHome) {
		configHome = filepath.Join(home, ".config")
	}

	f, err := os.Open(filepath.Join(filepath.Clean(configHome), "user-dirs.dirs"))
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 || strings.TrimSpace(line[:eq]) != "XDG_DOWNLOAD_DIR" {
			continue
		}
		value, ok := parseUserDirValue(line[eq+1:])
		if !ok {
			return "", false
		}
		return normalizeDownloadDir(value, home, false)
	}
	return "", false
}

// parseUserDirValue accepts the small assignment-value subset emitted by
// xdg-user-dirs. The returned value is data only and is never shell-evaluated.
func parseUserDirValue(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if raw[0] == '"' {
		end := strings.IndexByte(raw[1:], '"')
		if end < 0 {
			return "", false
		}
		end++
		value := raw[1:end]
		tail := strings.TrimSpace(raw[end+1:])
		if tail != "" && !strings.HasPrefix(tail, "#") {
			return "", false
		}
		if strings.ContainsRune(value, '\\') {
			// Escapes require shell semantics; reject rather than interpreting
			// them incompletely or unexpectedly.
			return "", false
		}
		return value, value != ""
	}

	value := raw
	if i := strings.IndexAny(raw, " \t"); i >= 0 {
		value = raw[:i]
		tail := strings.TrimSpace(raw[i:])
		if tail != "" && !strings.HasPrefix(tail, "#") {
			return "", false
		}
	}
	return value, value != ""
}
