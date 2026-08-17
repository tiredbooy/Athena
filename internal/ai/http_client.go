package ai

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

const (
	providerDialTimeout         = 10 * time.Second
	providerTLSHandshakeTimeout = 10 * time.Second
	// Longer than chat.TurnTimeout on purpose: for a non-streaming completion
	// the server sends no headers until the whole answer is generated, so this
	// clock is still ticking while a slow local model is working correctly.
	// The turn context is the deadline that should normally fire; this one only
	// catches callers that pass an unbounded context.
	providerResponseHeaderTimeout = 6 * time.Minute
)

// newProviderHTTPClient builds the HTTP client every model and embedding
// adapter uses.
//
// There is deliberately no http.Client.Timeout. That clock covers the entire
// exchange including reading the response body, so on a local model it would
// abort a generation that is slow but perfectly alive. The timeouts below sit
// on the connection instead: they kill a dial, TLS handshake, or server that
// never answers, and never interrupt a stream that is still delivering bytes.
// End-to-end bounding is the caller's context, not this client.
func newProviderHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: providerDialTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = providerTLSHandshakeTimeout
	transport.ResponseHeaderTimeout = providerResponseHeaderTimeout
	return &http.Client{Transport: transport, CheckRedirect: refuseCredentialLeakingRedirect}
}

// newOAuthHTTPClient bounds the whole exchange, which is correct here and only
// here: a token or device-code call is one short request/response, never a
// generation, so a hang has no legitimate explanation.
func newOAuthHTTPClient() *http.Client {
	client := newProviderHTTPClient()
	client.Timeout = 30 * time.Second
	return client
}

// refuseCredentialLeakingRedirect stops a provider request from carrying its
// credentials to a host the user never configured.
//
// net/http strips only the headers it knows are sensitive (Authorization,
// Cookie, WWW-Authenticate) and only across a domain change; Athena also sends
// bearer tokens as x-api-key and chatgpt-account-id, and posts refresh tokens
// in request bodies that a 307/308 replays verbatim. Refusing the redirect is
// the only rule that covers all of those without enumerating them.
func refuseCredentialLeakingRedirect(req *http.Request, via []*http.Request) error {
	previous := via[len(via)-1]
	// An http -> https upgrade on the same host is safe; the reverse leaks the
	// token in clear text, and any host change leaks it to a third party.
	if req.URL.Host == previous.URL.Host && (req.URL.Scheme == previous.URL.Scheme || req.URL.Scheme == "https") {
		return nil
	}
	return fmt.Errorf("refusing redirect from %s to %s: request carries provider credentials", previous.URL.Redacted(), req.URL.Redacted())
}
