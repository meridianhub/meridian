// Atlas configuration for Control Plane Service
// Manages manifest application, validation, diffing, and staff identity
// Uses dedicated meridian_control_plane database

env "local" {
  // Service-specific migration directory
  migration {
    dir = "file://services/control-plane/migrations"
  }

  // Dev database - uses default public schema
  dev = "docker://postgres/16/dev"

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
    dir = "file://services/control-plane/migrations"
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

env "production" {
  // Production environment - apply only, never diff
  // URL points to shared platform database (meridian_platform)
  url = getenv("DATABASE_URL")

  migration {
    dir = "file://services/control-plane/migrations"
  }
}
