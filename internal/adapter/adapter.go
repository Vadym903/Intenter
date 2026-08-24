// Package adapter defines the boundary between Intenter's core and the
// coding agents it gates. Everything agent-specific — hook payloads, settings
// files, permission rules — lives behind this interface, so the core never
// learns which agent it is protecting (PROTOTYPE_SPEC.md §6.2, INVARIANT I-7).
package adapter

import (
	"context"
	"fmt"
	"io"
	"sort"
)

// IO is the process environment one hook invocation runs in. Passing it
// explicitly keeps adapters testable without touching real stdio.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Env looks up an environment variable; nil means os.Getenv.
	Env func(string) string
}

// Lookup reads an environment variable through the injected accessor.
func (e IO) Lookup(name string) string {
	if e.Env == nil {
		return ""
	}
	return e.Env(name)
}

// Adapter translates one agent's hook protocol into Intenter decisions.
type Adapter interface {
	// Name is the agent identifier used in requests, approvals and the CLI.
	Name() string
	// Run handles exactly one hook invocation. It writes at most one response
	// and MUST NOT report failure to the agent: an adapter that cannot decide
	// stays silent so the agent's own permission flow continues unchanged
	// (INVARIANT I-2, I-12).
	Run(ctx context.Context, env IO) error
}

// Registry maps agent names to adapters.
type Registry struct {
	adapters map[string]Adapter
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]Adapter)}
}

// Register adds an adapter, replacing any previous registration.
func (r *Registry) Register(adapter Adapter) {
	r.adapters[adapter.Name()] = adapter
}

// Get returns the adapter for an agent, or an error naming the known ones.
func (r *Registry) Get(name string) (Adapter, error) {
	adapter, ok := r.adapters[name]
	if !ok {
		return nil, fmt.Errorf("adapter: unknown agent %q (known: %v)", name, r.Names())
	}
	return adapter, nil
}

// Names lists the registered agents, sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.adapters))
	for name := range r.adapters {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
