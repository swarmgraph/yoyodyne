package inventory

import (
	"strings"
	"testing"
)

// TestEveryTermSaysWhatItMeansAndWhatIsProposed holds the inventory to the
// shape its document promises: a meaning in one sentence, a decision, and the
// words to write instead or the reason it is kept.
func TestEveryTermSaysWhatItMeansAndWhatIsProposed(t *testing.T) {
	seen := make(map[string]bool)
	for _, term := range Inventory {
		if term.Term == "" || len(term.Match) == 0 {
			t.Errorf("%q: a term needs a name and something to match", term.Term)
		}
		if seen[term.Term] {
			t.Errorf("%q is listed twice", term.Term)
		}
		seen[term.Term] = true
		if strings.TrimSpace(term.Meaning) == "" || strings.TrimSpace(term.Words) == "" {
			t.Errorf("%q: a term needs its meaning and its proposal in words", term.Term)
		}
		switch term.Decision {
		case Replace, Register, Keep:
		default:
			t.Errorf("%q: decision %q is not one the document renders", term.Term, term.Decision)
		}
	}
	for _, word := range StopWords {
		if word.Meaning == "" || word.Words == "" {
			t.Errorf("stop word %q needs its meaning and its proposal", word.Word)
		}
	}
}

func TestCountMatchesSpellingsAndLeavesOrdinaryUsesOut(t *testing.T) {
	cases := []struct {
		name string
		term Term
		body string
		want int
	}{
		{"a stem counts its inflections", Term{Match: []string{"docket"}}, "the docket; docketed; Docket entries", 3},
		{"parts match however they are spaced", Term{Match: []string{"side thread"}}, "side thread, side-thread, sidethread, side\nthread", 4},
		{"only a word start matches", Term{Match: []string{"lease"}}, "release the lease", 1},
		{"exact holds the hyphen", Term{Match: []string{"carry-out"}, Exact: true}, "a carry-out; carry out her decision", 1},
		{"whole holds the word end", Term{Match: []string{"gate"}, Whole: true}, "a gate; gates; gated", 1},
		{"an exception at the same place is dropped", Term{Match: []string{"pull"}, Except: []string{"pull request"}}, "the next pull; a pull request; pull-request", 1},
	}
	for _, c := range cases {
		if got := Count(c.term, c.body); got != c.want {
			t.Errorf("%s: counted %d in %q, want %d", c.name, got, c.body, c.want)
		}
	}
}

// TestCollectReadsEachSurfaceAndNothingElse measures a small repository laid
// out the way this one is, so a surface that stopped being read, or a test file
// or a comment that started being counted, fails here.
