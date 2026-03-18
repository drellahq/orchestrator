# Hacking on the Orchestrator

## Running Unit Tests

```bash
go test ./...
```

## Running Integration Tests on Fedora

The integration tests provision real libvirt VMs via gjoll and exercise
the full MCP server flow (open_pr, comment_on_pr, transcript streaming,
sandbox stop/start). They run fully rootless using libvirt session mode
(`qemu:///session`) with passt user networking — no sudo, group
membership, or polkit rules required at any point.

### One-time setup

Install packages:

```bash
sudo dnf install -y golang libvirt-daemon-kvm libvirt-client qemu-kvm passt opentofu
```

Fedora ships modular libvirt daemons behind socket activation. The
session-mode daemon (virtqemud) starts automatically on first use, but
the terraform-libvirt provider expects the legacy `libvirt-sock` socket
name. Create a symlink (this is per-boot, or add it to your login
scripts):

```bash
ln -sf /run/user/$UID/libvirt/virtqemud-sock /run/user/$UID/libvirt/libvirt-sock
```

Session mode provides a `default` storage pool at
`~/.local/share/libvirt/images` automatically — no manual pool setup is
needed. Networking uses passt (user-space TCP/UDP forwarding), so no
libvirt networks or bridges are required either.

Install gjoll:

```bash
GOPROXY=direct go install github.com/ondrejbudai/gjoll/cmd/gjoll@latest
```

### Running the tests

```bash
go test -tags integration -v -timeout 30m .
```

The tests take around 20 minutes (most of that is gjoll's SSH wait
timeouts, which are harmless) and clean up after themselves.
