terraform {
  required_providers {
    automox = {
      source  = "registry.sweetgreen.engineering/sweetgreen/automox"
      version = "~> 0.1"
    }
  }
}

# The API key is sensitive and is read from the environment rather than written
# into configuration, so it stays out of version control and out of state.
#
#   export AUTOMOX_API_KEY="..."
#   export AUTOMOX_ORGANIZATION_ID="120547"
provider "automox" {
  # api_key         = var.automox_api_key      # or AUTOMOX_API_KEY
  # organization_id = 120547                   # or AUTOMOX_ORGANIZATION_ID
}
