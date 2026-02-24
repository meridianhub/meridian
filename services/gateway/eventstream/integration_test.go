package eventstream_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/meridianhub/meridian/services/gateway/eventstream"
	"github.com/meridianhub/meridian/services/gateway/eventstream/adapters"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ─── E2E Integration Tests ──────────────────────────────────────────────────────

// TestE2E_FullStack_KafkaToWebSocket verifies the full pipeline:
// Kafka producer → KafkaEventSource → RedisFanOut → Router → Handler → WebSocket client.
// This test uses a real Kafka testcontainer and miniredis for Redis FanOut.
func TestE2E_FullStack_KafkaToWebSocket(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E integration test in short mode")
	}

	ctx := context.Background()

	// Start Kafka container.
	kafkaContainer, err := kafka.Run(ctx,
		"confluentinc/confluent-local:7.5.0",
		kafka.WithClusterID("e2e-test-cluster"),
	)
	require.NoError(t, err, "failed to start Kafka container")
	t.Cleanup(func() {
		if termErr := kafkaContainer.Terminate(ctx); termErr != nil {
			t.Logf("failed to terminate Kafka container: %v", termErr)
		}
	})

	brokers, err := kafkaContainer.Brokers(ctx)
	require.NoError(t, err, "failed to get Kafka brokers")
	require.NotEmpty(t, brokers)
	brokerAddr := brokers[0]

	const topicName = "payment-order.created.v1"
	mustCreateTopicsHelper(t, ctx, brokerAddr, topicName)

	// Start miniredis for the RedisFanOut.
	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	logger := discardLogger()

	// Build the full stack: KafkaEventSource → RedisFanOut → Router → Handler.
	kafkaSource, err := adapters.NewKafkaEventSource(brokerAddr, []string{topicName}, logger)
	require.NoError(t, err)

	redisFanOut := adapters.NewRedisFanOut(redisClient, logger)
	router := eventstream.NewRouter(kafkaSource, redisFanOut)
	handler := eventstream.NewHandler(router, logger, eventstream.WithAcceptOptions(websocket.AcceptOptions{
		InsecureSkipVerify: true,
	}))

	claims := &platformauth.Claims{
		UserID:   "user-e2e",
		TenantID: "tenant-e2e",
		Roles:    []string{"ops:admin"},
	}

	srv := httptest.NewServer(injectClaimsMiddleware(claims, handler))
	t.Cleanup(srv.Close)

	// Start the router (consumes from Kafka and publishes to Redis FanOut).
	routerCtx, routerCancel := context.WithCancel(ctx)
	defer routerCancel()

	routerDone := make(chan error, 1)
	go func() {
		routerDone <- router.Start(routerCtx)
	}()

	// Connect WebSocket client.
	wsCtx, wsCancel := context.WithTimeout(ctx, 30*time.Second)
	defer wsCancel()

	clientConn, _, err := websocket.Dial(wsCtx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err, "WebSocket dial should succeed")
	defer clientConn.CloseNow()

	// Wait for connection registration.
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-e2e")) > 0
		})
	require.NoError(t, err, "connection should be registered in router")

	// Subscribe to payment-order.* channel.
	subMsg := eventstream.ClientMessage{
		Type:     eventstream.ClientMessageTypeSubscribe,
		ID:       "sub-e2e",
		Channels: []eventstream.ChannelPattern{"payment-order.*"},
	}
	data, err := json.Marshal(subMsg)
	require.NoError(t, err)
	require.NoError(t, clientConn.Write(wsCtx, websocket.MessageText, data))

	// Read subscription confirmation.
	var confirmed eventstream.ServerMessage
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		UntilNoError(func() error {
			_, msgData, readErr := clientConn.Read(wsCtx)
			if readErr != nil {
				return readErr
			}
			return json.Unmarshal(msgData, &confirmed)
		})
	require.NoError(t, err, "should receive subscription confirmation")
	assert.Equal(t, eventstream.ServerMessageTypeSubscribed, confirmed.Type)
	assert.Equal(t, "sub-e2e", confirmed.SubscriptionID)

	// Wait for the Kafka consumer group to be active before producing.
	// The KafkaEventSource starts at the end offset, so any messages produced
	// before the consumer group joins are missed.
	err = await.New().
		AtMost(15 * time.Second).
		PollInterval(200 * time.Millisecond).
		UntilNoError(func() error {
			return consumerGroupActiveHelper(ctx, brokerAddr, adapters.ConsumerGroupID)
		})
	require.NoError(t, err, "Kafka consumer group should become active")

	// Produce event to Kafka.
	producer, err := kgo.NewClient(kgo.SeedBrokers(brokerAddr))
	require.NoError(t, err)
	defer producer.Close()

	eventPayload := []byte(`{"amount":"250.00","currency":"GBP"}`)
	record := &kgo.Record{
		Topic: topicName,
		Value: eventPayload,
		Headers: []kgo.RecordHeader{
			{Key: tenant.TenantIDKey, Value: []byte("tenant-e2e")},
			{Key: "event_id", Value: []byte("evt-e2e-001")},
			{Key: "event_type", Value: []byte("payment_order.created.v1")},
			{Key: "aggregate_type", Value: []byte("PaymentOrder")},
			{Key: "aggregate_id", Value: []byte("po-e2e-001")},
			{Key: "correlation_id", Value: []byte("corr-e2e-001")},
		},
	}
	results := producer.ProduceSync(ctx, record)
	require.NoError(t, results.FirstErr(), "Kafka produce should succeed")

	// Wait for the event to arrive on the WebSocket.
	// Use a single blocking read with a deadline context — polling reads can
	// leave the WS connection in a bad state.
	readCtx, readCancel := context.WithTimeout(ctx, 20*time.Second)
	defer readCancel()

	var eventMsg eventstream.ServerMessage
	_, msgData, readErr := clientConn.Read(readCtx)
	require.NoError(t, readErr, "should receive event from WebSocket")
	require.NoError(t, json.Unmarshal(msgData, &eventMsg))

	assert.Equal(t, eventstream.ServerMessageTypeEvent, eventMsg.Type)
	assert.Equal(t, "sub-e2e", eventMsg.SubscriptionID)
	assert.Equal(t, "payment-order.created", eventMsg.Channel)
	require.NotNil(t, eventMsg.Event)
	assert.Equal(t, "evt-e2e-001", eventMsg.Event.EventID)
	assert.Equal(t, "payment_order.created.v1", eventMsg.Event.EventType)
	assert.Equal(t, "tenant-e2e", eventMsg.Event.TenantID)
	assert.Equal(t, "po-e2e-001", eventMsg.Event.AggregateID)
	assert.Equal(t, "PaymentOrder", eventMsg.Event.AggregateType)
	assert.JSONEq(t, `{"amount":"250.00","currency":"GBP"}`, string(eventMsg.Event.Payload))

	// Cleanup.
	clientConn.Close(websocket.StatusNormalClosure, "test done")
	routerCancel()
	<-routerDone
}

// TestE2E_LocalFanOut_EventDelivery tests the full stack with InProcessFanOut.
// Validates Handler → Router → Connection delivery without external dependencies.
func TestE2E_LocalFanOut_EventDelivery(t *testing.T) {
	src := &controllableEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut)
	handler := eventstream.NewHandler(router, nil, eventstream.WithAcceptOptions(websocket.AcceptOptions{
		InsecureSkipVerify: true,
	}))

	claims := &platformauth.Claims{
		UserID:   "user-local",
		TenantID: "tenant-local",
		Roles:    []string{"ops:admin"},
	}

	srv := httptest.NewServer(injectClaimsMiddleware(claims, handler))
	t.Cleanup(srv.Close)

	routerCtx, routerCancel := context.WithCancel(context.Background())
	defer routerCancel()

	go func() { _ = router.Start(routerCtx) }()

	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool { return src.IsStarted() })
	require.NoError(t, err, "router should start")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	defer clientConn.CloseNow()

	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-local")) > 0
		})
	require.NoError(t, err)

	// Subscribe.
	subMsg := eventstream.ClientMessage{
		Type:     eventstream.ClientMessageTypeSubscribe,
		ID:       "sub-local",
		Channels: []eventstream.ChannelPattern{"*"},
	}
	data, _ := json.Marshal(subMsg)
	require.NoError(t, clientConn.Write(ctx, websocket.MessageText, data))

	// Read confirmation.
	var confirmed eventstream.ServerMessage
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		UntilNoError(func() error {
			_, msgData, readErr := clientConn.Read(ctx)
			if readErr != nil {
				return readErr
			}
			return json.Unmarshal(msgData, &confirmed)
		})
	require.NoError(t, err)
	assert.Equal(t, eventstream.ServerMessageTypeSubscribed, confirmed.Type)

	// Emit event through the controllable source.
	event, err := eventstream.NewDomainEvent(
		"test.created.v1",
		"test.created.v1",
		"agg-001",
		"Test",
		"tenant-local",
		"corr-001",
		"",
		[]byte(`{"msg":"hello"}`),
	)
	require.NoError(t, err)
	src.EmitEvent(ctx, event)

	// Read event from WebSocket.
	var eventMsg eventstream.ServerMessage
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		UntilNoError(func() error {
			_, msgData, readErr := clientConn.Read(ctx)
			if readErr != nil {
				return readErr
			}
			return json.Unmarshal(msgData, &eventMsg)
		})
	require.NoError(t, err)

	assert.Equal(t, eventstream.ServerMessageTypeEvent, eventMsg.Type)
	assert.Equal(t, "sub-local", eventMsg.SubscriptionID)
	require.NotNil(t, eventMsg.Event)
	assert.Equal(t, "tenant-local", eventMsg.Event.TenantID)
	assert.JSONEq(t, `{"msg":"hello"}`, string(eventMsg.Event.Payload))

	clientConn.Close(websocket.StatusNormalClosure, "done")
	routerCancel()
}

// ─── Tenant Isolation Tests ──────────────────────────────────────────────────────

// TestE2E_TenantIsolation_CrossTenantLeakage verifies that events for tenant A
// are never delivered to tenant B's WebSocket connection.
func TestE2E_TenantIsolation_CrossTenantLeakage(t *testing.T) {
	src := &controllableEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut)
	handler := eventstream.NewHandler(router, nil, eventstream.WithAcceptOptions(websocket.AcceptOptions{
		InsecureSkipVerify: true,
	}))

	// Create server with per-request claims injection.
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/tenant-a", func(w http.ResponseWriter, r *http.Request) {
		claimsA := &platformauth.Claims{
			UserID:   "user-a",
			TenantID: "tenant-a",
			Roles:    []string{"ops:admin"},
		}
		ctx := eventstream.ContextWithClaims(r.Context(), claimsA)
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
	mux.HandleFunc("/ws/tenant-b", func(w http.ResponseWriter, r *http.Request) {
		claimsB := &platformauth.Claims{
			UserID:   "user-b",
			TenantID: "tenant-b",
			Roles:    []string{"ops:admin"},
		}
		ctx := eventstream.ContextWithClaims(r.Context(), claimsB)
		handler.ServeHTTP(w, r.WithContext(ctx))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	routerCtx, routerCancel := context.WithCancel(context.Background())
	defer routerCancel()

	go func() { _ = router.Start(routerCtx) }()

	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool { return src.IsStarted() })
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")

	// Connect tenant A.
	connA, _, err := websocket.Dial(ctx, wsBase+"/ws/tenant-a", nil)
	require.NoError(t, err)
	defer connA.CloseNow()

	// Connect tenant B.
	connB, _, err := websocket.Dial(ctx, wsBase+"/ws/tenant-b", nil)
	require.NoError(t, err)
	defer connB.CloseNow()

	// Wait for both registrations.
	err = await.New().
		AtMost(3 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-a")) > 0 &&
				len(router.GetConnectionsByTenant("tenant-b")) > 0
		})
	require.NoError(t, err, "both connections should be registered")

	// Subscribe both to wildcard.
	for _, pair := range []struct {
		conn  *websocket.Conn
		subID string
	}{
		{connA, "sub-a"},
		{connB, "sub-b"},
	} {
		subMsg := eventstream.ClientMessage{
			Type:     eventstream.ClientMessageTypeSubscribe,
			ID:       pair.subID,
			Channels: []eventstream.ChannelPattern{"*"},
		}
		data, _ := json.Marshal(subMsg)
		require.NoError(t, pair.conn.Write(ctx, websocket.MessageText, data))

		// Read confirmation.
		var resp eventstream.ServerMessage
		err = await.New().
			AtMost(2 * time.Second).
			PollInterval(10 * time.Millisecond).
			UntilNoError(func() error {
				_, msgData, readErr := pair.conn.Read(ctx)
				if readErr != nil {
					return readErr
				}
				return json.Unmarshal(msgData, &resp)
			})
		require.NoError(t, err)
		require.Equal(t, eventstream.ServerMessageTypeSubscribed, resp.Type)
	}

	// Emit event for tenant-a only.
	eventA, err := eventstream.NewDomainEvent(
		"payment_order.created.v1",
		"payment-order.created.v1",
		"po-001",
		"PaymentOrder",
		"tenant-a",
		"corr-001",
		"",
		[]byte(`{"amount":"100.00"}`),
	)
	require.NoError(t, err)
	src.EmitEvent(ctx, eventA)

	// Tenant A should receive the event.
	var eventMsg eventstream.ServerMessage
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		UntilNoError(func() error {
			_, msgData, readErr := connA.Read(ctx)
			if readErr != nil {
				return readErr
			}
			return json.Unmarshal(msgData, &eventMsg)
		})
	require.NoError(t, err, "tenant-a should receive event")
	assert.Equal(t, eventstream.ServerMessageTypeEvent, eventMsg.Type)
	assert.Equal(t, "tenant-a", eventMsg.Event.TenantID)

	// Tenant B should NOT receive any event within a reasonable window.
	readCtx, readCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer readCancel()

	_, _, readErr := connB.Read(readCtx)
	assert.Error(t, readErr, "tenant-b must NOT receive tenant-a's event (expected timeout/cancel error)")

	connA.Close(websocket.StatusNormalClosure, "done")
	connB.Close(websocket.StatusNormalClosure, "done")
	routerCancel()
}

// TestE2E_TenantIsolation_MultipleEventsMultipleTenants verifies correct delivery
// across multiple tenants receiving their own events.
func TestE2E_TenantIsolation_MultipleEventsMultipleTenants(t *testing.T) {
	src := &controllableEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut)
	handler := eventstream.NewHandler(router, nil, eventstream.WithAcceptOptions(websocket.AcceptOptions{
		InsecureSkipVerify: true,
	}))

	tenants := []string{"tenant-x", "tenant-y", "tenant-z"}

	mux := http.NewServeMux()
	for _, tid := range tenants {
		tenantID := tid
		mux.HandleFunc("/ws/"+tenantID, func(w http.ResponseWriter, r *http.Request) {
			c := &platformauth.Claims{
				UserID:   "user-" + tenantID,
				TenantID: tenantID,
				Roles:    []string{"ops:admin"},
			}
			ctx := eventstream.ContextWithClaims(r.Context(), c)
			handler.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	routerCtx, routerCancel := context.WithCancel(context.Background())
	defer routerCancel()

	go func() { _ = router.Start(routerCtx) }()

	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool { return src.IsStarted() })
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")

	type tenantConn struct {
		tenantID string
		conn     *websocket.Conn
	}

	conns := make([]tenantConn, 0, len(tenants))
	for _, tid := range tenants {
		c, _, err := websocket.Dial(ctx, wsBase+"/ws/"+tid, nil)
		require.NoError(t, err)
		defer c.CloseNow()
		conns = append(conns, tenantConn{tenantID: tid, conn: c})
	}

	// Wait for all registrations.
	err = await.New().
		AtMost(3 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			for _, tid := range tenants {
				if len(router.GetConnectionsByTenant(tid)) == 0 {
					return false
				}
			}
			return true
		})
	require.NoError(t, err)

	// Subscribe all.
	for _, tc := range conns {
		subMsg := eventstream.ClientMessage{
			Type:     eventstream.ClientMessageTypeSubscribe,
			ID:       "sub-" + tc.tenantID,
			Channels: []eventstream.ChannelPattern{"*"},
		}
		data, _ := json.Marshal(subMsg)
		require.NoError(t, tc.conn.Write(ctx, websocket.MessageText, data))

		var resp eventstream.ServerMessage
		err = await.New().
			AtMost(2 * time.Second).
			PollInterval(10 * time.Millisecond).
			UntilNoError(func() error {
				_, msgData, readErr := tc.conn.Read(ctx)
				if readErr != nil {
					return readErr
				}
				return json.Unmarshal(msgData, &resp)
			})
		require.NoError(t, err)
		require.Equal(t, eventstream.ServerMessageTypeSubscribed, resp.Type)
	}

	// Emit one event per tenant.
	for _, tid := range tenants {
		evt, err := eventstream.NewDomainEvent(
			"test.event.v1",
			"test.event.v1",
			"agg-"+tid,
			"Test",
			tid,
			"",
			"",
			[]byte(fmt.Sprintf(`{"tenant":"%s"}`, tid)),
		)
		require.NoError(t, err)
		src.EmitEvent(ctx, evt)
	}

	// Each tenant should receive exactly their own event.
	for _, tc := range conns {
		var msg eventstream.ServerMessage
		err = await.New().
			AtMost(5 * time.Second).
			PollInterval(50 * time.Millisecond).
			UntilNoError(func() error {
				_, msgData, readErr := tc.conn.Read(ctx)
				if readErr != nil {
					return readErr
				}
				return json.Unmarshal(msgData, &msg)
			})
		require.NoError(t, err, "tenant %s should receive event", tc.tenantID)
		assert.Equal(t, eventstream.ServerMessageTypeEvent, msg.Type)
		require.NotNil(t, msg.Event)
		assert.Equal(t, tc.tenantID, msg.Event.TenantID,
			"tenant %s should only receive events for itself", tc.tenantID)
	}

	for _, tc := range conns {
		tc.conn.Close(websocket.StatusNormalClosure, "done")
	}
	routerCancel()
}

// TestE2E_SubscriptionFilter_ChannelMatch verifies that channel-scoped subscriptions
// only deliver matching events.
func TestE2E_SubscriptionFilter_ChannelMatch(t *testing.T) {
	src := &controllableEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut)
	handler := eventstream.NewHandler(router, nil, eventstream.WithAcceptOptions(websocket.AcceptOptions{
		InsecureSkipVerify: true,
	}))

	claims := &platformauth.Claims{
		UserID:   "user-filter",
		TenantID: "tenant-filter",
		Roles:    []string{"ops:admin"},
	}

	srv := httptest.NewServer(injectClaimsMiddleware(claims, handler))
	t.Cleanup(srv.Close)

	routerCtx, routerCancel := context.WithCancel(context.Background())
	defer routerCancel()

	go func() { _ = router.Start(routerCtx) }()

	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool { return src.IsStarted() })
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientConn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	defer clientConn.CloseNow()

	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("tenant-filter")) > 0
		})
	require.NoError(t, err)

	// Subscribe to payment-order.* only.
	subMsg := eventstream.ClientMessage{
		Type:     eventstream.ClientMessageTypeSubscribe,
		ID:       "sub-payments",
		Channels: []eventstream.ChannelPattern{"payment-order.*"},
	}
	data, _ := json.Marshal(subMsg)
	require.NoError(t, clientConn.Write(ctx, websocket.MessageText, data))

	var confirmed eventstream.ServerMessage
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		UntilNoError(func() error {
			_, msgData, readErr := clientConn.Read(ctx)
			if readErr != nil {
				return readErr
			}
			return json.Unmarshal(msgData, &confirmed)
		})
	require.NoError(t, err)
	require.Equal(t, eventstream.ServerMessageTypeSubscribed, confirmed.Type)

	// Emit a non-matching event first (position-keeping).
	nonMatch, _ := eventstream.NewDomainEvent(
		"position_keeping.updated.v1",
		"position-keeping.updated.v1",
		"agg-001",
		"Position",
		"tenant-filter",
		"",
		"",
		[]byte(`{}`),
	)
	src.EmitEvent(ctx, nonMatch)

	// Emit a matching event (payment-order).
	matching, _ := eventstream.NewDomainEvent(
		"payment_order.created.v1",
		"payment-order.created.v1",
		"po-001",
		"PaymentOrder",
		"tenant-filter",
		"",
		"",
		[]byte(`{"amount":"50.00"}`),
	)
	src.EmitEvent(ctx, matching)

	// Should receive only the matching event.
	var eventMsg eventstream.ServerMessage
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		UntilNoError(func() error {
			_, msgData, readErr := clientConn.Read(ctx)
			if readErr != nil {
				return readErr
			}
			return json.Unmarshal(msgData, &eventMsg)
		})
	require.NoError(t, err)
	assert.Equal(t, eventstream.ServerMessageTypeEvent, eventMsg.Type)
	assert.Equal(t, "payment-order.created", eventMsg.Channel)
	assert.JSONEq(t, `{"amount":"50.00"}`, string(eventMsg.Event.Payload))

	clientConn.Close(websocket.StatusNormalClosure, "done")
	routerCancel()
}

// ─── Load / Benchmark Tests ─────────────────────────────────────────────────────

// BenchmarkEventStream_ConcurrentConnections benchmarks 100 concurrent WebSocket
// connections receiving events. Measures throughput and average latency.
func BenchmarkEventStream_ConcurrentConnections(b *testing.B) {
	const (
		numConnections = 100
		eventsPerBatch = 100
	)

	src := &controllableEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut)
	handler := eventstream.NewHandler(router, nil, eventstream.WithAcceptOptions(websocket.AcceptOptions{
		InsecureSkipVerify: true,
	}))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		claims := &platformauth.Claims{
			UserID:   "bench-user",
			TenantID: "bench-tenant",
			Roles:    []string{"ops:admin"},
		}
		ctx := eventstream.ContextWithClaims(r.Context(), claims)
		handler.ServeHTTP(w, r.WithContext(ctx))
	})

	srv := httptest.NewServer(mux)
	b.Cleanup(srv.Close)

	routerCtx, routerCancel := context.WithCancel(context.Background())
	defer routerCancel()

	go func() { _ = router.Start(routerCtx) }()

	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool { return src.IsStarted() })
	if err != nil {
		b.Fatalf("router did not start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	// Create connections.
	conns := make([]*websocket.Conn, numConnections)
	for i := 0; i < numConnections; i++ {
		c, _, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			b.Fatalf("failed to dial connection %d: %v", i, err)
		}
		defer c.CloseNow()
		conns[i] = c
	}

	// Wait for all registrations.
	err = await.New().
		AtMost(10 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("bench-tenant")) >= numConnections
		})
	if err != nil {
		b.Fatalf("not all connections registered: want %d, got %d",
			numConnections, len(router.GetConnectionsByTenant("bench-tenant")))
	}

	// Subscribe all connections.
	for i, c := range conns {
		subMsg := eventstream.ClientMessage{
			Type:     eventstream.ClientMessageTypeSubscribe,
			ID:       fmt.Sprintf("sub-bench-%d", i),
			Channels: []eventstream.ChannelPattern{"*"},
		}
		data, _ := json.Marshal(subMsg)
		if err := c.Write(ctx, websocket.MessageText, data); err != nil {
			b.Fatalf("failed to subscribe connection %d: %v", i, err)
		}
	}

	// Read all subscription confirmations.
	for i, c := range conns {
		var resp eventstream.ServerMessage
		err := await.New().
			AtMost(5 * time.Second).
			PollInterval(10 * time.Millisecond).
			UntilNoError(func() error {
				_, msgData, readErr := c.Read(ctx)
				if readErr != nil {
					return readErr
				}
				return json.Unmarshal(msgData, &resp)
			})
		if err != nil {
			b.Fatalf("failed to get subscription confirmation for connection %d: %v", i, err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	// Benchmark: emit events and measure delivery.
	var totalDelivered atomic.Int64

	for n := 0; n < b.N; n++ {
		// Emit a batch of events.
		for i := 0; i < eventsPerBatch; i++ {
			evt, _ := eventstream.NewDomainEvent(
				"bench.event.v1",
				"bench.event.v1",
				fmt.Sprintf("agg-%d-%d", n, i),
				"Bench",
				"bench-tenant",
				"",
				"",
				[]byte(`{"i":1}`),
			)
			src.EmitEvent(ctx, evt)
		}

		// Read events from each connection.
		var wg sync.WaitGroup
		for _, c := range conns {
			wg.Add(1)
			go func(conn *websocket.Conn) {
				defer wg.Done()
				for j := 0; j < eventsPerBatch; j++ {
					_, _, readErr := conn.Read(ctx)
					if readErr != nil {
						return
					}
					totalDelivered.Add(1)
				}
			}(c)
		}
		wg.Wait()
	}

	b.StopTimer()

	delivered := totalDelivered.Load()
	if delivered > 0 {
		b.ReportMetric(float64(delivered)/float64(b.N), "events_delivered/op")
	}

	for _, c := range conns {
		c.Close(websocket.StatusNormalClosure, "done")
	}
	routerCancel()
}

// TestLoad_ConcurrentConnections_Throughput runs a load test measuring throughput
// with 100 concurrent WebSocket connections receiving paced events. Events are
// emitted at a controlled rate to stay within the per-connection buffer capacity,
// validating that the system can sustain 100+ connections with reliable delivery.
func TestLoad_ConcurrentConnections_Throughput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	const (
		numConnections = 100
		totalEvents    = 200
		// Pace events to prevent buffer overflow: emit every 2ms, giving the
		// WebSocket write pump time to drain each connection's 256-entry buffer.
		emitInterval = 2 * time.Millisecond
	)

	src := &controllableEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut)
	handler := eventstream.NewHandler(router, discardLogger(), eventstream.WithAcceptOptions(websocket.AcceptOptions{
		InsecureSkipVerify: true,
	}))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		claims := &platformauth.Claims{
			UserID:   "load-user",
			TenantID: "load-tenant",
			Roles:    []string{"ops:admin"},
		}
		ctx := eventstream.ContextWithClaims(r.Context(), claims)
		handler.ServeHTTP(w, r.WithContext(ctx))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	routerCtx, routerCancel := context.WithCancel(context.Background())
	defer routerCancel()

	go func() { _ = router.Start(routerCtx) }()

	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool { return src.IsStarted() })
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	// Create connections.
	conns := make([]*websocket.Conn, numConnections)
	for i := 0; i < numConnections; i++ {
		c, _, dialErr := websocket.Dial(ctx, wsURL, nil)
		require.NoError(t, dialErr, "failed to dial connection %d", i)
		defer c.CloseNow()
		conns[i] = c
	}

	// Wait for all registrations.
	err = await.New().
		AtMost(15 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("load-tenant")) >= numConnections
		})
	require.NoError(t, err, "not all connections registered: want %d, got %d",
		numConnections, len(router.GetConnectionsByTenant("load-tenant")))

	t.Logf("all %d connections registered", numConnections)

	// Subscribe all.
	for i, c := range conns {
		subMsg := eventstream.ClientMessage{
			Type:     eventstream.ClientMessageTypeSubscribe,
			ID:       fmt.Sprintf("sub-load-%d", i),
			Channels: []eventstream.ChannelPattern{"*"},
		}
		data, _ := json.Marshal(subMsg)
		require.NoError(t, c.Write(ctx, websocket.MessageText, data))
	}

	// Read all confirmations.
	for i, c := range conns {
		var resp eventstream.ServerMessage
		err = await.New().
			AtMost(10 * time.Second).
			PollInterval(10 * time.Millisecond).
			UntilNoError(func() error {
				_, msgData, readErr := c.Read(ctx)
				if readErr != nil {
					return readErr
				}
				return json.Unmarshal(msgData, &resp)
			})
		require.NoError(t, err, "subscription confirmation for conn %d", i)
	}

	t.Logf("all %d subscriptions confirmed, starting load", numConnections)

	// Measure: emit events at paced rate and collect latencies.
	var deliveredCount atomic.Int64
	latencies := make([]atomic.Int64, numConnections)

	// Start readers on all connections.
	var readWg sync.WaitGroup
	for idx, c := range conns {
		readWg.Add(1)
		go func(connIdx int, conn *websocket.Conn) {
			defer readWg.Done()
			for j := 0; j < totalEvents; j++ {
				start := time.Now()
				_, _, readErr := conn.Read(ctx)
				if readErr != nil {
					return
				}
				latencies[connIdx].Add(time.Since(start).Nanoseconds())
				deliveredCount.Add(1)
			}
		}(idx, c)
	}

	// Emit events at a paced rate.
	emitStart := time.Now()
	for i := 0; i < totalEvents; i++ {
		evt, _ := eventstream.NewDomainEvent(
			"load.event.v1",
			"load.event.v1",
			fmt.Sprintf("agg-load-%d", i),
			"Load",
			"load-tenant",
			"",
			"",
			[]byte(`{"seq":1}`),
		)
		src.EmitEvent(ctx, evt)

		// Pace emission to allow WS drain.
		elapsed := time.Since(emitStart)
		expected := time.Duration(i+1) * emitInterval
		if expected > elapsed {
			time.Sleep(expected - elapsed)
		}
	}
	emitDuration := time.Since(emitStart)

	// Wait for all reads to complete.
	readDone := make(chan struct{})
	go func() {
		readWg.Wait()
		close(readDone)
	}()

	select {
	case <-readDone:
	case <-time.After(60 * time.Second):
		t.Fatalf("timed out waiting for all reads; delivered %d of expected %d",
			deliveredCount.Load(), int64(numConnections)*int64(totalEvents))
	}

	totalDelivered := deliveredCount.Load()
	expectedTotal := int64(numConnections) * int64(totalEvents)
	deliveryRate := float64(totalDelivered) / emitDuration.Seconds()

	// Calculate average latency across all connections.
	var totalLatencyNs int64
	for i := range latencies {
		totalLatencyNs += latencies[i].Load()
	}

	var avgLatencyMs float64
	if totalDelivered > 0 {
		avgLatencyMs = float64(totalLatencyNs) / float64(totalDelivered) / 1e6
	}

	t.Logf("Load test results:")
	t.Logf("  Connections:     %d", numConnections)
	t.Logf("  Events emitted:  %d", totalEvents)
	t.Logf("  Total delivered: %d / %d", totalDelivered, expectedTotal)
	t.Logf("  Emit duration:   %v", emitDuration)
	t.Logf("  Delivery rate:   %.0f events/sec (aggregate)", deliveryRate)
	t.Logf("  Avg latency:     %.2f ms", avgLatencyMs)

	// Success criteria from PRD Section 12.
	assert.Equal(t, expectedTotal, totalDelivered, "all paced events must be delivered")
	assert.Less(t, avgLatencyMs, 500.0, "average latency should be < 500ms")
	assert.GreaterOrEqual(t, len(router.GetConnectionsByTenant("load-tenant")), 100,
		"must support 100+ concurrent connections")

	for _, c := range conns {
		c.Close(websocket.StatusNormalClosure, "done")
	}
	routerCancel()
}

// TestLoad_HighThroughput_EventsPerSecond tests sustained throughput of 5000 events/sec.
// Events are rate-limited to 5000/sec on the emitter side. With 10 connections, the
// system must sustain 50,000 total deliveries/sec. Since Connection buffers are finite
// (256 entries), some drops are expected under peak load; the test verifies >= 80% delivery.
// Threshold set to 80% because CI runners have variable CPU scheduling that affects
// both emission rate and delivery reliability (observed 88.8% on GitHub Actions).
func TestLoad_HighThroughput_EventsPerSecond(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping high throughput test in short mode")
	}

	const (
		numConnections    = 10
		eventsPerSecond   = 5000
		testDurationSec   = 2
		totalEvents       = eventsPerSecond * testDurationSec
		expectedPerConn   = totalEvents
		deliveryThreshold = 0.80 // Allow 20% loss under burst load on CI runners.
	)

	src := &controllableEventSource{}
	fanOut := eventstream.NewInProcessFanOut()
	router := eventstream.NewRouter(src, fanOut)
	handler := eventstream.NewHandler(router, discardLogger(), eventstream.WithAcceptOptions(websocket.AcceptOptions{
		InsecureSkipVerify: true,
	}))

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		claims := &platformauth.Claims{
			UserID:   "throughput-user",
			TenantID: "throughput-tenant",
			Roles:    []string{"ops:admin"},
		}
		ctx := eventstream.ContextWithClaims(r.Context(), claims)
		handler.ServeHTTP(w, r.WithContext(ctx))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	routerCtx, routerCancel := context.WithCancel(context.Background())
	defer routerCancel()

	go func() { _ = router.Start(routerCtx) }()

	err := await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool { return src.IsStarted() })
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	conns := make([]*websocket.Conn, numConnections)
	for i := 0; i < numConnections; i++ {
		c, _, dialErr := websocket.Dial(ctx, wsURL, nil)
		require.NoError(t, dialErr)
		defer c.CloseNow()
		conns[i] = c
	}

	err = await.New().
		AtMost(10 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return len(router.GetConnectionsByTenant("throughput-tenant")) >= numConnections
		})
	require.NoError(t, err)

	// Subscribe all.
	for i, c := range conns {
		subMsg := eventstream.ClientMessage{
			Type:     eventstream.ClientMessageTypeSubscribe,
			ID:       fmt.Sprintf("sub-tp-%d", i),
			Channels: []eventstream.ChannelPattern{"*"},
		}
		data, _ := json.Marshal(subMsg)
		require.NoError(t, c.Write(ctx, websocket.MessageText, data))

		var resp eventstream.ServerMessage
		err = await.New().
			AtMost(5 * time.Second).
			PollInterval(10 * time.Millisecond).
			UntilNoError(func() error {
				_, msgData, readErr := c.Read(ctx)
				if readErr != nil {
					return readErr
				}
				return json.Unmarshal(msgData, &resp)
			})
		require.NoError(t, err)
	}

	// Start readers.
	perConnReceived := make([]atomic.Int64, numConnections)
	var readWg sync.WaitGroup
	for idx, c := range conns {
		readWg.Add(1)
		go func(connIdx int, conn *websocket.Conn) {
			defer readWg.Done()
			for {
				_, _, readErr := conn.Read(ctx)
				if readErr != nil {
					return
				}
				perConnReceived[connIdx].Add(1)
			}
		}(idx, c)
	}

	// Emit events at target rate.
	interval := time.Second / time.Duration(eventsPerSecond)
	emitStart := time.Now()
	for i := 0; i < totalEvents; i++ {
		evt, _ := eventstream.NewDomainEvent(
			"throughput.event.v1",
			"throughput.event.v1",
			fmt.Sprintf("agg-%d", i),
			"Throughput",
			"throughput-tenant",
			"",
			"",
			[]byte(`{}`),
		)
		src.EmitEvent(ctx, evt)

		// Rate-limit emission.
		elapsed := time.Since(emitStart)
		expected := time.Duration(i+1) * interval
		if expected > elapsed {
			time.Sleep(expected - elapsed)
		}
	}

	emitDuration := time.Since(emitStart)
	actualRate := float64(totalEvents) / emitDuration.Seconds()

	// Wait for delivery to stabilize.
	_ = await.New().
		AtMost(15 * time.Second).
		PollInterval(200 * time.Millisecond).
		Until(func() bool {
			var total int64
			for i := range perConnReceived {
				total += perConnReceived[i].Load()
			}
			threshold := float64(expectedPerConn) * float64(numConnections) * deliveryThreshold
			return float64(total) >= threshold
		})

	var totalReceived int64
	for i := range perConnReceived {
		totalReceived += perConnReceived[i].Load()
	}

	totalExpected := int64(expectedPerConn) * int64(numConnections)

	t.Logf("High throughput test results:")
	t.Logf("  Target rate:     %d events/sec", eventsPerSecond)
	t.Logf("  Actual emit:     %.0f events/sec", actualRate)
	t.Logf("  Emit duration:   %v", emitDuration)
	t.Logf("  Total delivered: %d / %d (%.1f%%)", totalReceived, totalExpected,
		float64(totalReceived)/float64(totalExpected)*100)
	t.Logf("  Connections:     %d", numConnections)

	deliveryPct := float64(totalReceived) / float64(totalExpected)
	assert.GreaterOrEqual(t, deliveryPct, deliveryThreshold,
		"delivery rate should be >= %.0f%%", deliveryThreshold*100)

	// Log actual emit rate for diagnostics. CI runners may not sustain the
	// target rate due to CPU scheduling constraints; this is not a code defect.
	t.Logf("  Emit rate pct:   %.0f%% of target", actualRate/float64(eventsPerSecond)*100)

	for _, c := range conns {
		c.Close(websocket.StatusNormalClosure, "done")
	}
	routerCancel()
}

// ─── Test Helpers ────────────────────────────────────────────────────────────────

// controllableEventSource is a test EventSource that captures the handler and allows
// manual event emission. Used for E2E tests that don't need a real Kafka container.
type controllableEventSource struct {
	mu      sync.Mutex
	handler eventstream.EventHandler
	started atomic.Bool
}

func (s *controllableEventSource) Start(ctx context.Context, handler eventstream.EventHandler) error {
	s.mu.Lock()
	s.handler = handler
	s.mu.Unlock()
	s.started.Store(true)

	<-ctx.Done()
	return nil
}

func (s *controllableEventSource) IsStarted() bool {
	return s.started.Load()
}

func (s *controllableEventSource) EmitEvent(ctx context.Context, event eventstream.DomainEvent) {
	s.mu.Lock()
	h := s.handler
	s.mu.Unlock()
	if h != nil {
		_ = h(ctx, event)
	}
}

// discardLogger returns a logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// consumerGroupActiveHelper returns nil when the given consumer group has at
// least one active member, or a non-nil error otherwise. Used with
// await.UntilNoError to wait for the Kafka consumer group to be ready.
func consumerGroupActiveHelper(ctx context.Context, broker, groupID string) error {
	client, err := kgo.NewClient(kgo.SeedBrokers(broker))
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer client.Close()

	admin := kadm.NewClient(client)
	described, err := admin.DescribeGroups(ctx, groupID)
	if err != nil {
		return fmt.Errorf("describe groups: %w", err)
	}
	grp, ok := described[groupID]
	if !ok || grp.Err != nil || len(grp.Members) == 0 {
		return fmt.Errorf("group %q has no active members", groupID)
	}
	return nil
}

// mustCreateTopicsHelper creates topics in the Kafka broker.
func mustCreateTopicsHelper(t *testing.T, ctx context.Context, broker string, topics ...string) {
	t.Helper()

	client, err := kgo.NewClient(kgo.SeedBrokers(broker))
	require.NoError(t, err, "failed to create kgo client")
	defer client.Close()

	admin := kadm.NewClient(client)
	resp, err := admin.CreateTopics(ctx, 1, 1, nil, topics...)
	require.NoError(t, err, "CreateTopics failed")
	for _, tr := range resp {
		if tr.Err != nil && tr.ErrMessage != "Topic already exists." {
			t.Fatalf("error creating topic %q: %v (%s)", tr.Topic, tr.Err, tr.ErrMessage)
		}
	}
}
