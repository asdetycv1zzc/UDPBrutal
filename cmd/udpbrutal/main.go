package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/asdetycv1zzc/UDPBrutal/internal/config"
	"github.com/asdetycv1zzc/UDPBrutal/internal/proxy"
)

var version = "dev"
var commit = "unknown"

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func usage(w io.Writer) {
	fmt.Fprintln(w, `UDPBrutal — plaintext UDP proxy with Hysteria2 Brutal congestion control

Usage:
  udpbrutal [client|server] [-c config.yaml] [options]
  udpbrutal version

The default command is client.
  -c, --config PATH       YAML/JSON configuration file
  -l, --log-level LEVEL   debug, info, warn, error
  -f, --log-format FORMAT console, json
      --check            Validate configuration and exit
      --disable-update-check  Accepted for Hysteria-style service scripts
  -h, --help             Show help

Config search: ./config.{yaml,yml,json}, ~/.udpbrutal/, /etc/udpbrutal/
Transport uses the HY2 QUIC engine with a plaintext handshake and unprotected packets.
TLS, ACME and obfuscation are not used.`)
}
func run(args []string) error {
	mode := "client"
	// Extract a command while preserving flag arguments, so -c can precede it.
	remaining := make([]string, 0, len(args))
	valueFlags := map[string]bool{"-c": true, "--config": true, "-config": true, "-l": true, "--log-level": true, "-log-level": true, "-f": true, "--log-format": true, "-log-format": true}
	commandSeen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if valueFlags[arg] {
			remaining = append(remaining, arg)
			i++
			if i >= len(args) {
				return fmt.Errorf("%s needs a value", arg)
			}
			remaining = append(remaining, args[i])
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			if commandSeen {
				return fmt.Errorf("unexpected argument: %s", arg)
			}
			if arg != "client" && arg != "server" && arg != "version" && arg != "help" {
				return fmt.Errorf("unknown command: %s", arg)
			}
			mode = arg
			commandSeen = true
			continue
		}
		remaining = append(remaining, arg)
	}
	flags := flag.NewFlagSet("udpbrutal", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { usage(flags.Output()) }
	var path, level, format string
	var check, noUpdate, showVersion bool
	flags.StringVar(&path, "c", "", "config file")
	flags.StringVar(&path, "config", "", "config file")
	flags.StringVar(&level, "l", env("UDPBRUTAL_LOG_LEVEL", "info"), "log level")
	flags.StringVar(&level, "log-level", env("UDPBRUTAL_LOG_LEVEL", "info"), "log level")
	flags.StringVar(&format, "f", env("UDPBRUTAL_LOG_FORMAT", "console"), "log format")
	flags.StringVar(&format, "log-format", env("UDPBRUTAL_LOG_FORMAT", "console"), "log format")
	flags.BoolVar(&check, "check", false, "validate config")
	flags.BoolVar(&noUpdate, "disable-update-check", true, "no automatic update checks are performed")
	flags.BoolVar(&showVersion, "version", false, "show version")
	if err := flags.Parse(remaining); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if mode == "help" {
		usage(os.Stdout)
		return nil
	}
	if mode == "version" || showVersion {
		fmt.Printf("UDPBrutal %s\nCommit: %s\nTransport: plaintext UDP\n", version, commit)
		return nil
	}
	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(level)); err != nil {
		return err
	}
	options := &slog.HandlerOptions{Level: logLevel}
	switch format {
	case "console":
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, options)))
	case "json":
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, options)))
	default:
		return fmt.Errorf("unsupported log format: %s", format)
	}
	if debug := os.Getenv("UDPBRUTAL_BRUTAL_DEBUG"); debug != "" {
		_ = os.Setenv("HYSTERIA_BRUTAL_DEBUG", debug)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if mode == "server" {
		var cfg config.Server
		if err := config.Load(path, &cfg); err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		if check {
			fmt.Println("Server configuration valid")
			return nil
		}
		s, err := proxy.NewServer(cfg)
		if err != nil {
			return err
		}
		defer s.Close()
		slog.Info("UDPBrutal server listening", "listen", s.Addr(), "transport", "Hysteria plaintext profile")
		return s.Serve(ctx)
	}
	var cfg config.Client
	if err := config.Load(path, &cfg); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if check {
		fmt.Println("Client configuration valid")
		return nil
	}
	c, err := proxy.StartClient(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	c.Wait()
	return nil
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "udpbrutal:", err)
		os.Exit(1)
	}
}
