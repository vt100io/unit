# unit

> Deployment tool for statically compiled binaries using systemd — no containers, no daemons, just PID 1.

Describe what your app needs in a toml file. unit generates hardened systemd units, ships the binary over SSH, and activates the service.

---

## Background

I build with Go. The stack is deliberately small — Go, htmx, Postgres. Static binaries, no runtime dependencies.

When it came to deployment I kept reaching for containers out of habit. But a statically compiled binary runs on any Linux host as-is. The container adds nothing except complexity: a daemon to manage, an image registry to push to, a base image to pull, a runtime to exec into. I was paying a cost I didn't need to pay.

I got curious: systemd is already running as PID 1 on every Linux server. It has spent two decades accumulating exactly the primitives you'd want for running software safely in production — isolation, secrets, resource limits, socket activation, scheduled jobs. Could it replace the container layer entirely for static binaries?

It can. The gap is ergonomics. A correct, hardened systemd service unit is 40+ lines of directives before you write a single line specific to your app. unit closes that gap — it takes a short description of what your app needs and writes the unit files correctly every time.

This is an experiment born from curiosity, not a prescription. It makes sense when you have a static binary and want to use what Linux already provides rather than adding a layer on top of it.

---

## How it works

unit starts from a default-deny security posture and you whitelist what the app needs:

| What | Default | How to enable |
|---|---|---|
| Outbound network | **Denied** | `allow_outbound_network = true` |
| Filesystem writes | **Denied** | `state_directory`, `logs_directory`, `writable_paths` |
| Running programs from state | **Denied** (noexec) | `allow_state_exec = true` |
| Capabilities | **None** | not configurable — empty by default |
| Inbound connections | Socket activation | `port = <n>` |
| Secrets | Credential files | `secrets = "./secrets.sh"` |
| Memory | Unconstrained | `memory_max = "512M"` |
| CPU | Unconstrained | `cpu_quota = "50%"` |
| Tasks | Unconstrained | `tasks_max = 512` |

---

## Usage

```sh
go install github.com/vt100io/unit/cmd/unit@latest

unit init --name myapp --port 8080   # scaffold a unit.toml
unit setup -e prod                    # provision the server once
unit deploy -e prod                   # ship and activate (-skip-ci to deploy what CI hasn't passed)
unit logs -e prod                     # stream journald output
unit restart -e prod                  # restart without redeploying
unit audit -e prod                    # run systemd-analyze security
unit history -e prod                  # who deployed what, when, and how it went
unit uninstall -e prod                # remove everything
```

Requires Go 1.21+. The binary lands in `$(go env GOPATH)/bin`.

---

## Configuration

`unit.toml` is the base configuration. `unit.<env>.toml` is merged on top — it only needs to contain what differs per environment. Both are committed. Secrets never go in either file, they come from the secrets script. CI deploys by running `unit deploy -e prod` with no additional setup.

```toml
# unit.toml
name       = "api"
executable = "bin/api"
port       = 8080

[sandbox]
allow_outbound_network = true
state_directory        = true   # /var/lib/api, persists across restarts

[resources]
memory_max = "512M"
cpu_quota  = "100%"
tasks_max  = 256

[depends_on]
# requires = hard dependency — if postgres stops, this service stops too
# after    = start ordering only — does not imply dependency
# you typically want both: one for safety, one for correct boot order
after    = ["postgresql.service"]
requires = ["postgresql.service"]
```

```toml
# unit.prod.toml
[server]
host = "prod-1"
user = "deploy"

domain  = "api.example.com"
secrets = "./secrets.prod.sh"
```

---

## Secrets

Provide a script that exports secrets as environment variables. unit runs it locally at deploy time, captures the exports, and uploads them to the server.

The script is safe to commit — it typically just describes how to fetch secrets, not the secrets themselves:

```bash
export DB_PASSWORD=$(op read "op://vault/db/password")
export API_KEY=$(cat ~/.config/myapp/key)
```

On the server, unit writes each secret to `/etc/credentials/<name>/<KEY>` — root-owned, mode 600, inside a directory only root can list. systemd reads these files and injects them into a private memory-backed directory for the service at `$CREDENTIALS_DIRECTORY`. The service reads them as files from there.

This is the recommended secret handling strategy on Linux with systemd. The reason secrets are files rather than environment variables: environment variables are visible in `/proc/<pid>/environ`, leak into core dumps, and are inherited by every child process. A file in a private credential store is scoped to the service and never touches disk.

At runtime:

```go
credDir := os.Getenv("CREDENTIALS_DIRECTORY")
data, _ := os.ReadFile(filepath.Join(credDir, "DB_PASSWORD"))
```

In development there is no `$CREDENTIALS_DIRECTORY`, so the typical pattern is to fall back to a regular environment variable or a local `.env` file:

```go
func secret(name string) string {
    if dir := os.Getenv("CREDENTIALS_DIRECTORY"); dir != "" {
        data, err := os.ReadFile(filepath.Join(dir, name))
        if err == nil {
            return strings.TrimSpace(string(data))
        }
    }
    return os.Getenv(name) // local development fallback
}
```

---

## Scheduled jobs

Each `[[jobs]]` entry generates a `.service` + `.timer` pair. The same binary is invoked with the subcommand as an argument, inheriting the same sandbox and credentials as the main service.

```toml
[[jobs]]
command     = "cleanup"
on_calendar = "daily"
persistent  = true   # catch up on missed runs after downtime
```

---

## HTTPS

Set `domain =` in your environment file and unit writes a Caddy config snippet and reloads it on every deploy. Caddy handles certificate provisioning and renewal automatically — you get HTTPS with no extra steps.

```toml
# unit.prod.toml
domain = "api.example.com"
```

This requires [Caddy](https://caddyserver.com) on the server. If you are not using Caddy, omit `domain =` and handle TLS separately.

---

## Why deploy from a laptop?

Most teams deploy from their CI pipeline, and for good reasons. With many developers, CI keeps production access off fifty laptops and puts it in one controlled place. Every deploy follows the same path and leaves a trail, and deploys don't wait for anyone to be at a desk. It is safe there because hosted CI runs each job on a fresh, isolated machine with short-lived credentials scoped to that job, often behind a required approval for production.

unit deploys from wherever you run it, and I run it from my laptop:

- **There is one of me.** "Keep production access off developer laptops" doesn't apply when the developer is the admin; my laptop can reach the servers anyway.
- **My CI isn't isolated, on purpose.** [Punchcard](https://github.com/vt100io/punchcard) keeps a warm workspace per repo on one machine, which is what makes it fast. It also means every build runs as the same user, so deploy rights given to CI would be given to every build, including whatever a dependency does during one.
- **A person is there when a deploy goes wrong.** unit rolls back a version that fails its health check. One that starts but is wrong needs someone watching.

I keep what CI deploys are good at. The [CI gate](#ci-gate) deploys only commits CI has passed, and [deploy records](#deploy-records) log who deployed what, when, and how it went. CI builds and tests; a person presses the button; the tools do the rest.

I'd move deploys into CI the day a second person should deploy without being root on the servers, deploys must happen while nobody is around, or there are many a day. Then it should be done properly: each build in its own sandbox, a deploy identity no build can read, and deploys only from the default branch after CI passes.

---

## CI gate

Name the GitHub check a commit must have passed, and unit refuses to deploy anything else:

```toml
ci_check = "punchcard/ci"
```

Before shipping, unit asks GitHub for that check on the exact commit it deploys, the one the existing rules already require to be committed and pushed. It goes ahead only if the check passed. A failed check is refused with a link to its build log, a running one with a "deploy when it passes", a missing one with a question. `unit deploy -skip-ci` deploys anyway, and the deploy record says `skipped` where it otherwise says `success`.

### The same binary CI built

A passing check says the *commit* is good, but the binary you ship is built again on your machine. With a reproducible build, both builds give the same bytes, and unit can prove it:

```toml
ci_check    = "punchcard/ci"
ci_checksum = "punchcard/sha256"
```

[Punchcard](https://github.com/vt100io/punchcard) publishes the SHA-256 of each binary a passing build writes to `bin/`, as a commit status named `punchcard/sha256 bin/<name>`. unit compares it with the binary it is about to ship and refuses if they differ, showing both checksums. The deploy record and `unit history` say `same as CI`. Two independent builds have to agree, so neither a compromised CI nor a compromised laptop can slip in a binary alone.

Go builds are reproducible with the same Go version (pin it with mise), the same `go.sum`, `CGO_ENABLED=0` and `-trimpath`, which leaves the build directory's path out of the binary:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/app .
```

Public repos need no token. For private ones unit uses `GITHUB_TOKEN`, or the GitHub CLI's token if `gh` is logged in. CI never needs deploy rights: it reports on GitHub, and the deploy stays with a person.

---

## Deploy records

Every deploy and uninstall is recorded on the server, in `/var/log/unit/deploys.jsonl`: one JSON line with the app, the release and the one before it, the binary's SHA-256, whether CI had passed, who deployed it and from which machine, when, how long it took, and how it went (`live`, `rolled-back`, `failed` or `uninstalled`). One file holds every app on the server, and it survives an app's uninstall.

```
$ unit history
WHEN              APP    VERSION            RESULT  CI       BINARY      WHO            FROM   TOOK
2026-11-14 23:05  vt100  8cc5a1f → 4f44815  live    success  same as CI  stianh@github  elcap  17s
```

`unit history -all` shows every app on the server, `-n 50` more records. Over Tailscale SSH, *who* and *from* are the tailnet login and machine, answered by the server's own `tailscale whois`, not claimed by your laptop; without Tailscale they are your local user and host. The record is an honest log, not a tamper-proof one: whoever can deploy has sudo. It is plain JSON lines, readable by anyone on the server, for `grep`, `jq` and tools that watch servers.

---

## What your app must do

unit runs your binary as a hardened systemd service with socket activation and a watchdog. Three things are expected of it:

- **Use the socket systemd opens.** With `port =`, systemd listens and passes the socket as file descriptor 3 (`LISTEN_FDS=1`). Don't bind the port yourself, or the two collide.
- **Say when it's ready.** The service is `Type=notify`: send `READY=1` to `$NOTIFY_SOCKET` once you're serving. Without it, systemd kills the service after 30 seconds.
- **Keep pinging the watchdog.** `WatchdogSec=10s`: send `WATCHDOG=1` at least every 10 seconds (half of `$WATCHDOG_USEC` is the usual interval), or systemd restarts the service.

In Go, with only the standard library:

```go
func listen() (net.Listener, error) {
    if os.Getenv("LISTEN_PID") == strconv.Itoa(os.Getpid()) && os.Getenv("LISTEN_FDS") == "1" {
        return net.FileListener(os.NewFile(3, "systemd-socket"))
    }
    return net.Listen("tcp", ":8080") // run by hand
}

func notify(state string) { // "READY=1", "WATCHDOG=1"; does nothing outside systemd
    if conn, err := net.Dial("unixgram", os.Getenv("NOTIFY_SOCKET")); err == nil {
        conn.Write([]byte(state))
        conn.Close()
    }
}
```

Call `notify("READY=1")` after `listen`, and `notify("WATCHDOG=1")` from a goroutine on a timer.

## Server requirements

- Linux with systemd and cgroup v2 (Ubuntu 22.04+)
- Deploy user with `sudo` rights for `/etc/systemd/system/` and `systemctl`
- SSH agent (`$SSH_AUTH_SOCK`) with a key authorised on the target

---

## Contributing

`cmd/unit-inspector` is a small HTTP server used to verify that unit configures things correctly. It introspects its own sandbox at runtime — credentials, filesystem restrictions, cgroup limits, socket activation. Useful for development and debugging, not something you'd deploy for your own app.

To run it, create `cmd/unit-inspector/unit.<env>.toml` with your server details (see `unit.example.toml` for the format):

```sh
make build
cd cmd/unit-inspector
unit deploy -e <env>
```
