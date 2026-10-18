package main

import (
	"context"
	"fmt"
	"net"
)

// SSRF protection for webhook URLs.
//
// validateWebhook (register.go) checked only that a URL was https with a
// non-empty host. A pentest registered webhooks pointing at
// https://127.0.0.1, https://169.254.169.254 (cloud instance metadata),
// an RFC1918 address and an internal cluster DNS name, and the webhooks
// service dialed every one of them itself: loopback refused in ~3ms,
// link-local/RFC1918 hit the client timeout, and the internal resolver
// answered for the internal service name. The delivery log returned to
// the registering tenant exposed the exact transport error and timing for
// each, turning an authenticated tenant API key into a port scanner and
// DNS probe against this platform's own network.
//
// Fixed at the only point that cannot be bypassed by DNS trickery: the
// dialer itself, right before it connects. A URL that resolves to a
// public address at registration time and a private one at delivery time
// (DNS rebinding) is still caught, because this runs on every dial, not
// once at registration.
func isGloballyRoutable(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// IPv4-mapped IPv6 (::ffff:10.0.0.1 etc.) must be judged by the
	// embedded IPv4 address, or a private v4 address smuggled in v6 form
	// would pass. To4 returns the address itself, unchanged, when it is
	// already a 4-byte (or already-unmapped) form -- it does not recurse
	// -- so normalising once here and never calling back into this
	// function is both correct and the only way that is not infinite.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() ||
		ip.IsPrivate() {
		return false
	}
	return true
}

// ErrDestinationNotAllowed is returned (wrapped) when a webhook URL
// resolves to a non-public address.
var ErrDestinationNotAllowed = fmt.Errorf("destination is not a publicly routable address")

// safeDialer wraps a net.Dialer so every connection it makes -- the
// initial request and the dial behind any redirect the caller chooses to
// follow -- is checked against the resolved IP, not the hostname a DNS
// response could change between check and connect.
type safeDialer struct {
	inner net.Dialer
}

func (d *safeDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolving %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%q did not resolve to any address", host)
	}
	for _, ip := range ips {
		if !isGloballyRoutable(ip.IP) {
			return nil, fmt.Errorf("%s (resolved from %q): %w", ip.IP, host, ErrDestinationNotAllowed)
		}
	}
	// Dial the address this resolution actually checked, by IP, so a
	// second resolution inside the dialer cannot return something
	// different from what was just verified (a classic TOCTOU/DNS
	// rebinding path if the dialer were instead given the hostname again).
	return d.inner.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}
