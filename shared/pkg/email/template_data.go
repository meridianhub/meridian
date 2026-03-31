package email

// LineItem represents a single line item on an invoice.
type LineItem struct {
	Description string
	Amount      string
}

// InvoiceData holds the data required to render an invoice email.
type InvoiceData struct {
	CustomerName  string
	InvoiceNumber string
	LineItems     []LineItem
	Total         string
	DueDate       string
	PaymentLink   string
}

// DunningNoticeData holds the data required to render a dunning notice email.
type DunningNoticeData struct {
	CustomerName   string
	InvoiceNumber  string
	Amount         string
	DaysOverdue    int
	Severity       int // 1=gentle reminder, 2=urgent notice, 3=final warning
	SupportContact string
}

// PaymentReceivedData holds the data required to render a payment received email.
type PaymentReceivedData struct {
	CustomerName  string
	InvoiceNumber string
	Amount        string
	PaymentDate   string
	ReceiptNumber string
}

// AccountFrozenData holds the data required to render an account frozen email.
type AccountFrozenData struct {
	CustomerName   string
	AccountID      string
	FrozenReason   string
	SupportContact string
}

// VerifyEmailData holds the data required to render an email verification email.
type VerifyEmailData struct {
	TenantName       string
	VerificationLink string
	SupportEmail     string
}

// PasswordResetData holds the data required to render a password reset email.
type PasswordResetData struct {
	TenantName   string
	ResetLink    string
	SupportEmail string
}

// InviteUserData holds the data required to render a user invitation email.
type InviteUserData struct {
	TenantName   string
	TenantSlug   string
	InviterEmail string
	AcceptLink   string
	SupportEmail string
}

// WelcomeData holds the data required to render a welcome email.
type WelcomeData struct {
	TenantName        string
	LoginURL          string
	GettingStartedURL string
	UnsubscribeURL    string // populated by the processor for non-transactional emails
}

// AccountLockoutData holds the data required to render an account lockout email.
type AccountLockoutData struct {
	TenantName   string
	SupportEmail string
	LockoutTime  string
}
