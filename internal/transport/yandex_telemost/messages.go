package telemost

import (
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
)

var capabilitiesOffer = map[string][]string{
	"offerAnswerMode":                       {"SEPARATE"},
	"initialSubscriberOffer":                {"ON_HELLO"},
	"slotsMode":                             {"FROM_CONTROLLER"},
	"simulcastMode":                         {"DISABLED", "STATIC"},
	"selfVadStatus":                         {"FROM_SERVER", "FROM_CLIENT"},
	"dataChannelSharing":                    {"TO_RTP"},
	"videoEncoderConfig":                    {"NO_CONFIG", "ONLY_INIT_CONFIG", "RUNTIME_CONFIG"},
	"dataChannelVideoCodec":                 {"VP8", "UNIQUE_CODEC_FROM_TRACK_DESCRIPTION"},
	"bandwidthLimitationReason":             {"BANDWIDTH_REASON_DISABLED", "BANDWIDTH_REASON_ENABLED"},
	"sdkDefaultDeviceManagement":            {"SDK_DEFAULT_DEVICE_MANAGEMENT_DISABLED", "SDK_DEFAULT_DEVICE_MANAGEMENT_ENABLED"},
	"joinOrderLayout":                       {"JOIN_ORDER_LAYOUT_DISABLED", "JOIN_ORDER_LAYOUT_ENABLED"},
	"pinLayout":                             {"PIN_LAYOUT_DISABLED"},
	"sendSelfViewVideoSlot":                 {"SEND_SELF_VIEW_VIDEO_SLOT_DISABLED", "SEND_SELF_VIEW_VIDEO_SLOT_ENABLED"},
	"serverLayoutTransition":                {"SERVER_LAYOUT_TRANSITION_DISABLED"},
	"sdkPublisherOptimizeBitrate":           {"SDK_PUBLISHER_OPTIMIZE_BITRATE_DISABLED", "SDK_PUBLISHER_OPTIMIZE_BITRATE_FULL", "SDK_PUBLISHER_OPTIMIZE_BITRATE_ONLY_SELF"},
	"sdkNetworkLostDetection":               {"SDK_NETWORK_LOST_DETECTION_DISABLED"},
	"sdkNetworkPathMonitor":                 {"SDK_NETWORK_PATH_MONITOR_DISABLED"},
	"publisherVp9":                          {"PUBLISH_VP9_DISABLED", "PUBLISH_VP9_ENABLED"},
	"svcMode":                               {"SVC_MODE_DISABLED", "SVC_MODE_L3T3", "SVC_MODE_L3T3_KEY"},
	"subscriberOfferAsyncAck":               {"SUBSCRIBER_OFFER_ASYNC_ACK_DISABLED", "SUBSCRIBER_OFFER_ASYNC_ACK_ENABLED"},
	"subscriberDtlsPassiveMode":             {"SUBSCRIBER_DTLS_PASSIVE_MODE_DISABLED", "SUBSCRIBER_DTLS_PASSIVE_MODE_ENABLED"},
	"androidBluetoothRoutingFix":            {"ANDROID_BLUETOOTH_ROUTING_FIX_DISABLED"},
	"fixedIceCandidatesPoolSize":            {"FIXED_ICE_CANDIDATES_POOL_SIZE_DISABLED"},
	"sdkAndroidTelecomIntegration":          {"SDK_ANDROID_TELECOM_INTEGRATION_DISABLED"},
	"setActiveCodecsMode":                   {"SET_ACTIVE_CODECS_MODE_DISABLED", "SET_ACTIVE_CODECS_MODE_VIDEO_ONLY"},
	"publisherOpusDred":                     {"PUBLISHER_OPUS_DRED_DISABLED"},
	"publisherOpusLowBitrate":               {"PUBLISHER_OPUS_LOW_BITRATE_DISABLED"},
	"sdkAndroidDestroySessionOnTaskRemoved": {"SDK_ANDROID_DESTROY_SESSION_ON_TASK_REMOVED_DISABLED"},
	"svcModes":                              {"FALSE"},
	"reportTelemetryModes":                  {"TRUE"},
	"keepDefaultDevicesModes":               {"FALSE"},
}

var startupSlotSizes = [][][2]int{
	{{0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}},
	{{464, 261}, {464, 261}, {464, 261}, {336, 189}, {272, 153}, {272, 153}, {272, 153}, {272, 153}, {224, 126}, {224, 126}, {224, 126}, {224, 126}},
	{{464, 261}, {464, 261}, {464, 261}, {336, 189}, {272, 153}, {272, 153}, {272, 153}, {272, 153}, {224, 126}, {224, 126}, {224, 126}, {224, 126}},
	{{672, 378}, {672, 378}, {464, 261}, {336, 189}, {320, 180}, {320, 180}, {320, 180}, {320, 180}, {272, 153}, {272, 153}, {224, 126}, {224, 126}},
}

func jitterSize(w int, rnd *rand.Rand) (int, int) {
	if w == 0 {
		return 0, 0
	}
	nw := w + rnd.Intn(11) - 5
	return nw, nw * 9 / 16
}

func slotsMessageWithSizes(key int, template [][2]int, rnd *rand.Rand) map[string]any {
	slots := make([]map[string]any, len(template))
	for i, wh := range template {
		w, h := wh[0], wh[1]
		if rnd != nil {
			w, h = jitterSize(wh[0], rnd)
		}
		slots[i] = map[string]any{"width": w, "height": h}
	}
	return map[string]any{
		"uid": uuid.NewString(),
		"setSlots": map[string]any{
			"slots":              slots,
			"audioSlotsCount":    0,
			"key":                key,
			"shutdownAllVideo":   nil,
			"withSelfView":       true,
			"selfViewVisibility": "ON_LOADING_THEN_SHOW",
			"gridConfig":         map[string]any{},
		},
	}
}

func startupSetSlotsMessage(i, key int) map[string]any {
	rnd := rand.New(rand.NewSource(time.Now().UnixNano() + int64(i)))
	return slotsMessageWithSizes(key, startupSlotSizes[i], rnd)
}

func setSlotsMessage(key int) map[string]any {
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	return slotsMessageWithSizes(key, startupSlotSizes[len(startupSlotSizes)-1], rnd)
}

func setSlotsOffsetMessage(offset int) map[string]any {
	return map[string]any{
		"uid":            uuid.NewString(),
		"setSlotsOffset": map[string]any{"offset": offset},
	}
}

func sdkCodecsInfoMessage() map[string]any {
	return map[string]any{
		"uid": uuid.NewString(),
		"sdkCodecsInfo": map[string]any{
			"vp8": map[string]any{
				"supported": "CODEC_FEATURE_SUPPORTED",
				"hwDecode":  "CODEC_FEATURE_NOT_SUPPORTED",
				"hwEncode":  "CODEC_FEATURE_NOT_SUPPORTED",
				"isoString": "vp8",
			},
		},
	}
}

func updatePublisherTrackDescriptionMessage(pc *webrtc.PeerConnection, audioLabel, videoLabel string) map[string]any {
	var descs []map[string]any
	for _, tr := range pc.GetTransceivers() {
		sender := tr.Sender()
		if sender == nil || sender.Track() == nil {
			continue
		}
		kind := "VIDEO"
		label := videoLabel
		groupId := 2
		if sender.Track().Kind().String() == "audio" {
			kind = "AUDIO"
			label = audioLabel
			groupId = 1
		}
		mid := tr.Mid()
		descs = append(descs, map[string]any{
			"mid":            mid,
			"transceiverMid": mid,
			"kind":           kind,
			"priority":       0,
			"label":          label,
			"codecs":         map[string]any{},
			"groupId":        groupId,
			"description":    "",
		})
	}
	return map[string]any{
		"uid": uuid.NewString(),
		"updatePublisherTrackDescription": map[string]any{
			"publisherTrackDescriptions": descs,
		},
	}
}