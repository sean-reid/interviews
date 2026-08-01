package registry

import (
	"strings"
	"testing"

	"github.com/sean-reid/interviews/internal/content"
)

// A problem that varies in nothing but its fault pack is one leaked
// write-up away from useless, so validation counts the environments real
// seeds actually draw.
func TestVariantSpread(t *testing.T) {
	packOnly := map[string]content.ParamSpec{
		"fault_pack": {Type: content.Choice, Of: []string{"pack-a", "pack-b"}},
	}
	if _, ok := checkVariantSpread(&content.Problem{
		Manifest: content.Manifest{ID: "narrow", Params: packOnly},
	}); ok {
		t.Error("a problem with two possible environments passed the spread check")
	}

	wide := map[string]content.ParamSpec{
		"fault_pack": {Type: content.Choice, Of: []string{"pack-a", "pack-b"}},
		"team":       {Type: content.Choice, Of: []string{"a", "b", "c", "d", "e", "f"}},
		"scale":      {Type: content.Int, Min: ptr(1), Max: ptr(40)},
		"port":       {Type: content.Int, Min: ptr(8000), Max: ptr(8999)},
	}
	if issue, ok := checkVariantSpread(&content.Problem{
		Manifest: content.Manifest{ID: "wide", Params: wide},
	}); !ok {
		t.Errorf("a problem with a wide parameter space failed: %s", issue.Msg)
	}

	// The message has to say what to do about it, since the author reading it
	// has to decide which parameters to add.
	issue, _ := checkVariantSpread(&content.Problem{
		Manifest: content.Manifest{ID: "narrow", Params: packOnly},
	})
	for _, want := range []string{"distinct variants", "vary more than the fault pack"} {
		if !strings.Contains(issue.Msg, want) {
			t.Errorf("message missing %q: %s", want, issue.Msg)
		}
	}
}

func ptr(i int) *int { return &i }
