//go:build !linux

package config

func configuredDownloadDir(_, _ string) (string, bool) {
	return "", false
}
