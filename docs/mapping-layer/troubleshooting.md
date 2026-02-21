# Mapping Layer — Troubleshooting

## CEL Compilation Errors

### "found no matching overload for '_>_' applied to '(double, int)'"

**Cause**: CEL is strongly typed. `double()` returns a `double`, and `0` is an `int`.
There is no built-in `double > int` overload.

**Fix**: Use a float literal.

```cel
# WRONG
double(payload.amount) > 0

# CORRECT
double(payload.amount) > 0.0
```

### "undeclared reference to 'input'"

**Cause**: In `inbound_validation_cel`, the variable is `payload`, not `input`.
The `input` variable is only available inside computed field `cel_expression` strings.

```cel
# Validation expression - use 'payload'
has(payload.party_type) && size(payload.full_name) > 0

# Computed field expression - use 'input'
has(input.first_name) ? input.first_name : input.full_name
```

### "no such key" at runtime

**Cause**: Accessing a field that may not be present without a `has()` guard.

**Fix**: Guard with `has()` before accessing:

```cel
has(input.middle_name) ? input.middle_name : ''
```

## Transform Errors

### Enum value not mapped

If an incoming enum value has no entry in `enum_mapping.values` and no `fallback` is
set, the transform returns an error. Either add the value to the map or add a `fallback`:

```json
{
  "transform": {
    "enum_mapping": {
      "values": {
        "individual": "PARTY_TYPE_PERSON"
      },
      "fallback": "PARTY_TYPE_UNSPECIFIED"
    }
  }
}
```

### Date parse failure

`date_format` uses Go time layout strings. The most common layouts:

| Format | Go Layout |
|--------|-----------|
| ISO date | `2006-01-02` |
| ISO datetime UTC | `2006-01-02T15:04:05Z` |
| ISO datetime with zone | `2006-01-02T15:04:05Z07:00` |

If the incoming value does not match the layout exactly, the transform returns an error.
Use `DryRunInbound` to test layouts before registering the definition.

### Attribute flatten produces empty list

`attribute_flatten` only includes keys that are present in the input. If all
`source_keys` are absent, the output list is empty (not an error). This is expected
behaviour — add required-field validation in `inbound_validation_cel` if at least one
key must be present.

## Batch Transform Errors

### "input must be a JSON array"

Batch mappings (`is_batch: true`) require the HTTP body to be a JSON array `[...]`.
A single object `{...}` will be rejected.

### Per-element validation failure

When a batch item fails validation, the entire batch request is rejected with a 400
status. The error message includes the index of the failing element.

## DryRun Debugging

Use `DryRunInbound` to inspect transforms before registering:

```go
result := engine.DryRunInbound(def, inputJSON)
if result.TransformError != nil {
    // CEL compilation or parse error
}
if !result.ValidationPassed {
    fmt.Println("Validation errors:", result.ValidationErrors)
}
for _, trace := range result.FieldTraces {
    fmt.Printf("%s -> %s: %v\n", trace.ExternalPath, trace.InternalPath, trace.OutputValue)
}
```

The `FieldTraces` slice shows each field that was processed, making it straightforward
to identify which field rules are not behaving as expected.
