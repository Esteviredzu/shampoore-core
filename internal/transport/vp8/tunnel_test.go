package vp8

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func newPC(t *testing.T) *webrtc.PeerConnection {
	t.Helper()
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	return pc
}

func connect(t *testing.T, a, b *webrtc.PeerConnection) {
	t.Helper()

	a.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		_ = b.AddICECandidate(c.ToJSON())
	})
	b.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		_ = a.AddICECandidate(c.ToJSON())
	})

	offer, err := a.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	if err := b.SetRemoteDescription(offer); err != nil {
		t.Fatal(err)
	}

	answer, err := b.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetLocalDescription(answer); err != nil {
		t.Fatal(err)
	}
	if err := a.SetRemoteDescription(answer); err != nil {
		t.Fatal(err)
	}

	connected := make(chan struct{})
	a.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {
			select {
			case <-connected:
			default:
				close(connected)
			}
		}
	})

	select {
	case <-connected:
	case <-time.After(15 * time.Second):
		t.Fatal("PCs did not connect in 15s")
	}
}

func TestTunnelRoundTrip(t *testing.T) {
	a := newPC(t)
	defer a.Close()
	b := newPC(t)
	defer b.Close()

	tunnelA, err := NewTunnel(a)
	if err != nil {
		t.Fatal(err)
	}
	tunnelB, err := NewTunnel(b)
	if err != nil {
		t.Fatal(err)
	}

	connect(t, a, b)

	// Даём WebRTC пару секунд чтобы раскатать media path.
	// Иногда первый sample теряется — потому шлём несколько.
	payload := []byte("hello from A to B")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		for {
			got, err := tunnelB.Receive(ctx)
			if err != nil {
				errCh <- err
				return
			}
			if bytes.Equal(got, payload) {
				done <- got
				return
			}
		}
	}()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatal("timeout waiting for round trip")
		case err := <-errCh:
			t.Fatal(err)
		case got := <-done:
			t.Logf("received: %q", got)
			return
		case <-ticker.C:
			if err := tunnelA.WriteSample(payload); err != nil {
				t.Fatal(err)
			}
		}
	}
}