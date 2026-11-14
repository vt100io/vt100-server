// vt100-server declares the one server that runs the vt100 site and
// Punchcard, and brings Hetzner and the server in line with it.
//
//	go run .          # show what would change, change nothing
//	go run . -apply   # make the changes
//
// Secrets come from pass (vt100/hetzner-token, vt100/tailscale-authkey), or
// from 1Password with VT100_SECRETS=op (see vt100Secret).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/vt100io/smed"
	"github.com/vt100io/smed/cloud/hetzner"
	"github.com/vt100io/smed/connect"
	"github.com/vt100io/smed/module/apt"
	"github.com/vt100io/smed/module/command"
	"github.com/vt100io/smed/module/file"
	"github.com/vt100io/smed/module/user"
	"github.com/vt100io/smed/secret"
	secretcmd "github.com/vt100io/smed/secret/command"
	"github.com/vt100io/smed/secret/env"
	"github.com/vt100io/smed/secret/onepassword"
	"github.com/vt100io/smed/tailnet"
)

const name = "vt100"

// vt100Secret reads one of this server's secrets. An environment variable
// wins if it's set (VT100_TAILSCALE_AUTHKEY for "tailscale-authkey"): the
// way to hand over a single-use key that isn't worth storing. Otherwise
// it comes from pass as vt100/<key>, or with VT100_SECRETS=op from
// 1Password as the "credential" field of the item vt100-<key> in the vault
// $VT100_OP_VAULT (default Private).
func vt100Secret(key string) func(context.Context) (string, error) {
	envName := "VT100_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
	if _, ok := os.LookupEnv(envName); ok {
		return secret.Lazy(env.Resolver{}, secret.Ref(envName))
	}
	if os.Getenv("VT100_SECRETS") == "op" {
		vault := os.Getenv("VT100_OP_VAULT")
		if vault == "" {
			vault = "Private"
		}
		return secret.Lazy(onepassword.Resolver{}, secret.Ref("op://"+vault+"/vt100-"+key+"/credential"))
	}
	pass := secretcmd.Resolver{Command: []string{"pass", "show"}, FirstLine: true}
	return secret.Lazy(pass, secret.Ref("vt100/"+key))
}

// rescueKey is ~/.ssh/vt100.pub. It replaces the root password Hetzner
// would otherwise email, and lets me in through Hetzner's rescue system if
// Tailscale ever doesn't. A public key, safe to commit.
var rescueKey = hetzner.Key{
	Name:      "stian-vt100",
	PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB6dE3n1GppaNnMDyhaGKty6+fPl+t5EvYnVes7/g6wc stian@hegglund.com",
}

// The machine itself, at Hetzner.
var server = hetzner.Server(name).
	Type("cx33").Location("hel1").Image("debian-13").
	SSHKeys(rescueKey).
	OpenPorts(80, 443). // the site and Punchcard's webhook; SSH only over Tailscale
	JoinTailnet(vt100Secret("tailscale-authkey"), "tag:vt100", "tag:ci")

// caddyfile is Caddy's main configuration. unit writes one snippet per app
// into /etc/caddy/conf.d (its domain and port) and reloads Caddy on deploy;
// this file pulls those in. Caddy gets and renews Let's Encrypt
// certificates for every domain by itself.
const caddyfile = `{
	email stian@hegglund.com
}

import /etc/caddy/conf.d/*.conf
`

// What runs on it. unit deploys the site and Punchcard on top of this.
func host() *smed.HostSpec {
	return smed.Host(name).
		// Punchcard's builds need git, and molt's DuckDB driver needs a C compiler.
		Run(apt.Installed{Name: "git"}).
		Run(apt.Installed{Name: "curl"}).
		Run(apt.Installed{Name: "build-essential"}).
		Run(apt.Installed{Name: "sudo"}).
		// mise, for Punchcard to install each repo's tools.
		Run(command.Run{
			Cmd:    "curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh",
			Unless: command.PathExists("/usr/local/bin/mise"),
		}).
		// The user unit deploys as. unit writes unit files and credentials
		// with `sudo tee` on paths it chooses, which is root in all but
		// name, so restricting sudo to its commands would protect nothing.
		// The real limit is the tailnet policy: only tag:ci reaches this
		// server as deploy.
		Run(user.Account{Name: "deploy", Shell: "/bin/bash"}).
		Run(file.Content{Path: "/etc/sudoers.d/deploy", Data: "deploy ALL=(ALL) NOPASSWD: ALL\n", Mode: "0440", Owner: "root", Group: "root"}).
		// HTTPS for vt100.io (the site) and ci.vt100.io (Punchcard).
		Run(apt.Installed{Name: "caddy"}).
		Run(file.Directory{Path: "/etc/caddy/conf.d", Owner: "root", Group: "root", Mode: "0755"}).
		Run(file.Content{Path: "/etc/caddy/Caddyfile", Data: caddyfile, Mode: "0644", Owner: "root", Group: "root"}).
		Notify("reload-caddy").
		Handler("reload-caddy", command.Run{Cmd: "systemctl reload caddy"})
}

func main() {
	apply := flag.Bool("apply", false, "make the changes; without it, only show what would change")
	flag.Parse()
	ctx := context.Background()

	hz := hetzner.Account{
		Token:   vt100Secret("hetzner-token"),
		Project: "vt100",
		Tailnet: tailnet.LocalDaemon(),
	}
	cloud, err := hz.Plan(ctx, server)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print("Hetzner:\n", cloud)
	if *apply && cloud.Pending() {
		if _, err := hz.Apply(ctx, server); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Hetzner is up to date.")
	}

	// The server can only be configured once it exists.
	if cloud.Pending() && !*apply {
		fmt.Println("\nThe server doesn't exist yet; run with -apply to create it, then again to configure it.")
		return
	}
	opts := smed.Options{
		Tailnet: tailnet.LocalDaemon(),
		Rules:   []connect.Rule{{MatchHost: name, User: "root"}},
	}
	h := host()
	plan, err := h.Plan(ctx, opts)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print("\nServer:\n", plan)
	if !*apply || !plan.Drift() {
		return
	}
	results, err := h.Apply(ctx, opts)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(results)
}
