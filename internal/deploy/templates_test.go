package deploy

import (
	"strings"
	"testing"
	"text/template"

	"github.com/vt100io/unit/internal/config"
)

func render(t *testing.T, templ string, data any) string {
	t.Helper()
	var b strings.Builder
	if err := template.Must(template.New("t").Parse(templ)).Execute(&b, data); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// systemd 257 mounts a DynamicUser state directory noexec; ExecPaths= is the
// only way to run what the service installs there.
func TestAllowStateExec(t *testing.T) {
	for _, allow := range []bool{false, true} {
		sandbox := config.SandboxConfig{StateDirectory: true, AllowStateExec: allow}
		service := render(t, ServiceTemplate, &config.Config{Name: "ci", Executable: "bin/ci", Sandbox: sandbox})
		job := render(t, JobServiceTemplate, jobTemplateData{Name: "ci", Sandbox: sandbox, Job: config.JobConfig{Command: "nightly"}})
		for name, unit := range map[string]string{"service": service, "job": job} {
			if got := strings.Contains(unit, "ExecPaths=/var/lib/ci\n"); got != allow {
				t.Errorf("%s with allow_state_exec=%v: ExecPaths present = %v", name, allow, got)
			}
		}
	}
}

// ProcSubset=pid hides /proc/loadavg, /proc/meminfo and /proc/stat; a
// monitoring tool needs them, and only it should get them.
func TestAllowSystemStats(t *testing.T) {
	for _, allow := range []bool{false, true} {
		sandbox := config.SandboxConfig{AllowSystemStats: allow}
		service := render(t, ServiceTemplate, &config.Config{Name: "stats", Executable: "bin/stats", Sandbox: sandbox})
		job := render(t, JobServiceTemplate, jobTemplateData{Name: "stats", Sandbox: sandbox, Job: config.JobConfig{Command: "report"}})
		for name, unit := range map[string]string{"service": service, "job": job} {
			if got := strings.Contains(unit, "ProcSubset=pid"); got == allow {
				t.Errorf("%s with allow_system_stats=%v: ProcSubset=pid present = %v", name, allow, got)
			}
			if !strings.Contains(unit, "ProtectProc=invisible") {
				t.Errorf("%s with allow_system_stats=%v: ProtectProc=invisible missing", name, allow)
			}
		}
	}
}

// A port without a domain must not end up on the public interface by
// accident: the default is loopback, and only "all" binds everywhere.
func TestSocketListen(t *testing.T) {
	cases := []struct {
		listen string
		want   []string
		absent []string
	}{
		{"", []string{"ListenStream=127.0.0.1:5001\n"}, []string{"BindToDevice"}},
		{config.ListenLocalhost, []string{"ListenStream=127.0.0.1:5001\n"}, []string{"BindToDevice"}},
		{config.ListenTailnet, []string{"ListenStream=5001\n", "BindToDevice=tailscale0\n"}, []string{"127.0.0.1"}},
		{config.ListenAll, []string{"ListenStream=5001\n"}, []string{"BindToDevice", "127.0.0.1"}},
	}
	for _, c := range cases {
		socket := render(t, SocketTemplate, &config.Config{Name: "app", Port: 5001, Listen: c.listen})
		for _, w := range c.want {
			if !strings.Contains(socket, w) {
				t.Errorf("listen=%q: socket lacks %q:\n%s", c.listen, w, socket)
			}
		}
		for _, a := range c.absent {
			if strings.Contains(socket, a) {
				t.Errorf("listen=%q: socket has %q:\n%s", c.listen, a, socket)
			}
		}
	}
}

// Caddy must dial the address the socket binds, and send the headers.
func TestCaddySnippet(t *testing.T) {
	snippet := render(t, CaddySnippetTemplate, struct {
		Domain string
		Port   int
	}{"example.com, www.example.com", 5001})
	for _, w := range []string{
		"example.com, www.example.com {",
		"reverse_proxy 127.0.0.1:5001",
		`Strict-Transport-Security "max-age=31536000"`,
		`X-Content-Type-Options "nosniff"`,
		`Referrer-Policy "strict-origin-when-cross-origin"`,
		"-Server",
	} {
		if !strings.Contains(snippet, w) {
			t.Errorf("snippet lacks %q:\n%s", w, snippet)
		}
	}
	if strings.Contains(snippet, "localhost") {
		t.Errorf("snippet dials localhost, which may resolve to ::1:\n%s", snippet)
	}
}
