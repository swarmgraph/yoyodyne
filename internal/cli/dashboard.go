package cli

// Serving the read model to a browser on this machine.
//
// `yoyo dashboard` is the standalone command the observability-and-dashboard
// design names as the V1 shape: a process of its own that reads the same
// durable records `yoyo status` reads and projects them at a loopback port, for
// as long as it is left running. It is a projection and nothing else — it owns
// no state, offers no write, and restarting it changes nothing about the
// harness — so it is started and stopped freely, and a later supervisor can
// own its lifecycle without a redesign. It writes two records as it starts,
// never on a request: when each program manager instance was first seen in
// the configuration, which a never-woken instance's stale reading is measured
// from, and its own build and the configuration keys that build reads.
//
// What it prints when it starts is the whole of what an operator needs: the
// URL, and beside it where the token every request for the read model has to
// carry comes from. Where the configuration's services.dashboard.token is
// `generated`, that is the token itself, printed once; where it names the
// keychain or the file, the token is read from there and is never printed —
// what is printed is where it was read from, so the same token serves across a
// restart and nobody pastes a fresh one. The token is never put in the URL,
// where it would reach a browser history and every log a proxy keeps, and
// never in a cookie, which on 127.0.0.1 is sent to every port of 127.0.0.1;
// the page asks for it and keeps it in the tab's session storage, scoped to
// this port.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

func serveDashboard(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	port := flags.Int("port", 0, "the loopback port to serve on (default: one the operating system chooses)")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "dashboard does not accept positional arguments")
		printDashboardUsage(stderr)
		return 2
	}

	// The records are opened once before anything is bound, so a configuration
	// that does not resolve refuses at the terminal rather than at the first
	// request. They are opened again on every request after that, because a
	// dashboard left running for a week must not go on serving a state root
	// that has since stopped being readable.
	reader := dashboardReader{configPath: *configPath}
	if err := reader.ready(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// When each program manager instance was first seen, recorded as the
	// dashboard starts and never on a request, so a new one the scheduler never
	// wakes still reads stale.
	observeProgramManagers(resolved.Config, stateRoot, time.Now())
	// And which configuration keys this build reads, so a later landing that
	// adds one this build does not know is named against this process rather
	// than shown as the decoder's error on every page.
	recordConfigReader(resolved, string(config.ServiceDashboard), stderr)
	// The token is read before anything is bound, so a store that does not hold
	// it refuses at the terminal with the command that stores it rather than
	// serving under a token nobody has.
	server, announcement, err := dashboardServer(ctx, resolved.Config.Services.Dashboard.Token, resolved.Config.Product.ID, dashboardTokenStores(stateRoot), reader)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	url, err := server.Listen(*port)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	fmt.Fprintf(stdout, "dashboard for %s serving at %s\n", resolved.Config.Product.ID, url)
	for _, line := range announcement {
		fmt.Fprintln(stdout, line)
	}

	if err := server.Serve(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "dashboard stopped")
	return 0
}

// dashboardTokenStores is where a supplied token is read from on this machine:
// the keychain on macOS, which is the one place the harness reads a keychain
// from, and the file under the state root everywhere.
func dashboardTokenStores(stateRoot string) dashboard.TokenStores {
	stores := dashboard.TokenStores{Platform: runtime.GOOS, StateRoot: stateRoot}
	if runtime.GOOS == "darwin" {
		stores.Keychain = slack.Keychain{Runner: execution.OSProcessRunner{}}
	}
	return stores
}

// dashboardHeaderLine is said whichever way the token came, because it is
// about how the token is presented rather than where it came from.
const dashboardHeaderLine = "the page asks for the token and keeps it in the tab's session storage; a tool sends it as `Authorization: Bearer <token>` to /api/standing, /api/throughput, /api/spend, /api/items/<work-item-id>, and /api/program-managers/<agent>"

// dashboardServer makes the server under the token the entry names, and says
// what the command prints about it. Under `generated` the server makes its own
// and the lines carry it, once, because the terminal is the one place it is
// readable; under `keychain` or `file` the token is read from that store and
// the lines say where it was read from and never what it is, so that a
// terminal's scrollback holds nothing that outlives the process. A store that
// does not hold the token is a refusal carrying the command that stores it —
// the command `yoyo doctor` prints under service:dashboard, from the same
// function — rather than a dashboard serving under a token it invented.
func dashboardServer(ctx context.Context, source config.DashboardTokenSource, productID domain.ProductID, stores dashboard.TokenStores, reader dashboard.Reader) (*dashboard.Server, []string, error) {
	if source == config.DashboardTokenGenerated {
		server, err := dashboard.New(string(productID), reader)
		if err != nil {
			return nil, nil, err
		}
		return server, []string{
			"token: " + server.Token(),
			dashboardHeaderLine,
			"it is printed here and nowhere else, and a restarted dashboard prints a new one; stop with ctrl-c",
		}, nil
	}
	supplied, err := stores.Read(ctx, source, productID)
	if err != nil {
		return nil, nil, err
	}
	server, err := dashboard.NewWithToken(string(productID), reader, supplied.Value)
	if err != nil {
		return nil, nil, err
	}
	return server, []string{
		fmt.Sprintf("the token was read from %s, as services.dashboard.token names, and is not printed", supplied.Origin),
		dashboardHeaderLine,
		"it outlives a restart: a restarted dashboard reads the same one; stop with ctrl-c",
	}, nil
}

// dashboardReader is the read model as the dashboard is handed it, over the
// same records and the same wiring `yoyo status` reads: the four lines are one
// derivation, and a dashboard that assembled its own would be the second
// surface the read model exists to prevent.
type dashboardReader struct {
	configPath string
}

// ready opens what the reading needs and closes nothing else: the configuration,
// the state root, and the run store that every other record sits beside. A
// failure here is the state being unreadable, which the verb refuses to start
// on and the read model is refused on afterwards.
func (r dashboardReader) ready() error {
	resolved, err := loadConfiguration(r.configPath)
	if err != nil {
		return err
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return err
	}
	if _, err := runstate.NewStore(stateRoot, resolved.Config.Product.ID); err != nil {
		return err
	}
	return nil
}

// Standing is the four lines and everything the model carries beside them. What
// a source that could not be read costs is said inside the answer, line by
// line, exactly as the terminal says it; what refuses the whole answer is the
// state being unreadable at all.
func (r dashboardReader) Standing(ctx context.Context) (readmodel.Standing, error) {
	if err := r.ready(); err != nil {
		return readmodel.Standing{}, err
	}
	return readmodel.ReadStanding(ctx, standingSources(r.configPath)), nil
}

// The readers the throughput and the work item are handed are the terminal's
// own stores and client, held here to the model's interfaces so the
// substitution of a second pricing or a second tracker would not compile:
// *runstate.StreamStore is what reportSpend prices `yoyo status --spend` from,
// through its Spend method; *runstate.Store is what `yoyo status` reads each
// run's Outcome from and, through History, what `yoyo status <item>` lists the
// item's runs with; and beads.Client is the one tracker every role reads.
var (
	_ readmodel.Ledger      = (*runstate.StreamStore)(nil)
	_ readmodel.Runs        = (*runstate.Store)(nil)
	_ readmodel.Histories   = (*runstate.Store)(nil)
	_ readmodel.ItemTracker = beads.Client{}
)

// WorkItem is one work item whole — the tracker's own fields, and the run the
// harness last made for it as `yoyo status <item>` lists it — for the card the
// page opens on an item. It is read from the same tracker client and the same
// run store the standing is read from, one item at a time, when the card is
// opened: the page never reads the tracker, and this is the projection it reads
// instead.
func (r dashboardReader) WorkItem(ctx context.Context, id string) (readmodel.WorkItem, error) {
	if err := r.ready(); err != nil {
		return readmodel.WorkItem{}, err
	}
	sources, err := workItemSources(r.configPath)
	if err != nil {
		return readmodel.WorkItem{}, err
	}
	return readmodel.ReadWorkItem(ctx, sources, id)
}

// ProgramManagerReport is one program manager instance and its current lane
// report, for the card the page opens on the report. It is read from the same
// sources the standing is, through the instance's one query, so the card and
// the standing's line for the instance — and `yoyo status` — cannot disagree
// about its status.
func (r dashboardReader) ProgramManagerReport(ctx context.Context, agent string) (readmodel.ProgramManagerReport, error) {
	if err := r.ready(); err != nil {
		return readmodel.ProgramManagerReport{}, err
	}
	return readmodel.ReadProgramManagerReport(standingSources(r.configPath), agent)
}

// workItemSources opens the tracker and the run store one work item is read
// from. The tracker not resolving refuses the reading, because the item is the
// tracker's; the state root not resolving or the run store not opening costs
// the run beside it, and the reading says which under run_problem, as the
// throughput's runs_problem does.
func workItemSources(configPath string) (readmodel.WorkItemSources, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return readmodel.WorkItemSources{}, err
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return readmodel.WorkItemSources{}, fmt.Errorf("resolve product repository: %w", err)
	}
	sources := readmodel.WorkItemSources{
		Tracker:        beads.Client{Runner: execution.OSProcessRunner{}, Dir: repository},
		TrackerTimeout: chatTrackerTimeout,
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		sources.RunsProblem = fmt.Sprintf("the state root could not be resolved: %v", err)
		return sources, nil
	}
	if store, err := runstate.NewStore(stateRoot, resolved.Config.Product.ID); err != nil {
		sources.RunsProblem = err.Error()
	} else {
		sources.Runs = store
		// The repository, so the card says what a finished run left from a look
		// rather than from the run's removal flags.
		sources.Remains = standingRemains(resolved)
		// The recurring passes, so the card says when a pass last considered
		// an item waiting in a role's conversation.
		sources.Passes = store.Sweeps()
	}
	return sources, nil
}

// Throughput is what landed over the model's two windows.
// readmodel.ReadThroughput derives nothing of its own about endings: it
// classifies each run by runstate.State.Outcome, the word `yoyo status` prints
// for it. So a figure on the page is a figure the terminal prints.
func (r dashboardReader) Throughput(ctx context.Context) (readmodel.Throughput, error) {
	if err := r.ready(); err != nil {
		return readmodel.Throughput{}, err
	}
	stateRoot, productID, err := r.stateRoot()
	if err != nil {
		return readmodel.Throughput{}, err
	}
	return readmodel.ReadThroughput(ctx, throughputSources(stateRoot, productID)), nil
}

// Spend is what the harness spent over the last twenty-four hours and the last
// seven local days, and what each of the last thirty days cost.
// readmodel.ReadSpend derives nothing of its own about money: it calls
// (*runstate.StreamStore).Spend once — the one call
// internal/cli/statusstream.go's reportSpend makes to price
// `yoyo status --spend` — and adds the rows up by runstate.SpendTotals, the
// summation the terminal prints its own total and split from. So a figure on
// the page is a figure the terminal prints, and the two cannot disagree about
// what the last week cost.
func (r dashboardReader) Spend(ctx context.Context) (readmodel.Spend, error) {
	if err := r.ready(); err != nil {
		return readmodel.Spend{}, err
	}
	stateRoot, productID, err := r.stateRoot()
	if err != nil {
		return readmodel.Spend{}, err
	}
	return readmodel.ReadSpend(ctx, spendSources(stateRoot, productID)), nil
}

// stateRoot is the state root and the product the readings are taken over.
// Neither resolving refuses the whole reading, because there is nothing to
// project without them.
func (r dashboardReader) stateRoot() (string, domain.ProductID, error) {
	resolved, err := loadConfiguration(r.configPath)
	if err != nil {
		return "", "", err
	}
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return "", "", err
	}
	return stateRoot, resolved.Config.Product.ID, nil
}

// throughputSources opens the run store the throughput is read from. It failing
// to open costs the reading its endings, and the reason travels with the gap:
// the reading says "could not be opened: <why>" under runs_problem, which is
// what the page's error state shows, rather than that nothing was wired.
func throughputSources(stateRoot string, productID domain.ProductID) readmodel.ThroughputSources {
	sources := readmodel.ThroughputSources{}
	if store, err := runstate.NewStore(stateRoot, productID); err != nil {
		sources.RunsProblem = err.Error()
	} else {
		sources.Runs = store
	}
	return sources
}

// spendSources opens the stream store the spend is priced from, and carries the
// reason it could not be opened the same way.
func spendSources(stateRoot string, productID domain.ProductID) readmodel.SpendSources {
	sources := readmodel.SpendSources{}
	if store, err := runstate.NewStreamStore(stateRoot, productID); err != nil {
		sources.LedgerProblem = err.Error()
	} else {
		sources.Ledger = store
	}
	return sources
}

func printDashboardUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo dashboard [options]

Serves the read model -- the same four lines and capacity state `+"`yoyo status`"+`
reads, what the harness is spending, and what landed -- to a browser on this
machine, at a loopback port, until stopped, as a page of eight sections: the
status band, what the harness is spending, the runs and conversations in
flight, where admitted work stands in the pipeline, throughput, provider
capacity, factory problems, and the program managers. It prints the URL and, beside it,
where the token every request for the read model has to carry as
`+"`Authorization: Bearer <token>`"+` comes from: with services.dashboard.token at its
`+"`generated`"+` default, the token itself, once, and a restart makes a new one; with
it set to `+"`keychain`"+` or `+"`file`"+`, the token is read from the keychain item
yoyo-dashboard.<product id> under the account yoyo or the file
<state root>/projects/<product id>/state/dashboard.token, is never printed, and the
same one serves after a restart. A store that does not hold the token refuses to
start with the command that stores it, the one `+"`yoyo doctor`"+` prints. The page
asks for the token and keeps it in the tab's session storage, scoped to this
port, and never in a URL or a cookie. It serves
the read model as JSON behind the token -- the four lines and the capacity state
at /api/standing, what landed over today and the last seven days at
/api/throughput, what was spent over the last 24 hours and the last seven local
days with a line for each of the last thirty at /api/spend, and one work item
whole -- its tracker fields and the run last made for it -- at
/api/items/<work-item-id>, which is what the page's card on an item reads,
and one program manager instance with its current lane report at
/api/program-managers/<agent>, which is what the page's report card reads; the page shell at / and its own script and style are
static text with nothing of the read model in them, served to the browser before
it has a token. Everything else is refused: a request for the read model with no
token or the wrong one, a Host or Origin that is not the address it bound, and
durable state it cannot read each get a refusal and never part of an answer.

It is a projection. It owns no state, offers no write, and restarting it changes
nothing about the harness.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --port <n>        the loopback port to serve on (default: one the operating system chooses)`)
}
