package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type langfuseClient struct {
	base, public, secret string
	http                 *http.Client
}

func newLangfuseClient(base, public, secret string) (*langfuseClient, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" ||
		(u.Scheme != "https" && u.Scheme != "http") || public == "" || secret == "" {
		return nil, fmt.Errorf("Langfuse connection settings required")
	}
	return &langfuseClient{
		base: strings.TrimRight(base, "/") + "/api/public", public: public, secret: secret,
		http: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *langfuseClient) request(ctx context.Context, method, path string, payload any, target any) (int, error) {
	var in []byte
	if payload != nil {
		var err error
		in, err = json.Marshal(payload)
		if err != nil {
			return 0, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(in))
	if err != nil {
		return 0, fmt.Errorf("build Langfuse request")
	}
	request.SetBasicAuth(c.public, c.secret)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, fmt.Errorf("Langfuse request failed")
	}
	defer response.Body.Close()
	if target != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(target); err != nil {
			return response.StatusCode, fmt.Errorf("read Langfuse response")
		}
	}
	return response.StatusCode, nil
}

// publish only creates missing items. An existing Langfuse item's editable
// expectedOutput belongs to annotators and is never written by the exporter.
func (c *langfuseClient) publish(ctx context.Context, name string, items []datasetItem) error {
	if len(items) == 0 {
		return fmt.Errorf("empty evaluation dataset")
	}
	const datasets = "/v2/datasets"
	status, err := c.request(ctx, http.MethodGet, datasets+"/"+url.PathEscape(name), nil, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		payload := map[string]string{"name": name, "description": "Nagi QQ 入站决策评估。人工在 Expected Output 填写预期动作、对象和话题。"}
		status, err = c.request(ctx, http.MethodPost, datasets, payload, nil)
	}
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("Langfuse dataset request returned HTTP %d", status)
	}
	created, existing := 0, 0
	for _, item := range items {
		const endpoint = "/dataset-items"
		var previous struct {
			DatasetName string       `json:"datasetName"`
			Metadata    itemMetadata `json:"metadata"`
		}
		status, err := c.request(ctx, http.MethodGet, endpoint+"/"+url.PathEscape(item.ID), nil, &previous)
		if err != nil {
			return err
		}
		switch status {
		case http.StatusOK:
			if previous.DatasetName != name || previous.Metadata.Split != item.Metadata.Split {
				return fmt.Errorf("dataset item exists with a different dataset or split")
			}
			existing++
		case http.StatusNotFound:
			status, err := c.request(ctx, http.MethodPost, endpoint, item, nil)
			if err != nil {
				return err
			}
			if status < 200 || status >= 300 {
				return fmt.Errorf("Langfuse dataset item create returned HTTP %d", status)
			}
			created++
		default:
			return fmt.Errorf("Langfuse dataset item lookup returned HTTP %d", status)
		}
	}
	fmt.Printf("Langfuse items created=%d existing=%d\n", created, existing)
	return nil
}
