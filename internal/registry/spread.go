package registry

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/sean-reid/interviews/internal/content"
	"github.com/sean-reid/interviews/internal/variant"
)

// Seeds drawn when measuring how many distinct environments a problem can
// produce, and the share of them that must differ. A leaked write-up ages
// fast only if the next candidate draws a different environment, so a
// problem that varies in nothing but its fault pack is one write-up away
// from useless: pack alone once left one scenario with two variants.
const (
	spreadSeeds = 500
	spreadRatio = 9 // out of 10
)

// checkVariantSpread resolves real seeds against a debugging problem and
// reports when too few of them differ. Counted by resolving rather than by
// multiplying the parameter space, because the number that matters is the
// one candidates actually draw.
func checkVariantSpread(p *content.Problem) (content.Issue, bool) {
	id := p.Manifest.ID
	seen := make(map[string]bool, spreadSeeds)
	for i := range spreadSeeds {
		r, err := variant.Resolve(id, p.Manifest.Params, "spread-"+strconv.Itoa(i), nil)
		if err != nil {
			return content.Issue{
				Path: content.ManifestName,
				Msg:  fmt.Sprintf("resolving a variant failed, so its spread cannot be measured: %v", err),
			}, false
		}
		key, err := json.Marshal(r.Params)
		if err != nil {
			return content.Issue{Path: content.ManifestName, Msg: err.Error()}, false
		}
		seen[string(key)] = true
	}
	if want := spreadSeeds * spreadRatio / 10; len(seen) < want {
		return content.Issue{
			Path: content.ManifestName,
			Msg: fmt.Sprintf("only %d distinct variants over %d seeds, want at least %d: vary more than the fault pack, or a leaked write-up serves the next candidate",
				len(seen), spreadSeeds, want),
		}, false
	}
	return content.Issue{}, true
}
