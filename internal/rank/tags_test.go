package rank

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		title string
		want  Tags
	}{
		{
			"[SubsPlease] Sousou no Frieren - 28 (1080p) [ABCD1234].mkv",
			Tags{Resolution: Res1080},
		},
		{
			"Severance.S02E05.1080p.WEB.H264-SuccessfulCrab",
			Tags{Resolution: Res1080, Source: SrcWebDL, Codec: "x264", Season: 2, Episode: 5},
		},
		{
			"Inception 2010 MULTi 2160p UHD BluRay REMUX HDR HEVC-GROUP",
			Tags{Resolution: Res2160, Source: SrcRemux, Codec: "x265", HDR: true},
		},
		{
			"Breaking Bad S01-S05 Complete 1080p BluRay x265",
			Tags{Resolution: Res1080, Source: SrcBluRay, Codec: "x265", Season: 1, SeasonEnd: 5, Complete: true},
		},
		{
			"Some Movie 2023 720p HDCAM x264",
			Tags{Resolution: Res720, Source: SrcCam, Codec: "x264"},
		},
		{
			"The Office Season 3 Complete WEB-DL",
			Tags{Source: SrcWebDL, Season: 3, Complete: true},
		},
		{
			"Random.Movie.480p.DVDRip.XviD",
			Tags{Resolution: Res480, Source: SrcDVD},
		},
	}
	for _, c := range cases {
		got := Parse(c.title)
		if got != c.want {
			t.Errorf("Parse(%q)\n got  %+v\n want %+v", c.title, got, c.want)
		}
	}
}

func TestIsPack(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"Breaking Bad S01-S05 Complete 1080p", true},
		{"The Office Season 3 Complete", true},
		{"Show S02 1080p WEB", true},     // season, no episode
		{"Show S02E05 1080p WEB", false}, // single episode
		{"Attack on Titan Complete Series", true},
		{"Some Movie 2010 1080p", false}, // no season at all
	}
	for _, c := range cases {
		if got := Parse(c.title).IsPack(); got != c.want {
			t.Errorf("IsPack(%q) = %v, want %v", c.title, got, c.want)
		}
	}
}
