// Package rbac defines RBAC permission maps for the payment-order service.
package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all PaymentOrderService gRPC methods.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Write/execute operations: admin, operator
		"/meridian.payment_order.v1.PaymentOrderService/InitiatePaymentOrder": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.payment_order.v1.PaymentOrderService/UpdatePaymentOrder": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.payment_order.v1.PaymentOrderService/CancelPaymentOrder": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.payment_order.v1.PaymentOrderService/ReversePaymentOrder": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// Read operations: admin, operator, auditor
		"/meridian.payment_order.v1.PaymentOrderService/RetrievePaymentOrder": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.payment_order.v1.PaymentOrderService/ListPaymentOrders": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

// ExpectedMethods lists all gRPC methods expected to be registered for this service.
var ExpectedMethods = []string{
	"/meridian.payment_order.v1.PaymentOrderService/InitiatePaymentOrder",
	"/meridian.payment_order.v1.PaymentOrderService/RetrievePaymentOrder",
	"/meridian.payment_order.v1.PaymentOrderService/UpdatePaymentOrder",
	"/meridian.payment_order.v1.PaymentOrderService/CancelPaymentOrder",
	"/meridian.payment_order.v1.PaymentOrderService/ListPaymentOrders",
	"/meridian.payment_order.v1.PaymentOrderService/ReversePaymentOrder",
}
