// Package ci asks GitHub whether a commit passed its CI check, so unit
// deploys only what CI has passed.
package ci

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// API is GitHub's API root; tests point it elsewhere.
var API = "https://api.github.com"

// Result is the state of one check on one commit.
type Result struct {
	State string // success, failure, error, pending, or "" when the check is missing
	URL   string // the build log, if the check links one
}

// Passed reports whether the check passed.
func (r Result) Passed() bool { return r.State == "success" }

// Explain says in one sentence why a deploy may or may not go ahead.
func (r Result) Explain(check, sha string) string {
	short := sha
	if len(short) > 7 {
		short = short[:7]
	}
	link := ""
	if r.URL != "" {
		link = ": " + r.URL
	}
	switch r.State {
	case "success":
		return fmt.Sprintf("%s passed on %s%s", check, short, link)
	case "pending":
		return fmt.Sprintf("%s is still running on %s%s. Deploy when it passes", check, short, link)
	case "":
		return fmt.Sprintf("%s has not reported on %s. Has CI seen this commit?", check, short)
	}
	return fmt.Sprintf("%s %s on %s%s", check, statePast(r.State), short, link)
}

func statePast(state string) string {
	if state == "failure" {
		return "failed"
	}
	return "ended in " + state
}

// Status returns the latest state of the commit status named check on sha
// in repo ("owner/name"). It tries each token it can find, then none: a
// stale token GitHub rejects shouldn't block a public repo.
func Status(repo, sha, check string) (Result, error) {
	var lastErr error
	for _, t := range tokens() {
		r, err := status(repo, sha, check, t.value)
		if err == errUnauthorized {
			fmt.Printf("⚠️  GitHub rejected the token from %s; trying the next way to ask.\n", t.source)
			lastErr = fmt.Errorf("GitHub rejected the token from %s", t.source)
			continue
		}
		return r, err
	}
	return Result{}, lastErr
}

var errUnauthorized = errors.New("401 Unauthorized")

func status(repo, sha, check, token string) (Result, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/repos/%s/commits/%s/status", API, repo, sha), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && token != "" {
		return Result{}, errUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("GitHub returned %s for %s (a private repo needs a valid GITHUB_TOKEN or gh auth login)", resp.Status, repo)
	}
	// The combined status lists the latest status for each context.
	var combined struct {
		Statuses []struct {
			Context   string `json:"context"`
			State     string `json:"state"`
			TargetURL string `json:"target_url"`
		} `json:"statuses"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&combined); err != nil {
		return Result{}, err
	}
	for _, s := range combined.Statuses {
		if s.Context == check {
			return Result{State: s.State, URL: s.TargetURL}, nil
		}
	}
	return Result{}, nil
}

type token struct{ source, value string }

// tokens are the ways to ask GitHub, in order: GITHUB_TOKEN, the GitHub
// CLI's token if it is logged in, and no token, which is enough for public
// repos.
func tokens() []token {
	var ts []token
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		ts = append(ts, token{"GITHUB_TOKEN", t})
	}
	if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
		// gh hands out GITHUB_TOKEN itself when it is set; don't ask twice.
		if t := strings.TrimSpace(string(out)); t != "" && t != os.Getenv("GITHUB_TOKEN") {
			ts = append(ts, token{"gh auth token", t})
		}
	}
	return append(ts, token{"no token", ""})
}

var githubRemote = regexp.MustCompile(`github\.com[:/]([^/]+/[^/]+?)(\.git)?/?$`)

// RepoFromRemote returns "owner/name" from a GitHub remote URL, ssh or https.
func RepoFromRemote(url string) (string, bool) {
	m := githubRemote.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", false
	}
	return m[1], true
}
