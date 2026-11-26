package packetmap

import (
	"net/netip"
	"sync"
	"time"

	"tailscale.com/net/packet"
	"tailscale.com/types/ipproto"
)

// Idle timeouts for flows the host opened to a canonical alias. They mirror
// Linux conntrack defaults so a long-lived idle TCP session keeps its address.
const (
	aliasTCPTimeout     = 5 * 24 * time.Hour
	aliasClosingTimeout = 2 * time.Minute
	aliasUDPTimeout     = 3 * time.Minute
	aliasOtherTimeout   = 30 * time.Second
	aliasSweepInterval  = time.Minute
)

type aliasFlowKey struct {
	profileID string
	proto     ipproto.Proto
	peer      netip.Addr
	peerPort  uint16
	localPort uint16
}

// aliasFlows remembers flows the host opened to a peer's canonical address, so
// replies keep that source address instead of the peer's effective address.
// It outlives each immutable Mapper; see InheritFlows.
type aliasFlows struct {
	mu        sync.Mutex
	expires   map[aliasFlowKey]time.Time
	now       func() time.Time
	lastSweep time.Time
}

func newAliasFlows() *aliasFlows {
	return &aliasFlows{expires: map[aliasFlowKey]time.Time{}, now: time.Now}
}

// observe records or refreshes the flow. It reports whether the flow is known
// when create is false.
func (f *aliasFlows) observe(key aliasFlowKey, p *packet.Parsed, create bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if now.Sub(f.lastSweep) >= aliasSweepInterval {
		for k, exp := range f.expires {
			if !now.Before(exp) {
				delete(f.expires, k)
			}
		}
		f.lastSweep = now
	}
	exp, ok := f.expires[key]
	if ok && !now.Before(exp) {
		delete(f.expires, key)
		ok = false
	}
	if !ok && !create {
		return false
	}
	f.expires[key] = now.Add(aliasTimeout(p))
	return true
}

func aliasTimeout(p *packet.Parsed) time.Duration {
	switch p.IPProto {
	case ipproto.TCP:
		if p.TCPFlags&(packet.TCPFin|packet.TCPRst) != 0 {
			return aliasClosingTimeout
		}
		return aliasTCPTimeout
	case ipproto.UDP:
		return aliasUDPTimeout
	default:
		return aliasOtherTimeout
	}
}

// flowPorts returns the peer and local ports for p. ICMP echo uses its
// identifier for both, so concurrent pings stay apart.
func flowPorts(p *packet.Parsed, peer, local netip.AddrPort) (uint16, uint16) {
	if p.IPProto == ipproto.ICMPv4 || p.IPProto == ipproto.ICMPv6 {
		if p.IsEchoRequest() || p.IsEchoResponse() {
			id := uint16(p.EchoIDSeq())
			return id, id
		}
		return 0, 0
	}
	return peer.Port(), local.Port()
}

func outboundAliasKey(profileID string, p *packet.Parsed) aliasFlowKey {
	peerPort, localPort := flowPorts(p, p.Dst, p.Src)
	return aliasFlowKey{profileID: profileID, proto: p.IPProto, peer: p.Dst.Addr(), peerPort: peerPort, localPort: localPort}
}

func inboundAliasKey(profileID string, p *packet.Parsed) aliasFlowKey {
	peerPort, localPort := flowPorts(p, p.Src, p.Dst)
	return aliasFlowKey{profileID: profileID, proto: p.IPProto, peer: p.Src.Addr(), peerPort: peerPort, localPort: localPort}
}
