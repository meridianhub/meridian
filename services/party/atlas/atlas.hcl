// Atlas configuration for Party schema
// BIAN Service Domain: Party Reference Data Directory
// Manages party identities (persons and organizations)

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

  // Dev database - include party schema in search path
  dev = "docker://postgres/16/dev"

  // Source schema from GORM models via external loader
  src = data.external_schema.gorm.url

  // Schema configuration
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

  dev = "docker://postgres/16/dev"

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
  url = getenv("PROD_DATABASE_URL")

  migration {
    dir = "file://services/party/migrations"
    revisions_schema = "party_revisions"
  }

  schemas = ["party"]
}
