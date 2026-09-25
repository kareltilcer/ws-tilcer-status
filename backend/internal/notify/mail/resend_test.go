package mail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKey = "re_SHOULD-NEVER-BE-SHOWN"

// fakeResend answers every request with status and body, and records the last
// request it saw.
func fakeResend(t *testing.T, status int, body string, headers map[string]string) (*Resend, *http.Request, *[]byte) {
	t.Helper()
	var (
		seen    http.Request
		payload []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = *r.Clone(context.Background())
		payload, _ = io.ReadAll(r.Body)
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	r := NewResend(testKey)
	r.url = srv.URL
	return r, &seen, &payload
}

func msg() Message {
	return Message{
		From: "status <status@example.test>", To: []string{"a@example.test", "b@example.test"},
		Subject: "[status] Home is down", Text: "text part", HTML: "<p>html part</p>",
		IdempotencyKey: "status-digest-1",
	}
}

func TestResendSendsTheRequestResendExpects(t *testing.T) {
	r, seen, payload := fakeResend(t, 200, `{"id":"49a3999c-0ce1-4ea6-ab68-afcd6dc2e794"}`, nil)
	res, err := r.Send(context.Background(), msg())
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "49a3999c-0ce1-4ea6-ab68-afcd6dc2e794" {
		t.Fatalf("id = %q", res.ID)
	}
	if seen.Method != http.MethodPost || seen.Header.Get("Authorization") != "Bearer "+testKey ||
		seen.Header.Get("Idempotency-Key") != "status-digest-1" ||
		!strings.HasPrefix(seen.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("request = %s %v", seen.Method, seen.Header)
	}
	var body resendRequest
	if err := json.Unmarshal(*payload, &body); err != nil {
		t.Fatal(err)
	}
	if body.From != "status <status@example.test>" || len(body.To) != 2 || body.Subject != "[status] Home is down" ||
		body.Text != "text part" || body.HTML != "<p>html part</p>" {
		t.Fatalf("body = %s", *payload)
	}
}

func TestResendOmitsAnEmptyIdempotencyKey(t *testing.T) {
	r, seen, _ := fakeResend(t, 200, `{"id":"x"}`, nil)
	m := msg()
	m.IdempotencyKey = ""
	if _, err := r.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if _, ok := seen.Header["Idempotency-Key"]; ok {
		t.Fatal("an empty key was sent as a header")
	}
}

func TestResendRefusalsAreTyped(t *testing.T) {
	r, _, _ := fakeResend(t, 422, `{"statusCode":422,"name":"validation_error","message":"Invalid `+"`to`"+` field."}`, nil)
	_, err := r.Send(context.Background(), msg())
	var se *SendError
	if !errors.As(err, &se) || se.Status != 422 || se.Code != "validation_error" || se.Detail != "Invalid `to` field." {
		t.Fatalf("err = %#v", err)
	}
	if !IsPermanent(err) {
		t.Fatal("a validation error must be permanent")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatal("the API key leaked into an error")
	}
}

func TestResendRateLimitCarriesRetryAfter(t *testing.T) {
	r, _, _ := fakeResend(t, 429, `{"name":"rate_limit_exceeded","message":"Too many requests"}`, map[string]string{"Retry-After": "7"})
	_, err := r.Send(context.Background(), msg())
	if IsPermanent(err) || RetryAfterOf(err) != 7*time.Second {
		t.Fatalf("err = %v, retry after %s", err, RetryAfterOf(err))
	}
}

func TestResendErrorBodyIsBounded(t *testing.T) {
	r, _, _ := fakeResend(t, 502, strings.Repeat("x", 5000), nil)
	_, err := r.Send(context.Background(), msg())
	var se *SendError
	if !errors.As(err, &se) || len(se.Detail) > maxErrorBody || IsPermanent(err) {
		t.Fatalf("err = %v (detail %d bytes)", err, len(se.Detail))
	}
}

func TestPermanence(t *testing.T) {
	cases := []struct {
		status int
		code   string
		want   bool
	}{
		{400, "", true},
		{401, "missing_api_key", false}, // fixed in the environment, not in the message
		{403, "validation_error", false},
		{404, "", true},
		{405, "", true},
		{409, "invalid_idempotent_request", true},
		{409, "concurrent_idempotent_requests", false},
		{413, "", true},
		{422, "", true},
		{422, "validation_error", true},
		{422, "invalid_from_address", false}, // STATUS_MAIL_FROM: fixed in the environment too
		{429, "", false},
		{500, "", false},
		{503, "", false},
	}
	for _, c := range cases {
		if got := (&SendError{Status: c.status, Code: c.code}).Permanent(); got != c.want {
			t.Errorf("%d %s: permanent = %t, want %t", c.status, c.code, got, c.want)
		}
	}
	if IsPermanent(errors.New("dial tcp: connection refused")) {
		t.Error("a transport error must never be permanent: the provider may never have seen it")
	}
}

// TestATransportFailureIsTemporaryAndKeyless: a timeout or a refused connection
// says nothing about the message and must be retried; its error names the URL
// and never the key.
func TestATransportFailureIsTemporaryAndKeyless(t *testing.T) {
	stalled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-stalled:
		}
	}))
	defer srv.Close()
	defer close(stalled) // before Close, which waits for the handler
	r := NewResend(testKey)
	r.url = srv.URL
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := r.Send(ctx, msg())
	if err == nil || IsPermanent(err) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatal("the API key leaked into a transport error")
	}
}

func TestAnAcceptedMessageWithAnUnreadableBodyIsStillAccepted(t *testing.T) {
	r, _, _ := fakeResend(t, 200, `not json`, nil)
	if _, err := r.Send(context.Background(), msg()); err != nil {
		t.Fatalf("an accepted message was reported as failed: %v", err)
	}
}
