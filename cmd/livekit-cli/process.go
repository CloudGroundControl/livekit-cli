package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/pion/webrtc/v3"

	"github.com/urfave/cli/v2"

	"livekit-cli-cgc/pkg/config"
	"livekit-cli-cgc/pkg/service"
	"livekit-cli-cgc/pkg/telemetrydatasender"
	"livekit-cli-cgc/pkg/trackwriter"
)

var (
	ProcessCommands = []*cli.Command{
		{
			Name:     "jpeg",
			Usage:    "Joins a room as a participant dump video into jpeg",
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
				&cli.StringFlag{
					Name:  "dest-id",
					Value: "",
					Usage: "participant id to forward the data",
				},
			),
		},
		{
			Name:     "record",
			Usage:    "Joins a room as a participant record into file",
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
					Name:  "subscribe-id",
					Value: "",
					Usage: "service id to subscribe data",
				},
			),
		},
		{
			Name:     "redirect",
			Usage:    "Joins a room as a participant and redirect the video",
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
					Name:  "forward-folder",
					Value: "",
					Usage: "Forward video as local track to output with latest file in folder",
				},
				&cli.StringFlag{
					Name:  "dest-id",
					Value: "",
					Usage: "participant id to forward the data",
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

	var localParticipant *lksdk.LocalParticipant
	var monitorVehicleID = ""
	var telemetryDataSender *telemetrydatasender.TelemetryDataSender
	var outputFolder = ""
	var forwardFolder = ""
	var destName = ""
	var serviceName = ""
	var jsonOutputFolder = ""
	var isOutputJpeg = false
	token := pc.Token
	lkUrl := pc.URL

	roomName := c.String("room")
	identity := c.String("identity")
	if c.String("dump") != "" {
		monitorVehicleID = c.String("dump")
		onTrackSubscribedFunc = onTrackSubscribed
	}

	switch c.Command.Name {
	case "jpeg":
		outputFolder = c.String("output-folder")
		forwardFolder = c.String("forward-folder")
		destName = c.String("dest-id")
		isOutputJpeg = true
	case "record":
		outputFolder = c.String("output-folder")
		serviceName = c.String("subscribe-id")
		jsonOutputFolder = outputFolder + "/json"
	case "redirect":
		forwardFolder = c.String("forward-folder")
		destName = c.String("dest-id")
	}

	trackId := ""
	destId := ""

	roomCB := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnDataReceived: func(data []byte, rp *lksdk.RemoteParticipant) {
				logger.Infow("received data", "bytes", len(data))
				onDataReceivedFunc(data, rp, telemetryDataSender)
			},
			OnConnectionQualityChanged: func(update *livekit.ConnectionQualityInfo, p lksdk.Participant) {
				logger.Debugw("connection quality changed", "participant", p.Identity(), "quality", update.Quality)
			},
			OnTrackSubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
				logger.Infow("track subscribed", "kind", pub.Kind(), "trackID", pub.SID(), "source", pub.Source())
				if onTrackSubscribedFunc != nil {
					onTrackSubscribedFunc(track, pub, participant, localParticipant, token, roomName, monitorVehicleID, outputFolder, telemetryDataSender)
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
			ParticipantName:     identity,
		}, roomCB)
		if err != nil {
			return err
		}
	}
	localParticipant = room.LocalParticipant
	onlineNumber := len(room.GetParticipants())
	if onlineNumber == 0 {
		fmt.Printf("No online in Room %s called\n", room.Name())
	}
	fmt.Printf("Look for Dest ID :%s\n", destName)
	serviceSid := ""
	if destName == "broadcast" {
		destId = "broadcast"
	} else {
		for _, p := range room.GetParticipants() {
			fmt.Printf("Velicles online ID:%s Session:%s\n", p.Name(), p.SID())
			if p.Name() == destName {
				destId = p.SID()
			}
			if p.Name() == serviceName {
				serviceSid = p.SID()
			}
		}
	}
	for _, p := range room.GetParticipants() {
		//fmt.Printf("Velicles ID online:%s \n", p.SID())
		if p.Name() == monitorVehicleID {
			for _, track := range p.Tracks() {
				fmt.Printf("Available Track:%s \n", track.SID())
				if track.Kind() == lksdk.TrackKindVideo {
					if rt, ok := track.(*lksdk.RemoteTrackPublication); ok {
						trackId = track.SID()
						if !isOutputJpeg {
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
	telemetryDataSender, _ = telemetrydatasender.NewTelemetryDataSender(localParticipant, forwardFolder, jsonOutputFolder, monitorVehicleID, serviceSid, destId)
	if serviceSid != "" {
		telemetryDataSender.Subscribe(monitorVehicleID, []string{""})
	}

	if !isOutputJpeg {
		defer room.Disconnect()
		logger.Infow("connected to room", "room", room.Name())
	} else {
		//room.Disconnect()
		var req config.ProcessRequest
		if token != "" {
			req = config.ProcessRequest{
				WsUrl:               lkUrl,
				RoomId:              roomName,
				ParticipantIdentity: identity + "1",
				TrackId:             trackId,
				Token:               token,
				APIKey:              "",
				APISecret:           "",
				OutputJpeg:          isOutputJpeg,
				Filepath:            outputFolder,
				OnDataReceived: func(data []byte, rp *lksdk.RemoteParticipant) {
					onDataReceivedFunc(data, rp, telemetryDataSender)
				},
			}
		} else {
			defer room.Disconnect()
			req = config.ProcessRequest{
				WsUrl:               lkUrl,
				RoomId:              roomName,
				ParticipantIdentity: identity + "1",
				TrackId:             trackId,
				Token:               "",
				APIKey:              pc.APIKey,
				APISecret:           pc.APISecret,
				OutputJpeg:          isOutputJpeg,
				Filepath:            outputFolder,
				OnDataReceived: func(data []byte, rp *lksdk.RemoteParticipant) {
					onDataReceivedFunc(data, rp, telemetryDataSender)
				},
			}
		}
		err = dumpWithPipeline(req, telemetryDataSender)
		if err != nil {
			return err
		}
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	<-done
	return nil
}

var onTrackSubscribedFunc func(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant, lp *lksdk.LocalParticipant, token string, roomName string, vehicleID string, outputFolder string, telemetryDataSender *telemetrydatasender.TelemetryDataSender) error

func onTrackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant, lp *lksdk.LocalParticipant, token string, roomName string, vehicleID string, outputFolder string, telemetryDataSender *telemetrydatasender.TelemetryDataSender) error {
	if rp.Identity() == vehicleID && track.Kind().String() == "video" {
		fileName := ""
		if outputFolder != "" {
			fileName = fmt.Sprintf("%s-%s-%s", rp.Identity(), track.ID(), track.Kind().String())
			fileName = outputFolder + "/" + fileName
			fmt.Println("write track to file ", fileName)
		}

		trackwriter.NewTrackWriter(track, rp.WritePLI, lp, fileName, telemetryDataSender)
	}
	return nil
}

func dumpWithPipeline(req config.ProcessRequest, telemetryDataSender *telemetrydatasender.TelemetryDataSender) error {

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

	telemetryDataSender.ChangeLocalParticipant(handler.GetPipeline().GetInput().GetRoom().LocalParticipant)

	if telemetryDataSender.ForwardFolder != "" {
		go func() {
			for {
				telemetryDataSender.SendJson()
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

const (
	maxVideoLate = 1000 // nearly 2s for fhd video
	maxAudioLate = 200  // 4s for audio
)

func onDataReceivedFunc(recvData []byte, rp *lksdk.RemoteParticipant, telemetryDataSender *telemetrydatasender.TelemetryDataSender) {
	var topics telemetrydatasender.SubscribeTelemetry
	err := json.Unmarshal(recvData, &topics)
	if err == nil && len(topics.VehicleID) > 0 {
		//PrintJSON(topics)
		fmt.Printf("Raw Recv: %s\n", recvData)
		if topics.VehicleID == telemetryDataSender.MonitorVehicleId {
			telemetryDataSender.AddDest(rp, topics.Topics)
		}
	} else {
		telemetryDataSender.StoreJson(recvData)
		//fmt.Printf("Can't parse JSON data error: %s, %s", err, recvData)
	}
}
