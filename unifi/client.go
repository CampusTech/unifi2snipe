// Package unifi wraps the go-unifi sitemanager client with caching and
// convenience methods for the unifi2snipe sync process.
package unifi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lexfrei/go-unifi/api/sitemanager"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

// SetLogLevel sets the logger level for the unifi package.
func SetLogLevel(level logrus.Level) { log.SetLevel(level) }

// SetLogFormatter sets the logger formatter for the unifi package.
func SetLogFormatter(formatter logrus.Formatter) { log.SetFormatter(formatter) }

// SetLogOutput sets the logger output for the unifi package.
func SetLogOutput(output io.Writer) { log.SetOutput(output) }

// Client wraps the go-unifi sitemanager client.
type Client struct {
	api sitemanager.SiteManagerAPIClient
}

// NewClient creates a new UniFi API client.
func NewClient(apiKey string, baseURL string) (*Client, error) {
	cfg := &sitemanager.ClientConfig{
		APIKey: apiKey,
	}
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}

	client, err := sitemanager.NewWithConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating unifi client: %w", err)
	}

	return &Client{api: client}, nil
}

// FlatDevice is a flattened representation of a UniFi device for easier processing.
// It combines fields from DeviceItem and its parent Device (host info).
type FlatDevice struct {
	// Device fields
	ID             string    `json:"id"`
	MAC            string    `json:"mac"`
	IP             string    `json:"ip"`
	Model          string    `json:"model"`
	Name           string    `json:"name"`
	Shortname      string    `json:"shortname"`
	Version        string    `json:"version"`
	Status         string    `json:"status"`
	ProductLine    string    `json:"productLine"`
	FirmwareStatus string    `json:"firmwareStatus"`
	IsManaged      bool      `json:"isManaged"`
	IsConsole      bool      `json:"isConsole"`
	Note           string    `json:"note"`
	AdoptionTime   time.Time `json:"adoptionTime,omitempty"`
	StartupTime    time.Time `json:"startupTime,omitempty"`

	// Host context
	HostID   string `json:"hostId"`
	HostName string `json:"hostName"`
}

// GetAllDevices fetches all devices from the UniFi Site Manager API,
// handling pagination via nextToken.
func (c *Client) GetAllDevices(ctx context.Context, hostIDs []string) ([]FlatDevice, error) {
	var allDevices []FlatDevice

	var params sitemanager.ListDevicesParams
	if len(hostIDs) > 0 {
		params.HostIds = &hostIDs
	}
	pageSize := "200"
	params.PageSize = &pageSize

	for {
		if err := ctx.Err(); err != nil {
			return allDevices, err
		}

		resp, err := c.api.ListDevices(ctx, &params)
		if err != nil {
			return nil, fmt.Errorf("listing devices: %w", err)
		}

		for _, host := range resp.Data {
			hostID := ptrStr(host.HostId)
			hostName := ptrStr(host.HostName)

			if host.Devices == nil {
				continue
			}
			for _, d := range *host.Devices {
				fd := FlatDevice{
					ID:             ptrStr(d.Id),
					MAC:            ptrStr(d.Mac),
					IP:             ptrStr(d.Ip),
					Model:          ptrStr(d.Model),
					Name:           ptrStr(d.Name),
					Shortname:      ptrStr(d.Shortname),
					Version:        ptrStr(d.Version),
					Status:         ptrStr(d.Status),
					ProductLine:    ptrStr(d.ProductLine),
					FirmwareStatus: ptrStr(d.FirmwareStatus),
					IsManaged:      ptrBool(d.IsManaged),
					IsConsole:      ptrBool(d.IsConsole),
					Note:           ptrStr(d.Note),
					HostID:         hostID,
					HostName:       hostName,
				}
				if d.AdoptionTime != nil {
					fd.AdoptionTime = *d.AdoptionTime
				}
				if d.StartupTime != nil {
					fd.StartupTime = *d.StartupTime
				}
				allDevices = append(allDevices, fd)
			}
		}

		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		params.NextToken = resp.NextToken
	}

	return allDevices, nil
}

// GetAllSites fetches all sites from the UniFi Site Manager API.
func (c *Client) GetAllSites(ctx context.Context) ([]sitemanager.Site, error) {
	resp, err := c.api.ListSites(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing sites: %w", err)
	}
	return resp.Data, nil
}

// SaveDevicesCache writes devices to a JSON cache file.
func SaveDevicesCache(cacheDir string, devices []FlatDevice) error {
	return writeJSON(cacheDir, "devices.json", devices)
}

// LoadDevicesCache reads devices from a JSON cache file.
func LoadDevicesCache(cacheDir string) ([]FlatDevice, error) {
	path := filepath.Join(cacheDir, "devices.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var devices []FlatDevice
	if err := json.Unmarshal(data, &devices); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return devices, nil
}

// FilterByProductLine filters devices by configured product lines.
func FilterByProductLine(devices []FlatDevice, productLines []string) []FlatDevice {
	if len(productLines) == 0 {
		return devices
	}
	allowed := make(map[string]bool)
	for _, pl := range productLines {
		allowed[strings.ToLower(pl)] = true
	}
	var filtered []FlatDevice
	for _, d := range devices {
		if allowed[strings.ToLower(d.ProductLine)] {
			filtered = append(filtered, d)
		}
	}
	log.Infof("Filtered to %d devices (from %d) by product line: %v", len(filtered), len(devices), productLines)
	return filtered
}

func writeJSON(dir, filename string, v any) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", filename, err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ptrBool(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}
