//go:build darwin

package platform

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// newServiceManager returns the macOS LaunchAgent manager (research R-09).
func newServiceManager(p Platform, runner CommandRunner) ServiceManager {
	return &launchdService{platform: p, run: runner}
}

// launchdService registers the daemon as a per-user LaunchAgent.
//
// A LaunchAgent runs in the user's own session with no elevation, and launchd
// restarts it if it dies — which is what keeps the gate present without the
// user thinking about it.
type launchdService struct {
	platform Platform
	run      CommandRunner
}

func (s *launchdService) Name() string { return "launchd" }

func (s *launchdService) Available(ctx context.Context) bool {
	_, err := s.run(ctx, "launchctl", "help")
	return err == nil
}

// plistPath is where the LaunchAgent definition lives.
func (s *launchdService) plistPath() string {
	return filepath.Join(s.platform.HomeDir(), "Library", "LaunchAgents", ServiceLabel+".plist")
}

// domainTarget is the launchctl address of this user's session.
func (s *launchdService) domainTarget() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("platform: determine the current user: %w", err)
	}
	return "gui/" + current.Uid, nil
}

func (s *launchdService) Install(ctx context.Context, executable string) error {
	path := s.plistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("platform: create the LaunchAgents directory: %w", err)
	}

	plist, err := s.plist(executable)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, plist, 0o644); err != nil {
		return fmt.Errorf("platform: write %s: %w", path, err)
	}

	target, err := s.domainTarget()
	if err != nil {
		return err
	}
	// Bootstrapping an already-loaded agent fails; replacing it is the
	// idempotent path setup needs (§12.4).
	_, _ = s.run(ctx, "launchctl", "bootout", target+"/"+ServiceLabel)
	if _, err := s.run(ctx, "launchctl", "bootstrap", target, path); err != nil {
		return err
	}
	return nil
}

func (s *launchdService) Uninstall(ctx context.Context) error {
	target, err := s.domainTarget()
	if err != nil {
		return err
	}
	// A service that was never loaded is not an error to remove.
	_, _ = s.run(ctx, "launchctl", "bootout", target+"/"+ServiceLabel)

	if err := os.Remove(s.plistPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("platform: remove %s: %w", s.plistPath(), err)
	}
	return nil
}

func (s *launchdService) Start(ctx context.Context) error {
	target, err := s.domainTarget()
	if err != nil {
		return err
	}
	_, err = s.run(ctx, "launchctl", "kickstart", "-k", target+"/"+ServiceLabel)
	return err
}

func (s *launchdService) Stop(ctx context.Context) error {
	target, err := s.domainTarget()
	if err != nil {
		return err
	}
	_, err = s.run(ctx, "launchctl", "kill", "SIGTERM", target+"/"+ServiceLabel)
	return err
}

func (s *launchdService) Status(ctx context.Context) (ServiceState, error) {
	if _, err := os.Stat(s.plistPath()); os.IsNotExist(err) {
		return ServiceNotInstalled, nil
	}

	target, err := s.domainTarget()
	if err != nil {
		return ServiceNotInstalled, err
	}
	output, err := s.run(ctx, "launchctl", "print", target+"/"+ServiceLabel)
	if err != nil {
		return ServiceStopped, nil
	}
	if strings.Contains(string(output), "state = running") {
		return ServiceRunning, nil
	}
	return ServiceStopped, nil
}

// RegisteredExecutable reads the binary path out of the installed plist: the
// first entry of ProgramArguments, which is what launchd will execute at the
// next login (ServiceInspector).
func (s *launchdService) RegisteredExecutable() (string, bool) {
	content, err := os.ReadFile(s.plistPath())
	if err != nil {
		return "", false
	}
	body := string(content)

	start := strings.Index(body, "<key>ProgramArguments</key>")
	if start < 0 {
		return "", false
	}
	first := strings.Index(body[start:], "<string>")
	if first < 0 {
		return "", false
	}
	first += start + len("<string>")
	end := strings.Index(body[first:], "</string>")
	if end < 0 {
		return "", false
	}
	return xmlUnescape(strings.TrimSpace(body[first : first+end])), true
}

// xmlUnescape reverses xmlEscape for the values this package writes.
func xmlUnescape(value string) string {
	replacer := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&#39;", "'", "&#34;", `"`)
	return replacer.Replace(value)
}

// plist renders the LaunchAgent definition. RunAtLoad starts it with the
// session and KeepAlive brings it back if it exits.
func (s *launchdService) plist(executable string) ([]byte, error) {
	logDir := LogDir(s.platform)
	if err := os.MkdirAll(logDir, DirMode); err != nil {
		return nil, fmt.Errorf("platform: create the log directory: %w", err)
	}

	var body strings.Builder
	body.WriteString(xml.Header)
	body.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	body.WriteString("<plist version=\"1.0\">\n<dict>\n")
	body.WriteString("\t<key>Label</key>\n\t<string>" + ServiceLabel + "</string>\n")
	body.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, argument := range []string{executable, "daemon", "run"} {
		body.WriteString("\t\t<string>" + xmlEscape(argument) + "</string>\n")
	}
	body.WriteString("\t</array>\n")
	body.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	body.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	body.WriteString("\t<key>ProcessType</key>\n\t<string>Background</string>\n")
	body.WriteString("\t<key>StandardOutPath</key>\n\t<string>" +
		xmlEscape(filepath.Join(logDir, "daemon.out.log")) + "</string>\n")
	body.WriteString("\t<key>StandardErrorPath</key>\n\t<string>" +
		xmlEscape(filepath.Join(logDir, "daemon.err.log")) + "</string>\n")
	body.WriteString("</dict>\n</plist>\n")
	return []byte(body.String()), nil
}

// xmlEscape escapes a value for the plist body.
func xmlEscape(value string) string {
	var escaped strings.Builder
	if err := xml.EscapeText(&escaped, []byte(value)); err != nil {
		return value
	}
	return escaped.String()
}
