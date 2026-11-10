package enclave

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
)

// Relay copies bytes both ways until both directions finish, then closes
// both connections. It does not look at what it copies: everything the host
// relays for a party is TLS that ends inside the enclave.
func Relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	pipe := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		// Pass the end of stream on, so the other side's read returns.
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	go pipe(a, b)
	go pipe(b, a)
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

// Serve accepts connections on ln and relays each to a new connection from
// dial, until ctx ends or ln is closed. logf, if set, is told about dial
// failures.
func Serve(ctx context.Context, ln net.Listener, dial func(ctx context.Context) (net.Conn, error), logf func(format string, args ...any)) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go func() {
			up, err := dial(ctx)
			if err != nil {
				if logf != nil {
					logf("relay from %s: %v", c.RemoteAddr(), err)
				}
				_ = c.Close()
				return
			}
			Relay(c, up)
		}()
	}
}
