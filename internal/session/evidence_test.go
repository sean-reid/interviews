package session

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The bundle syncs to the bucket every two minutes while the interview is
// still running, so a token inside it is a live credential. The old test
// named the two fields redacted() cleared, which is the implementation
// restated: it could not notice a third field arriving, and one had. This
// asserts the property instead, over whatever fields Info happens to have.
func TestRedactionLeavesNoCredentialInTheBundle(t *testing.T) {
	secret := func(field string) string { return "SECRET-" + field }
	var info Info
	v := reflect.ValueOf(&info).Elem()
	var planted []string
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		if v.Field(i).Kind() != reflect.String || !credentialField(name) {
			continue
		}
		v.Field(i).SetString(secret(name))
		planted = append(planted, name)
	}
	if len(planted) < 5 {
		t.Fatalf("planted %v; Info should carry at least the two tokens, two urls and the app pair", planted)
	}

	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	// Positive control: the values are really in the unredacted file, so a
	// clean result below means redaction worked rather than that the search
	// cannot find anything.
	for _, name := range planted {
		if !bytes.Contains(raw, []byte(secret(name))) {
			t.Fatalf("%s never reached the file; this test cannot prove redaction", name)
		}
	}

	out, err := redacted(InfoFile, raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range planted {
		if bytes.Contains(out, []byte(secret(name))) {
			t.Errorf("%s survives into the evidence bundle", name)
		}
	}
}

// credentialField names the Info fields that are secrets or that embed one.
func credentialField(name string) bool {
	return strings.Contains(name, "Token") || strings.Contains(name, "URL")
}
