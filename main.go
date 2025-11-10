package main

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/andreykaipov/goobs"
	obsevents "github.com/andreykaipov/goobs/api/events"
	"github.com/andreykaipov/goobs/api/events/subscriptions"
	obsinputs "github.com/andreykaipov/goobs/api/requests/inputs"
	"github.com/davecgh/go-spew/spew"
	"github.com/facebookincubator/go-belt"
	"github.com/facebookincubator/go-belt/tool/logger"
	"github.com/facebookincubator/go-belt/tool/logger/implementation/logrus"
	"github.com/spf13/pflag"
)

const (
	magicSuffix = "_reopening"
)

func main() {
	logLevel := logger.LevelInfo
	pflag.Var(&logLevel, "log-level", "Log level (debug, info, warning, error, panic, fatal)")
	addr := pflag.String("addr", "localhost:4455", "OBS websocket host:port")
	password := pflag.String("pass", "", "OBS websocket password (if set)")
	inputs := pflag.StringSlice("input", nil, "OBS input names to check (may be specified multiple times)")
	dryRun := pflag.Bool("dry-run", false, "Show what would change without applying it")
	pflag.Parse()

	l := logrus.Default().WithLevel(logLevel)
	ctx := context.Background()
	ctx = logger.CtxWithLogger(ctx, l)
	logger.Default = func() logger.Logger {
		return l
	}
	defer belt.Flush(ctx)

	if len(*inputs) == 0 {
		logger.Fatalf(ctx, "--input must be specified at least once")
	}

	client := must(goobs.New(
		*addr,
		goobs.WithPassword(*password),
		goobs.WithEventSubscriptions(subscriptions.InputVolumeMeters),
	))
	defer client.Disconnect()

	currentLevel := map[string]float64{}
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Debugf(ctx, "context is done")
			return
		case ev, ok := <-client.IncomingEvents:
			if !ok {
				logger.Fatalf(ctx, "events channel is closed")
				return
			}
			logger.Tracef(ctx, "got an OBS event: %T", ev)
			switch ev := ev.(type) {
			case *obsevents.InputVolumeMeters:
				logger.Tracef(ctx, "got an OBS InputVolumeMeters event: %#+v", spew.Sdump(ev))
				for name := range currentLevel {
					delete(currentLevel, name)
				}
				for _, input := range ev.Inputs {
					volume := -math.MaxFloat64
					for _, s := range input.Levels {
						for _, cmp := range s {
							volume = math.Max(volume, cmp)
						}
					}
					currentLevel[input.Name] = volume
				}
			}
		case <-t.C:
			for _, inputName := range *inputs {
				checkAndFixInput(ctx, client, inputName, currentLevel[inputName], *dryRun)
			}
		}
	}
}

func checkAndFixInput(
	ctx context.Context,
	cl *goobs.Client,
	inputName string,
	currentLevel float64,
	dryRun bool,
) {
	isZero := currentLevel < 0
	logger.Tracef(ctx, "Input=%q volume=%.6f; isZero=%v", inputName, currentLevel, isZero)
	if !isZero {
		logger.Tracef(ctx, "Input=%q volume is non-zero; nothing to change", inputName)
		return
	}

	settingsResp := must(cl.Inputs.GetInputSettings(&obsinputs.GetInputSettingsParams{
		InputName: ptr(inputName),
	}))
	settings := settingsResp.InputSettings
	logger.Debugf(ctx, "Input=%q settings: %+v", inputName, settings)
	deviceID := settings["device_id"].(string)
	if deviceID != "__custom__" {
		logger.Fatalf(ctx, "Input=%q has unsupported device_id=%q; only device_id=\"__custom__\" is supported", inputName, deviceID)
	}
	customPCM := settings["custom_pcm"].(string)

	if !strings.HasSuffix(customPCM, magicSuffix) {
		customPCM += magicSuffix
		settings["custom_pcm"] = customPCM
		req := obsinputs.SetInputSettingsParams{
			InputName:     ptr(inputName),
			InputSettings: settings,
			Overlay:       ptr(true),
		}
		if dryRun {
			logger.Infof(ctx, "[dry-run] would set Input=%q settings: %+v", inputName, req)
		} else {
			logger.Debugf(ctx, "sending Input=%q settings to close device: %+v", inputName, req)
			_ = must(cl.Inputs.SetInputSettings(&req))
		}
	}

	customPCM = strings.TrimSuffix(customPCM, magicSuffix)
	settings["custom_pcm"] = customPCM
	req := obsinputs.SetInputSettingsParams{
		InputName:     ptr(inputName),
		InputSettings: settings,
		Overlay:       ptr(true),
	}
	if dryRun {
		logger.Infof(ctx, "[dry-run] would set Input=%q settings: %+v", inputName, req)
	} else {
		logger.Debugf(ctx, "sending Input=%q settings to reopen device: %+v", inputName, req)
		_ = must(cl.Inputs.SetInputSettings(&req))
	}
	time.Sleep(5 * time.Second) // give it time to reopen
}
