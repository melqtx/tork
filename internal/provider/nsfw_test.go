package provider

import "testing"

func TestIsAdultResult(t *testing.T) {
	cases := []struct {
		name string
		r    Result
		want bool
	}{
		{"xxx category", Result{Title: "Some Video", Category: "XXX"}, true},
		{"xxx category lowercase", Result{Title: "clip", Category: "xxx"}, true},
		{"onlyfans in title", Result{Title: "Model OnlyFans Leak 2024"}, true},
		{"brazzers in title", Result{Title: "Brazzers.2024.1080p"}, true},
		{"porn whole word", Result{Title: "Amateur Porn 2024"}, true},

		// The collisions the filter must never eat:
		{"xXx the movie", Result{Title: "xXx (2002) 1080p BluRay", Category: "Movies"}, false},
		{"sussex place name", Result{Title: "Sussex Documentary 2021"}, false},
		{"portland not porn", Result{Title: "Portlandia S01 1080p"}, false},
		{"sex education show", Result{Title: "Sex Education S04 1080p", Category: "TV"}, false},
		{"plain movie", Result{Title: "Inception (2010) [1080p]", Category: "Movies"}, false},
		{"no category no token", Result{Title: "Ubuntu 24.04 LTS"}, false},
	}
	for _, tc := range cases {
		if got := isAdultResult(tc.r); got != tc.want {
			t.Errorf("%s: isAdultResult = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestQueryAllowsAdult(t *testing.T) {
	// only unambiguous brand/term tokens bypass the filter
	allowed := []string{"onlyfans", "some model onlyfans", "porn 2024", "brazzers", "pornhub leak"}
	for _, q := range allowed {
		if !queryAllowsAdult(q) {
			t.Errorf("queryAllowsAdult(%q) = false, want true", q)
		}
	}
	// collision-prone or plain queries must NOT bypass, so their XXX rows stay
	// filtered - this is the P1 leak guard.
	denied := []string{"inception", "ubuntu 24.04", "the office", "sussex", "xXx 2002", "sex education", "xxx", "essex"}
	for _, q := range denied {
		if queryAllowsAdult(q) {
			t.Errorf("queryAllowsAdult(%q) = true, want false", q)
		}
	}
}

func TestContentFilterAllow(t *testing.T) {
	adult := Result{Title: "Model OnlyFans 2024", Category: "XXX"}
	clean := Result{Title: "Inception (2010)", Category: "Movies"}

	off := ContentFilter{HideNSFW: false}
	if !off.Allow("inception", adult) {
		t.Error("filter off should allow everything")
	}

	on := ContentFilter{HideNSFW: true}
	if on.Allow("inception", adult) {
		t.Error("adult result should be hidden for a clean query")
	}
	if !on.Allow("inception", clean) {
		t.Error("clean result must never be hidden")
	}
	if !on.Allow("onlyfans", adult) {
		t.Error("adult result must be shown when the query asks for it")
	}

	// P1 guard: a legit search that merely collides with "xxx"/"sex" must NOT
	// bypass the filter - the movie shows, the XXX-category row stays hidden.
	if on.Allow("xXx 2002", adult) {
		t.Error("xXx 2002 must not surface XXX-category rows")
	}
	if !on.Allow("xXx 2002", Result{Title: "xXx (2002) 1080p", Category: "Movies"}) {
		t.Error("the actual xXx movie must still show")
	}
	if on.Allow("sex education", adult) {
		t.Error("sex education must not surface XXX-category rows")
	}
	if !on.Allow("sex education", Result{Title: "Sex Education S04 1080p", Category: "TV"}) {
		t.Error("the actual Sex Education show must still show")
	}
}
