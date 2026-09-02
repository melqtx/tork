package engine

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strings"
)

type ChecksumAlgorithm string

const (
	ChecksumSHA1   ChecksumAlgorithm = "sha1"
	ChecksumSHA256 ChecksumAlgorithm = "sha256"
	ChecksumSHA512 ChecksumAlgorithm = "sha512"
)

// Checksum is a published digest attached to a direct download.
// Empty is valid and means the source did not publish a checksum.
type Checksum struct {
	Algorithm ChecksumAlgorithm
	Hex       string
}

func NewChecksum(algorithm, digest string) (Checksum, error) {
	c := Checksum{
		Algorithm: ChecksumAlgorithm(strings.ToLower(strings.TrimSpace(algorithm))),
		Hex:       strings.ToLower(strings.TrimSpace(digest)),
	}
	if err := c.Validate(); err != nil {
		return Checksum{}, err
	}
	return c, nil
}

func SHA256Checksum(digest string) (Checksum, error) {
	if strings.TrimSpace(digest) == "" {
		return Checksum{}, nil
	}
	return NewChecksum(string(ChecksumSHA256), digest)
}

func (c Checksum) Empty() bool {
	return strings.TrimSpace(c.Hex) == "" && strings.TrimSpace(string(c.Algorithm)) == ""
}

func (c Checksum) Validate() error {
	if c.Empty() {
		return nil
	}
	want := 0
	switch c.Algorithm {
	case ChecksumSHA1:
		want = sha1.Size
	case ChecksumSHA256:
		want = sha256.Size
	case ChecksumSHA512:
		want = sha512.Size
	default:
		return fmt.Errorf("unsupported checksum algorithm %q", c.Algorithm)
	}
	if len(c.Hex) != want*2 {
		return fmt.Errorf("invalid %s checksum length", c.Algorithm)
	}
	if _, err := hex.DecodeString(c.Hex); err != nil {
		return fmt.Errorf("invalid %s checksum: %w", c.Algorithm, err)
	}
	return nil
}

func (c Checksum) newHash() (hash.Hash, error) {
	if c.Empty() {
		// SHA-256 remains the streaming default for unverified downloads. The
		// digest is discarded, but using one path keeps resume logic simple.
		return sha256.New(), nil
	}
	switch c.Algorithm {
	case ChecksumSHA1:
		return sha1.New(), nil
	case ChecksumSHA256:
		return sha256.New(), nil
	case ChecksumSHA512:
		return sha512.New(), nil
	default:
		return nil, errors.New("unsupported checksum algorithm")
	}
}

func (c Checksum) Label() string {
	if c.Empty() {
		return "checksum"
	}
	return strings.ToUpper(string(c.Algorithm))
}

type DirectDownload struct {
	URL          string
	Name         string
	Checksum     Checksum
	ExpectedSize int64
	// VerifyExisting prevents a direct download from silently trusting a file
	// that happened to exist at the destination before the request.
	VerifyExisting bool
	// LockToOrigin refuses redirects to a different scheme, host, or port. It
	// is intended for downloads whose publisher origin is known.
	LockToOrigin bool
}
