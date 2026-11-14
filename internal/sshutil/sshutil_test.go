package sshutil

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// testServer is an SSH server offering both an ECDSA and an ed25519 host
// key, like OpenSSH or Tailscale SSH, and accepting any client.
func testServer(t *testing.T) (addr string, ed25519Key ssh.PublicKey) {
	t.Helper()
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	ecPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	for _, k := range []any{ecPriv, edPriv} {
		signer, err := ssh.NewSignerFromKey(k)
		if err != nil {
			t.Fatal(err)
		}
		cfg.AddHostKey(signer)
	}
	edSigner, _ := ssh.NewSignerFromKey(edPriv)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					c.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					ch.Reject(ssh.Prohibited, "test server")
				}
			}()
		}
	}()
	return l.Addr().String(), edSigner.PublicKey()
}

// fakeHome makes a home directory with a client key and a known_hosts
// file holding lines.
func fakeHome(t *testing.T, lines ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, clientKey, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(clientKey, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "id_ed25519"), pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	var known string
	for _, l := range lines {
		known += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte(known), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// The bug: known_hosts had the server's ed25519 key, the client asked for
// ECDSA first, and the mismatch was reported as a changed host key.
func TestConnectUsesTheKnownKeyType(t *testing.T) {
	addr, edKey := testServer(t)
	fakeHome(t, knownhosts.Line([]string{knownhosts.Normalize(addr)}, edKey))

	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	client, err := Connect("deploy", host, port, true, 5*time.Second)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	client.Close()
}

// A genuinely different key is still refused.
func TestConnectRefusesAChangedKey(t *testing.T) {
	addr, _ := testServer(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherKey, _ := ssh.NewPublicKey(other.Public())
	fakeHome(t, knownhosts.Line([]string{knownhosts.Normalize(addr)}, otherKey))

	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	if _, err := Connect("deploy", host, port, true, 5*time.Second); err == nil {
		t.Fatal("connected to a server whose key isn't the known one")
	}
}

func TestKnownHostKeyAlgorithms(t *testing.T) {
	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	edKey, _ := ssh.NewPublicKey(edPriv.Public())
	home := fakeHome(t, knownhosts.Line([]string{"vt100"}, edKey))
	cb, err := knownhosts.New(filepath.Join(home, ".ssh", "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	if got := knownHostKeyAlgorithms(cb, "vt100:22"); len(got) != 1 || got[0] != ssh.KeyAlgoED25519 {
		t.Errorf("known host: %v", got)
	}
	if got := knownHostKeyAlgorithms(cb, "elsewhere:22"); got != nil {
		t.Errorf("unknown host: %v, want nil (any type)", got)
	}
}
