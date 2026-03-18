// Package mappers provides conversion utilities between domain types and protobuf types.
//
// The functions in this package bridge the domain layer and the generated proto layer,
// translating between domain primitives (e.g., [money.Currency]) and their protobuf
// enum equivalents (e.g., [commonpb.Currency]).
//
// Deprecated currency enum converters are retained for backward compatibility.
// New code should pass instrument code strings directly to proto fields.
package mappers
