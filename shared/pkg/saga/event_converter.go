// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ErrNilEvent is returned when a nil proto.Message is passed to EventToInputData.
var ErrNilEvent = errors.New("event must not be nil")

// protoJSONMarshaler serializes proto messages with snake_case field names
// matching CEL filter expressions (event.account_id) and Starlark script
// access patterns (input_data["event"]["account_id"]).
var protoJSONMarshaler = protojson.MarshalOptions{
	UseProtoNames:   true,  // snake_case field names per proto definition
	EmitUnpopulated: false, // omit zero/empty values
}

// EventToInputData converts a proto event message to the input_data map passed
// to Starlark saga scripts. The resulting map has two top-level keys:
//   - "event": the proto message fields as a map[string]any with snake_case keys
//   - "metadata": the provided metadata map (may be nil)
//
// Field names use the proto field name (snake_case) rather than JSON camelCase,
// so CEL expressions like `event.account_id` and Starlark access patterns like
// `input_data["event"]["account_id"]` work as expected.
//
// Nested messages become nested maps, repeated fields become []any slices, and
// zero-valued fields are omitted. Timestamp fields are serialized as RFC3339 strings.
func EventToInputData(event proto.Message, metadata map[string]string) (map[string]any, error) {
	if event == nil {
		return nil, ErrNilEvent
	}
	// Guard against typed-nil: a (*T)(nil) passed as proto.Message satisfies
	// event != nil (non-nil interface) but holds a nil pointer that Marshal panics on.
	if v := reflect.ValueOf(event); v.Kind() == reflect.Ptr && v.IsNil() {
		return nil, ErrNilEvent
	}

	jsonBytes, err := protoJSONMarshaler.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal event to JSON: %w", err)
	}

	var eventMap map[string]any
	if err := json.Unmarshal(jsonBytes, &eventMap); err != nil {
		return nil, fmt.Errorf("unmarshal JSON to map: %w", err)
	}

	return map[string]any{
		"event":    eventMap,
		"metadata": metadata,
	}, nil
}
