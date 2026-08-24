package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
	"github.com/Vadym903/Intenter/internal/version"
)

// nodeRepo is a workspace with a package.json and a generated dist directory.
func nodeRepo(t *testing.T, manifest string) *repo {
	t.Helper()
	r := newRepo(t)
	r.write(t, "package.json", manifest)
	if err := os.MkdirAll(filepath.Join(r.root, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	return r
}

func (r *repo) resolve(t *testing.T, command string) *action.ResolvedAction {
	t.Helper()
	return r.resolveIn(t, r.root, command)
}

func (r *repo) resolveIn(t *testing.T, cwd, command string) *action.ResolvedAction {
	t.Helper()
	out, _ := New(r.builder, version.EngineVersion).Resolve(action.ActionRequest{
		Dialect:    action.DialectPosix,
		RawCommand: command,
		Cwd:        cwd,
	})
	if out == nil {
		t.Fatalf("Resolve(%q) returned nothing", command)
	}
	return out
}

// actionEffects renders an action's effects the way effectSummary does.
func actionEffects(out *action.ResolvedAction) []string {
	return effectSummary(action.ResolvedCommand{Effects: out.Effects})
}

func assertActionEffects(t *testing.T, out *action.ResolvedAction, want ...string) {
	t.Helper()
	got := actionEffects(out)
	sortStrings(want)
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("effects = %v, want %v", got, want)
	}
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func fingerprintValue(out *action.ResolvedAction, key string) (string, bool) {
	for _, fingerprint := range out.Fingerprints {
		if fingerprint.Key == key {
			return fingerprint.Value, true
		}
	}
	return "", false
}

func TestNpmRunResolvesTheScriptItExecutes(t *testing.T) {
	r := nodeRepo(t, `{"name":"demo","scripts":{"cleanup":"rm -rf ./dist"}}`)

	out := r.resolve(t, "npm run cleanup")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s), want RESOLVED", out.Status, out.StatusReason)
	}
	if len(out.SemanticOps) != 1 || out.SemanticOps[0] != action.OpRunScript {
		t.Errorf("semantic ops = %v, want [RUN_SCRIPT]", out.SemanticOps)
	}
	assertActionEffects(t, out, "DELETE(force,recursive) ./dist")

	if len(out.Targets()) != 1 {
		t.Fatalf("targets = %+v, want ./dist", out.Targets())
	}
	if scope := out.Targets()[0].Scope; scope != action.ScopeWorkspaceGenerated {
		t.Errorf("scope = %s, want WORKSPACE_GENERATED", scope)
	}
	if _, ok := fingerprintValue(out, "npm-script:package.json#scripts.cleanup"); !ok {
		t.Errorf("fingerprints = %+v, want the script fingerprint", out.Fingerprints)
	}
	if _, ok := fingerprintValue(out, "npm-config:.npmrc#script-shell"); !ok {
		t.Error("the script shell must be fingerprinted (§15.5.1 step 5)")
	}
	if _, ok := fingerprintValue(out, "npm-config:package.json#packageManager"); !ok {
		t.Error("packageManager must be fingerprinted (§15.5.1 step 5)")
	}
}

func TestNpmRunExplanationNamesTheResolvedCommand(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)

	out := r.resolve(t, "npm run cleanup")
	joined := strings.Join(out.Explanation, "\n")
	if !strings.Contains(joined, "npm run cleanup") || !strings.Contains(joined, "rm -rf ./dist") {
		t.Errorf("explanation must show the resolved chain, got %v", out.Explanation)
	}
	if !strings.Contains(joined, "WORKSPACE_GENERATED") {
		t.Errorf("explanation must name the target scope, got %v", out.Explanation)
	}
}

func TestNpmScriptChangeChangesTheFingerprintAndTarget(t *testing.T) {
	// The hypothesis the prototype exists to prove: approving the effect, not
	// the string, means a changed script no longer matches.
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)
	before := r.resolve(t, "npm run cleanup")

	r.write(t, "package.json", `{"scripts":{"cleanup":"rm -rf ~/Documents"}}`)
	after := r.resolve(t, "npm run cleanup")

	beforeHash, _ := fingerprintValue(before, "npm-script:package.json#scripts.cleanup")
	afterHash, _ := fingerprintValue(after, "npm-script:package.json#scripts.cleanup")
	if beforeHash == afterHash {
		t.Error("a changed script must change its fingerprint")
	}
	if before.ActionKey == after.ActionKey {
		t.Error("a changed script must change the action key")
	}
	// On Windows the script is read under cmd.exe as well as under a POSIX
	// shell (§15.5.4), and cmd.exe leaves `~` alone; the POSIX reading is the
	// one that reaches the home directory.
	documents, found := targetByDisplay(after, "~/Documents")
	if !found {
		t.Fatalf("targets = %+v, want ~/Documents", after.Targets())
	}
	if documents.Scope != action.ScopeHome {
		t.Errorf("scope = %s, want HOME", documents.Scope)
	}
	if !documents.HasFlag(action.FlagBroad) {
		t.Error("~/Documents is a standard HOME directory and must be broad")
	}
}

// targetByDisplay finds the target a resolved action shows under a display path.
func targetByDisplay(out *action.ResolvedAction, display string) (action.Target, bool) {
	for _, target := range out.Targets() {
		if target.Display == display {
			return target, true
		}
	}
	return action.Target{}, false
}

func TestNpmRunIncludesPreAndPostScripts(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{
		"prebuild":"rm -rf ./dist",
		"build":"mkdir -p ./dist",
		"postbuild":"cp ./README.md ./dist"
	}}`)

	out := r.resolve(t, "npm run build")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	assertActionEffects(t, out,
		"DELETE(force,recursive) ./dist",
		"CREATE ./dist",
		"READ ./README.md",
		"WRITE ./dist")

	for _, stage := range []string{"prebuild", "build", "postbuild"} {
		if _, ok := fingerprintValue(out, "npm-script:package.json#scripts."+stage); !ok {
			t.Errorf("script %s must be fingerprinted", stage)
		}
	}
}

func TestNpmRunPassthroughArguments(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"clean":"rm -rf"}}`)

	out := r.resolve(t, "npm run clean -- ./dist")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	assertActionEffects(t, out, "DELETE(force,recursive) ./dist")
}

func TestNpmRunResolvesNestedNpmRun(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{
		"cleanup":"npm run clean:dist",
		"clean:dist":"rm -rf ./dist"
	}}`)

	out := r.resolve(t, "npm run cleanup")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	assertActionEffects(t, out, "DELETE(force,recursive) ./dist")
	if _, ok := fingerprintValue(out, "npm-script:package.json#scripts.clean:dist"); !ok {
		t.Errorf("the nested script must be fingerprinted too, got %+v", out.Fingerprints)
	}
}

func TestNpmRunDetectsScriptCycles(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"a":"npm run b","b":"npm run a"}}`)

	out := r.resolve(t, "npm run a")
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED for a script loop", out.Status)
	}
}

func TestNpmRunRefusals(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		command  string
		reason   string
	}{
		{"missing script", `{"scripts":{"build":"tsc"}}`, "npm run cleanup", "cleanup"},
		{"no script name", `{"scripts":{}}`, "npm run", "script name"},
		{"workspace flag", `{"scripts":{"build":"tsc"}}`, "npm run build --workspace=api", "--workspace"},
		{"filter flag", `{"scripts":{"build":"tsc"}}`, "pnpm run build --filter api", "--filter"},
		{"recursive flag", `{"scripts":{"build":"tsc"}}`, "pnpm run build -r", "-r"},
		// AG-140: a directory-redirecting flag points npm at a manifest Intenter
		// never reads, so the resolved script text would not be the one that
		// actually runs.
		{"prefix flag", `{"scripts":{"build":"tsc"}}`, "npm run build --prefix ../other", "--prefix"},
		{"cwd flag", `{"scripts":{"build":"tsc"}}`, "yarn run build --cwd ../other", "--cwd"},
		{"dir flag", `{"scripts":{"build":"tsc"}}`, "pnpm run build --dir ../other", "--dir"},
		// AG-140: these flags replace the .npmrc files Intenter reads to decide
		// the script-shell dialect and the registry, from a source it never sees.
		{"userconfig flag", `{"scripts":{"build":"tsc"}}`, "npm run build --userconfig /tmp/x.npmrc", "--userconfig"},
		{"script-shell override", `{"scripts":{"build":"tsc"}}`, "npm run build --script-shell=/bin/zsh", "--script-shell"},
		{"registry override", `{"scripts":{"build":"tsc"}}`, "npm run build --registry=http://evil.example", "--registry"},
		{"unmodeled subcommand", `{"scripts":{}}`, "npm publish", "publish"},
		{"yarn builtin is not a script", `{"scripts":{"add":"tsc"}}`, "yarn add lodash", "add"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := nodeRepo(t, tt.manifest)
			out := r.resolve(t, tt.command)
			if out.Status != action.StatusUnresolved {
				t.Fatalf("status = %s (%s), want UNRESOLVED", out.Status, out.StatusReason)
			}
			if !strings.Contains(out.StatusReason, tt.reason) {
				t.Errorf("reason = %q, want it to mention %q", out.StatusReason, tt.reason)
			}
		})
	}
}

func TestNpmRunWithoutAPackageJSONIsUnresolved(t *testing.T) {
	r := newRepo(t)

	out := r.resolve(t, "npm run cleanup")
	if out.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED", out.Status)
	}
	if !strings.Contains(out.StatusReason, "package.json") {
		t.Errorf("reason = %q, want it to mention package.json", out.StatusReason)
	}
}

func TestNpmRunUsesTheNearestPackageJSONBoundedByTheWorkspace(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)
	nested := filepath.Join(r.root, "packages", "api")
	if err := os.MkdirAll(filepath.Join(nested, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	r.write(t, "packages/api/package.json", `{"scripts":{"cleanup":"rm -rf ./build"}}`)

	out := r.resolveIn(t, nested, "npm run cleanup")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}
	if got := out.DisplayTargets(); len(got) != 1 || got[0] != "./packages/api/build" {
		t.Errorf("targets = %v, want the nested package's script target", got)
	}
	if _, ok := fingerprintValue(out, "npm-script:packages/api/package.json#scripts.cleanup"); !ok {
		t.Errorf("the fingerprint key must embed the workspace-relative path, got %+v", out.Fingerprints)
	}
}

func TestNpmLifecycleSubcommands(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"test":"rm -rf ./dist","start":"mkdir -p ./dist"}}`)

	for _, command := range []string{"npm test", "npm t", "npm tst", "yarn test", "pnpm test"} {
		out := r.resolve(t, command)
		if out.Status != action.StatusResolved {
			t.Errorf("%q: status = %s (%s)", command, out.Status, out.StatusReason)
			continue
		}
		assertActionEffects(t, out, "DELETE(force,recursive) ./dist")
	}

	start := r.resolve(t, "npm start")
	assertActionEffects(t, start, "CREATE ./dist")
}

func TestPnpmAndYarnScriptShorthand(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)

	for _, command := range []string{"pnpm cleanup", "yarn cleanup", "pnpm run cleanup", "yarn run cleanup"} {
		out := r.resolve(t, command)
		if out.Status != action.StatusResolved {
			t.Errorf("%q: status = %s (%s)", command, out.Status, out.StatusReason)
			continue
		}
		assertActionEffects(t, out, "DELETE(force,recursive) ./dist")
	}
}

func TestNpmInstallRunsLifecycleScripts(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)
	r.write(t, "package-lock.json", "{}")

	out := r.resolve(t, "npm install")
	if out.Status != action.StatusUnresolved {
		t.Fatalf("status = %s, want UNRESOLVED without --ignore-scripts (§15.4)", out.Status)
	}
	if !strings.Contains(out.StatusReason, "lifecycle") {
		t.Errorf("reason = %q, want it to mention lifecycle scripts", out.StatusReason)
	}

	ignored := r.resolve(t, "npm install --ignore-scripts")
	if ignored.Status != action.StatusDeclared {
		t.Fatalf("status = %s (%s), want DECLARED with --ignore-scripts", ignored.Status, ignored.StatusReason)
	}

	network := false
	for _, effect := range ignored.Effects {
		if effect.Type == action.EffectNetwork && effect.Network.DeclaredKind == "dependency-registry" {
			network = true
		}
	}
	if !network {
		t.Errorf("effects = %v, want a dependency-registry network effect", actionEffects(ignored))
	}

	targets := ignored.DisplayTargets()
	for _, want := range []string{"./node_modules", "./package.json", "./package-lock.json"} {
		if !containsString(targets, want) {
			t.Errorf("targets = %v, want %s", targets, want)
		}
	}
}

func TestNpmInstallAliases(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	for _, command := range []string{"npm i", "npm ci", "npm add lodash", "yarn add lodash", "pnpm add lodash", "npm uninstall lodash"} {
		out := r.resolve(t, command)
		if out.Status != action.StatusUnresolved {
			t.Errorf("%q: status = %s, want UNRESOLVED", command, out.Status)
		}
		if len(out.SemanticOps) != 1 || out.SemanticOps[0] != action.OpInstallDependencies {
			t.Errorf("%q: semantic ops = %v, want [INSTALL_DEPENDENCIES]", command, out.SemanticOps)
		}
	}
}

// AG-140: npmInstall never checked the workspace-, directory- or
// config-redirecting flags npmRunScript already refused, so `npm install
// --workspace=other`, `npm install --prefix ../other` and similar could
// install (and run the lifecycle scripts of) an entirely different project
// than the one whose package.json, lockfile and node_modules Intenter
// declared as the targets.
func TestNpmInstallRefusesWorkspaceDirectoryAndConfigFlags(t *testing.T) {
	tests := []struct {
		name    string
		command string
		reason  string
	}{
		{"workspace flag", "npm install --workspace=other", "--workspace"},
		{"prefix flag", "npm install --prefix ../other", "--prefix"},
		{"cwd flag", "yarn install --cwd ../other", "--cwd"},
		{"dir flag", "pnpm install --dir ../other", "--dir"},
		{"userconfig flag", "npm install --userconfig /tmp/x.npmrc", "--userconfig"},
		{"registry override", "npm install --registry=http://evil.example", "--registry"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := nodeRepo(t, `{"scripts":{}}`)
			out := r.resolve(t, tt.command)
			if out.Status != action.StatusUnresolved {
				t.Fatalf("status = %s (%s), want UNRESOLVED", out.Status, out.StatusReason)
			}
			if !strings.Contains(out.StatusReason, tt.reason) {
				t.Errorf("reason = %q, want it to mention %q", out.StatusReason, tt.reason)
			}
		})
	}
}

func TestNpxResolvesOnlyLocallyInstalledBinaries(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	remote := r.resolve(t, "npx create-react-app my-app")
	if remote.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED for a package that is not installed", remote.Status)
	}
	if !strings.Contains(remote.StatusReason, "download") {
		t.Errorf("reason = %q, want it to explain the download", remote.StatusReason)
	}

	// A binary already in node_modules/.bin is a normal local invocation.
	binDir := filepath.Join(r.root, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "rimraf"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	local := r.resolve(t, "npx rimraf ./dist")
	// rimraf itself has no recognizer yet (T047), so the action stays
	// UNRESOLVED, but for the right reason: the program, not the download.
	if strings.Contains(local.StatusReason, "download") {
		t.Errorf("a locally installed binary must not be reported as a download, got %q", local.StatusReason)
	}
}

// npxRimraf writes a fake local `node_modules/.bin/rimraf`, the same shim the
// real npx would find before ever downloading anything.
func npxRimraf(t *testing.T, r *repo) {
	t.Helper()
	binDir := filepath.Join(r.root, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "rimraf"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// AG-141: npx used to strip every option-shaped word anywhere in its
// arguments, including ones written before the package name that were never
// npx's own — `--prefix`, `--workspace` and the like passed straight through
// unexamined. They must now refuse like the run/install paths do.
func TestNpxRefusesUnmodeledLeadingFlags(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)
	npxRimraf(t, r)

	tests := []struct {
		name    string
		command string
		reason  string
	}{
		{"prefix", "npx --prefix /tmp/evil rimraf ./dist", "--prefix"},
		{"workspace", "npm exec --workspace=other -- rimraf ./dist", "--workspace"},
		{"unknown flag", "npx --some-unknown-flag rimraf ./dist", "--some-unknown-flag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.resolve(t, tt.command)
			if out.Status != action.StatusUnresolved {
				t.Fatalf("status = %s (%s), want UNRESOLVED", out.Status, out.StatusReason)
			}
			if !strings.Contains(out.StatusReason, tt.reason) {
				t.Errorf("reason = %q, want it to mention %q", out.StatusReason, tt.reason)
			}
		})
	}
}

// A leading flag that is genuinely npx's own and behavior-neutral (`-y`) must
// still resolve normally.
func TestNpxAllowsItsOwnSafeLeadingFlags(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)
	npxRimraf(t, r)

	out := r.resolve(t, "npx -y rimraf ./dist")
	if out.Status != action.StatusDeclared {
		t.Fatalf("status = %s (%s), want DECLARED", out.Status, out.StatusReason)
	}
}

// AG-141, the critical instance: npx used to drop every option-shaped
// argument anywhere in the command, not only ones before the package name —
// so an argument meant for the invoked program vanished before that
// program's own recognizer ever saw it, and the resolved command no longer
// matched what npx would really run. Preserving it lets the child's own
// grammar refuse what it does not model, instead of Intenter silently
// approving a shorter command than the real one.
func TestNpxPreservesArgumentsAfterThePackageName(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)
	npxRimraf(t, r)

	out := r.resolve(t, "npx rimraf --an-unknown-flag ./dist")
	if out.Status != action.StatusUnresolved {
		t.Fatalf("status = %s (%s), want UNRESOLVED: an argument after the package name must reach "+
			"rimraf's own grammar rather than being silently dropped", out.Status, out.StatusReason)
	}
	if !strings.Contains(out.StatusReason, "--an-unknown-flag") {
		t.Errorf("reason = %q, want it to name the flag rimraf's own grammar refuses", out.StatusReason)
	}
}

// AG-142, critical: a `.yarnrc.yml` yarnPath override must take every `yarn`
// invocation out of resolution, not only ones that would otherwise be an
// ordinary DECLARED build-tool run — a script whose text is entirely
// read-only stays RESOLVED and can be auto-allowed by the baseline (§18.3
// B1), exactly the AG-01 shape: a config-file execution redirect the resolver
// never sees.
func TestYarnPathMakesEveryYarnCommandUnresolved(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"test":"cat package.json"}}`)
	r.write(t, ".yarnrc.yml", "yarnPath: .yarn/releases/yarn-3.6.1.cjs\n")

	// Before the fix, this script's only effect is a workspace-scoped read,
	// which the read-only baseline auto-allows with zero prompts — while the
	// pinned yarn release, entirely unmodeled, is what would really run.
	out := r.resolve(t, "yarn test")
	if out.Status != action.StatusUnresolved {
		t.Fatalf("status = %s (%s), want UNRESOLVED: a pinned yarn release must not be trusted to behave like the modeled script runner", out.Status, out.StatusReason)
	}
	if !strings.Contains(out.StatusReason, "yarnPath") && !strings.Contains(out.StatusReason, ".yarn/releases/yarn-3.6.1.cjs") {
		t.Errorf("reason = %q, want it to name the yarnPath override", out.StatusReason)
	}

	// A workspace without a yarnPath override is unaffected.
	plain := nodeRepo(t, `{"scripts":{"test":"cat package.json"}}`)
	unaffected := plain.resolve(t, "yarn test")
	if unaffected.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s), want RESOLVED without a yarnPath override", unaffected.Status, unaffected.StatusReason)
	}

	// npm and pnpm, which never read .yarnrc.yml's yarnPath, are unaffected by
	// a workspace that happens to also carry one.
	npmStillResolves := r.resolve(t, "npm run test")
	if npmStillResolves.Status != action.StatusResolved {
		t.Errorf("status = %s (%s), want npm unaffected by yarn's yarnPath", npmStillResolves.Status, npmStillResolves.StatusReason)
	}
}

func TestNpmScriptShellConfigurationIsHonored(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)
	r.write(t, ".npmrc", "script-shell=/usr/bin/bash\n")

	out := r.resolve(t, "npm run cleanup")
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s)", out.Status, out.StatusReason)
	}

	r.write(t, ".npmrc", "script-shell=/opt/weird/fish\n")
	unknown := r.resolve(t, "npm run cleanup")
	if unknown.Status != action.StatusUnresolved {
		t.Errorf("status = %s, want UNRESOLVED for an unrecognized script-shell (§15.5.4)", unknown.Status)
	}
}

func TestNpmScriptShellChangeChangesTheFingerprint(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)
	before := r.resolve(t, "npm run cleanup")

	r.write(t, ".npmrc", "script-shell=/bin/bash\n")
	after := r.resolve(t, "npm run cleanup")

	beforeHash, _ := fingerprintValue(before, "npm-config:.npmrc#script-shell")
	afterHash, _ := fingerprintValue(after, "npm-config:.npmrc#script-shell")
	if beforeHash == afterHash {
		t.Error("configuring a script shell must change its fingerprint (§15.5.1 step 5)")
	}
	if before.ActionKey == after.ActionKey {
		t.Error("a changed script shell must change the action key")
	}
}

func TestNpmScriptWithUnsupportedSyntaxIsRefused(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf $(cat target.txt)"}}`)

	out := r.resolve(t, "npm run cleanup")
	if out.Status.Approvable() {
		t.Errorf("status = %s, want a non-approvable status for a script using command substitution", out.Status)
	}
}

func TestNpmScriptInheritsTheManifestDirectory(t *testing.T) {
	// A script runs in the directory of its package.json, not the shell's cwd.
	r := nodeRepo(t, `{"scripts":{"cleanup":"rm -rf ./dist"}}`)
	nested := filepath.Join(r.root, "src", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out := r.resolveIn(t, nested, "npm run cleanup")
	if got := out.DisplayTargets(); len(got) != 1 || got[0] != "./dist" {
		t.Errorf("targets = %v, want ./dist relative to the manifest", got)
	}
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
