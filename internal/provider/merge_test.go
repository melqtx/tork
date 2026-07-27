package provider

import (
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const testHash = "0123456789abcdef0123456789abcdef01234567"

// base32Magnet renders the same infohash in the base32 xt form some indexes
// still emit, so the dedupe cannot be fooled by an encoding difference.
func base32Magnet(t *testing.T, hexHash, name string) string {
	t.Helper()
	raw, err := hex.DecodeString(hexHash)
	if err != nil {
		t.Fatalf("bad test hash: %v", err)
	}
	return "magnet:?xt=urn:btih:" + base32.StdEncoding.EncodeToString(raw) + "&dn=" + name
}

func TestInfoHashIsStableAcrossListings(t *testing.T) {
	hex := Result{Magnet: BuildMagnet(testHash, "Some Release 1080p", []string{"udp://a:1/announce"})}
	upper := Result{Magnet: BuildMagnet(strings.ToUpper(testHash), "Some.Release.1080p.x264", nil)}
	b32 := Result{Magnet: base32Magnet(t, testHash, "Some Release")}

	for name, r := range map[string]Result{"hex": hex, "uppercase": upper, "base32": b32} {
		if got := r.InfoHash(); got != testHash {
			t.Errorf("%s listing: InfoHash() = %q, want %q", name, got, testHash)
		}
	}
}

func TestInfoHashEmptyWhenNothingToCompare(t *testing.T) {
	cases := map[string]Result{
		"detail page only": {DetailURL: "https://example.org/torrent/1"},
		"not a magnet":     {Magnet: "https://example.org/x.torrent"},
		"no infohash":      {Magnet: "magnet:?dn=whatever"},
		"truncated hash":   {Magnet: "magnet:?xt=urn:btih:abc"},
	}
	for name, r := range cases {
		if got := r.InfoHash(); got != "" {
			t.Errorf("%s: InfoHash() = %q, want \"\"", name, got)
		}
	}
}

// TestMergeUnionsTrackers is the core of the feature: two indexes each know a
// partial tracker set for one torrent, and the merged magnet must carry both.
func TestMergeUnionsTrackers(t *testing.T) {
	keep := Result{
		Provider: "knaben",
		Magnet:   BuildMagnet(testHash, "Release", []string{"udp://one:1/announce", "udp://shared:2/announce"}),
	}
	other := Result{
		Provider: "yts",
		// same shared tracker in another spelling, plus one only this index has
		Magnet: BuildMagnet(testHash, "Release", []string{"udp://SHARED:2/announce/", "udp://two:3/announce"}),
	}

	got := Merge(keep, other).Trackers()
	want := []string{"udp://one:1/announce", "udp://shared:2/announce", "udp://two:3/announce"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged trackers = %v, want %v (sorted union, no case/slash dupes)", got, want)
	}
	if h := Merge(keep, other).InfoHash(); h != testHash {
		t.Fatalf("merge changed the infohash: %q", h)
	}
}

func TestMergeLeavesMagnetAloneWhenNothingIsAdded(t *testing.T) {
	magnet := BuildMagnet(testHash, "Release", []string{"udp://one:1/announce"})
	keep := Result{Provider: "knaben", Magnet: magnet}
	other := Result{Provider: "yts", Magnet: BuildMagnet(testHash, "Release", []string{"udp://ONE:1/announce"})}

	if got := Merge(keep, other).Magnet; got != magnet {
		t.Fatalf("magnet rewritten for no gain:\n got %q\nwant %q", got, magnet)
	}
}

func TestMergeTakesBestKnownSwarmAndSize(t *testing.T) {
	keep := Result{
		Provider: "knaben", Title: "Release", Seeders: 12, Leechers: 30,
		Magnet: BuildMagnet(testHash, "Release", nil),
	}
	other := Result{
		Provider: "yts", Title: "Release", Seeders: 340, Leechers: 8, Trusted: true,
		Size: "2.1 GiB", SizeBytes: 2254857830, Category: "Movies",
		Magnet: BuildMagnet(testHash, "Release", nil),
	}

	got := Merge(keep, other)
	if got.Seeders != 340 {
		t.Errorf("Seeders = %d, want 340 (the higher scrape)", got.Seeders)
	}
	if got.Leechers != 30 {
		t.Errorf("Leechers = %d, want 30 (the higher scrape)", got.Leechers)
	}
	if !got.Trusted {
		t.Error("Trusted = false, want true (either index vouching is enough)")
	}
	if got.SizeBytes != 2254857830 || got.Size != "2.1 GiB" {
		t.Errorf("size = %q/%d, want the known one from the other index", got.Size, got.SizeBytes)
	}
	if got.Category != "Movies" {
		t.Errorf("Category = %q, want the known one", got.Category)
	}
	if got.Provider != "knaben" || got.Title != "Release" {
		t.Errorf("keeper lost its identity: %q / %q", got.Provider, got.Title)
	}
}

func TestMergeRecordsTheOtherIndexes(t *testing.T) {
	magnet := BuildMagnet(testHash, "Release", nil)
	a := Result{Provider: "knaben", Magnet: magnet}
	b := Result{Provider: "yts", Magnet: magnet}
	c := Result{Provider: "1337x", Magnet: magnet}

	got := Merge(Merge(a, b), c).AlsoOn
	if want := []string{"1337x", "yts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AlsoOn = %v, want %v (sorted, keeper excluded)", got, want)
	}

	// Folding a row back into its own provider must not list it against itself.
	self := Merge(Merge(a, b), Result{Provider: "knaben", Magnet: magnet})
	if want := []string{"yts"}; !reflect.DeepEqual(self.AlsoOn, want) {
		t.Fatalf("AlsoOn = %v, want %v", self.AlsoOn, want)
	}
}

func TestMergeAllCollapsesAndIsOrderIndependent(t *testing.T) {
	listings := []Result{
		{Provider: "knaben", Title: "Some Release 1080p", Seeders: 12,
			Magnet: BuildMagnet(testHash, "r", []string{"udp://one:1/announce"})},
		{Provider: "yts", Title: "Some.Release.2024.1080p.BluRay.x264-GRP", Seeders: 340,
			Magnet: BuildMagnet(testHash, "r", []string{"udp://two:2/announce"})},
		{Provider: "1337x", Title: "Some Release", Seeders: 5,
			Magnet: BuildMagnet(testHash, "r", []string{"udp://three:3/announce"})},
	}

	var first []Result
	for i, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 0, 2}, {2, 0, 1}} {
		in := make([]Result, 0, len(order))
		for _, j := range order {
			in = append(in, listings[j])
		}
		got := MergeAll(in)
		if len(got) != 1 {
			t.Fatalf("order %v: %d rows, want 1", order, len(got))
		}
		if got[0].Provider != "yts" {
			t.Errorf("order %v: keeper = %q, want yts (most seeders)", order, got[0].Provider)
		}
		if n := len(got[0].Trackers()); n != 3 {
			t.Errorf("order %v: %d trackers, want 3 (union of all listings)", order, n)
		}
		if i == 0 {
			first = got
			continue
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("order %v produced a different result than %v:\n got %+v\nwant %+v",
				order, []int{0, 1, 2}, got, first)
		}
	}
}

func TestMergeAllKeepsDistinctAndUnresolvedRows(t *testing.T) {
	otherHash := "89abcdef0123456789abcdef0123456789abcdef"
	in := []Result{
		{Provider: "knaben", Magnet: BuildMagnet(testHash, "a", nil)},
		{Provider: "yts", Magnet: BuildMagnet(otherHash, "b", nil)},
		{Provider: "1337x", DetailURL: "https://example.org/1"},
		{Provider: "1337x", DetailURL: "https://example.org/2"},
		{Provider: "eztv", Magnet: BuildMagnet(testHash, "a", nil)},
	}
	got := MergeAll(in)
	if len(got) != 4 {
		t.Fatalf("%d rows, want 4 (one merge; detail-page rows cannot be compared)", len(got))
	}
	if got[0].AlsoOn == nil {
		t.Error("first row should record eztv as a second source")
	}
}

func TestUnionTrackersIsCapped(t *testing.T) {
	var a, b []string
	for i := range maxMergedTrackers {
		a = append(a, fmt.Sprintf("udp://a%d:1/announce", i))
		b = append(b, fmt.Sprintf("udp://b%d:1/announce", i))
	}
	if got := len(unionTrackers(a, b)); got != maxMergedTrackers {
		t.Fatalf("union kept %d trackers, want the %d cap", got, maxMergedTrackers)
	}
}

// TestMergeSurvivesDuplicateTrackersInTheKeeper is a regression test. Magnets
// in the wild repeat trackers, and the "did other add anything?" check used to
// compare the merged list against the keeper's *raw* count: deduplicating the
// keeper's own repeats offset the new tracker, the counts matched, and the
// merge was discarded as a no-op. That silently defeated the whole feature for
// exactly the messy magnets it exists to improve.
func TestMergeSurvivesDuplicateTrackersInTheKeeper(t *testing.T) {
	keep := Result{Provider: "knaben", Magnet: BuildMagnet(testHash, "r",
		[]string{"udp://a:1/announce", "udp://a:1/announce/", "udp://b:2/announce"})}
	other := Result{Provider: "yts", Magnet: BuildMagnet(testHash, "r",
		[]string{"udp://new:9/announce"})}

	got := Merge(keep, other).Trackers()
	want := []string{"udp://a:1/announce", "udp://b:2/announce", "udp://new:9/announce"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged trackers = %v, want %v (the keeper's repeat must not mask the new one)", got, want)
	}
}
