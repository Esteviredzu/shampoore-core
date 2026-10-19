package telemost

import "strings"

// mungeSDPAddVideoContent оставлена для совместимости, но больше не
// добавляет a=content:speaker,main: этот атрибут заставляет SFU
// считать поток основным спикером и может блокировать форвардинг
// второго видео в комнате.
func mungeSDPAddVideoContent(sdp string) string {
	return sdp
}

var _ = strings.Split // заглушка, чтобы strings не был неиспользуемым