package worker

import "errors"

var (
	// ErrNilRefDataClient is returned when the Reference Data client is nil.
	ErrNilRefDataClient = errors.New("reference data client cannot be nil")
	// ErrNilReconClient is returned when the reconciliation client is nil.
	ErrNilReconClient = errors.New("reconciliation client cannot be nil")
	// ErrNilLeaderElector is returned when the leader elector is nil.
	ErrNilLeaderElector = errors.New("leader elector cannot be nil")
	// ErrNilLogger is returned when the logger is nil.
	ErrNilLogger = errors.New("logger cannot be nil")
	// ErrInvalidPollInterval is returned when the poll interval is invalid.
	ErrInvalidPollInterval = errors.New("poll interval must be greater than zero")
	// ErrInvalidShutdownTimeout is returned when the shutdown timeout is invalid.
	ErrInvalidShutdownTimeout = errors.New("shutdown timeout must be greater than zero")
	// ErrAlreadyRunning is returned when Start is called while already running.
	ErrAlreadyRunning = errors.New("scheduler is already running")
	// ErrRunAlreadyExists is returned when a reconciliation run already exists for the period.
	ErrRunAlreadyExists = errors.New("reconciliation run already exists for this period")
	// ErrNilRunResponse is returned when the gRPC response contains a nil run.
	ErrNilRunResponse = errors.New("initiate reconciliation returned nil run")
	// ErrUnknownScope is returned when the reconciliation scope string is not recognized.
	ErrUnknownScope = errors.New("unknown reconciliation scope")
	// ErrUnknownSettlementType is returned when the settlement type string is not recognized.
	ErrUnknownSettlementType = errors.New("unknown settlement type")
)
