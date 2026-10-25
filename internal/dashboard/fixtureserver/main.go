// Command fixtureserver serves the dashboard page over the fixtures under
// internal/dashboard/testdata/fixtures, one dashboard per scenario, so the
// page can be looked at in a browser in every state without a harness behind
// it. It is what a rendered capture of each section in each state is taken
// from, and it is deliberately not part of `yoyo`: it reads fixtures rather than
// a state root, and nothing about it is a surface.
//
//	go run ./internal/dashboard/fixtureserver
//
// It prints one line per scenario — the scenario, the URL, and the token that
// page asks for — and serves until interrupted. Each scenario is a dashboard of
// its own on a port of its own, bound to loopback with the same server and the
// same policy `yoyo dashboard` uses, so what the browser shows is what the
// product shows and the policy is exercised on the way.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
)

// scenario is one dashboard's worth of answers: which fixture each reading is
// served from, or that the reading never answers, or that it is refused.
type scenario struct {
	standing   string
	throughput string
	spend      string
	// pending is a dashboard whose readings never answer, which is what the
	// loading states are looked at under.
	pending bool
	// refused is a dashboard whose state cannot be read at all.
	refused string
}

// scenarios are the same pairings testdata/render.js renders, so a capture and
// a render of one name show one state.
var scenarios = map[string]scenario{
	"quiet":      {standing: "standing-quiet", throughput: "throughput-quiet", spend: "spend-quiet"},
	"busy":       {standing: "standing-busy", throughput: "throughput-busy", spend: "spend-busy"},
	"held":       {standing: "standing-held", throughput: "throughput-busy", spend: "spend-busy"},
	"roles":      {standing: "standing-roles", throughput: "throughput-busy", spend: "spend-busy"},
	"degraded":   {standing: "standing-degraded", throughput: "throughput-degraded", spend: "spend-busy"},
	"unreadable": {standing: "standing-unreadable", throughput: "throughput-unreadable", spend: "spend-unreadable"},
	"loading":    {pending: true},
	"refused":    {refused: "the state root could not be resolved: open /Users/somebody/Library/Application Support/Yoyodyne/state: permission denied"},
}

// fixtureReader is a read model that answers from fixtures. A work item is
// answered from the item fixtures by id — `item-<id>.json` — so every card a
// scenario's page can open has a fixture behind it, and an id with none is the
// tracker holding nothing under it. A program manager's report is answered the
// same way from `report-<agent>.json`.
type fixtureReader struct {
	dir        string
	standing   readmodel.Standing
	throughput readmodel.Throughput
	spend      readmodel.Spend
	pending    bool
	refused    error
}

func (r fixtureReader) WorkItem(ctx context.Context, id string) (readmodel.WorkItem, error) {
	if r.pending {
		<-ctx.Done()
		return readmodel.WorkItem{}, ctx.Err()
	}
	if r.refused != nil {
		return readmodel.WorkItem{}, r.refused
	}
	var item readmodel.WorkItem
	if err := load(r.dir, "item-"+id, &item); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return readmodel.WorkItem{}, fmt.Errorf("%w: no fixture item-%s.json", readmodel.ErrNoSuchWorkItem, id)
		}
		return readmodel.WorkItem{}, err
	}
	return item, nil
}

// ProgramManagerReport answers an instance's report from the report fixtures by
// name — `report-<agent>.json` — and a name with none is the read model knowing
// no such instance.
func (r fixtureReader) ProgramManagerReport(ctx context.Context, agent string) (readmodel.ProgramManagerReport, error) {
	if r.pending {
		<-ctx.Done()
		return readmodel.ProgramManagerReport{}, ctx.Err()
	}
	if r.refused != nil {
		return readmodel.ProgramManagerReport{}, r.refused
	}
	var answer readmodel.ProgramManagerReport
	if err := load(r.dir, "report-"+agent, &answer); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return readmodel.ProgramManagerReport{}, fmt.Errorf("%w: no fixture report-%s.json", readmodel.ErrNoSuchProgramManager, agent)
		}
		return readmodel.ProgramManagerReport{}, err
	}
	return answer, nil
}

func (r fixtureReader) Standing(ctx context.Context) (readmodel.Standing, error) {
	if r.pending {
		<-ctx.Done()
		return readmodel.Standing{}, ctx.Err()
	}
	return r.standing, r.refused
}

func (r fixtureReader) Throughput(ctx context.Context) (readmodel.Throughput, error) {
	if r.pending {
		<-ctx.Done()
		return readmodel.Throughput{}, ctx.Err()
	}
	return r.throughput, r.refused
}

func (r fixtureReader) Spend(ctx context.Context) (readmodel.Spend, error) {
	if r.pending {
		<-ctx.Done()
		return readmodel.Spend{}, ctx.Err()
	}
	return r.spend, r.refused
}

// Backlog answers from `backlog.json` where the fixtures hold one, and with an
// empty backlog where they do not, so every scenario serves the route.
func (r fixtureReader) Backlog(ctx context.Context) (readmodel.Backlog, error) {
	if r.pending {
		<-ctx.Done()
		return readmodel.Backlog{}, ctx.Err()
	}
	if r.refused != nil {
		return readmodel.Backlog{}, r.refused
	}
	var answer readmodel.Backlog
	if err := load(r.dir, "backlog", &answer); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return readmodel.Backlog{Items: []readmodel.BacklogItem{}}, nil
		}
		return readmodel.Backlog{}, err
	}
	return answer, nil
}

// Delivery answers no scenario's delivery page yet: that page is drawn by its
// own work, and until then every scenario's delivery reading is the empty one.
func (r fixtureReader) Delivery(ctx context.Context) (readmodel.Delivery, error) {
	if r.pending {
		<-ctx.Done()
		return readmodel.Delivery{}, ctx.Err()
	}
	return readmodel.Delivery{}, r.refused
}

// Accomplishment answers from `accomplishment.json` where the fixtures hold
// one, and with an empty reading where they do not, so every scenario serves
// the route.
func (r fixtureReader) Accomplishment(ctx context.Context) (readmodel.Accomplishment, error) {
	if r.pending {
		<-ctx.Done()
		return readmodel.Accomplishment{}, ctx.Err()
	}
	if r.refused != nil {
		return readmodel.Accomplishment{}, r.refused
	}
	var answer readmodel.Accomplishment
	if err := load(r.dir, "accomplishment", &answer); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return readmodel.Accomplishment{Roles: []readmodel.RoleAccomplishment{}, Weeks: []readmodel.WeekAccomplishment{}}, nil
		}
		return readmodel.Accomplishment{}, err
	}
	return answer, nil
}

func load(dir, name string, into any) error {
	body, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, into)
}

func reader(dir string, s scenario) (dashboard.Reader, error) {
	r := fixtureReader{dir: dir, pending: s.pending}
	if s.refused != "" {
		r.refused = errors.New(s.refused)
	}
	if s.standing != "" {
		if err := load(dir, s.standing, &r.standing); err != nil {
			return nil, err
		}
	}
	if s.throughput != "" {
		if err := load(dir, s.throughput, &r.throughput); err != nil {
			return nil, err
		}
	}
	if s.spend != "" {
		if err := load(dir, s.spend, &r.spend); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func main() {
	dir := filepath.Join("internal", "dashboard", "testdata", "fixtures")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	sort.Strings(names)
	stopped := make(chan error, len(names))
	for _, name := range names {
		model, err := reader(dir, scenarios[name])
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
			os.Exit(1)
		}
		server, err := dashboard.New("yoyodyne", model)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		url, err := server.Listen(0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%-11s %s  token %s\n", name, url, server.Token())
		go func() { stopped <- server.Serve(ctx) }()
	}
	fmt.Println("serving every scenario until interrupted; paste each token into its page")
	<-ctx.Done()
	for range names {
		if err := <-stopped; err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
}
