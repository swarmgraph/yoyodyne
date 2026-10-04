package ownership

import "testing"

func TestPassFailureOwnershipHasOnlyOneReasonToNameTheOperator(t *testing.T) {
	for _, agent := range []string{"factory-flow-pm", ""} {
		for _, step := range []string{"", "renew the provider credential by hand"} {
			answer := ResolvePassFailure(agent, step)
			if (answer.Mover == MoverOperator) != (step != "") || answer.PersonStep != step || answer.Fallback != (agent == "") {
				t.Fatalf("ownership = %+v", answer)
			}
		}
	}
}
