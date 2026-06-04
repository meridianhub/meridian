package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/samber/lo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Tenant span attribute keys for multi-tenant tracing
const (
	// TenantIDAttrKey is the attribute key for the tenant ID in traces
	TenantIDAttrKey = "tenant.id"
	// TenantSchemaKey is the attribute key for the database schema name
	TenantSchemaKey = "tenant.schema"
)

// parseGRPCMethod parses a gRPC method string (e.g., "/package.Service/Method")
// into service and method components for OpenTelemetry semantic conventions
func parseGRPCMethod(fullMethod string) (service, method string) {
	// Full method format: "/package.Service/Method"
	fullMethod = strings.TrimPrefix(fullMethod, "/")
	parts := strings.Split(fullMethod, "/")

	if len(parts) == 2 {
		return parts[0], parts[1]
	}

	// Fallback: use full method for both if parsing fails
	return fullMethod, fullMethod
}

// grpcPropagator is a cached composite propagator for trace context extraction/injection
// Cached at package level for performance to avoid allocation on every RPC call
var grpcPropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
)

// UnaryServerInterceptor returns a gRPC unary server interceptor for tracing
//
// This interceptor automatically:
//   - Creates spans for each RPC call
//   - Extracts trace context from incoming metadata
//   - Records gRPC method, status code, and errors
//   - Sets span status based on RPC result
//
// Example usage:
//
//	tracer, err := observability.NewTracer(ctx, cfg)
//	if err != nil {
//	    return err
//	}
//
//	grpcServer := grpc.NewServer(
//	    grpc.UnaryInterceptor(tracer.UnaryServerInterceptor()),
//	)
func (t *Tracer) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Extract trace context from incoming metadata
		md, _ := metadata.FromIncomingContext(ctx)
		ctx = extractTraceContext(ctx, md)

		// Parse gRPC method for semantic conventions
		service, method := parseGRPCMethod(info.FullMethod)

		// Start span for this RPC
		ctx, span := t.Start(ctx, info.FullMethod,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.RPCSystemNameKey.String("grpc"),
				attribute.String("rpc.service", service),
				semconv.RPCMethodKey.String(method),
			),
		)
		defer span.End()

		// Call the handler
		resp, err := handler(ctx, req)

		// Add tenant attributes after handler (tenant context set by auth middleware)
		addTenantSpanAttributes(ctx, span)

		// Record error if present
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())

			// Add gRPC status code if available
			if st, ok := status.FromError(err); ok {
				span.SetAttributes(
					attribute.Int("rpc.grpc.status_code", int(st.Code())),
					attribute.String("rpc.grpc.status_message", st.Message()),
				)
			}
		} else {
			span.SetStatus(codes.Ok, "")
			span.SetAttributes(attribute.Int("rpc.grpc.status_code", 0))
		}

		return resp, err
	}
}

// StreamServerInterceptor returns a gRPC stream server interceptor for tracing
//
// This interceptor automatically:
//   - Creates spans for streaming RPC calls
//   - Extracts trace context from incoming metadata
//   - Records stream events (send, receive, close)
//   - Adds tenant attributes for multi-tenant tracing
//
// Example usage:
//
//	grpcServer := grpc.NewServer(
//	    grpc.UnaryInterceptor(tracer.UnaryServerInterceptor()),
//	    grpc.StreamInterceptor(tracer.StreamServerInterceptor()),
//	)
func (t *Tracer) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ctx := ss.Context()

		// Extract trace context from incoming metadata
		md, _ := metadata.FromIncomingContext(ctx)
		ctx = extractTraceContext(ctx, md)

		// Parse gRPC method for semantic conventions
		service, method := parseGRPCMethod(info.FullMethod)

		// Start span for this stream
		ctx, span := t.Start(ctx, info.FullMethod,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.RPCSystemNameKey.String("grpc"),
				attribute.String("rpc.service", service),
				semconv.RPCMethodKey.String(method),
				attribute.Bool("rpc.stream", true),
			),
		)
		defer span.End()

		// Wrap server stream to propagate context
		wrappedStream := &tracedServerStream{
			ServerStream: ss,
			ctx:          ctx,
			tracer:       t,
		}

		// Call the handler
		err := handler(srv, wrappedStream)

		// Add tenant attributes after handler (tenant context set by auth middleware)
		addTenantSpanAttributes(wrappedStream.ctx, span)

		// Record error if present
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())

			if st, ok := status.FromError(err); ok {
				span.SetAttributes(
					attribute.Int("rpc.grpc.status_code", int(st.Code())),
					attribute.String("rpc.grpc.status_message", st.Message()),
				)
			}
		} else {
			span.SetStatus(codes.Ok, "")
			span.SetAttributes(attribute.Int("rpc.grpc.status_code", 0))
		}

		return err
	}
}

// tracedServerStream wraps grpc.ServerStream with tracing context
type tracedServerStream struct {
	grpc.ServerStream
	ctx    context.Context
	tracer *Tracer
}

// Context returns the traced context
func (s *tracedServerStream) Context() context.Context {
	return s.ctx
}

// SendMsg records a span event when sending messages
func (s *tracedServerStream) SendMsg(m interface{}) error {
	s.tracer.AddEvent(s.ctx, "stream.send",
		trace.WithAttributes(
			attribute.String("message.type", fmt.Sprintf("%T", m)),
		),
	)
	//nolint:wrapcheck // Intentionally passing through gRPC stream error
	return s.ServerStream.SendMsg(m)
}

// RecvMsg records a span event when receiving messages
func (s *tracedServerStream) RecvMsg(m interface{}) error {
	err := s.ServerStream.RecvMsg(m)
	if err != nil {
		s.tracer.AddEvent(s.ctx, "stream.receive.error",
			trace.WithAttributes(
				attribute.String("error", err.Error()),
			),
		)
	} else {
		s.tracer.AddEvent(s.ctx, "stream.receive",
			trace.WithAttributes(
				attribute.String("message.type", fmt.Sprintf("%T", m)),
			),
		)
	}
	//nolint:wrapcheck // Intentionally passing through gRPC stream error
	return err
}

// UnaryClientInterceptor returns a gRPC unary client interceptor for tracing
//
// This interceptor automatically:
//   - Creates spans for outgoing RPC calls
//   - Injects trace context into outgoing metadata
//   - Records gRPC method, status code, and errors
//
// Example usage:
//
//	conn, err := grpc.NewClient(
//	    target,
//	    grpc.WithUnaryInterceptor(tracer.UnaryClientInterceptor()),
//	)
func (t *Tracer) UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		// Parse gRPC method for semantic conventions
		service, methodName := parseGRPCMethod(method)

		// Start span for this RPC call
		ctx, span := t.Start(ctx, method,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				semconv.RPCSystemNameKey.String("grpc"),
				attribute.String("rpc.service", service),
				semconv.RPCMethodKey.String(methodName),
			),
		)
		defer span.End()

		// Inject trace context into outgoing metadata
		ctx = injectTraceContext(ctx)

		// Invoke the RPC
		err := invoker(ctx, method, req, reply, cc, opts...)

		// Record error if present
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())

			if st, ok := status.FromError(err); ok {
				span.SetAttributes(
					attribute.Int("rpc.grpc.status_code", int(st.Code())),
					attribute.String("rpc.grpc.status_message", st.Message()),
				)
			}
		} else {
			span.SetStatus(codes.Ok, "")
			span.SetAttributes(attribute.Int("rpc.grpc.status_code", 0))
		}

		return err
	}
}

// StreamClientInterceptor returns a gRPC stream client interceptor for tracing
//
// This interceptor automatically:
//   - Creates spans for outgoing streaming RPC calls
//   - Injects trace context into outgoing metadata
//   - Records stream events
//
// Example usage:
//
//	conn, err := grpc.NewClient(
//	    target,
//	    grpc.WithUnaryInterceptor(tracer.UnaryClientInterceptor()),
//	    grpc.WithStreamInterceptor(tracer.StreamClientInterceptor()),
//	)
func (t *Tracer) StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		// Parse gRPC method for semantic conventions
		service, methodName := parseGRPCMethod(method)

		// Start span for this stream
		ctx, span := t.Start(ctx, method,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				semconv.RPCSystemNameKey.String("grpc"),
				attribute.String("rpc.service", service),
				semconv.RPCMethodKey.String(methodName),
				attribute.Bool("rpc.stream", true),
			),
		)
		// Note: span.End() is called when stream is closed

		// Inject trace context into outgoing metadata
		ctx = injectTraceContext(ctx)

		// Create the stream
		stream, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			span.End()
			return nil, err
		}

		// Wrap client stream to track events
		return &tracedClientStream{
			ClientStream: stream,
			ctx:          ctx,
			span:         span,
			tracer:       t,
		}, nil
	}
}

// tracedClientStream wraps grpc.ClientStream with tracing
type tracedClientStream struct {
	grpc.ClientStream
	ctx     context.Context
	span    trace.Span
	tracer  *Tracer
	endOnce sync.Once // Ensures span is ended exactly once
}

// SendMsg records a span event when sending messages
func (s *tracedClientStream) SendMsg(m interface{}) error {
	s.tracer.AddEvent(s.ctx, "stream.send",
		trace.WithAttributes(
			attribute.String("message.type", fmt.Sprintf("%T", m)),
		),
	)
	//nolint:wrapcheck // Intentionally passing through gRPC stream error
	return s.ClientStream.SendMsg(m)
}

// RecvMsg records a span event when receiving messages
// Ends the span when streaming concludes (io.EOF or any error)
func (s *tracedClientStream) RecvMsg(m interface{}) error {
	err := s.ClientStream.RecvMsg(m)
	if err != nil {
		// Any error means the stream is done - end the span
		if errors.Is(err, io.EOF) {
			// Normal stream completion
			s.tracer.AddEvent(s.ctx, "stream.complete")
			s.span.SetStatus(codes.Ok, "")
		} else {
			// Error during streaming - record event and set error status
			s.tracer.AddEvent(s.ctx, "stream.receive.error",
				trace.WithAttributes(
					attribute.String("error", err.Error()),
				),
			)

			// Set span status to error with appropriate message
			st, ok := status.FromError(err)
			if ok {
				s.span.SetStatus(codes.Error, st.Message())
			} else {
				s.span.SetStatus(codes.Error, err.Error())
			}
			s.span.RecordError(err)
		}

		// Always end span on any error (EOF or otherwise)
		s.endOnce.Do(func() {
			s.span.End()
		})
	} else {
		s.tracer.AddEvent(s.ctx, "stream.receive",
			trace.WithAttributes(
				attribute.String("message.type", fmt.Sprintf("%T", m)),
			),
		)
	}
	//nolint:wrapcheck // Intentionally passing through gRPC stream error
	return err
}

// CloseSend closes the stream and ends the span
func (s *tracedClientStream) CloseSend() error {
	err := s.ClientStream.CloseSend()
	s.endOnce.Do(func() {
		s.span.End()
	})
	//nolint:wrapcheck // Intentionally passing through gRPC stream error
	return err
}

// extractTraceContext extracts trace context from gRPC metadata
func extractTraceContext(ctx context.Context, md metadata.MD) context.Context {
	// Convert metadata to TextMapCarrier
	carrier := metadataCarrier(md)

	return grpcPropagator.Extract(ctx, carrier)
}

// injectTraceContext injects trace context into gRPC metadata
func injectTraceContext(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(nil)
	}

	// Convert metadata to TextMapCarrier
	carrier := metadataCarrier(md)

	// Inject trace context
	grpcPropagator.Inject(ctx, carrier)

	return metadata.NewOutgoingContext(ctx, md)
}

// metadataCarrier adapts metadata.MD to propagation.TextMapCarrier
type metadataCarrier metadata.MD

// Get returns the value associated with the passed key.
func (mc metadataCarrier) Get(key string) string {
	vals := metadata.MD(mc).Get(key)
	if len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// Set stores the key-value pair.
func (mc metadataCarrier) Set(key string, value string) {
	metadata.MD(mc).Set(key, value)
}

// Keys lists the keys stored in this carrier.
func (mc metadataCarrier) Keys() []string {
	return lo.Keys(mc)
}

// addTenantSpanAttributes adds tenant context attributes to a span.
// This enables filtering and grouping traces by tenant in Tempo/Jaeger.
//
// Attributes added:
//   - tenant.id: The tenant identifier (e.g., "acme_bank")
//   - tenant.schema: The database schema name (e.g., "org_acme_bank")
func addTenantSpanAttributes(ctx context.Context, span trace.Span) {
	if ctx == nil || !span.IsRecording() {
		return
	}

	orgID, ok := tenant.FromContext(ctx)
	if !ok || orgID.IsEmpty() {
		return
	}

	span.SetAttributes(
		attribute.String(TenantIDAttrKey, orgID.String()),
		attribute.String(TenantSchemaKey, orgID.SchemaName()),
	)
}
