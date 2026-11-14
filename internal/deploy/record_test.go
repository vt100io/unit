package deploy

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseWhois(t *testing.T) {
	out := []byte(`{"Node":{"ComputedName":"elcap"},"UserProfile":{"LoginName":"stianh@github"}}`)
	who, from, ok := parseWhois(out)
	if !ok || who != "stianh@github" || from != "elcap" {
		t.Errorf("got %q %q %v", who, from, ok)
	}
	if _, _, ok := parseWhois([]byte("no such peer")); ok {
		t.Error("garbage parsed as a login")
	}
}

// The log holds every app; history shows one, newest first, and skips
// lines it can't read.
func TestRecordsRoundTrip(t *testing.T) {
	start := time.Date(2026, 11, 14, 21, 0, 0, 0, time.UTC)
	var log bytes.Buffer
	for i, r := range []Record{
		{App: "vt100", Action: "deploy", Version: "8cc5a1f", Who: "stianh@github", From: "elcap", Result: ResultLive},
		{App: "punchcard", Action: "deploy", Version: "94172fb", Previous: "e28ef7b", Who: "stianh@github", From: "elcap", Result: ResultLive},
		{App: "vt100", Action: "deploy", Version: "4201889", Previous: "8cc5a1f", Who: "stianh@github", From: "elcap", Result: ResultRolledBack, CI: "skipped", Error: ErrRolledBack.Error()},
	} {
		r.Started = start.Add(time.Duration(i) * time.Hour)
		r.Finished = r.Started.Add(17 * time.Second)
		line, _ := json.Marshal(r)
		log.Write(append(line, '\n'))
	}
	log.WriteString("{half a line from a crash\n")

	vt100 := parseRecords(log.Bytes(), "vt100")
	if len(vt100) != 2 || vt100[1].Result != ResultRolledBack {
		t.Fatalf("vt100 records: %+v", vt100)
	}
	if all := parseRecords(log.Bytes(), ""); len(all) != 3 {
		t.Errorf("all apps: %d records, want 3", len(all))
	}

	var out bytes.Buffer
	PrintRecords(&out, vt100, 20)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "8cc5a1f → 4201889") || !strings.Contains(lines[1], "rolled-back") {
		t.Errorf("table:\n%s", out.String())
	}
	if !strings.Contains(lines[1], "stianh@github") || !strings.Contains(lines[1], "17s") || !strings.Contains(lines[1], "skipped") {
		t.Errorf("newest row lacks who or duration: %s", lines[1])
	}
}
