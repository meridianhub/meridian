// Package email provides types and utilities for sending transactional emails,
// including HTML/text template rendering backed by embedded templates, and
// data types for each email variant (invoice, dunning notice, payment received,
// account frozen).
//
// Templates are compiled into the binary at build time via embed.FS and rendered
// with Go's html/template (HTML) and text/template (plain text) packages.
// All HTML output is XSS-safe: user-supplied values are automatically escaped.
//
// Usage:
//
//	renderer, err := email.NewEmbeddedRenderer()
//	if err != nil {
//	    return err
//	}
//
//	html, text, err := renderer.Render("invoice", email.InvoiceData{
//	    CustomerName:  "Acme Corp",
//	    InvoiceNumber: "INV-001",
//	    Total:         "£750.00",
//	    DueDate:       "2026-04-01",
//	    PaymentLink:   "https://pay.example.com/INV-001",
//	})
package email
