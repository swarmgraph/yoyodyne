package orchestratortest

import (
	"context"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/backend"
)

// LoginProbe stands in for the developer's provider as a watch asks it one
// thing: whether the machine is logged in.
type LoginProbe struct {
	Mu            sync.Mutex
	Authenticated bool
	Asked         int
}

func (p *LoginProbe) CheckAvailability(context.Context) (backend.Availability, error) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	p.Asked++
	return backend.Availability{Installed: true, Authenticated: p.Authenticated}, nil
}

func (p *LoginProbe) Login() {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	p.Authenticated = true
}

func (p *LoginProbe) LoggedIn() bool {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	return p.Authenticated
}
