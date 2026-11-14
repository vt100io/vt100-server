# vt100-server

The one server that runs the vt100 site and Punchcard, declared with [smed](https://github.com/vt100io/smed).

```bash
mise run plan    # what would change at Hetzner and on the server; changes nothing
mise run apply   # create the server, or bring it in line
```

The first `apply` creates the server at Hetzner: a CX33 (4 vCPU, 8 GB) in Helsinki running Debian 13, with a firewall that lets in only ports 80 and 443, my rescue SSH key (`~/.ssh/vt100.pub`) for root instead of an emailed password, and a first boot that joins my tailnet as `tag:vt100` and `tag:ci`. SSH never faces the internet. The second `apply` configures it over Tailscale: git, a C compiler and mise for Punchcard's builds, and the `deploy` user that [unit](https://github.com/vt100io/unit) deploys as.

## Secrets

They live in [pass](https://www.passwordstore.org) or 1Password, never in this repo. With pass:

- `vt100/hetzner-token`: a Read & Write API token from the Hetzner project `vt100`, which holds nothing else
- `vt100/tailscale-authkey`: only needed when creating the server, see below.

With 1Password, set `VT100_SECRETS=op` and store them as **API Credential** items named `vt100-hetzner-token` and `vt100-tailscale-authkey`, in the vault named by `VT100_OP_VAULT` (default `Private`):

```bash
VT100_SECRETS=op mise run plan
```

An environment variable always wins: `VT100_HETZNER_TOKEN` and `VT100_TAILSCALE_AUTHKEY`. That's the way to hand over the Tailscale auth key, which is used once, when the server is created, and isn't worth storing. Make a single-use, pre-approved key tagged `tag:vt100` and `tag:ci` with a one-day expiry, then paste it without it reaching your shell history:

```bash
read -rs VT100_TAILSCALE_AUTHKEY && export VT100_TAILSCALE_AUTHKEY   # paste, then Enter
mise run apply
unset VT100_TAILSCALE_AUTHKEY
```

## Notes

- The `deploy` user has passwordless sudo. unit writes files with `sudo tee` on paths it chooses, which is root in all but name, so a narrower sudo rule would protect nothing. The limit is the tailnet policy: only `tag:ci` reaches the server as `deploy`.
- smed only touches servers labelled `smed=vt100`. A server made by hand in the same project is left alone.
