package network

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func PingDevice(address string) (*PingResponse, error) {
	transport := &http.Transport{
		Proxy: nil,
	}

	client := http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}

	response, err := client.Get("http://" + address + "/ping")
	if err != nil {
		return nil, err
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device returned status: %s", response.Status)
	}

	var pingResponse PingResponse
	err = json.NewDecoder(response.Body).Decode(&pingResponse)

	if err != nil {
		return nil, err
	}

	return &pingResponse, nil
}
