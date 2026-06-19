package bgp

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"
	"time"
)

func TestSpeakerStartStop(t *testing.T) {
	logger := slog.Default()
	cfg := SpeakerConfig{
		ASN:       64512,
		RouterID:  netip.MustParseAddr("192.0.2.1"),
		Port:      -1, // no TCP listener for unit test
		LocalAddr: netip.MustParseAddr("192.0.2.1"),
	}

	speaker := NewSpeaker(cfg, logger)

	ctx := context.Background()
	if err := speaker.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := speaker.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestSpeakerSetPeers(t *testing.T) {
	logger := slog.Default()
	cfg := SpeakerConfig{
		ASN:       64512,
		RouterID:  netip.MustParseAddr("192.0.2.1"),
		Port:      -1,
		LocalAddr: netip.MustParseAddr("192.0.2.1"),
	}

	speaker := NewSpeaker(cfg, logger)
	ctx := context.Background()
	if err := speaker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer speaker.Stop()

	peers := []PeerConfig{
		{
			ID:       1,
			Address:  netip.MustParseAddr("10.0.0.1"),
			ASN:      65001,
			Password: "",
			Name:     "peer1",
		},
		{
			ID:       2,
			Address:  netip.MustParseAddr("10.0.0.2"),
			ASN:      65002,
			Password: "secret",
			Name:     "peer2",
		},
	}

	speaker.SetPeers(peers)

	// Give peers a moment to start
	time.Sleep(100 * time.Millisecond)

	states := speaker.PeerStates()
	if len(states) != 2 {
		t.Fatalf("peer states len = %d, want 2", len(states))
	}

	// Both peers should be in some state (IDLE or CONNECT since they can't actually connect)
	if state, ok := states["10.0.0.1"]; !ok {
		t.Fatalf("peer 10.0.0.1 not in states: %v", states)
	} else if state != StateIdle {
		// Peer may have transitioned past idle; allowable
		t.Logf("peer 10.0.0.1 state = %s", state)
	}

	if state, ok := states["10.0.0.2"]; !ok {
		t.Fatalf("peer 10.0.0.2 not in states: %v", states)
	} else {
		t.Logf("peer 10.0.0.2 state = %s", state)
	}
}

func TestSpeakerPeerStatesEmpty(t *testing.T) {
	logger := slog.Default()
	cfg := SpeakerConfig{
		ASN:       64512,
		RouterID:  netip.MustParseAddr("192.0.2.1"),
		Port:      -1,
		LocalAddr: netip.MustParseAddr("192.0.2.1"),
	}

	speaker := NewSpeaker(cfg, logger)
	ctx := context.Background()
	if err := speaker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer speaker.Stop()

	states := speaker.PeerStates()
	if len(states) != 0 {
		t.Fatalf("peer states len = %d, want 0", len(states))
	}
}
