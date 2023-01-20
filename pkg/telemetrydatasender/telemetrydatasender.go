package telemetrydatasender

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
)

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
	Time     int64        `json:"time,omitempty"`
	Metadata YoloMeta     `json:"metadata,omitempty"`
	Objects  []YoloObject `json:"objects,omitempty"`
}

type SubscribeTelemetry struct {
	VehicleID string   `json:"vehicleid,omitempty"`
	Topics    []string `json:"topics,omitempty"`
}

type TelemetryDataSender struct {
	ForwardFolder    string
	outputFolder     string
	MonitorVehicleId string
	destIds          []string
	lp               *lksdk.LocalParticipant
	serviceSid       string
	dataIndex        int
}

func NewTelemetryDataSender(lp *lksdk.LocalParticipant, forwardFolder string, outputFolder string, monitorVehicleId string, serviceSid string, destSid string) (*TelemetryDataSender, error) {
	var destIds []string
	if destSid != "" {
		destIds = append(destIds, destSid)
	}
	os.MkdirAll(outputFolder, 0o755)
	s := &TelemetryDataSender{
		ForwardFolder:    forwardFolder,
		outputFolder:     outputFolder,
		MonitorVehicleId: monitorVehicleId,
		destIds:          destIds,
		lp:               lp,
		serviceSid:       serviceSid,
		dataIndex:        0,
	}
	return s, nil
}

func (s *TelemetryDataSender) ChangeLocalParticipant(lp *lksdk.LocalParticipant) {
	s.lp = lp
}

func (s *TelemetryDataSender) AddDest(rp *lksdk.RemoteParticipant, topics []string) {
	found := false
	for _, id := range s.destIds {
		if id == rp.SID() {
			found = true
			break
		}
	}
	if !found {
		s.destIds = append(s.destIds, rp.SID())
	}
}

func (s *TelemetryDataSender) RemoveDest(rp *lksdk.RemoteParticipant, topics []string) {
	idx := -1
	for i, id := range s.destIds {
		if id == rp.SID() {
			idx = i
			break
		}
	}
	if idx != -1 {
		s.destIds[idx] = s.destIds[len(s.destIds)-1]
		s.destIds = s.destIds[:len(s.destIds)-1]
	}
}

func (s *TelemetryDataSender) Subscribe(vehiclesID string, topics []string) {
	// poll for json data
	var sub SubscribeTelemetry
	sub.VehicleID = vehiclesID
	sub.Topics = topics
	data, _ := json.Marshal(sub)
	//
	var ids []string
	if len(s.serviceSid) > 0 {
		ids = []string{s.serviceSid}
	}
	fmt.Printf("Subscribe to %s %s", s.serviceSid, vehiclesID)
	err := s.lp.PublishData(data, livekit.DataPacket_RELIABLE, ids)
	fmt.Println(err)
}

func (s *TelemetryDataSender) SendJson() {
	// poll for json data
	jsonFilename := getLatestFile(s.ForwardFolder)
	if jsonFilename != "" {
		//fmt.Println("Json File Read: " + jsonFilename)
		data, err3 := os.ReadFile(jsonFilename)
		if err3 == nil && len(s.destIds) > 0 {
			var yoloData Yolo
			err3 = json.Unmarshal(data, &yoloData)
			yoloData.ID = s.MonitorVehicleId
			//PrintJSON(yoloData)
			data, err3 = json.Marshal(yoloData)
			var ids []string
			if (s.destIds)[0] != "broadcast" {
				ids = s.destIds
			}
			//fmt.Printf("ID: %d \n", len(ids))
			err3 = s.lp.PublishData(data, livekit.DataPacket_RELIABLE, ids)
		}
		if err3 != nil {
			fmt.Println(err3)
		}
	}
	removeFolder(s.ForwardFolder)
}

func (s *TelemetryDataSender) StoreJson(recvData []byte) {
	var yoloData Yolo
	err := json.Unmarshal(recvData, &yoloData)
	if err == nil && len(yoloData.Objects) > 1 {
		yoloData.Time = time.Now().UnixMilli()
		data, err3 := json.Marshal(yoloData)
		if err3 == nil {
			filename := fmt.Sprintf("%s/%d.json", s.outputFolder, s.dataIndex)
			s.dataIndex++
			err3 = os.WriteFile(filename, data, 0o644)
		}
		if err3 != nil {
			fmt.Println(err3)
		}
	}
}
