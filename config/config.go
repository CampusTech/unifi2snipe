package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for unifi2snipe.
type Config struct {
	UniFi   UniFiConfig   `yaml:"unifi"`
	SnipeIT SnipeITConfig `yaml:"snipe_it"`
	Sync    SyncConfig    `yaml:"sync"`
}

// UniFiConfig holds UniFi Site Manager API settings.
type UniFiConfig struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"` // defaults to https://api.ui.com
}

// SnipeITConfig holds Snipe-IT API settings.
type SnipeITConfig struct {
	URL               string `yaml:"url"`
	APIKey            string `yaml:"api_key"`
	ManufacturerID    int    `yaml:"manufacturer_id"`     // Ubiquiti manufacturer ID in Snipe
	DefaultStatusID   int    `yaml:"default_status_id"`   // Status for newly created assets
	CategoryID        int    `yaml:"category_id"`         // Default category for new models (fallback)
	NetworkCategoryID int    `yaml:"network_category_id"` // Category for network devices (switches, APs, routers)
	ConsoleCategoryID int    `yaml:"console_category_id"` // Category for console devices (UDM, UCK)
	CustomFieldsetID  int    `yaml:"custom_fieldset_id"`  // Optional fieldset for new models
}

// SyncConfig holds sync behavior settings.
type SyncConfig struct {
	DryRun       bool              `yaml:"dry_run"`
	Force        bool              `yaml:"force"`         // ignore timestamps, always update
	RateLimit    bool              `yaml:"rate_limit"`    // enable rate limiting
	UpdateOnly   bool              `yaml:"update_only"`   // only update existing assets, never create
	UseCache     bool              `yaml:"use_cache"`     // use cached data instead of fetching from UniFi
	CacheDir     string            `yaml:"cache_dir"`     // directory for cached API responses (default ".cache")
	FieldMapping    map[string]string `yaml:"field_mapping"`    // snipe field -> unifi attribute mapping
	LocationMapping map[string]int    `yaml:"location_mapping"` // host name or host ID -> snipe-it location ID
	ProductLines    []string          `yaml:"product_lines"`    // filter by product line (e.g. "network", "protect")
	HostIDs         []string          `yaml:"host_ids"`         // filter by specific host IDs
	SetName         bool              `yaml:"set_name"`         // set asset name on create (default false)
	Checkout        bool              `yaml:"checkout"`         // checkout assets to mapped locations
}

// Load reads configuration from a YAML file and applies environment variable overrides.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Environment variable overrides
	if v := os.Getenv("UNIFI_API_KEY"); v != "" {
		cfg.UniFi.APIKey = v
	}
	if v := os.Getenv("UNIFI_BASE_URL"); v != "" {
		cfg.UniFi.BaseURL = v
	}
	if v := os.Getenv("UNIFI_SNIPE_URL"); v != "" {
		cfg.SnipeIT.URL = v
	}
	if v := os.Getenv("UNIFI_SNIPE_API_KEY"); v != "" {
		cfg.SnipeIT.APIKey = v
	}

	return cfg, nil
}

// Validate checks that all required fields are set for a full sync.
func (c *Config) Validate() error {
	if !c.Sync.UseCache {
		if err := c.ValidateUniFi(); err != nil {
			return err
		}
	}
	return c.ValidateSnipeIT()
}

// ValidateUniFi checks that UniFi API credentials are set.
func (c *Config) ValidateUniFi() error {
	if c.UniFi.APIKey == "" {
		return fmt.Errorf("unifi.api_key is required")
	}
	return nil
}

// ValidateSnipeIT checks that Snipe-IT credentials and required IDs are set.
func (c *Config) ValidateSnipeIT() error {
	if c.SnipeIT.URL == "" {
		return fmt.Errorf("snipe_it.url is required")
	}
	if c.SnipeIT.APIKey == "" {
		return fmt.Errorf("snipe_it.api_key is required")
	}
	if c.SnipeIT.ManufacturerID == 0 {
		return fmt.Errorf("snipe_it.manufacturer_id is required")
	}
	if c.SnipeIT.DefaultStatusID == 0 {
		return fmt.Errorf("snipe_it.default_status_id is required")
	}
	if c.SnipeIT.CategoryID == 0 && c.SnipeIT.NetworkCategoryID == 0 && c.SnipeIT.ConsoleCategoryID == 0 {
		return fmt.Errorf("snipe_it.category_id (or network_category_id/console_category_id) is required")
	}
	return nil
}

// CategoryIDForProductLine returns the appropriate Snipe-IT category ID for
// a given UniFi product line (e.g. "network", "protect", "access").
func (c *SnipeITConfig) CategoryIDForProductLine(productLine string) int {
	switch productLine {
	case "network":
		if c.NetworkCategoryID != 0 {
			return c.NetworkCategoryID
		}
	case "console":
		if c.ConsoleCategoryID != 0 {
			return c.ConsoleCategoryID
		}
	}
	if c.CategoryID != 0 {
		return c.CategoryID
	}
	if c.NetworkCategoryID != 0 {
		return c.NetworkCategoryID
	}
	return c.ConsoleCategoryID
}

// LocationIDForHost resolves a Snipe-IT location ID from a device's host name
// or host ID using the configured location_mapping. Returns 0 if no match.
func (c *SyncConfig) LocationIDForHost(hostID, hostName string) int {
	if len(c.LocationMapping) == 0 {
		return 0
	}
	// Try host name first (more user-friendly)
	if hostName != "" {
		if id, ok := c.LocationMapping[hostName]; ok {
			return id
		}
	}
	// Fall back to host ID
	if hostID != "" {
		if id, ok := c.LocationMapping[hostID]; ok {
			return id
		}
	}
	return 0
}

// MergeFieldMapping reads a YAML config file, merges new field mappings into
// sync.field_mapping and writes it back.
func MergeFieldMapping(path string, newMappings map[string]string, replaceValues map[string]bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config file: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing config file: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return fmt.Errorf("unexpected YAML structure")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping at root")
	}

	syncNode := findOrCreateMapping(root, "sync")
	fmNode := findOrCreateMapping(syncNode, "field_mapping")

	// Remove stale entries whose value is in replaceValues
	if len(replaceValues) > 0 {
		var kept []*yaml.Node
		for i := 0; i < len(fmNode.Content)-1; i += 2 {
			if !replaceValues[fmNode.Content[i+1].Value] {
				kept = append(kept, fmNode.Content[i], fmNode.Content[i+1])
			}
		}
		fmNode.Content = kept
	}

	existing := make(map[string]bool)
	for i := 0; i < len(fmNode.Content)-1; i += 2 {
		existing[fmNode.Content[i].Value] = true
	}

	for dbCol, attr := range newMappings {
		if dbCol == "" || attr == "" || existing[dbCol] {
			continue
		}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: dbCol, Tag: "!!str"}
		valNode := &yaml.Node{Kind: yaml.ScalarNode, Value: attr, Tag: "!!str"}
		fmNode.Content = append(fmNode.Content, keyNode, valNode)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(path, out, 0600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

// MergeIDs updates snipe_it ID fields in the YAML config file.
func MergeIDs(path string, manufacturerID, networkCategoryID, consoleCategoryID, fieldsetID int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config file: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing config file: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return fmt.Errorf("unexpected YAML structure")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping at root")
	}

	snipeNode := findOrCreateMapping(root, "snipe_it")
	setScalar(snipeNode, "manufacturer_id", strconv.Itoa(manufacturerID), "!!int")
	setScalar(snipeNode, "network_category_id", strconv.Itoa(networkCategoryID), "!!int")
	setScalar(snipeNode, "console_category_id", strconv.Itoa(consoleCategoryID), "!!int")
	setScalar(snipeNode, "custom_fieldset_id", strconv.Itoa(fieldsetID), "!!int")

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(path, out, 0600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

func setScalar(parent *yaml.Node, key, value, tag string) {
	for i := 0; i < len(parent.Content)-1; i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1].Value = value
			parent.Content[i+1].Tag = tag
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Value: value, Tag: tag}
	parent.Content = append(parent.Content, keyNode, valNode)
}

func findOrCreateMapping(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(parent.Content)-1; i += 2 {
		if parent.Content[i].Value == key {
			valNode := parent.Content[i+1]
			if valNode.Kind != yaml.MappingNode {
				valNode.Kind = yaml.MappingNode
				valNode.Tag = "!!map"
				valNode.Value = ""
				valNode.Content = nil
			}
			return valNode
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"}
	valNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, keyNode, valNode)
	return valNode
}
