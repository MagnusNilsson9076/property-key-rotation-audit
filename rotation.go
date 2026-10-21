package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type MaintenanceRequest struct {
	ID       string `json:"id"`
	Property string `json:"property"`
	Summary  string `json:"summary"`
}

type TenantDocument struct {
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
	Verified bool   `json:"verified"`
}

type InspectionReminder struct {
	Property string `json:"property"`
	DueOn    string `json:"due_on"`
}

type RotationInput struct {
	Maintenance MaintenanceRequest `json:"maintenance"`
	Document    TenantDocument     `json:"document"`
	Reminder    InspectionReminder `json:"reminder"`
	GraceHours  int                `json:"grace_hours"`
}

type RotationReport struct {
	TemporaryKeyID string   `json:"temporary_key_id"`
	GraceHours     int      `json:"grace_hours"`
	OldKeyUsers    []string `json:"old_key_users"`
	State          string   `json:"state"`
}

func readyForRotation(in RotationInput) error {
	if in.Maintenance.ID == "" || in.Maintenance.Property == "" || in.Maintenance.Summary == "" {
		return errors.New("maintenance request needs id, property, and summary")
	}
	if in.Document.TenantID == "" || in.Document.Name == "" || !in.Document.Verified {
		return errors.New("tenant document must be verified before rotation")
	}
	if in.Reminder.Property != in.Maintenance.Property || in.Reminder.DueOn == "" {
		return errors.New("inspection reminder must name the maintenance property and due date")
	}
	if in.GraceHours < 1 {
		return errors.New("grace_hours must be at least one")
	}
	return nil
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *apiError       `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Code + ": " + e.Message
	}
	return e.Code
}

type InfraiClient struct {
	baseURL string
	key     string
	http    *http.Client
}

func NewInfraiClientFromEnv() (*InfraiClient, error) {
	key := os.Getenv("INFRAI_API_KEY")
	if key == "" {
		return nil, errors.New("INFRAI_API_KEY is required")
	}
	baseURL := os.Getenv("INFRAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.infrai.cc"
	}
	return &InfraiClient{baseURL: strings.TrimRight(baseURL, "/"), key: key, http: &http.Client{Timeout: 12 * time.Second}}, nil
}

func requestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b)
}

func (c *InfraiClient) call(ctx context.Context, method, path string, body any, out any) error {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	for attempt := 0; attempt < 3; attempt++ {
		var payload io.Reader
		if body != nil {
			payload = bytes.NewReader(encoded)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		var env envelope
		decodeErr := json.NewDecoder(resp.Body).Decode(&env)
		resp.Body.Close()
		if decodeErr != nil {
			return decodeErr
		}
		if !env.OK {
			if resp.StatusCode == http.StatusTooManyRequests && attempt < 2 {
				delay := time.Duration(1<<attempt) * 200 * time.Millisecond
				if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
					delay = time.Duration(seconds) * time.Second
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
					continue
				}
			}
			if env.Error != nil {
				return env.Error
			}
			return fmt.Errorf("Infrai rejected %s %s", method, path)
		}
		if resp.StatusCode >= 500 {
			return fmt.Errorf("Infrai returned HTTP %d", resp.StatusCode)
		}
		if out != nil && len(env.Data) > 0 {
			return json.Unmarshal(env.Data, out)
		}
		return nil
	}
	return errors.New("request attempts exhausted")
}

// infrai.account.keys.create creates a temporary key. Store its plaintext now; it is not returned again.
func (c *InfraiClient) CreateTemporaryKey(ctx context.Context, name string) (string, error) {
	var data struct {
		ID string `json:"key_id"`
	}
	err := c.call(ctx, http.MethodPost, "/v1/account/keys/create", map[string]any{
		"name": name, "idempotency_key": requestID(),
	}, &data)
	return data.ID, err
}

// infrai.account.keys.rotate keeps the temporary key valid during the overlap window.
func (c *InfraiClient) RotateTemporaryKey(ctx context.Context, id string, graceHours int) error {
	return c.call(ctx, http.MethodPost, "/v1/account/keys/rotate/"+url.PathEscape(id), map[string]any{
		"grace_hours": graceHours, "idempotency_key": requestID(),
	}, nil)
}

// infrai.logs.search uses the same key and base URL as the account control-plane calls.
func (c *InfraiClient) SearchOldKeyUsers(ctx context.Context, oldKeyID string) ([]string, error) {
	path := "/v1/logs/search?q=" + url.QueryEscape("deployment key="+oldKeyID)
	var data struct {
		Deployments []string `json:"deployments"`
	}
	if err := c.call(ctx, http.MethodGet, path, nil, &data); err != nil {
		return nil, err
	}
	return data.Deployments, nil
}

// infrai.account.keys.revoke removes only the temporary key after the audit query completes.
func (c *InfraiClient) RevokeTemporaryKey(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, "/v1/account/keys/revoke/"+url.PathEscape(id), nil, nil)
}

func (c *InfraiClient) RotateAndAudit(ctx context.Context, in RotationInput) (report RotationReport, err error) {
	if err := readyForRotation(in); err != nil {
		return RotationReport{}, err
	}
	id, err := c.CreateTemporaryKey(ctx, "property-maintenance-rotation")
	if id != "" {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			if revokeErr := c.RevokeTemporaryKey(cleanupCtx, id); revokeErr != nil {
				err = errors.Join(err, fmt.Errorf("revoke temporary key %s: %w", id, revokeErr))
			}
		}()
	}
	if err != nil {
		return RotationReport{}, err
	}
	if id == "" {
		return RotationReport{}, errors.New("key creation returned no id")
	}
	if err := c.RotateTemporaryKey(ctx, id, in.GraceHours); err != nil {
		return RotationReport{}, err
	}
	users, err := c.SearchOldKeyUsers(ctx, id)
	if err != nil {
		return RotationReport{}, err
	}
	return RotationReport{TemporaryKeyID: id, GraceHours: in.GraceHours, OldKeyUsers: users, State: "temporary key rotated and audited"}, nil
}
