// Package kafka provides Protocol Buffer consumer and producer utilities for Kafka.
//
// [ProtoConsumer] deserializes protobuf messages from Kafka topics and dispatches
// them to a [MessageHandler]. Failed messages are routed to a dead-letter queue
// after the configured number of retries.
//
// [ProtoProducer] serializes protobuf messages and publishes them to a Kafka topic
// with configurable retry and acknowledgement semantics.
//
// Configuration is loaded from environment variables (KAFKA_BOOTSTRAP_SERVERS, etc.)
// via [DefaultConfig].
//
// # Infrastructure package
//
// This is a platform-level package with no domain semantics.
// Business-level event definitions live in shared/platform/events.
package kafka
