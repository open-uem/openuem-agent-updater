//go:build windows

package common

import (
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go/jetstream"
	openuem_nats "github.com/open-uem/nats"
	openuem_utils "github.com/open-uem/utils"
	"golang.org/x/sys/windows/svc"
)

func NewUpdateService() (*UpdaterService, error) {
	var err error
	us := UpdaterService{}
	us.Logger = openuem_utils.NewLogger("openuem-agent-updater.txt")

	us.TaskScheduler, err = gocron.NewScheduler()
	if err != nil {
		return nil, err
	}

	return &us, nil
}

func ExecuteUpdate(data openuem_nats.OpenUEMUpdateRequest, msg jetstream.Msg) {
	// Download the file
	cwd, err := openuem_utils.GetWd()
	if err != nil {
		log.Printf("[ERROR]: could not get working directory, reason %v", err)
		msg.NakWithDelay(60 * time.Minute)
		SaveTaskInfoToINI(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not get working directory, reason %v", err))
		return
	}

	// TODO - find a better place to save the agent installer and manage certificate location
	downloadPath := filepath.Join(cwd, "updates", "agent-setup.exe")
	if err := openuem_utils.DownloadFile(data.DownloadFrom, downloadPath, data.DownloadHash); err != nil {
		log.Printf("[ERROR]: could not download update to directory, reason %v", err)
		msg.NakWithDelay(60 * time.Minute)
		SaveTaskInfoToINI(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not download update to directory, reason %v\n", err))
		return
	}

	// Stop service
	if err := openuem_utils.WindowsSvcControl("openuem-agent", svc.Stop, svc.Stopped); err != nil {
		log.Printf("[ERROR]: %v", err)
	}

	SaveTaskInfoToINI(openuem_nats.UPDATE_SUCCESS, "")
	log.Println("[INFO]: new OpenUEM Agent update command was called", downloadPath)

	if err := msg.Ack(); err != nil {
		log.Printf("[ERROR]: could not ACK message, reason: %v", err)
		return
	}

	if err := msg.Term(); err != nil {
		log.Printf("[ERROR]: could not Terminate message, reason: %v", err)
		return
	}

	cmd := exec.Command(downloadPath, "/VERYSILENT")
	err = cmd.Start()
	if err != nil {
		log.Printf("[ERROR]: could not run %s command, reason: %v", downloadPath, err)
		return
	}
}

func UninstallAgent() error {

	uninstallPath := "C:\\Program Files\\OpenUEM Agent\\unins000.exe"
	cmd := exec.Command(uninstallPath, "/VERYSILENT")
	err := cmd.Start()
	if err != nil {
		log.Printf("[ERROR]: could not run %s command, reason: %v", uninstallPath, err)
		return err
	}
	return nil
}
