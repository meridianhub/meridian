// gen-event-publishers generates type-safe event publisher packages from AsyncAPI specs.
//
// It reads AsyncAPI 3.0.0 YAML files from api/asyncapi/ and generates Go packages
// in gen/events/ that provide typed wrappers around the OutboxPublisher.
//
// Usage:
//
//	go run ./tools/gen-event-publishers
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/format"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

const (
	asyncAPIDir = "api/asyncapi"
	outputDir   = "gen/events"
	modulePath  = "github.com/meridianhub/meridian"
	protoDir    = "api/proto/meridian/events/v1"
)

// AsyncAPI represents the subset of AsyncAPI 3.0.0 we need.
type AsyncAPI struct {
	Info       Info                 `yaml:"info"`
	Channels   map[string]Channel   `yaml:"channels"`
	Operations map[string]Operation `yaml:"operations"`
	Components Components           `yaml:"components"`
}

// Info holds AsyncAPI document info.
type Info struct {
	Title string `yaml:"title"`
}

// Channel represents an AsyncAPI channel.
type Channel struct {
	Address     string             `yaml:"address"`
	Description string             `yaml:"description"`
	Messages    map[string]Message `yaml:"messages"`
}

// Message is a $ref to a component message.
type Message struct {
	Ref string `yaml:"$ref"`
}

// Operation represents an AsyncAPI operation.
type Operation struct {
	Action  string       `yaml:"action"`
	Channel OperationRef `yaml:"channel"`
}

// OperationRef is a $ref to a channel.
type OperationRef struct {
	Ref string `yaml:"$ref"`
}

// Components holds the reusable message and schema definitions.
type Components struct {
	Messages map[string]ComponentMessage `yaml:"messages"`
	Schemas  map[string]Schema           `yaml:"schemas"`
}

// ComponentMessage is a named message definition.
type ComponentMessage struct {
	Name    string `yaml:"name"`
	Payload Ref    `yaml:"payload"`
}

// Ref is a generic $ref.
type Ref struct {
	Ref string `yaml:"$ref"`
}

// Schema holds the JSON schema with a description containing the proto type.
type Schema struct {
	Description string `yaml:"description"`
}

// EventDef holds the data needed to generate a publisher method.
type EventDef struct {
	MethodName  string
	ProtoType   string
	Topic       string
	EventType   string
	Description string
}

// ServiceDef holds all events for a single service publisher package.
type ServiceDef struct {
	PackageName  string
	ServiceTitle string
	SourceFile   string
	Events       []EventDef
	ModulePath   string
}

// protoTypeRe extracts the proto message name from the schema description.
// Example: "Derived from protobuf message meridian.events.v1.TransactionCapturedEvent"
var protoTypeRe = regexp.MustCompile(`meridian\.events\.v1\.(\w+)`)

// protoMessageRe matches "message FooEvent {" lines in .proto files.
var protoMessageRe = regexp.MustCompile(`^message\s+(\w+)\s*\{`)

// loadProtoTypes scans .proto files and returns a set of known message type names.
func loadProtoTypes() (map[string]bool, error) {
	entries, err := os.ReadDir(protoDir)
	if err != nil {
		return nil, fmt.Errorf("read proto dir: %w", err)
	}

	types := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".proto") {
			continue
		}
		f, err := os.Open(filepath.Join(protoDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if m := protoMessageRe.FindStringSubmatch(scanner.Text()); len(m) == 2 {
				types[m[1]] = true
			}
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	return types, nil
}

func main() {
	knownProtoTypes, err := loadProtoTypes()
	if err != nil {
		log.Fatalf("Failed to load proto types: %v", err)
	}
	fmt.Printf("Found %d proto message types in %s\n", len(knownProtoTypes), protoDir)

	entries, err := os.ReadDir(asyncAPIDir)
	if err != nil {
		log.Fatalf("Failed to read %s: %v", asyncAPIDir, err)
	}

	var services []ServiceDef

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		svc, err := processAsyncAPIFile(filepath.Join(asyncAPIDir, entry.Name()), knownProtoTypes)
		if err != nil {
			log.Fatalf("Failed to process %s: %v", entry.Name(), err)
		}
		if svc == nil {
			continue
		}

		services = append(services, *svc)
	}

	if len(services) == 0 {
		log.Fatal("No services generated")
	}

	// Clean stale packages from prior runs so removed/renamed specs don't linger.
	if err := os.RemoveAll(outputDir); err != nil {
		log.Fatalf("Failed to clean %s: %v", outputDir, err)
	}

	for _, svc := range services {
		if err := generatePublisher(svc); err != nil {
			log.Fatalf("Failed to generate %s: %v", svc.PackageName, err)
		}
		fmt.Printf("Generated gen/events/%s/publisher.go (%d events)\n", svc.PackageName, len(svc.Events))
	}

	fmt.Printf("\nGenerated %d publisher packages in %s/\n", len(services), outputDir)
}

func processAsyncAPIFile(path string, knownProtoTypes map[string]bool) (*ServiceDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var spec AsyncAPI
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}

	filename := filepath.Base(path)
	serviceName := strings.TrimSuffix(filename, ".yaml")

	// Build a map from schema name to proto type extracted from description.
	// Only include types that exist in the actual .proto files.
	schemaToProto := make(map[string]string)
	for name, schema := range spec.Components.Schemas {
		matches := protoTypeRe.FindStringSubmatch(schema.Description)
		if len(matches) < 2 {
			continue
		}
		protoType := matches[1]
		if !knownProtoTypes[protoType] {
			log.Printf("INFO: skipping %s/%s (proto type %s not found in .proto files)", filename, name, protoType)
			continue
		}
		schemaToProto[name] = protoType
	}

	// If no schemas have valid proto types, skip this service.
	if len(schemaToProto) == 0 {
		log.Printf("INFO: skipping %s (no valid proto type references in schemas)", filename)
		return nil, nil //nolint:nilnil // nil,nil signals "skip this file" to caller
	}

	// Build event definitions from operations that have action=send.
	var events []EventDef
	for _, op := range spec.Operations {
		if op.Action != "send" {
			continue
		}

		// Resolve the channel ref: "#/channels/position-keeping.transaction-captured.v1"
		channelKey := strings.TrimPrefix(op.Channel.Ref, "#/channels/")
		channel, ok := spec.Channels[channelKey]
		if !ok {
			log.Printf("WARN: %s: operation references unknown channel %q", filename, channelKey)
			continue
		}

		events = append(events, resolveChannelEvents(channel, spec.Components, schemaToProto)...)
	}

	if len(events) == 0 {
		return nil, nil //nolint:nilnil // nil,nil signals "skip this file" to caller
	}

	// Sort events by method name for deterministic output.
	sort.Slice(events, func(i, j int) bool {
		return events[i].MethodName < events[j].MethodName
	})

	return &ServiceDef{
		PackageName:  serviceToPackage(serviceName),
		ServiceTitle: spec.Info.Title,
		SourceFile:   filename,
		Events:       events,
		ModulePath:   modulePath,
	}, nil
}

// resolveChannelEvents follows the $ref chain for each message in a channel
// and returns EventDefs for messages with known proto types.
func resolveChannelEvents(channel Channel, components Components, schemaToProto map[string]string) []EventDef {
	// Iterate messages deterministically by sorting keys.
	msgKeys := make([]string, 0, len(channel.Messages))
	for k := range channel.Messages {
		msgKeys = append(msgKeys, k)
	}
	sort.Strings(msgKeys)

	var out []EventDef
	for _, msgKey := range msgKeys {
		msg := channel.Messages[msgKey]

		// Follow the explicit $ref chain: message -> component message -> payload schema.
		componentName := strings.TrimPrefix(msg.Ref, "#/components/messages/")
		component, ok := components.Messages[componentName]
		if !ok {
			log.Printf("WARN: channel %q references unknown component message %q", channel.Address, componentName)
			continue
		}
		schemaName := strings.TrimPrefix(component.Payload.Ref, "#/components/schemas/")

		protoType, ok := schemaToProto[schemaName]
		if !ok {
			// Schema exists but has no valid proto type mapping (already logged during schema scan).
			continue
		}

		topic := channel.Address
		eventType := topicToEventType(topic)
		methodName := "Publish" + componentName

		out = append(out, EventDef{
			MethodName:  methodName,
			ProtoType:   protoType,
			Topic:       topic,
			EventType:   eventType,
			Description: channel.Description,
		})
	}
	return out
}

// topicToEventType converts "position-keeping.transaction-captured.v1" to "position_keeping.transaction_captured.v1".
func topicToEventType(topic string) string {
	return strings.ReplaceAll(topic, "-", "_")
}

// serviceToPackage converts "position-keeping" to "position_keeping".
func serviceToPackage(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

func generatePublisher(svc ServiceDef) error {
	dir := filepath.Join(outputDir, svc.PackageName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	var buf bytes.Buffer
	if err := publisherTmpl.Execute(&buf, svc); err != nil {
		return fmt.Errorf("template: %w", err)
	}

	// Format the generated code.
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		// Write unformatted for debugging.
		debugPath := filepath.Join(dir, "publisher.go.unformatted")
		_ = os.WriteFile(debugPath, buf.Bytes(), 0o644)
		return fmt.Errorf("gofmt (see %s): %w", debugPath, err)
	}

	return os.WriteFile(filepath.Join(dir, "publisher.go"), formatted, 0o644)
}

var publisherTmpl = template.Must(template.New("publisher").Parse(
	`// Code generated from api/asyncapi/{{ .SourceFile }}. DO NOT EDIT.

package {{ .PackageName }}

import (
	"context"

	eventsv1 "{{ .ModulePath }}/api/proto/meridian/events/v1"
	"{{ .ModulePath }}/shared/platform/events"
	"gorm.io/gorm"
)

// Publisher provides type-safe event publishing for the {{ .ServiceTitle }} domain.
type Publisher struct {
	outbox *events.OutboxPublisher
}

// NewPublisher creates a new Publisher wrapping the given OutboxPublisher.
func NewPublisher(outbox *events.OutboxPublisher) *Publisher {
	return &Publisher{outbox: outbox}
}

// PublishOption configures event publishing behavior.
type PublishOption func(*events.PublishConfig)

// WithCorrelationID returns a PublishOption that sets the correlation ID.
func WithCorrelationID(id string) PublishOption {
	return func(c *events.PublishConfig) { c.CorrelationID = id }
}

// WithCausationID returns a PublishOption that sets the causation ID.
func WithCausationID(id string) PublishOption {
	return func(c *events.PublishConfig) { c.CausationID = id }
}

// WithPartitionKey returns a PublishOption that overrides the default partition key.
func WithPartitionKey(key string) PublishOption {
	return func(c *events.PublishConfig) { c.PartitionKey = key }
}
{{ range .Events }}
// {{ .MethodName }} publishes a {{ .ProtoType }} to "{{ .Topic }}".
//
// {{ .Description }}
func (p *Publisher) {{ .MethodName }}(
	ctx context.Context,
	tx *gorm.DB,
	event *eventsv1.{{ .ProtoType }},
	aggregateID string,
	aggregateType string,
	opts ...PublishOption,
) error {
	config := events.PublishConfig{
		EventType:     "{{ .EventType }}",
		Topic:         "{{ .Topic }}",
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return p.outbox.Publish(ctx, tx, event, config)
}
{{ end -}}
`))
