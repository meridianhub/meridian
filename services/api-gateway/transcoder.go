package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"connectrpc.com/vanguard"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	descriptorpb "google.golang.org/protobuf/types/descriptorpb"
)

// Sentinel errors for NewTranscoder validation.
var (
	// ErrNoBackends is returned when NewTranscoder is called with an empty backends slice.
	ErrNoBackends = errors.New("at least one ServiceBackend must be provided")
	// ErrNotServiceDescriptor is returned when a descriptor name resolves to a non-service type.
	ErrNotServiceDescriptor = errors.New("descriptor is not a service")
)

// ServiceBackend maps a fully-qualified gRPC service name to the host:port of
// its backend. The service name must exactly match the service's full name in
// the proto descriptor (e.g. "meridian.party.v1.PartyService").
type ServiceBackend struct {
	// ServiceName is the fully-qualified proto service name.
	ServiceName string
	// BackendAddr is the host:port of the gRPC backend for this service.
	BackendAddr string
}

// NewTranscoder creates an HTTP handler that transcodes between REST/JSON, Connect,
// gRPC-Web, and gRPC protocols. It parses the given compiled FileDescriptorSet bytes
// to discover service schemas and their HTTP transcoding annotations, then routes
// each service's requests to the corresponding backend gRPC address.
//
// The descriptorBytes parameter must be a serialized descriptorpb.FileDescriptorSet
// (as produced by `buf build -o descriptor.binpb`).
//
// Only services listed in backends are registered with the transcoder; any service
// found in the descriptor that has no matching entry in backends is silently skipped.
// This allows callers to control which services are exposed and avoids HTTP-route
// conflicts between services that share REST path patterns.
//
// The returned handler accepts requests in any supported protocol (REST+JSON,
// Connect, gRPC-Web, gRPC) and forwards them to the configured backends over gRPC.
func NewTranscoder(descriptorBytes []byte, backends []ServiceBackend) (http.Handler, error) {
	if len(backends) == 0 {
		return nil, ErrNoBackends
	}

	// 1. Parse the compiled FileDescriptorSet.
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(descriptorBytes, &fds); err != nil {
		return nil, fmt.Errorf("unmarshal FileDescriptorSet: %w", err)
	}

	// 2. Build a protodesc.Files registry from the descriptor set so we can
	//    resolve service descriptors by fully-qualified name, independent of
	//    which packages happen to be imported in this binary.
	files, err := protodesc.NewFiles(&fds)
	if err != nil {
		return nil, fmt.Errorf("build file registry from descriptor set: %w", err)
	}

	// 3. Build a Vanguard service for each requested backend.
	services := make([]*vanguard.Service, 0, len(backends))
	for _, b := range backends {
		desc, err := files.FindDescriptorByName(protoreflect.FullName(b.ServiceName))
		if err != nil {
			return nil, fmt.Errorf("service %q not found in descriptor set: %w", b.ServiceName, err)
		}
		svcDesc, ok := desc.(protoreflect.ServiceDescriptor)
		if !ok {
			return nil, fmt.Errorf("%q: %w", b.ServiceName, ErrNotServiceDescriptor)
		}

		backendProxy := newGRPCReverseProxy(b.BackendAddr)
		services = append(services, vanguard.NewServiceWithSchema(
			svcDesc,
			backendProxy,
			// The backend speaks gRPC only; vanguard transcodes all other protocols.
			vanguard.WithTargetProtocols(vanguard.ProtocolGRPC),
			// The backend speaks proto only; vanguard transcodes JSON to proto when needed.
			vanguard.WithTargetCodecs(vanguard.CodecProto),
		))
	}

	// 4. Create the Vanguard transcoder with all registered services.
	transcoder, err := vanguard.NewTranscoder(services)
	if err != nil {
		return nil, fmt.Errorf("create vanguard transcoder: %w", err)
	}

	// 5. Wrap with error reformatting middleware to produce a consistent JSON
	//    error body format across all gRPC error codes.
	return errorReformattingMiddleware(transcoder), nil
}

// newGRPCReverseProxy builds an httputil.ReverseProxy that forwards requests to addr
// using cleartext HTTP/2 (h2c). gRPC backends require HTTP/2, so the proxy is
// configured with an http2.Transport that has AllowHTTP enabled and TLS disabled.
func newGRPCReverseProxy(addr string) http.Handler {
	target := &url.URL{
		Scheme: "http",
		Host:   addr,
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			d := net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
			return d.DialContext(ctx, network, addr)
		},
	}
	return proxy
}
