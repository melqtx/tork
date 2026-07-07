package tui

import (
	"strings"
	"time"
)

// tork's mascot is a cat that fetches torrents for you. Its mood follows what
// the app is doing - asleep when idle, curious while hunting, content while
// seeding - a small bit of warmth in the corners of the UI.

type mood int

const (
	moodSleeping mood = iota
	moodCurious
	moodBusy
	moodHappy
)

// smallCat is a compact three-line cat whose face matches the mood.
func smallCat(m mood) []string {
	face := "-.-"
	switch m {
	case moodCurious:
		face = "o.o"
	case moodBusy:
		face = ">.<"
	case moodHappy:
		face = "^.^"
	}
	return []string{
		` /\_/\`,
		`( ` + face + ` )`,
		` > ^ <`,
	}
}

// sleepingCat is the classic curled-up cat, for cozy empty states.
var sleepingCat = []string{
	"    |\\      _,,,---,,_",
	"    /,`.-'`'    -.  ;-;;,_",
	"   |,4-  ) )-,_..;\\ (  `'-'",
	"  '---''(_/--'  `-'\\_)",
}

// renderCat styles a cat block, padded to a rectangle so it stays aligned
// when centered. A sleeping cat gets a "z Z" beside its head.
func renderCat(lines []string, m mood) string {
	catLines := append([]string(nil), lines...)
	if m == moodSleeping && len(catLines) > 0 {
		catLines[0] += "  ~ z Z"
	}
	w := 0
	for _, l := range catLines {
		if len(l) > w {
			w = len(l)
		}
	}
	for i, l := range catLines {
		catLines[i] = l + strings.Repeat(" ", w-len(l))
	}
	return styleDim.Render(strings.Join(catLines, "\n"))
}

func cozyGreeting() string {
	switch h := time.Now().Hour(); {
	case h < 5:
		return "still up?"
	case h < 12:
		return "good morning"
	case h < 18:
		return "good afternoon"
	case h < 22:
		return "good evening"
	default:
		return "good night"
	}
}
