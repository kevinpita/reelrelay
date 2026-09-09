terraform {
  required_version = ">= 1.6, < 2.0"

  required_providers {
    vault = {
      source  = "hashicorp/vault"
      version = "5.10.0"
    }
  }

  backend "local" {}
}

locals {
  openbao = yamldecode(file("${path.module}/../chart/values.yaml")).openbao
}

provider "vault" {
  address = local.openbao.server
}

resource "vault_policy" "reelrelay" {
  name = "reelrelay-read"

  policy = <<-EOT
    path "kv/data/apps/reelrelay" {
      capabilities = ["read"]
    }

    # ESO checks and revokes its own short-lived token.
    path "auth/token/lookup-self" {
      capabilities = ["read"]
    }
    path "auth/token/revoke-self" {
      capabilities = ["update"]
    }
  EOT
}

resource "vault_kubernetes_auth_backend_role" "reelrelay" {
  backend                          = "kubernetes"
  role_name                        = "reelrelay"
  bound_service_account_names      = ["openbao-reader"]
  bound_service_account_namespaces = ["reelrelay"]
  audience                         = trimspace(local.openbao.audience)
  alias_name_source                = "serviceaccount_uid"
  token_policies                   = [vault_policy.reelrelay.name]
  token_no_default_policy          = true
  token_type                       = "service"
  token_ttl                        = 600
  token_max_ttl                    = 600
  token_explicit_max_ttl           = 600

  lifecycle {
    precondition {
      condition     = length(trimspace(local.openbao.audience)) > 0
      error_message = "Set openbao.audience in infra/chart/values.yaml to an audience accepted by the Kubernetes API."
    }
  }
}
