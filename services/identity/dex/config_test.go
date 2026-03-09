package dex

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_Validate_MissingIssuer(t *testing.T) {
	cfg := Config{
		Connector: &stubConnector{},
	}
	err := cfg.validate()
	assert.ErrorIs(t, err, ErrIssuerRequired)
}

func TestConfig_Validate_MissingConnector(t *testing.T) {
	cfg := Config{
		Issuer: "https://auth.example.com/dex",
	}
	err := cfg.validate()
	assert.ErrorIs(t, err, ErrConnectorRequired)
}

func TestConfig_Validate_Valid(t *testing.T) {
	cfg := Config{
		Issuer:    "https://auth.example.com/dex",
		Connector: &stubConnector{},
	}
	err := cfg.validate()
	assert.NoError(t, err)
}
