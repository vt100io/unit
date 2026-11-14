package deploy

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/crypto/ssh"
)

// RecordPath is the server's deploy log: one JSON line per deploy or
// uninstall, for every app on the server. It lives outside the apps'
// directories so an uninstall doesn't erase their history, and it is
// world-readable so tools that only look can read it.
const RecordPath = "/var/log/unit/deploys.jsonl"

// Results of a deploy.
const (
	ResultLive        = "live"        // the new version runs and passed its health check
	ResultRolledBack  = "rolled-back" // it failed, and the previous version runs again
	ResultFailed      = "failed"      // it failed, and nothing was restored
	ResultUninstalled = "uninstalled" // the app was removed
)

// ErrRolledBack is returned when a new version failed and the previous one
// was restored.
var ErrRolledBack = errors.New("deployment failed, but Unit restored the previous version")

// Record is one entry in the server's deploy log. It is an honest account,
// not a tamper-proof one: whoever can deploy has sudo and could edit it.
type Record struct {
	App      string    `json:"app"`
	Action   string    `json:"action"`             // "deploy" or "uninstall"
	Version  string    `json:"version,omitempty"`  // the release: the commit's short hash
	Previous string    `json:"previous,omitempty"` // the release that ran before
	SHA256   string    `json:"sha256,omitempty"`   // of the binary shipped
	CI       string    `json:"ci,omitempty"`       // the CI check's state when deployed, or "skipped"
	Who      string    `json:"who"`                // tailnet login, or local user@host without Tailscale
	From     string    `json:"from,omitempty"`     // the tailnet machine it came from
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Result   string    `json:"result"`
	Error    string    `json:"error,omitempty"`
}

// FileSHA256 returns the hex SHA-256 of the file at path.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Identify returns who is deploying and from which machine. Over Tailscale
// SSH the server answers, from the connection's own address, so the login
// is the one Tailscale vouches for. Otherwise it is the local user@host.
func Identify(client *ssh.Client) (who, from string) {
	if out, err := remoteOutput(client, `tailscale whois --json "${SSH_CLIENT%% *}"`); err == nil {
		if who, from, ok := parseWhois(out); ok {
			return who, from
		}
	}
	name := "unknown"
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	host, _ := os.Hostname()
	return name + "@" + host, ""
}

// parseWhois reads the login and machine name from `tailscale whois --json`.
func parseWhois(out []byte) (who, from string, ok bool) {
	var w struct {
		Node struct {
			ComputedName string
		}
		UserProfile struct {
			LoginName string
		}
	}
	if json.Unmarshal(out, &w) != nil || w.UserProfile.LoginName == "" {
		return "", "", false
	}
	return w.UserProfile.LoginName, w.Node.ComputedName, true
}

// AppendRecord adds r to the server's deploy log. A record that can't be
// written is reported, never fatal: the deploy itself already happened.
func AppendRecord(client *ssh.Client, r Record) {
	line, err := json.Marshal(r)
	if err != nil {
		fmt.Printf("⚠️  Could not record the deploy: %v\n", err)
		return
	}
	session, err := client.NewSession()
	if err != nil {
		fmt.Printf("⚠️  Could not record the deploy: %v\n", err)
		return
	}
	defer session.Close()
	session.Stdin = bytes.NewReader(append(line, '\n'))
	cmd := fmt.Sprintf("sudo install -d -m 0755 /var/log/unit && sudo tee -a %s > /dev/null && sudo chmod 0644 %s", RecordPath, RecordPath)
	if out, err := session.CombinedOutput(cmd); err != nil {
		fmt.Printf("⚠️  Could not record the deploy: %v %s\n", err, strings.TrimSpace(string(out)))
	}
}

// ReadRecords returns the server's deploy log, oldest first, for app, or for
// every app if app is empty. Lines that don't parse are skipped.
func ReadRecords(client *ssh.Client, app string) ([]Record, error) {
	out, err := remoteOutput(client, "cat "+RecordPath+" 2>/dev/null || true")
	if err != nil {
		return nil, err
	}
	return parseRecords(out, app), nil
}

func parseRecords(data []byte, app string) []Record {
	var records []Record
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var r Record
		if json.Unmarshal(scanner.Bytes(), &r) != nil {
			continue
		}
		if app == "" || r.App == app {
			records = append(records, r)
		}
	}
	return records
}

// PrintRecords writes the last n records as a table, newest first.
func PrintRecords(w io.Writer, records []Record, n int) {
	if len(records) == 0 {
		fmt.Fprintln(w, "No deploys recorded.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WHEN\tAPP\tVERSION\tRESULT\tCI\tWHO\tFROM\tTOOK")
	for i := len(records) - 1; i >= 0 && i >= len(records)-n; i-- {
		r := records[i]
		version := r.Version
		if r.Previous != "" && r.Version != "" {
			version = r.Previous + " → " + r.Version
		}
		took := r.Finished.Sub(r.Started).Round(time.Second).String()
		if r.Action == "uninstall" {
			version = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Started.Local().Format("2006-01-02 15:04"), r.App, orDash(version), r.Result, orDash(r.CI), r.Who, orDash(r.From), took)
	}
	tw.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// remoteOutput runs cmd on the server and returns its stdout.
func remoteOutput(client *ssh.Client, cmd string) ([]byte, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return session.Output(cmd)
}

// CurrentRelease returns the release the app runs now (its directory name),
// or "" before the first deploy.
func CurrentRelease(client *ssh.Client, deployPath, name string) string {
	out, err := remoteOutput(client, fmt.Sprintf("readlink -e %s/%s/current", deployPath, name))
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(out))
	return p[strings.LastIndex(p, "/")+1:]
}
