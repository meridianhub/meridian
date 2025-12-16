// Atlas configuration for Party Service
// BIAN Service Domain: Party Reference Data Directory
// Manages party identities (persons and organizations)
// Uses database-per-service architecture with unqualified table names
//
// Multi-tenant support:
// - Migrations use unqualified table names (no schema prefix)
// - For multi-org mode: search_path routes to organization schemas (org_{tenant_id})
// - For local development: uses default public schema in service-specific database

data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./utilities/atlas-loader",
    "--schema=party"
  ]
}

env "local" {
  // Service-specific migration directory
  migration {
    dir = "file://services/party/migrations"
  }

  // Dev database - uses default public schema
  dev = "docker://postgres/16/dev"

  // Source schema from GORM models via external loader
  src = data.external_schema.gorm.url

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
    dir = "file://services/party/migrations"
  }

  dev = "docker://postgres/16/dev"

  src = data.external_schema.gorm.url

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
  // URL points to service-specific database (meridian_party)
  // For multi-org: URL includes search_path for org schema
  url = getenv("DATABASE_URL")

  migration {
    dir = "file://services/party/migrations"
  }
}
