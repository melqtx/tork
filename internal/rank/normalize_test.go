package rank

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"[SubsPlease] Sousou no Frieren - 28 (1080p) [ABCD1234].mkv": "sousou no frieren 28",
		"Severance.S02E05.1080p.WEB.H264-SuccessfulCrab":             "severance",
		"Inception 2010 MULTi 2160p UHD BluRay REMUX HDR HEVC-GROUP": "inception 2010",
		"Breaking Bad S01-S05 Complete 1080p BluRay x265":            "breaking bad",
		"Attack.on.Titan.S01.1080p.BluRay.x264-CtrlHD":               "attack on titan",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	titles := []string{
		"[SubsPlease] Sousou no Frieren - 28 (1080p) [ABCD1234].mkv",
		"Severance.S02E05.1080p.WEB.H264-SuccessfulCrab",
	}
	for _, in := range titles {
		once := Normalize(in)
		if twice := Normalize(once); twice != once {
			t.Errorf("Normalize not idempotent: %q -> %q -> %q", in, once, twice)
		}
	}
}

// The same episode from different providers must converge on one GroupKey.
func TestGroupKeyConvergence(t *testing.T) {
	a := "Attack on Titan S01E05 1080p WEB-DL x264-GROUP"
	b := "[HorribleSubs] Attack on Titan - S1E5 (1080p) [DEADBEEF].mkv"
	ka := GroupKey(a, Parse(a))
	kb := GroupKey(b, Parse(b))
	if ka != kb {
		t.Errorf("group keys diverge:\n a=%q -> %q\n b=%q -> %q", a, ka, b, kb)
	}
}

// YTS parenthesizes the year, other providers leave it bare - they must
// still converge on the same group.
func TestGroupKeyYearConvergence(t *testing.T) {
	a := "Inception (2010) [1080p]"               // yts style
	b := "Inception 2010 1080p BluRay x265-GROUP" // scene style
	ka := GroupKey(a, Parse(a))
	kb := GroupKey(b, Parse(b))
	if ka != kb {
		t.Errorf("year styles diverge:\n a=%q -> %q\n b=%q -> %q", a, ka, b, kb)
	}
}

func TestGroupKeySeparatesResolutions(t *testing.T) {
	a := "Show S01E05 1080p WEB"
	b := "Show S01E05 720p WEB"
	if GroupKey(a, Parse(a)) == GroupKey(b, Parse(b)) {
		t.Error("different resolutions must not share a group key")
	}
}
