package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type serverAddress struct {
	URL         string `json:"url"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Token       string `json:"token,omitempty"`
}
type remoteClient struct {
	http    *http.Client
	address serverAddress
}

func newRemoteClient(address serverAddress) (*remoteClient, error) {
	client, base, err := secureRemoteClient(address.URL, address.Fingerprint)
	if err != nil {
		return nil, err
	}
	address.URL = base
	return &remoteClient{client, address}, nil
}
func (client *remoteClient) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, client.address.URL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if client.address.Token != "" {
		req.Header.Set("Authorization", "Bearer "+client.address.Token)
	}
	response, err := client.http.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		var problem struct{ Error string }
		json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&problem)
		if problem.Error == "" {
			problem.Error = response.Status
		}
		return nil, fmt.Errorf("%s", problem.Error)
	}
	return response, nil
}
func (client *remoteClient) json(ctx context.Context, method, path string, body, result any) error {
	response, err := client.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if result == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return err
	}
	return json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(result)
}
