package rank

import (
	"reflect"
	"testing"
)

func TestNoisy(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		title   string
		seeders int
		want    bool
	}{
		{"clean release", "oppenheimer", "Oppenheimer 2023 1080p BluRay x264-GRP", 120, false},
		{"dead swarm", "oppenheimer", "Oppenheimer 2023 1080p BluRay x264-GRP", 0, true},
		{"cam", "oppenheimer", "Oppenheimer 2023 HDCAM x264", 50, true},
		{"hdts", "oppenheimer", "Oppenheimer 2023 HDTS 720p", 50, true},
		{"soundtrack", "oppenheimer", "Oppenheimer 2023 Original Soundtrack FLAC", 40, true},
		{"real story", "the conjuring", "The Conjuring Based on a Real Story Documentary", 30, true},
		{"hindi dubbed on plain search", "oppenheimer", "Oppenheimer 2023 1080p Hindi Dubbed", 200, true},
		{"vostfr on plain search", "oppenheimer", "Oppenheimer 2023 1080p VOSTFR", 15, true},
		// The user asked for the language: it is what they want, not noise.
		{"hindi search keeps hindi", "oppenheimer hindi", "Oppenheimer 2023 1080p Hindi Dubbed", 200, false},
		{"vostfr search keeps vostfr", "oppenheimer vostfr", "Oppenheimer 2023 1080p VOSTFR", 15, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tags := Parse(tc.title)
			if got := Noisy(tc.query, tc.title, tags, tc.seeders); got != tc.want {
				t.Fatalf("Noisy(%q, %q, %d) = %v, want %v", tc.query, tc.title, tc.seeders, got, tc.want)
			}
		})
	}
}

func TestNoiseReasons(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		title   string
		seeders int
		want    []string
	}{
		{"zero seed", "oppenheimer", "Oppenheimer 2023 1080p BluRay", 0, []string{"zero seed"}},
		{"cam", "oppenheimer", "Oppenheimer 2023 HDTS 720p", 12, []string{"cam"}},
		{"soundtrack", "oppenheimer", "Oppenheimer Original OST FLAC", 10, []string{"soundtrack"}},
		{"real story", "oppenheimer", "Oppenheimer The Real Story Documentary", 10, []string{"real story"}},
		{"language variant on plain search", "oppenheimer", "Oppenheimer 2023 Hindi Dubbed", 10, []string{"language variant"}},
		{"language requested is not noise", "oppenheimer hindi", "Oppenheimer 2023 Hindi Dubbed", 10, nil},
		{"combined", "oppenheimer", "Oppenheimer HDCAM Hindi Dubbed OST", 0, []string{"zero seed", "cam", "soundtrack", "language variant"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tags := Parse(tc.title)
			if got := NoiseReasons(tc.query, tc.title, tags, tc.seeders); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("NoiseReasons(%q, %q, %d) = %#v, want %#v", tc.query, tc.title, tc.seeders, got, tc.want)
			}
			if got := Noisy(tc.query, tc.title, tags, tc.seeders); got != (len(tc.want) > 0) {
				t.Fatalf("Noisy(%q, %q, %d) = %v, want %v", tc.query, tc.title, tc.seeders, got, len(tc.want) > 0)
			}
		})
	}
}
