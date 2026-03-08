package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	unifisync "github.com/CampusTech/unifi2snipe/sync"
	"github.com/CampusTech/unifi2snipe/unifi"
)

// NewSyncCmd creates the sync command.
func NewSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync UniFi devices into Snipe-IT",
		Long:  "Fetches devices from the UniFi Site Manager API and creates or updates corresponding assets in Snipe-IT.",
		RunE:  runSync,
	}

	cmd.Flags().Bool("force", false, "Ignore timestamps, always update")
	cmd.Flags().String("mac", "", "Sync a single device by MAC address (implies --force)")
	cmd.Flags().Bool("use-cache", false, "Use cached data instead of fetching from UniFi API")
	cmd.Flags().Bool("update-only", false, "Only update existing assets, never create new ones")

	return cmd
}

func runSync(cmd *cobra.Command, args []string) error {
	applyBoolFlag(cmd, "force", &Cfg.Sync.Force)
	applyBoolFlag(cmd, "update-only", &Cfg.Sync.UpdateOnly)
	applyBoolFlag(cmd, "use-cache", &Cfg.Sync.UseCache)

	if err := Cfg.Validate(); err != nil {
		return err
	}

	if Cfg.Sync.DryRun {
		log.Info("Running in DRY RUN mode - no changes will be made")
	}

	ctx, cancel := contextWithSignal()
	defer cancel()

	var unifiClient *unifi.Client
	if !Cfg.Sync.UseCache {
		var err error
		unifiClient, err = newUniFiClient()
		if err != nil {
			return err
		}
	}

	snipeClient, err := newSnipeClient()
	if err != nil {
		return err
	}

	engine := unifisync.NewEngine(unifiClient, snipeClient, Cfg)

	if Cfg.Sync.UseCache {
		if err := engine.LoadCache(); err != nil {
			return fmt.Errorf("loading cache: %w", err)
		}
	}

	mac, _ := cmd.Flags().GetString("mac")
	if mac != "" {
		Cfg.Sync.Force = true
	}

	var stats *unifisync.Stats
	if mac != "" {
		stats, err = engine.RunSingle(ctx, mac)
	} else {
		stats, err = engine.Run(ctx)
	}
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	fmt.Printf("\nSync Results:\n")
	fmt.Printf("  Total devices processed: %d\n", stats.Total)
	fmt.Printf("  Assets created:          %d\n", stats.Created)
	fmt.Printf("  Assets updated:          %d\n", stats.Updated)
	fmt.Printf("  Assets skipped:          %d\n", stats.Skipped)
	fmt.Printf("  Errors:                  %d\n", stats.Errors)
	fmt.Printf("  New models created:      %d\n", stats.ModelNew)

	return nil
}
