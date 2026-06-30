terraform {
  required_providers {
    libvirt = { source = "dmacvicar/libvirt", version = "= 0.9.7" }
  }
}

provider "libvirt" { uri = "qemu:///system" }

resource "libvirt_volume" "base" {
  name   = "fedora-base-${var.gjoll_name}.qcow2"
  pool   = "default"
  target = { format = { type = "qcow2" } }
  create = {
    content = {
      url = "https://download.fedoraproject.org/pub/fedora/linux/releases/43/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-43-1.6.x86_64.qcow2"
    }
  }
}

resource "libvirt_volume" "root" {
  name          = "root-${var.gjoll_name}.qcow2"
  pool          = "default"
  capacity      = 53687091200 # 50 GiB
  target        = { format = { type = "qcow2" } }
  backing_store = { path = libvirt_volume.base.path, format = { type = "qcow2" } }
}

resource "libvirt_cloudinit_disk" "init" {
  name = "cloudinit-${var.gjoll_name}.iso"
  meta_data = jsonencode({
    instance-id    = "gjoll-${var.gjoll_name}"
    local-hostname = "gjoll-${var.gjoll_name}"
  })
  user_data = <<-EOF
    #cloud-config
    users:
      - name: fedora
        sudo: ALL=(ALL) NOPASSWD:ALL
        shell: /bin/bash
        ssh_authorized_keys:
          - ${var.gjoll_ssh_pubkey}
  EOF
}

resource "libvirt_domain" "sandbox" {
  name        = "gjoll-${var.gjoll_name}"
  type        = "kvm"
  memory      = 4096
  memory_unit = "MiB"
  vcpu        = 2
  running     = var.gjoll_instance_state == "running"

  cpu = { mode = "host-passthrough" }
  os  = { type = "hvm" }

  devices = {
    disks = [
      {
        source = { file = { file = libvirt_volume.root.path } }
        target = { dev = "vda", bus = "virtio" }
        driver = { name = "qemu", type = "qcow2" }
      },
      {
        device = "cdrom"
        source = { file = { file = libvirt_cloudinit_disk.init.path } }
        target = { dev = "sda", bus = "sata" }
        driver = { name = "qemu", type = "raw" }
      },
    ]
    interfaces = [
      {
        source      = { network = { network = "default" } }
        model       = { type = "virtio" }
        wait_for_ip = var.gjoll_instance_state == "running" ? { source = "lease" } : null
      },
    ]
    consoles = [
      { target = { type = "serial", port = 0 } },
    ]
  }
}

data "libvirt_domain_interface_addresses" "sandbox" {
  count  = var.gjoll_instance_state == "running" ? 1 : 0
  domain = libvirt_domain.sandbox.name
  source = "lease"
}

output "public_ip" {
  value = var.gjoll_instance_state == "running" ? data.libvirt_domain_interface_addresses.sandbox[0].interfaces[0].addrs[0].addr : ""
}
output "instance_id" { value = tostring(libvirt_domain.sandbox.id) }
output "ssh_user"    { value = "fedora" }
variable "rhel_org_id" {
  type        = string
  description = "Red Hat organization ID for subscription-manager registration"
  default     = ""
  sensitive   = true
}

variable "rhel_activation_key" {
  type        = string
  description = "Red Hat activation key for subscription-manager registration"
  default     = ""
  sensitive   = true
}

output "init_script" {
  sensitive = true
  value = <<-EOT
    #!/bin/bash
    set -euo pipefail
    sudo dnf install -y git-core tmux
    curl -fsSL https://claude.ai/install.sh | bash

    # Register with Red Hat subscription-manager if credentials are provided
    %{if var.rhel_org_id != "" && var.rhel_activation_key != ""}
    sudo dnf install -y subscription-manager
    sudo subscription-manager register --org '${var.rhel_org_id}' --activationkey '${var.rhel_activation_key}'

    # Workaround: osbuild's org.osbuild.rpm stage fails when building RHEL 10+
    # images because rpm-sequoia on Fedora rejects the Red Hat PQC GPG key
    # (release key 4). The ignore_import_failures option skips the key import
    # error but rpmkeys --checksig still hard-fails for packages signed with
    # the missing key. This patch makes checksig failures non-fatal when
    # ignore_import_failures is already set.
    # Upstream: https://github.com/osbuild/osbuild (org.osbuild.rpm stage)
    sudo tee /usr/local/sbin/patch-osbuild-pqc >/dev/null <<'PATCHEOF'
    #!/bin/bash
    set -euo pipefail
    STAGE=/usr/lib/osbuild/stages/org.osbuild.rpm
    [ -f "$STAGE" ] || exit 0
    grep -q 'ignoring because ignore_import_failures' "$STAGE" && exit 0
    sed -i 's/                print(f"Signature check failed on {filename}, lookup package name in manifest.")/                if ignore_import_failures:\n                    print(f"Signature check failed on {filename}, ignoring because ignore_import_failures is set.")\n                    continue\n                print(f"Signature check failed on {filename}, lookup package name in manifest.")/' "$STAGE"
    PATCHEOF
    sudo chmod +x /usr/local/sbin/patch-osbuild-pqc

    sudo dnf install -y osbuild osbuild-depsolve-dnf
    sudo /usr/local/sbin/patch-osbuild-pqc
    %{endif}

    # Configure Claude Code to use Vertex AI via local proxy
    cat >> ~/.bashrc <<'RCEOF'
    export CLAUDE_CODE_USE_VERTEX=1
    export CLOUD_ML_REGION=global
    export ANTHROPIC_VERTEX_PROJECT_ID=itpc-gcp-core-pe-eng-claude
    export ANTHROPIC_VERTEX_BASE_URL=http://localhost:18080
    export CLAUDE_CODE_SKIP_VERTEX_AUTH=1
    export ANTHROPIC_MODEL=claude-opus-4-6
    alias claude='claude --dangerously-skip-permissions'
    RCEOF
  EOT
}

# Proxy configuration — the orchestrator MCP server is tunneled via ssh -R
# at runtime rather than being listed here, so multiple tasks can share this
# tf file in parallel (each gets its own dynamic port).
output "proxies" {
  value = [
    {
      name   = "vertex"
      target = "https://us-east5-aiplatform.googleapis.com/v1"
      auth   = "gcp"
      port   = 18080
    },
  ]
}
