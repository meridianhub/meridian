// Atlas configuration for Reconciliation Service
// BIAN Service Domain: Reconciliation
// Manages reconciliation processes and settlement
// Uses database-per-service architecture with unqualified table names

data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./utilities/atlas-loader",
    "--schema=reconciliation"
  ]
}

env "local" {
  // Service-specific migration directory
  migration {
    dir = "file://services/reconciliation/migrations"
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
    dir = "file://services/reconciliation/migrations"
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
  // URL points to service-specific database (meridian_reconciliation)
  url = getenv("DATABASE_URL")

  migration {
    dir = "file://services/reconciliation/migrations"
  }
}
