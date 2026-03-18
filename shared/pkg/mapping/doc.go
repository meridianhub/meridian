// Package mapping provides utilities for transforming external JSON payloads
// into internal Meridian gRPC request messages and vice versa.
//
// The bidirectional transformation [Engine] applies MappingDefinition proto transforms
// between external and internal formats. Field extraction from external payloads uses
// gjson path expressions, which support dot notation, array indexing, and modifiers:
//
//	"customer.id"       — nested field
//	"items.#.price"     — array element iteration
//	"meta|@flatten"     — gjson modifier
//
// CEL expressions can be used for value transformation within mapping rules.
// See https://github.com/tidwall/gjson for full path syntax documentation.
package mapping
