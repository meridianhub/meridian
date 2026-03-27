// Package worker provides the email dispatch worker that polls the outbox table
// and sends emails via the configured Sender. It uses the generic dispatch.Worker
// for the poll loop and delegates email-specific processing to EmailProcessor.
package worker
