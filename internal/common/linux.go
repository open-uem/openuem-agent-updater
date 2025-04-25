//go:build linux

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

	// Start apt install command
	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("echo \"%s\" | at now +1 minute", "sudo apt install -y --allow-downgrades openuem-agent="+data.Version))
	err := cmd.Start()
	if err != nil {
		log.Printf("[ERROR]: could not run %s command, reason: %v", cmd.String(), err)
		msg.NakWithDelay(60 * time.Minute)
		SaveTaskInfoToINI(openuem_nats.UPDATE_ERROR, fmt.Sprintf("[ERROR]: could not run %s command, reason: %v", cmd.String(), err))
		return
	}

	// Confirm that update is going to run
	SaveTaskInfoToINI(openuem_nats.UPDATE_SUCCESS, "")

	if err := msg.Ack(); err != nil {
		log.Printf("[ERROR]: could not ACK message, reason: %v", err)
		return
	}

	if err := msg.Term(); err != nil {
		log.Printf("[ERROR]: could not Terminate message, reason: %v", err)
		return
	}

	log.Println("[INFO]: update command has been programmed: ", cmd.String())

	if err := cmd.Wait(); err != nil {
		log.Printf("[ERROR]: Command finished with error: %v", err)
		return
	}
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

func UninstallAgent() error {
	// Start apt remove command
	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("echo \"%s\" | at now +1 minute", "sudo apt remove -y openuem-agent"))
	err := cmd.Start()
	if err != nil {
		return err
	}

	return nil
}
