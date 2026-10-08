package main

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestIsGloballyRoutable(t *testing.T) {
	cases := []struct {
		ip      string
		allowed bool
	}{
		{"127.0.0.1", false},           // loopback
		{"169.254.169.254", false},     // link-local / cloud metadata
		{"10.0.0.1", false},            // RFC1918
		{"172.16.0.5", false},          // RFC1918
		{"192.168.1.1", false},         // RFC1918
		{"0.0.0.0", false},             // unspecified
		{"224.0.0.1", false},           // multicast
		{"::1", false},                 // IPv6 loopback
		{"fe80::1", false},             // IPv6 link-local
		{"fc00::1", false},             // IPv6 unique local
		{"::ffff:127.0.0.1", false},    // IPv4-mapped IPv6 loopback
		{"::ffff:10.0.0.1", false},     // IPv4-mapped IPv6 RFC1918
		{"8.8.8.8", true},              // public IPv4
		{"1.1.1.1", true},              // public IPv4
		{"2606:4700:4700::1111", true}, // public IPv6
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("test bug: %q did not parse as an IP", c.ip)
		}
		if got := isGloballyRoutable(ip); got != c.allowed {
			t.Errorf("isGloballyRoutable(%s) = %v, want %v", c.ip, got, c.allowed)
		}
	}
}

func TestSafeDialerRejectsNonRoutableAddresses(t *testing.T) {
	blocked := []string{
		"127.0.0.1:8443",
		"169.254.169.254:443",
		"10.0.0.1:443",
		"192.168.1.1:443",
		"[::1]:443",
	}
	d := &safeDialer{}
	for _, addr := range blocked {
		_, err := d.DialContext(context.Background(), "tcp", addr)
		if err == nil {
			t.Errorf("DialContext(%q) succeeded; want it blocked", addr)
			continue
		}
		if !errors.Is(err, ErrDestinationNotAllowed) {
			t.Errorf("DialContext(%q) error = %v; want ErrDestinationNotAllowed", addr, err)
		}
	}
}

func TestSafeDialerRejectsMalformedAddress(t *testing.T) {
	d := &safeDialer{}
	if _, err := d.DialContext(context.Background(), "tcp", "not-a-valid-addr"); err == nil {
		t.Error("DialContext with a malformed address should fail")
	}
}
