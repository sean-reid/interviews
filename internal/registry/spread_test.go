package registry

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/sean-reid/interviews/internal/taxonomy"
	"github.com/sean-reid/interviews/internal/variant"
)

// A leaked write-up ages fast only if the next candidate draws a different
// environment, so every shipped debugging scenario has to vary in more than
// its fault pack: pack alone once left one of them with two variants and the
// other with four. Counted by resolving real seeds against the shipped
// content, because the number that matters is the one candidates draw.
func TestShippedDebuggingVariantsStayDistinct(t *testing.T) {
	reg, err := Load(os.DirFS("../../content"))
	if err != nil {
		t.Fatalf("loading the content root: %v", err)
	}

	const seeds = 500
	checked := 0
	for _, e := range reg.Problems() {
		if e.Type != taxonomy.Debugging || e.Problem == nil {
			continue
		}
		id := e.Problem.Manifest.ID
		seen := map[string]bool{}
		for i := range seeds {
			r, err := variant.Resolve(id, e.Problem.Manifest.Params, "seed-"+strconv.Itoa(i), nil)
			if err != nil {
				t.Fatalf("%s: resolving seed %d: %v", id, i, err)
			}
			key, err := json.Marshal(r.Params)
			if err != nil {
				t.Fatalf("%s: %v", id, err)
			}
			seen[string(key)] = true
		}
		if len(seen) < seeds*9/10 {
			t.Errorf("%s: %d distinct variants over %d seeds, want at least %d",
				id, len(seen), seeds, seeds*9/10)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no debugging problems found; the content root moved")
	}
}
