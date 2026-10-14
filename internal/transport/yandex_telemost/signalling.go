package telemost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport/vp8"
)

const (
	wsPingPeriod     = 5 * time.Second
	pcConnectTimeout = 20 * time.Second
)

type signaling struct {
	cfg    *Config
	boot   *bootstrapResult
	logger logging.ContextLogger

	ws   *websocket.Conn
	wsMu sync.Mutex

	pubPC *webrtc.PeerConnection
	subPC *webrtc.PeerConnection

	sampleTrack *webrtc.TrackLocalStaticSample
	audioTrack  *webrtc.TrackLocalStaticRTP
	tunnel      *vp8.Tunnel

	pubRemoteSet bool
	subRemoteSet bool
	pubPending   []webrtc.ICECandidateInit
	subPending   []webrtc.ICECandidateInit
	remoteMu     sync.Mutex

	pubSeq       int
	subSeq       int
	slotsKey     int
	pubReadySent bool
	subReadySent bool
	readyMu      sync.Mutex

	onPublishReady func()
	onSubscribe    func()

	closeOnce sync.Once
	closed    chan struct{}
}

func newSignaling(cfg *Config, boot *bootstrapResult, logger logging.ContextLogger) *signaling {
	return &signaling{
		cfg:    cfg,
		boot:   boot,
		logger: logger,
		closed: make(chan struct{}),
	}
}

func (s *signaling) Tunnel() *vp8.Tunnel { return s.tunnel }

func (s *signaling) connectWS(ctx context.Context) error {
	header := http.Header{}
	header.Set("User-Agent", userAgent)
	header.Set("Origin", pageOrigin)
	header.Set("Referer", pageOrigin+"/")

	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Jar:              s.boot.jar,
	}

	conn, _, err := dialer.DialContext(ctx, s.boot.info.MediaServerURL, header)
	if err != nil {
		return fmt.Errorf("telemost: ws dial: %w", err)
	}

	s.wsMu.Lock()
	s.ws = conn
	s.wsMu.Unlock()
	return nil
}

func (s *signaling) closeWS() {
	s.wsMu.Lock()
	ws := s.ws
	s.ws = nil
	s.wsMu.Unlock()
	if ws != nil {
		_ = ws.Close()
	}
}

func (s *signaling) wsSend(v any) error {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()

	if s.ws == nil {
		return fmt.Errorf("telemost: ws not connected")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.ws.WriteMessage(websocket.TextMessage, data)
}

func (s *signaling) Run(ctx context.Context) error {
	if err := s.connectWS(ctx); err != nil {
		return err
	}
	defer s.closeWS()

	if err := s.sendHello(); err != nil {
		return fmt.Errorf("telemost: send hello: %w", err)
	}

	pingCtx, cancelPing := context.WithCancel(ctx)
	defer cancelPing()
	go s.pingLoop(pingCtx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.closed:
			return nil
		default:
		}

		s.wsMu.Lock()
		ws := s.ws
		s.wsMu.Unlock()
		if ws == nil {
			return fmt.Errorf("telemost: ws closed")
		}

		_, raw, err := ws.ReadMessage()
		if err != nil {
			return fmt.Errorf("telemost: ws read: %w", err)
		}
		if err := s.handleMessage(raw); err != nil {
			s.logger.Warn("telemost: handle message: ", err)
		}
	}
}

func (s *signaling) pingLoop(ctx context.Context) {
	t := time.NewTicker(wsPingPeriod)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = s.wsSend(map[string]any{
				"uid":  newUUID(),
				"ping": map[string]any{},
			})
		}
	}
}

func (s *signaling) sendHello() error {
	hello := map[string]any{
		"uid": newUUID(),
		"hello": map[string]any{
			"participantMeta": map[string]any{
				"name":        s.cfg.EffectiveDisplayName(),
				"role":        "SPEAKER",
				"description": "",
				"sendAudio":   false,
				"sendVideo":   true,
			},
			"participantAttributes": map[string]any{
				"name":        s.cfg.EffectiveDisplayName(),
				"role":        "SPEAKER",
				"description": "",
			},
			"sendAudio":             false,
			"sendVideo":             true,
			"sendSharing":           false,
			"participantId":         s.boot.info.PeerID,
			"roomId":                s.boot.info.RoomID,
			"serviceName":           s.boot.info.ServiceName,
			"credentials":           s.boot.info.Credentials,
			"capabilitiesOffer":     capabilitiesOffer,
			"sdkInfo": map[string]any{
				"implementation": "browser",
				"version":        s.cfg.SDKVersion,
				"userAgent":      userAgent,
				"hwConcurrency":  8,
			},
			"sdkInitializationId":    newUUID(),
			"disablePublisher":       false,
			"disableSubscriber":      false,
			"disableSubscriberAudio": false,
		},
	}
	return s.wsSend(hello)
}

func (s *signaling) handleMessage(raw []byte) error {
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		return err
	}
	uid, _ := msg["uid"].(string)

	for k := range msg {
		if k == "uid" || k == "ack" || k == "ping" {
			continue
		}
		s.logger.Debug("telemost: <- ", k)
		break
	}

	switch {
	case msg["serverHello"] != nil:
		s.ack(uid)
		return s.onServerHello(msg["serverHello"])

	case msg["publisherSdpAnswer"] != nil:
		s.ack(uid)
		return s.onPublisherAnswer(msg["publisherSdpAnswer"])

	case msg["subscriberSdpOffer"] != nil:
		s.ack(uid)
		return s.onSubscriberOffer(msg["subscriberSdpOffer"])

	case msg["webrtcIceCandidate"] != nil:
		s.ack(uid)
		return s.onICECandidate(msg["webrtcIceCandidate"])

	case msg["ack"] != nil:
		return nil

	case msg["ping"] != nil:
		return nil
	}

	if uid != "" {
		s.ack(uid)
	}
	return nil
}

func (s *signaling) ack(uid string) {
	if uid == "" {
		return
	}
	_ = s.wsSend(map[string]any{
		"uid": uid,
		"ack": map[string]any{
			"status": map[string]any{
				"code":        "OK",
				"description": "",
			},
		},
	})
}

func (s *signaling) onServerHello(raw any) error {
	m, _ := raw.(map[string]any)
	if m == nil {
		return nil
	}

	ice := s.boot.info.ICEServers

	if rtcCfg, ok := m["rtcConfiguration"].(map[string]any); ok {
		if servers, ok := rtcCfg["iceServers"].([]any); ok && len(servers) > 0 {
			var parsed []ICEServer
			for _, srv := range servers {
				sm, _ := srv.(map[string]any)
				if sm == nil {
					continue
				}
				var urls []string
				if u, ok := sm["urls"].([]any); ok {
					for _, item := range u {
						if str, ok := item.(string); ok {
							urls = append(urls, str)
						}
					}
				}
				if len(urls) == 0 {
					continue
				}
				entry := ICEServer{URLs: urls}
				if u, ok := sm["username"].(string); ok && u != "" {
					entry.Username = u
					entry.Credential, _ = sm["credential"].(string)
				}
				parsed = append(parsed, entry)
			}
			if len(parsed) > 0 {
				ice = parsed
			}
		}
	}

	_ = s.wsSend(setSlotsOffsetMessage(0))

	return s.initPeerConnections(ice)
}

func (s *signaling) initPeerConnections(ice []ICEServer) error {
	cfg := webrtc.Configuration{}
	for _, srv := range ice {
		entry := webrtc.ICEServer{URLs: srv.URLs}
		if srv.Username != "" {
			entry.Username = srv.Username
			entry.Credential = srv.Credential
		}
		cfg.ICEServers = append(cfg.ICEServers, entry)
	}

	pubPC, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return err
	}
	subPC, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		_ = pubPC.Close()
		return err
	}

	s.pubPC = pubPC
	s.subPC = subPC

	// AUDIO (Opus) — обязателен для SFU.
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "shampoore-audio",
	)
	if err != nil {
		_ = pubPC.Close()
		_ = subPC.Close()
		return err
	}
	audioTr, err := pubPC.AddTransceiverFromTrack(
		audioTrack,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly},
	)
	if err != nil {
		_ = pubPC.Close()
		_ = subPC.Close()
		return err
	}
	go drainSenderRTCP(audioTr.Sender())
	s.audioTrack = audioTrack

	// VIDEO (VP8) — основной канал.
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		vp8.TrackID, vp8.StreamID,
	)
	if err != nil {
		_ = pubPC.Close()
		_ = subPC.Close()
		return err
	}
	videoTr, err := pubPC.AddTransceiverFromTrack(
		track,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly},
	)
	if err != nil {
		_ = pubPC.Close()
		_ = subPC.Close()
		return err
	}
	go drainSenderRTCP(videoTr.Sender())
	s.sampleTrack = track

	tunnel, err := vp8.AttachTunnel(pubPC, subPC, track)
	if err != nil {
		_ = pubPC.Close()
		_ = subPC.Close()
		return err
	}
	s.tunnel = tunnel

	pubPC.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		s.sendICE(c, "PUBLISHER", s.pubSeq)
	})
	subPC.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		s.sendICE(c, "SUBSCRIBER", s.subSeq)
	})

	pubPC.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		s.logger.Info("telemost: pub ICE state = ", state.String())
	})
	subPC.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		s.logger.Info("telemost: sub ICE state = ", state.String())
	})

	pubPC.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		s.logger.Info("telemost: pub PC state = ", state.String())
		if state == webrtc.PeerConnectionStateConnected {
			s.markPublishReady()
		}
	})
	subPC.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		s.logger.Info("telemost: sub PC state = ", state.String())
		if state == webrtc.PeerConnectionStateConnected {
			s.markSubscribeReady()
		}
	})

	return s.startPublish()
}

func drainSenderRTCP(sender *webrtc.RTPSender) {
	if sender == nil {
		return
	}
	buf := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buf); err != nil {
			return
		}
	}
}

func (s *signaling) markPublishReady() {
	s.readyMu.Lock()
	if s.pubReadySent {
		s.readyMu.Unlock()
		return
	}
	s.pubReadySent = true
	cb := s.onPublishReady
	s.readyMu.Unlock()

	s.logger.Info("telemost: publish connected")
	if cb != nil {
		cb()
	}
}

func (s *signaling) markSubscribeReady() {
	s.readyMu.Lock()
	if s.subReadySent {
		s.readyMu.Unlock()
		return
	}
	s.subReadySent = true
	cb := s.onSubscribe
	s.readyMu.Unlock()

	s.logger.Info("telemost: subscribe connected")
	if cb != nil {
		cb()
	}
}

func (s *signaling) startPublish() error {
	s.pubSeq = 1
	offer, err := s.pubPC.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := s.pubPC.SetLocalDescription(offer); err != nil {
		return err
	}

	// ГЛАВНОЕ: мутируем SDP перед отправкой — добавляем a=content:speaker,main.
	offer.SDP = mungeSDPAddVideoContent(offer.SDP)

	videoMids, audioMids := parseMids(offer.SDP)

	var tracks []map[string]any
	if len(audioMids) > 0 {
		tracks = append(tracks, map[string]any{
			"mid":            audioMids[0],
			"transceiverMid": audioMids[0],
			"kind":           "AUDIO",
			"priority":       0,
			"label":          "",
			"codecs":         map[string]any{},
			"groupId":        1,
			"description":    "",
		})
	}
	if len(videoMids) > 0 {
		tracks = append(tracks, map[string]any{
			"mid":            videoMids[0],
			"transceiverMid": videoMids[0],
			"kind":           "VIDEO",
			"priority":       0,
			"label":          "",
			"codecs":         map[string]any{},
			"groupId":        1,
			"description":    "",
		})
	}

	s.logger.Debug("telemost: -> publisherSdpOffer pcSeq=", s.pubSeq,
		" tracks=", len(tracks), " audioMids=", audioMids, " videoMids=", videoMids)

	return s.wsSend(map[string]any{
		"uid": newUUID(),
		"publisherSdpOffer": map[string]any{
			"pcSeq":  s.pubSeq,
			"sdp":    offer.SDP,
			"tracks": tracks,
		},
	})
}

func (s *signaling) onPublisherAnswer(raw any) error {
	m, _ := raw.(map[string]any)
	sdp, _ := m["sdp"].(string)
	if sdp == "" {
		return fmt.Errorf("telemost: empty SDP in publisherSdpAnswer")
	}

	if err := s.pubPC.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	}); err != nil {
		return err
	}

	s.remoteMu.Lock()
	s.pubRemoteSet = true
	pending := s.pubPending
	s.pubPending = nil
	s.remoteMu.Unlock()

	for _, c := range pending {
		_ = s.pubPC.AddICECandidate(c)
	}

	// sdkCodecsInfo, updatePublisherTrackDescription, setSlots-ramp
	s.wsSend(sdkCodecsInfoMessage())
	s.wsSend(updatePublisherTrackDescriptionMessage(
		s.pubPC,
		"Microphone",
		"MacBook Pro Camera (0000:0001)",
	))

	for i := 0; i < 4; i++ {
		s.slotsKey++
		key := s.slotsKey
		s.wsSend(startupSetSlotsMessage(i, key))
		s.logger.Debug("telemost: -> setSlots key=", key, " startup=", i+1)
	}

	return nil
}

func (s *signaling) onSubscriberOffer(raw any) error {
	m, _ := raw.(map[string]any)
	sdp, _ := m["sdp"].(string)
	if sdp == "" {
		return fmt.Errorf("telemost: empty SDP in subscriberSdpOffer")
	}
	if pcSeq, ok := m["pcSeq"].(float64); ok {
		s.subSeq = int(pcSeq)
	}

	if err := s.subPC.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdp,
	}); err != nil {
		return err
	}

	s.remoteMu.Lock()
	s.subRemoteSet = true
	pending := s.subPending
	s.subPending = nil
	s.remoteMu.Unlock()

	for _, c := range pending {
		_ = s.subPC.AddICECandidate(c)
	}

	answer, err := s.subPC.CreateAnswer(nil)
	if err != nil {
		return err
	}
	if err := s.subPC.SetLocalDescription(answer); err != nil {
		return err
	}
	return s.wsSend(map[string]any{
		"uid": newUUID(),
		"subscriberSdpAnswer": map[string]any{
			"sdp":   answer.SDP,
			"pcSeq": s.subSeq,
		},
	})
}

func (s *signaling) onICECandidate(raw any) error {
	m, _ := raw.(map[string]any)
	cand, _ := m["candidate"].(string)
	mid, _ := m["sdpMid"].(string)
	target, _ := m["target"].(string)
	idxF, _ := m["sdpMlineIndex"].(float64)
	idx := uint16(idxF)

	init := webrtc.ICECandidateInit{
		Candidate:     cand,
		SDPMid:        &mid,
		SDPMLineIndex: &idx,
	}

	s.remoteMu.Lock()
	defer s.remoteMu.Unlock()

	switch target {
	case "PUBLISHER":
		if !s.pubRemoteSet {
			s.pubPending = append(s.pubPending, init)
			return nil
		}
		return s.pubPC.AddICECandidate(init)
	case "SUBSCRIBER":
		if !s.subRemoteSet {
			s.subPending = append(s.subPending, init)
			return nil
		}
		return s.subPC.AddICECandidate(init)
	}
	return nil
}

func (s *signaling) sendICE(c *webrtc.ICECandidate, target string, pcSeq int) {
	jc := c.ToJSON()
	var mid string
	if jc.SDPMid != nil {
		mid = *jc.SDPMid
	}
	var idx uint16
	if jc.SDPMLineIndex != nil {
		idx = *jc.SDPMLineIndex
	}
	_ = s.wsSend(map[string]any{
		"uid": newUUID(),
		"webrtcIceCandidate": map[string]any{
			"candidate":           jc.Candidate,
			"sdpMid":              mid,
			"sdpUsernameFragment": ufragFromCandidate(jc.Candidate),
			"sdpMlineIndex":       idx,
			"target":              target,
			"pcSeq":               pcSeq,
		},
	})
}

func (s *signaling) Close() {
	s.closeOnce.Do(func() { close(s.closed) })
	s.closeWS()
	if s.pubPC != nil {
		_ = s.pubPC.Close()
	}
	if s.subPC != nil {
		_ = s.subPC.Close()
	}
}

func ufragFromCandidate(c string) string {
	parts := strings.Fields(c)
	for i, p := range parts {
		if p == "ufrag" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func parseMids(sdp string) (video []string, audio []string) {
	var media string
	for _, line := range strings.Split(sdp, "\r\n") {
		switch {
		case strings.HasPrefix(line, "m=audio"):
			media = "audio"
		case strings.HasPrefix(line, "m=video"):
			media = "video"
		case strings.HasPrefix(line, "m="):
			media = ""
		case strings.HasPrefix(line, "a=mid:"):
			mid := strings.TrimPrefix(line, "a=mid:")
			switch media {
			case "audio":
				audio = append(audio, mid)
			case "video":
				video = append(video, mid)
			}
		}
	}
	return video, audio
}