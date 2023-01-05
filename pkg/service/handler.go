package service

import (
	"context"
	"errors"
	"fmt"

	//	"google.golang.org/grpc"
	//	"google.golang.org/protobuf/proto"

	// "livekit-cli-cgc/pkg/config"
	//	"github.com/livekit/egress/pkg/errors"
	//	"github.com/livekit/protocol/egress"
	"livekit-cli-cgc/pkg/config"
	"livekit-cli-cgc/pkg/pipeline"

	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/protocol/tracer"
)

const network = "unix"

type Handler struct {
	//	ipc.UnimplementedEgressHandlerServer

	conf *config.PipelineConfig
	//	rpcServer     egress.RPCServer
	//	grpcServer    *grpc.Server
	kill          chan struct{}
	debugRequests chan chan pipelineDebugResponse
	pipeline      *pipeline.Pipeline
}

type pipelineDebugResponse struct {
	dot string
	err error
}

/*
func NewHandler(conf *config.PipelineConfig, rpcServer egress.RPCServer) (*Handler, error) {
	h := &Handler{
		conf:          conf,
//		rpcServer:     rpcServer,
//		grpcServer:    grpc.NewServer(),
		kill:          make(chan struct{}),
		debugRequests: make(chan chan pipelineDebugResponse),
	}

	listener, err := net.Listen(network, getSocketAddress(conf.TmpDir))
	if err != nil {
		return nil, err
	}

	ipc.RegisterEgressHandlerServer(h.grpcServer, h)

	go func() {
		err := h.grpcServer.Serve(listener)
		if err != nil {
			logger.Errorw("failed to start grpc handler", err)
		}
	}()

	return h, nil
}
*/

func NewHandler(conf *config.PipelineConfig) (*Handler, error) {
	ctx, span := tracer.Start(context.Background(), "Handler.Run")
	defer span.End()
	h := &Handler{
		conf:          conf,
		kill:          make(chan struct{}),
		debugRequests: make(chan chan pipelineDebugResponse),
	}

	fmt.Println("1")
	p, err := h.buildPipeline(ctx)

	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	fmt.Println("build")

	h.pipeline = p

	return h, nil
}
func (h *Handler) Run() error {
	ctx, _ := tracer.Start(context.Background(), "Handler.Run")
	// start egress
	result := make(chan *livekit.EgressInfo, 1)
	go func() {
		result <- h.pipeline.Run(ctx)
	}()

	for {
		select {
		case <-h.kill:
			// kill signal received
			h.pipeline.SendEOS(ctx)

		case res := <-result:
			// recording finished
			h.sendUpdate(ctx, res)
			return nil
		case debugResponseChan := <-h.debugRequests:
			dot, err := h.pipeline.GetGstPipelineDebugDot()
			select {
			case debugResponseChan <- pipelineDebugResponse{dot: dot, err: err}:
			default:
				logger.Debugw("unable to return gstreamer debug dot file to caller")
			}
		}

	}
}

func (h *Handler) buildPipeline(ctx context.Context) (*pipeline.Pipeline, error) {
	ctx, span := tracer.Start(ctx, "Handler.buildPipeline")
	defer span.End()

	// build/verify params
	p, err := pipeline.New(ctx, h.conf)
	if err != nil {
		h.conf.Info.Error = err.Error()
		h.conf.Info.Status = livekit.EgressStatus_EGRESS_FAILED
		h.sendUpdate(ctx, h.conf.Info)
		span.RecordError(err)
		return nil, err
	}

	p.OnStatusUpdate(h.sendUpdate)
	return p, nil
}

func (h *Handler) sendUpdate(ctx context.Context, info *livekit.EgressInfo) {
	requestType, outputType := getTypes(info)
	switch info.Status {
	case livekit.EgressStatus_EGRESS_FAILED:
		logger.Warnw("egress failed", errors.New(info.Error),
			"egressID", info.EgressId,
			"request_type", requestType,
			"output_type", outputType,
		)
	case livekit.EgressStatus_EGRESS_COMPLETE:
		logger.Infow("egress completed",
			"egressID", info.EgressId,
			"request_type", requestType,
			"output_type", outputType,
		)
	default:
		logger.Infow("egress updated",
			"egressID", info.EgressId,
			"request_type", requestType,
			"output_type", outputType,
			"status", info.Status,
		)
	}

}

func (h *Handler) sendResponse(ctx context.Context, req *livekit.EgressRequest, info *livekit.EgressInfo, err error) {
	args := []interface{}{
		"egressID", info.EgressId,
		"requestID", req.RequestId,
		"senderID", req.SenderId,
	}

	if err != nil {
		logger.Warnw("request failed", err, args...)
	} else {
		logger.Debugw("request handled", args...)
	}

}

func (h *Handler) Kill() {
	select {
	case <-h.kill:
		return
	default:
		close(h.kill)
	}
}

func (h *Handler) GetPipeline() *pipeline.Pipeline {
	return h.pipeline
}

func getTypes(info *livekit.EgressInfo) (requestType string, outputType string) {
	switch req := info.Request.(type) {
	case *livekit.EgressInfo_RoomComposite:
		requestType = "room_composite"
		switch req.RoomComposite.Output.(type) {
		case *livekit.RoomCompositeEgressRequest_File:
			outputType = "file"
		case *livekit.RoomCompositeEgressRequest_Stream:
			outputType = "stream"
		case *livekit.RoomCompositeEgressRequest_Segments:
			outputType = "segments"
		}
	case *livekit.EgressInfo_Web:
		requestType = "web"
		switch req.Web.Output.(type) {
		case *livekit.WebEgressRequest_File:
			outputType = "file"
		case *livekit.WebEgressRequest_Stream:
			outputType = "stream"
		case *livekit.WebEgressRequest_Segments:
			outputType = "segments"
		}
	case *livekit.EgressInfo_TrackComposite:
		requestType = "track_composite"
		switch req.TrackComposite.Output.(type) {
		case *livekit.TrackCompositeEgressRequest_File:
			outputType = "file"
		case *livekit.TrackCompositeEgressRequest_Stream:
			outputType = "stream"
		case *livekit.TrackCompositeEgressRequest_Segments:
			outputType = "segments"
		}
	case *livekit.EgressInfo_Track:
		requestType = "track"
		switch req.Track.Output.(type) {
		case *livekit.TrackEgressRequest_File:
			outputType = "file"
		case *livekit.TrackEgressRequest_WebsocketUrl:
			outputType = "websocket"
		}
	}

	return
}
