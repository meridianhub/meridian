// Atlas configuration for Party schema
// BIAN Service Domain: Party Reference Data Directory
// Manages party identities (persons and organizations)
//
// Multi-tenant support:
// - Migrations use unqualified table names (no schema prefix)
// - PostgreSQL search_path routes queries to organization schemas (org_{tenant_id})
// - Use scripts/migrate-all-orgs.sh to apply migrations to all active organizations
// - For local development, tables are created in the 'party' schema

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
  // Schema-specific migration directory
  migration {
    dir = "file://services/party/migrations"
    // Use schema-specific revisions table to avoid conflicts with other services
    revisions_schema = "party_revisions"
  }

  // Dev database with search_path set to party schema
  // This ensures unqualified table names resolve to the party schema during development
  dev = "docker://postgres/16/dev?search_path=party"

  // Source schema from GORM models via external loader
  src = data.external_schema.gorm.url

  // Schema configuration for development
  // In production, migrations are applied per-organization via search_path
  schemas = ["party"]

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
    revisions_schema = "party_revisions"
  }

  // CI uses search_path to route to party schema
  dev = "docker://postgres/16/dev?search_path=party"

  src = data.external_schema.gorm.url

  schemas = ["party"]

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
  // URL should include search_path for the target organization schema
  // Example: postgres://user:pass@host/db?search_path=org_acme_bank
  url = getenv("PROD_DATABASE_URL")

  migration {
    dir = "file://services/party/migrations"
    // Revisions table is created in the target schema (set via search_path)
    revisions_schema = "party_revisions"
  }
}
