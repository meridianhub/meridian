// Atlas configuration for Current Account schema
// BIAN Service Domain: Current Account
// Manages customers and their accounts

data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./utilities/atlas-loader",
    "--schema=current_account"
  ]
}

env "local" {
  // Schema-specific migration directory
  migration {
    dir = "file://services/current-account/migrations"
    // Use schema-specific revisions table to avoid conflicts with other services
    revisions_schema = "current_account_revisions"
  }

  // Dev database - include current_account schema in search path
  dev = "docker://postgres/16/dev"

  // Source schema from GORM models via external loader
  src = data.external_schema.gorm.url

  // Schema configuration
  schemas = ["current_account"]

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
    dir = "file://services/current-account/migrations"
    revisions_schema = "current_account_revisions"
  }

  dev = "docker://postgres/16/dev"

  src = data.external_schema.gorm.url

  schemas = ["current_account"]

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
    dir = "file://services/current-account/migrations"
    revisions_schema = "current_account_revisions"
  }

  schemas = ["current_account"]
}
