package provider

import "errors"

var (
	ErrResponseFormat  = errors.New("unrecognized search response")
	ErrFeedAPI         = errors.New("feed rejected the search")
	ErrFeedFormat      = errors.New("not an RSS/Torznab feed")
	ErrFeedAuth        = errors.New("authentication failed")
	ErrFeedUnsupported = errors.New("unsupported torrent links")
)

// PermanentSearchError identifies failures a retry cannot fix.
func PermanentSearchError(err error) bool {
	return errors.Is(err, ErrResponseFormat) || errors.Is(err, ErrBlocked) || errors.Is(err, ErrFeedFormat) || errors.Is(err, ErrFeedAuth) || errors.Is(err, ErrFeedUnsupported) || errors.Is(err, ErrFeedAPI)
}
