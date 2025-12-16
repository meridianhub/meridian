// Atlas configuration for Financial Accounting Service
// BIAN Service Domain: Financial Accounting
// Manages ledger postings and financial booking logs
// Uses database-per-service architecture with unqualified table names

data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./utilities/atlas-loader",
    "--service=financial-accounting"
  ]
}

env "local" {
  // Service-specific migration directory
  migration {
    dir = "file://services/financial-accounting/migrations"
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
    dir = "file://services/financial-accounting/migrations"
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
  // URL points to service-specific database (meridian_financial_accounting)
  url = getenv("DATABASE_URL")

  migration {
    dir = "file://services/financial-accounting/migrations"
  }
}
