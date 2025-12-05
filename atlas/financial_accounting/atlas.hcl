// Atlas configuration for Financial Accounting schema
// BIAN Service Domain: Financial Accounting
// Manages ledger postings and financial booking logs

data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./cmd/atlas-loader",
    "--schema=financial_accounting"
  ]
}

env "local" {
  // Schema-specific migration directory
  migration {
    dir = "file://migrations/financial_accounting"
  }

  // Dev database
  dev = "docker://postgres/16/dev"

  // Source schema from GORM models via external loader
  src = data.external_schema.gorm.url

  // Schema configuration
  schemas = ["financial_accounting"]

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
    dir = "file://migrations/financial_accounting"
  }

  dev = "docker://postgres/16/dev"

  src = data.external_schema.gorm.url

  schemas = ["financial_accounting"]

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
    dir = "file://migrations/financial_accounting"
  }

  schemas = ["financial_accounting"]
}
