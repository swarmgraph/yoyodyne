package domain

import "testing"

func TestAWorkItemOriginSaysWhoAskedAndOnWhoseBehalf(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		origin     WorkItemOrigin
		askedBy    string
		onBehalfOf string
		describe   string
	}{
		{WorkItemOrigin{Asker: AskerOperator, AdmittedBy: RoleProductManager}, "operator", "operator",
			"asked for by the operator, admitted by the Lead Product Manager"},
		{WorkItemOrigin{Asker: AskerOperator, AdmittedBy: RoleProductManager, Directive: "directive-1"}, "operator", "operator",
			"asked for by the operator, admitted by the Lead Product Manager, in answer to their directive directive-1"},
		{WorkItemOrigin{Asker: AskerReport, AdmittedBy: RoleProgramManager, Report: "report-1", ReportedBy: RoleDeveloper, Directive: "directive-1"}, "developer", "operator",
			"asked for by report report-1, filed by the developer, admitted by the program manager, on the operator's behalf in answer to directive directive-1"},
		{WorkItemOrigin{Asker: AskerSweep, AdmittedBy: RoleProductManager}, "product-manager", "product-manager",
			"decided on in the Lead Product Manager's own pass"},
		{WorkItemOrigin{Asker: AskerHarness}, "harness", "harness", "filed by the harness itself"},
		{WorkItemOrigin{}, "", "", "not recorded; the item was admitted before origins were recorded"},
	} {
		if got := test.origin.AskedBy(); got != test.askedBy {
			t.Errorf("%#v AskedBy() = %q, want %q", test.origin, got, test.askedBy)
		}
		if got := test.origin.OnBehalfOf(); got != test.onBehalfOf {
			t.Errorf("%#v OnBehalfOf() = %q, want %q", test.origin, got, test.onBehalfOf)
		}
		if got := test.origin.Describe(); got != test.describe {
			t.Errorf("%#v Describe() = %q, want %q", test.origin, got, test.describe)
		}
		if test.origin.Known() {
			if err := test.origin.Validate(); err != nil {
				t.Errorf("%#v Validate() = %v, want it valid", test.origin, err)
			}
		}
	}
}

func TestAnOriginThatCouldNotDescribeAnAdmissionIsRefused(t *testing.T) {
	t.Parallel()

	for _, origin := range []WorkItemOrigin{
		{},
		{Asker: "colleague", AdmittedBy: RoleProductManager},
		{Asker: AskerOperator},
		{Asker: AskerOperator, AdmittedBy: "night-shift"},
		{Asker: AskerReport, AdmittedBy: RoleProductManager},
		{Asker: AskerReport, AdmittedBy: RoleProductManager, Report: "report-1"},
		{Asker: AskerSweep, AdmittedBy: RoleProductManager, Report: "report-1", ReportedBy: RoleDeveloper},
	} {
		if err := origin.Validate(); err == nil {
			t.Errorf("%#v Validate() = nil, want it refused", origin)
		}
	}
}
