package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func runHome(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHomeUsage(stderr)
		return 2
	}
	switch args[0] {
	case "migrate":
		return migrateHome(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printHomeUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown home command %q\n\n", args[0])
		printHomeUsage(stderr)
		return 2
	}
}

// migrateHome moves a home laid out the way earlier builds kept it into the
// machine home, once nothing in it is in flight, and says what it moved and what
// it left behind. docs/designs/machine-home.md is the design, and
// docs/operations.md#moving-into-the-machine-home is the operator's account.
func migrateHome(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("home migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration, where there is one)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "home migrate does not accept positional arguments")
		return 2
	}
	fail := func(err error) int { return commandFailure(stdout, stderr, *jsonOutput, err) }

	plan, err := planHomeMigration(os.Getenv, os.UserHomeDir, runtime.GOOS)
	if err != nil {
		return fail(err)
	}
	// Both homes are read for what is in flight: a migration run again after one
	// that stopped part way has records on both sides.
	var inFlight []string
	for _, root := range uniqueHomes(plan.From, plan.To) {
		named, err := runstate.HomeInFlight(root)
		if err != nil {
			return fail(err)
		}
		inFlight = append(inFlight, named...)
	}
	if len(inFlight) > 0 {
		return fail(&runstate.InFlightError{Home: plan.From, InFlight: inFlight})
	}

	// The checkout this was run from, where a configuration is found for it, is
	// the one the binding of its product is written from.
	checkouts := map[string]string{}
	if resolved, err := loadConfiguration(*configPath); err == nil {
		if repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository); err == nil {
			checkouts[string(resolved.Config.Product.ID)] = repository
		}
	} else if *configPath != "" {
		return fail(err)
	}

	// Said before anything moves, on the error stream so a --json answer stays
	// the one document on standard output.
	fmt.Fprintln(stderr, handStartedDashboardWarning)
	now := time.Now()
	by := home.ProcessAccount()
	migration, err := home.MoveHome(home.MigrateOptions{
		From:              plan.From,
		To:                plan.To,
		ConfigurationHome: plan.ConfigurationHome,
		MachineFrom:       plan.MachineFrom,
		MachineTo:         plan.MachineTo,
		Checkouts:         checkouts,
		Now:               func() time.Time { return now },
		BoundBy:           by,
	})
	if err != nil {
		var conflict *home.ConflictError
		if !errors.As(err, &conflict) && (len(migration.Moved) > 0 || len(migration.LeftBehind) > 0) {
			reportMigration(stdout, stderr, *jsonOutput, migration)
		}
		return fail(err)
	}
	rewritten, left, err := runstate.RewriteMigratedWorktrees(plan.From, plan.To, migration.Products, now, by)
	migration.Moved = append(migration.Moved, rewritten...)
	migration.LeftBehind = append(migration.LeftBehind, left...)
	if err != nil {
		migration.LeftBehind = append(migration.LeftBehind, home.LeftBehind{What: "the recorded worktree paths", Path: plan.To,
			Why: err.Error() + "; running the migration again rewrites what is left", Failed: true})
	}
	code := reportMigration(stdout, stderr, *jsonOutput, migration)
	if code == 0 && migration.Failed() {
		return 1
	}
	return code
}

// handStartedDashboardWarning is what the migration says before it moves
// anything. A dashboard started by hand holds no lease the in-flight check can
// see, so one left running goes on reading the earlier home after its records
// have moved; until the dashboard runs under the supervisor (yoyodyne-ifd.414),
// stopping it is the person's to do.
const handStartedDashboardWarning = "before anything moves: stop any dashboard you started by hand with `yoyo dashboard`; " +
	"this command cannot see one, and one left running goes on reading the earlier home after its records have moved"

// homeMigrationPlan is which home moves where.
type homeMigrationPlan struct {
	From, To               string
	ConfigurationHome      string
	MachineFrom, MachineTo string
}

// planHomeMigration decides where the state is and where it goes. A home the
// platform's earlier default holds goes to ~/.yoyodyne, including the rest of
// one a migration that stopped part way left there; a home the machine file or
// XDG_STATE_HOME names is laid out the new way where it stands, because
// whoever named it asked for it there; and YOYODYNE_STATE_HOME is refused,
// because it is how the migration is deferred for as long as anybody likes.
func planHomeMigration(getenv func(string) string, homeDir func() (string, error), goos string) (homeMigrationPlan, error) {
	resolved, err := runstate.ResolveRoot(getenv, homeDir, goos)
	if err != nil {
		return homeMigrationPlan{}, err
	}
	if resolved.Origin == home.OriginEnvironment {
		return homeMigrationPlan{}, fmt.Errorf("%s names %s for this shell, which keeps the migration deferred; %s moved nothing, and unsetting it is what lets it run",
			home.StateHomeVariable, resolved.Path, home.MigrateCommand)
	}
	fresh, err := home.DefaultPath(homeDir)
	if err != nil {
		return homeMigrationPlan{}, err
	}
	plan := homeMigrationPlan{From: resolved.Path, To: resolved.Path}
	switch resolved.Origin {
	case home.OriginEarlierDefault:
		plan.To = fresh
	case home.OriginDefault:
		if earlier, err := home.EarlierDefault(getenv, homeDir, goos); err == nil && home.EarlierLayout(earlier) {
			plan.From = earlier
		}
	}
	if plan.ConfigurationHome, err = home.EarlierConfigurationHome(getenv, homeDir); err != nil {
		return homeMigrationPlan{}, err
	}
	if plan.MachineFrom, err = home.EarlierMachinePath(getenv, homeDir); err != nil {
		return homeMigrationPlan{}, err
	}
	if plan.MachineTo, err = home.MachinePath(homeDir); err != nil {
		return homeMigrationPlan{}, err
	}
	return plan, nil
}

func uniqueHomes(from, to string) []string {
	if home.SameDirectory(from, to) {
		return []string{from}
	}
	return []string{from, to}
}

func reportMigration(stdout, stderr io.Writer, jsonOutput bool, migration home.Migration) int {
	if jsonOutput {
		return writeJSON(stdout, stderr, migration)
	}
	fmt.Fprintln(stdout, describeMigration(migration))
	return 0
}

// describeMigration is what a person reads after a migration: where the state
// went, each thing moved, each thing left behind and why, and each binding
// written.
func describeMigration(migration home.Migration) string {
	var rendered strings.Builder
	if home.SameDirectory(migration.From, migration.To) {
		fmt.Fprintf(&rendered, "laid out the home %s the way the machine home keeps it\n", migration.To)
	} else {
		fmt.Fprintf(&rendered, "moved the harness's state from %s into the machine home %s\n", migration.From, migration.To)
	}
	if len(migration.Moved) == 0 {
		rendered.WriteString("moved: nothing; there was nothing left to move\n")
	} else {
		fmt.Fprintf(&rendered, "moved (%d):\n", len(migration.Moved))
		for _, moved := range migration.Moved {
			fmt.Fprintf(&rendered, "  %s: %s -> %s\n", moved.What, moved.From, moved.To)
		}
	}
	if len(migration.LeftBehind) == 0 {
		rendered.WriteString("left behind: nothing\n")
	} else {
		fmt.Fprintf(&rendered, "left behind (%d):\n", len(migration.LeftBehind))
		for _, left := range migration.LeftBehind {
			lead := ""
			if left.Failed {
				lead = "NOT MOVED: "
			}
			fmt.Fprintf(&rendered, "  %s%s, at %s: %s\n", lead, left.What, left.Path, left.Why)
		}
	}
	for _, bound := range migration.Bound {
		fmt.Fprintf(&rendered, "%s\n", bound)
	}
	if migration.Failed() {
		fmt.Fprintf(&rendered, "what is marked NOT MOVED is where it was; running `%s` again attempts it again", home.MigrateCommand)
	} else {
		rendered.WriteString("`yoyo start` starts the product on the machine home")
	}
	return rendered.String()
}

func printHomeUsage(writer io.Writer) {
	fmt.Fprintln(writer, strings.TrimSpace(`
Usage: yoyo home migrate [--config <path>] [--json]

Moves the harness's state from the home earlier builds kept it in — the folder
the platform keeps application data in, and ~/.config/yoyodyne for the machine
file and external configurations — into the machine home, ~/.yoyodyne: each product's
records into projects/<product id>/state/, its worktrees into
projects/<product id>/worktrees/ with Git told where each now is, each external
configuration into the project directory its product id names, and the
operator's hold, the provider accounts, and the logs to the top. It then says
what it moved and what it left behind.

It moves nothing while any run, conversation turn, or recurring pass is in
flight, and says which. Nothing else moves the state: a build that finds it in
the earlier home keeps using it there until this is run. It cannot see a
dashboard you started by hand with yoyo dashboard, so stop that first: one left
running goes on reading the earlier home after its records have moved. Do not create
~/.yoyodyne by hand first: a home that holds anything is the one every start
uses, and an empty one is a home with no state in it.

options:
  --config <path>   the configuration whose repository the binding is written
                    from (default: the nearest project configuration)
  --json            emit machine-readable JSON`))
}
