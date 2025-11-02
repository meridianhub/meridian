// Atlas configuration for database migrations
// This configuration enables automatic migration generation from Go structs

data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./cmd/atlas-loader"
  ]
}

env "local" {
  // Path where migrations will be stored
  migration {
    dir = "file://migrations"
  }

  // Dev database for schema validation and testing
  dev = "docker://postgres/16/dev?search_path=public"

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
    dir = "file://migrations"
  }

  dev = "docker://postgres/16/dev?search_path=public"

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
  url = getenv("PROD_DATABASE_URL")

  migration {
    dir = "file://migrations"
  }

  // No dev database in production
  // No src in production - only apply migrations
}
