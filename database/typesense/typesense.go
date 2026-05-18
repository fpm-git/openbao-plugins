// Copyright (c) 2026 OpenBao a Series of LF Projects, LLC
// Copyright (c) 2026 Floatplane Media
// SPDX-License-Identifier: MPL-2.0

package typesense

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	dbplugin "github.com/openbao/openbao/sdk/v2/database/dbplugin/v5"
)

// New returns a new Typesense database implementation.
func New() (interface{}, error) {
	db := &typesenseDB{
		client: &http.Client{Timeout: 10 * time.Second},
	}
	// Use the middleware to prevent leaking the Typesense admin API key in error messages
	return dbplugin.NewDatabaseErrorSanitizerMiddleware(db, db.secretValues), nil
}

// typesenseDB implements the dbplugin.Database interface
type typesenseDB struct {
	client *http.Client
	apiURL string
	apiKey string
}

// secretValues tells the sanitizer which strings to redact from logs/errors
func (db *typesenseDB) secretValues() map[string]string {
	return map[string]string{
		db.apiKey: "[typesense-admin-key]",
	}
}

// Initialize configures the plugin and verifies the connection
func (db *typesenseDB) Initialize(ctx context.Context, req dbplugin.InitializeRequest) (dbplugin.InitializeResponse, error) {
	apiURL, ok := req.Config["api_url"].(string)
	if !ok || apiURL == "" {
		return dbplugin.InitializeResponse{}, errors.New("api_url is required")
	}
	apiKey, ok := req.Config["api_key"].(string)
	if !ok || apiKey == "" {
		return dbplugin.InitializeResponse{}, errors.New("api_key is required")
	}

	db.apiURL = apiURL
	db.apiKey = apiKey

	if req.VerifyConnection {
		// Ping the Typesense health endpoint
		httpReq, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/health", db.apiURL), nil)
		if err != nil {
			return dbplugin.InitializeResponse{}, err
		}

		resp, err := db.client.Do(httpReq)
		if err != nil {
			return dbplugin.InitializeResponse{}, fmt.Errorf("failed to connect to Typesense: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return dbplugin.InitializeResponse{}, fmt.Errorf("typesense returned non-200 status: %d", resp.StatusCode)
		}
	}

	return dbplugin.InitializeResponse{
		Config: req.Config,
	}, nil
}

// NewUser creates a new API key in Typesense
func (db *typesenseDB) NewUser(ctx context.Context, req dbplugin.NewUserRequest) (dbplugin.NewUserResponse, error) {
	// 1. Generate the username dynamically using OpenBao's contextual metadata
	// Example: v-myusername-myrole-1678829283
	generatedUsername := fmt.Sprintf("v-%s-%s-%d", req.UsernameConfig.DisplayName, req.UsernameConfig.RoleName, time.Now().Unix())

	// 2. Prepare the Typesense Key payload
	payloadStr := `{"actions": ["*"], "collections": ["*"]}`
	if len(req.Statements.Commands) > 0 {
		payloadStr = req.Statements.Commands[0]
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		return dbplugin.NewUserResponse{}, fmt.Errorf("failed to parse creation_statements as JSON: %w", err)
	}

	// 3. Inject the dynamically generated identity and password
	payload["description"] = generatedUsername
	payload["value"] = req.Password
	if !req.Expiration.IsZero() {
		payload["expires_at"] = req.Expiration.Unix()
	}

	payloadBytes, _ := json.Marshal(payload)

	// 4. Send request to Typesense
	httpReq, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/keys", db.apiURL), bytes.NewBuffer(payloadBytes))
	if err != nil {
		return dbplugin.NewUserResponse{}, err
	}
	httpReq.Header.Set("X-TYPESENSE-API-KEY", db.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := db.client.Do(httpReq)
	if err != nil {
		return dbplugin.NewUserResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return dbplugin.NewUserResponse{}, fmt.Errorf("failed to create Typesense key: %s (status %d)", string(bodyBytes), resp.StatusCode)
	}

	// 5. Return the generated username back to OpenBao so it can track it
	return dbplugin.NewUserResponse{
		Username: generatedUsername,
	}, nil
}

// DeleteUser revokes the API key from Typesense
func (db *typesenseDB) DeleteUser(ctx context.Context, req dbplugin.DeleteUserRequest) (dbplugin.DeleteUserResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/keys", db.apiURL), nil)
	if err != nil {
		return dbplugin.DeleteUserResponse{}, err
	}
	httpReq.Header.Set("X-TYPESENSE-API-KEY", db.apiKey)

	resp, err := db.client.Do(httpReq)
	if err != nil {
		return dbplugin.DeleteUserResponse{}, err
	}
	defer resp.Body.Close()

	var keysResponse struct {
		Keys []struct {
			ID          int    `json:"id"`
			Description string `json:"description"`
		} `json:"keys"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&keysResponse); err != nil {
		return dbplugin.DeleteUserResponse{}, err
	}

	var keyIDToDelete int = -1
	for _, k := range keysResponse.Keys {
		// OpenBao gives us back the exact username we generated during NewUser
		if k.Description == req.Username {
			keyIDToDelete = k.ID
			break
		}
	}

	if keyIDToDelete == -1 {
		return dbplugin.DeleteUserResponse{}, nil // Key already gone
	}

	delReq, err := http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("%s/keys/%d", db.apiURL, keyIDToDelete), nil)
	if err != nil {
		return dbplugin.DeleteUserResponse{}, err
	}
	delReq.Header.Set("X-TYPESENSE-API-KEY", db.apiKey)

	delResp, err := db.client.Do(delReq)
	if err != nil {
		return dbplugin.DeleteUserResponse{}, err
	}
	defer delResp.Body.Close()

	if delResp.StatusCode != http.StatusOK && delResp.StatusCode != http.StatusNotFound {
		return dbplugin.DeleteUserResponse{}, fmt.Errorf("failed to delete key: status %d", delResp.StatusCode)
	}

	return dbplugin.DeleteUserResponse{}, nil
}

// UpdateUser is not supported for Typesense keys.
func (db *typesenseDB) UpdateUser(ctx context.Context, req dbplugin.UpdateUserRequest) (dbplugin.UpdateUserResponse, error) {
	return dbplugin.UpdateUserResponse{}, errors.New("update/rotation is not supported for typesense keys")
}

// Type returns the type of the database.
func (db *typesenseDB) Type() (string, error) {
	return "typesense", nil
}

// Close is a no-op for Typesense.
func (db *typesenseDB) Close() error {
	return nil
}
