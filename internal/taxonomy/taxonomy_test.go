package taxonomy

import "testing"

func TestValidAcceptsEveryListedValue(t *testing.T) {
	for _, v := range Types {
		if !ValidType(v) {
			t.Errorf("ValidType(%q) = false", v)
		}
	}
	for _, v := range Flavors {
		if !ValidFlavor(v) {
			t.Errorf("ValidFlavor(%q) = false", v)
		}
	}
	for _, v := range Classes {
		if !ValidClass(v) {
			t.Errorf("ValidClass(%q) = false", v)
		}
	}
	for _, v := range Disciplines {
		if !ValidDiscipline(v) {
			t.Errorf("ValidDiscipline(%q) = false", v)
		}
	}
	for _, v := range Levels {
		if !ValidLevel(v) {
			t.Errorf("ValidLevel(%q) = false", v)
		}
	}
}

func TestValidRejectsUnknownAndEmpty(t *testing.T) {
	if ValidType("") || ValidType("quiz") {
		t.Error("ValidType accepted an unknown value")
	}
	if ValidFlavor("") || ValidFlavor("mainframe") {
		t.Error("ValidFlavor accepted an unknown value")
	}
	if ValidClass("") || ValidClass("trivia") {
		t.Error("ValidClass accepted an unknown value")
	}
	if ValidDiscipline("") || ValidDiscipline("astrology") {
		t.Error("ValidDiscipline accepted an unknown value")
	}
	if ValidLevel("") || ValidLevel("intern") {
		t.Error("ValidLevel accepted an unknown value")
	}
}

func TestNoDuplicateValuesAcrossLists(t *testing.T) {
	seen := map[string]string{}
	add := func(list string, v string) {
		if prev, ok := seen[v]; ok {
			t.Errorf("value %q appears in both %s and %s", v, prev, list)
		}
		seen[v] = list
	}
	for _, v := range Types {
		add("Types", string(v))
	}
	for _, v := range Flavors {
		add("Flavors", string(v))
	}
	for _, v := range Classes {
		add("Classes", string(v))
	}
	for _, v := range Disciplines {
		add("Disciplines", string(v))
	}
	for _, v := range Levels {
		add("Levels", string(v))
	}
}
