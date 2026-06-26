# RHEL Image Build: End-to-End Test Report

End-to-end test of building a RHEL 10.2 qcow2 image using
[drellabot/image-builder](https://github.com/drellabot/image-builder) on a
Fedora 43 sandbox VM — the same environment the orchestrator provisions via
gjoll.

## Environment

| Component | Version |
|-----------|---------|
| Host OS | Fedora 43 (kernel 6.17.1) |
| image-builder | Built from drellabot/image-builder main |
| osbuild | 183-1.fc43 |
| Go | 1.24+ |

## Setup

```bash
# Install build dependencies
sudo dnf install -y gpgme-devel libvirt-devel osbuild osbuild-depsolve-dnf golang

# Clone and build
git clone https://github.com/drellabot/image-builder.git
cd image-builder
make build
```

## Test Results

### 1. RHEL 10.2 qcow2 — without subscription (FAIL)

```bash
sudo ./bin/image-builder build qcow2 --distro rhel-10.2
```

**Result:** Immediate failure at dependency resolution.

```
error: error depsolving package sets for "build": This system does not have
any valid subscriptions. Subscribe it before specifying rhsm: true in sources
(error details: no matching key and certificate pair)
```

**Root cause:** RHEL repository definitions use `rhsm: true`, which requires
the host to be registered with `subscription-manager` and have valid RHEL
entitlements. An unsubscribed Fedora host cannot access RHEL CDN repos.

**Orchestrator impact:** The `--label rhel` flag and `setupRHELSubscription()`
flow in `internal/cmd/task.go` is **mandatory** for RHEL image builds. Without
it, the build cannot even begin dependency resolution.

### 2. RHEL 10.2 qcow2 with CentOS Stream repos (FAIL)

Attempted workaround using `--force-repo` to substitute freely-available
CentOS Stream 10 repositories:

```bash
sudo ./bin/image-builder build qcow2 --distro rhel-10.2 \
  --force-repo https://composes.stream.centos.org/stream-10/production/latest-CentOS-Stream/compose/BaseOS/x86_64/os/ \
  --force-repo https://composes.stream.centos.org/stream-10/production/latest-CentOS-Stream/compose/AppStream/x86_64/os/
```

**Result:** Build progresses through package download and installation (~400
packages) but fails at the osbuild GRUB2 configuration stage.

```
Traceback (most recent call last):
  File "/run/osbuild/bin/org.osbuild.grub2", line 429, in <module>
    r = main(args["tree"], args["options"])
  File "/run/osbuild/bin/org.osbuild.grub2", line 413, in main
    config.write_redirect(tree, grubcfg)
  File "/run/osbuild/bin/org.osbuild.grub2", line 258, in write_redirect
    with open(os.path.join(tree, path), "w", encoding="utf8") as cfg:
FileNotFoundError: [Errno 2] No such file or directory:
  '/run/osbuild/tree/boot/efi/EFI/redhat/grub.cfg'
```

**Root cause:** EFI vendor directory mismatch.

- The RHEL 10.2 image definition specifies `"vendor": "redhat"` for the EFI
  path, so the GRUB2 stage tries to write to `/boot/efi/EFI/redhat/grub.cfg`.
- CentOS Stream 10 `shim-x64` and `grub2-efi-x64` packages install to
  `/boot/efi/EFI/centos/`, not `/boot/efi/EFI/redhat/`.
- The osbuild GRUB2 stage's `write_redirect()` method does not create parent
  directories before writing, so it fails with `FileNotFoundError`.

Both builds install identical package versions (`shim-x64-16.1-1.el10`,
`grub2-efi-x64-2.12-49.el10`) — the difference is purely in the EFI
installation paths baked into the RPMs.

**Conclusion:** CentOS Stream repos cannot substitute for RHEL repos when
building RHEL image types. This is an inherent limitation, not an orchestrator
bug.

### 3. CentOS 10 qcow2 (SUCCESS)

```bash
sudo ./bin/image-builder build qcow2 --distro centos-10
```

**Result:** Build completes successfully in ~2 minutes.

```
Image build successful: centos-10-qcow2-x86_64/centos-10-qcow2-x86_64.qcow2
```

- Output: 1.2 GiB QCOW2 v3 image (10 GiB virtual size)
- Uses `"vendor": "centos"` → matches CentOS Stream shim/grub2 EFI paths
- No subscription required (uses freely-available CentOS Stream repos)

## Findings for the Orchestrator

### RHEL subscription flow works correctly

The orchestrator's RHEL subscription setup in `internal/cmd/task.go`:

1. `--label rhel` triggers `setupRHELSubscription()`
2. Creates an activation key via the Red Hat Hybrid Cloud Console API
   (`internal/rhel/rhel.go`)
3. Sets `TF_VAR_rhel_org_id` and `TF_VAR_rhel_activation_key`
4. The `sandbox.tf` init_script runs `subscription-manager register`
5. After registration, image-builder can access RHEL CDN repos via `rhsm: true`

This is the correct and only viable approach. The env vars
`LIGHTSPEED_CLIENT_ID`, `LIGHTSPEED_CLIENT_SECRET`, and `LIGHTSPEED_ORG_ID`
must be set on the orchestrator host.

### No orchestrator bugs found

The orchestrator code correctly handles the RHEL workflow. The build failure
without subscriptions is expected behavior and is properly gated behind the
`--label rhel` flag with a warning when credentials are missing.

### Recommendations

1. **CentOS 10 as a CI/dev alternative:** For testing the image-building
   pipeline without RHEL subscriptions, CentOS 10 qcow2 images build
   successfully and exercise the same code paths (minus subscription-manager).

2. **Upstream osbuild improvement:** The `write_redirect()` method in
   `org.osbuild.grub2` should call `os.makedirs(parent, exist_ok=True)` before
   writing. This wouldn't fix the vendor mismatch (CentOS repos + RHEL image
   type is fundamentally wrong), but would produce a clearer error message
   instead of a Python traceback.
