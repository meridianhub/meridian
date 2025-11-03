#!/usr/bin/env bash
#
# Keycloak Setup Script
# Automatically configures Keycloak for local development with:
# - Realm: meridian
# - Client: meridian-service
# - Test user: developer@meridian.local / developer
# - Roles: user, admin
#
# This script is idempotent - safe to run multiple times

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:18080}"
ADMIN_USER="${KEYCLOAK_ADMIN:-admin}"
ADMIN_PASSWORD="${KEYCLOAK_ADMIN_PASSWORD:-admin}"
REALM_NAME="meridian"
CLIENT_ID="meridian-service"
TEST_USER="developer@meridian.local"
TEST_PASSWORD="developer"

echo "========================================="
echo "Keycloak Setup for Meridian"
echo "========================================="
echo ""
echo "Keycloak URL: $KEYCLOAK_URL"
echo "Realm: $REALM_NAME"
echo "Client ID: $CLIENT_ID"
echo ""

# Function to wait for Keycloak to be ready
wait_for_keycloak() {
    echo -n "Waiting for Keycloak to be ready..."
    for i in {1..60}; do
        if curl -sf "$KEYCLOAK_URL/health/ready" > /dev/null 2>&1; then
            echo -e " ${GREEN}✓${NC}"
            return 0
        fi
        echo -n "."
        sleep 2
    done
    echo -e " ${RED}✗${NC}"
    echo "Error: Keycloak did not become ready in time"
    exit 1
}

# Function to get admin access token
get_admin_token() {
    curl -sf -X POST "$KEYCLOAK_URL/realms/master/protocol/openid-connect/token" \
        -H "Content-Type: application/x-www-form-urlencoded" \
        -d "username=$ADMIN_USER" \
        -d "password=$ADMIN_PASSWORD" \
        -d "grant_type=password" \
        -d "client_id=admin-cli" \
        | jq -r '.access_token'
}

# Function to check if realm exists
realm_exists() {
    local token=$1
    curl -sf -H "Authorization: Bearer $token" \
        "$KEYCLOAK_URL/admin/realms/$REALM_NAME" > /dev/null 2>&1
}

# Function to create realm
create_realm() {
    local token=$1
    echo -n "Creating realm '$REALM_NAME'..."

    curl -sf -X POST "$KEYCLOAK_URL/admin/realms" \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -d @- <<EOF > /dev/null
{
  "realm": "$REALM_NAME",
  "enabled": true,
  "displayName": "Meridian",
  "displayNameHtml": "<strong>Meridian</strong>",
  "accessTokenLifespan": 3600,
  "ssoSessionIdleTimeout": 1800,
  "ssoSessionMaxLifespan": 36000,
  "offlineSessionIdleTimeout": 2592000,
  "accessCodeLifespan": 60,
  "accessCodeLifespanUserAction": 300,
  "accessCodeLifespanLogin": 1800,
  "loginTheme": "keycloak",
  "accountTheme": "keycloak",
  "adminTheme": "keycloak",
  "emailTheme": "keycloak",
  "eventsEnabled": true,
  "eventsExpiration": 259200,
  "eventsListeners": ["jboss-logging"],
  "enabledEventTypes": [
    "LOGIN", "LOGIN_ERROR", "LOGOUT", "REGISTER", "REGISTER_ERROR",
    "UPDATE_EMAIL", "UPDATE_PASSWORD", "UPDATE_PASSWORD_ERROR",
    "VERIFY_EMAIL", "VERIFY_EMAIL_ERROR"
  ],
  "adminEventsEnabled": true,
  "adminEventsDetailsEnabled": true
}
EOF
    echo -e " ${GREEN}✓${NC}"
}

# Function to check if client exists
client_exists() {
    local token=$1
    curl -sf -H "Authorization: Bearer $token" \
        "$KEYCLOAK_URL/admin/realms/$REALM_NAME/clients?clientId=$CLIENT_ID" \
        | jq -e 'length > 0' > /dev/null 2>&1
}

# Function to create client
create_client() {
    local token=$1
    echo -n "Creating client '$CLIENT_ID'..."

    curl -sf -X POST "$KEYCLOAK_URL/admin/realms/$REALM_NAME/clients" \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -d @- <<EOF > /dev/null
{
  "clientId": "$CLIENT_ID",
  "name": "Meridian Service",
  "description": "Meridian gRPC service",
  "enabled": true,
  "protocol": "openid-connect",
  "publicClient": false,
  "bearerOnly": false,
  "standardFlowEnabled": true,
  "implicitFlowEnabled": false,
  "directAccessGrantsEnabled": true,
  "serviceAccountsEnabled": true,
  "authorizationServicesEnabled": false,
  "redirectUris": ["http://localhost:*"],
  "webOrigins": ["+"],
  "attributes": {
    "access.token.lifespan": "3600",
    "use.refresh.tokens": "true"
  }
}
EOF
    echo -e " ${GREEN}✓${NC}"
}

# Function to check if user exists
user_exists() {
    local token=$1
    curl -sf -H "Authorization: Bearer $token" \
        "$KEYCLOAK_URL/admin/realms/$REALM_NAME/users?username=$TEST_USER" \
        | jq -e 'length > 0' > /dev/null 2>&1
}

# Function to create test user
create_test_user() {
    local token=$1
    echo -n "Creating test user '$TEST_USER'..."

    # Create user
    curl -sf -X POST "$KEYCLOAK_URL/admin/realms/$REALM_NAME/users" \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -d @- <<EOF > /dev/null
{
  "username": "$TEST_USER",
  "email": "$TEST_USER",
  "emailVerified": true,
  "enabled": true,
  "firstName": "Developer",
  "lastName": "User",
  "credentials": [{
    "type": "password",
    "value": "$TEST_PASSWORD",
    "temporary": false
  }],
  "realmRoles": ["user"],
  "attributes": {
    "department": ["Engineering"],
    "title": ["Software Developer"]
  }
}
EOF
    echo -e " ${GREEN}✓${NC}"
}

# Main execution
main() {
    wait_for_keycloak

    echo -n "Getting admin token..."
    TOKEN=$(get_admin_token)
    if [[ -z "$TOKEN" || "$TOKEN" == "null" ]]; then
        echo -e " ${RED}✗${NC}"
        echo "Error: Failed to get admin token"
        exit 1
    fi
    echo -e " ${GREEN}✓${NC}"

    # Create realm if it doesn't exist
    if realm_exists "$TOKEN"; then
        echo -e "${YELLOW}ℹ${NC} Realm '$REALM_NAME' already exists"
    else
        create_realm "$TOKEN"
    fi

    # Create client if it doesn't exist
    if client_exists "$TOKEN"; then
        echo -e "${YELLOW}ℹ${NC} Client '$CLIENT_ID' already exists"
    else
        create_client "$TOKEN"
    fi

    # Create test user if it doesn't exist
    if user_exists "$TOKEN"; then
        echo -e "${YELLOW}ℹ${NC} User '$TEST_USER' already exists"
    else
        create_test_user "$TOKEN"
    fi

    echo ""
    echo "========================================="
    echo -e "${GREEN}✓ Keycloak setup complete!${NC}"
    echo "========================================="
    echo ""
    echo "Configuration:"
    echo "  Realm:         $REALM_NAME"
    echo "  Client ID:     $CLIENT_ID"
    echo "  Test User:     $TEST_USER"
    echo "  Test Password: $TEST_PASSWORD"
    echo ""
    echo "JWKS Endpoint:"
    echo "  $KEYCLOAK_URL/realms/$REALM_NAME/protocol/openid-connect/certs"
    echo ""
    echo "Token Endpoint:"
    echo "  $KEYCLOAK_URL/realms/$REALM_NAME/protocol/openid-connect/token"
    echo ""
    echo "Admin Console:"
    echo "  $KEYCLOAK_URL (admin/admin)"
    echo ""
    echo "To get a test token:"
    echo "  curl -X POST '$KEYCLOAK_URL/realms/$REALM_NAME/protocol/openid-connect/token' \\"
    echo "    -d 'grant_type=password' \\"
    echo "    -d 'client_id=$CLIENT_ID' \\"
    echo "    -d 'username=$TEST_USER' \\"
    echo "    -d 'password=$TEST_PASSWORD'"
    echo ""
}

main "$@"
