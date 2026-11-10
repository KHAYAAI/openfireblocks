package enclave

import (
	"context"
	"net"

	"github.com/mdlayher/vsock"
)

// HostCID is the vsock context id of the parent instance, as seen from an
// enclave.
const HostCID = 3

// ListenVsock listens on a vsock port on this side (enclave or host).
func ListenVsock(port uint32) (net.Listener, error) {
	return vsock.Listen(port, nil)
}

// DialVsock connects to a vsock port on another context id.
func DialVsock(ctx context.Context, cid, port uint32) (net.Conn, error) {
	return vsock.DialContext(ctx, cid, port, nil)
}

// DialHost connects from the enclave to a port on the parent instance.
func DialHost(ctx context.Context, port uint32) (net.Conn, error) {
	return DialVsock(ctx, HostCID, port)
}
