package dashboard

// These tests are the evidence that who a dashboard accepts can be replaced
// while it serves, and that where it listens is loopback alone: the people
// bound to their own tokens are replaced whole, a token two of them bound
// resolves to neither, a shared token that could not be read accepts nothing,
// an allowed host is answered until it is taken out, and an address that is
// not loopback is refused before anything is bound.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

func TestBindingPeopleReplacesWhoWasBoundBefore(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	if refused := w.server.BindPeople([]PersonBinding{{Token: ownerToken, Person: Person{Name: "mason"}}}); len(refused) != 0 {
		t.Fatalf("BindPeople() refused %v", refused)
	}
	if response, _ := w.get(sessionPath, bearer(ownerToken)); response.StatusCode != http.StatusOK {
		t.Fatalf("the bound token: %d", response.StatusCode)
	}
	// Mason's token changed in its store: the old one is refused, the new one is
	// mason.
	if refused := w.server.BindPeople([]PersonBinding{{Token: readerToken, Person: Person{Name: "mason"}}}); len(refused) != 0 {
		t.Fatalf("BindPeople() refused %v", refused)
	}
	if response, _ := w.get(sessionPath, bearer(ownerToken)); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the replaced token: %d, want it refused", response.StatusCode)
	}
	if response, body := w.get(sessionPath, bearer(readerToken)); response.StatusCode != http.StatusOK || !strings.Contains(body, `"name":"mason"`) {
		t.Fatalf("the new token: %d %s", response.StatusCode, body)
	}
	// Nobody bound any more.
	w.server.BindPeople(nil)
	if response, _ := w.get(sessionPath, bearer(readerToken)); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a token nobody binds any more: %d, want it refused", response.StatusCode)
	}
}

func TestATokenThreePeopleBoundResolvesToNoneOfThem(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	refused := w.server.BindPeople([]PersonBinding{
		{Token: ownerToken, Person: Person{Name: "ana"}},
		{Token: ownerToken, Person: Person{Name: "bo"}},
		{Token: ownerToken, Person: Person{Name: "cy"}},
		{Token: "short", Person: Person{Name: "di"}},
		{Token: w.server.Token(), Person: Person{Name: "ed"}},
	})
	for _, name := range []string{"ana", "bo", "cy", "di", "ed"} {
		if refused[name] == nil {
			t.Errorf("%s was bound: %v", name, refused)
		}
	}
	if response, _ := w.get(sessionPath, bearer(ownerToken)); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a token three people bound: %d, want it refused", response.StatusCode)
	}
	// Two people sharing a token with somebody between them bound in their own
	// right: neither of the two resolves, and the one between does.
	refused = w.server.BindPeople([]PersonBinding{
		{Token: ownerToken, Person: Person{Name: "ana"}},
		{Token: readerToken, Person: Person{Name: "bo"}},
		{Token: ownerToken, Person: Person{Name: "cy"}},
	})
	if refused["ana"] == nil || refused["cy"] == nil || refused["bo"] != nil {
		t.Fatalf("BindPeople() refused %v, want ana and cy and not bo", refused)
	}
	if response, _ := w.get(sessionPath, bearer(ownerToken)); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a token the first and third bound: %d, want it refused", response.StatusCode)
	}
	if response, body := w.get(sessionPath, bearer(readerToken)); response.StatusCode != http.StatusOK || !strings.Contains(body, `"name":"bo"`) {
		t.Fatalf("bo's own token: %d %s", response.StatusCode, body)
	}
}

func TestASharedTokenThatCouldNotBeReadAcceptsNothing(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	shared := w.server.Token()
	w.server.SetToken("")
	for _, token := range []string{shared, ""} {
		if response, _ := w.get(sessionPath, bearer(token)); response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q with no shared token: %d, want it refused", token, response.StatusCode)
		}
	}
	w.server.SetToken(shared)
	if response, _ := w.get(sessionPath, bearer(shared)); response.StatusCode != http.StatusOK {
		t.Fatalf("the shared token set again: %d", response.StatusCode)
	}
}

func TestAnAllowedHostIsAnsweredUntilItIsTakenOut(t *testing.T) {
	t.Parallel()
	w := serve(t, stubReader{standing: standingWith("title")})
	named := func(host string) func(*http.Request) {
		return func(request *http.Request) {
			request.Host = host + ":" + port
			request.Header.Set("Authorization", "Bearer "+w.server.Token())
			request.Header.Set("Origin", "http://"+host+":"+port)
		}
	}
	if response, _ := w.get(sessionPath, named("yoyo.local")); response.StatusCode != http.StatusForbidden {
		t.Fatalf("a host not allowed: %d", response.StatusCode)
	}
	w.server.AllowHosts([]string{"Yoyo.Local"})
	if response, _ := w.get(sessionPath, named("yoyo.local")); response.StatusCode != http.StatusOK {
		t.Fatalf("an allowed host, named in another case: %d", response.StatusCode)
	}
	if response, _ := w.get(sessionPath, named("localhost")); response.StatusCode != http.StatusOK {
		t.Fatalf("localhost beside loopback: %d", response.StatusCode)
	}
	w.server.AllowHosts(nil)
	if response, _ := w.get(sessionPath, named("yoyo.local")); response.StatusCode != http.StatusForbidden {
		t.Fatalf("a host taken out: %d, want it refused", response.StatusCode)
	}
}

// An address that is not loopback is refused before anything is bound, in
// words that name the setting and say the dashboard listens only here; a
// loopback address is listened on.
func TestTheDashboardListensOnALoopbackBindAndRefusesAnyOther(t *testing.T) {
	t.Parallel()
	server, err := New("yoyodyne", stubReader{})
	if err != nil {
		t.Fatal(err)
	}
	for _, bind := range []string{"192.168.1.20", "0.0.0.0", "::", "not-an-address"} {
		if _, err := server.ListenOn(bind, 0); err == nil || !strings.Contains(err.Error(), "is not a loopback address, and the dashboard listens only on this machine") {
			t.Errorf("ListenOn(%q) = %v, want it refused", bind, err)
		}
		if _, err := server.ListenSharedOn(bind, 8765); err == nil || !strings.Contains(err.Error(), "services.dashboard.bind") {
			t.Errorf("ListenSharedOn(%q) = %v, want it refused", bind, err)
		}
	}
	url, err := server.ListenOn(config.DefaultDashboardBind, 0)
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("this environment grants no listener: %v", err)
		}
		t.Fatal(err)
	}
	defer server.listener.Close()
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("URL = %q", url)
	}
}
