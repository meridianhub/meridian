// Atlas configuration for Tenant schema
// Platform infrastructure: Tenant registry for multi-tenant platform
// Manages platform tenants (infrastructure concept, distinct from BIAN Party.Organization)

data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./utilities/atlas-loader",
    "--schema=platform"
  ]
}

env "local" {
  // Schema-specific migration directory
  migration {
    dir = "file://services/tenant/migrations"
    // Use schema-specific revisions table to avoid conflicts with other services
    revisions_schema = "platform_revisions"
  }

  // Dev database - include platform schema in search path
  dev = "docker://postgres/16/dev"

  // Source schema from GORM models via external loader
  src = data.external_schema.gorm.url

  // Schema configuration
  schemas = ["platform"]

  // Lint configuration to catch dangerous changes
  lint {
    destructive {
      error = true
    }
    data_depend {
      error = true
    }
    incompatible {
      error = true
    }
  }
}

env "ci" {
  migration {
    dir = "file://services/tenant/migrations"
    revisions_schema = "platform_revisions"
  }

  dev = "docker://postgres/16/dev"

  src = data.external_schema.gorm.url

  schemas = ["platform"]

  lint {
    destructive {
      error = true
    }
    data_depend {
      error = true
    }
    incompatible {
      error = true
    }
  }
}

env "production" {
  // Production environment - apply only, never diff
  url = getenv("PROD_DATABASE_URL")

  migration {
    dir = "file://services/tenant/migrations"
    revisions_schema = "platform_revisions"
  }

  schemas = ["platform"]
}
