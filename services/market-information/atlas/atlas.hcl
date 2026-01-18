// Atlas configuration for Market Information Service
// BIAN Service Domain: Market Information Management
// Manages price benchmarks, market data feeds, and reference prices
// Uses database-per-service architecture with unqualified table names

data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./utilities/atlas-loader",
    "--schema=market_information"
  ]
}

env "local" {
  // Service-specific migration directory
  migration {
    dir = "file://services/market-information/migrations"
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
    dir = "file://services/market-information/migrations"
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
  // URL points to service-specific database (meridian_market_information)
  url = getenv("DATABASE_URL")

  migration {
    dir = "file://services/market-information/migrations"
  }
}
