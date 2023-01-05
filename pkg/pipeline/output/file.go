package output

import (
	"os"

	"github.com/tinyzimmer/go-gst/gst"

	"livekit-cli-cgc/pkg/config"

	"github.com/livekit/egress/pkg/errors"
)

func buildFileOutputBin(p *config.PipelineConfig) (*OutputBin, error) {
	// create elements
	sink, err := gst.NewElement("filesink")
	if err != nil {
		return nil, err
	}
	if err = sink.SetProperty("location", p.LocalFilepath); err != nil {
		return nil, err
	}
	if err = sink.SetProperty("sync", false); err != nil {
		return nil, err
	}

	// create bin
	bin := gst.NewBin("output")
	if err = bin.Add(sink); err != nil {
		return nil, err
	}

	// add ghost pad
	ghostPad := gst.NewGhostPad("sink", sink.GetStaticPad("sink"))
	if !bin.AddPad(ghostPad.Pad) {
		return nil, errors.ErrGhostPadFailed
	}

	return &OutputBin{
		bin: bin,
	}, nil
}

func buildMultiFileOutputBin(p *config.PipelineConfig) (*OutputBin, error) {
	// create elements
	sink, err := gst.NewElement("multifilesink")
	if err != nil {
		return nil, err
	}

	err = os.MkdirAll(p.LocalJpegFilepath, 0755)
	if err != nil {
		return nil, err
	}

	if err = sink.SetProperty("location", p.LocalJpegFilepath+"/img_%07d.jpg"); err != nil {
		return nil, err
	}

	// create bin
	bin := gst.NewBin("output")
	if err = bin.Add(sink); err != nil {
		return nil, err
	}

	// add ghost pad
	ghostPad := gst.NewGhostPad("sink", sink.GetStaticPad("sink"))
	if !bin.AddPad(ghostPad.Pad) {
		return nil, errors.ErrGhostPadFailed
	}

	return &OutputBin{
		bin: bin,
	}, nil
}
