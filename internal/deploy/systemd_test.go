package deploy

import (
	"errors"
	"testing"
)

func TestPreviousReleasePath(t *testing.T) {
	const releases = "/opt/unit/vt100/releases"
	for _, tc := range []struct {
		name   string
		output string
		err    error
		want   string
	}{
		{"an earlier release", releases + "/7cf596e\n", nil, releases + "/7cf596e"},
		{"first deploy: no link", "", errors.New("exit status 1"), ""},
		// What readlink -f printed on a first deploy, which made the
		// rollback point current at itself.
		{"the link's own path", "/opt/unit/vt100/current\n", nil, ""},
		{"somewhere else", "/tmp/elsewhere\n", nil, ""},
	} {
		if got := previousReleasePath(tc.output, tc.err, releases); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
