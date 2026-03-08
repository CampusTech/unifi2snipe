package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	snipeit "github.com/michellepellon/go-snipeit"
	"github.com/CampusTech/unifi2snipe/config"
	"github.com/CampusTech/unifi2snipe/snipe"
)

// NewSetupCmd creates the setup command.
func NewSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Create all required Snipe-IT resources for UniFi sync",
		Long: `Creates the Ubiquiti manufacturer, network/console categories, UniFi Device
fieldset, and custom fields in Snipe-IT. Saves the resulting IDs and field
mappings back to the config file.`,
		RunE: runSetup,
	}
}

func runSetup(cmd *cobra.Command, args []string) error {
	if Cfg.SnipeIT.URL == "" || Cfg.SnipeIT.APIKey == "" {
		return fmt.Errorf("snipe_it.url and snipe_it.api_key are required")
	}

	if Cfg.Sync.DryRun {
		log.Info("Running in DRY RUN mode - no changes will be made")
	}

	snipeClient, err := newSnipeClient()
	if err != nil {
		return err
	}

	ctx := context.Background()

	// --- Manufacturer ---
	manufacturerID := Cfg.SnipeIT.ManufacturerID
	if manufacturerID == 0 {
		id, err := findOrCreateManufacturer(ctx, snipeClient, "Ubiquiti")
		if err != nil {
			return err
		}
		manufacturerID = id
	}
	fmt.Printf("Manufacturer: Ubiquiti (ID %d)\n", manufacturerID)

	// --- Categories ---
	networkCategoryID := Cfg.SnipeIT.NetworkCategoryID
	if networkCategoryID == 0 {
		networkCategoryID = Cfg.SnipeIT.CategoryID
	}
	if networkCategoryID == 0 {
		id, err := findOrCreateCategory(ctx, snipeClient, "Networking", "asset")
		if err != nil {
			return err
		}
		networkCategoryID = id
	}
	fmt.Printf("Network category: ID %d\n", networkCategoryID)

	consoleCategoryID := Cfg.SnipeIT.ConsoleCategoryID
	if consoleCategoryID == 0 {
		id, err := findOrCreateCategory(ctx, snipeClient, "Network Console", "asset")
		if err != nil {
			return err
		}
		consoleCategoryID = id
	}
	fmt.Printf("Console category: ID %d\n", consoleCategoryID)

	// --- Fieldset ---
	fieldsetID := Cfg.SnipeIT.CustomFieldsetID
	if fieldsetID == 0 {
		id, err := findOrCreateFieldset(ctx, snipeClient, "UniFi Device")
		if err != nil {
			return err
		}
		fieldsetID = id
	}
	fmt.Printf("Fieldset: UniFi Device (ID %d)\n", fieldsetID)

	// --- Custom Fields ---
	fields := []snipe.FieldDef{
		{Name: "UniFi: IP Address", Element: "text", Format: "IP", HelpText: "Device IP address"},
		{Name: "UniFi: MAC Address", Element: "text", Format: "MAC", HelpText: "Device MAC address (colon-separated)"},
		{Name: "UniFi: Firmware Version", Element: "text", Format: "ANY", HelpText: "Current firmware version"},
		{Name: "UniFi: Firmware Status", Element: "listbox", Format: "ANY", HelpText: "Firmware update status", FieldValues: "upToDate\npendingUpdate\nreadyForUpdate"},
		{Name: "UniFi: Status", Element: "listbox", Format: "ANY", HelpText: "Device connection status", FieldValues: "online\noffline\npending\nadopting\nupdating"},
		{Name: "UniFi: Product Line", Element: "listbox", Format: "ANY", HelpText: "UniFi product line", FieldValues: "network\nprotect\naccess\ntalk\nconnect\nled"},
		{Name: "UniFi: Model", Element: "text", Format: "ANY", HelpText: "Hardware model identifier (e.g. U6-Pro, USW-24-PoE)"},
		{Name: "UniFi: Is Managed", Element: "text", Format: "BOOLEAN", HelpText: "Whether the device is managed by a UniFi controller"},
		{Name: "UniFi: Is Console", Element: "text", Format: "BOOLEAN", HelpText: "Whether the device is a UniFi console (UDM, UCK, etc.)"},
		{Name: "UniFi: Host Name", Element: "text", Format: "ANY", HelpText: "Name of the UniFi controller/console managing this device"},
		{Name: "UniFi: Host ID", Element: "text", Format: "ANY", HelpText: "ID of the UniFi controller/console managing this device"},
		{Name: "UniFi: Adoption Time", Element: "text", Format: "DATE", HelpText: "When the device was adopted by the controller"},
		{Name: "UniFi: Note", Element: "textarea", Format: "ANY", HelpText: "Note from UniFi device settings"},
	}

	log.Info("Creating custom fields in Snipe-IT...")
	results, err := snipeClient.SetupFields(fieldsetID, fields)
	if err != nil {
		return fmt.Errorf("setting up fields: %w", err)
	}

	// Map field names to their suggested UniFi attribute
	unifiAttr := map[string]string{
		"UniFi: IP Address":       "ip",
		"UniFi: MAC Address":      "mac",
		"UniFi: Firmware Version": "version",
		"UniFi: Firmware Status":  "firmware_status",
		"UniFi: Status":           "status",
		"UniFi: Product Line":     "product_line",
		"UniFi: Model":            "model",
		"UniFi: Is Managed":       "is_managed",
		"UniFi: Is Console":       "is_console",
		"UniFi: Host Name":        "host_name",
		"UniFi: Host ID":          "host_id",
		"UniFi: Adoption Time":    "adoption_time",
		"UniFi: Note":             "note",
	}

	// Build field mapping: DB column -> UniFi attribute
	fieldMapping := make(map[string]string)
	replaceValues := make(map[string]bool)
	for name, dbCol := range results {
		if attr, ok := unifiAttr[name]; ok {
			fieldMapping[dbCol] = attr
			replaceValues[attr] = true
		}
	}

	// Save IDs to config
	if err := config.MergeIDs(ConfigFile, manufacturerID, networkCategoryID, consoleCategoryID, fieldsetID); err != nil {
		log.Warnf("Could not save IDs to %s: %v", ConfigFile, err)
		fmt.Println("\nAdd these to your settings.yaml manually:")
		fmt.Printf("  manufacturer_id: %d\n", manufacturerID)
		fmt.Printf("  network_category_id: %d\n", networkCategoryID)
		fmt.Printf("  console_category_id: %d\n", consoleCategoryID)
		fmt.Printf("  custom_fieldset_id: %d\n", fieldsetID)
	}

	if err := config.MergeFieldMapping(ConfigFile, fieldMapping, replaceValues); err != nil {
		log.Warnf("Could not save field mappings to %s: %v", ConfigFile, err)
		fmt.Println("\nAdd these to your settings.yaml field_mapping manually:")
		for dbCol, attr := range fieldMapping {
			fmt.Printf("    %s: %s\n", dbCol, attr)
		}
	} else {
		fmt.Printf("\nConfiguration saved to %s\n", ConfigFile)
	}

	fmt.Println("\nCustom fields created and associated with fieldset:")
	for name, dbCol := range results {
		if attr, ok := unifiAttr[name]; ok {
			fmt.Printf("  %s: %s -> %s\n", name, dbCol, attr)
		} else {
			fmt.Printf("  %s: %s\n", name, dbCol)
		}
	}

	return nil
}

func findOrCreateManufacturer(ctx context.Context, c *snipe.Client, name string) (int, error) {
	resp, _, err := c.Manufacturers.ListContext(ctx, &snipeit.ListOptions{Limit: 500})
	if err != nil {
		return 0, fmt.Errorf("listing manufacturers: %w", err)
	}
	for _, m := range resp.Rows {
		if strings.EqualFold(m.Name, name) {
			log.Infof("Found existing manufacturer %q (ID %d)", name, m.ID)
			return m.ID, nil
		}
	}

	if c.DryRun {
		fmt.Printf("[DRY RUN] Would create manufacturer %q\n", name)
		return 0, nil
	}

	createResp, _, err := c.Manufacturers.CreateContext(ctx, snipeit.Manufacturer{
		CommonFields: snipeit.CommonFields{Name: name},
	})
	if err != nil {
		return 0, fmt.Errorf("creating manufacturer %q: %w", name, err)
	}
	if createResp.Status != "success" {
		return 0, fmt.Errorf("creating manufacturer %q: %s", name, createResp.Message)
	}
	log.Infof("Created manufacturer %q (ID %d)", name, createResp.Payload.ID)
	return createResp.Payload.ID, nil
}

func findOrCreateCategory(ctx context.Context, c *snipe.Client, name, categoryType string) (int, error) {
	resp, _, err := c.Categories.ListContext(ctx, &snipeit.ListOptions{Limit: 500})
	if err != nil {
		return 0, fmt.Errorf("listing categories: %w", err)
	}
	for _, cat := range resp.Rows {
		if strings.EqualFold(cat.Name, name) {
			log.Infof("Found existing category %q (ID %d)", name, cat.ID)
			return cat.ID, nil
		}
	}

	if c.DryRun {
		fmt.Printf("[DRY RUN] Would create category %q\n", name)
		return 0, nil
	}

	createResp, _, err := c.Categories.CreateContext(ctx, snipeit.Category{
		CommonFields: snipeit.CommonFields{Name: name},
		Type:         categoryType,
	})
	if err != nil {
		return 0, fmt.Errorf("creating category %q: %w", name, err)
	}
	if createResp.Status != "success" {
		return 0, fmt.Errorf("creating category %q: %s", name, createResp.Message)
	}
	log.Infof("Created category %q (ID %d)", name, createResp.Payload.ID)
	return createResp.Payload.ID, nil
}

func findOrCreateFieldset(ctx context.Context, c *snipe.Client, name string) (int, error) {
	resp, _, err := c.Fieldsets.List(nil)
	if err != nil {
		return 0, fmt.Errorf("listing fieldsets: %w", err)
	}
	for _, fs := range resp.Rows {
		if strings.EqualFold(fs.Name, name) {
			log.Infof("Found existing fieldset %q (ID %d)", name, fs.ID)
			return fs.ID, nil
		}
	}

	if c.DryRun {
		fmt.Printf("[DRY RUN] Would create fieldset %q\n", name)
		return 0, nil
	}

	createResp, _, err := c.Fieldsets.Create(snipeit.Fieldset{
		CommonFields: snipeit.CommonFields{Name: name},
	})
	if err != nil {
		return 0, fmt.Errorf("creating fieldset %q: %w", name, err)
	}
	if createResp.Status != "success" {
		return 0, fmt.Errorf("creating fieldset %q: %s", name, createResp.Message)
	}
	log.Infof("Created fieldset %q (ID %d)", name, createResp.Payload.ID)
	return createResp.Payload.ID, nil
}
