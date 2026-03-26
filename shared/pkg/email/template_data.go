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
