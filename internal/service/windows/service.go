//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/open-uem/openuem-agent-updater/internal/common"
	openuem_utils "github.com/open-uem/utils"
	"golang.org/x/sys/windows/svc"
)

func main() {
	us, err := common.NewUpdateService()
	if err != nil {
		log.Fatalf("[FATAL]: could not create task scheduler, reason: %s", err.Error())
	}

	if err := us.ReadConfig(); err != nil {
		log.Fatalf("[FATAL]: %v", err)
	}

	// Delete updates folder
	cwd, err := openuem_utils.GetWd()
	if err != nil {
		log.Fatalf("[FATAL]: %v", err)
	}

	agentUpdatePath := filepath.Join(cwd, "updates", "agent-setup.exe")
	_, err = os.Stat(agentUpdatePath)
	if err == nil {
		if err := os.Remove(agentUpdatePath); err != nil {
			log.Println("[ERROR]: could not remove previous agent update")
		}
	}

	ws := openuem_utils.NewOpenUEMWindowsService()
	ws.ServiceStart = us.StartService
	ws.ServiceStop = us.StopService

	// Run service
	err = svc.Run("openuem-agent-updater", ws)
	if err != nil {
		log.Printf("[ERROR]: could not run service: %v", err)
	}
}
