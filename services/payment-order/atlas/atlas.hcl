// Atlas configuration for Payment Order schema
// BIAN Service Domain: Payment Order
// Manages payment orders and saga orchestration for payment processing

data "external_schema" "gorm" {
  program = [
    "go",
    "run",
    "-mod=mod",
    "./utilities/atlas-loader",
    "--schema=payment_order"
  ]
}

env "local" {
  // Schema-specific migration directory
  migration {
    dir = "file://services/payment-order/migrations"
  }

  // Dev database - include payment_order schema in search path
  dev = "docker://postgres/16/dev"

  // Source schema from GORM models via external loader
  src = data.external_schema.gorm.url

  // Schema configuration
  schemas = ["payment_order"]

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
    dir = "file://services/payment-order/migrations"
  }

  dev = "docker://postgres/16/dev"

  src = data.external_schema.gorm.url

  schemas = ["payment_order"]

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
    dir = "file://services/payment-order/migrations"
  }

  schemas = ["payment_order"]
}
