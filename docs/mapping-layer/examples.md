# Mapping Layer — Reference Examples

Three reference mapping definitions are provided in `services/reference-data/examples/`.
Each demonstrates a distinct integration pattern.

---

## 1. Bank Party Onboarding (`bank-x-party-onboarding.json`)

**Pattern**: Field renaming + enum mapping + conditional computed field + bidirectional

### Use case

A bank sends party data in its own schema. Meridian maps it to the internal `RegisterParty`
RPC, translating enum values and deriving `display_name` from `first_name` when present.

### Key features

- `enum_mapping`: maps `"individual"` to `"PARTY_TYPE_PERSON"`,
  `"corporate"` to `"PARTY_TYPE_ORGANIZATION"`
- `date_format`: parses `date_of_birth` using Go layout `"2006-01-02"`
- Nested target path: `"govt_id"` maps to `"reference.government_id"`
- Inbound computed: `display_name` = `first_name` if present, else `full_name`
- Outbound computed: reconstructs `full_name` from `legal_name`
- Idempotency via `govt_id`

### Sample input

```json
{
  "party_type": "individual",
  "full_name": "Jane Smith",
  "first_name": "Jane",
  "date_of_birth": "1990-05-15",
  "govt_id": "UK-12345",
  "email": "jane@example.com",
  "phone": "+44-7700-900000"
}
```

### Sample output

```json
{
  "party_type": "PARTY_TYPE_PERSON",
  "legal_name": "Jane Smith",
  "date_of_birth": "1990-05-15",
  "reference": {"government_id": "UK-12345"},
  "contact": {"email": "jane@example.com", "phone": "+44-7700-900000"},
  "display_name": "Jane"
}
```

### Validation rule

```cel
has(payload.party_type) && has(payload.full_name) && size(payload.full_name) > 0
```

---

## 2. Energy Metering Feed (`energy-metering-feed.json`)

**Pattern**: Batch transform + attribute flattening + quality enum + computed idempotency key

### Use case

A utility sends arrays of meter readings. Meridian batches them into
`RecordObservationBatch`, normalising the quality enum and collecting `tenor` and
`settlement_date` into a structured attributes list.

### Key features

- `is_batch: true`: input is a JSON array; each element is transformed independently
- `batch_target_path`: output array is placed at `"observations"` in the request body
- `attribute_flatten`: collects `tenor` and `settlement_date` into `[{key, value}]` pairs
- Inbound computed: `resolution_key_value` = `meter_id + ':' + timestamp`
- Content-hash idempotency over `meter_id` + `timestamp`

### Sample input (single element in array)

```json
[
  {
    "meter_id": "METER-001",
    "timestamp": "2024-01-15T10:00:00Z",
    "value_kwh": 42.5,
    "quality": "actual",
    "tenor": "DAY_AHEAD",
    "settlement_date": "2024-01-16"
  }
]
```

### Sample output

```json
{
  "observations": [
    {
      "source_id": "METER-001",
      "observed_at": "2024-01-15T10:00:00Z",
      "quantity": 42.5,
      "data_quality": "DATA_QUALITY_ACTUAL",
      "attributes": [
        {"key": "tenor", "value": "DAY_AHEAD"},
        {"key": "settlement_date", "value": "2024-01-16"}
      ],
      "resolution_key_value": "METER-001:2024-01-15T10:00:00Z"
    }
  ]
}
```

### Validation rule

```cel
has(payload.meter_id) && has(payload.value_kwh) && double(payload.value_kwh) >= 0.0
```

---

## 3. Verra Carbon Credit (`verra-carbon-credit.json`)

**Pattern**: Attribute flattening + computed dimension + singleton idempotency

### Use case

A carbon registry posts VCS project data. Meridian maps it to `RegisterInstrument`,
collecting project metadata into attributes and injecting the asset dimension `"Carbon"`
automatically.

### Key features

- `attribute_flatten`: collects `registry`, `vintage_year`, `methodology` into
  attributes list
- `date_format`: parses `validation_date` with layout `"2006-01-02"`
- Inbound computed: `dimension` = `'Carbon'` (literal CEL string)
- Idempotency via `vcs_id`

### Sample input

```json
{
  "vcs_id": "VCS-2845",
  "project_name": "Kariba REDD+ Project",
  "vintage_year": 2023,
  "methodology": "VM0009",
  "registry": "Verra",
  "min_amount": 1.0,
  "validation_date": "2024-03-01"
}
```

### Sample output

```json
{
  "code": "VCS-2845",
  "display_name": "Kariba REDD+ Project",
  "validation_date": "2024-03-01",
  "min_amount": 1.0,
  "attributes": [
    {"key": "registry", "value": "Verra"},
    {"key": "vintage_year", "value": "2023"},
    {"key": "methodology", "value": "VM0009"}
  ],
  "dimension": "Carbon"
}
```

### Validation rule

```cel
has(payload.vcs_id) && has(payload.min_amount) && double(payload.min_amount) > 0.0
```
