package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// driftingProject is a project whose template has since improved one value it
// never edited: the baseline says the template supplied "sonnet", the project
// still says "sonnet", and the template in this executable says "opus".
//
// It returns the configuration path. The project extends nothing, because a
// project that inherits has no gap for a baseline to close.
func driftingProject(t *testing.T, improved string) string {
	t.Helper()

	lock, err := config.NewLock(config.BuiltinV1)
	if err != nil {
		t.Fatalf("NewLock() error = %v", err)
	}
	if _, recorded := lock.Values[improved]; !recorded {
		t.Fatalf("the baseline records no %s, so nothing here would be an improvement", improved)
	}
	lock.Values[improved] = "sonnet"

	path := writeProjectConfig(t, `version: 1
product:
  id: drifting
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
checks:
  - make test
agents:
  developer:
    role: developer
    backend: claude-code
    model: sonnet
  reviewer:
    role: reviewer
    backend: claude-code
    model: opus
`)
	if err := os.WriteFile(config.LockPath(path), lock.Render(), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	return path
}

// The operator's decision: the available class speaks without being asked, on a
// command already being run, and changes nothing about what that command
// reported.
func TestConfigValidateSpeaksAnAvailableImprovementWithoutBeingAsked(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, want the exit code unchanged by a notice; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "configuration valid") {
		t.Fatalf("stdout = %q, want the validation the operator asked for", stdout.String())
	}
	// On standard error, beside the answer, rather than mixed into it: the
	// notice is an aside about a configuration that is valid.
	notice := stderr.String()
	for _, want := range []string{"agents.developer.model", config.BuiltinV1, "yoyo config drift"} {
		if !strings.Contains(notice, want) {
			t.Errorf("stderr = %q, want it to name %q", notice, want)
		}
	}
}

// Silence is what makes speaking on every run acceptable. A project that has
// nothing available says nothing at all, and a project that never recorded a
// baseline is not told about it on every invocation either.
func TestConfigValidateIsSilentWhenThereIsNothingToSay(t *testing.T) {
	// Not parallel: it asserts what this command says, and clearing the
	// provider keys is what keeps a key exported on the machine running the
	// tests from being one more thing said.
	withoutProviderKeys(t)

	withBaseline := driftingProject(t, "agents.developer.model")
	// Adopt it by hand, which is what the report is for: the project and the
	// template now agree, so there is nothing to say.
	current, err := os.ReadFile(withBaseline)
	if err != nil {
		t.Fatalf("read configuration: %v", err)
	}
	adopted := strings.Replace(string(current), "    model: sonnet", "    model: opus", 1)
	if err := os.WriteFile(withBaseline, []byte(adopted), 0o600); err != nil {
		t.Fatalf("write configuration: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", withBaseline}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want silence once the improvement is taken", stderr.String())
	}

	// And a project with no baseline at all.
	unbaselined := writeProjectConfig(t, portableConfig)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "validate", "--config", unbaselined}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want silence where there is no baseline to compare against", stderr.String())
	}
}

// The same derivation reaches the machine-readable form, so anything automating
// a repair reads it structurally rather than parsing the line.
func TestConfigValidateCarriesTheComparisonInItsJSON(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", path, "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var report struct {
		Status string       `json:"status"`
		Drift  config.Drift `json:"drift"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout.String())
	}
	if report.Status != "valid" {
		t.Fatalf("status = %q, want a valid configuration", report.Status)
	}
	available := report.Drift.Available()
	if len(available) != 1 || available[0].Key != "agents.developer.model" {
		t.Fatalf("available = %+v, want the one improved value", available)
	}
	if available[0].Bundle != "opus" || available[0].Yours != "sonnet" {
		t.Errorf("available = %+v, want both sides carried", available[0])
	}
}

// doctor says the same thing the same way. Two surfaces disagreeing about
// whether a project is current is a disagreement only the operator could settle.
func TestDoctorSpeaksTheSameNoticeAsConfigValidate(t *testing.T) {
	// Not parallel: it asserts what this command says, and clearing the
	// provider keys is what keeps a key exported on the machine running the
	// tests from being one more thing said.
	withoutProviderKeys(t)

	path := driftingProject(t, "agents.developer.model")

	var validateOut, validateErr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", path}, &validateOut, &validateErr, "test"); code != 0 {
		t.Fatalf("config validate code = %d", code)
	}
	var doctorOut, doctorErr bytes.Buffer
	// doctor exits 1 here: this temporary project has no repository, no tracker,
	// and no authenticated provider. What is asserted is the notice beside that
	// diagnosis, which the diagnosis neither suppresses nor changes. That the
	// exit code is the same one either way is
	// TestDoctorsExitCodeIsTheSameWithAndWithoutANotice below.
	Run([]string{"doctor", "--config", path, "--quiet"}, &doctorOut, &doctorErr, "test")

	notice := strings.TrimSpace(validateErr.String())
	if notice == "" {
		t.Fatal("config validate said nothing, so there is nothing to compare doctor against")
	}
	if !strings.Contains(doctorErr.String(), notice) {
		t.Errorf("doctor stderr = %q, want the same line config validate printed: %q", doctorErr.String(), notice)
	}
	// The report itself is untouched: a notice is not a finding, and it does not
	// appear among the things that would stop work running.
	if strings.Contains(doctorOut.String(), "config drift") {
		t.Errorf("doctor stdout = %q, want the notice kept out of the diagnosis", doctorOut.String())
	}
}

func TestDoctorCarriesTheNoticeBesideItsJSONWithoutDisturbingIt(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")

	var stdout, stderr bytes.Buffer
	Run([]string{"doctor", "--config", path, "--json"}, &stdout, &stderr, "test")
	var report struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout.String())
	}
	if report.SchemaVersion == 0 {
		t.Fatalf("stdout = %q, want the report unchanged on standard output", stdout.String())
	}
	if !strings.Contains(stderr.String(), "agents.developer.model") {
		t.Errorf("stderr = %q, want the notice beside the machine-readable report", stderr.String())
	}
}

// The command the notices point at has to exist and show what the notice
// deliberately leaves out.
func TestConfigDriftShowsBothSidesAndTheClassesTheNoticeStaysQuietAbout(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")
	// A value this project moved and the template did not, which the notice
	// never mentions and this report names as the project's own.
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read configuration: %v", err)
	}
	edited := strings.Replace(string(current), "    model: opus", "    model: haiku", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("write configuration: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "drift", "--config", path, "--all"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	report := stdout.String()
	for _, want := range []string{"available", "agents.developer.model", "sonnet", "opus", "yours", "agents.reviewer.model", "haiku"} {
		if !strings.Contains(report, want) {
			t.Errorf("stdout = %q, want it to carry %q", report, want)
		}
	}
}

// A project that predates the baseline keeps working and is told what it does
// not have. Refusing would break it over a file that decides nothing about how
// it runs.
func TestConfigDriftAnswersUnknownForAProjectWithNoBaseline(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "drift", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, want a project with no baseline to be reported rather than refused; stderr = %q", code, stderr.String())
	}
	if report := stdout.String(); !strings.Contains(report, "no baseline") {
		t.Errorf("stdout = %q, want it to say there is no baseline", report)
	}
}

// A baseline that is on disk and cannot be used is a different situation from
// one that was never written, and the operator can see the file. Reporting it as
// missing would tell them the opposite of the facts, and would point them at a
// remedy for the wrong problem.
func TestConfigDriftTellsAnUnusableBaselineApartFromAMissingOne(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)
	// A baseline whose revision does not digest its values: what a hand edit
	// leaves behind.
	if err := os.WriteFile(config.LockPath(path),
		[]byte("version: 1\nbundle: builtin:v1\nrevision: bnd-000000000000\nvalues:\n  agents.developer.model: \"opus\"\n"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "drift", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, want an unusable baseline reported rather than refused; stderr = %q", code, stderr.String())
	}
	report := stdout.String()
	if strings.Contains(report, "no baseline") {
		t.Errorf("stdout = %q, want a baseline that is present and refused not called missing", report)
	}
	if !strings.Contains(report, "unusable baseline") {
		t.Errorf("stdout = %q, want the headline to say the baseline cannot be used", report)
	}
	// The remedy for a corrupt record is the copy in version control, not a
	// regeneration that takes the configuration and the personas with it.
	if !strings.Contains(report, "version control") {
		t.Errorf("stdout = %q, want the non-destructive way back named first", report)
	}
	if strings.Contains(report, "init --force") && !strings.Contains(report, "every persona") {
		t.Errorf("stdout = %q, names `yoyo init --force` without saying what it overwrites", report)
	}
}

// The baseline is written where the configuration is, by the command that
// materializes a project, because a project that never recorded one has nothing
// to compare and this is the only moment the record is true by construction.
func TestInitWritesTheBaselineBesideTheConfigurationItGenerated(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", directory, "--product", "baselined"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	lockPath := filepath.Join(directory, config.DirectoryName, config.LockFileName)
	if !strings.Contains(stdout.String(), lockPath) {
		t.Errorf("stdout = %q, want the baseline reported among the files written", stdout.String())
	}
	content, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	lock, err := config.DecodeLock(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("DecodeLock() error = %v", err)
	}
	if lock.Bundle != config.BuiltinV1 {
		t.Errorf("bundle = %q, want %q", lock.Bundle, config.BuiltinV1)
	}
	// A project generated a moment ago has nothing available, which is what
	// makes the notice's silence mean something.
	resolved, err := config.LoadResolved(filepath.Join(directory, config.DirectoryName, config.FileName))
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	drift, unknown := config.ReadDrift(resolved)
	if !drift.Current() {
		t.Fatalf("a project generated a moment ago is not current: %s", unknown.Reason)
	}
	if notice := drift.Notice(); notice != "" {
		t.Errorf("Notice() = %q, want silence", notice)
	}

	// The property the whole comparison rests on, and the one nothing above
	// would catch: every value the baseline recorded is the value the generated
	// project actually holds. A persona copied into the project that did not
	// digest to what the baseline recorded would be reported as the operator's
	// own for ever after, so a later improvement to it would reach them as a
	// conflict they never made rather than as an improvement they could take.
	values := config.ProjectValues(resolved.Config, resolved.Path)
	for key, recorded := range lock.Values {
		if got := values[key]; got != recorded {
			t.Errorf("%s: the generated project holds %q and the baseline recorded %q", key, got, recorded)
		}
	}
	if _, digested := lock.Values["agents.developer.persona.text"]; !digested {
		t.Error("the baseline recorded no persona text, so the loop above proved nothing about the personas")
	}
}

// generatedProject is a project as `yoyo init` leaves it, with its baseline
// removed and its configuration and one persona edited afterwards: a project
// that predates the record, which is every project that exists today.
func generatedProject(t *testing.T) (directory, configPath, persona string) {
	t.Helper()

	directory = t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", directory, "--product", "predating"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("init code = %d, stderr = %q", code, stderr.String())
	}
	configPath = filepath.Join(directory, config.DirectoryName, config.FileName)
	persona = filepath.Join(directory, config.DirectoryName, "personas", "developer.md")
	if err := os.Remove(config.LockPath(configPath)); err != nil {
		t.Fatalf("remove the baseline: %v", err)
	}
	// The edits the operator would lose to a regeneration, which is what makes
	// the non-destructive route the only usable one here.
	existing, err := os.ReadFile(persona)
	if err != nil {
		t.Fatalf("read the persona: %v", err)
	}
	if err := os.WriteFile(persona, append(existing, "\n\nSomething this project added.\n"...), 0o600); err != nil {
		t.Fatalf("edit the persona: %v", err)
	}
	return directory, configPath, persona
}

// digestOf is every file under the configuration directory except the baseline,
// so a command that claims to write one file can be held to it.
func digestOf(t *testing.T, directory string) map[string]string {
	t.Helper()
	contents := map[string]string{}
	root := filepath.Join(directory, config.DirectoryName)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Base(path) == config.LockFileName {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		contents[path] = string(body)
		return nil
	}); err != nil {
		t.Fatalf("read the configuration directory: %v", err)
	}
	if len(contents) == 0 {
		t.Fatal("nothing was read, so comparing it proves nothing")
	}
	return contents
}

// The whole reason this command exists. Before it, a baseline could only arrive
// through `yoyo init`, which rewrites the configuration and every persona from
// the template -- so a project that predated the record could obtain one only by
// discarding the edits the comparison exists to protect, and therefore never saw
// a notice at all.
func TestConfigBaselineGivesAProjectWithNoneOneWithoutTouchingAnythingElse(t *testing.T) {
	t.Parallel()

	directory, configPath, persona := generatedProject(t)
	before := digestOf(t, directory)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "baseline", "--config", configPath}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	// Every file but the baseline is byte-identical, which is the promise.
	after := digestOf(t, directory)
	if len(before) != len(after) {
		t.Fatalf("the configuration directory held %d files and now holds %d", len(before), len(after))
	}
	for path, was := range before {
		if after[path] != was {
			t.Errorf("%s was rewritten by a command that writes only the baseline", path)
		}
	}
	if !strings.Contains(stdout.String(), config.LockFileName) {
		t.Errorf("stdout = %q, want the file it wrote named", stdout.String())
	}

	// And the project now compares, which is what makes a notice reachable here
	// at all.
	resolved, err := config.LoadResolved(configPath)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	drift, unknown := config.ReadDrift(resolved)
	if !drift.Known {
		t.Fatalf("the project still does not compare: %s", unknown.Reason)
	}
	// The persona this project edited is the project's, not an improvement
	// waiting to be taken, and the unprompted line stays silent about it.
	if got := driftClassOf(drift, "agents.developer.persona.text"); got != config.ClassYours {
		t.Errorf("the edited persona = %q, want %q", got, config.ClassYours)
	}
	if notice := drift.Notice(); notice != "" {
		t.Errorf("Notice() = %q, want silence for a project that has just levelled with its template", notice)
	}
	if _, err := os.Stat(persona); err != nil {
		t.Errorf("the persona is gone: %v", err)
	}

	// From here the notices work exactly as they do for a generated project: when
	// the template moves a value this one never edited, it is spoken. Arranged by
	// moving the project and its baseline together away from the template, which
	// is the shape of a project that recorded a baseline and was then overtaken.
	reviewer := filepath.Join(directory, config.DirectoryName, "personas", "reviewer.md")
	asItWas, err := os.ReadFile(reviewer)
	if err != nil {
		t.Fatalf("read the reviewer persona: %v", err)
	}
	if err := os.WriteFile(reviewer, append(asItWas, "\n\nAs the template used to word it.\n"...), 0o600); err != nil {
		t.Fatalf("edit the reviewer persona: %v", err)
	}
	overtaken, err := config.LoadResolved(configPath)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	lock, err := config.NewLock(config.BuiltinV1)
	if err != nil {
		t.Fatalf("NewLock() error = %v", err)
	}
	lock.Values["agents.reviewer.persona.text"] = config.ProjectValues(overtaken.Config, configPath)["agents.reviewer.persona.text"]
	if err := os.WriteFile(config.LockPath(configPath), lock.Render(), 0o600); err != nil {
		t.Fatalf("write the moved baseline: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "validate", "--config", configPath}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("config validate code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "agents.reviewer.persona.text") {
		t.Errorf("stderr = %q, want the improvement spoken unprompted in a project that recorded its baseline this way", stderr.String())
	}
	// And still nothing about the persona this project edited itself.
	if strings.Contains(stderr.String(), "agents.developer.persona.text") {
		t.Errorf("stderr = %q, want the project's own edit still not spoken", stderr.String())
	}
}

// A baseline already there knows something this command cannot reconstruct, so
// replacing it is never the quiet default.
func TestConfigBaselineWillNotReplaceARecordWithoutBeingTold(t *testing.T) {
	t.Parallel()

	_, configPath, _ := generatedProject(t)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "baseline", "--config", configPath}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "baseline", "--config", configPath}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want a baseline already there refused", code)
	}
	if !strings.Contains(stderr.String(), "--force") {
		t.Errorf("stderr = %q, want the refusal to name what would replace it", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "baseline", "--config", configPath, "--force"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d with --force, stderr = %q", code, stderr.String())
	}
}

// An unusable baseline is the other case an existing project can be in, and the
// one the previous advice sent to a destructive regeneration.
func TestConfigBaselineReplacesAnUnusableRecordOnlyWhenTold(t *testing.T) {
	t.Parallel()

	_, configPath, _ := generatedProject(t)
	if err := os.WriteFile(config.LockPath(configPath),
		[]byte("version: 1\nbundle: builtin:v1\nrevision: bnd-000000000000\nvalues:\n  agents.developer.model: \"opus\"\n"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "baseline", "--config", configPath}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want an unusable baseline refused rather than silently replaced", code)
	}
	// Version control first: it has what this command cannot put back.
	if !strings.Contains(stderr.String(), "version control") {
		t.Errorf("stderr = %q, want the copy that knows more named first", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "baseline", "--config", configPath, "--force"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d with --force, stderr = %q", code, stderr.String())
	}
	resolved, err := config.LoadResolved(configPath)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if drift, unknown := config.ReadDrift(resolved); !drift.Known {
		t.Fatalf("the replaced baseline still does not compare: %s", unknown.Reason)
	}
}

// The report an operator reaches with no baseline has to name the command that
// writes one, and must not send them to the one that rewrites everything else
// along with it.
func TestConfigDriftSendsAMissingBaselineToTheCommandThatWritesOnlyIt(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "drift", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	report := stdout.String()
	if !strings.Contains(report, "yoyo config baseline") {
		t.Errorf("stdout = %q, want the command that writes a baseline named", report)
	}
	if strings.Contains(report, "init --force") {
		t.Errorf("stdout = %q, want a project with no baseline not sent to a regeneration", report)
	}
	// What recording one now cannot do is worth saying while they decide.
	if !strings.Contains(report, "counted as yours") {
		t.Errorf("stdout = %q, want the cost of levelling now stated", report)
	}
}

// A machine reader has to be able to tell the two unknowns apart wherever the
// comparison is reported, not only where a person reads it.
func TestConfigValidateJSONTellsAMissingBaselineFromAnUnusableOne(t *testing.T) {
	t.Parallel()

	read := func(t *testing.T, path string) struct {
		Drift   config.Drift   `json:"drift"`
		Unknown config.Unknown `json:"unknown"`
	} {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"config", "validate", "--config", path, "--json"}, &stdout, &stderr, "test"); code != 0 {
			t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
		}
		var report struct {
			Drift   config.Drift   `json:"drift"`
			Unknown config.Unknown `json:"unknown"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout.String())
		}
		return report
	}

	missing := writeProjectConfig(t, portableConfig)
	absent := read(t, missing)
	if absent.Drift.Known || !absent.Unknown.Absent {
		t.Errorf("a project with no baseline reported %+v, want an absence", absent.Unknown)
	}

	unusable := writeProjectConfig(t, portableConfig)
	if err := os.WriteFile(config.LockPath(unusable),
		[]byte("version: 1\nbundle: builtin:v1\nrevision: bnd-000000000000\nvalues:\n  agents.developer.model: \"opus\"\n"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	refused := read(t, unusable)
	if refused.Drift.Known {
		t.Fatalf("an edited baseline compared anyway: %+v", refused.Drift)
	}
	if refused.Unknown.Absent {
		t.Errorf("a baseline that is present and refused reported as absent: %+v", refused.Unknown)
	}
	if refused.Unknown.Reason == "" {
		t.Error("the machine-readable form carries no reason, so the two unknowns are still indistinguishable")
	}
}

func driftClassOf(drift config.Drift, key string) config.Class {
	for _, value := range drift.Values {
		if value.Key == key {
			return value.Class
		}
	}
	return ""
}

// The operator's condition on the whole notice: it never changes what the
// command it rides on reported. `config validate` is asserted where it is
// printed, because it exits 0 there either way; doctor's exit code is whatever
// the installation deserves, so what has to be pinned is that it is the same
// number with a notice and without one.
//
// The same project answers both times and only the baseline moves between them,
// so nothing but the notice differs: any change in the code would be the notice
// having moved it.
func TestDoctorsExitCodeIsTheSameWithAndWithoutANotice(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")

	var withOut, withErr bytes.Buffer
	speaking := Run([]string{"doctor", "--config", path, "--quiet"}, &withOut, &withErr, "test")
	if !strings.Contains(withErr.String(), "agents.developer.model") {
		t.Fatalf("doctor said nothing about the improvement (stderr = %q), so the comparison below is empty", withErr.String())
	}

	// Level the project with its template, which is the only difference: the
	// baseline now records what the bundle actually supplies, so there is
	// nothing available and nothing to say.
	level, err := config.NewLock(config.BuiltinV1)
	if err != nil {
		t.Fatalf("NewLock() error = %v", err)
	}
	if err := os.WriteFile(config.LockPath(path), level.Render(), 0o600); err != nil {
		t.Fatalf("write the levelled baseline: %v", err)
	}
	var silentOut, silentErr bytes.Buffer
	silent := Run([]string{"doctor", "--config", path, "--quiet"}, &silentOut, &silentErr, "test")
	if strings.Contains(silentErr.String(), "agents.developer.model") {
		t.Fatalf("doctor still speaks with nothing available (stderr = %q)", silentErr.String())
	}

	if speaking != silent {
		t.Errorf("doctor exited %d with a notice and %d without one; the notice must not move the exit code", speaking, silent)
	}
	// And the diagnosis itself is the same, so the notice is not reaching the
	// report by another route either.
	if withOut.String() != silentOut.String() {
		t.Errorf("doctor's report differs with a notice present:\nwith:\n%s\nwithout:\n%s", withOut.String(), silentOut.String())
	}
}

// The report the notice points at shows both classes the notice stays quiet
// about, without a flag. Hiding `yours` behind --all would leave the on-demand
// command as quiet about it as the line that sent you here.
func TestConfigDriftShowsWhatTheNoticeStaysQuietAboutWithoutAFlag(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")
	// agents.reviewer.model is "opus" in this project and in the template; move
	// the project's so it reads as the project's own.
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read configuration: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(current), "    model: opus", "    model: haiku", 1)), 0o600); err != nil {
		t.Fatalf("write configuration: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "drift", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	report := stdout.String()
	for _, want := range []string{"available", "agents.developer.model", "yours", "agents.reviewer.model", "haiku"} {
		if !strings.Contains(report, want) {
			t.Errorf("stdout = %q, want %q printed without --all", report, want)
		}
	}
	// The fourth class is what --all adds, and what it holds back by default.
	if strings.Contains(report, "unchanged") {
		t.Errorf("stdout = %q, want the values neither side moved held back until --all", report)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "drift", "--config", path, "--all"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() --all code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "unchanged") {
		t.Errorf("stdout = %q, want --all to add the values neither side moved", stdout.String())
	}
}

// projectWithAnOlderDeliveryCopy is a project keeping its own copy of the
// delivery definition, made before this build routed a replay conflict back to
// its developer: the shipped file with that one transition taken out.
func projectWithAnOlderDeliveryCopy(t *testing.T) string {
	t.Helper()
	shipped, err := os.ReadFile(filepath.Join("..", "orchestrator", "delivery.yaml"))
	if err != nil {
		t.Fatalf("read the shipped delivery definition: %v", err)
	}
	const line = "      reconciling: develop\n"
	if !bytes.Contains(shipped, []byte(line)) {
		t.Fatalf("the shipped delivery definition does not route integrate -> reconciling to develop")
	}
	path := writeProjectConfig(t, portableConfig)
	copied := filepath.Join(filepath.Dir(path), "workflows", "delivery.yaml")
	if err := os.MkdirAll(filepath.Dir(copied), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(copied, bytes.Replace(shipped, []byte(line), nil, 1), 0o600); err != nil {
		t.Fatalf("write the project's copy: %v", err)
	}
	return path
}

// A transition the build added to a delivery definition reaches a project that
// keeps its own copy only by being reported: `config drift` names it as
// available with both sides, and the commands already being run say it in one
// line, naming the transition rather than that two definitions differ.
func TestConfigDriftReportsATransitionTheBuildRoutesAndTheProjectsCopyDoesNot(t *testing.T) {
	t.Parallel()

	path := projectWithAnOlderDeliveryCopy(t)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "drift", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("config drift code = %d, stderr = %q", code, stderr.String())
	}
	report := stdout.String()
	for _, want := range []string{"workflow delivery", "available", "integrate -> reconciling", "yours: (nothing)", "build: develop"} {
		if !strings.Contains(report, want) {
			t.Errorf("config drift stdout = %q, want it to carry %q", report, want)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "drift", "--config", path, "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("config drift --json code = %d, stderr = %q", code, stderr.String())
	}
	var decoded struct {
		Workflows []struct {
			Workflow string `json:"workflow"`
			Changes  []struct {
				Key   string `json:"key"`
				Class string `json:"class"`
				Build string `json:"build"`
			} `json:"changes"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("decode config drift --json: %v\n%s", err, stdout.String())
	}
	if len(decoded.Workflows) != 1 || len(decoded.Workflows[0].Changes) != 1 ||
		decoded.Workflows[0].Changes[0].Key != "states.integrate.on.reconciling" ||
		decoded.Workflows[0].Changes[0].Class != string(config.ClassAvailable) {
		t.Errorf("config drift --json workflows = %+v, want the one available transition", decoded.Workflows)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("config validate code = %d, stderr = %q", code, stderr.String())
	}
	if notice := stderr.String(); !strings.Contains(notice, "integrate -> reconciling") || !strings.Contains(notice, "yoyo config drift") {
		t.Errorf("config validate stderr = %q, want the notice naming the transition", notice)
	}
}
