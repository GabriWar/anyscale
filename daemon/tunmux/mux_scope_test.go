package tunmux

import "testing"

func TestIsLocalScope(t *testing.T) {
	v4 := func(a, b, c, d byte) []byte {
		p := make([]byte, 20)
		p[0] = 0x45
		p[16], p[17], p[18], p[19] = a, b, c, d
		return p
	}
	for _, test := range []struct {
		pkt  []byte
		want bool
	}{
		{v4(239, 255, 255, 250), true}, // SSDP
		{v4(224, 0, 0, 251), true},     // mDNS
		{v4(255, 255, 255, 255), true},
		{v4(169, 254, 1, 1), true},
		{v4(100, 127, 0, 5), false},
		{v4(8, 8, 8, 8), false},
		{[]byte{0x45}, false},
	} {
		if got := isLocalScope(test.pkt); got != test.want {
			t.Errorf("isLocalScope(%v) = %v, want %v", test.pkt[16:], got, test.want)
		}
	}
}
