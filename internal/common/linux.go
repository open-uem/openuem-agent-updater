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
	"github.com/zcalusic/sysinfo"
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
	var cmd *exec.Cmd

	os := GetOSVendor()

	// Refresh repositories before install
	RefreshRepositories(os)

	// Start install command
	version := data.Version

	// Update package
	switch os {
	case "debian", "ubuntu", "linuxmint":
		cmd = exec.Command("/bin/sh", "-c", fmt.Sprintf("echo \"%s\" | at now +1 minute", "sudo apt install -y --allow-downgrades openuem-agent="+version))
	case "fedora", "almalinux", "redhat":
		cmd = exec.Command("/bin/sh", "-c", fmt.Sprintf("echo \"%s\" | at now +1 minute", "sudo dnf install --refresh -y openuem-agent="+version))
	}

	if cmd == nil {
		if err := msg.Ack(); err != nil {
			log.Printf("[ERROR]: could not ACK message, reason: %v", err)
		}
		SaveTaskInfoToINI(openuem_nats.UPDATE_ERROR, fmt.Sprintf("[ERROR]: unsupported OS: %s", os))
		return
	}

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
	var cmd *exec.Cmd

	os := GetOSVendor()

	switch os {
	case "debian", "ubuntu", "linuxmint":
		cmd = exec.Command("/bin/sh", "-c", fmt.Sprintf("echo \"%s\" | at now +1 minute", "sudo apt remove -y openuem-agent"))
	case "fedora", "almalinux", "redhat":
		cmd = exec.Command("/bin/sh", "-c", fmt.Sprintf("echo \"%s\" | at now +1 minute", "sudo dnf remove -y openuem-agent"))
	default:
		return fmt.Errorf("unsupported os")
	}

	// Start apt remove command
	err := cmd.Start()
	if err != nil {
		return err
	}

	log.Println("[INFO]: uninstall command has been programmed: ", cmd.String())

	return nil
}

func GetOSVendor() string {
	var si sysinfo.SysInfo

	si.GetSysInfo()

	return si.OS.Vendor
}

func RefreshRepositories(os string) {

	switch os {
	case "debian", "ubuntu", "linuxmint":
		if err := exec.Command("apt", "update").Run(); err != nil {
			log.Printf("[ERROR]: could not run apt update, reason: %v", err)
		}
		// case "fedora", "almalinux", "redhat":
		// 	if err := exec.Command("dnf", "check-update").Run(); err != nil {
		// 		log.Printf("[ERROR]: could not run dnf check-update, reason: %v", err)
		// 	}
	}

}
