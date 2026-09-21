package main

import (
	"FileSyncWails/network"
	"context"
)

// App struct
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	go network.StartServer("8080")
}

func (a *App) PingDevice(address string) (network.PingResponse, error) {
	response, err := network.PingDevice(address)

	if err != nil {
		return network.PingResponse{}, err
	}

	return *response, nil
}
