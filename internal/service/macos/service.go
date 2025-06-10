//go:build darwin

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/open-uem/openuem-agent-updater/internal/common"
)

func main() {
	us, err := common.NewUpdateService()
	if err != nil {
		log.Fatalf("[FATAL]: could not create task scheduler, reason: %s", err.Error())
	}

	if err := us.ReadConfig(); err != nil {
		log.Fatalf("[FATAL]: %v", err)
	}

	us.StartService()

	// Keep the connection alive
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-done

	us.StopService()

}
