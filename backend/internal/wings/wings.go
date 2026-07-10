// Package wings talks to the Wings daemon on each node. The panel side of the
// protocol is plain HTTPS with a bearer token ("<token_id>.<token>"), plus
// HS256 JWTs (signed with the node's daemon token) that the browser presents
// directly to wings for websocket and upload access.
package wings

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"roost/internal/auth"
	"roost/internal/store"
)

// Client wraps one node's daemon endpoint.
type Client struct {
	node *store.Node
	http *http.Client
}

func New(node *store.Node) *Client {
	return &Client{
		node: node,
		http: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				// Self-signed daemon certificates are common on private nets.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

// BaseURL is the daemon endpoint the panel talks to.
func (c *Client) BaseURL() string {
	return fmt.Sprintf("%s://%s:%d", c.node.Scheme, c.node.FQDN, c.node.DaemonListen)
}

func (c *Client) token() string {
	return c.node.DaemonTokenID + "." + c.node.DaemonToken
}

// Do performs a JSON request against wings and decodes the response into out
// (out may be nil). Returns an error for any non-2xx response.
func (c *Client) Do(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.BaseURL()+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("wings unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("wings responded with HTTP %d: %s", res.StatusCode, truncate(string(raw), 300))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Raw performs a request and returns the raw response for pass-through
// endpoints (file contents, downloads).
func (c *Client) Raw(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.BaseURL()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token())
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.http.Do(req)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ---- panel → wings operations ----

func (c *Client) SystemInformation() (map[string]any, error) {
	out := map[string]any{}
	err := c.Do("GET", "/api/system", nil, &out)
	return out, err
}

func (c *Client) CreateServer(uuid string, startOnCompletion bool) error {
	return c.Do("POST", "/api/servers", map[string]any{
		"uuid":                uuid,
		"start_on_completion": startOnCompletion,
	}, nil)
}

func (c *Client) DeleteServer(uuid string) error {
	return c.Do("DELETE", "/api/servers/"+uuid, nil, nil)
}

func (c *Client) SendPower(uuid, action string) error {
	return c.Do("POST", "/api/servers/"+uuid+"/power", map[string]string{"action": action}, nil)
}

func (c *Client) SendCommands(uuid string, commands []string) error {
	return c.Do("POST", "/api/servers/"+uuid+"/commands", map[string]any{"commands": commands}, nil)
}

func (c *Client) Reinstall(uuid string) error {
	return c.Do("POST", "/api/servers/"+uuid+"/reinstall", nil, nil)
}

func (c *Client) Sync(uuid string) error {
	return c.Do("POST", "/api/servers/"+uuid+"/sync", nil, nil)
}

func (c *Client) Backup(uuid string, payload any) error {
	return c.Do("POST", "/api/servers/"+uuid+"/backup", payload, nil)
}

func (c *Client) DeleteBackup(uuid, backup string) error {
	return c.Do("DELETE", "/api/servers/"+uuid+"/backup/"+backup, nil, nil)
}

func (c *Client) RestoreBackup(uuid, backup string, payload any) error {
	return c.Do("POST", "/api/servers/"+uuid+"/backup/"+backup+"/restore", payload, nil)
}

// ---- browser-facing JWTs ----

// WebsocketToken creates the JWT the browser presents to wings' websocket.
func WebsocketToken(panelURL string, node *store.Node, server *store.Server, user *store.User, permissions []string) (token, socket string, err error) {
	claims := auth.StandardClaims(panelURL, fmt.Sprintf("%s://%s:%d", node.Scheme, node.FQDN, node.DaemonListen), 10*time.Minute)
	claims["server_uuid"] = server.UUID
	claims["user_id"] = user.ID
	claims["user_uuid"] = user.UUID
	claims["permissions"] = permissions
	token, err = auth.SignJWT(node.DaemonToken, claims)
	wsScheme := "ws"
	if node.Scheme == "https" {
		wsScheme = "wss"
	}
	socket = fmt.Sprintf("%s://%s:%d/api/servers/%s/ws", wsScheme, node.FQDN, node.DaemonListen, server.UUID)
	return token, socket, err
}

// FileDownloadURL signs a one-time JWT URL for downloading a file directly
// from wings.
func FileDownloadURL(panelURL string, node *store.Node, server *store.Server, user *store.User, filePath string) (string, error) {
	claims := auth.StandardClaims(panelURL, fmt.Sprintf("%s://%s:%d", node.Scheme, node.FQDN, node.DaemonListen), 15*time.Minute)
	claims["file_path"] = filePath
	claims["server_uuid"] = server.UUID
	claims["user_uuid"] = user.UUID
	claims["unique_id"] = auth.RandomHex(16)
	token, err := auth.SignJWT(node.DaemonToken, claims)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s:%d/download/file?token=%s", node.Scheme, node.FQDN, node.DaemonListen, url.QueryEscape(token)), nil
}

// BackupDownloadURL signs a JWT URL for downloading a backup from wings.
func BackupDownloadURL(panelURL string, node *store.Node, server *store.Server, user *store.User, backupUUID string) (string, error) {
	claims := auth.StandardClaims(panelURL, fmt.Sprintf("%s://%s:%d", node.Scheme, node.FQDN, node.DaemonListen), 15*time.Minute)
	claims["backup_uuid"] = backupUUID
	claims["server_uuid"] = server.UUID
	claims["user_uuid"] = user.UUID
	claims["unique_id"] = auth.RandomHex(16)
	token, err := auth.SignJWT(node.DaemonToken, claims)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s:%d/download/backup?token=%s", node.Scheme, node.FQDN, node.DaemonListen, url.QueryEscape(token)), nil
}

// UploadURL signs a JWT URL for uploading files directly to wings.
func UploadURL(panelURL string, node *store.Node, server *store.Server, user *store.User) (string, error) {
	claims := auth.StandardClaims(panelURL, fmt.Sprintf("%s://%s:%d", node.Scheme, node.FQDN, node.DaemonListen), 15*time.Minute)
	claims["server_uuid"] = server.UUID
	claims["user_uuid"] = user.UUID
	claims["unique_id"] = auth.RandomHex(16)
	token, err := auth.SignJWT(node.DaemonToken, claims)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s:%d/upload/file?token=%s", node.Scheme, node.FQDN, node.DaemonListen, url.QueryEscape(token)), nil
}
