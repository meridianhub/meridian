// DEPRECATED: This configuration is no longer used.
//
// The migration system now uses per-schema configurations for service isolation:
//   - atlas/shared.hcl              → migrations/shared/          (_audit_factory schema)
//   - atlas/current_account.hcl     → migrations/current_account/ (current_account + current_account_audit)
//   - atlas/position_keeping.hcl    → migrations/position_keeping/ (position_keeping + position_keeping_audit)
//   - atlas/financial_accounting.hcl → migrations/financial_accounting/
//   - atlas/payment_order.hcl       → migrations/payment_order/
//
// This file is kept for reference only. Use the schema-specific configurations instead.
//
// Historical note: This configuration enabled automatic migration generation from Go structs

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
