// Package app graceful shutdown logic for releasing container-managed resources.
package app

import (
	"context"
	"fmt"

	"github.com/meridianhub/meridian/shared/platform/audit"
)

// Close gracefully closes all resources in the container
func (c *Container) Close(ctx context.Context) error {
	c.Logger.Info("closing container resources...")

	var errs []error

	c.closeAuditPublisher(&errs) //nolint:contextcheck // publisher manages its own timeout
	c.closeKafkaProducer()       //nolint:contextcheck // producer flush uses millisecond timeout
	c.closeGRPCConnections(&errs)
	c.closeDBPool()
	c.closeRedis(&errs)
	c.closeTracer(ctx, &errs)

	if len(errs) > 0 {
		return fmt.Errorf("%w: %v", ErrContainerCloseFailures, errs)
	}

	c.Logger.Info("container resources closed successfully")
	return nil
}

// closeAuditPublisher flushes and closes the audit publisher.
func (c *Container) closeAuditPublisher(errs *[]error) {
	if c.auditPublisher == nil {
		return
	}
	if err := c.auditPublisher.Close(); err != nil {
		c.Logger.Error("failed to close audit publisher", "error", err)
		*errs = append(*errs, fmt.Errorf("audit publisher close: %w", err))
	} else {
		c.Logger.Info("audit publisher closed")
	}
	audit.SetGlobalPublisher(nil)
}

// closeKafkaProducer flushes and closes the Kafka producer.
func (c *Container) closeKafkaProducer() {
	if c.kafkaProducer == nil {
		return
	}
	remaining := c.kafkaProducer.FlushWithTimeout(5000)
	if remaining > 0 {
		c.Logger.Warn("kafka producer flush incomplete", "remaining_messages", remaining)
	}
	c.kafkaProducer.Close()
	c.Logger.Info("kafka producer closed")
}

// closeGRPCConnections closes all gRPC client connections.
func (c *Container) closeGRPCConnections(errs *[]error) {
	for _, conn := range c.grpcConns {
		if err := conn.Close(); err != nil {
			c.Logger.Error("failed to close gRPC connection", "error", err)
			*errs = append(*errs, fmt.Errorf("grpc connection close: %w", err))
		}
	}
}

// closeDBPool closes the database connection pool.
func (c *Container) closeDBPool() {
	if c.DBPool == nil {
		return
	}
	c.DBPool.Close()
	c.Logger.Info("database pool closed")
}

// closeRedis closes the Redis client.
func (c *Container) closeRedis(errs *[]error) {
	if c.RedisClient == nil {
		return
	}
	if err := c.RedisClient.Close(); err != nil {
		c.Logger.Error("failed to close redis client", "error", err)
		*errs = append(*errs, fmt.Errorf("redis close: %w", err))
	} else {
		c.Logger.Info("redis client closed")
	}
}

// closeTracer shuts down the OpenTelemetry tracer.
func (c *Container) closeTracer(ctx context.Context, errs *[]error) {
	if c.Tracer == nil {
		return
	}
	if err := c.Tracer.Shutdown(ctx); err != nil {
		c.Logger.Error("failed to shutdown tracer", "error", err)
		*errs = append(*errs, fmt.Errorf("tracer shutdown: %w", err))
	} else {
		c.Logger.Info("tracer shutdown complete")
	}
}
