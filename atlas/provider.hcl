// Atlas GORM provider configuration
provider "gorm" {
  path   = "./internal/domain/models"
  dialect = "postgres"
}
