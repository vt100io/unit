// Package ci asks GitHub whether a commit passed its CI check, so unit
// deploys only what CI has passed.
package ci

import (
	"encoding/json"
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
// in repo ("owner/name").
func Status(repo, sha, check string) (Result, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/repos/%s/commits/%s/status", API, repo, sha), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := token(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("GitHub returned %s for %s (a private repo needs GITHUB_TOKEN or gh auth login)", resp.Status, repo)
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

// token is GITHUB_TOKEN, or the GitHub CLI's token if it is logged in, or
// nothing: public repos need none.
func token() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
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
