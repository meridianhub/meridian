// Atlas configuration for Position Keeping schema
// BIAN Service Domain: Position Keeping
// Pre-ledger transaction log and position tracking

data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./utilities/atlas-loader",
    "--schema=position_keeping"
  ]
}

env "local" {
  // Schema-specific migration directory
  migration {
    dir = "file://services/position-keeping/migrations"
    revisions_schema = "position_keeping_revisions"
  }

  // Dev database
  dev = "docker://postgres/16/dev"

  // Source schema from GORM models via external loader
  src = data.external_schema.gorm.url

  // Schema configuration
  // NOTE: position_keeping depends on current_account schema existing (for FK constraints)
  // Apply current_account migrations before position_keeping migrations
  schemas = ["position_keeping"]

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
    dir = "file://services/position-keeping/migrations"
    revisions_schema = "position_keeping_revisions"
  }

  dev = "docker://postgres/16/dev"

  src = data.external_schema.gorm.url

  schemas = ["position_keeping"]

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
    dir = "file://services/position-keeping/migrations"
    revisions_schema = "position_keeping_revisions"
  }

  schemas = ["position_keeping"]
}
