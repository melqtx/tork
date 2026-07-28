package provider

import (
	"sort"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

// maxMergedTrackers caps a merged tracker list. Every tracker in a magnet is an
// announce the client will actually make, so a torrent listed on a dozen
// indexes should not turn into a hundred outbound announces. Real listings
// carry 10-40 trackers each and overlap heavily, so this only ever bites in
// pathological cases.
const maxMergedTrackers = 64

// InfoHash returns the lowercase hex v1 infohash carried by r's magnet, or ""
// when the result is only a detail-page link or the magnet does not parse.
//
// This is the one identity two indexes cannot disagree about: they will spell
// the title differently, scrape different seeder counts, and file it under
// different categories, but the same infohash is the same bytes.
func (r Result) InfoHash() string {
	if r.Magnet == "" {
		return ""
	}
	m, err := metainfo.ParseMagnetUri(r.Magnet)
	if err != nil {
		return ""
	}
	return m.InfoHash.HexString()
}

// Trackers returns the announce URLs carried by r's magnet. After Merge this
// is the union of every index's list, which is the whole point of merging: the
// UI can show what a merged row actually bought.
func (r Result) Trackers() []string {
	if r.Magnet == "" {
		return nil
	}
	m, err := metainfo.ParseMagnetUri(r.Magnet)
	if err != nil {
		return nil
	}
	return m.Trackers
}

// Merge folds other into keep, which must be the same torrent (same infohash)
// listed on two indexes. keep holds on to its identity - title, provider,
// category, detail URL - because the caller is better placed to decide which
// listing describes the release well. What combines is everything the two
// indexes can each know a different piece of:
//
//   - trackers: the union, rebuilt into the magnet. This is the point of the
//     whole exercise. Every index ships whichever tracker set it happened to
//     record, so a torrent listed three times means three partial views of one
//     swarm; announcing to the union finds peers that no single listing would
//     have led us to.
//   - seeders/leechers: the higher of the two. The indexes scraped different
//     trackers at different times, and a swarm is at least as large as the
//     biggest count anyone reports for it.
//   - trusted: one index vouching for a release is enough.
//   - size: whichever is known. Identical infohashes are identical bytes, so a
//     zero here only ever means "that index didn't say".
//   - AlsoOn: the other listing's provider, so the UI can show corroboration.
//
// Non-tracker magnet parameters (webseeds, peer hints) stay with keep. They
// point at hosts rather than at announce endpoints, and quietly inheriting one
// index's idea of where to fetch bytes from is a bigger trust step than
// widening the set of trackers we ask about peers.
func Merge(keep, other Result) Result {
	out := keep
	out.Seeders = max(keep.Seeders, other.Seeders)
	out.Leechers = max(keep.Leechers, other.Leechers)
	out.Trusted = keep.Trusted || other.Trusted
	if out.SizeBytes <= 0 && other.SizeBytes > 0 {
		// Take the scraped string with the byte count, or the row would sort by
		// one index's size while displaying another's placeholder.
		out.SizeBytes, out.Size = other.SizeBytes, other.Size
	}
	if strings.TrimSpace(out.Size) == "" {
		out.Size = other.Size
	}
	if out.Category == "" {
		out.Category = other.Category
	}
	out.AlsoOn = mergedSources(keep, other)
	out.Magnet = unionMagnet(keep.Magnet, other.Magnet)
	return out
}

// MergeAll collapses results sharing an infohash into one entry each.
//
// The keeper is chosen by a total order that ignores arrival order, so a
// concurrent fan-out over several providers yields the same list every run:
// most seeders first, then the longest title (a full scene name carries the
// codec and release group that a tidied-up listing drops, which is what the
// tag parser and the ranker read), then provider and title alphabetically to
// break the remaining ties.
//
// Results without a magnet keep their own row: with no infohash there is
// nothing to match on, and resolving every detail page just to find out would
// cost a network round trip per row.
func MergeAll(in []Result) []Result {
	out := make([]Result, 0, len(in))
	at := make(map[string]int, len(in))
	for _, r := range in {
		hash := r.InfoHash()
		if hash == "" {
			out = append(out, r)
			continue
		}
		i, ok := at[hash]
		if !ok {
			at[hash] = len(out)
			out = append(out, r)
			continue
		}
		keep, other := out[i], r
		if betterKeeper(other, keep) {
			keep, other = other, keep
		}
		out[i] = Merge(keep, other)
	}
	return out
}

// betterKeeper reports whether a's listing should front a merged row instead
// of b's. Deliberately independent of arrival order - see MergeAll.
func betterKeeper(a, b Result) bool {
	if a.Seeders != b.Seeders {
		return a.Seeders > b.Seeders
	}
	if len(a.Title) != len(b.Title) {
		return len(a.Title) > len(b.Title)
	}
	if a.Provider != b.Provider {
		return a.Provider < b.Provider
	}
	return a.Title < b.Title
}

// mergedSources is the sorted set of other indexes carrying this torrent, with
// the keeper's own provider left out - the UI renders that one separately.
func mergedSources(keep, other Result) []string {
	set := make(map[string]bool, len(keep.AlsoOn)+len(other.AlsoOn)+2)
	for _, name := range keep.AlsoOn {
		set[name] = true
	}
	for _, name := range other.AlsoOn {
		set[name] = true
	}
	set[other.Provider] = true
	delete(set, keep.Provider)
	delete(set, "")

	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// unionMagnet rebuilds keep's magnet carrying both listings' trackers. It
// returns keep's magnet untouched when either side does not parse or when
// other adds nothing, so an ordinary result never gets its magnet rewritten
// for no reason.
func unionMagnet(keepMagnet, otherMagnet string) string {
	km, err := metainfo.ParseMagnetUri(keepMagnet)
	if err != nil {
		return keepMagnet
	}
	om, err := metainfo.ParseMagnetUri(otherMagnet)
	if err != nil {
		return keepMagnet
	}
	// Compare against keep's own deduplicated list, not its raw one. Magnets in
	// the wild repeat trackers, and against a raw count the dedupe of those
	// repeats can cancel out a genuinely new tracker - leaving the totals equal
	// and silently discarding what the other index brought.
	base := unionTrackers(km.Trackers, nil)
	merged := unionTrackers(km.Trackers, om.Trackers)
	if len(merged) == len(base) {
		return keepMagnet // other added nothing; leave the string byte-identical
	}
	km.Trackers = merged
	return km.String()
}

// unionTrackers is the deduplicated set of both announce lists, sorted.
//
// Sorting rather than preserving either side's order is what makes a merged
// magnet reproducible: folding four listings together pairwise would otherwise
// interleave their trackers by arrival, so the same search could yield a
// different magnet string run to run. Order carries no meaning to a client -
// every tracker in a magnet gets announced to - so there is nothing to lose by
// fixing one.
func unionTrackers(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, tr := range list {
			tr = strings.TrimSpace(tr)
			key := trackerKey(tr)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, tr)
		}
	}
	sort.Strings(out)
	if len(out) > maxMergedTrackers {
		out = out[:maxMergedTrackers]
	}
	return out
}

// trackerKey normalizes an announce URL for comparison only; the original
// string is what ends up in the magnet. Indexes differ on case and on a
// trailing slash while meaning the same endpoint.
func trackerKey(tr string) string {
	return strings.TrimSuffix(strings.ToLower(tr), "/")
}
