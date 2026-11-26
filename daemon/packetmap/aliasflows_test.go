package packetmap

import (
	"net/netip"
	"testing"
	"time"

	"github.com/gaissmai/bart"
	"tailscale.com/net/packet"
)

func aliasTestMapper() (*Mapper, netip.Addr, netip.Addr, netip.Addr, netip.Addr) {
	effectivePeer := netip.MustParseAddr("100.127.0.28")
	canonicalPeer := netip.MustParseAddr("100.120.159.93")
	hostNAT := netip.MustParseAddr("10.250.0.10")
	canonicalSelf := netip.MustParseAddr("100.64.0.7")
	table := Table{
		Destinations: new(bart.Table[Destination]),
		Sources: map[SourceKey]Source{
			{ProfileID: "csi"}: {HostIP: hostNAT, CanonicalIP: canonicalSelf},
		},
		InboundPeers: map[string]*bart.Table[netip.Addr]{"csi": new(bart.Table[netip.Addr])},
	}
	table.Destinations.Insert(netip.PrefixFrom(effectivePeer, 32), Destination{ProfileID: "csi", CanonicalIP: canonicalPeer})
	table.Destinations.Insert(netip.PrefixFrom(canonicalPeer, 32), Destination{ProfileID: "csi", CanonicalIP: canonicalPeer, Alias: true})
	table.InboundPeers["csi"].Insert(netip.PrefixFrom(canonicalPeer, 32), effectivePeer)
	return New(table), effectivePeer, canonicalPeer, hostNAT, canonicalSelf
}

func replySource(t *testing.T, m *Mapper, pkt []byte) netip.Addr {
	t.Helper()
	translated, err := m.Inbound("csi", pkt)
	if err != nil {
		t.Fatal(err)
	}
	var p packet.Parsed
	p.Decode(translated)
	return p.Src.Addr()
}

func TestAliasDestinationRoutesCanonicalAddress(t *testing.T) {
	m, _, canonicalPeer, hostNAT, canonicalSelf := aliasTestMapper()
	translated, route, err := m.Outbound(udp4(hostNAT, canonicalPeer, 40000, 443))
	if err != nil {
		t.Fatal(err)
	}
	var p packet.Parsed
	p.Decode(translated)
	if route.ProfileID != "csi" || p.Src.Addr() != canonicalSelf || p.Dst.Addr() != canonicalPeer {
		t.Fatalf("outbound = %q %v > %v, want csi %v > %v", route.ProfileID, p.Src.Addr(), p.Dst.Addr(), canonicalSelf, canonicalPeer)
	}
}

func TestAliasReplyKeepsDialedAddress(t *testing.T) {
	m, effectivePeer, canonicalPeer, hostNAT, canonicalSelf := aliasTestMapper()
	if _, _, err := m.Outbound(udp4(hostNAT, canonicalPeer, 40000, 443)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Outbound(udp4(hostNAT, effectivePeer, 40001, 443)); err != nil {
		t.Fatal(err)
	}
	if got := replySource(t, m, udp4(canonicalPeer, canonicalSelf, 443, 40000)); got != canonicalPeer {
		t.Fatalf("reply to canonical flow from %v, want %v", got, canonicalPeer)
	}
	if got := replySource(t, m, udp4(canonicalPeer, canonicalSelf, 443, 40001)); got != effectivePeer {
		t.Fatalf("reply to effective flow from %v, want %v", got, effectivePeer)
	}
	if got := replySource(t, m, udp4(canonicalPeer, canonicalSelf, 5555, 22)); got != effectivePeer {
		t.Fatalf("peer-initiated packet from %v, want %v", got, effectivePeer)
	}
}

func TestAliasEchoReplyKeepsDialedAddress(t *testing.T) {
	m, effectivePeer, canonicalPeer, hostNAT, canonicalSelf := aliasTestMapper()
	echo := func(src, dst netip.Addr, typ packet.ICMP4Type, id uint16) []byte {
		return packet.Generate(packet.ICMP4Header{
			IP4Header: packet.IP4Header{Src: src, Dst: dst},
			Type:      typ,
		}, []byte{byte(id >> 8), byte(id), 0, 1, 'p', 'i', 'n', 'g'})
	}
	if _, _, err := m.Outbound(echo(hostNAT, canonicalPeer, packet.ICMP4EchoRequest, 7)); err != nil {
		t.Fatal(err)
	}
	if got := replySource(t, m, echo(canonicalPeer, canonicalSelf, packet.ICMP4EchoReply, 7)); got != canonicalPeer {
		t.Fatalf("echo reply from %v, want %v", got, canonicalPeer)
	}
	if got := replySource(t, m, echo(canonicalPeer, canonicalSelf, packet.ICMP4EchoReply, 8)); got != effectivePeer {
		t.Fatalf("unrelated echo reply from %v, want %v", got, effectivePeer)
	}
}

func TestAliasFlowsSurviveMapperReplacement(t *testing.T) {
	m, _, canonicalPeer, hostNAT, canonicalSelf := aliasTestMapper()
	if _, _, err := m.Outbound(udp4(hostNAT, canonicalPeer, 40000, 443)); err != nil {
		t.Fatal(err)
	}
	next, _, _, _, _ := aliasTestMapper()
	next.InheritFlows(m)
	if got := replySource(t, next, udp4(canonicalPeer, canonicalSelf, 443, 40000)); got != canonicalPeer {
		t.Fatalf("reply after mapper swap from %v, want %v", got, canonicalPeer)
	}
}

func TestAliasFlowExpires(t *testing.T) {
	m, effectivePeer, canonicalPeer, hostNAT, canonicalSelf := aliasTestMapper()
	now := time.Unix(1000, 0)
	m.flows.now = func() time.Time { return now }
	if _, _, err := m.Outbound(udp4(hostNAT, canonicalPeer, 40000, 443)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(aliasUDPTimeout - time.Second)
	if got := replySource(t, m, udp4(canonicalPeer, canonicalSelf, 443, 40000)); got != canonicalPeer {
		t.Fatalf("reply before timeout from %v, want %v", got, canonicalPeer)
	}
	now = now.Add(aliasUDPTimeout + time.Second)
	if got := replySource(t, m, udp4(canonicalPeer, canonicalSelf, 443, 40000)); got != effectivePeer {
		t.Fatalf("reply after timeout from %v, want %v", got, effectivePeer)
	}
	if len(m.flows.expires) != 0 {
		t.Fatalf("expired flows retained: %v", m.flows.expires)
	}
}
