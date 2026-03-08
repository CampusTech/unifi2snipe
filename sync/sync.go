// Package sync implements the core synchronization logic between
// UniFi Site Manager and Snipe-IT.
package sync

import (
	"context"
	"fmt"
	"html"
	"io"
	"strings"

	snipeit "github.com/michellepellon/go-snipeit"
	"github.com/sirupsen/logrus"

	"github.com/CampusTech/unifi2snipe/config"
	"github.com/CampusTech/unifi2snipe/snipe"
	"github.com/CampusTech/unifi2snipe/unifi"
)

var log = logrus.New()

// SetLogLevel sets the logger level.
func SetLogLevel(level logrus.Level) { log.SetLevel(level) }

// SetLogFormatter sets the logger formatter.
func SetLogFormatter(formatter logrus.Formatter) { log.SetFormatter(formatter) }

// SetLogOutput sets the logger output.
func SetLogOutput(output io.Writer) { log.SetOutput(output) }

// Stats tracks sync operation counts.
type Stats struct {
	Total    int
	Created  int
	Updated  int
	Skipped  int
	Errors   int
	ModelNew int
}

// Engine performs the sync between UniFi and Snipe-IT.
type Engine struct {
	unifi  *unifi.Client
	snipe  *snipe.Client
	cfg    *config.Config
	models map[string]int // model identifier -> snipe model ID
	stats  Stats
	cache  []unifi.FlatDevice // populated when using --use-cache
}

// NewEngine creates a new sync engine.
func NewEngine(unifiClient *unifi.Client, snipeClient *snipe.Client, cfg *config.Config) *Engine {
	return &Engine{
		unifi:  unifiClient,
		snipe:  snipeClient,
		cfg:    cfg,
		models: make(map[string]int),
	}
}

// NewDownloadEngine creates a lightweight engine for downloading UniFi data
// without needing a Snipe-IT client.
func NewDownloadEngine(unifiClient *unifi.Client, cfg *config.Config) *Engine {
	return &Engine{
		unifi: unifiClient,
		cfg:   cfg,
	}
}

// CacheDir returns the configured cache directory, defaulting to ".cache".
func (e *Engine) CacheDir() string {
	if e.cfg.Sync.CacheDir != "" {
		return e.cfg.Sync.CacheDir
	}
	return ".cache"
}

// FetchAndSaveCache fetches all devices from UniFi and writes them to cache.
func (e *Engine) FetchAndSaveCache(ctx context.Context) ([]unifi.FlatDevice, error) {
	log.Info("Fetching all devices from UniFi Site Manager...")
	devices, err := e.unifi.GetAllDevices(ctx, e.cfg.Sync.HostIDs)
	if err != nil {
		return nil, fmt.Errorf("fetching UniFi devices: %w", err)
	}
	log.Infof("Fetched %d devices from UniFi", len(devices))

	devices = unifi.FilterByProductLine(devices, e.cfg.Sync.ProductLines)

	if devices == nil {
		devices = []unifi.FlatDevice{}
	}
	if err := unifi.SaveDevicesCache(e.CacheDir(), devices); err != nil {
		return nil, fmt.Errorf("writing devices cache: %w", err)
	}
	log.Infof("Saved %d devices to %s/devices.json", len(devices), e.CacheDir())
	return devices, nil
}

// LoadCache reads UniFi cache from JSON files in the cache directory.
func (e *Engine) LoadCache() error {
	devices, err := unifi.LoadDevicesCache(e.CacheDir())
	if err != nil {
		return err
	}
	log.Infof("Loaded cache from %s/ (%d devices)", e.CacheDir(), len(devices))
	e.cache = devices
	return nil
}

// RunSingle syncs a single device identified by MAC address.
func (e *Engine) RunSingle(ctx context.Context, mac string) (*Stats, error) {
	mac = strings.ToUpper(strings.ReplaceAll(mac, ":", ""))
	log.Infof("Syncing single device: %s", mac)

	if err := e.loadModels(ctx); err != nil {
		return nil, fmt.Errorf("loading snipe models: %w", err)
	}

	var device *unifi.FlatDevice
	if e.cache != nil {
		for _, d := range e.cache {
			if strings.EqualFold(normalizeMAC(d.MAC), mac) {
				device = &d
				break
			}
		}
		if device == nil {
			return nil, fmt.Errorf("device %s not found in cache", mac)
		}
	} else {
		devices, err := e.unifi.GetAllDevices(ctx, e.cfg.Sync.HostIDs)
		if err != nil {
			return nil, fmt.Errorf("fetching devices from UniFi: %w", err)
		}
		for _, d := range devices {
			if strings.EqualFold(normalizeMAC(d.MAC), mac) {
				device = &d
				break
			}
		}
		if device == nil {
			return nil, fmt.Errorf("device %s not found in UniFi", mac)
		}
	}

	if err := e.processDevice(ctx, *device); err != nil {
		log.WithError(err).WithField("mac", mac).Error("Failed to process device")
		e.stats.Errors++
	}

	return &e.stats, nil
}

// Run executes the full sync process.
func (e *Engine) Run(ctx context.Context) (*Stats, error) {
	log.Info("Starting unifi2snipe sync")

	if err := e.loadModels(ctx); err != nil {
		return nil, fmt.Errorf("loading snipe models: %w", err)
	}
	log.Infof("Loaded %d existing models from Snipe-IT", len(e.models))

	devices, err := e.fetchDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching devices: %w", err)
	}
	log.Infof("Processing %d devices", len(devices))

	for i, device := range devices {
		if err := ctx.Err(); err != nil {
			return &e.stats, err
		}

		if err := e.processDevice(ctx, device); err != nil {
			log.WithError(err).WithField("mac", device.MAC).Error("Failed to process device")
			e.stats.Errors++
		}

		if (i+1)%50 == 0 {
			log.WithFields(logrus.Fields{"progress": i + 1, "total": len(devices)}).Info("Processing devices")
		}
	}

	log.Infof("Sync complete: total=%d created=%d updated=%d skipped=%d errors=%d new_models=%d",
		e.stats.Total, e.stats.Created, e.stats.Updated, e.stats.Skipped, e.stats.Errors, e.stats.ModelNew)

	return &e.stats, nil
}

// loadModels fetches all models from Snipe-IT and builds a lookup map.
func (e *Engine) loadModels(ctx context.Context) error {
	models, err := e.snipe.ListAllModels(ctx)
	if err != nil {
		return err
	}
	for _, m := range models {
		if m.ModelNumber != "" {
			e.models[m.ModelNumber] = m.ID
		}
		if m.Name != "" {
			e.models[m.Name] = m.ID
		}
	}
	return nil
}

// fetchDevices retrieves all devices from UniFi (or cache), with optional filtering.
func (e *Engine) fetchDevices(ctx context.Context) ([]unifi.FlatDevice, error) {
	var devices []unifi.FlatDevice

	if e.cache != nil {
		devices = e.cache
		log.Infof("Using %d cached devices", len(devices))
	} else {
		var err error
		devices, err = e.unifi.GetAllDevices(ctx, e.cfg.Sync.HostIDs)
		if err != nil {
			return nil, err
		}
	}

	return unifi.FilterByProductLine(devices, e.cfg.Sync.ProductLines), nil
}

// processDevice handles a single UniFi device - creating or updating it in Snipe-IT.
func (e *Engine) processDevice(ctx context.Context, device unifi.FlatDevice) error {
	e.stats.Total++

	if device.MAC == "" {
		log.WithField("device_id", device.ID).Debug("Skipping device with no MAC address")
		e.stats.Skipped++
		return nil
	}

	mac := formatMAC(device.MAC)
	logger := log.WithField("mac", mac)

	// Use MAC as the unique identifier for lookup (stored as serial in Snipe-IT)
	existing, err := e.snipe.GetAssetBySerial(ctx, mac)
	if err != nil {
		return fmt.Errorf("looking up MAC %s: %w", mac, err)
	}

	if existing.Total == 0 && e.cfg.Sync.UpdateOnly {
		logger.Info("Skipping asset not found in Snipe-IT (update_only mode)")
		e.stats.Skipped++
		return nil
	}

	switch existing.Total {
	case 0:
		modelID, err := e.ensureModel(ctx, device)
		if err != nil {
			return fmt.Errorf("ensuring model for %s: %w", mac, err)
		}
		return e.createAsset(ctx, logger, device, modelID)
	case 1:
		logger = logger.WithField("snipe_id", existing.Rows[0].ID)
		return e.updateAsset(ctx, logger, device, &existing.Rows[0])
	default:
		logger.Warnf("Multiple assets (%d) found for MAC, skipping", existing.Total)
		e.stats.Skipped++
		return nil
	}
}

// ensureModel checks if the device model exists in Snipe-IT, creating it if needed.
func (e *Engine) ensureModel(ctx context.Context, device unifi.FlatDevice) (int, error) {
	// Try matching by model identifier (e.g. "U6-Pro", "USW-24-PoE")
	if device.Model != "" {
		if id, ok := e.models[device.Model]; ok {
			return id, nil
		}
	}

	// Try matching by shortname (e.g. "U6 Pro")
	if device.Shortname != "" {
		if id, ok := e.models[device.Shortname]; ok {
			return id, nil
		}
	}

	if device.Model == "" && device.Shortname == "" {
		return 0, fmt.Errorf("device has no model identifier")
	}

	// Use shortname as display name, model as model number
	modelName := device.Shortname
	modelNumber := device.Model
	if modelName == "" {
		modelName = modelNumber
	}
	if modelNumber == "" {
		modelNumber = modelName
	}

	if e.cfg.Sync.UpdateOnly {
		log.WithFields(logrus.Fields{
			"model_name":   modelName,
			"model_number": modelNumber,
		}).Warn("Model not found in Snipe-IT and update_only mode is enabled, skipping")
		return 0, fmt.Errorf("model %q not found (update_only mode)", modelName)
	}

	if e.cfg.Sync.DryRun {
		log.WithFields(logrus.Fields{
			"model_name":   modelName,
			"model_number": modelNumber,
		}).Info("[DRY RUN] Would create model")
		e.stats.ModelNew++
		return 0, nil
	}

	productLine := device.ProductLine
	if device.IsConsole {
		productLine = "console"
	}

	model := snipeit.Model{
		CommonFields: snipeit.CommonFields{Name: modelName},
		ModelNumber:  modelNumber,
		Category: snipeit.Category{
			CommonFields: snipeit.CommonFields{ID: e.cfg.SnipeIT.CategoryIDForProductLine(productLine)},
		},
		Manufacturer: snipeit.Manufacturer{
			CommonFields: snipeit.CommonFields{ID: e.cfg.SnipeIT.ManufacturerID},
		},
		FieldsetID: e.cfg.SnipeIT.CustomFieldsetID,
	}

	newModel, err := e.snipe.CreateModel(ctx, model)
	if err != nil {
		return 0, err
	}

	log.WithFields(logrus.Fields{
		"model_name":   modelName,
		"model_number": modelNumber,
		"snipe_id":     newModel.ID,
	}).Info("Created new model in Snipe-IT")

	e.models[modelName] = newModel.ID
	e.models[modelNumber] = newModel.ID
	e.stats.ModelNew++
	return newModel.ID, nil
}

// createAsset creates a new asset in Snipe-IT from UniFi device data.
func (e *Engine) createAsset(ctx context.Context, logger *logrus.Entry, device unifi.FlatDevice, modelID int) error {
	mac := formatMAC(device.MAC)

	asset := snipeit.Asset{
		CommonFields: snipeit.CommonFields{
			CustomFields: make(map[string]string),
		},
		Serial:   mac,
		AssetTag: mac,
		Model: snipeit.Model{
			CommonFields: snipeit.CommonFields{ID: modelID},
		},
		StatusLabel: snipeit.StatusLabel{
			CommonFields: snipeit.CommonFields{ID: e.cfg.SnipeIT.DefaultStatusID},
		},
	}

	if e.cfg.Sync.SetName && device.Name != "" {
		asset.Name = device.Name
	}

	if locID := e.cfg.Sync.LocationIDForHost(device.HostID, device.HostName); locID != 0 {
		asset.Location = snipeit.Location{
			CommonFields: snipeit.CommonFields{ID: locID},
		}
	}

	e.applyFieldMapping(&asset, device)

	if e.cfg.Sync.DryRun {
		logger.WithField("payload", asset).Info("[DRY RUN] Would create asset")
		if e.cfg.Sync.Checkout {
			if locID := e.cfg.Sync.LocationIDForHost(device.HostID, device.HostName); locID != 0 {
				checkoutDate := ""
				if !device.AdoptionTime.IsZero() {
					checkoutDate = device.AdoptionTime.Format("2006-01-02")
				}
				logger.WithFields(logrus.Fields{
					"location_id":   locID,
					"checkout_date": checkoutDate,
				}).Info("[DRY RUN] Would checkout asset to location")
			}
		}
		e.stats.Created++
		return nil
	}

	created, err := e.snipe.CreateAsset(ctx, asset)
	if err != nil {
		return err
	}

	logger.WithField("snipe_id", created.ID).Info("Created asset in Snipe-IT")
	e.stats.Created++

	// Checkout to location if enabled and configured
	if e.cfg.Sync.Checkout {
		if locID := e.cfg.Sync.LocationIDForHost(device.HostID, device.HostName); locID != 0 {
			checkoutDate := ""
			if !device.AdoptionTime.IsZero() {
				checkoutDate = device.AdoptionTime.Format("2006-01-02")
			}
			if err := e.snipe.CheckoutToLocation(ctx, created.ID, locID, checkoutDate); err != nil {
				logger.WithError(err).WithFields(logrus.Fields{
					"snipe_id":    created.ID,
					"location_id": locID,
				}).Warn("Failed to checkout asset to location")
			} else {
				logger.WithFields(logrus.Fields{
					"snipe_id":      created.ID,
					"location_id":   locID,
					"checkout_date": checkoutDate,
				}).Info("Checked out asset to location")
			}
		}
	}

	return nil
}

// updateAsset updates an existing Snipe-IT asset with current UniFi data.
func (e *Engine) updateAsset(ctx context.Context, logger *logrus.Entry, device unifi.FlatDevice, existing *snipeit.Asset) error {
	desired := snipeit.Asset{
		CommonFields: snipeit.CommonFields{
			CustomFields: make(map[string]string),
		},
	}

	if locID := e.cfg.Sync.LocationIDForHost(device.HostID, device.HostName); locID != 0 {
		desired.Location = snipeit.Location{
			CommonFields: snipeit.CommonFields{ID: locID},
		}
	}

	e.applyFieldMapping(&desired, device)

	// Check if checkout is needed (independent of field changes)
	var needsCheckout bool
	var checkoutLocID int
	if e.cfg.Sync.Checkout {
		if locID := e.cfg.Sync.LocationIDForHost(device.HostID, device.HostName); locID != 0 {
			// status_meta is "deployable" when not checked out, "deployed" when checked out
			if existing.StatusLabel.StatusMeta == "deployable" {
				needsCheckout = true
				checkoutLocID = locID
			} else if existing.User != nil && existing.User.ID == locID {
				// Already checked out to the correct location
				logger.Debug("Asset already checked out to correct location")
			} else if existing.StatusLabel.StatusMeta != "deployable" {
				logger.WithFields(logrus.Fields{
					"status":      existing.StatusLabel.Name,
					"status_meta": existing.StatusLabel.StatusMeta,
					"location_id": locID,
				}).Debug("Skipping checkout — asset is not available for checkout")
			}
		}
	}

	update := &desired
	if !e.cfg.Sync.Force {
		update = diffAsset(&desired, existing)
		if update == nil && !needsCheckout {
			logger.Debug("All fields already match, skipping update")
			e.stats.Skipped++
			return nil
		}
	}

	checkoutDate := ""
	if !device.AdoptionTime.IsZero() {
		checkoutDate = device.AdoptionTime.Format("2006-01-02")
	}

	if e.cfg.Sync.DryRun {
		if update != nil {
			logger.WithFields(logrus.Fields{
				"snipe_id": existing.ID,
				"updates":  formatAssetDiff(update),
			}).Info("[DRY RUN] Would update asset")
		}
		if needsCheckout {
			logger.WithFields(logrus.Fields{
				"snipe_id":      existing.ID,
				"location_id":   checkoutLocID,
				"checkout_date": checkoutDate,
			}).Info("[DRY RUN] Would checkout asset to location")
		}
		if update != nil {
			e.stats.Updated++
		} else {
			e.stats.Skipped++
		}
		return nil
	}

	if update != nil {
		if update.Model.ID == 0 {
			update.Model = existing.Model
		}

		if _, err := e.snipe.PatchAsset(ctx, existing.ID, *update); err != nil {
			return err
		}

		logger.WithField("snipe_id", existing.ID).Info("Updated asset in Snipe-IT")
		e.stats.Updated++
	} else {
		e.stats.Skipped++
	}

	// Checkout to location if configured and not already assigned
	if needsCheckout {
		if err := e.snipe.CheckoutToLocation(ctx, existing.ID, checkoutLocID, checkoutDate); err != nil {
			logger.WithError(err).WithFields(logrus.Fields{
				"snipe_id":    existing.ID,
				"location_id": checkoutLocID,
			}).Warn("Failed to checkout asset to location")
		} else {
			logger.WithFields(logrus.Fields{
				"snipe_id":      existing.ID,
				"location_id":   checkoutLocID,
				"checkout_date": checkoutDate,
			}).Info("Checked out asset to location")
		}
	}

	return nil
}

// diffAsset compares desired asset values against the existing Snipe-IT asset
// and returns an asset containing only the fields that differ, or nil if everything matches.
func diffAsset(desired *snipeit.Asset, existing *snipeit.Asset) *snipeit.Asset {
	diff := snipeit.Asset{
		CommonFields: snipeit.CommonFields{
			CustomFields: make(map[string]string),
		},
	}
	hasChanges := false

	if desired.Name != "" && desired.Name != existing.Name {
		diff.Name = desired.Name
		hasChanges = true
	}

	if desired.Location.ID != 0 && desired.Location.ID != existing.Location.ID {
		diff.Location = desired.Location
		hasChanges = true
	}

	for key, desiredVal := range desired.CustomFields {
		currentVal := html.UnescapeString(existing.CustomFields[key])
		if normalizeBoolStr(currentVal) != normalizeBoolStr(desiredVal) {
			diff.CustomFields[key] = desiredVal
			hasChanges = true
		}
	}

	if !hasChanges {
		return nil
	}
	return &diff
}

// applyFieldMapping applies user-configured field mappings from config.
func (e *Engine) applyFieldMapping(asset *snipeit.Asset, device unifi.FlatDevice) {
	for snipeField, unifiField := range e.cfg.Sync.FieldMapping {
		var value string
		switch strings.ToLower(unifiField) {
		case "mac":
			value = formatMAC(device.MAC)
		case "ip":
			value = device.IP
		case "name":
			value = device.Name
		case "model":
			value = device.Model
		case "shortname":
			value = device.Shortname
		case "version", "firmware":
			value = device.Version
		case "status":
			value = device.Status
		case "product_line", "productline":
			value = device.ProductLine
		case "firmware_status", "firmwarestatus":
			value = device.FirmwareStatus
		case "is_managed", "ismanaged":
			value = fmt.Sprintf("%t", device.IsManaged)
		case "is_console", "isconsole":
			value = fmt.Sprintf("%t", device.IsConsole)
		case "note":
			value = device.Note
		case "host_id", "hostid":
			value = device.HostID
		case "host_name", "hostname":
			value = device.HostName
		case "adoption_time", "adoptiontime":
			if !device.AdoptionTime.IsZero() {
				value = device.AdoptionTime.Format("2006-01-02")
			}
		case "startup_time", "startuptime":
			if !device.StartupTime.IsZero() {
				value = device.StartupTime.Format("2006-01-02 15:04:05")
			}
		}
		if value != "" {
			switch snipeField {
			case "name":
				asset.Name = value
			case "asset_tag":
				asset.AssetTag = value
			default:
				asset.CustomFields[snipeField] = value
			}
		}
	}
}

// formatMAC inserts colons into a raw MAC address (e.g. "2cca164bd29d" -> "2C:CA:16:4B:D2:9D").
func formatMAC(s string) string {
	raw := strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(s), ":", ""), "-", "")
	if len(raw) != 12 {
		return strings.ToUpper(s)
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		raw[0:2], raw[2:4], raw[4:6], raw[6:8], raw[8:10], raw[10:12])
}

// normalizeMAC strips separators and uppercases a MAC address.
func normalizeMAC(s string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(s, ":", ""), "-", ""))
}

func normalizeBoolStr(s string) string {
	switch strings.ToLower(s) {
	case "0", "false":
		return "false"
	case "1", "true":
		return "true"
	default:
		return strings.ToLower(s)
	}
}

func formatAssetDiff(a *snipeit.Asset) map[string]any {
	m := make(map[string]any)
	if a.Name != "" {
		m["name"] = a.Name
	}
	for k, v := range a.CustomFields {
		m[k] = v
	}
	return m
}
