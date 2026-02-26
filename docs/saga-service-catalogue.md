# Saga Service Catalogue

This document provides a reference for all saga handlers available in the Meridian platform.

## current_account.create_lien

Create a lien (hold) on an account for a specified amount

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| account_id | string | ✓ | Identifier of the account to place lien on |
| amount | Decimal | ✓ | Amount to hold (must be positive) |

### Returns

| Name | Type | Description |
|------|------|-------------|
| account_id | string | Echo of the input account ID |
| amount | Decimal | Echo of the input amount |
| lien_id | string | Generated lien identifier (UUID) |
| status | string | Status of the lien (ACTIVE) |

**Compensation Handler:** `current_account.terminate_lien`

### Example Usage

```starlark
result = invoke_handler("current_account.create_lien", {
    "account_id": <value>,
    "amount": <value>
})
```

---

## current_account.execute_lien

Execute (consume) a previously created lien

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| lien_id | string | ✓ | Identifier of the lien to execute |

### Returns

| Name | Type | Description |
|------|------|-------------|
| lien_id | string | Echo of the input lien ID |
| status | string | Status of the lien (EXECUTED) |

### Example Usage

```starlark
result = invoke_handler("current_account.execute_lien", {
    "lien_id": <value>
})
```

---

## current_account.terminate_lien

Terminate (release) a lien without execution (compensation handler)

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| lien_id | string | ✓ | Identifier of the lien to terminate |

### Returns

| Name | Type | Description |
|------|------|-------------|
| lien_id | string | Echo of the input lien ID |
| status | string | Status of the lien (TERMINATED) |

### Example Usage

```starlark
result = invoke_handler("current_account.terminate_lien", {
    "lien_id": <value>
})
```

---

## financial_accounting.create_booking

Create a booking log entry for audit purposes

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| description | string | ✓ | Human-readable description of the booking |

### Returns

| Name | Type | Description |
|------|------|-------------|
| booking_id | string | Generated booking identifier (UUID) |
| description | string | Echo of the input description |
| status | string | Status of the booking (CREATED) |

### Example Usage

```starlark
result = invoke_handler("financial_accounting.create_booking", {
    "description": <value>
})
```

---

## financial_accounting.post_entries

Post double-entry accounting entries to the ledger

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| entries | array | ✓ | Array of accounting entries to post |

### Returns

| Name | Type | Description |
|------|------|-------------|
| posting_ids | array | Array of generated posting IDs (UUIDs) |
| status | string | Status of the posting (POSTED) |

**Compensation Handler:** `financial_accounting.reverse_entries`

### Example Usage

```starlark
result = invoke_handler("financial_accounting.post_entries", {
    "entries": <value>
})
```

---

## financial_accounting.reverse_entries

Reverse previously posted accounting entries (compensation handler)

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| posting_ids | array | ✓ | Array of posting IDs to reverse |

### Returns

| Name | Type | Description |
|------|------|-------------|
| original_posting_ids | array | Echo of the input posting IDs |
| status | string | Status of the reversal (REVERSED) |

### Example Usage

```starlark
result = invoke_handler("financial_accounting.reverse_entries", {
    "posting_ids": <value>
})
```

---

## notification.send

Send a notification to a recipient

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| recipient | string | ✓ | Recipient identifier (email, phone, user ID) |
| type | string | ✓ | Notification type (e.g., EMAIL, SMS, PUSH) |

### Returns

| Name | Type | Description |
|------|------|-------------|
| notification_id | string | Generated notification identifier (UUID) |
| recipient | string | Echo of the input recipient |
| status | string | Status of the notification (SENT) |
| type | string | Echo of the input notification type |

### Example Usage

```starlark
result = invoke_handler("notification.send", {
    "recipient": <value>,
    "type": <value>
})
```

---

## position_keeping.cancel_log

Cancel a position log entry (compensation handler)

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| log_id | string | ✓ | Identifier of the log entry to cancel |

### Returns

| Name | Type | Description |
|------|------|-------------|
| log_id | string | Echo of the input log ID |
| status | string | Status of the log entry (CANCELLED) |

### Example Usage

```starlark
result = invoke_handler("position_keeping.cancel_log", {
    "log_id": <value>
})
```

---

## position_keeping.initiate_log

Initiate a position log entry for a DEBIT or CREDIT transaction

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| amount | Decimal | ✓ | Transaction amount (must be positive) |
| direction | enum (DEBIT, CREDIT) | ✓ | Direction of the transaction |
| position_id | string | ✓ | Unique identifier for the position |

### Returns

| Name | Type | Description |
|------|------|-------------|
| amount | Decimal | Echo of the input amount |
| direction | string | Echo of the input direction |
| log_id | string | Generated log entry identifier (UUID) |
| position_id | string | Echo of the input position ID |
| status | string | Status of the log entry (INITIATED) |

**Compensation Handler:** `position_keeping.cancel_log`

### Example Usage

```starlark
result = invoke_handler("position_keeping.initiate_log", {
    "amount": <value>,
    "direction": <value>,
    "position_id": <value>
})
```

---

## position_keeping.update_log

Update an existing position log entry

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| log_id | string | ✓ | Identifier of the log entry to update |

### Returns

| Name | Type | Description |
|------|------|-------------|
| log_id | string | Echo of the input log ID |
| status | string | Status of the log entry (UPDATED) |

### Example Usage

```starlark
result = invoke_handler("position_keeping.update_log", {
    "log_id": <value>
})
```

---

## repository.save

Persist an entity to the repository

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| entity | map | ✓ | Entity data to persist |
| entity_type | string | ✓ | Type of entity being saved (e.g., Account, Transaction) |

### Returns

| Name | Type | Description |
|------|------|-------------|
| entity | map | Echo of the saved entity |
| entity_id | string | Generated or existing entity identifier (UUID) |
| entity_type | string | Echo of the input entity type |
| status | string | Status of the save operation (SAVED) |

### Example Usage

```starlark
result = invoke_handler("repository.save", {
    "entity": <value>,
    "entity_type": <value>
})
```

---

## valuation_engine.valuate

Valuate an instrument at a specific point in time

### Parameters

| Name | Type | Required | Description |
|------|------|----------|-------------|
| context_type | string | ✓ | Valuation context (e.g., MARK_TO_MARKET, FAIR_VALUE) |
| instrument | string | ✓ | Instrument identifier or code |
| quantity | Decimal | ✓ | Quantity to valuate |

### Returns

| Name | Type | Description |
|------|------|-------------|
| context_type | string | Echo of the input context type |
| currency | string | Currency of the valuation |
| instrument | string | Echo of the input instrument |
| quantity | Decimal | Echo of the input quantity |
| unit_price | Decimal | Price per unit of the instrument |
| value | Decimal | Total value (quantity * unit_price) |
| valued_at | string | Timestamp when valuation was computed |

### Example Usage

```starlark
result = invoke_handler("valuation_engine.valuate", {
    "context_type": <value>,
    "instrument": <value>,
    "quantity": <value>
})
```

---
