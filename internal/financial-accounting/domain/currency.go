package domain

import "fmt"

// Currency represents ISO 4217 currency codes supported by the system.
type Currency string

// Supported currency codes following ISO 4217 standard.
const (
	CurrencyGBP Currency = "GBP" // British Pound Sterling
	CurrencyUSD Currency = "USD" // United States Dollar
	CurrencyEUR Currency = "EUR" // Euro
	CurrencyJPY Currency = "JPY" // Japanese Yen
	CurrencyCHF Currency = "CHF" // Swiss Franc
	CurrencyCAD Currency = "CAD" // Canadian Dollar
	CurrencyAUD Currency = "AUD" // Australian Dollar
)

// IsValid checks if the currency code is supported.
func (c Currency) IsValid() bool {
	switch c {
	case CurrencyGBP, CurrencyUSD, CurrencyEUR, CurrencyJPY, 
	     CurrencyCHF, CurrencyCAD, CurrencyAUD:
		return true
	}
	return false
}

// String returns the string representation of the currency.
func (c Currency) String() string {
	return string(c)
}

// ParseCurrency converts a string to a Currency type with validation.
func ParseCurrency(s string) (Currency, error) {
	c := Currency(s)
	if !c.IsValid() {
		return "", fmt.Errorf("invalid currency code: %s", s)
	}
	return c, nil
}
