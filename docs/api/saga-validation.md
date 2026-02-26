# Saga Validation REST API

## Overview

The Saga Validation API allows you to validate saga scripts without deploying them.
The API performs dry-run execution to check for syntax errors, type mismatches,
and complexity analysis.

## Endpoint

```http
POST /v1/sagas/validate
```

## Authentication

This endpoint requires authentication via JWT token or API key:

- **JWT**: Include `Authorization: Bearer <token>` header
- **API Key**: Include `X-API-Key: <key>` header

## Request

### Headers

| Header | Required | Description |
|--------|----------|-------------|
| `Content-Type` | Yes | Must be `application/json` or `application/connect+proto` |
| `Authorisation` | Yes* | JWT bearer token for user authentication |
| `X-API-Key` | Yes* | API key for service-to-service authentication |

*One of `Authorisation` or `X-API-Key` is required.

### Request Body (JSON)

```json
{
  "saga_name": "string",
  "script": "string",
  "version": "string",
  "handlers_yaml": "string (optional)"
}
```

#### Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `saga_name` | string | Yes | Unique identifier for the saga |
| `script` | string | Yes | Starlark script to validate |
| `version` | string | Yes | Semantic version (e.g., "1.0.0") |
| `handlers_yaml` | string | No | Optional custom handlers schema (defaults to platform handlers) |

### Example Request

```bash
curl -X POST https://api.meridianhub.cloud/v1/sagas/validate \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer eyJhbGciOiJSUzI1NiIs..." \
  -d '{
    "saga_name": "payment_withdrawal",
    "script": "lien = payment.create_lien(amount=\"100.00\", customer_id=\"cust_123\")",
    "version": "1.0.0"
  }'
```

## Response

### Success Response (200 OK)

```json
{
  "success": true,
  "errors": [],
  "metrics": {
    "handler_call_count": 2,
    "operation_count": 8,
    "complexity_score": 5,
    "max_nesting_depth": 2,
    "conditional_branches": 2
  },
  "formatted_report": "Validation successful\\nMetrics: 2 handlers, complexity 5/10\\n"
}
```

#### Response Fields

| Field | Type | Description |
|-------|------|-------------|
| `success` | boolean | `true` if validation passed, `false` if errors found |
| `errors` | array | List of validation error objects (empty if success) |
| `metrics` | object | Complexity and usage metrics |
| `formatted_report` | string | Human-readable validation report |

#### Metrics Object

| Field | Type | Description |
|-------|------|-------------|
| `handler_call_count` | int32 | Number of handler calls in the script |
| `operation_count` | int32 | Total number of operations (assignments, conditionals, etc.) |
| `complexity_score` | int32 | Cyclomatic complexity score |
| `max_nesting_depth` | int32 | Maximum nesting level of control structures |
| `conditional_branches` | int32 | Number of conditional branches (if/else) |

### Error Response (200 OK, success: false)

When validation fails, the response still returns 200 OK but with `success: false` and populated `errors` array:

```json
{
  "success": false,
  "errors": [
    {
      "type": "SYNTAX_ERROR",
      "message": "undefined: payment.invalid_handler",
      "line": 2,
      "column": 16,
      "severity": "ERROR"
    }
  ],
  "metrics": {
    "handler_call_count": 0,
    "operation_count": 0,
    "complexity_score": 0,
    "max_nesting_depth": 0,
    "conditional_branches": 0
  },
  "formatted_report": "Validation failed\\nLine 2:16 - undefined handler\\n"
}
```

#### Error Object

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Error type (SYNTAX_ERROR, TYPE_MISMATCH, HANDLER_NOT_FOUND, etc.) |
| `message` | string | Human-readable error description |
| `line` | int32 | Line number where error occurred (0 if unknown) |
| `column` | int32 | Column number where error occurred (0 if unknown) |
| `severity` | string | ERROR, WARNING, or INFO |

### HTTP Error Responses

| Status Code | Description |
|-------------|-------------|
| 400 Bad Request | Invalid request body or missing required fields |
| 401 Unauthorized | Missing or invalid authentication credentials |
| 403 Forbidden | Authenticated but not authorised for this tenant |
| 404 Not Found | Endpoint not found (check URL path) |
| 500 Internal Server Error | Server error during validation |
| 503 Service Unavailable | Saga validation service temporarily unavailable |

#### Example Error Response (400 Bad Request)

```json
{
  "code": "invalid_argument",
  "message": "saga_name is required",
  "details": []
}
```

## Usage Examples

### Valid Saga Script

```bash
curl -X POST https://api.meridianhub.cloud/v1/sagas/validate \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "saga_name": "simple_test",
    "script": "result = test_service.test_method()",
    "version": "1.0.0"
  }'
```

**Response:**

```json
{
  "success": true,
  "errors": [],
  "metrics": {
    "handler_call_count": 1,
    "operation_count": 1,
    "complexity_score": 0,
    "max_nesting_depth": 0,
    "conditional_branches": 0
  },
  "formatted_report": "Validation successful\\nHandler calls: 1, Complexity: 0/10\\n"
}
```

### Invalid Saga Script (Handler Not Found)

```bash
curl -X POST https://api.meridianhub.cloud/v1/sagas/validate \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "saga_name": "invalid_test",
    "script": "result = nonexistent.handler()",
    "version": "1.0.0"
  }'
```

**Response:**

```json
{
  "success": false,
  "errors": [
    {
      "type": "HANDLER_NOT_FOUND",
      "message": "handler not found: nonexistent.handler",
      "line": 1,
      "column": 9,
      "severity": "ERROR"
    }
  ],
  "metrics": {
    "handler_call_count": 0,
    "operation_count": 0,
    "complexity_score": 0,
    "max_nesting_depth": 0,
    "conditional_branches": 0
  },
  "formatted_report": "Validation failed\\nLine 1:9 - handler not found\\n"
}
```

### Complex Saga with Multiple Handlers

```bash
curl -X POST https://api.meridianhub.cloud/v1/sagas/validate \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "saga_name": "payment_flow",
    "script": "lien = payment.create_lien(amount=\"100.00\", customer_id=\"cust_123\")",
    "version": "1.0.0"
  }'
```

**Response:**

```json
{
  "success": true,
  "errors": [],
  "metrics": {
    "handler_call_count": 3,
    "operation_count": 14,
    "complexity_score": 8,
    "max_nesting_depth": 2,
    "conditional_branches": 2
  },
  "formatted_report": "Validation successful\\nHandler calls: 1, Complexity: 0/10\\n"
}
```

## Using with Meridian CLI

The Meridian CLI provides a convenient wrapper for saga validation:

```bash
# Validate from file
meridian-cli saga validate --file payment_saga.star

# Validate inline script
meridian-cli saga validate --script "result = payment.create_lien(amount=\"100.00\")"

# With custom version
meridian-cli saga validate --file saga.star --version 2.0.0
```

See `meridian-cli saga validate --help` for more options.

## Rate Limits

- **Authenticated users**: 100 requests per minute
- **API keys**: Configured per key (default 100 req/min)

Rate limit headers are included in responses:

```http
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 95
X-RateLimit-Reset: 1704067200
```

## Best Practices

1. **Validate before deployment**: Always validate saga scripts before deploying to production
2. **Monitor complexity**: Scripts with complexity > 50 may be difficult to maintain
3. **Handle errors gracefully**: Check `success` field before processing `metrics`
4. **Use versioning**: Increment `version` when making changes to saga scripts
5. **Test locally**: Use the CLI for local validation during development

## Related APIs

- [POST /v1/sagas](./saga-registry.md#create-saga) - Deploy a validated saga
- [PATCH /v1/sagas/{id}](./saga-registry.md#update-saga) - Update an existing saga
- [POST /v1/sagas/{id}/activate](./saga-registry.md#activate-saga) - Activate a saga draft

## Support

For questions or issues:

- **Documentation**: <https://docs.meridianhub.cloud>
- **GitHub Issues**: <https://github.com/meridianhub/meridian/issues>
- **Community**: <https://discord.gg/meridianhub>
