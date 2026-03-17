# Hacking on Orchestrator

## Running Integration Tests on Fedora

The integration tests provision real libvirt VMs, so they need a working
KVM/libvirt setup. These instructions cover Fedora (tested on Fedora 43).

### Prerequisites

Install required packages:

```bash
sudo dnf install -y libvirt qemu-kvm
```

Install Go dependencies:

```bash
go install github.com/ondrejbudai/gjoll/cmd/gjoll@latest
```

Make sure `gjoll` is in your `PATH` (it installs to `$(go env GOPATH)/bin`).

### Libvirt Setup

Start the libvirt daemon. On Fedora, the monolithic `libvirtd` daemon is
needed because the libvirt OpenTofu provider connects via
`/var/run/libvirt/libvirt-sock`, which the modular daemons
(`virtqemud`/`virtstoraged`/`virtnetworkd`) do not create:

```bash
sudo systemctl start libvirtd
```

Verify the default storage pool and network exist:

```bash
virsh -c qemu:///system pool-list --all
virsh -c qemu:///system net-list --all
```

If the default pool is missing:

```bash
sudo virsh pool-define-as default dir --target /var/lib/libvirt/images
sudo virsh pool-start default
sudo virsh pool-autostart default
```

If the default network is missing:

```bash
sudo virsh net-define /usr/share/libvirt/networks/default.xml
sudo virsh net-start default
sudo virsh net-autostart default
```

### User Permissions

The tests connect to `qemu:///system`, which requires permissions. Add a
polkit rule so your user can manage libvirt without `sudo`:

```bash
sudo tee /etc/polkit-1/rules.d/80-libvirt.rules > /dev/null << 'EOF'
polkit.addRule(function(action, subject) {
    if (action.id == "org.libvirt.unix.manage" && subject.user == "YOUR_USERNAME") {
        return polkit.Result.YES;
    }
});
EOF
```

Replace `YOUR_USERNAME` with your actual username. Verify it works:

```bash
virsh -c qemu:///system pool-list
```

### Running the Tests

```bash
go test -tags integration -v -timeout 30m .
```

The first run downloads a Fedora Cloud image (~500 MB), so it takes longer.
Subsequent runs reuse the cached image.

To run a specific integration test:

```bash
go test -tags integration -v -timeout 30m -run '^TestIntegration$' .
```

### Cleaning Up

If a test fails and leaves resources behind:

```bash
gjoll down orch-integ-test
gjoll down orch-integ-author
```

If gjoll state is corrupted, clean up manually:

```bash
rm -rf ~/.local/share/gjoll/instances/orch-integ-test
rm -rf ~/.local/share/gjoll/instances/orch-integ-author
virsh -c qemu:///system destroy gjoll-orch-integ-test 2>/dev/null
virsh -c qemu:///system undefine gjoll-orch-integ-test 2>/dev/null
virsh -c qemu:///system vol-delete root-orch-integ-test.qcow2 --pool default 2>/dev/null
virsh -c qemu:///system vol-delete fedora-base-orch-integ-test.qcow2 --pool default 2>/dev/null
```
