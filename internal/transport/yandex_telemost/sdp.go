package telemost

import "strings"
func mungeSDPAddVideoContent(sdp string) string {
	lines := strings.Split(sdp, "\r\n")
	out := make([]string, 0, len(lines)+4)
	inVideo := false
	inserted := false
	for _, line := range lines {
		if strings.HasPrefix(line, "m=") {
			if inVideo && !inserted {
				out = append(out, "a=content:speaker,main")
				inserted = true
			}
			inVideo = strings.HasPrefix(line, "m=video")
			inserted = false
		}
		out = append(out, line)
		if inVideo && !inserted && strings.HasPrefix(line, "a=mid:") {
			out = append(out, "a=content:speaker,main")
			inserted = true
		}
	}
	return strings.Join(out, "\r\n")
}