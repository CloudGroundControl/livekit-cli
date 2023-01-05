package input

import (
	"context"

	"github.com/tinyzimmer/go-gst/gst"

	"livekit-cli-cgc/pkg/pipeline/input/sdk"

	"livekit-cli-cgc/pkg/config"

	lksdk "github.com/livekit/server-sdk-go"
)

type Input interface {
	GetRoom() *lksdk.Room
	Bin() *gst.Bin
	Element() *gst.Element
	Link() error
	StartRecording() chan struct{}
	EndRecording() chan struct{}
	Close()
}

func New(ctx context.Context, p *config.PipelineConfig) (Input, error) {
	return sdk.NewSDKInput(ctx, p)
}
