package agents

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

//go:embed prices.json
var pricesJSON []byte

// price is a model's rate card in USD per million tokens.
type price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// priceTable resolves model ids (in any vendor spelling) to prices.
type priceTable struct {
	Updated  string           `json:"updated"`
	Models   map[string]price `json:"models"`
	Families []struct {
		Match string `json:"match"`
		Model string `json:"model"`
	} `json:"families"`

	mu    sync.Mutex
	cache map[string]priceLookup
}

// priceLookup is the resolved price for one model id.
type priceLookup struct {
	Price     price
	Known     bool // any price found (exact or family)
	Estimated bool // family fallback or unknown model
}

func loadPrices(b []byte) (*priceTable, error) {
	var t priceTable
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("prices.json: %w", err)
	}
	for _, f := range t.Families {
		if _, ok := t.Models[f.Model]; !ok {
			return nil, fmt.Errorf("prices.json: family %q points at unknown model %q", f.Match, f.Model)
		}
	}
	t.cache = map[string]priceLookup{}
	return &t, nil
}

var (
	dateSuffixRe = regexp.MustCompile(`[-@](20\d{6}|\d{4}-\d{2}-\d{2}|latest|preview(-\d+)*|exp(-\d+)*)$`)
	bracketRe    = regexp.MustCompile(`\[[^\]]*\]$`)
)

// normalizeModel maps vendor spellings onto the price table's keys:
// "anthropic/claude-sonnet-4.5-20250929[1m]" -> "claude-sonnet-4-5".
func normalizeModel(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	m = bracketRe.ReplaceAllString(m, "")
	m = strings.TrimSuffix(m, "-thinking")
	for {
		n := dateSuffixRe.ReplaceAllString(m, "")
		if n == m {
			break
		}
		m = n
	}
	m = strings.ReplaceAll(m, ".", "-")
	m = strings.ReplaceAll(m, "_", "-")
	return m
}

// lookup returns the price for model. Exact keys win, then the longest
// key that prefixes the normalised id, then family substrings (estimated).
func (t *priceTable) lookup(model string) priceLookup {
	t.mu.Lock()
	defer t.mu.Unlock()
	if l, ok := t.cache[model]; ok {
		return l
	}
	l := t.resolve(normalizeModel(model))
	if len(t.cache) < 4096 {
		t.cache[model] = l
	}
	return l
}

func (t *priceTable) resolve(n string) priceLookup {
	if n == "" {
		return priceLookup{Estimated: true}
	}
	if p, ok := t.Models[n]; ok {
		return priceLookup{Price: p, Known: true}
	}
	best := ""
	for k := range t.Models {
		if strings.HasPrefix(n, k+"-") && len(k) > len(best) {
			best = k
		}
	}
	if best != "" {
		return priceLookup{Price: t.Models[best], Known: true}
	}
	for _, f := range t.Families {
		if strings.Contains(n, f.Match) {
			return priceLookup{Price: t.Models[f.Model], Known: true, Estimated: true}
		}
	}
	return priceLookup{Estimated: true}
}

// cost prices one usage record. Records that carry the agent's own cost
// figure (OpenCode, pi, Grok, Hermes, Crush) use it when the model is not
// in the table; otherwise the table wins so every agent is priced alike.
func (t *priceTable) cost(u usageRec) (usd float64, estimated bool) {
	l := t.lookup(u.Model)
	if !l.Known || (l.Estimated && u.NativeCost > 0) {
		if u.NativeCost > 0 {
			return u.NativeCost, false
		}
		return 0, true
	}
	p := l.Price
	usd = (float64(u.Input)*p.Input + float64(u.Output)*p.Output +
		float64(u.CacheRead)*p.CacheRead + float64(u.CacheWrite)*p.CacheWrite) / 1e6
	return usd, l.Estimated
}
