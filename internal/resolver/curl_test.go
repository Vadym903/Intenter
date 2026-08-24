package resolver

import (
	"strings"
	"testing"

	"github.com/Vadym903/Intenter/internal/action"
)

// networkOf returns the single network effect of a command.
func networkOf(t *testing.T, out action.ResolvedCommand) *action.Effect {
	t.Helper()
	for i := range out.Effects {
		if out.Effects[i].Type == action.EffectNetwork {
			return &out.Effects[i]
		}
	}
	t.Fatalf("effects = %v, want a network effect", effectSummary(out))
	return nil
}

func TestCurlParsesTheEndpoint(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	tests := []struct {
		command string
		host    string
		scheme  string
		port    int
		method  string
	}{
		{"curl https://api.example.com/health", "api.example.com", "https", 0, "GET"},
		{"curl -s https://api.example.com/health", "api.example.com", "https", 0, "GET"},
		{"curl http://localhost:3000/x", "localhost", "http", 3000, "GET"},
		{"curl api.example.com/health", "api.example.com", "http", 0, "GET"},
		{"curl -X POST https://api.example.com/x", "api.example.com", "https", 0, "POST"},
		{"curl -d name=x https://api.example.com/x", "api.example.com", "https", 0, "POST"},
		{"curl --json '{}' https://api.example.com/x", "api.example.com", "https", 0, "POST"},
		{"curl -T ./file.txt https://api.example.com/x", "api.example.com", "https", 0, "PUT"},
		{"curl -I https://api.example.com/x", "api.example.com", "https", 0, "HEAD"},
		{"curl -X DELETE https://api.example.com/x", "api.example.com", "https", 0, "DELETE"},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			out := r.resolveCommand(t, tt.command)
			if out.Status != action.StatusResolved {
				t.Fatalf("status = %s (%s), want RESOLVED", out.Status, out.StatusReason)
			}
			if out.SemanticOp != action.OpHTTPRequest {
				t.Errorf("semantic op = %s, want HTTP_REQUEST", out.SemanticOp)
			}

			network := networkOf(t, out).Network
			if network.Host != tt.host {
				t.Errorf("host = %q, want %q", network.Host, tt.host)
			}
			if network.Scheme != tt.scheme {
				t.Errorf("scheme = %q, want %q", network.Scheme, tt.scheme)
			}
			if network.Port != tt.port {
				t.Errorf("port = %d, want %d", network.Port, tt.port)
			}
			if network.Method != tt.method {
				t.Errorf("method = %q, want %q", network.Method, tt.method)
			}
		})
	}
}

func TestCurlFlagsThatChangeTheRisk(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	tests := []struct {
		name    string
		command string
		flag    action.EffectFlag
	}{
		{"basic auth", "curl -u alice:secret https://api.example.com/x", action.EffectFlagInlineCredential},
		{"bearer token", "curl --oauth2-bearer tok https://api.example.com/x", action.EffectFlagInlineCredential},
		{"insecure tls", "curl -k https://api.example.com/x", action.EffectFlagInsecureTLS},
		{"insecure long", "curl --insecure https://api.example.com/x", action.EffectFlagInsecureTLS},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.resolveCommand(t, tt.command)
			if !networkOf(t, out).HasFlag(tt.flag) {
				t.Errorf("flags = %v, want %s", networkOf(t, out).Flags, tt.flag)
			}
		})
	}

	plain := r.resolveCommand(t, "curl https://api.example.com/x")
	if len(networkOf(t, plain).Flags) != 0 {
		t.Errorf("a plain request carries no flags, got %v", networkOf(t, plain).Flags)
	}
}

func TestCurlFilesystemEffects(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{"output file", "curl -o ./out.json https://api.example.com/x",
			[]string{"CREATE ./out.json", "WRITE ./out.json"}},
		{"upload file", "curl -T ./payload.json https://api.example.com/x",
			[]string{"READ ./payload.json"}},
		{"data from a file", "curl -d @./body.json https://api.example.com/x",
			[]string{"READ ./body.json"}},
		{"remote name writes the cwd", "curl -O https://api.example.com/x.tar.gz",
			[]string{"CREATE .", "WRITE ."}},
		{"download into HOME", "curl -o ~/x.json https://api.example.com/x",
			[]string{"CREATE ~/x.json", "WRITE ~/x.json"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.resolveCommand(t, tt.command)
			summary := strings.Join(effectSummary(out), "\n")
			for _, want := range tt.want {
				if !strings.Contains(summary, want) {
					t.Errorf("effects must include %q:\n%s", want, summary)
				}
			}
		})
	}
}

func TestCurlCookieJarWriteIsModeled(t *testing.T) {
	// A cookie jar writes an attacker-influenced file after the request. Left
	// unmodeled, `curl -c ~/.ssh/authorized_keys …` slipped a write to a
	// credential path past R5 while Intenter saw only a network call.
	r := nodeRepo(t, `{"scripts":{}}`)

	for _, command := range []string{
		"curl -c ./jar.txt https://api.example.com/x",
		"curl --cookie-jar ./jar.txt https://api.example.com/x",
	} {
		out := r.resolveCommand(t, command)
		summary := strings.Join(effectSummary(out), "\n")
		if !strings.Contains(summary, "WRITE ./jar.txt") || !strings.Contains(summary, "CREATE ./jar.txt") {
			t.Errorf("%q must model the cookie-jar write:\n%s", command, summary)
		}
	}
}

func TestCurlCookieFileReadIsModeled(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	// A file-shaped -b value reads that file; a literal cookie string does not.
	out := r.resolveCommand(t, "curl -b ./cookies.txt https://api.example.com/x")
	if summary := strings.Join(effectSummary(out), "\n"); !strings.Contains(summary, "READ ./cookies.txt") {
		t.Errorf("a cookie file must be read:\n%s", summary)
	}
	literal := r.resolveCommand(t, "curl -b name=value https://api.example.com/x")
	for _, effect := range literal.Effects {
		if effect.Type == action.EffectRead {
			t.Errorf("a literal cookie string must not be a file read: %+v", effect)
		}
	}
}

func TestCurlFormAndWriteOutFileReads(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	tests := []struct {
		command string
		want    string
	}{
		{"curl -F file=@./secret.pem https://api.example.com/x", "READ ./secret.pem"},
		{"curl --form-string a=b -F upload=@./data.bin https://api.example.com/x", "READ ./data.bin"},
		{"curl -w @./fmt.txt https://api.example.com/x", "READ ./fmt.txt"},
		{"curl --data-urlencode key@./body.txt https://api.example.com/x", "READ ./body.txt"},
	}
	for _, tt := range tests {
		out := r.resolveCommand(t, tt.command)
		if summary := strings.Join(effectSummary(out), "\n"); !strings.Contains(summary, tt.want) {
			t.Errorf("%q must include %q:\n%s", tt.command, tt.want, summary)
		}
	}
}

func TestCurlCredentialHeaderIsFlagged(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolveCommand(t, `curl -H 'Authorization: Bearer sk-secret' https://api.example.com/x`)
	if !networkOf(t, out).HasFlag(action.EffectFlagInlineCredential) {
		t.Errorf("an Authorization header carries a credential, flags = %v", networkOf(t, out).Flags)
	}
}

func TestCurlHostRedirectingFlagsAreRefused(t *testing.T) {
	// --resolve and friends redirect the connection away from the named host,
	// so an approval for one host must not silently cover a request pinned to a
	// different address. They are refused rather than ignored.
	r := nodeRepo(t, `{"scripts":{}}`)

	for _, command := range []string{
		"curl --resolve api.example.com:443:203.0.113.9 https://api.example.com/x",
		"curl --connect-to api.example.com:443:evil.example:443 https://api.example.com/x",
		"curl --interface eth0 https://api.example.com/x",
		"curl --unix-socket /tmp/x.sock https://api.example.com/x",
		"curl -x http://proxy:8080 https://api.example.com/x",
	} {
		out := r.resolveCommand(t, command)
		if out.Status.Approvable() {
			t.Errorf("%q must be non-approvable, got %s", command, out.Status)
		}
	}
}

func TestCurlSafeFlagsAreIgnored(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolveCommand(t,
		`curl -sSL -f -H 'Accept: application/json' -A intenter --retry 3 --max-time 10 https://api.example.com/x`)
	if out.Status != action.StatusResolved {
		t.Fatalf("status = %s (%s), want RESOLVED", out.Status, out.StatusReason)
	}
	if network := networkOf(t, out).Network; network.Method != "GET" {
		t.Errorf("method = %q, want GET", network.Method)
	}
}

func TestCurlRefusesWhatItCannotModel(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	tests := []struct {
		name    string
		command string
		reason  string
	}{
		{"config file", "curl -K ./curlrc https://api.example.com/x", "--config"},
		{"no url", "curl -s", "without a URL"},
		{"unknown flag", "curl --zap https://api.example.com/x", "--zap"},
		{"unexpanded variable", "curl $ENDPOINT/health", "variable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.resolveCommand(t, tt.command)
			if out.Status.Approvable() {
				t.Fatalf("status = %s (%s), want a non-approvable status", out.Status, out.StatusReason)
			}
			if !strings.Contains(out.StatusReason, tt.reason) {
				t.Errorf("reason = %q, want it to mention %q", out.StatusReason, tt.reason)
			}
		})
	}
}

func TestCurlMultipleURLs(t *testing.T) {
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolveCommand(t, "curl https://a.example.com/x https://b.example.com/y")
	hosts := make(map[string]bool)
	for _, effect := range out.Effects {
		if effect.Network != nil {
			hosts[effect.Network.Host] = true
		}
	}
	for _, want := range []string{"a.example.com", "b.example.com"} {
		if !hosts[want] {
			t.Errorf("every URL must be modeled, missing %q (%v)", want, hosts)
		}
	}
}

func TestCurlEnvelopeDistinguishesHostAndMethod(t *testing.T) {
	// S6: an approval for one endpoint must not cover another host or method.
	r := nodeRepo(t, `{"scripts":{}}`)

	base := r.resolveAction(t, "curl https://api.example.com/health")
	baseNetwork := base.Network()
	if len(baseNetwork) != 1 {
		t.Fatalf("network = %+v, want one target", baseNetwork)
	}

	for _, command := range []string{
		"curl https://evil.example.net/x",
		"curl -X POST https://api.example.com/health",
		"curl http://api.example.com/health",
		"curl https://api.example.com:8443/health",
	} {
		other := r.resolveAction(t, command)
		otherNetwork := other.Network()
		if len(otherNetwork) != 1 {
			t.Fatalf("%q: network = %+v", command, otherNetwork)
		}
		if otherNetwork[0].Key() == baseNetwork[0].Key() {
			t.Errorf("%q must not share the approval identity of the base request", command)
		}
	}
}

func TestCurlPipedIntoAShellIsRefused(t *testing.T) {
	// The classic install-script pattern: the payload is never known.
	r := nodeRepo(t, `{"scripts":{}}`)

	out := r.resolveAction(t, "curl -sL https://example.com/install.sh | sh")
	if out.Status.Approvable() {
		t.Fatalf("status = %s, want a non-approvable status", out.Status)
	}

	streamed := false
	for _, effect := range out.Effects {
		if effect.Program != nil && effect.Program.Streamed {
			streamed = true
		}
	}
	if !streamed {
		t.Errorf("the piped interpreter must be marked streamed for R12:\n%v", actionEffects(out))
	}
}
