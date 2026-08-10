package stdio

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/chat"
	"github.com/tiredbooy/internal/models"
)

type modelProvider struct{ model string }

func (p *modelProvider) Name() string          { return "Ollama" }
func (p *modelProvider) ChatModel() string     { return p.model }
func (p *modelProvider) SetChatModel(v string) { p.model = v }
func (p *modelProvider) ChatModels(context.Context) ([]ai.ModelInfo, error) {
	return []ai.ModelInfo{{Name: "small-chat"}, {Name: "large-chat"}}, nil
}
func (p *modelProvider) ChatWithToolsResult(context.Context, []models.Message, []models.ToolDefinition) (ai.ToolChatResult, error) {
	return ai.ToolChatResult{}, nil
}
func (p *modelProvider) StreamChatWith(context.Context, []models.Message, ai.StreamCallbacks) (string, error) {
	return "", nil
}

func TestServeHandlesMalformedRequestThenHello(t *testing.T) {
	input := bytes.NewBufferString("not json\n{\"version\":1,\"requestId\":\"hello-1\",\"type\":\"engine.hello\"}\n")
	var output bytes.Buffer

	if err := Serve(context.Background(), input, &output, chat.NewSession(nil)); err != nil {
		t.Fatalf("serve: %v", err)
	}

	decoder := json.NewDecoder(&output)
	var invalid, ready Event
	if err := decoder.Decode(&invalid); err != nil {
		t.Fatalf("decode invalid request event: %v", err)
	}
	if invalid.Type != "error" || invalid.Version != ProtocolVersion {
		t.Fatalf("invalid request event = %#v", invalid)
	}
	if err := decoder.Decode(&ready); err != nil {
		t.Fatalf("decode ready event: %v", err)
	}
	if ready.Type != "engine.ready" || ready.RequestID != "hello-1" {
		t.Fatalf("ready event = %#v", ready)
	}
}

func TestServeRejectsUnsupportedVersionWithoutStopping(t *testing.T) {
	input := bytes.NewBufferString("{\"version\":2,\"requestId\":\"bad-version\",\"type\":\"engine.hello\"}\n{\"version\":1,\"requestId\":\"hello-2\",\"type\":\"engine.hello\"}\n")
	var output bytes.Buffer

	if err := Serve(context.Background(), input, &output, chat.NewSession(nil)); err != nil {
		t.Fatalf("serve: %v", err)
	}

	decoder := json.NewDecoder(&output)
	var rejected, ready Event
	_ = decoder.Decode(&rejected)
	_ = decoder.Decode(&ready)
	if rejected.RequestID != "bad-version" || rejected.Type != "error" {
		t.Fatalf("version rejection = %#v", rejected)
	}
	if ready.Type != "engine.ready" {
		t.Fatalf("engine did not continue after version rejection: %#v", ready)
	}
}

func TestValidateRequiresRequestIdentity(t *testing.T) {
	if err := validate(Request{Version: ProtocolVersion, Type: RequestHello}); err == nil {
		t.Fatal("request without requestId was accepted")
	}
	if err := validate(Request{Version: ProtocolVersion, RequestID: "r1"}); err == nil {
		t.Fatal("request without type was accepted")
	}
}

func TestValidateRequiresOperationFields(t *testing.T) {
	tests := []Request{
		{Version: ProtocolVersion, RequestID: "r1", Type: RequestSubmit},
		{Version: ProtocolVersion, RequestID: "r2", Type: RequestCancel},
		{Version: ProtocolVersion, RequestID: "r3", Type: RequestPlanApprove},
		{Version: ProtocolVersion, RequestID: "r4", Type: RequestPlanReject},
		{Version: ProtocolVersion, RequestID: "r5", Type: RequestModelSelect},
		{Version: ProtocolVersion, RequestID: "r6", Type: RequestProviderConnect},
		{Version: ProtocolVersion, RequestID: "r7", Type: RequestProviderOAuth},
	}
	for _, request := range tests {
		if err := validate(request); err == nil {
			t.Fatalf("request without operation field was accepted: %#v", request)
		}
	}
}

func TestServeListsAndSelectsModelsThroughSessionBoundary(t *testing.T) {
	provider := &modelProvider{model: "small-chat"}
	loop := chat.NewLoop(provider, map[string]ai.ChatProvider{"ollama": provider}, nil, nil, nil, nil)
	session := chat.NewSession(loop)
	input := bytes.NewBufferString(
		`{"version":1,"requestId":"models-1","type":"model.list"}` + "\n" +
			`{"version":1,"requestId":"models-2","type":"model.select","providerId":"ollama","model":"large-chat"}` + "\n",
	)
	var output bytes.Buffer
	if err := Serve(context.Background(), input, &output, session); err != nil {
		t.Fatalf("serve: %v", err)
	}

	decoder := json.NewDecoder(&output)
	var listed, selected Event
	if err := decoder.Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&selected); err != nil {
		t.Fatal(err)
	}
	if listed.Type != "model.options" || len(listed.Models) != 2 || !listed.Models[0].Current {
		t.Fatalf("listed models = %#v", listed)
	}
	if selected.Type != "model.selected" || selected.Provider != "Ollama" || selected.Model != "large-chat" {
		t.Fatalf("selected model = %#v", selected)
	}
}

func TestServeListsStaticAndCustomProviderOptions(t *testing.T) {
	input := bytes.NewBufferString(`{"version":1,"requestId":"providers-1","type":"provider.list"}` + "\n")
	var output bytes.Buffer
	if err := Serve(context.Background(), input, &output, chat.NewSession(nil)); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var event Event
	if err := json.NewDecoder(&output).Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "provider.presets" || event.RequestID != "providers-1" {
		t.Fatalf("event = %#v", event)
	}
	want := map[string]bool{"openai-codex": false, "xai-oauth": false, "xai": false, "custom": false}
	for _, preset := range event.Presets {
		if _, exists := want[preset.ID]; exists {
			want[preset.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("provider preset %q is missing", id)
		}
	}
}
