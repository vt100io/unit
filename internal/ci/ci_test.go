package ci

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRepoFromRemote(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:vt100io/unit.git\n":   "vt100io/unit",
		"https://github.com/vt100io/unit":     "vt100io/unit",
		"https://github.com/vt100io/unit.git": "vt100io/unit",
		"ssh://git@github.com/vt100io/unit/":  "vt100io/unit",
		"git@gitlab.com:vt100io/unit.git":     "",
	} {
		got, ok := RepoFromRemote(url)
		if got != want || ok != (want != "") {
			t.Errorf("%q: got %q %v, want %q", url, got, ok, want)
		}
	}
}

// The combined status holds the latest state per check; only the named
// one counts.
func TestStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/vt100io/unit/commits/abc123/status" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"state":"failure","statuses":[
			{"context":"other/lint","state":"failure"},
			{"context":"punchcard/ci","state":"success","target_url":"https://punchcard.vt100.io/builds/x"}]}`))
	}))
	defer srv.Close()
	old := API
	API = srv.URL
	defer func() { API = old }()
	t.Setenv("GITHUB_TOKEN", "test")
	t.Setenv("PATH", t.TempDir()) // no gh

	r, err := Status("vt100io/unit", "abc123", "punchcard/ci")
	if err != nil || !r.Passed() || r.URL != "https://punchcard.vt100.io/builds/x" {
		t.Errorf("punchcard/ci: %+v %v", r, err)
	}
	if r, _ := Status("vt100io/unit", "abc123", "missing/check"); r.Passed() || r.State != "" {
		t.Errorf("missing check: %+v", r)
	}
	if _, err := Status("vt100io/nope", "abc123", "punchcard/ci"); err == nil {
		t.Error("a 404 must be an error")
	}
}

// A stale token GitHub rejects falls back to asking without one.
func TestStatusRejectedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"statuses":[{"context":"punchcard/ci","state":"success"}]}`))
	}))
	defer srv.Close()
	old := API
	API = srv.URL
	defer func() { API = old }()
	t.Setenv("GITHUB_TOKEN", "stale")
	t.Setenv("PATH", t.TempDir()) // no gh

	if r, err := Status("vt100io/unit", "abc123", "punchcard/ci"); err != nil || !r.Passed() {
		t.Errorf("got %+v %v, want a pass asked without the token", r, err)
	}
}

func TestExplain(t *testing.T) {
	for state, want := range map[string]string{
		"success": "punchcard/ci passed on abc1234",
		"failure": "punchcard/ci failed on abc1234: https://log",
		"pending": "still running",
		"":        "has not reported",
	} {
		if got := (Result{State: state, URL: "https://log"}).Explain("punchcard/ci", "abc1234def"); !strings.Contains(got, want) {
			t.Errorf("%q: %q lacks %q", state, got, want)
		}
	}
}
