package variant

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/sean-reid/interviews/internal/content"
)

func intp(n int) *int { return &n }

func specs() map[string]content.ParamSpec {
	return map[string]content.ParamSpec{
		"fault_pack": {Type: content.Choice, Of: []string{"pack-a", "pack-b", "pack-c"}},
		"scale":      {Type: content.Int, Min: intp(3), Max: intp(9)},
		"team_name":  {Type: content.String, Default: "umbrella"},
		"region":     {Type: content.Choice, Of: []string{"east", "west"}, Default: "east"},
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	a, err := Resolve("pipeline-meltdown", specs(), "calm-bison-0731", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Resolve("pipeline-meltdown", specs(), "calm-bison-0731", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Params, b.Params) {
		t.Errorf("same inputs resolved differently: %v vs %v", a.Params, b.Params)
	}
}

func TestResolveVariesByInterviewAndProblem(t *testing.T) {
	// Wide space so distinct seeds virtually always differ; all inputs fixed,
	// so this cannot flake.
	wide := map[string]content.ParamSpec{
		"n": {Type: content.Int, Min: intp(0), Max: intp(1 << 30)},
	}
	a, _ := Resolve("prob", wide, "interview-a", nil)
	b, _ := Resolve("prob", wide, "interview-b", nil)
	c, _ := Resolve("other-prob", wide, "interview-a", nil)
	if a.Params["n"] == b.Params["n"] {
		t.Error("different interview ids resolved identically")
	}
	if a.Params["n"] == c.Params["n"] {
		t.Error("different problems resolved identically")
	}
}

func TestAddingAParamDoesNotShiftOthers(t *testing.T) {
	base, err := Resolve("pipeline-meltdown", specs(), "calm-bison-0731", nil)
	if err != nil {
		t.Fatal(err)
	}
	grown := specs()
	grown["aaa_new"] = content.ParamSpec{Type: content.Int, Min: intp(0), Max: intp(100)}
	after, err := Resolve("pipeline-meltdown", grown, "calm-bison-0731", nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range base.Params {
		if !reflect.DeepEqual(after.Params[name], v) {
			t.Errorf("param %q changed from %v to %v when an unrelated param was added", name, v, after.Params[name])
		}
	}
}

func TestDefaultsPinAndOverridesWin(t *testing.T) {
	r, err := Resolve("p", specs(), "i", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Params["region"] != "east" {
		t.Errorf("region = %v, want pinned default east", r.Params["region"])
	}
	if r.Params["team_name"] != "umbrella" {
		t.Errorf("team_name = %v, want default umbrella", r.Params["team_name"])
	}

	r, err = Resolve("p", specs(), "i", map[string]string{"region": "west", "scale": "7", "team_name": "aperture"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Params["region"] != "west" || r.Params["scale"] != 7 || r.Params["team_name"] != "aperture" {
		t.Errorf("overrides not applied: %v", r.Params)
	}
	if r.Overrides["region"] != "west" {
		t.Errorf("overrides not recorded in provenance: %v", r.Overrides)
	}
}

func TestResolvedValuesRespectSpecs(t *testing.T) {
	for _, interview := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		r, err := Resolve("p", specs(), interview, nil)
		if err != nil {
			t.Fatal(err)
		}
		pack := r.Params["fault_pack"].(string)
		if pack != "pack-a" && pack != "pack-b" && pack != "pack-c" {
			t.Errorf("fault_pack = %q not in spec", pack)
		}
		scale := r.Params["scale"].(int)
		if scale < 3 || scale > 9 {
			t.Errorf("scale = %d outside [3, 9]", scale)
		}
	}
}

func TestOverrideValidation(t *testing.T) {
	tests := []struct {
		overrides map[string]string
		want      string
	}{
		{map[string]string{"nope": "x"}, "no such parameter"},
		{map[string]string{"fault_pack": "pack-z"}, "is not one of"},
		{map[string]string{"scale": "two"}, "not an integer"},
		{map[string]string{"scale": "99"}, "outside"},
	}
	for _, tt := range tests {
		_, err := Resolve("p", specs(), "i", tt.overrides)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Resolve(%v) error = %v, want containing %q", tt.overrides, err, tt.want)
		}
	}
}

func TestStringWithoutDefaultFails(t *testing.T) {
	bad := map[string]content.ParamSpec{"name": {Type: content.String}}
	if _, err := Resolve("p", bad, "i", nil); err == nil {
		t.Error("string param without default resolved, want error")
	}
}

func TestProvenanceSerializes(t *testing.T) {
	r, err := Resolve("p", specs(), "calm-bison", map[string]string{"scale": "5"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"problem":"p"`, `"interview_id":"calm-bison"`, `"scale":5`, `"overrides"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("provenance JSON missing %s: %s", want, raw)
		}
	}
}

func TestRender(t *testing.T) {
	r, err := Resolve("p", specs(), "i", map[string]string{"scale": "5", "region": "west"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render("Team {{.team_name}} runs {{.scale}} nodes in {{.region}}.", r)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Team umbrella runs 5 nodes in west." {
		t.Errorf("Render = %q", out)
	}

	if _, err := Render("{{.not_a_param}}", r); err == nil {
		t.Error("undefined parameter rendered, want error")
	}
	if _, err := Render("{{.broken", r); err == nil {
		t.Error("unparseable template rendered, want error")
	}
}
