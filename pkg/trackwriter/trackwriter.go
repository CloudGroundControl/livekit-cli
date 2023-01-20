package trackwriter

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/h264writer"
	"github.com/pion/webrtc/v3/pkg/media/ivfwriter"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"

	"livekit-cli-cgc/pkg/samplebuilder"
	"livekit-cli-cgc/pkg/telemetrydatasender"
)

const (
	maxVideoLate = 2000 // nearly 2s for fhd video
	maxAudioLate = 200  // 4s for audio
)

type TrackWriter struct {
	sb                    *samplebuilder.SampleBuilder
	writer                media.Writer
	track                 *webrtc.TrackRemote
	localVideoTrack       *lksdk.LocalSampleTrack
	lp                    *lksdk.LocalParticipant
	localTrackPublication *lksdk.LocalTrackPublication
	telemetryDataSender   *telemetrydatasender.TelemetryDataSender
	isStart               bool
}

func NewTrackWriter(track *webrtc.TrackRemote, pliWriter lksdk.PLIWriter, lp *lksdk.LocalParticipant, fileName string, telemetryDataSender *telemetrydatasender.TelemetryDataSender) (*TrackWriter, error) {
	var (
		sb                    *samplebuilder.SampleBuilder
		writer                media.Writer
		localVideoTrack       *lksdk.LocalSampleTrack
		localTrackPublication *lksdk.LocalTrackPublication
		err                   error
	)
	switch {
	case strings.EqualFold(track.Codec().MimeType, "video/vp8"):
		sb = samplebuilder.New(maxVideoLate, &codecs.VP8Packet{}, track.Codec().ClockRate, samplebuilder.WithPacketDroppedHandler(func() {
			pliWriter(track.SSRC())
		}))
		if fileName != "" {
			// ivfwriter use frame count as PTS, that might cause video played in a incorrect framerate(fast or slow)
			writer, err = ivfwriter.New(fileName + ".ivf")
		}

	case strings.EqualFold(track.Codec().MimeType, "video/h264"):
		sb = samplebuilder.New(maxVideoLate, &codecs.H264Packet{}, track.Codec().ClockRate, samplebuilder.WithPacketDroppedHandler(func() {
			pliWriter(track.SSRC())
		}))
		if fileName != "" {
			writer, err = h264writer.New(fileName + ".h264")
		}

	case strings.EqualFold(track.Codec().MimeType, "audio/opus"):
		sb = samplebuilder.New(maxAudioLate, &codecs.OpusPacket{}, track.Codec().ClockRate)
		if fileName != "" {
			writer, err = oggwriter.New(fileName+".ogg", 48000, track.Codec().Channels)
		}

	default:
		return nil, errors.New("unsupported codec type")
	}
	if fileName == "" {
		codecC := track.Codec().RTPCodecCapability

		localVideoTrack, _ = lksdk.NewLocalSampleTrack(codecC)
		localTrackPublication, err = lp.PublishTrack(localVideoTrack, &lksdk.TrackPublicationOptions{Name: lp.Name(), Source: livekit.TrackSource_CAMERA, VideoWidth: 1280, VideoHeight: 720})
	}

	if err != nil {
		return nil, err
	}
	t := &TrackWriter{
		sb:                    sb,
		writer:                writer,
		track:                 track,
		localVideoTrack:       localVideoTrack,
		lp:                    lp,
		localTrackPublication: localTrackPublication,
		telemetryDataSender:   telemetryDataSender,
		isStart:               true,
	}
	killChan := make(chan os.Signal, 1)
	signal.Notify(killChan, syscall.SIGINT)
	go func() {
		sig := <-killChan
		logger.Infow("exit requested, stopping recording and shutting down", "signal", sig)
		t.Kill()
	}()
	go t.Start()
	return t, nil
}

func (t *TrackWriter) Kill() {
	t.isStart = false
}
func (t *TrackWriter) Start() {
	if t.writer != nil {
		defer t.writer.Close()
	}
	errorCount := 0
	rtpRecevied := 0
	timeStamp := time.Now()

	for {
		if !t.isStart {
			break
		}
		pkt, _, err := t.track.ReadRTP()
		if err == nil {
			errorCount = 0
			t.sb.Push(pkt)
			if t.writer != nil {
				for _, p := range t.sb.PopPackets() {
					t.writer.WriteRTP(p)
				}
			} else {
				sample := t.sb.Pop()
				opts := &lksdk.SampleWriteOptions{}
				if sample != nil {
					timeStamp = timeStamp.Add(sample.Duration)
					sample.Timestamp = timeStamp
					err2 := t.localVideoTrack.WriteSample(*sample, opts)
					if err2 != nil {
						fmt.Println(err2)
					}
					t.telemetryDataSender.SendJson()
					rtpRecevied = 0
				} else {
					rtpRecevied++
				}
			}
		} else {
			errorCount++
			if errorCount > 100 {
				fmt.Println("Error over 100")
				t.isStart = false
			}
		}
	}
}
