// Package snipe wraps the go-snipeit library with dry-run enforcement and
// convenience methods for the unifi2snipe sync process.
package snipe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	snipeit "github.com/michellepellon/go-snipeit"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

// SetLogLevel sets the logger level for the snipe package.
func SetLogLevel(level logrus.Level) { log.SetLevel(level) }

// SetLogFormatter sets the logger formatter for the snipe package.
func SetLogFormatter(formatter logrus.Formatter) { log.SetFormatter(formatter) }

// SetLogOutput sets the logger output for the snipe package.
func SetLogOutput(output io.Writer) { log.SetOutput(output) }

// ErrDryRun is returned when a write operation is attempted in dry-run mode.
var ErrDryRun = fmt.Errorf("write blocked: dry-run mode is enabled")

// Client wraps the go-snipeit client with dry-run enforcement.
type Client struct {
	*snipeit.Client
	DryRun bool
}

type snipeLogger struct{}

func (l *snipeLogger) LogRequest(method, url string, body []byte) {
	log.WithFields(logrus.Fields{"method": method, "url": url}).Debug("snipe-it request")
}

func (l *snipeLogger) LogResponse(method, url string, statusCode int, body []byte) {
	log.WithFields(logrus.Fields{"method": method, "url": url, "status": statusCode}).Debug("snipe-it response")
}

// NewClient creates a new Snipe-IT client.
func NewClient(baseURL, apiKey string, rateLimit bool) (*Client, error) {
	baseURL = strings.TrimRight(baseURL, "/")

	opts := &snipeit.ClientOptions{
		Logger: &snipeLogger{},
	}
	if rateLimit {
		opts.RateLimiter = snipeit.NewTokenBucketRateLimiter(2, 5)
	}

	sc, err := snipeit.NewClientWithOptions(baseURL, apiKey, opts)
	if err != nil {
		return nil, fmt.Errorf("creating snipe-it client: %w", err)
	}

	return &Client{Client: sc}, nil
}

// ListAllModels returns all models from Snipe-IT, handling pagination.
func (c *Client) ListAllModels(ctx context.Context) ([]snipeit.Model, error) {
	var all []snipeit.Model
	offset := 0
	limit := 500

	for {
		resp, _, err := c.Models.ListContext(ctx, &snipeit.ListOptions{Limit: limit, Offset: offset})
		if err != nil {
			return nil, fmt.Errorf("listing models: %w", err)
		}
		all = append(all, resp.Rows...)
		if len(all) >= resp.Total {
			break
		}
		offset += limit
	}

	return all, nil
}

// CreateModel creates a new asset model in Snipe-IT.
func (c *Client) CreateModel(ctx context.Context, model snipeit.Model) (*snipeit.Model, error) {
	if c.DryRun {
		return nil, ErrDryRun
	}
	resp, _, err := c.Models.CreateContext(ctx, model)
	if err != nil {
		return nil, fmt.Errorf("creating model: %w", err)
	}
	if resp.Status != "success" {
		return nil, fmt.Errorf("creating model failed: %s", resp.Message)
	}
	return &resp.Payload, nil
}

// GetAssetBySerial looks up an asset by serial number.
func (c *Client) GetAssetBySerial(ctx context.Context, serial string) (*snipeit.AssetsResponse, error) {
	resp, _, err := c.Assets.GetAssetBySerialContext(ctx, serial)
	if err != nil {
		return nil, fmt.Errorf("looking up serial %s: %w", serial, err)
	}
	// Filter to exact matches only.
	exact := resp.Rows[:0]
	for _, a := range resp.Rows {
		if strings.EqualFold(a.Serial, serial) {
			exact = append(exact, a)
		}
	}
	resp.Rows = exact
	resp.Total = len(exact)
	return resp, nil
}

// CreateAsset creates a new hardware asset.
func (c *Client) CreateAsset(ctx context.Context, asset snipeit.Asset) (*snipeit.Asset, error) {
	if c.DryRun {
		return nil, ErrDryRun
	}
	resp, _, err := c.Assets.CreateContext(ctx, asset)
	if err != nil {
		return nil, fmt.Errorf("creating asset: %w", err)
	}
	if resp.Status != "success" {
		return nil, fmt.Errorf("creating asset failed: %s", resp.Message)
	}
	return &resp.Payload, nil
}

// PatchAsset partially updates an existing hardware asset by ID.
func (c *Client) PatchAsset(ctx context.Context, id int, asset snipeit.Asset) (*snipeit.Asset, error) {
	if c.DryRun {
		return nil, ErrDryRun
	}
	resp, _, err := c.Assets.PatchContext(ctx, id, asset)
	if err != nil {
		return nil, fmt.Errorf("updating asset %d: %w", id, err)
	}
	if resp.Status != "success" {
		rejected, reason := invalidFieldErrors(string(resp.Message))
		if len(rejected) > 0 && asset.CustomFields != nil {
			log.WithFields(logrus.Fields{
				"asset_id": id,
				"fields":   rejected,
				"reason":   reason,
			}).Warn("Snipe-IT rejected custom fields — retrying without them. Run 'unifi2snipe setup' to fix field configuration.")
			fieldsCopy := make(map[string]string, len(asset.CustomFields))
			for k, v := range asset.CustomFields {
				fieldsCopy[k] = v
			}
			for _, key := range rejected {
				delete(fieldsCopy, key)
			}
			asset.CustomFields = fieldsCopy
			resp, _, err = c.Assets.PatchContext(ctx, id, asset)
			if err != nil {
				return nil, fmt.Errorf("updating asset %d: %w", id, err)
			}
			if resp.Status != "success" {
				return nil, fmt.Errorf("updating asset %d failed: %s", id, resp.Message)
			}
			return &resp.Payload, nil
		}
		return nil, fmt.Errorf("updating asset %d failed: %s", id, resp.Message)
	}
	return &resp.Payload, nil
}

// CheckoutToLocation checks out an asset to a Snipe-IT location.
// checkoutDate should be in "YYYY-MM-DD" format; pass "" to use today.
func (c *Client) CheckoutToLocation(ctx context.Context, assetID, locationID int, checkoutDate string) error {
	if c.DryRun {
		return ErrDryRun
	}
	params := map[string]interface{}{
		"checkout_to_type":  "location",
		"assigned_location": locationID,
	}
	if checkoutDate != "" {
		params["checkout_at"] = checkoutDate
	}
	resp, _, err := c.Assets.CheckoutContext(ctx, assetID, params)
	if err != nil {
		return fmt.Errorf("checking out asset %d to location %d: %w", assetID, locationID, err)
	}
	if resp.Status != "success" {
		return fmt.Errorf("checking out asset %d to location %d failed: %s", assetID, locationID, resp.Message)
	}
	return nil
}

func invalidFieldErrors(msg string) ([]string, string) {
	var errs map[string][]string
	if err := json.Unmarshal([]byte(msg), &errs); err != nil {
		return nil, ""
	}
	var rejected []string
	reason := ""
fieldLoop:
	for key, msgs := range errs {
		for _, m := range msgs {
			switch {
			case strings.Contains(m, "not available on this Asset Model's fieldset"):
				rejected = append(rejected, key)
				reason = "fieldset missing"
				continue fieldLoop
			case strings.Contains(m, "is invalid."):
				rejected = append(rejected, key)
				reason = "invalid field value"
				continue fieldLoop
			}
		}
	}
	return rejected, reason
}

// FieldDef defines a custom field to create in Snipe-IT.
type FieldDef struct {
	Name        string
	Element     string
	Format      string
	HelpText    string
	FieldValues string
}

// SetupFields creates or updates custom fields in Snipe-IT and associates them
// with the given fieldset. Returns a map of field name -> db_column_name.
func (c *Client) SetupFields(fieldsetID int, fields []FieldDef) (map[string]string, error) {
	if c.DryRun {
		return nil, ErrDryRun
	}
	existing, _, err := c.Fields.List(nil)
	if err != nil {
		return nil, fmt.Errorf("listing existing fields: %w", err)
	}
	existingByName := make(map[string]snipeit.Field)
	for _, f := range existing.Rows {
		existingByName[f.Name] = f
	}

	results := make(map[string]string)

	for _, f := range fields {
		field := snipeit.Field{}
		field.Name = f.Name
		field.Element = f.Element
		field.Format = f.Format
		field.HelpText = f.HelpText
		field.FieldValues = f.FieldValues

		var fieldID int
		var dbColumn string

		if ex, ok := existingByName[f.Name]; ok {
			resp, _, err := c.Fields.Update(ex.ID, field)
			if err != nil {
				return results, fmt.Errorf("updating field %q: %w", f.Name, err)
			}
			if resp.Status != "success" {
				return results, fmt.Errorf("updating field %q: %s", f.Name, resp.Message)
			}
			fieldID = resp.Payload.ID
			dbColumn = resp.Payload.DBColumnName
			if dbColumn == "" {
				dbColumn = ex.DBColumnName
			}
		} else {
			resp, _, err := c.Fields.Create(field)
			if err != nil {
				return results, fmt.Errorf("creating field %q: %w", f.Name, err)
			}
			if resp.Status != "success" {
				return results, fmt.Errorf("creating field %q: %s", f.Name, resp.Message)
			}
			fieldID = resp.Payload.ID
			dbColumn = resp.Payload.DBColumnName
		}

		results[f.Name] = dbColumn

		if fieldsetID > 0 {
			if _, err := c.Fields.Associate(fieldID, fieldsetID); err != nil {
				return results, fmt.Errorf("associating field %q (ID %d) with fieldset %d: %w", f.Name, fieldID, fieldsetID, err)
			}
		}
	}

	// Re-fetch to fill in any missing db_column_name values
	hasMissing := false
	for _, v := range results {
		if v == "" {
			hasMissing = true
			break
		}
	}
	if hasMissing {
		refreshed, _, err := c.Fields.List(nil)
		if err == nil {
			byName := make(map[string]string)
			for _, f := range refreshed.Rows {
				byName[f.Name] = f.DBColumnName
			}
			for name, dbCol := range results {
				if dbCol == "" {
					if col, ok := byName[name]; ok && col != "" {
						results[name] = col
					}
				}
			}
		}
	}

	return results, nil
}
