package telemost

import "github.com/pion/webrtc/v4"

func (s *signaling) ConnectedIPs() []string {
	var ips []string
	seen := map[string]bool{}

	add := func(pc *webrtc.PeerConnection) {
		if pc == nil {
			return
		}
		for _, sender := range pc.GetSenders() {
			if tr := sender.Transport(); tr != nil {
				if ice := tr.ICETransport(); ice != nil {
					if pair, err := ice.GetSelectedCandidatePair(); err == nil && pair != nil && pair.Remote != nil {
						if addr := pair.Remote.Address; addr != "" && !seen[addr] {
							seen[addr] = true
							ips = append(ips, addr)
						}
					}
				}
			}
		}
		for _, receiver := range pc.GetReceivers() {
			if tr := receiver.Transport(); tr != nil {
				if ice := tr.ICETransport(); ice != nil {
					if pair, err := ice.GetSelectedCandidatePair(); err == nil && pair != nil && pair.Remote != nil {
						if addr := pair.Remote.Address; addr != "" && !seen[addr] {
							seen[addr] = true
							ips = append(ips, addr)
						}
					}
				}
			}
		}
	}

	add(s.pubPC)
	add(s.subPC)
	return ips
}

func (p *Pipe) ConnectedIPs() []string {
	if p.sig == nil {
		return nil
	}
	return p.sig.ConnectedIPs()
}