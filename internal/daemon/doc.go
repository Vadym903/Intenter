// Package daemon runs the single per-user background service: lifecycle,
// single-instance lock, request router, caches and method handlers
// (PROTOTYPE_SPEC.md §9, §10.4).
//
// # Exit codes
//
//	0   an ordinary shutdown: a signal, or a `shutdown` request
//	75  ExitCodeRefresh — the daemon stopped so the service manager would start
//	    it again on a newly installed binary (§9.4, refresh.go)
//
// Every other non-zero status is a startup or serving failure reported by Run.
package daemon
