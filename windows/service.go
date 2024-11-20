//go:build windows

package main

import (
	"log"

	"github.com/doncicuto/openuem-updater-service/service"
	"github.com/doncicuto/openuem_utils"
	"golang.org/x/sys/windows/svc"
)

func main() {
	us, err := service.NewUpdateService()
	if err != nil {
		log.Fatalf("[FATAL]: could not create task scheduler, reason: %s", err.Error())
	}

	if err := us.ReadWindowsConfig(); err != nil {
		log.Fatalf("[FATAL]: %v", err)
	}

	ws := openuem_utils.NewOpenUEMWindowsService()
	ws.ServiceStart = us.StartWindowsService
	ws.ServiceStop = us.StopWindowsService

	// Run service
	err = svc.Run("openuem-updater-service", ws)
	if err != nil {
		log.Printf("[ERROR]: could not run service: %v", err)
	}
}
