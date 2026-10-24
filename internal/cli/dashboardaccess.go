package cli

// Who a running dashboard accepts, re-read while it runs.
//
// A dashboard reads who it accepts as it starts: the people bound under
// operators.<name>.dashboard_token and each one's own token from the store
// their entry names, its own shared token where services.dashboard.token names
// a store, and, for the dashboard the supervisor starts, the extra names
// services.dashboard.allowed_hosts lets a request carry. None of that needs a
// new listener, so none of it waits for a restart: every dashboardAccessEvery
// the dashboard reads all of it again and replaces what it accepts whole. An
// operator bound in the configuration, or a token file created, is accepted
// from the next re-read on; a token changed or taken out of its store, or a
// person no longer bound, is refused from then on. Nothing it re-reads is a
// restart, so the shared token a tab holds goes on working throughout.
//
// What cannot be applied is said where the running parts are watched: the
// dashboard records it on its own record (runstate.DashboardInstance's
// AccessProblems), which `yoyo status` carries on the dashboard's line and
// `yoyo doctor` reports under dashboard-access, and says each change of it in
// its log. A configuration that does not load leaves what it accepts as it was,
// as the supervisor leaves a running part over one. Where the shared token
// comes from, and the port and address it listens on, are settings it holds
// from its start: a change to one is the supervisor's to apply by replacing it
// (internal/supervise/settings.go).

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// dashboardAccessEvery is how often a running dashboard re-reads who it
// accepts (dashboard.AccessEvery).
const dashboardAccessEvery = dashboard.AccessEvery

// dashboardAccess is what one dashboard re-reads, and what it last said.
type dashboardAccess struct {
	configPath string
	stores     dashboard.TokenStores
	productID  domain.ProductID
	// source is where the shared token came from as the dashboard started. The
	// token is read from that store again; a different source in the
	// configuration is a setting the supervisor applies by replacing the
	// dashboard, so it is left alone here.
	source config.DashboardTokenSource
	// supervised is a dashboard the supervisor started, which alone answers to
	// services.dashboard.allowed_hosts; one started by hand answers to the
	// loopback address it bound.
	supervised bool
	// every is how often it re-reads; zero is dashboardAccessEvery, and a test
	// sets a shorter one.
	every time.Duration
	said  string
}

// dashboardAccessReading is one reading of who the dashboard accepts: lines
// saying it, none of them a token, and among them the problems — whatever it
// could not apply, in words a person can act on.
type dashboardAccessReading struct {
	lines    []string
	problems []string
}

// apply reads who the dashboard accepts from the configuration and the token
// stores as they stand now and applies it to the server.
func (a *dashboardAccess) apply(ctx context.Context, server *dashboard.Server) dashboardAccessReading {
	resolved, err := loadConfiguration(a.configPath)
	if err != nil {
		problem := fmt.Sprintf("the configuration could not be read again, so who the dashboard accepts is left as it was: %v", err)
		return dashboardAccessReading{lines: []string{problem}, problems: []string{problem}}
	}
	return a.applyConfig(ctx, server, resolved.Config)
}

func (a *dashboardAccess) applyConfig(ctx context.Context, server *dashboard.Server, cfg config.Config) dashboardAccessReading {
	var reading dashboardAccessReading
	// The shared token first, because a person's token is refused where it is
	// the shared one.
	if a.source != config.DashboardTokenGenerated && cfg.Services.Dashboard.Token == a.source {
		supplied, err := a.stores.Read(ctx, a.source, a.productID)
		if err != nil {
			server.SetToken("")
			problem := fmt.Sprintf("the dashboard's own token could not be read again, so it accepts no shared token until it can: %v", err)
			reading.lines = append(reading.lines, problem)
			reading.problems = append(reading.problems, problem)
		} else {
			server.SetToken(supplied.Value)
		}
	}
	if a.supervised {
		hosts := cfg.Services.Dashboard.AllowedHosts
		server.AllowHosts(hosts)
		if len(hosts) == 0 {
			reading.lines = append(reading.lines, "it answers to the address it is bound to alone, as services.dashboard.allowed_hosts names no other host")
		} else {
			reading.lines = append(reading.lines, fmt.Sprintf("it answers to the address it is bound to and to %s, as services.dashboard.allowed_hosts names", strings.Join(hosts, ", ")))
		}
	}
	lines, problems := dashboardPeople(ctx, server, cfg, a.stores)
	reading.lines = append(reading.lines, lines...)
	reading.problems = append(reading.problems, problems...)
	return reading
}

// changed reports whether a reading says something other than the last one
// did, and remembers it.
func (a *dashboardAccess) changed(reading dashboardAccessReading) bool {
	said := strings.Join(reading.lines, "\n")
	if said == a.said {
		return false
	}
	a.said = said
	return true
}

// watch re-reads who the dashboard accepts every dashboardAccessEvery until
// the context ends, which is the dashboard stopping, and hands each reading
// that changed to record. It ends with the context, however the dashboard
// stops.
func (a *dashboardAccess) watch(ctx context.Context, server *dashboard.Server, stdout io.Writer, record func(problems []string)) {
	every := a.every
	if every <= 0 {
		every = dashboardAccessEvery
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		reading := a.apply(ctx, server)
		if ctx.Err() != nil || !a.changed(reading) {
			continue
		}
		fmt.Fprintf(stdout, "who this dashboard accepts changed at %s, re-read from the configuration and the token stores:\n", time.Now().Format("2006-01-02 15:04:05 MST"))
		for _, line := range reading.lines {
			fmt.Fprintln(stdout, "  "+line)
		}
		record(reading.problems)
	}
}

// dashboardPeople binds every person who named a dashboard token of their own,
// replacing whoever was bound before, and says what came of each: bound, with
// where the token was read from and never the token, or left unbound with the
// reason and the command that stores it. The second list is the people left
// unbound, which is what the dashboard could not apply.
func dashboardPeople(ctx context.Context, server *dashboard.Server, cfg config.Config, stores dashboard.TokenStores) ([]string, []string) {
	bindings := cfg.DashboardBindings()
	if len(bindings) == 0 {
		server.BindPeople(nil)
		return []string{"nobody has bound a dashboard token of their own under operators.<name>.dashboard_token, so the page reads only and refuses every approve, decline, and ask with the reason"}, nil
	}
	read := make([]dashboard.PersonBinding, 0, len(bindings))
	origins := map[string]string{}
	unread := map[string]error{}
	for _, binding := range bindings {
		supplied, err := stores.ReadPerson(ctx, binding.Source, cfg.Product.ID, binding.Operator)
		if err != nil {
			unread[binding.Operator] = err
			continue
		}
		origins[binding.Operator] = supplied.Origin
		read = append(read, dashboard.PersonBinding{Token: supplied.Value, Person: dashboard.Person{Name: binding.Operator, Grants: binding.Grants}})
	}
	refused := server.BindPeople(read)
	var lines, problems []string
	for _, binding := range bindings {
		err := unread[binding.Operator]
		if err == nil {
			err = refused[binding.Operator]
		}
		if err != nil {
			line := fmt.Sprintf("%s cannot approve, decline, or ask from the page: %v", binding.Operator, err)
			lines = append(lines, line)
			problems = append(problems, line)
			continue
		}
		lines = append(lines, fmt.Sprintf("%s approves, declines, and asks from the page with their own token, read from %s and not printed", binding.Operator, origins[binding.Operator]))
	}
	return lines, problems
}

// bindDashboardPeople is dashboardPeople's lines alone.
func bindDashboardPeople(ctx context.Context, server *dashboard.Server, cfg config.Config, stores dashboard.TokenStores) []string {
	lines, _ := dashboardPeople(ctx, server, cfg, stores)
	return lines
}
