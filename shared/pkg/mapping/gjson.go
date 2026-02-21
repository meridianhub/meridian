// Package mapping provides utilities for transforming external JSON payloads
// into internal Meridian gRPC request messages and vice versa.
//
// Field extraction from external payloads uses gjson path expressions,
// which support dot notation, array indexing, and modifiers:
//
//	"customer.id"            — nested field
//	"items.#.price"          — array element iteration
//	"meta|@flatten"          — gjson modifier
//
// See https://github.com/tidwall/gjson for full path syntax documentation.
package mapping

import "github.com/tidwall/gjson"

// Extract returns the gjson Result for the given path expression applied to the JSON payload.
// Returns a zero-value Result if the path is not found or the JSON is invalid.
func Extract(json, path string) gjson.Result {
	return gjson.Get(json, path)
}

// ExtractString returns the string value at path in the JSON payload.
// Returns an empty string if the path is not found.
func ExtractString(json, path string) string {
	return gjson.Get(json, path).String()
}

// ExtractAll returns all gjson Results for the given path expression,
// useful for iterating over arrays.
func ExtractAll(json, path string) []gjson.Result {
	return gjson.Get(json, path).Array()
}
