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
