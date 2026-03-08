package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/CampusTech/unifi2snipe/config"
	"github.com/CampusTech/unifi2snipe/snipe"
	unifisync "github.com/CampusTech/unifi2snipe/sync"
	"github.com/CampusTech/unifi2snipe/unifi"
)

var (
	// Cfg is the global application configuration.
	Cfg *config.Config
	// ConfigFile is the path to the config file.
	ConfigFile string
	// Version is the application version, set from main.go.
	Version string

	verbose   bool
	debug     bool
	logFile   string
	logFormat string
	logFileFD *os.File
)

var log = logrus.New()

// LoadConfig loads config from YAML file with env var overrides, then applies
// CLI flag overrides for flags that were explicitly set.
func LoadConfig(cmd *cobra.Command) error {
	var err error
	Cfg, err = config.Load(ConfigFile)
	if err != nil {
		if cmd.Flags().Changed("config") {
			return fmt.Errorf("loading config: %w", err)
		}
		Cfg = &config.Config{}
	}

	applyBoolFlag(cmd, "dry-run", &Cfg.Sync.DryRun)
	applyBoolFlag(cmd, "force", &Cfg.Sync.Force)
	applyBoolFlag(cmd, "update-only", &Cfg.Sync.UpdateOnly)
	applyStringFlag(cmd, "cache-dir", &Cfg.Sync.CacheDir)

	var level logrus.Level
	switch {
	case debug:
		level = logrus.DebugLevel
	case verbose:
		level = logrus.InfoLevel
	default:
		level = logrus.WarnLevel
	}
	setAllLogLevels(level)

	var formatter logrus.Formatter
	switch strings.ToLower(logFormat) {
	case "json":
		formatter = &logrus.JSONFormatter{}
	case "text", "":
		formatter = &logrus.TextFormatter{FullTimestamp: true}
	default:
		return fmt.Errorf("invalid --log-format %q: must be 'text' or 'json'", logFormat)
	}
	setAllLogFormatters(formatter)

	setAllLogOutputs(os.Stderr)
	if logFileFD != nil {
		_ = logFileFD.Close()
		logFileFD = nil
	}
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return fmt.Errorf("opening log file: %w", err)
		}
		logFileFD = f
		setAllLogOutputs(io.MultiWriter(os.Stderr, f))
	}

	return nil
}

func setAllLogLevels(level logrus.Level) {
	log.SetLevel(level)
	unifi.SetLogLevel(level)
	unifisync.SetLogLevel(level)
	snipe.SetLogLevel(level)
}

func setAllLogFormatters(formatter logrus.Formatter) {
	log.SetFormatter(formatter)
	unifi.SetLogFormatter(formatter)
	unifisync.SetLogFormatter(formatter)
	snipe.SetLogFormatter(formatter)
}

func setAllLogOutputs(output io.Writer) {
	log.SetOutput(output)
	unifi.SetLogOutput(output)
	unifisync.SetLogOutput(output)
	snipe.SetLogOutput(output)
}

func applyBoolFlag(cmd *cobra.Command, name string, dst *bool) {
	if cmd.Flags().Changed(name) {
		*dst, _ = cmd.Flags().GetBool(name)
	}
}

func applyStringFlag(cmd *cobra.Command, name string, dst *string) {
	if cmd.Flags().Changed(name) {
		*dst, _ = cmd.Flags().GetString(name)
	}
}

func contextWithSignal() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			log.Infof("Received signal %v, shutting down...", sig)
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func newUniFiClient() (*unifi.Client, error) {
	log.Info("Connecting to UniFi Site Manager...")
	client, err := unifi.NewClient(Cfg.UniFi.APIKey, Cfg.UniFi.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("creating UniFi client: %w", err)
	}
	return client, nil
}

func newSnipeClient() (*snipe.Client, error) {
	log.Info("Connecting to Snipe-IT...")
	client, err := snipe.NewClient(Cfg.SnipeIT.URL, Cfg.SnipeIT.APIKey, Cfg.Sync.RateLimit)
	if err != nil {
		return nil, fmt.Errorf("creating Snipe-IT client: %w", err)
	}
	client.DryRun = Cfg.Sync.DryRun
	return client, nil
}

// Execute builds the root command, registers subcommands, and runs.
func Execute() {
	rootCmd := &cobra.Command{
		Use:          "unifi2snipe",
		Short:        "Sync devices from UniFi Site Manager into Snipe-IT",
		Long:         "unifi2snipe syncs network devices from the UniFi Site Manager API into Snipe-IT asset management.",
		Version:      Version,
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return LoadConfig(cmd)
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			if logFileFD != nil {
				logFileFD.Close()
			}
		},
	}

	rootCmd.PersistentFlags().StringVar(&ConfigFile, "config", "settings.yaml", "Path to YAML config file")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output (INFO level)")
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Debug output (DEBUG level)")
	rootCmd.PersistentFlags().StringVar(&logFile, "log-file", "", "Append log output to this file (in addition to stderr)")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "Log format: text or json")

	syncCmd := NewSyncCmd()
	downloadCmd := NewDownloadCmd()
	setupCmd := NewSetupCmd()

	for _, cmd := range []*cobra.Command{syncCmd, setupCmd} {
		cmd.Flags().Bool("dry-run", false, "Simulate without making changes")
	}

	for _, cmd := range []*cobra.Command{downloadCmd, syncCmd, setupCmd} {
		cmd.Flags().String("cache-dir", "", `Directory for cached API responses (default ".cache")`)
	}

	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(downloadCmd)
	rootCmd.AddCommand(setupCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
