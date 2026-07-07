package rank

import (
	"math"

	"github.com/melqtx/tork/internal/provider"
)

// Weights tune the scoring formula. Persisted in config.yaml under `ranking:`.
type Weights struct {
	Seeders     float64 `yaml:"seeders"`
	Health      float64 `yaml:"health"`
	Quality     float64 `yaml:"quality"`
	Trusted     float64 `yaml:"trusted"`
	DeadPenalty float64 `yaml:"dead_penalty"`
}

func DefaultWeights() Weights {
	return Weights{
		Seeders:     40,
		Health:      15,
		Quality:     1.0,
		Trusted:     12,
		DeadPenalty: 1000,
	}
}

// Score ranks a result. Higher is better. Dead torrents (0 seeders) are sunk
// far below everything else; CAM/TS sources are sunk hard by the quality term.
func Score(r provider.Result, t Tags, w Weights) float64 {
	seeders := float64(max(0, r.Seeders))
	leechers := float64(max(0, r.Leechers))

	score := w.Seeders * math.Log10(1+seeders)
	score += w.Health * seeders / (seeders + leechers + 1)
	score += w.Quality * qualityBoost(t)
	score += w.Trusted * trustBoost(r)
	score -= deadPenalty(r.Seeders, w)
	return score
}

func qualityBoost(t Tags) float64 {
	var b float64
	switch t.Resolution {
	case Res2160:
		b += 18
	case Res1080:
		b += 15
	case Res720:
		b += 8
	}
	switch t.Source {
	case SrcRemux:
		b += 12
	case SrcWebDL:
		b += 10
	case SrcBluRay:
		b += 10
	case SrcWebRip:
		b += 7
	case SrcHDTV:
		b += 3
	case SrcDVD:
		b += 2
	case SrcCam:
		b -= 60 // hard sink: never surface a cam over a real release
	}
	switch t.Codec {
	case "x265", "av1":
		b += 4
	case "x264":
		b += 2
	}
	return b
}

func trustBoost(r provider.Result) float64 {
	if r.Trusted {
		return 1.0
	}
	switch r.Provider {
	case "yts":
		return 0.5 // curated API, consistently well-formed releases
	case "eztv":
		return 0.2
	}
	return 0
}

func deadPenalty(seeders int, w Weights) float64 {
	switch {
	case seeders <= 0:
		return w.DeadPenalty
	case seeders <= 2:
		return 20
	}
	return 0
}
