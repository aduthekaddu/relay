package search

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/aduthekaddu/relay/internal/api"
)

// Ranking weights. A provider score is clamped to [0, 1]; match boosts are
// computed by the service on the title so providers with coarse scoring
// still sort sensibly next to each other.
const (
	boostExact    = 0.6  // title equals the query (case-insensitive)
	boostPrefix   = 0.35 // title starts with the query
	boostWord     = 0.2  // a word in the title starts with the query
	boostContains = 0.05 // title contains the query somewhere
	boostAllWords = 0.1  // every query word starts a title word
	boostRecency  = 0.15 // max boost for something touched just now
	recencyHalf   = 72 * time.Hour
)

// rank merges provider batches into one list: it clamps and boosts scores,
// drops duplicates (same scope + id), caps each scope at limit and sorts by
// final score, then recency, then title.
func rank(query string, batches []batch, limit int, now time.Time) []api.SearchResult {
	q := strings.ToLower(query)
	words := strings.Fields(q)
	out := make([]api.SearchResult, 0, 32)
	for _, b := range batches {
		out = append(out, rankScope(b, q, words, limit, now)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// rankScope scores one provider's batch and keeps its best limit results.
func rankScope(b batch, q string, words []string, limit int, now time.Time) []api.SearchResult {
	seen := make(map[string]bool, len(b.results))
	res := make([]api.SearchResult, 0, len(b.results))
	for _, r := range b.results {
		if r.ID == "" || r.Title == "" {
			continue
		}
		if r.Scope == "" {
			r.Scope = b.scope
		}
		key := r.Scope + "\x00" + r.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		r.Score = round3(score(r, q, words, now))
		res = append(res, r)
	}
	sort.SliceStable(res, func(i, j int) bool { return less(res[i], res[j]) })
	if len(res) > limit {
		res = res[:limit]
	}
	return res
}

// less orders results by score, then most recent, then title.
func less(a, b api.SearchResult) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if !a.At.Equal(b.At) {
		return a.At.After(b.At)
	}
	return a.Title < b.Title
}

// score combines the provider score with match and recency boosts.
func score(r api.SearchResult, q string, words []string, now time.Time) float64 {
	s := clamp01(r.Score)
	if q != "" {
		s += matchBoost(strings.ToLower(r.Title), q, words)
	}
	s += recency(r.At, now)
	return s
}

// matchBoost rewards exact, prefix and word-boundary title matches.
func matchBoost(title, q string, words []string) float64 {
	var b float64
	switch {
	case title == q:
		b = boostExact
	case strings.HasPrefix(title, q):
		b = boostPrefix
	case wordPrefix(title, q):
		b = boostWord
	case strings.Contains(title, q):
		b = boostContains
	}
	if len(words) > 1 && allWordPrefixes(title, words) {
		b += boostAllWords
	}
	return b
}

// wordPrefix reports whether q starts at a word boundary inside title
// (after a space, punctuation, or a lower→upper camel-case step is not
// considered since title is already lower-cased).
func wordPrefix(title, q string) bool {
	for i := 0; i < len(title); {
		j := strings.Index(title[i:], q)
		if j < 0 {
			return false
		}
		at := i + j
		if at == 0 || isBoundary(rune(title[at-1])) {
			return true
		}
		i = at + 1
	}
	return false
}

// allWordPrefixes reports whether every query word starts some title word.
func allWordPrefixes(title string, words []string) bool {
	tw := strings.FieldsFunc(title, isBoundary)
	for _, w := range words {
		found := false
		for _, t := range tw {
			if strings.HasPrefix(t, w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func isBoundary(r rune) bool {
	return unicode.IsSpace(r) || (r < unicode.MaxASCII && (unicode.IsPunct(r) || unicode.IsSymbol(r)))
}

// recency decays from boostRecency with a half-life of recencyHalf. Zero
// and future timestamps count as "now" only if they are within a minute of
// the clock (clock skew); otherwise they get no boost.
func recency(at, now time.Time) float64 {
	if at.IsZero() {
		return 0
	}
	age := now.Sub(at)
	if age < 0 {
		if age < -time.Minute {
			return 0
		}
		age = 0
	}
	return boostRecency * math.Exp2(-float64(age)/float64(recencyHalf))
}

func clamp01(f float64) float64 {
	switch {
	case math.IsNaN(f) || f < 0:
		return 0
	case f > 1:
		return 1
	}
	return f
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }
