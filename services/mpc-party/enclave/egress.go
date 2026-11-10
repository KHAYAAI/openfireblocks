package enclave

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Egress sends each outbound connection to a fixed vsock port on the host,
// chosen by the destination's host:port. The host listens on that port and
// relays to that one destination (cmd/mpc-party-host), so the host cannot be
// asked to connect anywhere else, and the enclave refuses a destination that
// is not in the map instead of guessing.
//
// The relay is plain TCP. Whatever runs over it (TLS to a peer, to Vault, to
// KMS) is end to end between the enclave and that destination.
type Egress struct {
	routes map[string]uint32
	dial   func(ctx context.Context, port uint32) (net.Conn, error)
}

// ParseEgress reads a map in the form
//
//	party-2.internal:7000=7102,vault.internal:8200=7200
//
// where each entry is destination host:port = host vsock port.
func ParseEgress(spec string) (map[string]uint32, error) {
	routes := map[string]uint32{}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		dest, portStr, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("enclave egress %q: expected host:port=vsock-port", entry)
		}
		dest = strings.ToLower(strings.TrimSpace(dest))
		host, p, err := net.SplitHostPort(dest)
		if err != nil || host == "" || p == "" {
			return nil, fmt.Errorf("enclave egress %q: destination must be host:port", entry)
		}
		port, err := strconv.ParseUint(strings.TrimSpace(portStr), 10, 32)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("enclave egress %q: %q is not a vsock port", entry, portStr)
		}
		if _, dup := routes[dest]; dup {
			return nil, fmt.Errorf("enclave egress: %s is listed twice", dest)
		}
		routes[dest] = uint32(port)
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("enclave egress: no destinations")
	}
	return routes, nil
}

// NewEgress returns an egress over a dial function, normally DialHost.
func NewEgress(routes map[string]uint32, dial func(ctx context.Context, port uint32) (net.Conn, error)) *Egress {
	return &Egress{routes: routes, dial: dial}
}

// DialContext has the signature http.Transport and net.Dialer use.
func (e *Egress) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("enclave egress: %s connections are not relayed", network)
	}
	port, ok := e.routes[strings.ToLower(addr)]
	if !ok {
		known := make([]string, 0, len(e.routes))
		for k := range e.routes {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("enclave egress: %s is not an allowed destination (allowed: %s)", addr, strings.Join(known, ", "))
	}
	return e.dial(ctx, port)
}
