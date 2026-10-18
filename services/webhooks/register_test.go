package main

import (
	"testing"
)

func TestValidateWebhookRejectsNonRoutableLiteralIPHosts(t *testing.T) {
	cases := []string{
		"https://127.0.0.1/hook",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.1/hook",
		"https://192.168.1.1:8443/hook",
	}
	for _, u := range cases {
		req := &RegisterWebhookRequest{URL: u, Events: []string{"key.created"}}
		if err := validateWebhook(req); err == nil {
			t.Errorf("validateWebhook(%q) succeeded; want it rejected as non-routable", u)
		}
	}
}

func TestValidateWebhookAcceptsAPublicHttpsHostname(t *testing.T) {
	// A hostname (not a literal IP) always passes this particular check --
	// DNS resolution happens at delivery time, enforced by safeDialer, not
	// here. This only guards against an obvious literal IP in the URL.
	req := &RegisterWebhookRequest{URL: "https://example.com/hook", Events: []string{"key.created"}}
	if err := validateWebhook(req); err != nil {
		t.Errorf("validateWebhook(public hostname) failed: %v", err)
	}
}

func TestValidateWebhookRejectsNonHttps(t *testing.T) {
	req := &RegisterWebhookRequest{URL: "http://example.com/hook", Events: []string{"key.created"}}
	if err := validateWebhook(req); err == nil {
		t.Error("validateWebhook should reject a non-https URL")
	}
}
