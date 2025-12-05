// Atlas configuration for shared migrations
// Contains reusable infrastructure like the audit factory

// Local development environment
env "local" {
  migration {
    dir = "file://migrations/shared"
  }
  dev = "docker://postgres/16/dev"

  // Lint configuration - catch dangerous changes early
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

// CI environment (same as local)
env "ci" {
  migration {
    dir = "file://migrations/shared"
  }
  dev = "docker://postgres/16/dev"

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

// Production environment (apply only, no schema inspection)
env "production" {
  migration {
    dir = "file://migrations/shared"
  }

  // Use production DATABASE_URL
  url = env("PROD_DATABASE_URL")
}
