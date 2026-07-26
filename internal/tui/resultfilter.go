package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/melqtx/tork/internal/rank"
)

// parsedResultFilter combines fuzzy title text with structured predicates.
// Every predicate must match; a leading "-" negates one structured token.
type parsedResultFilter struct {
	text       string
	predicates []func(scoredRow) bool
	err        error
}

var filterNumber = regexp.MustCompile(`^(>=|<=|>|<|=)?(\d+(?:\.\d+)?)([a-zA-Z]*)$`)

func parseResultFilter(input string) parsedResultFilter {
	var filter parsedResultFilter
	var text []string
	for _, token := range strings.Fields(input) {
		predicate, recognized, err := parseFilterToken(token)
		if err != nil {
			filter.err = err
			return filter
		}
		if recognized {
			filter.predicates = append(filter.predicates, predicate)
		} else {
			text = append(text, token)
		}
	}
	filter.text = strings.Join(text, " ")
	return filter
}

func parseFilterToken(token string) (func(scoredRow) bool, bool, error) {
	negated := strings.HasPrefix(token, "-")
	raw := token
	if negated {
		raw = strings.TrimPrefix(raw, "-")
	}
	key, value, ok := strings.Cut(raw, ":")
	if !ok {
		return nil, false, nil
	}
	key = strings.ToLower(key)
	value = strings.ToLower(value)

	var predicate func(scoredRow) bool
	var err error
	switch key {
	case "seeders", "seed":
		predicate, err = numericFilter(value, true, func(row scoredRow) float64 {
			return float64(row.res.Seeders)
		})
	case "size":
		predicate, err = sizeFilter(value)
	case "res", "resolution":
		predicate, err = resolutionFilter(value)
	case "source", "src":
		predicate, err = sourceFilter(value)
	case "codec":
		predicate, err = codecFilter(value)
	case "provider", "prov":
		if value == "" {
			err = fmt.Errorf("%s needs a provider name", key)
		} else {
			predicate = func(row scoredRow) bool {
				return strings.EqualFold(row.res.Provider, value)
			}
		}
	case "category", "cat":
		if value == "" {
			err = fmt.Errorf("%s needs a category", key)
		} else {
			predicate = func(row scoredRow) bool {
				return strings.Contains(strings.ToLower(row.res.Category), value)
			}
		}
	case "is":
		predicate, err = attributeFilter(value)
	default:
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if negated {
		positive := predicate
		predicate = func(row scoredRow) bool { return !positive(row) }
	}
	return predicate, true, nil
}

func numericFilter(value string, defaultMinimum bool, read func(scoredRow) float64) (func(scoredRow) bool, error) {
	match := filterNumber.FindStringSubmatch(value)
	if match == nil || match[3] != "" {
		return nil, fmt.Errorf("invalid number %q", value)
	}
	want, err := strconv.ParseFloat(match[2], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid number %q", value)
	}
	op := match[1]
	if op == "" {
		if defaultMinimum {
			op = ">="
		} else {
			op = "<="
		}
	}
	return func(row scoredRow) bool { return compareNumber(read(row), want, op) }, nil
}

func sizeFilter(value string) (func(scoredRow) bool, error) {
	match := filterNumber.FindStringSubmatch(value)
	if match == nil || match[3] == "" {
		return nil, fmt.Errorf("size needs a unit, like size:<8gb")
	}
	number, err := strconv.ParseFloat(match[2], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid size %q", value)
	}
	multiplier, ok := sizeUnit(match[3])
	if !ok {
		return nil, fmt.Errorf("unknown size unit %q", match[3])
	}
	want := number * multiplier
	op := match[1]
	if op == "" {
		op = "<="
	}
	return func(row scoredRow) bool {
		return row.res.SizeBytes > 0 && compareNumber(float64(row.res.SizeBytes), want, op)
	}, nil
}

func sizeUnit(unit string) (float64, bool) {
	switch strings.ToLower(unit) {
	case "b":
		return 1, true
	case "kb", "kib":
		return 1 << 10, true
	case "mb", "mib":
		return 1 << 20, true
	case "gb", "gib":
		return 1 << 30, true
	case "tb", "tib":
		return 1 << 40, true
	default:
		return 0, false
	}
}

func resolutionFilter(value string) (func(scoredRow) bool, error) {
	var want rank.Resolution
	switch value {
	case "480", "480p":
		want = rank.Res480
	case "576", "576p":
		want = rank.Res576
	case "720", "720p":
		want = rank.Res720
	case "1080", "1080p":
		want = rank.Res1080
	case "2160", "2160p", "4k", "uhd":
		want = rank.Res2160
	default:
		return nil, fmt.Errorf("unknown resolution %q", value)
	}
	return func(row scoredRow) bool { return row.tags.Resolution == want }, nil
}

func sourceFilter(value string) (func(scoredRow) bool, error) {
	var want rank.Source
	switch strings.NewReplacer("-", "", "_", "", ".", "").Replace(value) {
	case "cam", "ts", "telesync":
		want = rank.SrcCam
	case "dvd":
		want = rank.SrcDVD
	case "hdtv":
		want = rank.SrcHDTV
	case "webrip":
		want = rank.SrcWebRip
	case "web", "webdl":
		want = rank.SrcWebDL
	case "bluray", "bdrip", "brrip":
		want = rank.SrcBluRay
	case "remux":
		want = rank.SrcRemux
	default:
		return nil, fmt.Errorf("unknown source %q", value)
	}
	return func(row scoredRow) bool { return row.tags.Source == want }, nil
}

func codecFilter(value string) (func(scoredRow) bool, error) {
	switch strings.NewReplacer(".", "", "-", "").Replace(value) {
	case "x265", "h265", "hevc":
		value = "x265"
	case "x264", "h264", "avc":
		value = "x264"
	case "av1":
		value = "av1"
	default:
		return nil, fmt.Errorf("unknown codec %q", value)
	}
	return func(row scoredRow) bool { return row.tags.Codec == value }, nil
}

func attributeFilter(value string) (func(scoredRow) bool, error) {
	switch value {
	case "trusted":
		return func(row scoredRow) bool { return row.res.Trusted }, nil
	case "hdr":
		return func(row scoredRow) bool { return row.tags.HDR }, nil
	case "dv", "dovi", "dolbyvision":
		return func(row scoredRow) bool { return row.tags.DV }, nil
	case "pack", "complete":
		return func(row scoredRow) bool { return row.tags.IsPack() }, nil
	default:
		return nil, fmt.Errorf("unknown attribute %q", value)
	}
}

func compareNumber(got, want float64, op string) bool {
	switch op {
	case ">":
		return got > want
	case ">=":
		return got >= want
	case "<":
		return got < want
	case "<=":
		return got <= want
	case "=":
		return got == want
	default:
		return false
	}
}

func (f parsedResultFilter) matches(row scoredRow) bool {
	if f.err != nil {
		return false
	}
	for _, predicate := range f.predicates {
		if !predicate(row) {
			return false
		}
	}
	return true
}
