package sweep

import (
	"strings"
	"testing"
)

// An audit records which standing goals a closed item was checked against and
// what was found, and a violation carries the correction admitted for it or
// says it is deferred: a broken goal with neither is a violation nobody acted on.
func TestAnAuditIsHeldToItsContract(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		block string
		want  string
	}{
		"met":               {`{"item":"x-1","goals":["the plain-language goal"],"finding":"met"}`, ""},
		"broken, corrected": {`{"item":"x-1","goals":["the plain-language goal"],"finding":"broken","detail":"a code word","correction":"x-9"}`, ""},
		"broken, deferred":  {`{"item":"x-1","goals":["the plain-language goal"],"finding":"broken","detail":"a code word","deferred":true}`, ""},
		"broken, neither":   {`{"item":"x-1","goals":["the plain-language goal"],"finding":"broken","detail":"a code word"}`, "names the correction admitted or widened for it, or says it is deferred"},
		"broken, both":      {`{"item":"x-1","goals":["g"],"finding":"broken","detail":"a code word","correction":"x-9","deferred":true}`, "and not both"},
		"broken, no detail": {`{"item":"x-1","goals":["g"],"finding":"broken","correction":"x-9"}`, "says where in detail"},
		"met, corrected":    {`{"item":"x-1","goals":["g"],"finding":"met","correction":"x-9"}`, "nothing to correct or defer"},
		"no goals":          {`{"item":"x-1","goals":[],"finding":"met"}`, "names none"},
		"unknown finding":   {`{"item":"x-1","goals":["g"],"finding":"fine"}`, `finding "fine"`},
	} {
		_, err := Decode(`{"status":"complete","summary":"audited","audits":[` + test.block + `]}`)
		switch {
		case test.want == "" && err != nil:
			t.Errorf("%s: Decode() error = %v, want the audit accepted", name, err)
		case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
			t.Errorf("%s: Decode() error = %v, want %q", name, err, test.want)
		}
	}
}

// A pass's audits accumulate across its turns, and the corrections it says it
// admitted are counted once each however many audits name one.
func TestAuditsAccumulateAcrossTurnsAndCorrectionsAreCountedOnce(t *testing.T) {
	t.Parallel()

	first := Result{Status: StatusMore, Summary: "half", Audits: []Audit{{Item: "x-1", Goals: []string{"g"}, Finding: AuditBroken, Detail: "d", Correction: "x-9"}}}
	second := Result{Status: StatusComplete, Summary: "done", Audits: []Audit{
		{Item: "x-2", Goals: []string{"g"}, Finding: AuditBroken, Detail: "d", Correction: "x-9"},
		{Item: "x-3", Goals: []string{"g"}, Finding: AuditMet},
	}}
	merged := first.Merge(second)
	if len(merged.Audits) != 3 || merged.Corrections() != 1 {
		t.Fatalf("merged = %+v, want three audits naming one correction", merged.Audits)
	}
	if err := merged.Validate(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(AuditContract(), `"audits"`) || !strings.Contains(AuditContract(), "at most three corrections") {
		t.Errorf("contract = %q, want the field and the bound stated", AuditContract())
	}
}
