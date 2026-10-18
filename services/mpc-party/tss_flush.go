package main

import (
	tsscommon "github.com/bnb-chain/tss-lib/v2/tss"
)

// relayPending sends every message already queued on outCh, then returns.
//
// A tss-lib party's last round emits a message of its own and then, as soon
// as it has everyone else's, finishes. Both happen on the goroutine that
// delivered the last incoming message, so by the time the result is on the
// end channel that message is already on the out channel. A driver that
// selects between the two picks one at random when both are ready, and if it
// picks the result it returns without ever sending the message. The party
// reports success; its partner waits for ever for a message that was never
// sent. It needs the sender to be a round ahead of the receiver, which is
// timing, so it passed on an idle machine and hung on a loaded one -- and it
// hung most often exactly where recovery runs it: freshly restored parties
// signing straight away.
//
// So a driver that has received its result flushes the out channel first.
func relayPending(outCh <-chan tsscommon.Message, relay func(tsscommon.Message) error) error {
	for {
		select {
		case msg := <-outCh:
			if err := relay(msg); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}
