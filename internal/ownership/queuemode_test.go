package ownership

import "testing"

func TestQueueModeHoldIsTheOperatorsOnlyForARepositorySetting(t *testing.T) {
	for _, tc := range []struct {
		remedy *PersonOnlyRemedy
		want   Mover
	}{
		{want: MoverDevelopmentManager},
		{remedy: &PersonOnlyRemedy{Step: "publish through pull requests"}, want: MoverDevelopmentManager},
		{remedy: &PersonOnlyRemedy{Reason: PersonProtectedFile, Target: "internal/queuemode/queuemode.go", Step: "teach the adapter to name the combined commit"}, want: MoverDevelopmentManager},
		{remedy: &PersonOnlyRemedy{Reason: PersonRepositorySetting, Target: "the merge queue requirement on main", Step: "remove the merge queue requirement from main's protection rules"}, want: MoverOperator},
	} {
		if got := ResolveQueueModeHold(tc.remedy); got != tc.want {
			t.Fatalf("ResolveQueueModeHold(%+v) = %s, want %s", tc.remedy, got, tc.want)
		}
	}
}
