//go:build darwin

package common

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/nats-io/nats.go/jetstream"
	openuem_nats "github.com/open-uem/nats"
	openuem_utils "github.com/open-uem/utils"
)

func NewUpdateService() (*UpdaterService, error) {
	var err error
	us := UpdaterService{}
	us.Logger = NewLogger("openuem-updater.log")

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
	downloadPath := filepath.Join(cwd, "updates", "agent.pkg")
	if err := openuem_utils.DownloadFile(data.DownloadFrom, downloadPath, data.DownloadHash); err != nil {
		log.Printf("[ERROR]: could not download update to directory, reason %v", err)
		msg.NakWithDelay(60 * time.Minute)
		SaveTaskInfoToINI(openuem_nats.UPDATE_ERROR, fmt.Sprintf("could not download update to directory, reason %v\n", err))
		return
	}

	// Stop service
	stopServiceCmd := "launchctl unload -w /Library/LaunchDaemons/openuem-agent.plist"
	cmd := exec.Command("bash", "-c", stopServiceCmd)
	if err := cmd.Run(); err != nil {
		log.Printf("[ERROR]: could not stop the openuem-agent service, reason: %v", err)
	}

	SaveTaskInfoToINI(openuem_nats.UPDATE_SUCCESS, "")
	log.Println("[INFO]: new OpenUEM Agent update command was called", downloadPath)

	if err := msg.Ack(); err != nil {
		log.Printf("[ERROR]: could not ACK message, reason: %v", err)
	}

	installCmd := fmt.Sprintf("installer -pkg %s -target /;launchctl kickstart -k -p system/eu.openuem.openuem-agent;launchctl kickstart -k -p system/eu.openuem.openuem-agent-updater", downloadPath)
	err = exec.Command("bash", "-c", installCmd).Start()
	if err != nil {
		log.Printf("[ERROR]: could not run %s command, reason: %v", installCmd, err)
		return
	}
}

func UninstallAgent() error {
	// Start at daemon
	startAtCmd := "launchctl load -F /System/Library/LaunchDaemons/com.apple.atrun.plist"
	atCmd := exec.Command("bash", "-c", startAtCmd)
	if err := atCmd.Run(); err != nil {
		log.Printf("[ERROR]: could not start the at service, reason: %v", err)
	}

	uninstallPath := "/Library/OpenUEMAgent/uninstall.sh"
	log.Println("[INFO]: will try to uninstall the agent using: ", uninstallPath)

	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("echo \"%s\" | at now +1 minute", "bash /Library/OpenUEMAgent/uninstall.sh"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[ERROR]: could not run %s command, reason: %s", uninstallPath, string(out))
		return err
	}
	return nil
}

func NewLogger(logFilename string) *openuem_utils.OpenUEMLogger {
	var err error

	logger := openuem_utils.OpenUEMLogger{}

	// Get executable path to store logs
	wd := "/var/log/openuem-agent"

	if _, err := os.Stat(wd); os.IsNotExist(err) {
		if err := os.MkdirAll(wd, 0660); err != nil {
			log.Fatalf("[FATAL]: could not create log directory, reason: %v", err)
		}
	}

	logPath := filepath.Join(wd, logFilename)
	logger.LogFile, err = os.Create(logPath)
	if err != nil {
		log.Fatalf("could not create log file,: %v", err)
	}

	logPrefix := strings.TrimSuffix(filepath.Base(logFilename), filepath.Ext(logFilename))
	log.SetOutput(logger.LogFile)
	log.SetPrefix(logPrefix + ": ")
	log.SetFlags(log.Ldate | log.Ltime)

	return &logger
}
