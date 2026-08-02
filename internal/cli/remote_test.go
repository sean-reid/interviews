package cli

import (
	"strings"
	"testing"

	"github.com/sean-reid/interviews/internal/interview"
)

// The reserved seed names live in the interview package and the keys that
// make them dangerous are built here. Change a prefix on this side without
// reserving it and a seed could point an interview at shared data again, so
// assert the keys really do start where the reservation says.
func TestBucketKeysStartWithTheReservedPrefixes(t *testing.T) {
	if got := StateKey("calm-bison-0801"); !strings.HasPrefix(got, interview.StatePrefix+"/") {
		t.Errorf("state key %q does not start with the reserved %q", got, interview.StatePrefix)
	}
	// Positive control: the prefix is not something every string starts with.
	if strings.HasPrefix("calm-bison-0801/terraform.tfstate", interview.StatePrefix+"/") {
		t.Fatal("the prefix check matches a key without it, so the assertion above proves nothing")
	}
	if err := interview.ValidSeed(interview.StatePrefix); err == nil {
		t.Errorf("%q builds the state key and is still an allowed seed", interview.StatePrefix)
	}
	if err := interview.ValidSeed(interview.TarballPrefix); err == nil {
		t.Errorf("%q holds the shared tarball and is still an allowed seed", interview.TarballPrefix)
	}
}
