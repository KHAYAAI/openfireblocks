package main

import "testing"

func TestAMinorityThresholdIsRefusedBeforeAnyCeremonyStarts(t *testing.T) {
	peers := func(n int) map[int]string {
		m := map[int]string{}
		for i := 1; i <= n; i++ {
			m[i] = "http://x"
		}
		return m
	}
	m := &TSSPartyManager{partyID: 1}
	// threshold is tss-lib's t: t+1 parties sign.
	for _, c := range []struct{ t, n int }{{1, 5}, {1, 4}, {2, 7}} { // 2-of-5, 2-of-4, 3-of-7
		if err := m.StartKeygen("c", c.t, peers(c.n), CurveSecp256k1); err == nil {
			t.Errorf("%d-of-%d was accepted", c.t+1, c.n)
		}
	}
}
