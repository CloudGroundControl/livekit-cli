package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
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

	"github.com/urfave/cli/v2"

	"livekit-cli-cgc/pkg/config"
	"livekit-cli-cgc/pkg/samplebuilder"
	"livekit-cli-cgc/pkg/service"
)

var (
	ProcessCommands = []*cli.Command{
		{
			Name:     "process-track",
			Usage:    "Joins a room as a participant and process video track",
			Action:   processTrack,
			Category: "Process",
			Flags: withDefaultFlags(
				roomFlag,
				identityFlag,
				&cli.StringFlag{
					Name:  "dump",
					Usage: "Particpant ID for extract first video of this participant into jpeg,  video file or forward video",
				},
				&cli.StringFlag{
					Name:  "output-folder",
					Value: "",
					Usage: "output-folder to store jpeg or video file",
				},
				&cli.StringFlag{
					Name:  "forward-folder",
					Value: "",
					Usage: "Forward video as local track to output with latest file in folder",
				},
				&cli.BoolFlag{
					Name:  "outputJpeg",
					Usage: "set to true to output video as list of jpeg file, false to dump/forward video file",
				},
			),
		},
	}
)

func processTrack(c *cli.Context) error {
	pc, err := loadProjectDetails(c)
	if err != nil {
		return err
	}

	var onTrackSubscribedFunc func(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant, lp *lksdk.LocalParticipant, string, roomName string, vehicleID string, outputFolder string, forwardFolder string) error
	var localParticipant *lksdk.LocalParticipant
	roomName := c.String("room")
	outputFolder := c.String("output-folder")
	forwardFolder := c.String("forward-folder")
	token := pc.Token
	WsUrl := pc.URL
	var monitorVehicleID = ""
	if c.String("dump") != "" {
		monitorVehicleID = c.String("dump")
		onTrackSubscribedFunc = onTrackSubscribed
	}
	isOutputJpeg := false
	if c.Bool("outputJpeg") {
		isOutputJpeg = true
	}

	roomCB := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnDataReceived: func(data []byte, rp *lksdk.RemoteParticipant) {
				logger.Infow("received data", "bytes", len(data))
			},
			OnConnectionQualityChanged: func(update *livekit.ConnectionQualityInfo, p lksdk.Participant) {
				logger.Debugw("connection quality changed", "participant", p.Identity(), "quality", update.Quality)
			},
			OnTrackSubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
				logger.Infow("track subscribed", "kind", pub.Kind(), "trackID", pub.SID(), "source", pub.Source())
				if onTrackSubscribedFunc != nil {
					onTrackSubscribedFunc(track, pub, participant, localParticipant, token, roomName, monitorVehicleID, outputFolder, forwardFolder)
				}

			},
			OnTrackUnsubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
				logger.Infow("track unsubscribed", "kind", pub.Kind(), "trackID", pub.SID(), "source", pub.Source())
			},
		},
		OnRoomMetadataChanged: func(metadata string) {
			logger.Infow("room metadata changed", "metadata", metadata)
		},
	}
	var room *lksdk.Room
	identity := c.String("identity")
	if token != "" {
		room, err = lksdk.ConnectToRoomWithToken(pc.URL, pc.Token, roomCB)
		if err != nil {
			return err
		}
		logger.Infow("track ConnectToRoomWithToken", "URL", pc.URL, "Token", pc.Token)
	} else {
		room, err = lksdk.ConnectToRoom(pc.URL, lksdk.ConnectInfo{
			APIKey:              pc.APIKey,
			APISecret:           pc.APISecret,
			RoomName:            roomName,
			ParticipantIdentity: identity,
		}, roomCB)
		if err != nil {
			return err
		}
	}
	localParticipant = room.LocalParticipant
	trackId := ""
	for _, p := range room.GetParticipants() {
		if p.Name() == monitorVehicleID {
			for _, track := range p.Tracks() {
				if track.Kind() == lksdk.TrackKindVideo {
					if rt, ok := track.(*lksdk.RemoteTrackPublication); ok {
						if isOutputJpeg {
							trackId = track.SID()
						} else {
							err := rt.SetSubscribed(true)
							if err != nil {
								return err
							}
						}
					}
				}
			}
		}
	}

	if !isOutputJpeg {
		defer room.Disconnect()
		logger.Infow("connected to room", "room", room.Name())
	} else {
		//room.Disconnect()
		err = dumpWithPipeline(WsUrl, token, roomName, monitorVehicleID, trackId, isOutputJpeg, outputFolder, forwardFolder)
		if err != nil {
			return err
		}
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	<-done
	return nil
}

func onTrackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant, lp *lksdk.LocalParticipant, token string, roomName string, vehicleID string, outputFolder string, forwardFolder string) error {
	fmt.Println("Sub: " + forwardFolder + "name" + outputFolder)
	if rp.Identity() == vehicleID && track.Kind().String() == "video" {
		fileName := ""
		if outputFolder != "" {
			fileName = fmt.Sprintf("%s-%s-%s", rp.Identity(), track.ID(), track.Kind().String())
			fileName = outputFolder + "/" + fileName
			fmt.Println("write track to file ", fileName)
		}

		NewTrackWriter(track, rp.WritePLI, lp, vehicleID, fileName, forwardFolder)
	}
	return nil
}

const (
	maxVideoLate = 1000 // nearly 2s for fhd video
	maxAudioLate = 200  // 4s for audio
)

type TrackWriter struct {
	sb                    *samplebuilder.SampleBuilder
	writer                media.Writer
	track                 *webrtc.TrackRemote
	localVideoTrack       *lksdk.LocalSampleTrack
	lp                    *lksdk.LocalParticipant
	localTrackPublication *lksdk.LocalTrackPublication
	forwardFolder         string
	monitorVehicleId      string
}

func NewTrackWriter(track *webrtc.TrackRemote, pliWriter lksdk.PLIWriter, lp *lksdk.LocalParticipant, monitorVehicleId string, fileName string, forwardFolder string) (*TrackWriter, error) {
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
		forwardFolder:         forwardFolder,
		monitorVehicleId:      monitorVehicleId,
	}
	go t.start()
	return t, nil
}

func getLatestFile(dir string) (rv string) {
	rv = ""
	files, err := ioutil.ReadDir(dir)
	if err == nil {
		var modTime time.Time
		for _, fi := range files {
			if fi.Mode().IsRegular() {
				if !fi.ModTime().Before(modTime) {
					if fi.ModTime().After(modTime) {
						modTime = fi.ModTime()
						rv = dir + "/" + fi.Name()
					}
				}
			}
		}
	}
	return rv
}

func removeFolder(dir string) {
	files, err := ioutil.ReadDir(dir)
	if err == nil {
		for _, fi := range files {
			os.Remove(dir + "/" + fi.Name())
		}
	}
}

type YoloMeta struct {
	Width  int64 `json:"width,omitempty"`
	Height int64 `json:"height,omitempty"`
}

type YoloObject struct {
	Xmin       float64 `json:"xmin,omitempty"`
	Ymin       float64 `json:"ymin,omitempty"`
	Xmax       float64 `json:"xmax,omitempty"`
	Ymax       float64 `json:"ymax,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Clazz      int64   `json:"clazz"`
	Name       string  `json:"name,omitempty"`
}

type Yolo struct {
	ID       string       `json:"id,omitempty"`
	Metadata YoloMeta     `json:"metadata,omitempty"`
	Objects  []YoloObject `json:"objects,omitempty"`
}

func sendJson(lp *lksdk.LocalParticipant, forwardFolder string, monitorVehicleId string) {
	if forwardFolder != "" {
		// poll for json data
		jsonFilename := getLatestFile(forwardFolder)
		if jsonFilename != "" {
			fmt.Println("Json File Read: " + jsonFilename)
			data, err3 := os.ReadFile(jsonFilename)
			if err3 == nil {
				var yoloData Yolo
				err3 = json.Unmarshal(data, &yoloData)
				yoloData.ID = monitorVehicleId
				//PrintJSON(yoloData)
				data, err3 = json.Marshal(yoloData)
				err3 = lp.PublishData(data, livekit.DataPacket_RELIABLE, nil)
			}
			if err3 != nil {
				fmt.Println(err3)
			}
		}
		removeFolder(forwardFolder)
	}
}

func (t *TrackWriter) start() {
	if t.writer != nil {
		defer t.writer.Close()
	}
	errorCount := 0
	rtpRecevied := 0
	timeStamp := time.Now()
	for {
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
					sendJson(t.lp, t.forwardFolder, t.monitorVehicleId)
					rtpRecevied = 0
				} else {
					rtpRecevied++
				}
			}
		} else {
			errorCount++
			if errorCount > 100 {
				fmt.Println("Error over 100")
				break
			}
		}
	}
}

func dumpWithPipeline(WsUrl string, token string, roomName string, vehicleID string, trackId string, outputJpeg bool, outputFolder string, forwardFolder string) error {
	logger.Infow("track unsubscribed", "room", roomName, "trackID", trackId, "outputFolder", outputFolder)
	var req = config.ProcessRequest{
		WsUrl:      WsUrl,
		RoomId:     roomName,
		TrackId:    trackId,
		Token:      token,
		OutputJpeg: outputJpeg,
		Filepath:   outputFolder,
	}

	conf, err := config.NewPipelineConfig(&req)
	if err != nil {
		return err
	}
	logger.Infow("handler launched")

	if conf.TmpDir == "" {
		conf.TmpDir = "/tmp/lk"
	}
	err = os.MkdirAll(conf.TmpDir, 0755)
	if err != nil {
		return err
	}
	_ = os.Setenv("TMPDIR", conf.TmpDir)

	handler, err := service.NewHandler(conf)
	if err != nil {
		return err
	}
	lp := handler.GetPipeline().GetInput().GetRoom().LocalParticipant

	if forwardFolder != "" {
		go func() {
			for {
				sendJson(lp, forwardFolder, vehicleID)
				time.Sleep(time.Millisecond * 15)
			}
		}()
	}

	killChan := make(chan os.Signal, 1)
	signal.Notify(killChan, syscall.SIGINT)

	go func() {
		sig := <-killChan
		logger.Infow("exit requested, stopping recording and shutting down", "signal", sig)
		handler.Kill()
	}()

	return handler.Run()
}
