// Atlas configuration for Financial Gateway Service
// BIAN Service Domain: Payment Gateway Management
// Manages inbound webhook reception from payment providers (Stripe).
// Uses database-per-service architecture with unqualified table names.

env "local" {
  migration {
    dir = "file://services/financial-gateway/migrations"
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

env "ci" {
  migration {
    dir = "file://services/financial-gateway/migrations"
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
  url = getenv("DATABASE_URL")

  migration {
    dir = "file://services/financial-gateway/migrations"
  }
}
