package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/livekit/egress/pkg/errors"
	"github.com/livekit/egress/pkg/types"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
)

type SessionLimits struct {
	FileOutputMaxDuration time.Duration `yaml:"file_output_max_duration"`
}

type PipelineConfig struct {
	WsUrl         string `yaml:"ws_url"` // required (env LIVEKIT_WS_URL)
	SessionLimits `yaml:"session_limits"`

	HandlerID  string    `yaml:"handler_id"`
	TmpDir     string    `yaml:"tmp_dir"`
	MutedChan  chan bool `yaml:"-"`
	OutputJpeg bool      `yaml:"-"`

	SourceParams        `yaml:"-"`
	AudioParams         `yaml:"-"`
	VideoParams         `yaml:"-"`
	StreamParams        `yaml:"-"`
	FileParams          `yaml:"-"`
	SegmentedFileParams `yaml:"-"`
	UploadParams        `yaml:"-"`
	types.EgressType    `yaml:"-"`
	types.OutputType    `yaml:"-"`

	GstReady chan struct{}       `yaml:"-"`
	Info     *livekit.EgressInfo `yaml:"-"`

	OnDataReceived func(data []byte, rp *lksdk.RemoteParticipant)
}

type SourceParams struct {
	// source
	Token     string
	APIKey    string
	APISecret string
	Room      string

	// web source
	Display    string
	Layout     string
	CustomBase string
	WebUrl     string

	// sdk source
	TrackID             string
	TrackSource         string
	TrackKind           string
	AudioTrackID        string
	VideoTrackID        string
	ParticipantIdentity string
}

type AudioParams struct {
	AudioEnabled   bool
	AudioCodec     types.MimeType
	AudioBitrate   int32
	AudioFrequency int32
}

type VideoParams struct {
	VideoEnabled     bool
	VideoTranscoding bool
	VideoCodec       types.MimeType
	VideoProfile     types.Profile
	Width            int32
	Height           int32
	Depth            int32
	Framerate        int32
	VideoBitrate     int32
}

type StreamParams struct {
	WebsocketUrl string
	StreamUrls   []string
	StreamInfo   map[string]*livekit.StreamInfo
}

type FileParams struct {
	FileInfo          *livekit.FileInfo
	LocalFilepath     string
	LocalJpegFilepath string
	StorageFilepath   string
}

type SegmentedFileParams struct {
	SegmentsInfo      *livekit.SegmentsInfo
	LocalFilePrefix   string
	StoragePathPrefix string
	PlaylistFilename  string
	SegmentDuration   int
}

type UploadParams struct {
	UploadConfig    interface{}
	DisableManifest bool
}

type ProcessRequest struct {
	room                *lksdk.Room
	roomCB              *lksdk.RoomCallback
	WsUrl               string
	RoomId              string
	ParticipantIdentity string
	TrackId             string
	Token               string
	APIKey              string
	APISecret           string
	Filepath            string
	OutputJpeg          bool
	OnDataReceived      func(data []byte, rp *lksdk.RemoteParticipant)
}

func NewPipelineConfig(req *ProcessRequest) (*PipelineConfig, error) {
	p := &PipelineConfig{
		GstReady: make(chan struct{}),
	}
	fmt.Println("1")

	return p, p.Update(req)
}

func (p *PipelineConfig) Update(request *ProcessRequest) error {
	// start with defaults
	p.OutputJpeg = request.OutputJpeg
	p.OnDataReceived = request.OnDataReceived
	p.Info = &livekit.EgressInfo{
		RoomId: request.RoomId,
		Status: livekit.EgressStatus_EGRESS_STARTING,
	}
	/*
		p.VideoParams = VideoParams{
			VideoProfile: types.ProfileMain,
			Width:        1920,
			Height:       1080,
			Depth:        24,
			Framerate:    30,
			VideoBitrate: 4500,
		}
	*/
	p.VideoParams = VideoParams{
		VideoProfile: types.ProfileMain,
		Width:        640,
		Height:       480,
		Depth:        24,
		Framerate:    30,
		VideoBitrate: 4500,
	}

	p.Info.RoomName = request.RoomId
	p.TrackID = request.TrackId

	if p.TrackID == "" {
		return errors.ErrInvalidInput("track_id")
	}
	p.DisableManifest = true
	if err := p.updateFileParams(request.Filepath); err != nil {
		return err
	}
	if p.Info.RoomName == "" {
		return errors.ErrInvalidInput("room_name")
	}
	// token
	if request.Token != "" {
		p.Token = request.Token
	} else {
		if request.APIKey != "" && request.APISecret != "" {
			p.APIKey = request.APIKey
			p.APISecret = request.APISecret
			p.ParticipantIdentity = request.ParticipantIdentity
			p.Room = request.RoomId
			p.Token = ""
		} else {
			return errors.ErrInvalidInput("token or api key/secret")
		}
	}

	// url
	if request.WsUrl != "" {
		p.WsUrl = request.WsUrl
	} else if p.WsUrl == "" {
		return errors.ErrInvalidInput("ws_url")
	}

	// codec compatibilities
	if p.OutputType != "" {
		// check video codec
		if p.VideoEnabled {
			if p.VideoCodec == "" {
				p.VideoCodec = types.DefaultVideoCodecs[p.OutputType]
			} else if !types.CodecCompatibility[p.OutputType][p.VideoCodec] {
				return errors.ErrIncompatible(p.OutputType, p.VideoCodec)
			}
		}
	}

	return nil
}

func (p *PipelineConfig) updateFileParams(storageFilepath string) error {
	p.EgressType = types.EgressTypeFile
	p.StorageFilepath = storageFilepath
	p.FileInfo = &livekit.FileInfo{}
	p.Info.Result = &livekit.EgressInfo_File{File: p.FileInfo}

	// filename
	identifier, replacements := p.getFilenameInfo()
	if p.OutputType != "" {
		err := p.updateFilepath(identifier, replacements)
		if err != nil {
			return err
		}
	} else {
		p.StorageFilepath = stringReplace(p.StorageFilepath, replacements)
	}

	return nil
}

func (p *PipelineConfig) updateFilepath(identifier string, replacements map[string]string) error {
	p.StorageFilepath = stringReplace(p.StorageFilepath, replacements)

	// get file extension
	ext := types.FileExtensionForOutputType[p.OutputType]

	if p.StorageFilepath == "" || strings.HasSuffix(p.StorageFilepath, "/") {
		// generate filepath
		commonPath := fmt.Sprintf("%s%s-%s", p.StorageFilepath, identifier, time.Now().Format("2006-01-02T150405"))
		p.StorageFilepath = fmt.Sprintf("%s%s", commonPath, ext)
		p.LocalJpegFilepath = fmt.Sprintf("%s%s", commonPath, "jpeg")
	} else if !strings.HasSuffix(p.StorageFilepath, string(ext)) {
		// check for existing (incorrect) extension
		extIdx := strings.LastIndex(p.StorageFilepath, ".")
		if extIdx > 0 {
			existingExt := types.FileExtension(p.StorageFilepath[extIdx:])
			if _, ok := types.FileExtensions[existingExt]; ok {
				p.StorageFilepath = p.StorageFilepath[:extIdx]
			}
		}
		// add file extension
		p.LocalJpegFilepath = p.StorageFilepath[:len(p.StorageFilepath)] + "/jpeg"
		p.StorageFilepath = p.StorageFilepath + string(ext)
	}

	// update filename
	p.FileInfo.Filename = p.StorageFilepath

	// get local filepath
	dir, _ := path.Split(p.StorageFilepath)
	if dir != "" {
		// create local directory
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	// write directly to requested location
	p.LocalFilepath = p.StorageFilepath
	return nil
}

func (p *PipelineConfig) getFilenameInfo() (string, map[string]string) {
	if p.Info.RoomName != "" {
		return p.Info.RoomName, map[string]string{
			"{room_name}": p.Info.RoomName,
			"{room_id}":   p.Info.RoomId,
			"{time}":      time.Now().Format("2006-01-02T150405"),
		}
	}

	return "web", map[string]string{
		"{time}": time.Now().Format("2006-01-02T150405"),
	}
}

// used for sdk input source
func (p *PipelineConfig) UpdateFileInfoFromSDK(fileIdentifier string, replacements map[string]string) error {
	if p.OutputType == "" {
		if !p.VideoEnabled {
			// audio input is always opus
			p.OutputType = types.OutputTypeOGG
		} else {
			p.OutputType = types.OutputTypeMP4
		}
	}

	return p.updateFilepath(fileIdentifier, replacements)
}

func (p *SegmentedFileParams) GetStorageFilepath(filename string) string {
	// Remove any path prepended to the filename
	_, filename = path.Split(filename)

	return path.Join(p.StoragePathPrefix, filename)
}

type Manifest struct {
	EgressID          string `json:"egress_id,omitempty"`
	RoomID            string `json:"room_id,omitempty"`
	RoomName          string `json:"room_name,omitempty"`
	Url               string `json:"url,omitempty"`
	StartedAt         int64  `json:"started_at,omitempty"`
	EndedAt           int64  `json:"ended_at,omitempty"`
	PublisherIdentity string `json:"publisher_identity,omitempty"`
	TrackID           string `json:"track_id,omitempty"`
	TrackKind         string `json:"track_kind,omitempty"`
	TrackSource       string `json:"track_source,omitempty"`
	AudioTrackID      string `json:"audio_track_id,omitempty"`
	VideoTrackID      string `json:"video_track_id,omitempty"`
	SegmentCount      int64  `json:"segment_count,omitempty"`
}

func (p *PipelineConfig) GetManifest() ([]byte, error) {
	manifest := Manifest{
		EgressID:          p.Info.EgressId,
		RoomID:            p.Info.RoomId,
		RoomName:          p.Info.RoomName,
		Url:               p.WebUrl,
		StartedAt:         p.Info.StartedAt,
		EndedAt:           p.Info.EndedAt,
		PublisherIdentity: p.ParticipantIdentity,
		TrackID:           p.TrackID,
		TrackKind:         p.TrackKind,
		TrackSource:       p.TrackSource,
		AudioTrackID:      p.AudioTrackID,
		VideoTrackID:      p.VideoTrackID,
	}
	if p.SegmentsInfo != nil {
		manifest.SegmentCount = p.SegmentsInfo.SegmentCount
	}
	return json.Marshal(manifest)
}

func stringReplace(s string, replacements map[string]string) string {
	for template, value := range replacements {
		s = strings.Replace(s, template, value, -1)
	}
	return s
}
