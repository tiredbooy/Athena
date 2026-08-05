package stdio

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/tiredbooy/internal/chat"
)

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
