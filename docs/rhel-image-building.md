# RHEL Image Building: End-to-End Test Findings

## Overview

This document captures findings from an end-to-end test of building a RHEL 10.2
qcow2 image using [image-builder](https://github.com/osbuild/images) inside an
orchestrator sandbox. The test was performed on a Fedora 43 host (the same OS
used in the default sandbox `.tf`).

## Test Environment

- **Host OS:** Fedora 43 (x86_64)
- **image-builder:** built from [drellabot/image-builder](https://github.com/drellabot/image-builder) `main`
- **osbuild:** 185-1.fc43
- **Target:** `rhel-10.2` distro, `qcow2` image type

## Test 1: Direct Build (no RHSM)

```
sudo image-builder build qcow2 --distro rhel-10.2
```

**Result:** Failed at manifest generation (depsolving).

```
error: error depsolving package sets for "build": This system does not have
any valid subscriptions. Subscribe it before specifying rhsm: true in sources
(error details: no matching key and certificate pair)
```

**Root cause:** RHEL repository definitions in
`data/repositories/rhel-10.2.json` set `"rhsm": true` on every repo, meaning
the depsolver requires entitlement certificates at `/etc/pki/entitlement/` to
access `cdn.redhat.com`. A fresh Fedora sandbox has no RHEL subscription.

## Test 2: Build with `--force-repo` (CentOS Stream 10 repos)

```
sudo image-builder build qcow2 --distro rhel-10.2 \
  --force-repo https://mirror.stream.centos.org/10-stream/BaseOS/x86_64/os/ \
  --force-repo https://mirror.stream.centos.org/10-stream/AppStream/x86_64/os/
```

**Result:** `--force-repo` successfully bypassed the RHSM check — the depsolver
resolved all packages and osbuild downloaded them. The build proceeded through
RPM installation, SELinux labeling, and kernel configuration. It then failed at
the `org.osbuild.grub2` stage:

```
FileNotFoundError: [Errno 2] No such file or directory:
  '/run/osbuild/tree/boot/efi/EFI/redhat/grub.cfg'
```

**Root cause:** The RHEL 10.2 manifest sets `"vendor": "redhat"` for the UEFI
GRUB configuration, expecting the EFI System Partition to have a
`/boot/efi/EFI/redhat/` directory. This directory is created by the `shim-x64`
and `grub2-efi-x64` RPMs. However, the CentOS Stream 10 builds of these
packages install to `/boot/efi/EFI/centos/` instead:

```
# CentOS Stream shim-x64-16.1-1.el10.x86_64.rpm file list:
/boot/efi/EFI/centos/shimx64.efi
/boot/efi/EFI/centos/BOOTX64.CSV
...

# CentOS Stream grub2-efi-x64-2.12-48.el10.x86_64.rpm file list:
/boot/efi/EFI/centos/grub.cfg
/boot/efi/EFI/centos/grubx64.efi
```

The RHEL 10.2 equivalents of these packages install to `/boot/efi/EFI/redhat/`.
This vendor-directory mismatch means CentOS Stream packages cannot be used as a
drop-in substitute for RHEL packages when building RHEL images — even though the
rest of the package set is ABI-compatible.

## Conclusion

Building genuine RHEL images requires RHSM entitlement certificates. There is no
workaround using freely available repositories — the EFI vendor directory
mismatch between CentOS Stream and RHEL packages causes the osbuild GRUB stage
to fail even when `--force-repo` bypasses the RHSM depsolver check.

## Orchestrator RHEL Support

The orchestrator already has RHEL subscription support (added in `360b2de`):

1. **`internal/rhel/`** — API client that creates activation keys via the Red Hat
   Hybrid Cloud Console API using OAuth2 client credentials.
2. **`internal/cmd/task.go:setupRHELSubscription()`** — triggered when a task
   carries the `"rhel"` GitHub issue label. Creates an activation key and sets
   `TF_VAR_rhel_org_id` / `TF_VAR_rhel_activation_key` environment variables.
3. **`configs/sandbox.tf`** — the init script conditionally runs
   `subscription-manager register --org --activationkey` when the variables are set.

### Requirements for RHEL Builds

The following environment variables must be set on the orchestrator host:

| Variable | Description |
|---|---|
| `LIGHTSPEED_CLIENT_ID` | OAuth2 client ID for `sso.redhat.com` |
| `LIGHTSPEED_CLIENT_SECRET` | OAuth2 client secret |
| `LIGHTSPEED_ORG_ID` | Red Hat organization ID |

The task must carry the `rhel` label (via `--label rhel` on the CLI, or the
`rhel` label on the source GitHub issue).

### Untested Assumptions

The following parts of the RHEL flow could not be verified without Red Hat API
credentials:

1. Whether `subscription-manager register` on a Fedora 43 guest properly
   installs RHEL entitlement certificates to `/etc/pki/entitlement/`
2. Whether the activation key auto-attaches subscriptions that grant access to
   RHEL 10 content on `cdn.redhat.com`
3. Whether the entitlement certificates are sufficient for `image-builder` to
   depsolve against RHEL 10.2 repos

### Podman Backend Limitation

The RHEL subscription flow only works with the gjoll (VM) backend. The podman
backend has no mechanism to inject RHSM credentials into the container. For
container-based RHEL builds, entitlement certificates would need to be
bind-mounted from the host (e.g.
`-v /etc/pki/entitlement:/etc/pki/entitlement:ro`).
