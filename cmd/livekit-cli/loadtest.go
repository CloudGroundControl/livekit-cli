package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/urfave/cli/v2"

	"livekit-cli-cgc/pkg/loadtester"
	"text/tabwriter"

	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go"
)

var LoadTestCommands = []*cli.Command{
	{
		Name:     "load-test",
		Usage:    "Run load tests against LiveKit with simulated publishers & subscribers",
		Category: "Simulate",
		Action:   loadTest,
		Flags: withDefaultFlags(
			&cli.StringFlag{
				Name:  "room",
				Usage: "name of the room (default to random name)",
			},
			&cli.DurationFlag{
				Name:  "duration",
				Usage: "duration to run, 1m, 1h (by default will run until canceled)",
				Value: 0,
			},
			&cli.IntFlag{
				Name:    "video-publishers",
				Aliases: []string{"publishers"},
				Usage:   "number of participants that would publish video tracks",
			},
			&cli.IntFlag{
				Name:  "audio-publishers",
				Usage: "number of participants that would publish audio tracks",
			},
			&cli.IntFlag{
				Name:  "subscribers",
				Usage: "number of participants that would subscribe to tracks",
			},
			&cli.StringFlag{
				Name:  "identity-prefix",
				Usage: "identity prefix of tester participants (defaults to a random prefix)",
			},
			&cli.StringFlag{
				Name:  "video-resolution",
				Usage: "resolution of video to publish. valid values are: high, medium, or low",
				Value: "high",
			},
			&cli.StringFlag{
				Name:  "video-codec",
				Usage: "h264 or vp8, both will be used when unset",
			},
			&cli.Float64Flag{
				Name:  "num-per-second",
				Usage: "number of testers to start every second",
				Value: 5,
			},
			&cli.StringFlag{
				Name:  "layout",
				Usage: "layout to simulate, choose from speaker, 3x3, 4x4, 5x5",
				Value: "speaker",
			},
			&cli.BoolFlag{
				Name:  "no-simulcast",
				Usage: "disables simulcast publishing (simulcast is enabled by default)",
			},
			&cli.BoolFlag{
				Name:   "run-all",
				Usage:  "runs set list of load test cases",
				Hidden: true,
			},
			&cli.IntFlag{
				Name:   "run-cgc",
				Usage:  "runs set list of cgc load test cases",
				Value:  0,
				Hidden: true,
			},
			&cli.IntFlag{
				Name:   "use-case",
				Usage:  "runs set list of cgc load test cases",
				Value:  0,
				Hidden: true,
			},
		),
	},
}

func loadTest(c *cli.Context) error {
	pc, err := loadProjectDetails(c)
	if err != nil {
		return err
	}

	if !c.Bool("verbose") {
		lksdk.SetLogger(logger.LogRLogger(logr.Discard()))
	}
	_ = raiseULimit()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		<-done
		cancel()
	}()

	layout := loadtester.LayoutFromString(c.String("layout"))
	params := loadtester.Params{
		Context:         ctx,
		VideoResolution: c.String("video-resolution"),
		VideoCodec:      c.String("video-codec"),
		Duration:        c.Duration("duration"),
		NumPerSecond:    c.Float64("num-per-second"),
		Simulcast:       !c.Bool("no-simulcast"),
		TesterParams: loadtester.TesterParams{
			URL:            pc.URL,
			APIKey:         pc.APIKey,
			APISecret:      pc.APISecret,
			Room:           c.String("room"),
			IdentityPrefix: c.String("identity-prefix"),
			Layout:         layout,
		},
	}

	if c.Bool("run-all") {
		// leave out room name and pub/sub counts
		test := loadtester.NewLoadTest(params)
		if test.Params.Duration == 0 {
			test.Params.Duration = time.Second * 15
		}
		return test.RunSuite()
	}

	cgcRoom := c.Int("run-cgc")
	var w *tabwriter.Writer
	useCase := c.Int("use-case")
	filename := fmt.Sprintf("cgc_%d_%d.log", useCase, cgcRoom)
	if cgcRoom > 0 {
		f, _ := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE, 0600)
		w = tabwriter.NewWriter(f, 1, 1, 1, ' ', 0)
		_, _ = fmt.Fprint(w, "\nRoom\t|Pubs\t| Subs\t| Tracks\t| Video\t| Packet loss\t| Errors\n")
	}
	for i := cgcRoom; i > 0; i-- {
		// leave out room name and pub/sub counts
		cgcParams := loadtester.Params{
			Context:         ctx,
			VideoResolution: "high",
			VideoCodec:      "h264",
			Duration:        0,
			NumPerSecond:    3,
			Simulcast:       true,
			TesterParams: loadtester.TesterParams{
				URL:            pc.URL,
				APIKey:         pc.APIKey,
				APISecret:      pc.APISecret,
				Room:           fmt.Sprintf("%s_%d", c.String("room"), i),
				IdentityPrefix: c.String("identity-prefix"),
				Layout:         layout,
			},
		}

		test := loadtester.NewLoadTest(cgcParams)
		if test.Params.Duration == 0 {
			test.Params.Duration = time.Second * 100
		}
		if i == 1 {
			test.Params.Duration += time.Second * 5
			test.RunCGCSuite(useCase, w)
			return w.Flush()
		} else {
			go func() {
				test.RunCGCSuite(useCase, w)
			}()
		}
	}

	params.VideoPublishers = c.Int("video-publishers")
	params.AudioPublishers = c.Int("audio-publishers")
	params.Subscribers = c.Int("subscribers")
	test := loadtester.NewLoadTest(params)

	return test.Run()
}
