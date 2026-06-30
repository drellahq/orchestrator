terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 6.0" }
  }
}

provider "aws" {
  region = "us-east-1"
  ignore_tags {
    keys         = ["architecture"]
  }
}

resource "aws_key_pair" "gjoll" {
  key_name   = "gjoll-${var.gjoll_name}"
  public_key = var.gjoll_ssh_pubkey
  tags = {
    ManagedBy = "drella"
    Project   = "drella"
  }
}

resource "aws_security_group" "gjoll" {
  name = "gjoll-${var.gjoll_name}"
  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = {
    ManagedBy = "drella"
    Project   = "drella"
  }
}

resource "aws_instance" "sandbox" {
  ami                    = "ami-0edf1d45580ac3fa3" # Fedora 43 x86_64 in us-east-1
  instance_type          = "m8i.large"
  key_name               = aws_key_pair.gjoll.key_name
  vpc_security_group_ids = [aws_security_group.gjoll.id]

  cpu_options {
    nested_virtualization = "enabled"
  }

  root_block_device {
    volume_size = 50
    tags = {
      ManagedBy = "drella"
      Project   = "drella"
    }
  }

  tags = {
    Name      = "gjoll-${var.gjoll_name}"
    ManagedBy = "drella"
    persist   = "true"
    Project   = "drella"
  }
}

resource "aws_ec2_instance_state" "sandbox" {
  instance_id = aws_instance.sandbox.id
  state       = var.gjoll_instance_state
}

output "public_ip"   { value = var.gjoll_instance_state == "running" ? aws_instance.sandbox.public_ip : "" }
output "instance_id" { value = aws_instance.sandbox.id }
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
    curl -fsSL https://claude.ai/install.sh | bash
    sudo dnf install -y git-core

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
    echo 'export CLAUDE_CODE_USE_VERTEX=1' >> ~/.bashrc
    echo 'export CLOUD_ML_REGION=global' >> ~/.bashrc
    echo 'export ANTHROPIC_VERTEX_PROJECT_ID=itpc-gcp-core-pe-eng-claude' >> ~/.bashrc
    echo 'export ANTHROPIC_VERTEX_BASE_URL=http://localhost:18080' >> ~/.bashrc
    echo 'export CLAUDE_CODE_SKIP_VERTEX_AUTH=1' >> ~/.bashrc
    echo 'export ANTHROPIC_MODEL=claude-opus-4-6' >> ~/.bashrc
    echo "alias claude='claude --dangerously-skip-permissions'" >> ~/.bashrc
  EOT
}

# Proxy configuration — no secrets on VM!
# GCP credentials are injected by the proxy on the host via ADC.
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
