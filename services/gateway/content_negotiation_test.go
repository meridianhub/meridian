package gateway

// Content Negotiation and Multi-Protocol Integration Tests
//
// Vanguard acts as a protocol-translating gateway: it accepts inbound requests
// in any of the four supported protocols and translates them to the target
// backend protocol (gRPC in our case). Protocol selection is driven entirely
// by the request Content-Type header.
//
// Supported inbound protocols:
//
//   - REST/JSON (HTTP/JSON transcoding):
//       Content-Type: application/json
//       Paths: /v1/parties, /v1/tenants, etc.  (from google.api.http annotations)
//       HTTP verbs: GET, POST, PUT, PATCH, DELETE
//
//   - Connect protocol:
//       Content-Type: application/connect+json  or  application/connect+proto
//       Paths: /meridian.party.v1.PartyService/RegisterParty  (RPC-style)
//       HTTP verb: POST
//
//   - gRPC-Web:
//       Content-Type: application/grpc-web+proto  or  application/grpc-web+json
//       Paths: /meridian.party.v1.PartyService/RetrieveParty  (RPC-style)
//       HTTP verb: POST
//       Body: length-prefixed protobuf frames (5-byte prefix per message)
//
//   - Native gRPC (passthrough):
//       Direct to the gRPC backend port over HTTP/2. The mock backend in tests
//       is reachable at the address configured in ServiceBackend.BackendAddr.
//
// URL construction for Connect/gRPC-Web clients:
//   The gateway mounts the transcoder on /, so clients target the full URL:
//     http://<host>/<package>.<Service>/<Method>
//   The connect-go library uses the last two path segments (/<Service>/<Method>)
//   as the RPC procedure identifier.
//
// Protocol selection guidelines:
//   - REST clients (curl, fetch, browsers): use HTTP/JSON
//   - Browser clients needing RPC semantics: use Connect protocol (HTTP/1.1 compatible)
//   - Legacy clients or proxies needing gRPC-Web: use gRPC-Web
//   - Server-to-server with HTTP/2: native gRPC directly to backend port
//
// All inbound protocols are transcoded by Vanguard before forwarding to the
// gRPC backend. The backend always receives native gRPC, regardless of the
// inbound protocol used by the client.

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
)

// partyRetrieveURL returns the full URL for the PartyService/RetrieveParty RPC.
func partyRetrieveURL(baseURL string) string {
	return baseURL + "/meridian.party.v1.PartyService/RetrieveParty"
}

// partyRegisterURL returns the full URL for the PartyService/RegisterParty RPC.
func partyRegisterURL(baseURL string) string {
	return baseURL + "/meridian.party.v1.PartyService/RegisterParty"
}

// ---------------------------------------------------------------------------
// Protocol 1: HTTP/JSON (REST) – already covered in transcoding_test.go
// ---------------------------------------------------------------------------
// The HTTP/JSON protocol tests live in transcoding_test.go. The tests below
// complement that suite by verifying the three non-REST protocols and the
// content-type routing behavior that selects between them.

// ---------------------------------------------------------------------------
// Protocol 2: Connect protocol
// ---------------------------------------------------------------------------

// TestContentNegotiation_ConnectProto verifies that Vanguard accepts
// Connect-protocol requests using binary protobuf encoding.
//
// Wire format: POST /<package>.<Service>/<Method>
// Content-Type: application/connect+proto
// Body: 5-byte length-prefixed protobuf message
func TestContentNegotiation_ConnectProto(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	// Connect protocol with binary proto encoding (application/connect+proto).
	client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
		&http.Client{},
		partyRetrieveURL(env.baseURL),
	)

	resp, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RetrievePartyRequest{
		PartyId: "connect-proto-test",
	}))
	require.NoError(t, err)

	assert.Equal(t, "connect-proto-test", resp.Msg.GetParty().GetPartyId())
	assert.Equal(t, "Jane Doe", resp.Msg.GetParty().GetLegalName())
}

// TestContentNegotiation_ConnectJSON verifies Connect protocol with JSON encoding.
//
// Wire format: POST /<package>.<Service>/<Method>
// Content-Type: application/connect+json
// Body: JSON-encoded request message
func TestContentNegotiation_ConnectJSON(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	// Connect protocol with JSON encoding (application/connect+json).
	client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
		&http.Client{},
		partyRetrieveURL(env.baseURL),
		connect.WithProtoJSON(),
	)

	resp, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RetrievePartyRequest{
		PartyId: "connect-json-test",
	}))
	require.NoError(t, err)

	assert.Equal(t, "connect-json-test", resp.Msg.GetParty().GetPartyId())
	assert.Equal(t, partyv1.PartyStatus_PARTY_STATUS_ACTIVE, resp.Msg.GetParty().GetStatus())
}

// TestContentNegotiation_ConnectProtocol_RegisterParty verifies that
// POST (mutating) RPCs work correctly over the Connect protocol.
func TestContentNegotiation_ConnectProtocol_RegisterParty(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	client := connect.NewClient[partyv1.RegisterPartyRequest, partyv1.RegisterPartyResponse](
		&http.Client{},
		partyRegisterURL(env.baseURL),
		connect.WithProtoJSON(),
	)

	resp, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RegisterPartyRequest{
		PartyType:   partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName:   "Connect Corp",
		DisplayName: "CC",
	}))
	require.NoError(t, err)

	assert.Equal(t, "party-001", resp.Msg.GetParty().GetPartyId())
	assert.Equal(t, "Connect Corp", resp.Msg.GetParty().GetLegalName())
}

// ---------------------------------------------------------------------------
// Protocol 3: gRPC-Web
// ---------------------------------------------------------------------------

// TestContentNegotiation_GRPCWebProto verifies that Vanguard accepts
// gRPC-Web requests using binary protobuf encoding.
//
// Wire format: POST /<package>.<Service>/<Method>
// Content-Type: application/grpc-web+proto
// Body: 5-byte length-prefixed protobuf frames
func TestContentNegotiation_GRPCWebProto(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	// gRPC-Web with binary proto encoding (application/grpc-web+proto).
	client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
		&http.Client{},
		partyRetrieveURL(env.baseURL),
		connect.WithGRPCWeb(),
	)

	resp, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RetrievePartyRequest{
		PartyId: "grpc-web-proto-test",
	}))
	require.NoError(t, err)

	assert.Equal(t, "grpc-web-proto-test", resp.Msg.GetParty().GetPartyId())
	assert.Equal(t, "Jane Doe", resp.Msg.GetParty().GetLegalName())
}

// TestContentNegotiation_GRPCWebJSON verifies gRPC-Web with JSON encoding.
//
// Wire format: POST /<package>.<Service>/<Method>
// Content-Type: application/grpc-web+json
func TestContentNegotiation_GRPCWebJSON(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	// gRPC-Web with JSON encoding (application/grpc-web+json).
	client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
		&http.Client{},
		partyRetrieveURL(env.baseURL),
		connect.WithGRPCWeb(),
		connect.WithProtoJSON(),
	)

	resp, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RetrievePartyRequest{
		PartyId: "grpc-web-json-test",
	}))
	require.NoError(t, err)

	assert.Equal(t, "grpc-web-json-test", resp.Msg.GetParty().GetPartyId())
}

// TestContentNegotiation_GRPCWebProto_ManualFraming verifies the raw gRPC-Web
// wire protocol by crafting the request manually, without relying on the
// connect-go client library. This documents the exact framing format.
//
// gRPC-Web framing:
//
//	Byte 0:   0x00 (no compression) or 0x01 (compressed)
//	Bytes 1-4: message length as 4-byte big-endian uint32
//	Bytes 5+:  serialized protobuf message
func TestContentNegotiation_GRPCWebProto_ManualFraming(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	// Serialize the request message.
	reqProto := &partyv1.RetrievePartyRequest{PartyId: "manual-grpc-web-test"}
	msgBytes, err := proto.Marshal(reqProto)
	require.NoError(t, err)

	// Frame the message with the gRPC-Web 5-byte prefix.
	var frame bytes.Buffer
	frame.WriteByte(0x00) // compression flag: not compressed
	var lenBytes [4]byte
	binary.BigEndian.PutUint32(lenBytes[:], uint32(len(msgBytes)))
	frame.Write(lenBytes[:])
	frame.Write(msgBytes)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		env.baseURL+"/meridian.party.v1.PartyService/RetrieveParty",
		&frame)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	req.Header.Set("X-Grpc-Web", "1")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// gRPC-Web returns HTTP 200 even for application-level errors;
	// the real status is in the Grpc-Status trailer.
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "grpc-web")
}

// ---------------------------------------------------------------------------
// Protocol 4: Native gRPC passthrough
// ---------------------------------------------------------------------------

// TestContentNegotiation_NativeGRPC_Direct demonstrates native gRPC connecting
// directly to the backend port. This represents the passthrough path for
// service-to-service calls that bypass the gateway entirely.
//
// In production, native gRPC clients connect to the service's gRPC port (:50051)
// directly using HTTP/2, not through the HTTP/1.1 gateway endpoint.
func TestContentNegotiation_NativeGRPC_Direct(t *testing.T) {
	ctx := context.Background()

	// Start a standalone mock gRPC server on a random port.
	grpcListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	grpcSrv := grpc.NewServer()
	mockParty := &mockPartyService{}
	partyv1.RegisterPartyServiceServer(grpcSrv, mockParty)
	go func() { _ = grpcSrv.Serve(grpcListener) }()
	t.Cleanup(grpcSrv.GracefulStop)

	grpcAddr := grpcListener.Addr().String()

	// Native gRPC client connects directly to the backend without going
	// through the HTTP gateway or Vanguard transcoder.
	conn, err := grpc.NewClient(
		grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer conn.Close()

	client := partyv1.NewPartyServiceClient(conn)
	resp, err := client.RetrieveParty(ctx, &partyv1.RetrievePartyRequest{
		PartyId: "native-grpc-direct",
	})
	require.NoError(t, err)

	assert.Equal(t, "native-grpc-direct", resp.GetParty().GetPartyId())
	assert.Equal(t, "Jane Doe", resp.GetParty().GetLegalName())
	assert.Equal(t, partyv1.PartyStatus_PARTY_STATUS_ACTIVE, resp.GetParty().GetStatus())
}

// ---------------------------------------------------------------------------
// Content-Type routing verification
// ---------------------------------------------------------------------------

// TestContentNegotiation_ContentTypeRouting_JSON verifies that requests with
// Content-Type: application/json are handled as REST/JSON (HTTP transcoding).
func TestContentNegotiation_ContentTypeRouting_JSON(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	body := `{"partyType":"PARTY_TYPE_PERSON","legalName":"JSON Client","displayName":"JC"}`
	resp, err := httpPost(context.Background(), env.baseURL+"/v1/parties",
		"application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	ct := resp.Header.Get("Content-Type")
	assert.Contains(t, ct, "application/json",
		"REST/JSON responses should have Content-Type: application/json")

	result := readJSONBody(t, resp)
	party, ok := result["party"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "JSON Client", party["legalName"])
}

// TestContentNegotiation_ContentTypeRouting_ConnectJSON verifies that
// application/connect+json routes to the Connect protocol handler.
func TestContentNegotiation_ContentTypeRouting_ConnectJSON(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
		&http.Client{},
		partyRetrieveURL(env.baseURL),
		connect.WithProtoJSON(),
	)

	req := connect.NewRequest(&partyv1.RetrievePartyRequest{PartyId: "routing-test"})
	resp, err := client.CallUnary(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "routing-test", resp.Msg.GetParty().GetPartyId())
}

// TestContentNegotiation_ContentTypeRouting_GRPCWeb verifies that
// application/grpc-web+proto routes to the gRPC-Web protocol handler.
func TestContentNegotiation_ContentTypeRouting_GRPCWeb(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
		&http.Client{},
		partyRetrieveURL(env.baseURL),
		connect.WithGRPCWeb(),
	)

	req := connect.NewRequest(&partyv1.RetrievePartyRequest{PartyId: "grpc-web-routing-test"})
	resp, err := client.CallUnary(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "grpc-web-routing-test", resp.Msg.GetParty().GetPartyId())
}

// TestContentNegotiation_RPCPathVsRESTPath verifies that the same backend
// service is reachable via both RPC-style paths (Connect/gRPC-Web) and
// REST-style paths (HTTP/JSON transcoding).
func TestContentNegotiation_RPCPathVsRESTPath(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	t.Run("REST path: GET /v1/parties/{id}", func(t *testing.T) {
		resp, err := httpGet(ctx, env.baseURL+"/v1/parties/rest-path-test")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		result := readJSONBody(t, resp)
		party, ok := result["party"].(map[string]interface{})
		require.True(t, ok, "expected 'party' object in response, got: %v", result)
		assert.Equal(t, "rest-path-test", party["partyId"])
	})

	t.Run("Connect RPC path: POST /<svc>/RetrieveParty", func(t *testing.T) {
		client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
			&http.Client{},
			partyRetrieveURL(env.baseURL),
			connect.WithProtoJSON(),
		)
		resp, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RetrievePartyRequest{
			PartyId: "rpc-path-test",
		}))
		require.NoError(t, err)
		assert.Equal(t, "rpc-path-test", resp.Msg.GetParty().GetPartyId())
	})

	t.Run("gRPC-Web RPC path: POST /<svc>/RetrieveParty", func(t *testing.T) {
		client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
			&http.Client{},
			partyRetrieveURL(env.baseURL),
			connect.WithGRPCWeb(),
		)
		resp, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RetrievePartyRequest{
			PartyId: "grpc-web-rpc-path-test",
		}))
		require.NoError(t, err)
		assert.Equal(t, "grpc-web-rpc-path-test", resp.Msg.GetParty().GetPartyId())
	})
}

// ---------------------------------------------------------------------------
// Error handling across protocols
// ---------------------------------------------------------------------------

// TestContentNegotiation_ConnectProtocol_NotFound verifies that gRPC NOT_FOUND
// errors are correctly mapped for Connect protocol clients.
func TestContentNegotiation_ConnectProtocol_NotFound(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
		&http.Client{},
		partyRetrieveURL(env.baseURL),
		connect.WithProtoJSON(),
	)

	_, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RetrievePartyRequest{
		PartyId: "not-found",
	}))
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeNotFound, connectErr.Code())
}

// TestContentNegotiation_GRPCWeb_NotFound verifies that gRPC NOT_FOUND errors
// are correctly mapped for gRPC-Web clients.
func TestContentNegotiation_GRPCWeb_NotFound(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
		&http.Client{},
		partyRetrieveURL(env.baseURL),
		connect.WithGRPCWeb(),
	)

	_, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RetrievePartyRequest{
		PartyId: "not-found",
	}))
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeNotFound, connectErr.Code())
}

// TestContentNegotiation_GRPCWeb_InvalidArgument verifies INVALID_ARGUMENT
// error mapping for gRPC-Web clients.
func TestContentNegotiation_GRPCWeb_InvalidArgument(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
		&http.Client{},
		partyRetrieveURL(env.baseURL),
		connect.WithGRPCWeb(),
	)

	_, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RetrievePartyRequest{
		PartyId: "invalid",
	}))
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
}

// ---------------------------------------------------------------------------
// gRPC-Web content-type inspection
// ---------------------------------------------------------------------------

// TestContentNegotiation_GRPCWebContentType_Response verifies that gRPC-Web
// responses carry the correct Content-Type header, confirming Vanguard is
// producing protocol-conformant responses.
func TestContentNegotiation_GRPCWebContentType_Response(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
	})

	ctx := context.Background()

	// Encode a valid request in gRPC-Web wire format.
	reqMsg := &partyv1.RetrievePartyRequest{PartyId: "ct-inspect-test"}
	msgBytes, err := proto.Marshal(reqMsg)
	require.NoError(t, err)

	var body bytes.Buffer
	body.WriteByte(0x00) // no compression
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(msgBytes)))
	body.Write(lenBuf[:])
	body.Write(msgBytes)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		env.baseURL+"/meridian.party.v1.PartyService/RetrieveParty",
		&body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/grpc-web+proto")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// gRPC-Web responses always return HTTP 200; status is in trailers.
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	ct := resp.Header.Get("Content-Type")
	assert.Contains(t, ct, "grpc-web",
		"gRPC-Web responses must have grpc-web Content-Type, got: %s", ct)
}

// ---------------------------------------------------------------------------
// Multi-service content negotiation
// ---------------------------------------------------------------------------

// TestContentNegotiation_MultipleServices_AllProtocols verifies that when
// multiple services are registered, each can be reached using any protocol.
func TestContentNegotiation_MultipleServices_AllProtocols(t *testing.T) {
	env := startTranscodingTestEnv(t, []ServiceBackend{
		{ServiceName: "meridian.party.v1.PartyService"},
		{ServiceName: "meridian.tenant.v1.TenantService"},
	})

	ctx := context.Background()

	t.Run("party via REST/JSON", func(t *testing.T) {
		resp, err := httpGet(ctx, env.baseURL+"/v1/parties/multi-proto-test")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("tenant via REST/JSON", func(t *testing.T) {
		resp, err := httpGet(ctx, env.baseURL+"/v1/tenants/multi-proto-tenant")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("party via Connect protocol", func(t *testing.T) {
		client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
			&http.Client{},
			partyRetrieveURL(env.baseURL),
			connect.WithProtoJSON(),
		)
		resp, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RetrievePartyRequest{
			PartyId: "multi-svc-connect-test",
		}))
		require.NoError(t, err)
		assert.Equal(t, "multi-svc-connect-test", resp.Msg.GetParty().GetPartyId())
	})

	t.Run("party via gRPC-Web", func(t *testing.T) {
		client := connect.NewClient[partyv1.RetrievePartyRequest, partyv1.RetrievePartyResponse](
			&http.Client{},
			partyRetrieveURL(env.baseURL),
			connect.WithGRPCWeb(),
		)
		resp, err := client.CallUnary(ctx, connect.NewRequest(&partyv1.RetrievePartyRequest{
			PartyId: "multi-svc-grpc-web-test",
		}))
		require.NoError(t, err)
		assert.Equal(t, "multi-svc-grpc-web-test", resp.Msg.GetParty().GetPartyId())
	})
}
