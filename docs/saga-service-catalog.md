# Saga Service Catalog

This document provides a reference for all saga handlers available in the Meridian platform.



## current_account.control

Perform lifecycle control action on an account (FREEZE, UNFREEZE, CLOSE)

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = current_account.control(

)
```

---


## current_account.create_lien

Create a lien (hold) on an account for a specified amount

### Parameters


_No parameters_


### Returns


_No return values_



**Compensation Handler:** `current_account.terminate_lien`


### Example Usage

```starlark
result = current_account.create_lien(

)
```

---


## current_account.execute_lien

Execute (consume) a previously created lien

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = current_account.execute_lien(

)
```

---


## current_account.save

Persist current account metadata for a transaction

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = current_account.save(

)
```

---


## current_account.terminate_lien

Terminate (release) a lien without execution (compensation handler)

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = current_account.terminate_lien(

)
```

---


## financial_accounting.capture_posting

Capture a single-sided posting entry within a booking log

### Parameters


_No parameters_


### Returns


_No return values_



**Compensation Handler:** `financial_accounting.compensate_posting`


### Example Usage

```starlark
result = financial_accounting.capture_posting(

)
```

---


## financial_accounting.compensate_posting

Compensate (reverse) a captured posting entry

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = financial_accounting.compensate_posting(

)
```

---


## financial_accounting.create_booking

Create a booking log entry for audit purposes

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = financial_accounting.create_booking(

)
```

---


## financial_accounting.initiate_booking_log

Initiate a booking log for a deposit or withdrawal transaction

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = financial_accounting.initiate_booking_log(

)
```

---


## financial_accounting.post_entries

Post double-entry accounting entries to the ledger

### Parameters


_No parameters_


### Returns


_No return values_



**Compensation Handler:** `financial_accounting.reverse_entries`


### Example Usage

```starlark
result = financial_accounting.post_entries(

)
```

---


## financial_accounting.reverse_entries

Reverse previously posted accounting entries (compensation handler)

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = financial_accounting.reverse_entries(

)
```

---


## financial_accounting.update_booking_log

Update the status of an existing booking log

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = financial_accounting.update_booking_log(

)
```

---


## financial_gateway.cancel_payment

Cancel a pending payment dispatch (compensation handler)

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = financial_gateway.cancel_payment(

)
```

---


## financial_gateway.dispatch_payment

Dispatch a payment to an external provider via the Financial Gateway

### Parameters


_No parameters_


### Returns


_No return values_



**Compensation Handler:** `financial_gateway.cancel_payment`


### Example Usage

```starlark
result = financial_gateway.dispatch_payment(

)
```

---


## financial_gateway.dispatch_refund

Dispatch a refund for a previously processed payment

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = financial_gateway.dispatch_refund(

)
```

---


## internal_account.get_balance

Query the current balance for an internal account

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = internal_account.get_balance(

)
```

---


## internal_account.initiate

Initiate a new internal account

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = internal_account.initiate(

)
```

---


## internal_account.retrieve

Retrieve an internal account by ID

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = internal_account.retrieve(

)
```

---


## market_information.get_rate

Fetch FX rates for currency pair conversion

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = market_information.get_rate(

)
```

---


## operational_gateway.cancel_instruction

Cancel a pending instruction before dispatch (compensation handler)

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = operational_gateway.cancel_instruction(

)
```

---


## operational_gateway.dispatch_instruction

Queue an instruction for dispatch to an external provider

### Parameters


_No parameters_


### Returns


_No return values_



**Compensation Handler:** `operational_gateway.cancel_instruction`


### Example Usage

```starlark
result = operational_gateway.dispatch_instruction(

)
```

---


## operational_gateway.get_instruction

Get instruction status and details by ID

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = operational_gateway.get_instruction(

)
```

---


## party.get_default_payment_method

Retrieve the default payment method for a party

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = party.get_default_payment_method(

)
```

---


## party.get_structuring_data

Retrieve structuring metadata for a participant in a syndicate

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = party.get_structuring_data(

)
```

---


## party.list_participants

List active participants for a syndicate organization

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = party.list_participants(

)
```

---


## position_keeping.cancel_log

Cancel a position log entry (compensation handler)

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = position_keeping.cancel_log(

)
```

---


## position_keeping.initiate_log

Initiate a position log entry for a DEBIT or CREDIT transaction

### Parameters


_No parameters_


### Returns


_No return values_



**Compensation Handler:** `position_keeping.cancel_log`


### Example Usage

```starlark
result = position_keeping.initiate_log(

)
```

---


## position_keeping.update_log

Update an existing position log entry

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = position_keeping.update_log(

)
```

---


## reconciliation.assert_balance

Evaluate a balance assertion against current positions

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = reconciliation.assert_balance(

)
```

---


## reconciliation.cancel_run

Cancel a settlement run (compensation handler)

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = reconciliation.cancel_run(

)
```

---


## reconciliation.execute_run

Trigger execution of a pending settlement run

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = reconciliation.execute_run(

)
```

---


## reconciliation.initiate_dispute

Raise a formal dispute against a detected variance

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = reconciliation.initiate_dispute(

)
```

---


## reconciliation.initiate_run

Initiate a new settlement reconciliation run

### Parameters


_No parameters_


### Returns


_No return values_



**Compensation Handler:** `reconciliation.cancel_run`


### Example Usage

```starlark
result = reconciliation.initiate_run(

)
```

---


## reconciliation.retrieve_run

Retrieve a settlement run summary

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = reconciliation.retrieve_run(

)
```

---


## reference_data.retrieve_instrument

Retrieve an instrument definition by code and version

### Parameters


_No parameters_


### Returns


_No return values_




### Example Usage

```starlark
result = reference_data.retrieve_instrument(

)
```

---



