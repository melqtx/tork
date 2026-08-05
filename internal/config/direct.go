package config

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const MaxDirectConnections = 32

var byteSizePattern = regexp.MustCompile(`(?i)^\s*(\d+(?:\.\d+)?)\s*(kb|kib|mb|mib|gb|gib)\s*$`)

// DirectMinChunkBytes parses Direct.MinChunkSize using binary units. Both MB
// and MiB mean 2^20 bytes, matching the rest of tork's size handling.
func (c Config) DirectMinChunkBytes() (int64, error) {
	if c.Direct.MaxConnections < 0 || c.Direct.MaxConnections > MaxDirectConnections {
		return 0, fmt.Errorf("direct.max_connections must be between 0 and %d", MaxDirectConnections)
	}
	m := byteSizePattern.FindStringSubmatch(c.Direct.MinChunkSize)
	if m == nil {
		return 0, errorsForDirectChunkSize()
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil || n <= 0 {
		return 0, errorsForDirectChunkSize()
	}
	var multiplier float64
	switch strings.ToLower(m[2]) {
	case "kb", "kib":
		multiplier = 1 << 10
	case "mb", "mib":
		multiplier = 1 << 20
	case "gb", "gib":
		multiplier = 1 << 30
	}
	if n > math.MaxInt64/multiplier {
		return 0, errorsForDirectChunkSize()
	}
	return int64(n * multiplier), nil
}

func errorsForDirectChunkSize() error {
	return fmt.Errorf("direct.min_chunk_size must be a positive size such as 10MB or 64MiB")
}
