// Atlas configuration for Internal Account Service
// BIAN Service Domain: Internal Account
// Manages counterparty and operational accounts for internal accounting
// Uses database-per-service architecture with unqualified table names

data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./utilities/atlas-loader",
    "--schema=internal_account"
  ]
}

env "local" {
  // Service-specific migration directory
  migration {
    dir = "file://services/internal-account/migrations"
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
    dir = "file://services/internal-account/migrations"
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
  // URL points to service-specific database (meridian_internal_account)
  url = getenv("DATABASE_URL")

  migration {
    dir = "file://services/internal-account/migrations"
  }
}
