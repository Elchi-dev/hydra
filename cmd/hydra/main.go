// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

// Command hydra is the relay server: RTMP ingest + ffmpeg fan-out + web UI.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Elchi-dev/hydra/internal/api"
	"github.com/Elchi-dev/hydra/internal/benchmark"
	"github.com/Elchi-dev/hydra/internal/build"
	"github.com/Elchi-dev/hydra/internal/config"
	"github.com/Elchi-dev/hydra/internal/hardware"
	"github.com/Elchi-dev/hydra/internal/pipeline"
	"github.com/Elchi-dev/hydra/internal/rtmpingest"
	"github.com/Elchi-dev/hydra/internal/state"
)

func main() {
	cfgPath := flag.String("config", "hydra.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	doctor := flag.Bool("doctor", false, "print a hardware and encoder report, then exit")
	bench := flag.Bool("benchmark", false, "measure transcoding capacity, then exit")
	wizard := flag.Bool("wizard", false, "interactive setup to generate a config, then exit")
	benchRes := flag.String("benchmark-res", "1920x1080", "benchmark resolution WxH")
	benchFPS := flag.Int("benchmark-fps", 60, "benchmark frame rate")
	benchPreset := flag.String("benchmark-preset", "veryfast", "benchmark x264 preset")
	flag.Parse()

	if *showVersion {
		fmt.Println(build.VersionLine())
		return
	}

	if *doctor {
		runDoctor(*cfgPath)
		return
	}

	if *bench {
		runBenchmark(*cfgPath, *benchRes, *benchFPS, *benchPreset)
		return
	}

	if *wizard {
		runWizard(*cfgPath)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	// Targets are filled with their platform-preset defaults during Load, so a
	// new platform is just a config entry (or a preset addition).

	log := newLogger(cfg.Logging.Level)

	hw := hardware.Detect(cfg.Server.FFmpegPath)
	log.Info("hardware detected",
		"cores", hw.LogicalCores,
		"encoders", len(hw.Encoders),
		"hardware_encoder", hw.HasHardwareEncoder(),
	)

	store := state.New()
	mgr := pipeline.NewManager(cfg, store, log)

	// RTMP ingest.
	rtmpSrv := rtmpingest.New(cfg, mgr, log)
	if err := rtmpSrv.Listen(); err != nil {
		log.Error("rtmp listen failed", "err", err)
		os.Exit(1)
	}

	// HTTP API + dashboard.
	apiSrv := api.New(cfg, mgr, store, log, hw)
	httpSrv := &http.Server{
		Addr:    cfg.Server.HTTPListen,
		Handler: apiSrv.Handler(),
	}

	// Local control socket: the same API over a unix socket so hydractl can talk
	// to the server locally without a token (filesystem permissions are the auth).
	sockSrv := &http.Server{Handler: apiSrv.Handler()}
	sockLn, sockErr := listenUnix(cfg.Server.ControlSocket)
	if sockErr != nil {
		log.Warn("control socket unavailable; CLI must use --url", "err", sockErr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("dashboard listening", "url", "http://"+cfg.Server.HTTPListen)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http server error", "err", err)
		}
	}()

	go func() {
		if err := rtmpSrv.Serve(); err != nil {
			// Serve returns an error on Close; only log if unexpected.
			select {
			case <-ctx.Done():
			default:
				log.Error("rtmp serve error", "err", err)
			}
		}
	}()

	if sockLn != nil {
		go func() {
			log.Info("control socket listening", "path", cfg.Server.ControlSocket)
			if err := sockSrv.Serve(sockLn); err != nil && err != http.ErrServerClosed {
				log.Error("control socket error", "err", err)
			}
		}()
	}

	printBanner(cfg)

	<-ctx.Done()
	log.Info("shutting down")

	mgr.StopSession("server shutdown")
	_ = rtmpSrv.Close()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	if sockLn != nil {
		_ = sockSrv.Shutdown(shutCtx)
		_ = os.Remove(cfg.Server.ControlSocket)
	}
}

// listenUnix prepares and binds the control socket, replacing any stale file.
func listenUnix(path string) (net.Listener, error) {
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	_ = os.Remove(path) // clear stale socket from a previous run
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o660)
	return ln, nil
}

// runBenchmark measures transcoding capacity for a profile and prints a report.
func runBenchmark(cfgPath, res string, fps int, preset string) {
	ffmpegBin := "ffmpeg"
	if cfg, err := config.Load(cfgPath); err == nil && cfg.Server.FFmpegPath != "" {
		ffmpegBin = cfg.Server.FFmpegPath
	}

	hw := hardware.Detect(ffmpegBin)
	if !hw.FFmpegOK {
		fmt.Fprintln(os.Stderr, "ffmpeg could not be run; fix the install first (see: hydra -doctor)")
		os.Exit(1)
	}

	encoder := "libx264"
	if e, ok := hw.BestEncoderFor("h264"); ok {
		encoder = e.Name
	}

	fmt.Printf("%s %s  %s\n\n", build.Name, build.Version, build.Credit)
	fmt.Printf("Benchmarking %s %dfps with %s. This runs several real encodes and takes about a minute...\n\n", res, fps, encoder)

	profile := benchmark.Profile{Encoder: encoder, Resolution: res, FPS: fps, Preset: preset}
	result := benchmark.Run(context.Background(), ffmpegBin, profile, benchmark.Options{})
	fmt.Print(result.Report())
}

// runDoctor prints a hardware and encoder report without starting the server.
// It works even without a valid config, falling back to ffmpeg on PATH.
func runDoctor(cfgPath string) {
	ffmpegBin := "ffmpeg"
	if cfg, err := config.Load(cfgPath); err == nil && cfg.Server.FFmpegPath != "" {
		ffmpegBin = cfg.Server.FFmpegPath
	}
	fmt.Printf("%s %s  %s\n\n", build.Name, build.Version, build.Credit)
	fmt.Print(hardware.Detect(ffmpegBin).Report())
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}

func printBanner(cfg *config.Config) {
	host := "<host>"
	if h, _ := os.Hostname(); h != "" {
		host = h
	}
	port := ":1935"
	if _, p, err := net.SplitHostPort(cfg.Server.RTMPListen); err == nil && p != "" {
		port = ":" + p
	}
	n := 0
	for _, t := range cfg.Targets {
		if t.Enabled {
			n++
		}
	}
	fmt.Print("\n")
	fmt.Println("    ██╗  ██╗██╗   ██╗██████╗ ██████╗  █████╗ ")
	fmt.Println("    ██║  ██║╚██╗ ██╔╝██╔══██╗██╔══██╗██╔══██╗")
	fmt.Println("    ███████║ ╚████╔╝ ██║  ██║██████╔╝███████║")
	fmt.Println("    ██╔══██║  ╚██╔╝  ██║  ██║██╔══██╗██╔══██║")
	fmt.Println("    ██║  ██║   ██║   ██████╔╝██║  ██║██║  ██║")
	fmt.Println("    ╚═╝  ╚═╝   ╚═╝   ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝")
	fmt.Printf("    %s v%s   %s\n", build.Tagline, build.Version, build.Credit)
	fmt.Println()
	fmt.Printf("    ingest    rtmp://%s%s/%s/<stream_key>\n", host, port, cfg.Server.IngestApp)
	fmt.Printf("    dashboard http://%s\n", cfg.Server.HTTPListen)
	fmt.Printf("    control   hydractl --socket %s status\n", cfg.Server.ControlSocket)
	fmt.Printf("    outputs   %d enabled, fallback %s\n", n, brbState(cfg.BRB.Enabled))
	fmt.Println()
}

func brbState(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
