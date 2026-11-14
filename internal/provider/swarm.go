package provider

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// SwarmObservation is one source's report, captured when fetched. FetchedAt
// does not claim that the index refreshed its tracker statistics at that time.
type SwarmObservation struct {
	Provider                        string
	Seeders, Leechers               int
	SeedersUnknown, LeechersUnknown bool
	FetchedAt                       time.Time
}

func (r Result) WithSwarm(seeders, leechers string) Result {
	r.Seeders, r.SeedersUnknown = parseSwarmCount(seeders)
	r.Leechers, r.LeechersUnknown = parseSwarmCount(leechers)
	return r
}

func parseSwarmCount(s string) (int, bool) {
	n, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(s), ",", ""))
	if err != nil || n < 0 {
		return 0, true
	}
	return n, false
}

func (r Result) KnownSeeders() bool  { return !r.SeedersUnknown && r.Seeders >= 0 }
func (r Result) KnownLeechers() bool { return !r.LeechersUnknown && r.Leechers >= 0 }

func (r Result) WithObservation(at time.Time) Result {
	if len(r.Observations) == 0 {
		r.Observations = []SwarmObservation{{r.Provider, r.Seeders, r.Leechers, !r.KnownSeeders(), !r.KnownLeechers(), at}}
	}
	return r
}

// Retain one latest report per source, in deterministic display order.
func mergeObservations(a, b Result) []SwarmObservation {
	a, b = a.WithObservation(time.Time{}), b.WithObservation(time.Time{})
	bySource := make(map[string]SwarmObservation)
	for _, list := range [][]SwarmObservation{a.Observations, b.Observations} {
		for _, observation := range list {
			prev, ok := bySource[observation.Provider]
			if !ok || observation.FetchedAt.After(prev.FetchedAt) ||
				(observation.FetchedAt.Equal(prev.FetchedAt) && observationBetter(observation, prev)) {
				bySource[observation.Provider] = observation
			}
		}
	}
	var out []SwarmObservation
	for _, observation := range bySource {
		out = append(out, observation)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

func observationBetter(a, b SwarmObservation) bool {
	if a.SeedersUnknown != b.SeedersUnknown {
		return !a.SeedersUnknown
	}
	if a.Seeders != b.Seeders {
		return a.Seeders > b.Seeders
	}
	if a.LeechersUnknown != b.LeechersUnknown {
		return !a.LeechersUnknown
	}
	if !a.FetchedAt.Equal(b.FetchedAt) {
		return a.FetchedAt.After(b.FetchedAt)
	}
	if a.Leechers != b.Leechers {
		return a.Leechers < b.Leechers
	}
	return a.Provider < b.Provider
}
