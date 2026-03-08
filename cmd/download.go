package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	unifisync "github.com/CampusTech/unifi2snipe/sync"
)

// NewDownloadCmd creates the download command.
func NewDownloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "download",
		Short: "Download UniFi device data to local cache",
		Long:  "Fetches all devices from the UniFi Site Manager API and saves them to a local cache directory for offline sync.",
		RunE:  runDownload,
	}
}

func runDownload(cmd *cobra.Command, args []string) error {
	if err := Cfg.ValidateUniFi(); err != nil {
		return err
	}

	ctx, cancel := contextWithSignal()
	defer cancel()

	unifiClient, err := newUniFiClient()
	if err != nil {
		return err
	}

	engine := unifisync.NewDownloadEngine(unifiClient, Cfg)

	devices, err := engine.FetchAndSaveCache(ctx)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	fmt.Printf("\nDownloaded %d devices to %s/devices.json\n", len(devices), engine.CacheDir())

	return nil
}
