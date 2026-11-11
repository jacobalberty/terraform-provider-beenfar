package provider

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// Acceptance tests run against a real beenfard on a temporary SQLite
// database. Set TF_ACC=1 and BEENFARD to the path of a beenfard binary
// built from the beenfar version the api module comes from, e.g.
//
//	go build -o /tmp/beenfard github.com/jacobalberty/beenfar/cmd/beenfard
//	TF_ACC=1 BEENFARD=/tmp/beenfard go test ./...
//
// For OpenTofu also set TF_ACC_TERRAFORM_PATH=$(command -v tofu) and
// TF_ACC_PROVIDER_HOST=registry.opentofu.org.

// controller is a running beenfard.
type controller struct {
	URL   string
	Token string
	// Client talks to the controller as the beenfar UI would.
	Client *Client
}

func startController(t *testing.T) *controller {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance tests need TF_ACC=1")
	}
	bin := os.Getenv("BEENFARD")
	if bin == "" {
		t.Fatal("TF_ACC is set but BEENFARD (path to a beenfard binary) is not")
	}
	dir := t.TempDir()
	db := "sqlite://" + filepath.Join(dir, "beenfar.db")

	run := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "BEENFAR_DB="+db)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("beenfard %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("acceptance-password\n", "admin", "create", "-username", "admin")
	token := strings.TrimSpace(lastLine(run("", "admin", "token", "-username", "admin", "-name", "terraform")))

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(os.Environ(),
		"BEENFAR_DB="+db,
		"BEENFAR_LISTEN="+addr,
		"BEENFAR_LOG_FORMAT=text",
		"BEENFAR_LOG_LEVEL=warn",
		// A reset device is offered for re-adoption after one silent second.
		"BEENFAR_UNIFI_READOPT_AFTER=1s",
	)
	logs := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("beenfard log:\n%s", logs)
		}
	})

	url := "http://" + addr
	for i := 0; ; i++ {
		resp, err := http.Get(url + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if i > 200 {
			t.Fatalf("beenfard did not come up: %v\n%s", err, logs)
		}
		time.Sleep(25 * time.Millisecond)
	}
	client, err := NewClient(http.DefaultClient, url, token, defaultTelemetry(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	return &controller{URL: url, Token: token, Client: client}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// providers serves the provider in process to the test framework.
func providers() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"beenfar": providerserver.NewProtocol6WithError(New("test")()),
	}
}

// config prefixes hcl with a provider block for c.
func (c *controller) config(hcl string) string {
	return fmt.Sprintf(`
provider "beenfar" {
  url   = %q
  token = %q
}
`, c.URL, c.Token) + hcl
}

// --- a second Terraform configuration ---

// cliConfig is a separate Terraform configuration, with its own state, run
// with the terraform (or tofu) CLI against the provider binary built from
// this module. It stands for another team's configuration next to the one
// under test.
type cliConfig struct {
	t   *testing.T
	dir string
	env []string
	bin string
}

func newCLIConfig(t *testing.T) *cliConfig {
	t.Helper()
	bin := os.Getenv("TF_ACC_TERRAFORM_PATH")
	if bin == "" {
		for _, name := range []string{"terraform", "tofu"} {
			if p, err := exec.LookPath(name); err == nil {
				bin = p
				break
			}
		}
	}
	if bin == "" {
		t.Skip("no terraform or tofu binary")
	}
	pluginDir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(pluginDir, "terraform-provider-beenfar"), "../..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the provider: %v\n%s", err, out)
	}
	rc := filepath.Join(t.TempDir(), "cli.tfrc")
	if err := os.WriteFile(rc, fmt.Appendf(nil, `provider_installation {
  dev_overrides {
    "registry.terraform.io/jacobalberty/beenfar" = %[1]q
    "registry.opentofu.org/jacobalberty/beenfar" = %[1]q
  }
  direct {}
}
`, pluginDir), 0o600); err != nil {
		t.Fatal(err)
	}
	return &cliConfig{
		t: t, dir: t.TempDir(), bin: bin,
		env: append(os.Environ(), "TF_CLI_CONFIG_FILE="+rc, "TF_IN_AUTOMATION=1", "TF_ACC="),
	}
}

func (c *cliConfig) write(hcl string) {
	c.t.Helper()
	full := `terraform {
  required_providers {
    beenfar = { source = "jacobalberty/beenfar" }
  }
}
` + hcl
	if err := os.WriteFile(filepath.Join(c.dir, "main.tf"), []byte(full), 0o600); err != nil {
		c.t.Fatal(err)
	}
}

func (c *cliConfig) run(args ...string) string {
	c.t.Helper()
	cmd := exec.Command(c.bin, append(args, "-no-color")...)
	cmd.Dir, cmd.Env = c.dir, c.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		c.t.Fatalf("%s %v: %v\n%s", filepath.Base(c.bin), args, err, out)
	}
	return string(out)
}

func (c *cliConfig) apply()   { c.run("apply", "-auto-approve", "-input=false") }
func (c *cliConfig) destroy() { c.run("destroy", "-auto-approve", "-input=false") }

// planIsEmpty reports whether a plan of the configuration has no changes.
func (c *cliConfig) planIsEmpty() (bool, string) {
	c.t.Helper()
	cmd := exec.Command(c.bin, "plan", "-detailed-exitcode", "-input=false", "-no-color")
	cmd.Dir, cmd.Env = c.dir, c.env
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 2 {
		return false, string(out)
	}
	if err != nil {
		c.t.Fatalf("plan: %v\n%s", err, out)
	}
	return true, string(out)
}
