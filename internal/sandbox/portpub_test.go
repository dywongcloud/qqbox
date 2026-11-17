//go:build darwin || linux || windows

package sandbox

import "testing"

// Real NFTA_RULE_EXPRESSIONS bytes captured (via a debug hook) from the nft and
// iptables-nft binaries building a `-p 8080 -> 10.88.0.5:80` DNAT under laboon.
// Both frontends encode the destination-port match the same way (a transport
// payload load + cmp) and differ only in the target (a native nat statement vs
// an xtables DNAT target carrying an nf_nat_range2). ruleDNAT must extract the
// same publication from each.
const (
	dnatNativeHex = "24000180090001006d6574610000000014000280080002000000001008000100000000012c00018008000100636d700020000280080001000000000108000200000000000c0003800500010006000000340001800c0001007061796c6f6164002400028008000100000000010800020000000002080003000000000208000400000000022c00018008000100636d700020000280080001000000000108000200000000000c000380060001001f9000002c0001800e000100696d6d6564696174650000001800028008000100000000010c000280080001000a5800052c0001800e000100696d6d6564696174650000001800028008000100000000020c000280060001000050000038000180080001006e6174002c00028008000100000000010800020000000002080003000000000108000500000000020800070000000002"
	dnatIptHex    = "340001800c0001007061796c6f6164002400028008000100000000010800020000000001080003000000000908000400000000012c00018008000100636d700020000280080001000000000108000200000000000c0003800500010006000000340001800c0001007061796c6f6164002400028008000100000000010800020000000002080003000000000208000400000000022c00018008000100636d700020000280080001000000000108000200000000000c000380060001001f9000002c0001800c000100636f756e746572001c0002800c00010000000000000000000c00020000000000000000005c0001800b00010074617267657400004c00028009000100444e415400000000080002000000000234000300030000000a5800050000000000000000000000000a580005000000000000000000000000005000500000000000000000"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b := make([]byte, len(s)/2)
	for i := range b {
		var v int
		if _, err := fmtSscan(s[2*i:2*i+2], &v); err != nil {
			t.Fatalf("bad hex: %v", err)
		}
		b[i] = byte(v)
	}
	return b
}

func fmtSscan(s string, v *int) (int, error) {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return 0, errBadHex
		}
		n = n*16 + d
	}
	*v = n
	return 1, nil
}

var errBadHex = errBadHexT("bad hex")

type errBadHexT string

func (e errBadHexT) Error() string { return string(e) }

func TestRuleDNATExtraction(t *testing.T) {
	want := struct {
		hostPort uint16
		dstAddr  uint32
		dstPort  uint16
	}{8080, 10<<24 | 88<<16 | 0<<8 | 5, 80}
	for _, tc := range []struct {
		name string
		hex  string
	}{{"native-nft", dnatNativeHex}, {"iptables-nft", dnatIptHex}} {
		r := &nftRule{exprs: unhex(t, tc.hex)}
		hp, da, dp, ok := ruleDNAT(r)
		if !ok {
			t.Fatalf("%s: ruleDNAT returned ok=false", tc.name)
		}
		if hp != want.hostPort || da != want.dstAddr || dp != want.dstPort {
			t.Fatalf("%s: got hostPort=%d dstAddr=%08x dstPort=%d; want %d %08x %d",
				tc.name, hp, da, dp, want.hostPort, want.dstAddr, want.dstPort)
		}
	}
}
