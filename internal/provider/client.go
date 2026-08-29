package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const apiBaseURL = "https://slack.com/api/"

// maxAttempts bounds retries for rate limits and 5xx responses; 404 responses
// are retried exactly once.
const maxAttempts = 5

type SlackClient struct {
	token        string
	http         http.Client
	baseURL      string
	retryBackoff time.Duration

	// ApprovalTimeout is how long slack-app_install waits for an admin
	// approval request to be granted. Set from provider configuration.
	ApprovalTimeout time.Duration

	mu             sync.Mutex
	tokenUserEmail string
	teamID         string
	auth           *authTestResponse
}

func NewSlackClient(token string) *SlackClient {
	return &SlackClient{
		token:           token,
		baseURL:         apiBaseURL,
		retryBackoff:    time.Second,
		ApprovalTimeout: time.Hour,
	}
}

type apiEnvelope struct {
	OK               bool            `json:"ok"`
	Error            string          `json:"error"`
	Errors           json.RawMessage `json:"errors"`
	ResponseMetadata struct {
		Messages []string `json:"messages"`
	} `json:"response_metadata"`
}

// APIError is a Slack API response with ok: false. Code is the machine-readable
// error string (e.g. "app_not_found"); Details carries the optional errors array.
type APIError struct {
	Code    string
	Details string
}

func (e *APIError) Error() string {
	if e.Details != "" {
		return e.Code + ": " + e.Details
	}
	return e.Code
}

// isAppAccessError reports whether err is Slack saying an app ID is no good:
// nonexistent, malformed, or not accessible with this token. Different
// endpoints use different codes for the same situation.
func isAppAccessError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case "app_not_found", "invalid_app_id", "invalid_arguments":
		return true
	}
	return false
}

// appAccessErrorDetail is the human explanation for isAppAccessError failures.
func appAccessErrorDetail(appID string) string {
	return fmt.Sprintf("Slack does not recognize app %q for this token: the app does not exist, "+
		"or it is not accessible with this token. Verify the app ID and that the token's user "+
		"is a collaborator on the app.", appID)
}

type retryClass int

const (
	retryNone retryClass = iota
	retryThrottle
	retryServer
	retryNotFound
)

// attempt performs a single API call and classifies any failure for the retry
// loop in send. delay is only set for throttled responses that carry a
// Retry-After header.
func (c *SlackClient) attempt(ctx context.Context, method, contentType string, body []byte, bearer bool, resultJson interface{}) (retryClass, time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+method, bytes.NewReader(body))
	if err != nil {
		return retryNone, 0, err
	}
	request.Header.Set("Content-Type", contentType)
	if bearer {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	// Transport errors are not retried: the request may already have been
	// applied server-side, and replaying a non-idempotent call (e.g.
	// apps.manifest.create) could apply it twice.
	response, err := c.http.Do(request)
	if err != nil {
		return retryNone, 0, err
	}
	respBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		return retryNone, 0, err
	}

	switch {
	case response.StatusCode == http.StatusTooManyRequests:
		var delay time.Duration
		if secs, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && secs > 0 {
			delay = time.Duration(secs) * time.Second
		}
		return retryThrottle, delay, fmt.Errorf("slack rate limited the request (HTTP 429)")
	case response.StatusCode >= 500:
		return retryServer, 0, fmt.Errorf("slack returned HTTP %d: %s", response.StatusCode, respBody)
	case response.StatusCode == http.StatusNotFound:
		return retryNotFound, 0, fmt.Errorf("slack returned HTTP 404: %s", respBody)
	case response.StatusCode != http.StatusOK:
		return retryNone, 0, errors.New(string(respBody))
	}

	var envelope apiEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return retryNone, 0, err
	}
	if !envelope.OK {
		details := string(envelope.Errors)
		if len(envelope.ResponseMetadata.Messages) > 0 {
			details += strings.Join(envelope.ResponseMetadata.Messages, "; ")
		}
		apiErr := &APIError{Code: envelope.Error, Details: details}
		if envelope.Error == "ratelimited" {
			return retryThrottle, 0, apiErr
		}
		return retryNone, 0, apiErr
	}
	if resultJson != nil {
		return retryNone, 0, json.Unmarshal(respBody, resultJson)
	}
	return retryNone, 0, nil
}

// send performs an API call, retrying rate limits (respecting Retry-After) and
// 5xx responses with exponential backoff up to maxAttempts, and 404 responses
// exactly once.
func (c *SlackClient) send(ctx context.Context, method, contentType string, body []byte, bearer bool, resultJson interface{}) error {
	backoff := c.retryBackoff
	for attempt := 1; ; attempt++ {
		class, delay, err := c.attempt(ctx, method, contentType, body, bearer, resultJson)
		if err == nil {
			return nil
		}
		if class == retryNone {
			return err
		}
		if class == retryNotFound && attempt > 1 {
			return err
		}
		if attempt >= maxAttempts {
			return fmt.Errorf("giving up after %d attempts: %w", attempt, err)
		}
		if delay == 0 {
			delay = backoff
			backoff *= 2
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// JSONRequest calls Slack API methods that accept JSON bodies with bearer auth.
func (c *SlackClient) JSONRequest(ctx context.Context, method string, body interface{}, resultJson interface{}) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.send(ctx, method, "application/json; charset=utf-8", payload, true, resultJson)
}

type authTestResponse struct {
	UserID string `json:"user_id"`
	TeamID string `json:"team_id"`
}

// authTest resolves and caches the token's auth.test identity. The caller
// must hold c.mu.
func (c *SlackClient) authTest(ctx context.Context) (*authTestResponse, error) {
	if c.auth != nil {
		return c.auth, nil
	}
	var auth authTestResponse
	if err := c.JSONRequest(ctx, "auth.test", struct{}{}, &auth); err != nil {
		return nil, fmt.Errorf("auth.test: %w", err)
	}
	c.auth = &auth
	return c.auth, nil
}

// TeamID returns the workspace to request installation approvals for: the
// provider-configured team_id when set, otherwise the workspace the token
// belongs to (from auth.test), cached for the provider instance's lifetime.
func (c *SlackClient) TeamID(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.teamID != "" {
		return c.teamID, nil
	}
	auth, err := c.authTest(ctx)
	if err != nil {
		return "", err
	}
	c.teamID = auth.TeamID
	return c.teamID, nil
}

type ownersListResponse struct {
	Owners []struct {
		UserEmail      string `json:"user_email"`
		UserID         string `json:"user_id"`
		PermissionType string `json:"permission_type"`
	} `json:"owners"`
}

// ListOwners returns the collaborators of an app via the undocumented
// developer.apps.owners.list endpoint, which accepts both CLI service tokens
// and app configuration tokens.
func (c *SlackClient) ListOwners(ctx context.Context, appID string) (*ownersListResponse, error) {
	var owners ownersListResponse
	if err := c.FormRequest(ctx, "developer.apps.owners.list", url.Values{"app_id": {appID}}, &owners); err != nil {
		return nil, err
	}
	return &owners, nil
}

// TokenUserEmail resolves the email address of the user the provider token
// belongs to, by matching the auth.test user ID against the app's collaborator
// list. The undocumented developer.apps.owners.list endpoint is used because it
// accepts both CLI service tokens and app configuration tokens, unlike the
// users.* endpoints, which both token types lack the scopes for. appID must be
// an app the token user collaborates on. The result is cached for the lifetime
// of the provider instance.
func (c *SlackClient) TokenUserEmail(ctx context.Context, appID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokenUserEmail != "" {
		return c.tokenUserEmail, nil
	}

	auth, err := c.authTest(ctx)
	if err != nil {
		return "", err
	}
	owners, err := c.ListOwners(ctx, appID)
	if err != nil {
		return "", fmt.Errorf("developer.apps.owners.list: %w", err)
	}
	for _, owner := range owners.Owners {
		if owner.UserID == auth.UserID {
			c.tokenUserEmail = owner.UserEmail
			return c.tokenUserEmail, nil
		}
	}
	return "", fmt.Errorf("token user %s is not a collaborator on app %s", auth.UserID, appID)
}

// FormRequest calls Slack API methods that only accept form-encoded bodies,
// such as the undocumented developer.* endpoints used by the Slack CLI.
func (c *SlackClient) FormRequest(ctx context.Context, method string, form url.Values, resultJson interface{}) error {
	form.Set("token", c.token)
	return c.send(ctx, method, "application/x-www-form-urlencoded; charset=utf-8", []byte(form.Encode()), false, resultJson)
}
