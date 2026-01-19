// Package service provides gRPC service implementations for the Market Information service.
// BIAN Service Domain: Market Information Management
package service

import (
	"context"
	"errors"
	"log/slog"
	"os"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

// Service initialization errors.
var (
	// ErrDataSetRepositoryNil is returned when attempting to create a server with a nil DataSetRepository.
	ErrDataSetRepositoryNil = errors.New("market information server: dataset repository cannot be nil")

	// ErrObservationRepositoryNil is returned when attempting to create a server with a nil ObservationRepository.
	ErrObservationRepositoryNil = errors.New("market information server: observation repository cannot be nil")

	// ErrSourceRepositoryNil is returned when attempting to create a server with a nil SourceRepository.
	ErrSourceRepositoryNil = errors.New("market information server: source repository cannot be nil")
)

// EventPublisher defines the interface for publishing domain events to Kafka.
// Implementations should handle serialization and delivery to the messaging infrastructure.
type EventPublisher interface {
	// Publish publishes a domain event to the appropriate Kafka topic.
	// Returns an error if publishing fails.
	Publish(ctx context.Context, event any) error
}

// Server implements the MarketInformationService gRPC service.
// It holds all required dependencies and embeds UnimplementedMarketInformationServiceServer
// for forward compatibility.
type Server struct {
	marketinformationv1.UnimplementedMarketInformationServiceServer

	dataSetRepo     domain.DataSetRepository
	observationRepo domain.ObservationRepository
	sourceRepo      domain.SourceRepository
	celValidator    *CelValidator
	eventPublisher  EventPublisher
	logger          *slog.Logger
}

// Option configures optional dependencies for Server.
type Option func(*Server)

// WithCelValidator sets an optional CEL validator for expression validation.
// If not set or set to nil, CEL validation is skipped for backwards compatibility.
func WithCelValidator(validator *CelValidator) Option {
	return func(s *Server) {
		s.celValidator = validator
	}
}

// WithEventPublisher sets an optional event publisher for Kafka publishing.
// If not set or set to nil, event publishing is skipped.
func WithEventPublisher(publisher EventPublisher) Option {
	return func(s *Server) {
		s.eventPublisher = publisher
	}
}

// WithLogger sets a custom logger for the server.
// If not set, defaults to a JSON handler writing to stdout.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) {
		s.logger = logger
	}
}

// NewServer creates a new Market Information gRPC server with dependency injection.
//
// Required Dependencies:
//   - dataSetRepo: Persistence layer for dataset definitions (must not be nil)
//   - observationRepo: Persistence layer for market price observations (must not be nil)
//   - sourceRepo: Persistence layer for data sources (must not be nil)
//
// Optional dependencies can be provided via Option functions:
//   - WithCelValidator: Enables CEL validation of expressions
//   - WithEventPublisher: Enables publishing domain events to Kafka
//   - WithLogger: Sets a custom logger (defaults to JSON stdout)
//
// Returns an error if any required dependency is nil.
func NewServer(
	dataSetRepo domain.DataSetRepository,
	observationRepo domain.ObservationRepository,
	sourceRepo domain.SourceRepository,
	opts ...Option,
) (*Server, error) {
	if dataSetRepo == nil {
		return nil, ErrDataSetRepositoryNil
	}
	if observationRepo == nil {
		return nil, ErrObservationRepositoryNil
	}
	if sourceRepo == nil {
		return nil, ErrSourceRepositoryNil
	}

	srv := &Server{
		dataSetRepo:     dataSetRepo,
		observationRepo: observationRepo,
		sourceRepo:      sourceRepo,
	}

	// Apply optional configurations
	for _, opt := range opts {
		opt(srv)
	}

	// Default logger to stdout JSON handler if not provided
	if srv.logger == nil {
		srv.logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	return srv, nil
}
