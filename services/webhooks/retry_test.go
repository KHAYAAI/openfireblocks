package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// A receiver that records exactly what arrived, so the tests can assert on
// the bytes and headers rather than on the sender's own account of them.
type recordingReceiver struct {
	mu       sync.Mutex
	bodies   []string
	sigs     []string
	attempts []string
	status   []int // status to return, consumed in order; last value repeats
	calls    int
}

func (r *recordingReceiver) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, string(body))
		r.sigs = append(r.sigs, req.Header.Get("X-Webhook-Signature"))
		r.attempts = append(r.attempts, req.Header.Get("X-Webhook-Attempt"))
		code := r.status[len(r.status)-1]
		if r.calls < len(r.status) {
			code = r.status[r.calls]
		}
		r.calls++
		r.mu.Unlock()
		w.WriteHeader(code)
	}
}

func testWebhook(url string) *Webhook {
	return &Webhook{
		WebhookID:          "11111111-1111-1111-1111-111111111111",
		URL:                url,
		Secret:             "shhh",
		MaxRetries:         3,
		BackoffSeconds:     1,
		ExponentialBackoff: true,
	}
}

func testService() *WebhookService {
	return &WebhookService{httpClient: &http.Client{Timeout: 5 * time.Second}}
}

// A 5xx from the endpoint is the most common webhook failure there is, and
// it was the one that never got retried: the old code scheduled a retry only
// when the request failed at the transport level, and merely *logged*
// "scheduling retry" for an HTTP error status.
func TestNonTransportFailureSchedulesARetry(t *testing.T) {
	rec := &recordingReceiver{status: []int{500}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	svc := testService()
	hook := testWebhook(srv.URL)

	delivery := svc.attemptDelivery(context.Background(), hook, "evt-1", "signing.completed",
		[]byte(`{"event":"one"}`), 1)

	if delivery.Success {
		t.Fatal("a 500 was recorded as a successful delivery")
	}
	if delivery.StatusCode != 500 {
		t.Errorf("status = %d, want 500", delivery.StatusCode)
	}
	if delivery.NextRetryAt == nil {
		t.Fatal("no retry was scheduled for a 500 -- the endpoint error most receivers actually return")
	}
	if !delivery.NextRetryAt.After(time.Now()) {
		t.Errorf("next retry is not in the future: %s", delivery.NextRetryAt)
	}
}

func TestSuccessSchedulesNoRetry(t *testing.T) {
	rec := &recordingReceiver{status: []int{200}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	delivery := testService().attemptDelivery(context.Background(), testWebhook(srv.URL),
		"evt-2", "signing.completed", []byte(`{"event":"two"}`), 1)

	if !delivery.Success {
		t.Fatalf("200 recorded as failure: %s", delivery.ErrorMessage)
	}
	if delivery.NextRetryAt != nil {
		t.Error("a successful delivery scheduled a retry")
	}
}

// The reason the payload is stored rather than the event: the signature is
// over the exact bytes, so a retry that re-marshals could produce a
// different body -- and therefore a signature the receiver rejects, which
// would look like a receiver bug rather than ours.
func TestRetrySendsByteIdenticalPayloadAndSignature(t *testing.T) {
	rec := &recordingReceiver{status: []int{500, 200}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	svc := testService()
	hook := testWebhook(srv.URL)
	payload := []byte(`{"event":"three","nested":{"b":2,"a":1}}`)

	first := svc.attemptDelivery(context.Background(), hook, "evt-3", "settlement.completed", payload, 1)
	if first.Success {
		t.Fatal("expected the first attempt to fail")
	}
	second := svc.attemptDelivery(context.Background(), hook, "evt-3", "settlement.completed",
		first.Payload, first.Attempt+1)
	if !second.Success {
		t.Fatalf("retry failed: %s", second.ErrorMessage)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.bodies) != 2 {
		t.Fatalf("receiver saw %d requests, want 2", len(rec.bodies))
	}
	if rec.bodies[0] != rec.bodies[1] {
		t.Errorf("retry body differs from the original:\n  first: %s\n  retry: %s", rec.bodies[0], rec.bodies[1])
	}
	if rec.sigs[0] != rec.sigs[1] {
		t.Error("retry signature differs from the original; the receiver would reject it")
	}

	// And the signature actually verifies against the shared secret.
	mac := hmac.New(sha256.New, []byte(hook.Secret))
	mac.Write(payload)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if rec.sigs[1] != want {
		t.Errorf("signature = %s, want %s", rec.sigs[1], want)
	}

	// A receiver has to be able to tell a retry from a fresh duplicate.
	if rec.attempts[0] != "1" || rec.attempts[1] != "2" {
		t.Errorf("attempt headers = %v, want [1 2]", rec.attempts)
	}
}

// Backoff has to grow, and has to stay a sane duration. An uncapped shift
// overflows well before a double-digit retry limit and lands the next
// attempt in the past or the far future.
func TestExponentialBackoffGrowsAndStaysBounded(t *testing.T) {
	svc := testService()
	hook := testWebhook("http://127.0.0.1:1")
	hook.MaxRetries = 40
	hook.BackoffSeconds = 2

	// Compared at second resolution: once the cap engages the delay is
	// constant, and time.Until drifts by microseconds between calls, so a
	// strict comparison would fail on the cap working correctly.
	const capHours = 48
	var previous time.Duration
	for attempt := 1; attempt <= 30; attempt++ {
		d := &WebhookDelivery{Attempt: attempt}
		svc.scheduleRetry(d, hook)
		if d.NextRetryAt == nil {
			t.Fatalf("attempt %d scheduled no retry despite being under the limit", attempt)
		}
		delay := time.Until(*d.NextRetryAt).Round(time.Second)
		if delay <= 0 {
			t.Fatalf("attempt %d scheduled a retry in the past (%s) -- backoff overflowed", attempt, delay)
		}
		if delay > capHours*time.Hour {
			t.Fatalf("attempt %d backoff is %s; an uncapped shift has run away", attempt, delay)
		}
		if attempt > 1 && delay < previous {
			t.Fatalf("attempt %d backoff (%s) is shorter than attempt %d's (%s)", attempt, delay, attempt-1, previous)
		}
		previous = delay
	}

	// It must actually have grown, or a constant backoff would pass.
	first := &WebhookDelivery{Attempt: 1}
	svc.scheduleRetry(first, hook)
	if time.Until(*first.NextRetryAt).Round(time.Second) >= previous {
		t.Error("backoff never grew across 30 attempts")
	}
}

func TestNoRetryScheduledAtTheLimit(t *testing.T) {
	svc := testService()
	hook := testWebhook("http://127.0.0.1:1")
	hook.MaxRetries = 3

	d := &WebhookDelivery{Attempt: 3}
	svc.scheduleRetry(d, hook)
	if d.NextRetryAt != nil {
		t.Error("scheduled a retry at the maximum attempt count")
	}
}

// A transport-level failure must still be recorded and retried -- the case
// the old code did handle, kept so the fix does not trade one gap for
// another.
func TestTransportFailureIsRecordedAndRetryScheduled(t *testing.T) {
	svc := testService()
	// Nothing listening on port 1.
	delivery := svc.attemptDelivery(context.Background(), testWebhook("http://127.0.0.1:1"),
		"evt-4", "key.created", []byte(`{}`), 1)

	if delivery.Success {
		t.Fatal("a connection failure was recorded as success")
	}
	if delivery.StatusCode != 0 {
		t.Errorf("status = %d, want 0 for a transport failure", delivery.StatusCode)
	}
	if delivery.ErrorMessage == "" {
		t.Error("no error message recorded")
	}
	if delivery.NextRetryAt == nil {
		t.Error("no retry scheduled for a transport failure")
	}
}

// The payload must be captured on the delivery record, or there is nothing
// to retry with -- which is exactly why retry was unimplementable before.
func TestDeliveryCarriesThePayloadItSent(t *testing.T) {
	rec := &recordingReceiver{status: []int{503}}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	payload := []byte(`{"event":"five"}`)
	delivery := testService().attemptDelivery(context.Background(), testWebhook(srv.URL),
		"evt-5", "compliance.check", payload, 1)

	if string(delivery.Payload) != string(payload) {
		t.Errorf("delivery payload = %q, want %q", delivery.Payload, payload)
	}
}
