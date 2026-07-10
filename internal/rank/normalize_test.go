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

func TestSplitTitle(t *testing.T) {
	cases := []struct {
		title, content, year string
	}{
		// scene style: cut at the year, decoration never reaches the content
		{"Project.Hail.Mary.2026.2160p.WEB-DL.DDP5.1.Atmos.H.265-RDNYB", "project hail mary", "2026"},
		// yts style parenthesized year
		{"Project Hail Mary (2026) 2160p WEBRip 5.1 10Bit x265 -YTS", "project hail mary", "2026"},
		// year and tech buried in one bracketed metadata blob
		{"Проект «Конец света» / Project Hail Mary [2026, США, фантастика, WEB-DL 1080p]", "project hail mary", "2026"},
		// a leading year is the title, the later year is the release year
		{"1917.2019.1080p.BluRay.x264-GROUP", "1917", "2019"},
		// a mid-title year: the last year before the tech tokens wins
		{"Blade.Runner.2049.2017.2160p.WEB-DL-GROUP", "blade runner 2049", "2017"},
		// no year at all: cut at the first tech token
		{"[SubsPlease] Sousou no Frieren - 28 (1080p) [ABCD1234].mkv", "sousou no frieren 28", ""},
		// no year, no tech: the whole title is content
		{"Andy Weir - Project Hail Mary", "andy weir project hail mary", ""},
	}
	for _, c := range cases {
		content, year := SplitTitle(c.title)
		if content != c.content || year != c.year {
			t.Errorf("SplitTitle(%q) = (%q, %q), want (%q, %q)", c.title, content, year, c.content, c.year)
		}
	}
}

// Real-world releases of one movie must collapse into one group per
// resolution, not one group per release - the regression behind the graph
// view degenerating into a flat list.
func TestGroupKeyClustersRealWorldReleases(t *testing.T) {
	releases1080 := []string{
		"Project Hail Mary 2026 1080p WEB-DL HEVC x265 5.1 BONE",
		"Project.Hail.Mary.2026.1080p.WEB-DL.DDP5.1.Atmos.H.264-RDNYB",
		"Project.Hail.Mary.2026.1080p.AMZN.WEBRip.AAC5.1.10bits.x265-Rapta",
		"Project Hail Mary 2026 1080p 10bit WEBRip 6CH x265 HEVC-PSA",
		"Проект «Конец света» / Project Hail Mary [2026, США, фантастика, WEB-DL 1080p]",
		"Project Hail Mary (2026) 1080p H264 iMAX iTA EnG AC3 Sub iTA En",
	}
	keys := map[string]bool{}
	for _, title := range releases1080 {
		keys[GroupKey(title, Parse(title))] = true
	}
	if len(keys) != 1 {
		t.Errorf("1080p releases split into %d groups, want 1: %v", len(keys), keys)
	}
}

func TestReleaseGroup(t *testing.T) {
	cases := map[string]string{
		"Project.Hail.Mary.2026.2160p.WEB-DL.DDP5.1.Atmos.H.265-RDNYB": "RDNYB",
		"Project Hail Mary 2026 1080p 10bit WEBRip 6CH x265 HEVC-PSA":  "PSA",
		"Movie.2020.1080p.WEB.x264-AAC":                                "",       // tech token, not a group
		"Spider-Man":                                                   "",       // plain title, no release metadata
		"Andy Weir - Project Hail Mary":                                "",       // not a scene name
		"Attack.on.Titan.S01.1080p.BluRay.x264-CtrlHD":                 "CtrlHD", //
		"Severance.S02E05.1080p.WEB.H264-SuccessfulCrab":               "SuccessfulCrab",
	}
	for title, want := range cases {
		if got := ReleaseGroup(title); got != want {
			t.Errorf("ReleaseGroup(%q) = %q, want %q", title, got, want)
		}
	}
}
