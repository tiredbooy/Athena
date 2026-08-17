package stdio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/tiredbooy/internal/agent"
	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/chat"
)

// F-01: protocol/athena.v1.schema.json is the contract, not a description of
// it. These tests fail when the Go side drifts — a new struct field, a renamed
// JSON tag, or an event type the schema has never heard of. Whoever changes the
// wire format has to change the schema in the same commit.

const schemaPath = "../../../protocol/athena.v1.schema.json"

type jsonSchema struct {
	Defs map[string]struct {
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
	} `json:"$defs"`
}

func loadSchema(t *testing.T) jsonSchema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(schemaPath))
	if err != nil {
		t.Fatalf("read protocol schema: %v", err)
	}
	var schema jsonSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse protocol schema: %v", err)
	}
	return schema
}

// schemaEnum pulls a property's enum values out of the raw schema.
func schemaEnum(t *testing.T, schema jsonSchema, def, property string) []string {
	t.Helper()
	raw, ok := schema.Defs[def].Properties[property]
	if !ok {
		t.Fatalf("schema $defs.%s has no %q property", def, property)
	}
	var holder struct {
		Enum []string `json:"enum"`
	}
	if err := json.Unmarshal(raw, &holder); err != nil {
		t.Fatalf("parse %s.%s enum: %v", def, property, err)
	}
	if len(holder.Enum) == 0 {
		t.Fatalf("schema $defs.%s.%s has no enum", def, property)
	}
	return holder.Enum
}

// jsonFieldNames lists the wire names a Go struct actually produces.
func jsonFieldNames(t *testing.T, value any) []string {
	t.Helper()
	structType := reflect.TypeOf(value)
	names := make([]string, 0, structType.NumField())
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			name = field.Name
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func assertSchemaCoversStruct(t *testing.T, schema jsonSchema, def string, value any) {
	t.Helper()
	definition, ok := schema.Defs[def]
	if !ok {
		t.Fatalf("schema has no $defs.%s", def)
	}
	if definition.AdditionalProperties == nil || *definition.AdditionalProperties {
		t.Fatalf("$defs.%s must set additionalProperties:false so extra fields are explicit", def)
	}
	for _, name := range jsonFieldNames(t, value) {
		if _, described := definition.Properties[name]; !described {
			t.Errorf("%T field %q is on the wire but missing from schema $defs.%s", value, name, def)
		}
	}
	for name := range definition.Properties {
		found := false
		for _, actual := range jsonFieldNames(t, value) {
			if actual == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("schema $defs.%s documents %q, which %T no longer sends", def, name, value)
		}
	}
}

func TestSchemaMatchesWireStructs(t *testing.T) {
	schema := loadSchema(t)
	assertSchemaCoversStruct(t, schema, "request", Request{})
	assertSchemaCoversStruct(t, schema, "event", Event{})
	assertSchemaCoversStruct(t, schema, "action", ai.Action{})
	assertSchemaCoversStruct(t, schema, "activity", chat.ActivityEvent{})
	assertSchemaCoversStruct(t, schema, "modelOption", chat.ModelOption{})
	assertSchemaCoversStruct(t, schema, "providerPreset", chat.ProviderPreset{})
	assertSchemaCoversStruct(t, schema, "ledgerRecord", agent.LedgerRecord{})
	assertSchemaCoversStruct(t, schema, "connection", chat.ConnectionInput{})
}

// The request constants and the schema enum must name exactly the same set.
func TestSchemaListsEveryRequestType(t *testing.T) {
	handled := []string{
		RequestHello, RequestSubmit, RequestCancel, RequestSessionReset,
		RequestPlanApprove, RequestPlanReject, RequestModelList, RequestModelSelect,
		RequestProviderList, RequestProviderConnect, RequestProviderOAuth,
	}
	assertSameSet(t, "request type", schemaEnum(t, loadSchema(t), "request", "type"), handled)
}

// emittedEventTypes is every type name the server can put on the wire. Static
// names come from Event{Type: "..."} literals; the turn.* names are built in
// forwardSessionEvent from chat's own event types.
func emittedEventTypes() []string {
	forwarded := []string{
		string(chat.EventActivity),
		string(chat.EventResponse),
		string(chat.EventPlanReady),
		"turn." + string(chat.EventCompleted),
		"turn." + string(chat.EventCancelled),
		"turn." + string(chat.EventError),
	}
	static := []string{
		"engine.ready", "turn.started", "turn.failed", "turn.cancellation_requested",
		"plan.approved", "plan.rejected",
		"model.options", "model.selected",
		"provider.presets", "provider.connected",
		"provider.oauth.started", "provider.oauth.progress", "provider.oauth.cancelled",
		"session.reset", "error",
	}
	return append(forwarded, static...)
}

func TestSchemaListsEveryEventType(t *testing.T) {
	assertSameSet(t, "event type", schemaEnum(t, loadSchema(t), "event", "type"), emittedEventTypes())
}

// The server builds turn.* names by concatenation, so a renamed chat event type
// would silently produce a name the schema never listed.
func TestForwardedEventNamesStayInSchema(t *testing.T) {
	allowed := make(map[string]bool)
	for _, name := range schemaEnum(t, loadSchema(t), "event", "type") {
		allowed[name] = true
	}
	for _, eventType := range []chat.EventType{
		chat.EventActivity, chat.EventResponse, chat.EventPlanReady,
		chat.EventCompleted, chat.EventCancelled, chat.EventError,
	} {
		name := string(eventType)
		switch eventType {
		case chat.EventCompleted, chat.EventCancelled, chat.EventError:
			name = "turn." + name
		}
		if !allowed[name] {
			t.Errorf("forwarded chat event %q becomes wire type %q, which the schema does not list", eventType, name)
		}
	}
}

func assertSameSet(t *testing.T, label string, schemaValues, goValues []string) {
	t.Helper()
	inSchema := make(map[string]bool, len(schemaValues))
	for _, value := range schemaValues {
		inSchema[value] = true
	}
	inGo := make(map[string]bool, len(goValues))
	for _, value := range goValues {
		inGo[value] = true
	}
	for value := range inGo {
		if !inSchema[value] {
			t.Errorf("%s %q is used by the server but missing from the schema", label, value)
		}
	}
	for value := range inSchema {
		if !inGo[value] {
			t.Errorf("%s %q is in the schema but the server never uses it", label, value)
		}
	}
}
