package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"tuxplay/internal/model"
)

type Client struct {
	httpClient *http.Client
}

func New(socketPath string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

func (c *Client) Status() (model.Status, error) {
	var status model.Status
	err := c.doJSON(http.MethodGet, "/v1/status", nil, &status)
	return status, err
}

func (c *Client) Devices() ([]model.Device, error) {
	var response struct {
		Devices []model.Device `json:"devices"`
	}
	err := c.doJSON(http.MethodGet, "/v1/devices", nil, &response)
	return response.Devices, err
}

func (c *Client) Route(device string, add bool) error {
	return c.doJSON(http.MethodPost, "/v1/route", map[string]any{
		"device": device,
		"add":    add,
	}, nil)
}

func (c *Client) Unroute(device string) error {
	return c.doJSON(http.MethodPost, "/v1/unroute", map[string]any{"device": device}, nil)
}

func (c *Client) Volume(device string, percent int) error {
	return c.doJSON(http.MethodPost, "/v1/volume", map[string]any{
		"device":  device,
		"percent": percent,
	}, nil)
}

func (c *Client) doJSON(method string, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, "http://unix"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var apiErr map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&apiErr); err == nil && apiErr["error"] != "" {
			return fmt.Errorf("%s", apiErr["error"])
		}
		return fmt.Errorf("request failed: %s", resp.Status)
	}

	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
