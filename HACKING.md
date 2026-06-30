# Hacking on Orchestrator

This document covers running the test suite locally. For day-to-day development
and running tasks, see [README.md](README.md) § [Local Development](README.md#local-development).
The Podman backend is faster for iteration; libvirt is required for integration tests.

## Unit tests

```bash
go test ./...
```

## Integration tests

Integration tests provision real libvirt VMs via gjoll and take up to 30 minutes.
They require hardware virtualization (KVM) and a working libvirt setup.

```bash
go test -tags integration -v -timeout 30m
```

### Prerequisites

- **Go** (1.24+)
- **gjoll**: `go install github.com/ondrejbudai/gjoll/cmd/gjoll@latest`
- **OpenTofu**: `tofu version` must work
- **libvirt** + **qemu-kvm**

### Fedora / RHEL

```bash
sudo dnf install -y libvirt-daemon-kvm qemu-kvm
sudo systemctl enable --now libvirtd
sudo usermod -aG libvirt "$USER"
```

Log out and back in (or use `newgrp libvirt`) so group membership takes effect.

Install OpenTofu from [opentofu.org](https://opentofu.org/docs/intro/install/) if
not already available.

### Start libvirt default network and storage pool

```bash
sudo virsh net-start default
sudo virsh pool-define-as default dir --target /var/lib/libvirt/images
sudo virsh pool-start default
```

If the default network is not set to autostart:

```bash
sudo virsh net-autostart default
sudo virsh pool-autostart default
```

### Run integration tests

From the repository root:

```bash
go test -tags integration -v -timeout 30m .
```

If you see permission errors connecting to libvirt, confirm your user is in the
`libvirt` group and that `virsh list` works without sudo.

### Debian / Ubuntu

The CI workflow (`.github/workflows/integration.yaml`) installs `tofu`,
`libvirt-daemon-system`, and `qemu-kvm` via apt. The libvirt network and pool
setup steps above apply equally on Debian-based systems.
