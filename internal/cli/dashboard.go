package cli

// Serving the read model to a browser on this machine.
//
// `yoyo dashboard` is the standalone command the observability-and-dashboard
// design names as the V1 shape: a process of its own that reads the same
// durable records `yoyo status` reads and projects them at a loopback port, for
// as long as it is left running. It is a projection — it owns no state, and
// restarting it changes nothing about the harness — so it is started and
// stopped freely, by a person or by the product's supervisor
// (dashboardchild.go), which starts it with --start-id. The one thing it takes
// besides reads is the operator's own approve, decline, and ask on what waits
// on his decision, from a person who bound a token of their own, carried out
// by the verbs' own code (dashboardacts.go). It writes two records as it starts,
// never on a request: when each program manager instance was first seen in
// the configuration, which a never-woken instance's stale reading is measured
// from, and its own build and the configuration keys that build reads.
//
// What it prints when it starts is the whole of what an operator needs: the
// URL, and beside it where the token every request for the read model has to
// carry comes from. Where the configuration's services.dashboard.token is
// `generated`, that is the token itself, printed once by a dashboard started
// by hand and never by one the supervisor started, whose output goes to a log;
// either writes it to the token file `yoyo dashboard token` reads. Where it names the
// keychain or the file, the token is read from there and is never printed —
// what is printed is where it was read from, so the same token serves across a
// restart and nobody pastes a fresh one. The token is never put in the URL,
// where it would reach a browser history and every log a proxy keeps, and
// never in a cookie, which on 127.0.0.1 is sent to every port of 127.0.0.1;
// the page asks for it and keeps it in the tab's session storage, scoped to
// this port.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

func serveDashboard(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "token" {
		return printDashboardToken(ctx, args[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	port := flags.Int("port", 0, "the loopback port to serve on (default: one the operating system chooses)")
	startID := flags.String("start-id", "", "the start identity the product's supervisor hands a dashboard it starts")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "dashboard does not accept positional arguments")
		printDashboardUsage(stderr)
		return 2
	}
	supervised := *startID != ""
	if supervised && !runstate.ValidDashboardStartID(*startID) {
		fmt.Fprintf(stderr, "dashboard: --start-id %q is not a start identity the supervisor makes\n", *startID)
		return 2
	}
	if supervised && *port <= 0 {
		fmt.Fprintln(stderr, "dashboard: a supervised dashboard serves on the configured port, and --port names none")
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
	productID := resolved.Config.Product.ID
	// When each program manager instance was first seen, recorded as the
	// dashboard starts and never on a request, so a new one the scheduler never
	// wakes still reads stale.
	observeProgramManagers(resolved.Config, stateRoot, time.Now())
	// And which configuration keys this build reads, so a later landing that
	// adds one this build does not know is named against this process rather
	// than shown as the decoder's error on every page.
	// The port, the address, and the token source are read once, here, and held
	// for as long as it serves; recording what it started with is what lets the
	// supervisor see one change and replace it with a dashboard on the new
	// value. A dashboard started by hand serves on the port it was given, and
	// binds loopback, rather than the configured ones, so only its token source
	// is the configuration's. Who it accepts is not held: it is re-read while it
	// serves (dashboardaccess.go).
	held := map[string]string{config.SettingDashboardToken: string(resolved.Config.Services.Dashboard.Token)}
	bind := config.DefaultDashboardBind
	if supervised {
		bind = strings.TrimSpace(resolved.Config.Services.Dashboard.Bind)
		// A bind outside loopback is refused before anything is recorded or
		// bound: the dashboard listens only on this machine.
		if err := dashboard.LoopbackBind(bind); err != nil {
			fmt.Fprintf(stderr, "dashboard: %v\n", err)
			return 1
		}
		held[config.SettingDashboardPort] = strconv.Itoa(*port)
		held[config.SettingDashboardBind] = bind
	}
	recordConfigReaderHolding(resolved, string(config.ServiceDashboard), held, stderr)
	records, err := runstate.NewDashboardStore(stateRoot, productID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// The token is read before anything is bound, so a store that does not hold
	// it refuses at the terminal with the command that stores it rather than
	// serving under a token nobody has.
	source := resolved.Config.Services.Dashboard.Token
	stores := dashboardTokenStores(stateRoot)
	server, announcement, err := dashboardServer(ctx, source, productID, stores, reader)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// The operator's own decisions, taken from the page by a person who bound a
	// token of their own and carried out by the verbs' own code. A person whose
	// token cannot be read is said here and left unbound, so the page goes on
	// reading and refuses that person's acts with the reason.
	server.SetActor(newDashboardActor(ctx, *configPath, stderr))
	var url, readyURL string
	if supervised {
		url, err = server.ListenSharedOn(bind, *port)
		if err == nil {
			readyURL, err = server.ListenReadiness()
		}
	} else {
		url, err = server.Listen(*port)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// Who it accepts, read now and again every dashboardAccessEvery for as long
	// as it serves, so a person bound or a token stored after it started is
	// accepted without a restart, and one taken away is refused.
	access := &dashboardAccess{configPath: *configPath, stores: stores, productID: productID, source: source, supervised: supervised}
	reading := access.applyConfig(ctx, server, resolved.Config)
	access.changed(reading)
	if !supervised {
		if *startID, err = runstate.NewDashboardStartID(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	build := buildinfo.Commit()
	server.Identify(*startID, build)
	instance := runstate.DashboardInstance{
		StartID:    *startID,
		PID:        os.Getpid(),
		Build:      build,
		StartedAt:  time.Now().UTC(),
		URL:        url,
		Port:       server.Port(),
		ReadyURL:   readyURL,
		Supervised: supervised,
		// What it could not apply of who it accepts, kept current as it re-reads.
		AccessProblems: reading.problems,
	}
	receipt, published := publishDashboardToken(records, source, server, supervised, &instance, stderr)
	// The record is rewritten as the dashboard becomes ready and as what it
	// could not apply changes, which happen on two goroutines.
	var recording sync.Mutex
	if supervised {
		server.Supervise(*startID, build, func() {
			recording.Lock()
			defer recording.Unlock()
			instance.ReadyAt = time.Now().UTC()
			if err := records.Update(instance); err != nil {
				fmt.Fprintf(stderr, "the dashboard is ready and could not record that it is: %v\n", err)
			}
		})
		if instance.TokenProblem != "" {
			server.Block(instance.TokenProblem)
		}
	} else {
		instance.ReadyAt = instance.StartedAt
	}
	lease, err := records.Register(instance)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() {
		// A clean stop takes the token file with it, where it is still the one
		// this dashboard published, and then its own record.
		if err := records.WithdrawToken(receipt); err != nil {
			fmt.Fprintf(stderr, "the token file %s could not be removed: %v\n", records.TokenPath(), err)
		}
		if err := records.Unregister(lease, *startID); err != nil {
			fmt.Fprintf(stderr, "the dashboard's record could not be removed: %v\n", err)
		}
	}()

	fmt.Fprintf(stdout, "dashboard for %s serving at %s\n", productID, url)
	if supervised {
		// Its output goes to a log, so it says where the token is and never what.
		fmt.Fprintf(stdout, "started by the product's supervisor as %s; it serves its health answer alone until the supervisor confirms it with its token\n", *startID)
		if published {
			fmt.Fprintf(stdout, "the token is in %s, readable by its owner alone, and `yoyo dashboard token` prints it for a person\n", records.TokenPath())
		}
		if source != config.DashboardTokenGenerated {
			fmt.Fprintln(stdout, announcement[0])
		}
	} else {
		for _, line := range announcement {
			fmt.Fprintln(stdout, line)
		}
	}
	// Who may act from the page is said either way: it names where each
	// person's token is read from and never the token.
	for _, line := range reading.lines {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintf(stdout, "who it accepts is read again every %s, so a person bound or a token stored, changed, or removed is applied without a restart\n", dashboardAccessEvery)
	// The re-reading stops before the record is removed, however the dashboard
	// stops, so nothing writes the record of a dashboard that has gone.
	watching, stopWatching := context.WithCancel(ctx)
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		access.watch(watching, server, stdout, func(problems []string) {
			recording.Lock()
			defer recording.Unlock()
			instance.AccessProblems = problems
			if err := records.Update(instance); err != nil {
				fmt.Fprintf(stderr, "the dashboard could not record what it could not apply of who it accepts: %v\n", err)
			}
		})
	}()
	defer func() {
		stopWatching()
		<-watched
	}()
	if !supervised {
		if published {
			fmt.Fprintf(stdout, "it is also in %s while this dashboard runs, and `yoyo dashboard token` prints it\n", records.TokenPath())
		}
	}

	if err := server.Serve(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "dashboard stopped")
	return 0
}

// publishDashboardToken writes a generated token to the token file, which is
// what lets a person read it from a dashboard whose output goes to a log. A
// configured token is never written there. A dashboard started by hand
// publishes only where no supervised dashboard is serving, so the file goes on
// holding the product's dashboard's token; its own is on its terminal. A
// supervised dashboard that could not publish it safely records why, and is
// held to serving nothing (dashboardchild.go).
func publishDashboardToken(records *runstate.DashboardStore, source config.DashboardTokenSource, server *dashboard.Server, supervised bool, instance *runstate.DashboardInstance, stderr io.Writer) (runstate.DashboardTokenReceipt, bool) {
	if source != config.DashboardTokenGenerated {
		return runstate.DashboardTokenReceipt{}, false
	}
	if !supervised && supervisedDashboardServing(records) {
		fmt.Fprintf(stderr, "the product's supervised dashboard is serving and its token is the one in %s, so this dashboard's token is printed here and not written there\n", records.TokenPath())
		return runstate.DashboardTokenReceipt{}, false
	}
	receipt, err := records.PublishToken(server.Token())
	if err != nil {
		instance.TokenProblem = err.Error()
		fmt.Fprintf(stderr, "the token could not be published: %v\n", err)
		return runstate.DashboardTokenReceipt{}, false
	}
	instance.TokenPublished = true
	return receipt, true
}

// supervisedDashboardServing reports whether the dashboard the supervisor last
// started is still serving.
func supervisedDashboardServing(records *runstate.DashboardStore) bool {
	start, found, err := records.Start()
	if err != nil || !found {
		return false
	}
	look, found, err := records.Instance(start.StartID)
	return err == nil && found && look.Live && look.Instance.Supervised
}

// printDashboardToken is `yoyo dashboard token`: the token the product's
// dashboard generated and published, printed for a person. It is refused to a
// process the harness launched for a role, and it prints nothing for a token
// the configuration supplies, which the person stored themselves and whose
// store it names instead.
func printDashboardToken(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("dashboard token", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 0 {
		fmt.Fprintln(stderr, "dashboard token does not accept positional arguments")
		return 2
	}
	if err := refusedToAgentProcess("yoyo dashboard token", "reading the dashboard's token is a person's"); err != nil {
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
	productID := resolved.Config.Product.ID
	if source := resolved.Config.Services.Dashboard.Token; source != config.DashboardTokenGenerated {
		where := "the file " + dashboard.TokenFile(stateRoot, productID)
		if source == config.DashboardTokenKeychain {
			where = fmt.Sprintf("the keychain item %s under the account %s", dashboard.TokenSecret(productID), dashboard.TokenAccount)
		}
		fmt.Fprintf(stderr, "services.dashboard.token is %q, so the dashboard serves the token stored in %s, which this command does not print\n", source, where)
		return 1
	}
	records, err := runstate.NewDashboardStore(stateRoot, productID)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	token, err := records.ReadToken()
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, runstate.ErrNoDashboardToken) {
			fmt.Fprintln(stderr, "no dashboard is serving a generated token for this product; `yoyo start` starts the product's, and `yoyo status` says how it stands")
		}
		return 1
	}
	fmt.Fprintln(stdout, token)
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
const dashboardHeaderLine = "the page asks for the token and keeps it in the tab's session storage; a tool sends it as `Authorization: Bearer <token>` to /api/standing, /api/throughput, /api/spend, /api/backlog, /api/delivery, /api/accomplishment, /api/items/<work-item-id>, /api/program-managers/<agent>, and /api/session, and a person's own token posts the operator's approve, decline, and ask to /api/acts/decide and /api/acts/ask"

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
			"a restarted dashboard prints a new one; stop with ctrl-c",
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

// Backlog is every work item the tracker holds, open and closed, for the
// backlog page. It is read from the same sources the standing is, through the
// queue derivation the standing's not-startable line is worded from, so the page
// and `yoyo status` cannot disagree about whether an item is ready.
func (r dashboardReader) Backlog(ctx context.Context) (readmodel.Backlog, error) {
	if err := r.ready(); err != nil {
		return readmodel.Backlog{}, err
	}
	return readmodel.ReadBacklog(ctx, standingSources(r.configPath)), nil
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
	reading := readmodel.ReadThroughput(ctx, throughputSources(stateRoot, productID))
	// The landed items are named from the tracker as the reading is made, as
	// every item the page shows is; a tracker that cannot be listed marks them.
	titles, _ := readmodel.ReadWorkItemTitles(ctx, standingSources(r.configPath))
	reading.NameWorkItems(titles)
	return reading, nil
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

// Accomplishment is what the spend of the current week and the three before it
// accomplished. readmodel.ReadAccomplishment is the one reading `yoyo status
// --spend --from` prints, read here over its default range from the same
// stores (accomplishmentSources), so the page and the terminal cannot disagree
// about what a pass led to.
func (r dashboardReader) Accomplishment(ctx context.Context) (readmodel.Accomplishment, error) {
	if err := r.ready(); err != nil {
		return readmodel.Accomplishment{}, err
	}
	stateRoot, productID, err := r.stateRoot()
	if err != nil {
		return readmodel.Accomplishment{}, err
	}
	return readmodel.ReadAccomplishment(ctx, accomplishmentSources(stateRoot, productID), readmodel.AccomplishmentRange{}), nil
}

// Delivery is the factory's delivery measures, day by day and week by week in
// Pacific time. readmodel.ReadDelivery counts a merge exactly where the
// throughput counts a landing, and the hand steps per change by
// readmodel.CountInterventions, the count `yoyo intervention` prints.
func (r dashboardReader) Delivery(ctx context.Context) (readmodel.Delivery, error) {
	if err := r.ready(); err != nil {
		return readmodel.Delivery{}, err
	}
	resolved, err := loadConfiguration(r.configPath)
	if err != nil {
		return readmodel.Delivery{}, err
	}
	return readmodel.ReadDelivery(ctx, deliverySources(resolved)), nil
}

// deliverySources opens every record the delivery measures are read from. A
// store that will not open costs the measures read from it, and the reason
// travels with the gap.
//
// The tracker is told the zone its writers ran bd in: this repository's are in
// Pacific time, and a dependency stamp bd wrote in a writer's wall clock is
// corrected in that zone rather than in whichever zone the dashboard happens to
// run in (beads.Client.WriterZone). No delivery measure is read from a
// dependency stamp, and naming the zone keeps it so for anything the listing
// carries.
func deliverySources(resolved config.Resolved) readmodel.DeliverySources {
	zone := readmodel.DeliveryZone()
	sources := readmodel.DeliverySources{TrackerTimeout: chatTrackerTimeout, Zone: zone}
	roots, err := resolveRoots(resolved)
	if err != nil {
		problem := fmt.Sprintf("the product's directories could not be resolved: %v", err)
		sources.TrackerProblem, sources.RunsProblem, sources.UsageProblem = problem, problem, problem
		sources.HandStepsProblem, sources.PassesProblem, sources.StallsProblem = problem, problem, problem
		sources.FactoryStallsProblem, sources.RevertsProblem = problem, problem
		return sources
	}
	productID := resolved.Config.Product.ID
	sources.Tracker = beads.Client{Runner: execution.OSProcessRunner{}, Dir: roots.repository, WriterZone: zone}
	if worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: roots.repository,
		WorktreeRoot:   roots.worktreeRoot,
		Remote:         resolved.Config.Execution.Remote,
	}); err != nil {
		sources.RevertsProblem = err.Error()
	} else {
		sources.Reverts = worktrees
	}
	if store, err := runstate.NewStore(roots.stateRoot, productID); err != nil {
		sources.RunsProblem, sources.PassesProblem = err.Error(), err.Error()
	} else {
		sources.Runs = store
		sources.Passes = store.Sweeps()
	}
	if store, err := runstate.NewSpendStore(roots.stateRoot, productID); err != nil {
		sources.UsageProblem = err.Error()
	} else {
		sources.Usage = store
	}
	if store, err := runstate.NewInterventionStore(roots.stateRoot, productID); err != nil {
		sources.HandStepsProblem = err.Error()
	} else {
		sources.HandSteps = store
	}
	if store, err := runstate.NewStallStore(roots.stateRoot, productID); err != nil {
		sources.StallsProblem = err.Error()
	} else {
		sources.Stalls = store
	}
	if store, err := runstate.NewFactoryStallStore(roots.stateRoot, productID); err != nil {
		sources.FactoryStallsProblem = err.Error()
	} else {
		sources.FactoryStalls = store
	}
	return sources
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
       yoyo dashboard token [--config <path>]

Serves the read model -- the same four lines and capacity state `+"`yoyo status`"+`
reads, what the harness is spending, and what landed -- to a browser on this
machine, at a loopback port, until stopped, as a page of eight sections: the
status band, what the harness is spending, the runs and conversations in
flight, where admitted work stands in the pipeline, throughput, provider
capacity, factory problems, and the program managers, with a ninth, the merge
queues, shown only where the product has a merge queue record. It prints the URL and, beside it,
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
days with a line for each of the last thirty at /api/spend, the factory's
delivery measures for each of the last thirty days and thirteen weeks in Pacific
time at /api/delivery, what the spend of the current week and the three before
it accomplished at /api/accomplishment -- the reading `+"`yoyo status --spend --from`"+`
prints, rebuilt every fifteen minutes -- and one work item
whole -- its tracker fields and the run last made for it -- at
/api/items/<work-item-id>, which is what the page's card on an item reads,
and one program manager instance with its current lane report at
/api/program-managers/<agent>, which is what the page's report card reads,
and every work item the tracker holds, open and closed, with when it was filed
and closed, its priority, labels, who does it, whether it is ready or waiting,
and who filed it, at /api/backlog; the page shell at / and its own script and style are
static text with nothing of the read model in them, served to the browser before
it has a token. Everything else is refused: a request for the read model with no
token or the wrong one, a Host or Origin that is not the address it bound, and
durable state it cannot read each get a refusal and never part of an answer.

What the attention line names as the operator's and a verb approves or
declines is put to him on the page one at a time, with Approve, Decline, and a
way to ask the role that raised it first. Today that is the documents waiting
for his approval -- the brief, the goals, the non-goals, and the operating
rules -- and the changes proposed to the brief or the goals that the line names
as his. Each decision is recorded exactly as its own verb at the command line records
it: `+"`yoyo amendment approve`"+` or `+"`decline`"+` for a proposed change, and the
conversation's `+"`approve document-...`"+` or `+"`decline document-...`"+` message for a
document. Asking decides nothing. Only a person acts: the token printed or read
above reads only, and the operator -- for now the one person holding own-intent
-- binds a token of their own under operators.<name>.dashboard_token --
`+"`keychain`"+`, the item yoyo-dashboard.<product id>.<name> under the account yoyo,
or `+"`file`"+`, <state root>/projects/<product id>/state/dashboard.<name>.token --
and acts only where they hold the grant: own-intent for a document or proposed work, and
decide-amendments for a proposed change, with own-intent beside it for a change
to a document the Lead Product Manager owns. Every other act is refused.

It is a projection. It owns no state, and restarting it changes nothing about
the harness.

With services.dashboard.enabled, `+"`yoyo start`"+` starts it as a part of the product
on services.dashboard.port, and `+"`yoyo stop`"+` stops it. A dashboard the product
starts prints no token: a generated one is written to
<state root>/projects/<product id>/state/dashboard/token, readable by its owner
alone, and the dashboard serves nothing but /api/health until the supervisor has
made a request carrying that token. One started by hand writes the same file
unless the product's dashboard is running.

`+"`yoyo dashboard token`"+` prints the generated token of the dashboard that is
running, for a person; it is refused from a process the harness started for a
role, and prints nothing for a token the configuration names a store for.

Options:
  --config <path>    configuration file (default: the nearest .yoyodyne/config.yaml)
  --port <n>         the loopback port to serve on (default: one the operating system chooses)
  --start-id <id>    the start identity the product's supervisor hands a dashboard it starts`)
}
